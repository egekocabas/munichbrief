package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/parser"
)

func TestRSSDetailsClassifyAndPreserveSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := openTestStore(ctx, filepath.Join(t.TempDir(), "rss.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	start, end := now.Add(-24*time.Hour), now.Add(24*time.Hour)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	doc := domain.SourceDocument{ExternalID: "synthetic", SourceURL: "https://fixture.invalid/rss-detail", Title: "Synthetic release", PublishedAt: now, FeedFingerprint: "v1"}
	outside := doc
	outside.SourceURL += "-old"
	outside.PublishedAt = start.Add(-time.Hour)
	release := func(body string) parser.ParsedRelease {
		p, e := parser.ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><h2>1. Synthetic one</h2><p>` + body + `</p><h2>2. Synthetic two</h2><p>Second body</p></section>`))
		must(e)
		return p
	}
	original := release("First body")
	var firstCheck, firstDetail int64
	var previousSnapshot int64
	for n := 0; n < 4; n++ {
		check, e := db.RecordSyncAttempt(ctx, now.Add(time.Duration(n)*time.Hour), start, end)
		must(e)
		must(db.ObserveRSSDocuments(ctx, check, []domain.SourceDocument{doc, doc, outside}, now, start, end))
		docs, e := db.ListDocumentsForFetch(ctx, start, end, now, true)
		must(e)
		if len(docs) != 1 {
			t.Fatalf("fetch docs=%d", len(docs))
		}
		must(db.QueueRSSFetches(ctx, check, docs, true))
		parsed := original
		if n == 2 {
			parsed = release("Changed body")
		}
		if n == 3 {
			parsed = original
			parsed.Incidents = parsed.Incidents[:1]
			parsed.SourceHash = "shorter"
		}
		must(db.StoreRSSFetch(ctx, check, docs[0].ID, parsed, now))
		detail, e := db.RSSCheckDetails(ctx, check, 1, 0)
		must(e)
		if !detail.HasOlder || len(detail.Documents) != 1 {
			t.Fatalf("detail pagination=%+v", detail)
		}
		d := detail.Documents[0]
		if d.Existed != (n > 0) || d.FetchStatus != "stored" {
			t.Fatalf("classification=%+v", d)
		}
		page, e := db.ListRSSSyncHistory(ctx, 1, nil, nil)
		must(e)
		h := page.Entries[0]
		if h.NewDocuments+h.ExistingDocuments != 1 || !h.DetailsRecorded {
			t.Fatalf("counts=%+v", h)
		}
		switch n {
		case 0:
			if h.Inserted != 2 || h.NewDocuments != 1 {
				t.Fatalf("new counts=%+v", h)
			}
			firstCheck = check
			firstDetail = d.ID
		case 1:
			if h.Unchanged != 2 || h.ExistingDocuments != 1 || d.SnapshotID != previousSnapshot {
				t.Fatalf("unchanged=%+v / %+v", h, d)
			}
		case 2:
			if h.Updated != 1 || h.Unchanged != 1 {
				t.Fatalf("updated=%+v", h)
			}
		case 3:
			if h.Removed != 1 || h.Updated != 1 {
				t.Fatalf("removed=%+v", h)
			}
		}
		previousSnapshot = d.SnapshotID
		more, e := db.RSSCheckDetails(ctx, check, 1, d.ID)
		must(e)
		if len(more.Documents) != 1 || more.Documents[0].FetchStatus != "outside_window" || more.HasOlder {
			t.Fatalf("outside=%+v", more)
		}
		must(db.RecordSyncSuccess(ctx, check, "", "", now, SyncRunResult{Fetched: 1}))
	}
	old, e := db.RSSDocumentSnapshot(ctx, firstCheck, firstDetail)
	must(e)
	if len(old.Incidents) != 2 || old.Incidents[0].BodyDE != "First body" || old.Outcomes[0].Status != "inserted" {
		t.Fatalf("old snapshot changed=%+v", old)
	}
	if _, e := db.RSSDocumentSnapshot(ctx, firstCheck+1, firstDetail); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross-check read=%v", e)
	}
}

func TestRSSDetailsFailuresRollbackAndInterruption(t *testing.T) {
	ctx := context.Background()
	db, err := openTestStore(ctx, filepath.Join(t.TempDir(), "rss.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	var docs []domain.SourceDocument
	for n := 0; n < 4; n++ {
		docs = append(docs, domain.SourceDocument{SourceURL: fmt.Sprintf("https://fixture.invalid/%d", n), ExternalID: fmt.Sprint(n), Title: "Synthetic", PublishedAt: now, FeedFingerprint: "v1"})
	}
	check, e := db.RecordSyncAttempt(ctx, now, start, end)
	must(e)
	must(db.ObserveRSSDocuments(ctx, check, docs, now, start, end))
	selected, e := db.ListDocumentsForFetch(ctx, start, end, now, true)
	must(e)
	must(db.QueueRSSFetches(ctx, check, selected, false))
	must(db.FailRSSFetch(ctx, check, selected[0].ID, "fetch_failed", parser.ParsedRelease{}, now, errors.New("upstream\nfailed")))
	failedParse, e := parser.ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><p>Synthetic unnumbered text</p></section>`))
	if !errors.Is(e, parser.ErrNoIncidents) {
		t.Fatalf("parse=%v", e)
	}
	must(db.FailRSSFetch(ctx, check, selected[1].ID, "parse_failed", failedParse, now, e))
	parsed, e := parser.ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><h2>1. Synthetic</h2><p>Body</p></section>`))
	must(e)
	// A failure after incident insertion must roll back both source state and incidents.
	_, e = db.db.Exec(`CREATE TRIGGER fail_rss_snapshot BEFORE INSERT ON rss_source_snapshots BEGIN SELECT RAISE(ABORT,'synthetic disk failure'); END`)
	must(e)
	if e := db.StoreRSSFetch(ctx, check, selected[2].ID, parsed, now); e == nil {
		t.Fatal("expected rollback")
	}
	var count int
	must(db.db.QueryRow(`SELECT COUNT(*) FROM incidents`).Scan(&count))
	if count != 0 {
		t.Fatalf("uncommitted incidents=%d", count)
	}
	_, e = db.db.Exec(`DROP TRIGGER fail_rss_snapshot`)
	must(e)
	must(db.FailRSSFetch(ctx, check, selected[2].ID, "storage_failed", parsed, now, errors.New("synthetic disk failure")))
	_, e = db.RecordSyncAttempt(ctx, now.Add(time.Minute), start, end)
	must(e)
	details, e := db.RSSCheckDetails(ctx, check, 10, 0)
	must(e)
	statuses := map[string]RSSDocumentDetail{}
	for _, d := range details.Documents {
		statuses[d.FetchStatus] = d
	}
	for _, status := range []string{"fetch_failed", "parse_failed", "storage_failed", "interrupted"} {
		if _, ok := statuses[status]; !ok {
			t.Fatalf("missing %s: %+v", status, statuses)
		}
	}
	d, e := db.RSSDocumentSnapshot(ctx, check, statuses["parse_failed"].ID)
	must(e)
	if d.ExtractedText != "Synthetic unnumbered text" || len(d.Incidents) != 0 {
		t.Fatalf("parse failure text=%+v", d)
	}
	history, e := db.ListRSSSyncHistory(ctx, 10, nil, nil)
	must(e)
	if history.Entries[1].FetchFailures != 1 || history.Entries[1].ParserFailures != 1 {
		t.Fatalf("lost partial counters=%+v", history.Entries[1])
	}
}

func TestRSSDetailsMigrationLeavesLegacyUnknown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = raw.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`)
	must(err)
	files, err := migrationFiles.ReadDir("migrations")
	must(err)
	version := 0
	for _, f := range files {
		if f.Name() >= "015" {
			break
		}
		version++
		body, e := migrationFiles.ReadFile("migrations/" + f.Name())
		must(e)
		_, e = raw.Exec(string(body))
		must(e)
		_, e = raw.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-09-07T12:00:00Z')`, version)
		must(e)
	}
	_, err = raw.Exec(`INSERT INTO rss_sync_history(source_name,started_at,completed_at,window_start,window_end,status,fetched) VALUES('munich-police-rss','2026-09-07T12:00:00Z','2026-09-07T12:01:00Z','2026-09-01T00:00:00Z','2026-09-08T00:00:00Z','succeeded',7)`)
	must(err)
	must(raw.Close())
	db, err := Open(ctx, path)
	must(err)
	defer db.Close()
	page, err := db.ListRSSSyncHistory(ctx, 10, nil, nil)
	must(err)
	if len(page.Entries) != 1 || page.Entries[0].DetailsRecorded || page.Entries[0].Fetched != 7 {
		t.Fatalf("legacy=%+v", page)
	}
	details, err := db.RSSCheckDetails(ctx, page.Entries[0].ID, 10, 0)
	must(err)
	if details.Recorded || len(details.Documents) != 0 {
		t.Fatalf("invented legacy details=%+v", details)
	}
}
