package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/ssivitskii/dbcompat/internal/compat"
	"github.com/ssivitskii/dbcompat/internal/model"
)

const label = "io.github.ssivitskii.dbcompat.run"

type CommandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandOutput(stdout.Bytes(), stderr.Bytes(), err)
}

func commandOutput(stdout, _ []byte, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("docker command failed: %w", err)
	}
	return strings.TrimSpace(string(stdout)), nil
}

type Instance struct {
	ID, DSN, ImageID string
	runner           CommandRunner
}

func Start(ctx context.Context, runner CommandRunner, image string, startup time.Duration) (*Instance, error) {
	password, err := secret()
	if err != nil {
		return nil, err
	}
	runID, err := secret()
	if err != nil {
		return nil, err
	}
	args := dockerRunArgs(image, password, runID)
	runCtx, cancel := context.WithTimeout(ctx, startup)
	defer cancel()
	idOutput, err := runner.Run(runCtx, "docker", args...)
	if err != nil {
		return nil, err
	}
	id, err := parseContainerID(idOutput)
	if err != nil {
		return nil, err
	}
	inst := &Instance{ID: id, runner: runner}
	portOutput, err := runner.Run(runCtx, "docker", "port", id, "5432/tcp")
	if err != nil {
		_ = inst.Close(ctx)
		return nil, err
	}
	port, err := parseLoopbackPort(portOutput)
	if err != nil {
		_ = inst.Close(ctx)
		return nil, err
	}
	inst.DSN = fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/dbcompat?sslmode=disable", password, port)
	if imageID, e := runner.Run(runCtx, "docker", "image", "inspect", "--format", "{{.Id}}", image); e == nil {
		inst.ImageID = imageID
	}
	deadline := time.Now().Add(startup)
	for time.Now().Before(deadline) {
		pingCtx, c := context.WithTimeout(ctx, time.Second)
		conn, e := pgx.Connect(pingCtx, inst.DSN)
		if e == nil {
			e = conn.Ping(pingCtx)
			closeCtx, closeCancel := cleanupContext()
			conn.Close(closeCtx)
			closeCancel()
		}
		c()
		if e == nil {
			return inst, nil
		}
		select {
		case <-ctx.Done():
			inst.Close(ctx)
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	inst.Close(ctx)
	return nil, errors.New("postgres startup timeout")
}

func (i *Instance) ApplySchema(ctx context.Context, sql []byte) error {
	conn, err := pgx.Connect(ctx, i.DSN)
	if err != nil {
		return err
	}
	defer func() { closeCtx, cancel := cleanupContext(); defer cancel(); conn.Close(closeCtx) }()
	_, err = conn.Exec(ctx, string(sql))
	return err
}
func (i *Instance) Close(ctx context.Context) error {
	if i == nil || i.ID == "" {
		return nil
	}
	cleanupCtx, cancel := cleanupContext()
	defer cancel()
	_, err := i.runner.Run(cleanupCtx, "docker", "rm", "-f", i.ID)
	return err
}

func (i *Instance) Execute(ctx context.Context, q model.Query) error {
	conn, err := pgx.Connect(ctx, i.DSN)
	if err != nil {
		return classify("connect", err)
	}
	defer func() { closeCtx, cancel := cleanupContext(); defer cancel(); conn.Close(closeCtx) }()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return classify("begin", err)
	}
	defer func() { rollbackCtx, cancel := cleanupContext(); defer cancel(); _ = tx.Rollback(rollbackCtx) }()
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		milliseconds := remaining.Milliseconds()
		if milliseconds > 25 {
			milliseconds -= 25
		}
		if milliseconds < 1 {
			milliseconds = 1
		}
		if _, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = '%dms'", milliseconds)); err != nil {
			return classify("begin", err)
		}
	}
	const name = "dbcompat_query"
	oids, args, err := typedParams(q.Args)
	if err != nil {
		return &compat.QueryError{Phase: "prepare", Category: "invalid typed argument", Cause: err}
	}
	description, err := tx.Conn().PgConn().Prepare(ctx, name, q.SQL, oids)
	if err != nil {
		return classify("prepare", err)
	}
	var builder pgx.ExtendedQueryBuilder
	if err = builder.Build(tx.Conn().TypeMap(), description, args); err != nil {
		return &compat.QueryError{Phase: "execute", Category: "argument encoding failed", Cause: err}
	}
	_, err = tx.Conn().PgConn().ExecPrepared(ctx, name, builder.ParamValues, builder.ParamFormats, builder.ResultFormats).Close()
	if err != nil {
		return classify("execute", err)
	}
	return nil
}

func classify(phase string, err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return &compat.QueryError{SQLState: pe.Code, Phase: phase, Category: "database rejected query"}
	}
	category := phase + " failed"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		category = "query timeout"
	}
	return &compat.QueryError{Phase: phase, Category: category, Cause: err}
}

func typedParams(arguments []model.Argument) ([]uint32, []any, error) {
	oids := make([]uint32, len(arguments))
	values := make([]any, len(arguments))
	for i, argument := range arguments {
		typeName := argument.Type
		if typeName == "null" {
			typeName = argument.As
		}
		switch typeName {
		case "text":
			oids[i] = pgtype.TextOID
		case "int8":
			oids[i] = pgtype.Int8OID
		case "float8":
			oids[i] = pgtype.Float8OID
		case "bool":
			oids[i] = pgtype.BoolOID
		case "uuid":
			oids[i] = pgtype.UUIDOID
		case "timestamptz":
			oids[i] = pgtype.TimestamptzOID
		case "json":
			oids[i] = pgtype.JSONOID
		default:
			return nil, nil, fmt.Errorf("unsupported argument type %q", typeName)
		}
		values[i] = argument.Value
	}
	return oids, values, nil
}

func dockerRunArgs(image, password, runID string) []string {
	return []string{"run", "-d", "--rm", "--label", label + "=" + runID, "--memory", "512m", "--cpus", "1", "--pids-limit", "256", "-e", "POSTGRES_PASSWORD=" + password, "-e", "POSTGRES_DB=dbcompat", "-p", "127.0.0.1::5432", image}
}

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

func parseContainerID(value string) (string, error) {
	id := strings.TrimSpace(value)
	if !containerIDPattern.MatchString(id) {
		return "", errors.New("docker run returned an invalid container ID")
	}
	return id, nil
}

func parseLoopbackPort(value string) (int, error) {
	address := strings.TrimSpace(value)
	if strings.Contains(address, "\n") || !strings.HasPrefix(address, "127.0.0.1:") {
		return 0, errors.New("docker returned an invalid PostgreSQL port binding")
	}
	port, err := strconv.Atoi(strings.TrimPrefix(address, "127.0.0.1:"))
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("docker returned an invalid PostgreSQL port binding")
	}
	return port, nil
}
func secret() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
