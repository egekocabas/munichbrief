package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
	directory, err := os.MkdirTemp("", "munichbrief-store-tests-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)

	path := filepath.Join(directory, "migrated.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	if err := database.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// openTestStore starts from an empty migrated snapshot. Use Open directly
// when testing migration, initial defaults, backups, or reopening persisted data.
func openTestStore(ctx context.Context, path string) (*Store, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create test database: %w", err)
	}
	if _, err := file.Write(migratedTestDatabase); err != nil {
		file.Close()
		return nil, fmt.Errorf("copy migrated test database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return Open(ctx, path)
}

func TestMigratedTestStoresAreIsolated(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "first.db")
	first, err := openTestStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	control, err := first.AIControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetAutomaticProcessing(ctx, false, control.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	second, err := openTestStore(ctx, filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	control, err = second.AIControl(ctx)
	if err != nil || !control.AutomaticProcessingEnabled {
		t.Fatalf("second database inherited first database mutations: %#v/%v", control, err)
	}
	if existing, err := openTestStore(ctx, path); err == nil {
		existing.Close()
		t.Fatal("snapshot overwrote an existing database")
	}
	control, err = first.AIControl(ctx)
	if err != nil || control.AutomaticProcessingEnabled {
		t.Fatalf("first database was reset: %#v/%v", control, err)
	}
}
