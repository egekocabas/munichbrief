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
	"github.com/egekocabas/munichbrief/internal/store"
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

func TestLocationEvaluationRejectsUnavailableSelectionWithoutRequests(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot.db")
	db, err := store.Open(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, ids := range []string{"999999", ""} {
		output := filepath.Join(dir, "results.jsonl")
		args := []string{"--snapshot", snapshot, "--model", "unused", "--output", output}
		if ids != "" {
			args = append(args, "--ids", ids)
		}
		err := runLocationEvaluation(context.Background(), config.Config{DatabasePath: filepath.Join(dir, "live.db")}, args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "no eligible") {
			t.Errorf("selection %q: %v", ids, err)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("created output for empty selection")
		}
	}
}

func TestLocationEvaluationRequiresEveryRequestedIncident(t *testing.T) {
	inputs := []store.LocationEvaluationInput{{IncidentID: 1}, {IncidentID: 2}}
	if _, err := selectLocationEvaluationInputs(inputs, map[int64]bool{1: true, 3: true}); err == nil {
		t.Fatal("silently accepted partial cohort")
	}
	selected, err := selectLocationEvaluationInputs(inputs, map[int64]bool{2: true})
	if err != nil || len(selected) != 1 || selected[0].IncidentID != 2 {
		t.Fatalf("selection %#v / %v", selected, err)
	}
}
