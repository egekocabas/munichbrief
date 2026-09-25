package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

var migratedTestDatabase []byte
var fixtureTestDatabase []byte

func TestMain(m *testing.M) {
	contents, err := createTestDatabase(false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare migrated test database: %v\n", err)
		os.Exit(1)
	}
	migratedTestDatabase = contents
	contents, err = createTestDatabase(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare fixture test database: %v\n", err)
		os.Exit(1)
	}
	fixtureTestDatabase = contents
	os.Exit(m.Run())
}

func createTestDatabase(withFixtures bool) ([]byte, error) {
	directory, err := os.MkdirTemp("", "munichbrief-web-tests-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)

	path := filepath.Join(directory, "migrated.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	if withFixtures {
		documents, err := source.NewFixtureProvider().Load(context.Background())
		if err == nil {
			err = database.UpsertDocuments(context.Background(), documents, time.Now())
		}
		if err != nil {
			database.Close()
			return nil, err
		}
	}
	if err := database.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Each parallel web test owns its database file and connection; only the
// immutable migrated bytes are shared.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	return openTestSnapshot(t, migratedTestDatabase)
}

func openTestSnapshot(t *testing.T, snapshot []byte) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "munichbrief.db")
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		t.Fatalf("copy migrated test database: %v", err)
	}
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestFixtureStoresAreIsolated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	first := fixtureStore(t)
	_, count, err := first.ListIncidents(ctx, 1, 0)
	if err != nil || count == 0 {
		t.Fatalf("fixture snapshot has no incidents: %d/%v", count, err)
	}
	if err := first.SetAutomaticProcessing(ctx, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	second := fixtureStore(t)
	control, err := second.AIControl(ctx)
	if err != nil || !control.AutomaticProcessingEnabled {
		t.Fatalf("fixture stores share mutable state: %#v/%v", control, err)
	}
	_, emptyCount, err := openTestStore(t).ListIncidents(ctx, 1, 0)
	if err != nil || emptyCount != 0 {
		t.Fatalf("empty store inherited fixture rows: %d/%v", emptyCount, err)
	}
}
