package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPublicationPeriodsUseBerlinCalendarDays(t *testing.T) {
	for _, tc := range []struct{ instant, period, from, to string }{
		{"2026-10-02T22:30:00Z", "today", "2026-10-03", "2026-10-03"},
		{"2026-03-29T22:30:00Z", "week", "2026-03-24", "2026-03-30"},
		{"2026-10-25T23:30:00Z", "week", "2026-10-20", "2026-10-26"},
	} {
		now, err := time.Parse(time.RFC3339, tc.instant)
		if err != nil {
			t.Fatal(err)
		}
		from, to, err := publicationPeriod(tc.period, now)
		if err != nil || from != tc.from || to != tc.to {
			t.Errorf("%s/%s: %s–%s, %v", tc.instant, tc.period, from, to, err)
		}
	}
}

func TestNeighborhoodUnionCombinesWithAdvancedFilters(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "neighborhoods.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	insertPipelineDocuments(t, ctx, db, now, "north", "center", "south")
	rows, err := db.db.QueryContext(ctx, "SELECT id,content_hash FROM incidents ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	type item struct {
		id   int64
		hash string
	}
	var items []item
	for rows.Next() {
		var v item
		if err := rows.Scan(&v.id, &v.hash); err != nil {
			t.Fatal(err)
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for n, v := range items {
		category := "traffic"
		if n == 1 {
			category = "other"
		}
		insertCompletedPresentationRun(t, ctx, db, v.id, v.hash, PipelineVersion, now, map[string]string{
			"title_de": "Synthetischer Fahrradbericht", "summary_de": "Ein erfundener Testbericht.",
			"category": category, "area_name": []string{"Schwabing", "Maxvorstadt", "Sendling"}[n],
			"public_assistance_status": "requested", "event_start_date": "2026-09-01",
		})
	}
	query := ReaderQuery{Language: "de", SourceMode: "fixture", Limit: 20, Filters: ReaderFilters{Areas: []string{"Schwabing", "Maxvorstadt"}}}
	assertTotal := func(want int) {
		t.Helper()
		result, err := db.ListReaderEntries(ctx, query)
		if err != nil || result.Total != want {
			t.Fatalf("%+v: total=%d want=%d err=%v", query.Filters, result.Total, want, err)
		}
	}
	assertTotal(2)
	query.Filters.Text, query.Filters.Category, query.Filters.Assistance = "Fahrrad", "traffic", "yes"
	query.Filters.DateField, query.Filters.From, query.Filters.To = "incident", "2026-09-01", "2026-09-01"
	assertTotal(1)
	query.Filters = ReaderFilters{Areas: []string{"Schwabing", "Maxvorstadt"}, Period: "today"}
	assertTotal(2)
	query.Limit, query.Offset = 1, 1
	result, err := db.ListReaderEntries(ctx, query)
	if err != nil || result.Total != 2 || len(result.Records) != 1 {
		t.Fatalf("pagination: %+v/%v", result, err)
	}
	query.Limit, query.Offset = 20, 0
	query.Filters.Areas = []string{"Schwabing') OR 1=1 --"}
	assertTotal(0)
	query.Filters.Areas = []string{"Schwabing", "Maxvorstadt"}
	if _, err = db.db.ExecContext(ctx, "UPDATE incidents SET content_hash='changed' WHERE id=?", items[0].id); err != nil {
		t.Fatal(err)
	}
	assertTotal(1)
}
