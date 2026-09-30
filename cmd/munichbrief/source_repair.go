package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/egekocabas/munichbrief/internal/config"
	"github.com/egekocabas/munichbrief/internal/parser"
	"github.com/egekocabas/munichbrief/internal/store"
)

// runSourceRepair requires an explicit retained source file and document ID;
// it never broadens routine RSS discovery or fetches arbitrary URLs.
func runSourceRepair(ctx context.Context, cfg config.Config, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("source-repair", flag.ContinueOnError)
	document := flags.Int64("document-id", 0, "Existing source document ID")
	htmlPath := flags.String("html", "", "Local official source HTML")
	apply := flags.Bool("apply", false, "Apply the previewed source replacement")
	expected := flags.String("expected-source-hash", "", "Required current source hash when applying")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *document <= 0 || *htmlPath == "" || flags.NArg() != 0 {
		return errors.New("source-repair requires --document-id and --html")
	}
	f, err := os.Open(*htmlPath)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return errors.New("source HTML exceeds limit")
	}
	parsed, err := parser.ParsePoliceRelease(data)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	changes, err := db.PreviewDocumentRepair(ctx, *document, parsed.Incidents)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(changes); err != nil {
		return err
	}
	if !*apply {
		return nil
	}
	if *expected == "" {
		return errors.New("applying source repair requires --expected-source-hash from the retained document")
	}
	return db.ApplyDocumentRepair(ctx, *document, *expected, parsed, time.Now())
}
