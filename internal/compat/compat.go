package compat

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/ssivitskii/dbcompat/internal/model"
)

type QueryError struct {
	SQLState string
	Phase    string
	Category string
	Cause    error
}

func (e *QueryError) Error() string { return e.Category }
func (e *QueryError) Unwrap() error { return e.Cause }

type Executor interface {
	Execute(context.Context, model.Query) error
}

type Inputs struct {
	Previous, Next     []model.Query
	PreviousDB, NextDB Executor
	Required           map[model.Cell]bool
	Timeout            time.Duration
}

func Run(ctx context.Context, in Inputs) []model.Result {
	type work struct {
		cell    model.Cell
		queries []model.Query
		db      Executor
	}
	worklist := []work{{model.PreviousPrevious, in.Previous, in.PreviousDB}, {model.PreviousNext, in.Previous, in.NextDB}, {model.NextPrevious, in.Next, in.PreviousDB}, {model.NextNext, in.Next, in.NextDB}}
	var results []model.Result
	for _, w := range worklist {
		for _, q := range w.queries {
			start := time.Now()
			qctx, cancel := context.WithTimeout(ctx, in.Timeout)
			err := w.db.Execute(qctx, q)
			cancel()
			r := model.Result{Cell: w.cell, QueryID: q.ID, Required: in.Required[w.cell] || w.cell.Baseline(), Status: model.Passed, Phase: "execute", DurationMS: time.Since(start).Milliseconds()}
			if err != nil {
				r.Status = model.Failed
				r.Message = Sanitize(err.Error())
				var qe *QueryError
				if errors.As(err, &qe) {
					r.SQLState = qe.SQLState
					r.Phase = qe.Phase
				}
				if errors.Is(err, context.DeadlineExceeded) || r.SQLState == "57014" {
					r.Status = model.Timeout
					r.Message = "query timeout"
				}
			}
			results = append(results, r)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Cell != results[j].Cell {
			return cellIndex(results[i].Cell) < cellIndex(results[j].Cell)
		}
		return results[i].QueryID < results[j].QueryID
	})
	classifyResults(results)
	return results
}

func classifyResults(results []model.Result) {
	baselines := make(map[string]bool)
	for _, r := range results {
		if r.Cell.Baseline() {
			baselines[string(r.Cell)+"\x00"+r.QueryID] = r.Status == model.Passed
		}
	}
	for i := range results {
		r := &results[i]
		if r.Cell.Baseline() {
			if r.Status == model.Passed {
				r.Classification = "baseline_pass"
			} else {
				r.Classification = "baseline_failure"
			}
			continue
		}
		baseline := model.PreviousPrevious
		if r.Cell == model.NextPrevious {
			baseline = model.NextNext
		}
		if !baselines[string(baseline)+"\x00"+r.QueryID] {
			r.Classification = "unclassified"
		} else if r.Status == model.Passed {
			r.Classification = "compatible"
		} else if r.Required {
			r.Classification = "incompatibility"
		} else {
			r.Classification = "optional_incompatibility"
		}
	}
}

func SetupResults(phase string, required map[model.Cell]bool) []model.Result {
	results := make([]model.Result, 0, len(model.OrderedCells))
	for _, cell := range model.OrderedCells {
		results = append(results, model.Result{Cell: cell, QueryID: "environment", Required: required[cell] || cell.Baseline(), Status: model.SetupError, Phase: phase, Classification: "unclassified", Message: "database environment unavailable"})
	}
	return results
}

func cellIndex(c model.Cell) int {
	for i, x := range model.OrderedCells {
		if x == c {
			return i
		}
	}
	return 99
}

func ExitCode(results []model.Result) int {
	for _, r := range results {
		if r.Cell.Baseline() && r.Status != model.Passed && r.Status != model.Skipped {
			return 2
		}
	}
	for _, r := range results {
		if r.Required && !r.Cell.Baseline() && r.Status != model.Passed && r.Status != model.Skipped {
			return 1
		}
	}
	return 0
}

func Sanitize(s string) string {
	const max = 320
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if i := indexFold(s, prefix); i >= 0 {
			s = s[:i] + "[redacted-dsn]"
		}
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFoldASCII(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 32
		}
		if y >= 'A' && y <= 'Z' {
			y += 32
		}
		if x != y {
			return false
		}
	}
	return true
}
