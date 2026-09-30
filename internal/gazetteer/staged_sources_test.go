package gazetteer

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedRefreshRetainsValidatedDownloadsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gazetteer.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sources := []SourceDefinition{}
	for _, key := range []string{"first", "second"} {
		sources = append(sources, SourceDefinition{Key: key, DisplayName: key, URL: "https://example.test/" + key, License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 1024, Parse: func(data []byte) ([]Entry, error) {
			entries, err := parseMunichStreets(data)
			for i := range entries {
				entries[i].Sources[0].Key = key
			}
			return entries, err
		}})
	}
	calls := map[string]int{}
	failSecond := true
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls[r.URL.Path]++
		if r.URL.Path == "/second" && failSecond {
			return &http.Response{StatusCode: 504, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("busy"))}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"features":[{"id":"1","properties":{"strassenname":"Example Straße","sb_name":"Example District"}}]}`))}, nil
	})}
	manager := func(db *Store) *Manager {
		f, e := NewFetcher(client, "test", db)
		if e != nil {
			t.Fatal(e)
		}
		m, e := NewManager(ctx, db, f, sources, time.Hour, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	if _, err := manager(db).Refresh(ctx); err == nil {
		t.Fatal("expected upstream failure")
	}
	status, err := db.Status(ctx)
	if err != nil || status.ActiveGeneration != 0 {
		t.Fatalf("partial refresh activated: %#v %v", status, err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failSecond = false
	if _, err := manager(db).Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if calls["/first"] != 1 || calls["/second"] != 2 {
		t.Fatalf("downloads repeated: %v", calls)
	}
	entries, err := db.ActiveSourceEntries(ctx, "first")
	if err != nil || len(entries) != 1 || entries[0].Sources[0].DistrictHint != "Example District" {
		t.Fatalf("hint lost: %#v %v", entries, err)
	}
	var staged int
	if err := db.db.QueryRow(`SELECT count(*) FROM gazetteer_staged_sources`).Scan(&staged); err != nil || staged != 0 {
		t.Fatalf("successful refresh retained staging: %d %v", staged, err)
	}
	if _, err := manager(db).Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if calls["/first"] != 2 {
		t.Fatal("new refresh reused already published cache")
	}
}

func TestStagedSourceFreshnessAndDefinitionInvalidation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	d := SourceDefinition{Key: "streets", URL: "https://example.test/streets", MinimumRows: 1, MaximumRows: 2, MaximumSize: 1024}
	s := SourceSnapshot{Definition: d, ContentHash: "hash", FetchedAt: now, Entries: []Entry{{Name: "Example Straße"}}}
	if err := db.stageSource(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.stagedSource(ctx, d, now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("valid cache rejected: %v %v", ok, err)
	}
	for _, at := range []time.Time{now.Add(-time.Second), now.Add(stagedSourceLifetime)} {
		if _, ok, err := db.stagedSource(ctx, d, at); err != nil || ok {
			t.Fatalf("invalid age accepted: %v %v", ok, err)
		}
	}
	for _, change := range []func(*SourceDefinition){func(d *SourceDefinition) { d.URL += "?changed" }, func(d *SourceDefinition) { d.MinimumRows = 2 }, func(d *SourceDefinition) { d.License = "changed" }, func(d *SourceDefinition) { d.MaximumSize++ }} {
		changed := d
		change(&changed)
		if _, ok, err := db.stagedSource(ctx, changed, now); err != nil || ok {
			t.Fatalf("changed definition accepted: %v %v", ok, err)
		}
	}
	if _, err := db.db.Exec(`UPDATE gazetteer_staged_sources SET definition_hash='old-parser-contract'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.stagedSource(ctx, d, now); err != nil || ok {
		t.Fatalf("old contract accepted: %v %v", ok, err)
	}
}
