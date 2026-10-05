package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssivitskii/dbcompat/internal/compat"
	"github.com/ssivitskii/dbcompat/internal/model"
)

func TestDeterministicReportsAndEscaping(t *testing.T) {
	r := New(time.Unix(0, 0), Image{Reference: "postgres:17.6"}, map[string]string{"config": "abc"}, []model.Result{{Cell: model.NextPrevious, QueryID: "z<&", Required: true, Status: model.Failed, Message: "bad | <&", SQLState: "42703"}, {Cell: model.PreviousPrevious, QueryID: "a", Status: model.Passed}}, 1)
	d1 := t.TempDir()
	d2 := t.TempDir()
	if err := WriteAll(d1, r); err != nil {
		t.Fatal(err)
	}
	if err := WriteAll(d2, r); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"report.json", "report.xml", "report.md"} {
		a, _ := os.ReadFile(filepath.Join(d1, n))
		b, _ := os.ReadFile(filepath.Join(d2, n))
		if string(a) != string(b) {
			t.Fatalf("%s nondeterministic", n)
		}
	}
	x, _ := os.ReadFile(filepath.Join(d1, "report.xml"))
	if !strings.Contains(string(x), "z&lt;&amp;") {
		t.Fatalf("not escaped: %s", x)
	}
	j, _ := os.ReadFile(filepath.Join(d1, "report.json"))
	var decoded Report
	if err := json.Unmarshal(j, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Failed != 1 {
		t.Fatal(decoded.Summary)
	}
}

func TestReportsArePrivateAndContainNoServerSecret(t *testing.T) {
	const secret = "TOP-SECRET-SERVER-TEXT"
	serverError := &compat.QueryError{SQLState: "42703", Phase: "prepare", Category: "database rejected query", Cause: errors.New(secret)}
	r := New(time.Unix(0, 0), Image{Reference: "postgres:17.6"}, map[string]string{}, []model.Result{{Cell: model.PreviousPrevious, QueryID: "q", Required: true, Status: model.Failed, Phase: "prepare", Classification: "baseline_failure", SQLState: "42703", Message: serverError.Error()}}, 2)
	d := t.TempDir()
	if err := WriteAll(d, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.json", "report.xml", "report.md"} {
		path := filepath.Join(d, name)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret in %s", name)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%o", name, info.Mode().Perm())
		}
	}
}
