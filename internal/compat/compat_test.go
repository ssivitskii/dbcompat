package compat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ssivitskii/dbcompat/internal/model"
)

type fake struct{ err map[string]error }

func (f fake) Execute(_ context.Context, q model.Query) error { return f.err[q.ID] }
func TestMatrixAndExitReduction(t *testing.T) {
	q1 := []model.Query{{ID: "old"}}
	q2 := []model.Query{{ID: "new"}}
	results := Run(context.Background(), Inputs{Previous: q1, Next: q2, PreviousDB: fake{map[string]error{"new": &QueryError{SQLState: "42703", Phase: "prepare", Category: "database rejected query"}}}, NextDB: fake{map[string]error{}}, Required: map[model.Cell]bool{model.NextPrevious: true}, Timeout: time.Second})
	if len(results) != 4 {
		t.Fatalf("got %d results", len(results))
	}
	if ExitCode(results) != 1 {
		t.Fatalf("exit=%d", ExitCode(results))
	}
	if results[2].Cell != model.NextPrevious || results[2].SQLState != "42703" {
		t.Fatalf("unexpected: %+v", results[2])
	}
	if results[2].Phase != "prepare" || results[2].Classification != "incompatibility" {
		t.Fatalf("classification: %+v", results[2])
	}
}
func TestBaselineFailureIsInputError(t *testing.T) {
	r := []model.Result{{Cell: model.PreviousPrevious, Required: false, Status: model.Failed}}
	if ExitCode(r) != 2 {
		t.Fatal(ExitCode(r))
	}
}
func TestOptionalCrossFailureDoesNotFail(t *testing.T) {
	r := []model.Result{{Cell: model.PreviousNext, Required: false, Status: model.Failed}}
	if ExitCode(r) != 0 {
		t.Fatal(ExitCode(r))
	}
}
func TestFailedBaselineLeavesCrossUnclassified(t *testing.T) {
	q := []model.Query{{ID: "q"}}
	bad := fake{map[string]error{"q": errors.New("bad")}}
	results := Run(context.Background(), Inputs{Previous: q, Next: nil, PreviousDB: bad, NextDB: bad, Required: map[model.Cell]bool{model.PreviousNext: true}, Timeout: time.Second})
	if results[0].Classification != "baseline_failure" || results[1].Classification != "unclassified" {
		t.Fatalf("%+v", results)
	}
}
func TestOptionalFailureClassification(t *testing.T) {
	q := []model.Query{{ID: "q"}}
	results := Run(context.Background(), Inputs{Previous: q, PreviousDB: fake{map[string]error{}}, NextDB: fake{map[string]error{"q": errors.New("bad")}}, Required: map[model.Cell]bool{}, Timeout: time.Second})
	if results[1].Classification != "optional_incompatibility" {
		t.Fatalf("%+v", results[1])
	}
}
func TestQueryErrorCauseNeverEntersResult(t *testing.T) {
	const secret = "TOP-SECRET-SERVER-TEXT"
	q := []model.Query{{ID: "q"}}
	queryErr := &QueryError{SQLState: "22000", Phase: "execute", Category: "database rejected query", Cause: errors.New(secret)}
	results := Run(context.Background(), Inputs{Previous: q, PreviousDB: fake{map[string]error{"q": queryErr}}, NextDB: fake{map[string]error{"q": queryErr}}, Timeout: time.Second})
	for _, result := range results {
		if strings.Contains(result.Message, secret) {
			t.Fatalf("secret leaked: %+v", result)
		}
	}
}
func TestSetupResultsAreSafeAndBaselinesRequired(t *testing.T) {
	results := SetupResults("schema", map[model.Cell]bool{})
	if len(results) != 4 {
		t.Fatal(len(results))
	}
	for _, result := range results {
		if result.Status != model.SetupError || result.Classification != "unclassified" || result.Message != "database environment unavailable" {
			t.Fatalf("%+v", result)
		}
	}
	if !results[0].Required || !results[3].Required || results[1].Required || results[2].Required {
		t.Fatalf("required flags: %+v", results)
	}
}
func TestSanitize(t *testing.T) {
	s := Sanitize("boom postgres://user:secret@host/db " + strings.Repeat("x", 500))
	if strings.Contains(s, "secret") || len(s) > 324 {
		t.Fatalf("not sanitized: %q", s)
	}
}
