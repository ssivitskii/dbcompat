package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ssivitskii/dbcompat/internal/compat"
	"github.com/ssivitskii/dbcompat/internal/config"
	"github.com/ssivitskii/dbcompat/internal/postgres"
	"github.com/ssivitskii/dbcompat/internal/report"
)

func main() { os.Exit(run()) }
func run() int {
	fs := flag.NewFlagSet("dbcompat", flag.ContinueOnError)
	cfgPath := fs.String("config", "dbcompat.json", "configuration file")
	out := fs.String("output", "reports", "report directory")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	prevQ, prevQHash, err := config.LoadManifest(cfg.PreviousQueries)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	nextQ, nextQHash, err := config.LoadManifest(cfg.NextQueries)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	prevSchema, prevSchemaHash, err := config.ValidateSchema(cfg.PreviousSchema)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	nextSchema, nextSchemaHash, err := config.ValidateSchema(cfg.NextSchema)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	absOut, err := config.ResolveOutput(cfg.Root, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	inputs := map[string]string{"config": cfg.Hash, "schemas.previous": prevSchemaHash, "schemas.next": nextSchemaHash, "querySets.previous": prevQHash, "querySets.next": nextQHash}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := postgres.OSRunner{}
	prevDB, err := postgres.Start(ctx, runner, cfg.PostgresImage, cfg.Timeouts.Startup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "previous database startup failed")
		if err := writeSetupReport(absOut, cfg, inputs, "startup", ""); err != nil {
			fmt.Fprintln(os.Stderr, "write reports:", err)
			return 4
		}
		return 3
	}
	defer prevDB.Close(ctx)
	nextDB, err := postgres.Start(ctx, runner, cfg.PostgresImage, cfg.Timeouts.Startup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "next database startup failed")
		if err := writeSetupReport(absOut, cfg, inputs, "startup", prevDB.ImageID); err != nil {
			fmt.Fprintln(os.Stderr, "write reports:", err)
			return 4
		}
		return 3
	}
	defer nextDB.Close(ctx)
	setupCtx, cancel := context.WithTimeout(ctx, cfg.Timeouts.Startup)
	defer cancel()
	if err := prevDB.ApplySchema(setupCtx, prevSchema); err != nil {
		fmt.Fprintln(os.Stderr, "previous schema setup failed")
		if err := writeSetupReport(absOut, cfg, inputs, "schema", prevDB.ImageID); err != nil {
			fmt.Fprintln(os.Stderr, "write reports:", err)
			return 4
		}
		return 3
	}
	if err := nextDB.ApplySchema(setupCtx, nextSchema); err != nil {
		fmt.Fprintln(os.Stderr, "next schema setup failed")
		if err := writeSetupReport(absOut, cfg, inputs, "schema", prevDB.ImageID); err != nil {
			fmt.Fprintln(os.Stderr, "write reports:", err)
			return 4
		}
		return 3
	}
	results := compat.Run(ctx, compat.Inputs{Previous: prevQ, Next: nextQ, PreviousDB: prevDB, NextDB: nextDB, Required: cfg.Required, Timeout: cfg.Timeouts.Query})
	code := compat.ExitCode(results)
	r := report.New(time.Now(), report.Image{Reference: cfg.PostgresImage, ResolvedID: prevDB.ImageID}, inputs, results, code)
	if err := report.WriteAll(absOut, r); err != nil {
		fmt.Fprintln(os.Stderr, "write reports:", err)
		return 4
	}
	fmt.Printf("dbcompat: %d/%d passed; reports: %s\n", r.Summary.Passed, r.Summary.Total, absOut)
	return code
}

func writeSetupReport(output string, cfg config.Config, inputs map[string]string, phase, imageID string) error {
	r := report.New(time.Now(), report.Image{Reference: cfg.PostgresImage, ResolvedID: imageID}, inputs, compat.SetupResults(phase, cfg.Required), 3)
	return report.WriteAll(output, r)
}
