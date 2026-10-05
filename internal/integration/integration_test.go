//go:build integration

package integration

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ssivitskii/dbcompat/internal/compat"
	"github.com/ssivitskii/dbcompat/internal/config"
	"github.com/ssivitskii/dbcompat/internal/model"
	"github.com/ssivitskii/dbcompat/internal/postgres"
)

const integrationImage = "postgres@sha256:00bc86618629af00d2937fdc5a5d63db3ff8450acf52f0636ec813c7f4902929"

func TestCompatibilityExamples(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker unavailable")
	}
	root := filepath.Join("..", "..", "examples")
	tests := []struct {
		name        string
		want        int
		state       string
		failingCell model.Cell
	}{{"additive", 0, "", ""}, {"premature-drop", 1, "42703", model.PreviousNext}, {"code-first", 1, "42703", model.NextPrevious}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(filepath.Join(root, tc.name, "dbcompat.json"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.PostgresImage = integrationImage
			pq, _, err := config.LoadManifest(cfg.PreviousQueries)
			if err != nil {
				t.Fatal(err)
			}
			nq, _, err := config.LoadManifest(cfg.NextQueries)
			if err != nil {
				t.Fatal(err)
			}
			ps, _, err := config.ValidateSchema(cfg.PreviousSchema)
			if err != nil {
				t.Fatal(err)
			}
			ns, _, err := config.ValidateSchema(cfg.NextSchema)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			a, err := postgres.Start(ctx, postgres.OSRunner{}, cfg.PostgresImage, cfg.Timeouts.Startup)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(context.Background())
			b, err := postgres.Start(ctx, postgres.OSRunner{}, cfg.PostgresImage, cfg.Timeouts.Startup)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close(context.Background())
			if err = a.ApplySchema(ctx, ps); err != nil {
				t.Fatal(err)
			}
			if err = b.ApplySchema(ctx, ns); err != nil {
				t.Fatal(err)
			}
			r := compat.Run(ctx, compat.Inputs{Previous: pq, Next: nq, PreviousDB: a, NextDB: b, Required: map[model.Cell]bool{model.PreviousPrevious: true, model.PreviousNext: true, model.NextPrevious: true, model.NextNext: true}, Timeout: cfg.Timeouts.Query})
			if got := compat.ExitCode(r); got != tc.want {
				t.Fatalf("exit=%d results=%+v", got, r)
			}
			if tc.state != "" {
				found := false
				for _, x := range r {
					if x.SQLState == tc.state && x.Cell == tc.failingCell {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing SQLSTATE %s: %+v", tc.state, r)
				}
			}
		})
	}
}

func TestTypedNullTimeoutAndBrokenSchema(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := postgres.Start(ctx, postgres.OSRunner{}, integrationImage, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	if err := db.Execute(ctx, model.Query{ID: "typed-null", SQL: "SELECT $1 IS NULL", Args: []model.Argument{{Type: "null", As: "text", Value: nil}}}); err != nil {
		t.Fatalf("typed null: %v", err)
	}
	queryCtx, queryCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer queryCancel()
	err = db.Execute(queryCtx, model.Query{ID: "timeout", SQL: "SELECT pg_sleep(2)"})
	var queryErr *compat.QueryError
	if !errors.As(err, &queryErr) {
		t.Fatalf("timeout type: %v", err)
	}
	if queryErr.SQLState != "57014" || queryErr.Phase != "execute" {
		t.Fatalf("timeout classification: %+v", queryErr)
	}
	if err := db.ApplySchema(ctx, []byte("CREATE TABLE broken (")); err == nil {
		t.Fatal("expected broken schema error")
	}
}

func TestOptionalCrossFailureIsVisibleButNonBlocking(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker unavailable")
	}
	root := filepath.Join("..", "..", "examples", "code-first")
	cfg, err := config.Load(filepath.Join(root, "dbcompat.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.PostgresImage = integrationImage
	pq, _, err := config.LoadManifest(cfg.PreviousQueries)
	if err != nil {
		t.Fatal(err)
	}
	nq, _, err := config.LoadManifest(cfg.NextQueries)
	if err != nil {
		t.Fatal(err)
	}
	ps, _, err := config.ValidateSchema(cfg.PreviousSchema)
	if err != nil {
		t.Fatal(err)
	}
	ns, _, err := config.ValidateSchema(cfg.NextSchema)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a, err := postgres.Start(ctx, postgres.OSRunner{}, cfg.PostgresImage, cfg.Timeouts.Startup)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	b, err := postgres.Start(ctx, postgres.OSRunner{}, cfg.PostgresImage, cfg.Timeouts.Startup)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if err = a.ApplySchema(ctx, ps); err != nil {
		t.Fatal(err)
	}
	if err = b.ApplySchema(ctx, ns); err != nil {
		t.Fatal(err)
	}
	required := map[model.Cell]bool{model.PreviousPrevious: true, model.NextNext: true}
	results := compat.Run(ctx, compat.Inputs{Previous: pq, Next: nq, PreviousDB: a, NextDB: b, Required: required, Timeout: cfg.Timeouts.Query})
	if code := compat.ExitCode(results); code != 0 {
		t.Fatalf("exit=%d results=%+v", code, results)
	}
	found := false
	for _, result := range results {
		if result.Cell == model.NextPrevious && result.Status == model.Failed && result.Classification == "optional_incompatibility" {
			found = true
		}
	}
	if !found {
		t.Fatalf("optional failure missing: %+v", results)
	}
}
