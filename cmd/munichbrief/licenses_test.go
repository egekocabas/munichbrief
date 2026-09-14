package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLicensesCommandIgnoresInvalidOperationalConfiguration(t *testing.T) {
	t.Setenv("MUNICHBRIEF_PAGE_SIZE", "invalid")
	db := filepath.Join(t.TempDir(), "must-not-open.db")
	t.Setenv("MUNICHBRIEF_DATABASE_PATH", db)
	f, err := os.CreateTemp(t.TempDir(), "notices")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = old; f.Close() })
	if err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), []string{"licenses"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "MunichBrief") || !strings.Contains(string(b), "SIL OPEN FONT LICENSE") {
		t.Fatal("missing original notices")
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("licences command touched database")
	}
	if err := run(context.Background(), slog.Default(), []string{"licenses", "unexpected"}); err == nil {
		t.Fatal("unexpected argument accepted")
	}
}
