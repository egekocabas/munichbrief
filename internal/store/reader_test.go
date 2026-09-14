package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderSearchOrderingAndTransactionalInvalidation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "reader.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 12, 22, 30, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, db, now, "clock-early", "clock-late", "day-part", "no-time", "no-date")
	rows, err := db.db.QueryContext(ctx, "SELECT id,content_hash FROM incidents ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	type identity struct {
		id   int64
		hash string
	}
	var ids []identity
	for rows.Next() {
		var item identity
		if err := rows.Scan(&item.id, &item.hash); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item)
	}
	rows.Close()
	var runs []int64
	for n, item := range ids {
		values := map[string]string{"title_de": fmt.Sprintf("İSTANBUL Straße 警察 हिंदी %d", n), "summary_de": "A literal quote \" and two matching words.", "title_en": fmt.Sprintf("English report %d", n), "summary_en": "Translated bicycle summary.", "category": "traffic", "area_name": "Maxvorstadt", "public_assistance_status": "requested"}
		if n < 4 {
			values["event_start_date"] = "2026-09-10"
		}
		if n == 0 {
			values["event_start_time"] = "09:30"
		}
		if n == 1 {
			values["event_start_time"] = "17:45"
		}
		if n == 2 {
			values["event_day_part"] = "night"
		}
		runs = append(runs, insertCompletedPresentationRun(t, ctx, db, item.id, item.hash, PipelineVersion, now, values))
	}
	assertReaderProjectionParity(t, db)
	// An existing v1 projection is detected and rebuilt transactionally on upgrade.
	if _, err := db.db.ExecContext(ctx, "DROP VIEW reader_projection; CREATE VIEW reader_projection AS "+originalReaderProjection()); err != nil {
		t.Fatal(err)
	}
	if err := db.installReaderIndex(ctx); err != nil {
		t.Fatal(err)
	}
	assertReaderProjectionParity(t, db)
	q := ReaderQuery{Language: "de", SourceMode: "fixture", View: "incident", Limit: 20}
	result, err := db.ListReaderEntries(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 5 {
		t.Fatalf("total=%d", result.Total)
	}
	for n, want := range []int{1, 0, 2, 3, 4} {
		if result.Records[n].ID != ids[want].id {
			t.Fatalf("position %d=%d, want %d", n, result.Records[n].ID, ids[want].id)
		}
	}
	// Equal clock times have a stable ID tie-breaker, including across a page
	// boundary. Updating the extracted value refreshes the index immediately.
	if _, err := db.db.ExecContext(ctx, "UPDATE presentation_values SET value='09:30' WHERE presentation_run_id=? AND kind='event_start_time'", runs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE source_documents SET published_at=? WHERE id=(SELECT source_document_id FROM incidents WHERE id=?)", formatTime(now), ids[1].id); err != nil {
		t.Fatal(err)
	}
	tied := q
	tied.Limit, tied.Offset = 1, 1
	if page, err := db.ListReaderEntries(ctx, tied); err != nil || len(page.Records) != 1 || page.Records[0].ID != ids[1].id {
		t.Fatalf("equal-time page boundary: %#v / %v", page, err)
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE presentation_values SET value='17:45' WHERE presentation_run_id=? AND kind='event_start_time'", runs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE source_documents SET published_at=? WHERE id=(SELECT source_document_id FROM incidents WHERE id=?)", formatTime(now.Add(time.Second)), ids[1].id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"istanbul", "ıstanbul", "STRASSE", "警察", "हिंदी", "matching words", "\""} {
		q.Filters = ReaderFilters{Text: query}
		r, e := db.ListReaderEntries(ctx, q)
		if e != nil || r.Total != 5 {
			t.Errorf("query %q = %d/%v", query, r.Total, e)
		}
	}
	q.Filters = ReaderFilters{Text: `anything" OR *`}
	r, e := db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 0 {
		t.Errorf("literal operators: %d/%v", r.Total, e)
	}
	q.Filters = ReaderFilters{Area: "Maxvorstadt", Category: "traffic", Number: "1", Assistance: "yes", DateField: "incident", From: "2026-09-10", To: "2026-09-10"}
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 4 {
		t.Fatalf("combined filters %d/%v", r.Total, e)
	}
	q.Filters = ReaderFilters{DateField: "published", From: "2026-09-13", To: "2026-09-13"}
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 5 {
		t.Fatalf("Berlin publication date %d/%v", r.Total, e)
	}
	q.Language = "en"
	q.Filters = ReaderFilters{Text: "istanbul"}
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 0 {
		t.Fatalf("language leaked %d/%v", r.Total, e)
	}
	q.Filters = ReaderFilters{Text: "bicycle"}
	q.Limit = 2
	q.Offset = 2
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 5 || len(r.Records) != 2 {
		t.Fatalf("page=%#v/%v", r, e)
	}
	// Source invalidation must remove both languages and FTS entries immediately.
	if _, e = db.db.ExecContext(ctx, "UPDATE incidents SET content_hash='changed' WHERE id=?", ids[0].id); e != nil {
		t.Fatal(e)
	}
	q.Limit = 20
	q.Offset = 0
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 4 {
		t.Fatalf("invalidated source=%d/%v", r.Total, e)
	}
	if _, e = db.db.ExecContext(ctx, "UPDATE post_processing_jobs SET status='superseded' WHERE presentation_run_id=?", runs[1]); e != nil {
		t.Fatal(e)
	}
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 3 {
		t.Fatalf("invalidated translation=%d/%v", r.Total, e)
	}
	assertReaderProjectionParity(t, db)
	// Rebuilding is idempotent and preserves selection, rather than old content.
	if e = db.installReaderIndex(ctx); e != nil {
		t.Fatal(e)
	}
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 3 {
		t.Fatalf("rebuild=%d/%v", r.Total, e)
	}
	if _, e = db.db.ExecContext(ctx, "DELETE FROM incidents WHERE id=?", ids[2].id); e != nil {
		t.Fatal(e)
	}
	r, e = db.ListReaderEntries(ctx, q)
	if e != nil || r.Total != 2 {
		t.Fatalf("delete=%d/%v", r.Total, e)
	}
	assertReaderProjectionParity(t, db)
}

