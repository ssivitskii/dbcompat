package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/ssivitskii/dbcompat/internal/model"
)

type pair struct {
	Previous string `json:"previous"`
	Next     string `json:"next"`
}

type timeoutJSON struct {
	Startup string `json:"startup"`
	Query   string `json:"query"`
}

type rawConfig struct {
	Version       int         `json:"version"`
	PostgresImage string      `json:"postgresImage"`
	Schemas       pair        `json:"schemas"`
	QuerySets     pair        `json:"querySets"`
	RequiredCells []string    `json:"requiredCells"`
	Timeouts      timeoutJSON `json:"timeouts"`
}

type Config struct {
	Path, Root, Hash, PostgresImage                          string
	PreviousSchema, NextSchema, PreviousQueries, NextQueries string
	Required                                                 map[model.Cell]bool
	Timeouts                                                 model.Timeouts
}

func Load(path string) (Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Config{}, err
	}
	var raw rawConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config: trailing JSON value")
	}
	if raw.Version != 1 {
		return Config{}, fmt.Errorf("config: unsupported version %d", raw.Version)
	}
	if err := validateImageReference(raw.PostgresImage); err != nil {
		return Config{}, err
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return Config{}, err
	}
	resolve := func(name, value string) (string, error) {
		if value == "" {
			return "", fmt.Errorf("config: %s is required", name)
		}
		p := value
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		p, err = filepath.EvalSymlinks(p)
		if err != nil {
			return "", fmt.Errorf("config: %s: %w", name, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("config: %s escapes config root", name)
		}
		return p, nil
	}
	ps, err := resolve("schemas.previous", raw.Schemas.Previous)
	if err != nil {
		return Config{}, err
	}
	ns, err := resolve("schemas.next", raw.Schemas.Next)
	if err != nil {
		return Config{}, err
	}
	pq, err := resolve("querySets.previous", raw.QuerySets.Previous)
	if err != nil {
		return Config{}, err
	}
	nq, err := resolve("querySets.next", raw.QuerySets.Next)
	if err != nil {
		return Config{}, err
	}
	required := map[model.Cell]bool{}
	if raw.RequiredCells == nil {
		for _, c := range model.OrderedCells {
			required[c] = true
		}
	} else {
		for _, s := range raw.RequiredCells {
			c, ok := model.ParseCell(s)
			if !ok {
				return Config{}, fmt.Errorf("config: unknown required cell %q", s)
			}
			if required[c] {
				return Config{}, fmt.Errorf("config: duplicate required cell %q", s)
			}
			required[c] = true
		}
	}
	// Matching-version baselines define whether a query contract is valid at all.
	// They are mandatory even when only selected cross-version cells are required.
	required[model.PreviousPrevious] = true
	required[model.NextNext] = true
	startup, err := parseDuration("startup", raw.Timeouts.Startup, 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	query, err := parseDuration("query", raw.Timeouts.Query, 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	sum := sha256.Sum256(data)
	return Config{Path: abs, Root: root, Hash: hex.EncodeToString(sum[:]), PostgresImage: raw.PostgresImage, PreviousSchema: ps, NextSchema: ns, PreviousQueries: pq, NextQueries: nq, Required: required, Timeouts: model.Timeouts{Startup: startup, Query: query}}, nil
}

func validateImageReference(value string) error {
	if value == "" {
		return errors.New("config: postgresImage is required")
	}
	if len(value) > 512 || strings.ContainsAny(value, "`|") {
		return errors.New("config: postgresImage contains unsupported characters")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("config: postgresImage contains unsupported characters")
		}
	}
	return nil
}

// ResolveOutput returns an output directory contained by root. Any existing
// symlink path component is rejected so report writes cannot escape after
// validation.
func ResolveOutput(root, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("output path is required")
	}
	p := value
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("output path escapes config root")
	}
	current := root
	if rel != "." {
		for _, component := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			info, statErr := os.Lstat(current)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return "", fmt.Errorf("output path: %w", statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("output path contains symlink component %q", component)
			}
		}
	}
	return p, nil
}

func parseDuration(name, value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("config: invalid %s timeout %q", name, value)
	}
	return d, nil
}
