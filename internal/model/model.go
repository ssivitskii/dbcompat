package model

import "time"

type Cell string

const (
	PreviousPrevious Cell = "previous_previous"
	PreviousNext     Cell = "previous_next"
	NextPrevious     Cell = "next_previous"
	NextNext         Cell = "next_next"
)

var OrderedCells = []Cell{PreviousPrevious, PreviousNext, NextPrevious, NextNext}

func (c Cell) Baseline() bool { return c == PreviousPrevious || c == NextNext }

func ParseCell(s string) (Cell, bool) {
	for _, c := range OrderedCells {
		if string(c) == s {
			return c, true
		}
	}
	return "", false
}

type Status string

const (
	Passed     Status = "passed"
	Failed     Status = "failed"
	Timeout    Status = "timeout"
	SetupError Status = "setup_error"
	InputError Status = "input_error"
	Skipped    Status = "skipped"
)

type Argument struct {
	Type  string
	As    string
	Value any
}

type Query struct {
	ID   string
	SQL  string
	Args []Argument
}

type Result struct {
	Cell           Cell   `json:"cell"`
	QueryID        string `json:"queryId"`
	Required       bool   `json:"required"`
	Status         Status `json:"status"`
	Phase          string `json:"phase"`
	Classification string `json:"classification"`
	SQLState       string `json:"sqlState,omitempty"`
	Message        string `json:"message,omitempty"`
	DurationMS     int64  `json:"durationMs"`
}

type Timeouts struct {
	Startup time.Duration
	Query   time.Duration
}
