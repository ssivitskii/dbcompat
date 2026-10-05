package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ssivitskii/dbcompat/internal/config"
	"github.com/ssivitskii/dbcompat/internal/model"
	"github.com/ssivitskii/dbcompat/internal/report"
)

func TestWriteSetupReport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reports")
	cfg := config.Config{PostgresImage: "postgres:17.6", Required: map[model.Cell]bool{}}
	if err := writeSetupReport(dir, cfg, map[string]string{"config": "hash"}, "schema", ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got report.Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 3 || got.Summary.SetupError != 4 {
		t.Fatalf("%+v", got)
	}
}
