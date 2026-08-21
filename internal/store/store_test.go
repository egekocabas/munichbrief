package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/source"
)

func TestFixtureIngestionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "munichbrief.db")
	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })

	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	observedAt := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	for range 2 {
		if err := database.UpsertDocuments(ctx, documents, observedAt); err != nil {
			t.Fatalf("UpsertDocuments() error = %v", err)
		}
	}

	records, total, err := database.ListIncidents(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListIncidents() error = %v", err)
	}
	if total != 3 || len(records) != 3 {
		t.Fatalf("incident totals = %d/%d, want 3/3", total, len(records))
	}
	if records[0].Number != "1244" || records[1].Number != "1245" {
		t.Errorf("daily incident order = %q, %q; want 1244, 1245", records[0].Number, records[1].Number)
	}

	record, err := database.GetIncident(ctx, records[0].ID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if record.TitleDE == "" || record.SourceTitle == "" {
		t.Error("incident detail is missing title or source metadata")
	}
}

func TestGetIncidentNotFound(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if _, err := database.GetIncident(ctx, 999); err != ErrNotFound {
		t.Fatalf("GetIncident() error = %v, want ErrNotFound", err)
	}
}

func TestBackupCreatesRestorableDatabaseWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	database := fixtureStoreForBackup(t, ctx)
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	if err := database.Backup(ctx, backupPath); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup permissions = %o, want 600", info.Mode().Perm())
	}
	restored, err := Open(ctx, backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	t.Cleanup(func() { restored.Close() })
	_, total, err := restored.ListIncidents(ctx, 20, 0)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if total != 3 {
		t.Fatalf("backup incident total = %d, want 3", total)
	}
	if err := database.Backup(ctx, backupPath); err == nil {
		t.Fatal("second Backup() error = nil, want overwrite refusal")
	}
}

func fixtureStoreForBackup(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatalf("UpsertDocuments() error = %v", err)
	}
	return database
}
