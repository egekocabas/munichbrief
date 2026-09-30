package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRSSControlUpgradeAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rss-control.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { database.Close() }()
	now := time.Now()
	id, err := database.RecordSyncAttempt(ctx, now, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the prior schema while retaining an existing RSS history entry.
	if _, err := database.db.ExecContext(ctx, `DROP TABLE rss_runtime_control; DELETE FROM schema_migrations WHERE version=28`); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		t.Helper()
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		database, err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	if enabled, err := database.RSSSyncEnabled(ctx); err != nil || !enabled {
		t.Fatalf("upgraded RSS control = %t, %v", enabled, err)
	}
	if err := database.SetRSSSyncEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	reopen()
	if enabled, err := database.RSSSyncEnabled(ctx); err != nil || enabled {
		t.Fatalf("persisted RSS control = %t, %v", enabled, err)
	}
	history, err := database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil || len(history.Entries) != 1 || history.Entries[0].ID != id {
		t.Fatalf("preserved RSS history = %+v, %v", history, err)
	}
	if err := database.SetRSSSyncEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	reopen()
	if enabled, err := database.RSSSyncEnabled(ctx); err != nil || !enabled {
		t.Fatalf("re-enabled RSS control = %t, %v", enabled, err)
	}
}
