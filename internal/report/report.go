package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ssivitskii/dbcompat/internal/model"
)

type Image struct {
	Reference  string `json:"reference"`
	ResolvedID string `json:"resolvedId,omitempty"`
}
type Summary struct {
	Total      int `json:"total"`
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Timeout    int `json:"timeout"`
	SetupError int `json:"setupError"`
	InputError int `json:"inputError"`
	Skipped    int `json:"skipped"`
}
type Report struct {
	Version     int               `json:"version"`
	GeneratedAt string            `json:"generatedAt"`
	Image       Image             `json:"image"`
	Inputs      map[string]string `json:"inputs"`
	Results     []model.Result    `json:"results"`
	Summary     Summary           `json:"summary"`
	ExitCode    int               `json:"exitCode"`
}

func New(at time.Time, image Image, inputs map[string]string, results []model.Result, exit int) Report {
	r := Report{Version: 1, GeneratedAt: at.UTC().Format(time.RFC3339), Image: image, Inputs: inputs, Results: append([]model.Result(nil), results...), ExitCode: exit}
	sort.SliceStable(r.Results, func(i, j int) bool {
		if r.Results[i].Cell != r.Results[j].Cell {
			return index(r.Results[i].Cell) < index(r.Results[j].Cell)
		}
		return r.Results[i].QueryID < r.Results[j].QueryID
	})
	r.Summary.Total = len(r.Results)
	for _, x := range r.Results {
		switch x.Status {
		case model.Passed:
			r.Summary.Passed++
		case model.Failed:
			r.Summary.Failed++
		case model.Timeout:
			r.Summary.Timeout++
		case model.SetupError:
			r.Summary.SetupError++
		case model.InputError:
			r.Summary.InputError++
		case model.Skipped:
			r.Summary.Skipped++
		}
	}
	return r
}
func index(c model.Cell) int {
	for i, x := range model.OrderedCells {
		if x == c {
			return i
		}
	}
	return 99
}

func WriteAll(dir string, r Report) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	j, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	j = append(j, '\n')
	x, err := junit(r)
	if err != nil {
		return err
	}
	m := markdown(r)
	for _, f := range []struct {
		name string
		data []byte
	}{{"report.json", j}, {"report.xml", x}, {"report.md", m}} {
		if err := writeAtomic(filepath.Join(dir, f.name), f.data); err != nil {
			return err
		}
	}
	return nil
}
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".dbcompat-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(0o600)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

type testsuite struct {
	XMLName                          xml.Name   `xml:"testsuite"`
	Name                             string     `xml:"name,attr"`
	Tests, Failures, Errors, Skipped int        `xml:",attr"`
	Cases                            []testcase `xml:"testcase"`
}
type testcase struct {
	Name    string    `xml:"name,attr"`
	Class   string    `xml:"classname,attr"`
	Time    string    `xml:"time,attr"`
	Failure *failure  `xml:"failure,omitempty"`
	Skipped *struct{} `xml:"skipped,omitempty"`
}
type failure struct {
	Message, Type string `xml:",attr"`
	Text          string `xml:",chardata"`
}

func junit(r Report) ([]byte, error) {
	s := testsuite{Name: "dbcompat", Tests: len(r.Results)}
	for _, x := range r.Results {
		c := testcase{Name: x.QueryID, Class: string(x.Cell), Time: fmt.Sprintf("%.3f", float64(x.DurationMS)/1000)}
		switch x.Status {
		case model.Passed:
		case model.Skipped:
			c.Skipped = &struct{}{}
			s.Skipped++
		case model.Failed, model.Timeout:
			c.Failure = &failure{Message: x.Message, Type: string(x.Status), Text: x.SQLState}
			s.Failures++
		default:
			c.Failure = &failure{Message: x.Message, Type: string(x.Status)}
			s.Errors++
		}
		s.Cases = append(s.Cases, c)
	}
	b, err := xml.MarshalIndent(s, "", "  ")
	return append([]byte(xml.Header), append(b, '\n')...), err
}
func markdown(r Report) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# dbcompat report\n\n- Image: `%s`", r.Image.Reference)
	if r.Image.ResolvedID != "" {
		fmt.Fprintf(&b, " (`%s`)", r.Image.ResolvedID)
	}
	fmt.Fprintf(&b, "\n- Exit code: `%d`\n- Passed: %d/%d\n\n| Cell | Query | Required | Status | Classification | Phase | SQLSTATE | Message |\n|---|---|---:|---|---|---|---|---|\n", r.ExitCode, r.Summary.Passed, r.Summary.Total)
	for _, x := range r.Results {
		fmt.Fprintf(&b, "| %s | %s | %t | %s | %s | %s | %s | %s |\n", x.Cell, x.QueryID, x.Required, x.Status, x.Classification, x.Phase, x.SQLState, md(x.Message))
	}
	return b.Bytes()
}
func md(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ") }
