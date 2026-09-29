package main

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egekocabas/munichbrief/internal/config"
)

func TestLocationEvaluationRejectsSQLiteURIAliasBeforeOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live database.db")
	// Deliberately not SQLite: isolation must be checked before migrations/open.
	if err := os.WriteFile(path, []byte("do not open"), 0600); err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=rwc"}).String()
	output := filepath.Join(t.TempDir(), "results.jsonl")
	err := runLocationEvaluation(context.Background(), config.Config{DatabasePath: uri}, []string{"--snapshot", path, "--model", "unused", "--output", output}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "aliases configured live database") {
		t.Fatalf("isolation check: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("evaluation created output before isolation check")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "do not open" {
		t.Fatal("changed live database")
	}
}
