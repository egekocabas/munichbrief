package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egekocabas/munichbrief/internal/store"
)

var migratedTestDatabase []byte

func TestMain(m *testing.M) {
	contents, err := createMigratedTestDatabase()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare migrated test database: %v\n", err)
		os.Exit(1)
	}
	migratedTestDatabase = contents
	os.Exit(m.Run())
}

func createMigratedTestDatabase() ([]byte, error) {
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
	if err := database.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "munichbrief.db")
	if err := os.WriteFile(path, migratedTestDatabase, 0o600); err != nil {
		t.Fatalf("copy migrated test database: %v", err)
	}
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}