func TestReaderFiltersRejectInvalidInput(t *testing.T) {
	for _, f := range []ReaderFilters{{From: "2026-02-30"}, {From: "2026-09-12", To: "2026-09-01"}, {DateField: "sql"}, {Category: "invalid"}, {Category: "traffic|fire_hazard"}, {Assistance: "maybe"}, {Text: string([]byte{0xff})}, {Text: "hello\x00"}} {
		if f.Validate() == nil {
			t.Errorf("accepted %#v", f)
		}
	}
}

// Run explicitly with -run '^$' -bench BenchmarkReaderIndex10000 -benchtime=3x.
// Bulk setup uses processing runs, then publishes them through the same triggers.
func BenchmarkReaderIndex10000(b *testing.B) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statements := []string{
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000)
 INSERT INTO source_documents(id,external_id,source_url,title,published_at,discovered_at,last_seen_at,feed_fingerprint,fetch_status,source_hash)
 SELECT x,'synthetic-'||x,'https://fixture.invalid/'||x,'Synthetic source','2026-09-12T12:00:00Z','2026-09-12T12:00:00Z','2026-09-12T12:00:00Z','f','fixture','s' FROM n`,
		`INSERT INTO incidents(id,source_document_id,incident_number,position,title_de,content_hash,created_at,updated_at) SELECT id,id,CAST(id AS TEXT),0,'Synthetic','hash','2026-09-12T12:00:00Z','2026-09-12T12:00:00Z' FROM source_documents`,
		`INSERT INTO presentation_runs(id,incident_id,source_hash,pipeline_version,status,legacy,created_at) SELECT id,id,'hash','` + PipelineVersion + `','processing',0,'2026-09-12T12:00:00Z' FROM incidents`,
		`INSERT INTO presentation_values(presentation_run_id,kind,value,model_identity,prompt_version,generated_at) SELECT id,'title_de','Synthetic bicycle report '||id,'fixture','fixture','2026-09-12T12:00:00Z' FROM presentation_runs`,
		`INSERT INTO presentation_values(presentation_run_id,kind,value,model_identity,prompt_version,generated_at) SELECT id,'summary_de','Synthetic police report for testing only. 警察','fixture','fixture','2026-09-12T12:00:00Z' FROM presentation_runs`,
		`INSERT INTO presentation_values(presentation_run_id,kind,value,model_identity,prompt_version,generated_at) SELECT id,'event_start_date','2026-09-10','fixture','fixture','2026-09-12T12:00:00Z' FROM presentation_runs`,
		`UPDATE presentation_runs SET status='complete',completed_at='2026-09-12T12:00:00Z'`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			tx.Rollback()
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	for _, scenario := range []struct{ name, view, text string }{{"published", "published", ""}, {"incident", "incident", ""}, {"text", "published", "bicycle"}, {"short_chinese", "published", "警察"}} {
		b.Run(scenario.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				r, err := db.ListReaderEntries(ctx, ReaderQuery{Language: "de", SourceMode: "fixture", View: scenario.view, Limit: 20, Filters: ReaderFilters{Text: scenario.text}})
				if err != nil || r.Total != 10000 || len(r.Records) != 20 {
					b.Fatalf("benchmark results %d/%d/%v", r.Total, len(r.Records), err)
				}
			}
		})
	}
}

func TestReaderIndexReusesSchemaAndRepairsDrift(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reader-schema.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var initial int
	if err = db.db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if err = db.installReaderIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var unchanged int
	if err = db.db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&unchanged); err != nil || unchanged != initial {
		t.Fatalf("unchanged schema recreated: %d -> %d (%v)", initial, unchanged, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&unchanged); err != nil || unchanged != initial {
		t.Fatalf("reopen recreated schema: %d -> %d (%v)", initial, unchanged, err)
	}
	// Repair a missing trigger and an obsolete trigger from a previous definition.
	if _, err = db.db.ExecContext(ctx, `DROP TRIGGER reader_refresh_incidents_insert; CREATE TRIGGER reader_refresh_obsolete AFTER INSERT ON incidents BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	if err = db.installReaderIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name='reader_refresh_incidents_insert'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("trigger not repaired: %d %v", count, err)
	}
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='reader_refresh_obsolete'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("obsolete trigger remains: %d %v", count, err)
	}
	// A changed stored view forces a rebuild even if its object name still exists.
	if _, err = db.db.ExecContext(ctx, `DROP VIEW reader_projection; CREATE VIEW reader_projection AS SELECT 1 AS obsolete`); err != nil {
		t.Fatal(err)
	}
	if err = db.installReaderIndex(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `SELECT language FROM reader_projection LIMIT 1`); err != nil {
		t.Fatalf("view not repaired: %v", err)
	}
}
