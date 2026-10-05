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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ssivitskii/dbcompat/internal/model"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type rawManifest struct {
	Version int        `json:"version"`
	Queries []rawQuery `json:"queries"`
}
type rawQuery struct {
	ID   string   `json:"id"`
	SQL  string   `json:"sql"`
	Args []rawArg `json:"args"`
}
type rawArg struct {
	Type  string          `json:"type"`
	As    string          `json:"as,omitempty"`
	Value json.RawMessage `json:"value"`
}

func LoadManifest(path string) ([]model.Query, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var raw rawManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, "", fmt.Errorf("manifest: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("manifest: trailing JSON value")
	}
	if raw.Version != 1 {
		return nil, "", fmt.Errorf("manifest: unsupported version %d", raw.Version)
	}
	if len(raw.Queries) == 0 {
		return nil, "", errors.New("manifest: at least one query is required")
	}
	seen := map[string]bool{}
	out := make([]model.Query, 0, len(raw.Queries))
	for _, q := range raw.Queries {
		if !idPattern.MatchString(q.ID) {
			return nil, "", fmt.Errorf("manifest: invalid query id %q", q.ID)
		}
		if seen[q.ID] {
			return nil, "", fmt.Errorf("manifest: duplicate query id %q", q.ID)
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.SQL) == "" {
			return nil, "", fmt.Errorf("manifest: query %q has empty sql", q.ID)
		}
		args := make([]model.Argument, 0, len(q.Args))
		for i, a := range q.Args {
			v, err := parseArg(a)
			if err != nil {
				return nil, "", fmt.Errorf("manifest: query %q arg %d: %w", q.ID, i, err)
			}
			args = append(args, model.Argument{Type: a.Type, As: a.As, Value: v})
		}
		out = append(out, model.Query{ID: q.ID, SQL: q.SQL, Args: args})
	}
	sum := sha256.Sum256(data)
	return out, hex.EncodeToString(sum[:]), nil
}

func parseArg(a rawArg) (any, error) {
	allowed := map[string]bool{"text": true, "int8": true, "float8": true, "bool": true, "uuid": true, "timestamptz": true, "json": true, "null": true}
	if !allowed[a.Type] {
		return nil, fmt.Errorf("unknown type %q", a.Type)
	}
	if a.Type == "null" {
		if a.As == "" || !allowed[a.As] || a.As == "null" {
			return nil, errors.New("null requires a non-null 'as' type")
		}
		if len(a.Value) == 0 || !bytes.Equal(bytes.TrimSpace(a.Value), []byte("null")) {
			return nil, errors.New("null requires an explicit null value")
		}
		return nil, nil
	}
	if a.As != "" {
		return nil, errors.New("'as' is only valid for null")
	}
	if len(a.Value) == 0 {
		return nil, errors.New("value is required")
	}
	if bytes.Equal(bytes.TrimSpace(a.Value), []byte("null")) {
		return nil, errors.New("JSON null is only valid with type null and an explicit 'as' type")
	}
	var v any
	switch a.Type {
	case "text", "uuid", "timestamptz":
		var s string
		if err := json.Unmarshal(a.Value, &s); err != nil {
			return nil, errors.New("value must be a JSON string")
		}
		if a.Type == "uuid" && !regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`).MatchString(s) {
			return nil, errors.New("invalid uuid")
		}
		if a.Type == "timestamptz" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return nil, errors.New("invalid RFC3339 timestamp")
			}
			v = t
		} else {
			v = s
		}
	case "int8":
		var n json.Number
		dec := json.NewDecoder(bytes.NewReader(a.Value))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil {
			return nil, errors.New("value must be an integer")
		}
		if strings.ContainsAny(n.String(), ".eE") {
			return nil, errors.New("value must be an integer")
		}
		x, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			return nil, errors.New("value must fit int64")
		}
		v = x
	case "float8":
		var n json.Number
		dec := json.NewDecoder(bytes.NewReader(a.Value))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil {
			return nil, errors.New("value must be a number")
		}
		x, err := strconv.ParseFloat(n.String(), 64)
		if err != nil {
			return nil, errors.New("invalid float8")
		}
		v = x
	case "bool":
		if err := json.Unmarshal(a.Value, &v); err != nil {
			return nil, errors.New("value must be boolean")
		}
		if _, ok := v.(bool); !ok {
			return nil, errors.New("value must be boolean")
		}
	case "json":
		if a.Value[0] >= '0' && a.Value[0] <= '9' || a.Value[0] == '-' {
			return nil, errors.New("implicit JSON numbers are rejected; wrap the value in an object, array, string, boolean, or null")
		}
		if !json.Valid(a.Value) {
			return nil, errors.New("invalid json")
		}
		v = json.RawMessage(append([]byte(nil), a.Value...))
	}
	return v, nil
}

func ValidateSchema(path string) ([]byte, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "\\") {
			return nil, "", errors.New("schema: psql backslash directives are not supported")
		}
	}
	s := sha256.Sum256(b)
	return b, hex.EncodeToString(s[:]), nil
}
