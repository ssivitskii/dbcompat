package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ssivitskii/dbcompat/internal/model"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}
func TestLoadStrictAndDefaults(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a.sql", "b.sql", "a.json", "b.json"} {
		write(t, filepath.Join(d, f), "{}")
	}
	p := filepath.Join(d, "dbcompat.json")
	write(t, p, `{"version":1,"postgresImage":"postgres:17.6","schemas":{"previous":"a.sql","next":"b.sql"},"querySets":{"previous":"a.json","next":"b.json"},"timeouts":{}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Required) != 4 || !c.Required[model.NextPrevious] {
		t.Fatal(c.Required)
	}
}
func TestBaselinesAlwaysRequired(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a.sql", "b.sql", "a.json", "b.json"} {
		write(t, filepath.Join(d, f), "{}")
	}
	p := filepath.Join(d, "dbcompat.json")
	write(t, p, `{"version":1,"postgresImage":"postgres:17.6","schemas":{"previous":"a.sql","next":"b.sql"},"querySets":{"previous":"a.json","next":"b.json"},"requiredCells":[],"timeouts":{}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Required[model.PreviousPrevious] || !c.Required[model.NextNext] || c.Required[model.PreviousNext] {
		t.Fatal(c.Required)
	}
}
func TestRejectUnknownDuplicateAndEscape(t *testing.T) {
	for _, tc := range []string{
		`{"version":1,"postgresImage":"x","schemas":{"previous":"../x","next":"../x"},"querySets":{"previous":"../x","next":"../x"},"timeouts":{},"oops":1}`,
		`{"version":1,"postgresImage":"x","schemas":{"previous":"../x","next":"../x"},"querySets":{"previous":"../x","next":"../x"},"requiredCells":["previous_next","previous_next"],"timeouts":{}}`,
	} {
		d := t.TempDir()
		p := filepath.Join(d, "c.json")
		write(t, p, tc)
		if _, err := Load(p); err == nil {
			t.Fatal("expected error")
		}
	}
	d := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x")
	write(t, outside, "x")
	if err := os.Symlink(outside, filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "c.json")
	write(t, p, `{"version":1,"postgresImage":"x","schemas":{"previous":"link","next":"link"},"querySets":{"previous":"link","next":"link"},"timeouts":{}}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("got %v", err)
	}
}

func TestResolveOutputRejectsEscapeAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if _, err := ResolveOutput(root, "../reports"); err == nil {
		t.Fatal("expected escape error")
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveOutput(root, "linked/reports"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("got %v", err)
	}
	got, err := ResolveOutput(root, "new/reports")
	if err != nil || got != filepath.Join(root, "new/reports") {
		t.Fatalf("%q %v", got, err)
	}
}
func TestManifestTypesAndSchemaDirective(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "q.json")
	write(t, p, `{"version":1,"queries":[{"id":"q","sql":"select $1","args":[{"type":"int8","value":4},{"type":"null","as":"text","value":null}]}]}`)
	q, _, err := LoadManifest(p)
	if err != nil || q[0].Args[0].Value != int64(4) {
		t.Fatalf("%+v %v", q, err)
	}
	write(t, p, `{"version":1,"queries":[{"id":"q","sql":"x","args":[{"type":"json","value":42}]}]}`)
	if _, _, err := LoadManifest(p); err == nil {
		t.Fatal("expected implicit number error")
	}
	write(t, p, "\\set x y\nselect 1")
	if _, _, err := ValidateSchema(p); err == nil {
		t.Fatal("expected directive error")
	}
}

func TestManifestRejectsEmptyAndImplicitNulls(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "q.json")
	for _, input := range []string{`{"version":1,"queries":[]}`, `{"version":1,"queries":[{"id":"q","sql":"select $1","args":[{"type":"text","value":null}]}]}`, `{"version":1,"queries":[{"id":"q","sql":"select $1","args":[{"type":"null","as":"text"}]}]}`} {
		write(t, p, input)
		if _, _, err := LoadManifest(p); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	write(t, p, `{"version":1,"queries":[{"id":"q","sql":"select $1","args":[{"type":"null","as":"text","value":null}]}]}`)
	if _, _, err := LoadManifest(p); err != nil {
		t.Fatalf("explicit null rejected: %v", err)
	}
}

func TestRejectUnsafeImageReference(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a.sql", "b.sql", "a.json", "b.json"} {
		write(t, filepath.Join(d, f), "{}")
	}
	for _, image := range []string{"postgres:17.6 bad", "postgres:17.6|oops", "`postgres`", "postgres:\n17"} {
		p := filepath.Join(d, "c.json")
		write(t, p, `{"version":1,"postgresImage":`+strconv.Quote(image)+`,"schemas":{"previous":"a.sql","next":"b.sql"},"querySets":{"previous":"a.json","next":"b.json"},"timeouts":{}}`)
		if _, err := Load(p); err == nil {
			t.Errorf("accepted %q", image)
		}
	}
}
