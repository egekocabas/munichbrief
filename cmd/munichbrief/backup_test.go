package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/egekocabas/munichbrief/internal/config"
	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

func TestGazetteerBackupUsesOwnSchemaAndPreservesLiveData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "gazetteer.db")
	db, err := gazetteer.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO gazetteer_overrides(normalized_value,action,reason) VALUES ('Test place','context','synthetic backup test')`); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DatabasePath: filepath.Join(dir, "must-not-be-opened.db"), GazetteerDatabasePath: source}
	for _, mode := range []string{"file", "stream"} {
		t.Run(mode, func(t *testing.T) {
			target := filepath.Join(dir, mode+".db")
			output := target
			if mode == "stream" {
				output = "-"
			}
			var stream bytes.Buffer
			if err := runBackup(ctx, cfg, []string{"--database", "gazetteer", "--output", output}, &stream); err != nil {
				t.Fatal(err)
			}
			if mode == "stream" {
				if err := os.WriteFile(target, stream.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			copy, err := sql.Open("sqlite", target)
			if err != nil {
				t.Fatal(err)
			}
			defer copy.Close()
			var reason, integrity string
			if err := copy.QueryRow(`SELECT reason FROM gazetteer_overrides WHERE normalized_value='Test place'`).Scan(&reason); err != nil || reason != "synthetic backup test" {
				t.Fatalf("reason=%q err=%v", reason, err)
			}
			if err := copy.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity=%q err=%v", integrity, err)
			}
			var count int
			if err := copy.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='documents'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("main schema in gazetteer: count=%d err=%v", count, err)
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("backup permissions: %v %v", info, err)
			}
			if err := runBackup(ctx, cfg, []string{"--database", "gazetteer", "--output", target}, io.Discard); err == nil {
				t.Fatal("overwrote existing backup")
			}
		})
	}
	if _, err := os.Stat(cfg.DatabasePath); !os.IsNotExist(err) {
		t.Fatalf("opened main database: %v", err)
	}
	var count int
	if err := raw.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='documents'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("modified source schema: %d %v", count, err)
	}
}

func TestBackupRejectsMissingGazetteerAndInvalidSelection(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DatabasePath: filepath.Join(dir, "main.db"), GazetteerDatabasePath: filepath.Join(dir, "missing.db")}
	for _, args := range [][]string{
		{"--database", "gazetteer", "--output", "-"},
		{"--database", "invalid", "--output", "-"},
		{"--output", "-", "unexpected"},
	} {
		var output bytes.Buffer
		if err := runBackup(context.Background(), cfg, args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("args=%v err=%v bytes=%d", args, err, output.Len())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed backup created files: %v %v", entries, err)
	}
}
