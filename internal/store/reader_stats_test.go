package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/location"
)

func TestReaderStatsCanonicalCoverageAndFilterParity(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	type fixture struct {
		name, area, category, published string
		translated                      bool
	}
	fixtures := []fixture{
		{"locality", "Neuperlach", "traffic", "2026-08-20T12:00:00Z", true},
		{"district", "Ramersdorf-Perlach", "theft_burglary", "2026-08-27T12:00:00Z", false},
		{"broad", "Schwabing", "traffic", "2026-09-03T12:00:00Z", true},
		{"outside", "Hochbrück", "fire_hazard", "2026-09-03T12:00:00Z", true},
		{"unknown", "Uncataloged place", "other", "2026-09-05T12:00:00Z", false},
		{"berlin-date", "Maxvorstadt", "traffic", "2026-09-12T22:30:00Z", true},
		{"neighborhood", "Neuhausen", "traffic", "2026-09-13T12:00:00Z", true},
	}
	var ids, runs []int64
	for _, fixture := range fixtures {
		published, _ := time.Parse(time.RFC3339, fixture.published)
		insertPipelineDocuments(t, ctx, db, published, fixture.name)
		var id int64
		var hash string
		if err := db.db.QueryRow(`SELECT i.id,i.content_hash FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE d.external_id=?`, fixture.name).Scan(&id, &hash); err != nil {
			t.Fatal(err)
		}
		values := map[string]string{"title_de": "Öffentlicher Testbericht", "summary_de": "Synthetische Zusammenfassung.", "area_name": fixture.area, "category": fixture.category}
		if fixture.translated {
			values["title_en"], values["summary_en"] = "Public test report", "Synthetic summary."
		}
		ids = append(ids, id)
		runs = append(runs, insertCompletedPresentationRun(t, ctx, db, id, hash, PipelineVersion, published, values))
	}
	// Original-only, stale-source, and obsolete-pipeline reports are not part of
	// the public archive, even when they have older publication timestamps.
	old := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, db, old, "original-only", "stale-source", "old-pipeline")
	for _, name := range []string{"stale-source", "old-pipeline"} {
		var id int64
		var hash string
		if err := db.db.QueryRow(`SELECT i.id,i.content_hash FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE d.external_id=?`, name).Scan(&id, &hash); err != nil {
			t.Fatal(err)
		}
		version := PipelineVersion
		if name == "stale-source" {
			hash = "stale"
		} else {
			version = "obsolete"
		}
		insertCompletedPresentationRun(t, ctx, db, id, hash, version, old, map[string]string{"title_de": "Excluded", "summary_de": "Excluded", "area_name": "Maxvorstadt", "category": "traffic", "title_en": "Excluded", "summary_en": "Excluded"})
	}
	query := ReaderStatsQuery{Language: "en", SourceMode: "fixture"}
	stats, err := db.ReaderStats(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 7 || stats.Available != 5 || stats.Mapped != 4 || stats.Outside != 1 || stats.Unassigned != 2 || stats.EarliestPublishedDate != "2026-08-20" || len(stats.Districts) != 25 {
		t.Fatalf("unexpected canonical stats: %+v", stats)
	}
	if stats.Categories[0].Category != "traffic" || stats.Categories[0].Total != 4 {
		t.Fatalf("category ranking: %+v", stats.Categories)
	}
	for _, district := range stats.Districts {
		if district.ID == "munich:district:ramersdorf-perlach" && (district.Total != 2 || district.Available != 1) {
			t.Fatalf("district and locality did not roll up: %+v", district)
		}
	}
	assertStatsReconcile(t, stats)
	assertParity := func(filters ReaderFilters, wantTotal, wantAvailable int) ReaderStatsResult {
		t.Helper()
		query.Filters = filters
		stats, err := db.ReaderStats(ctx, query)
		if err != nil || stats.Total != wantTotal || stats.Available != wantAvailable || stats.EarliestPublishedDate != "2026-08-20" {
			t.Fatalf("filters %+v: %+v / %v", filters, stats, err)
		}
		for _, language := range []string{"de", "en"} {
			list, err := db.ListReaderEntries(ctx, ReaderQuery{Language: language, SourceMode: "fixture", Limit: 1, Filters: filters})
			want := stats.Total
			if language == "en" {
				want = stats.Available
			}
			if err != nil || list.Total != want {
				t.Fatalf("statistics/list mismatch for %s %+v: %+v / %v", language, filters, list, err)
			}
		}
		assertStatsReconcile(t, stats)
		return stats
	}
	assertParity(ReaderFilters{District: "munich:district:ramersdorf-perlach"}, 2, 1)
	assertParity(ReaderFilters{District: "munich:district:ramersdorf-perlach", Category: "traffic"}, 1, 1)
	assertParity(ReaderFilters{District: location.DistrictOutside}, 1, 1)
	assertParity(ReaderFilters{District: location.DistrictUnassigned}, 2, 1)
	assertParity(ReaderFilters{District: "munich:district:laim"}, 0, 0)
	assertParity(ReaderFilters{Category: "traffic", From: "2026-09-13", To: "2026-09-13"}, 2, 2)
	clipped := assertParity(ReaderFilters{From: "2026-09-03", To: "2026-09-05"}, 3, 2)
	if len(clipped.Trend) != 1 || clipped.Trend[0].From != "2026-09-03" || clipped.Trend[0].To != "2026-09-05" {
		t.Fatalf("trend link widens selected range: %+v", clipped.Trend)
	}
	// Superseded translation output must disappear from coverage and list links
	// together, without changing the canonical count.
	if _, err := db.db.Exec(`UPDATE post_processing_jobs SET status='superseded' WHERE presentation_run_id=? AND processor_key='translation'`, runs[0]); err != nil {
		t.Fatal(err)
	}
	assertParity(ReaderFilters{District: "munich:district:ramersdorf-perlach"}, 2, 0)
	// Source invalidation removes the report from every count immediately.
	if _, err := db.db.Exec(`UPDATE incidents SET content_hash='changed' WHERE id=?`, ids[2]); err != nil {
		t.Fatal(err)
	}
	assertParity(ReaderFilters{}, 6, 3)
	// Accepted replacement presentations must be counted once. A translation
	// from the previous presentation does not make the replacement available.
	insertCompletedPresentationRun(t, ctx, db, ids[3], "content-outside", PipelineVersion, old.AddDate(7, 0, 0), map[string]string{"title_de": "Replacement", "summary_de": "Replacement", "area_name": "Maxvorstadt", "category": "other"})
	assertParity(ReaderFilters{District: location.DistrictOutside}, 0, 0)
	assertParity(ReaderFilters{District: "munich:district:maxvorstadt"}, 2, 1)
	assertParity(ReaderFilters{}, 6, 2)
	// Verified categories must replace extraction categories in every aggregate
	// and linked search, without creating a second report.
	completed := "2027-01-01T12:00:00Z"
	job, err := db.db.Exec(`INSERT INTO post_processing_jobs(presentation_run_id,processor_key,scope_key,request_kind,status,model_identity,prompt_version,input_hash,attempt_count,started_at,completed_at,created_at,updated_at) VALUES(?,'category_verification','default','imported','succeeded','test','test','',0,?,?,?,?)`, runs[0], completed, completed, completed, completed)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := job.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO post_processing_values(job_id,kind,value) VALUES(?,'is_correct','false'),(?,'corrected_category','other')`, jobID, jobID); err != nil {
		t.Fatal(err)
	}
	assertParity(ReaderFilters{District: "munich:district:ramersdorf-perlach", Category: "traffic"}, 0, 0)
	assertParity(ReaderFilters{District: "munich:district:ramersdorf-perlach", Category: "other"}, 1, 0)
	assertParity(ReaderFilters{}, 6, 2)
	for _, language := range []string{"de", "fr"} {
		query.Language, query.Filters = language, ReaderFilters{}
		stats, err := db.ReaderStats(ctx, query)
		want := 0
		if language == "de" {
			want = 6
		}
		if err != nil || stats.Total != 6 || stats.Available != want {
			t.Fatalf("UI language changes canonical basis: %s %+v / %v", language, stats, err)
		}
	}
	query.SourceMode = "live"
	stats, err = db.ReaderStats(ctx, query)
	if err != nil || stats.Total != 0 || stats.EarliestPublishedDate != "" {
		t.Fatalf("fixture source leaked into live statistics: %+v / %v", stats, err)
	}
}

func assertStatsReconcile(t *testing.T, stats ReaderStatsResult) {
	t.Helper()
	if stats.Total != stats.Mapped+stats.Outside+stats.Unassigned || stats.Available > stats.Total {
		t.Fatalf("geographic totals do not reconcile: %+v", stats)
	}
	var mapped, categoryTotal, categoryAvailable, trendTotal, trendAvailable int
	for _, district := range stats.Districts {
		mapped += district.Total
		if district.Available > district.Total {
			t.Fatalf("district coverage exceeds canonical count: %+v", district)
		}
	}
	for _, category := range stats.Categories {
		categoryTotal += category.Total
		categoryAvailable += category.Available
	}
	for _, bin := range stats.Trend {
		trendTotal += bin.Total
		trendAvailable += bin.Available
	}
	if mapped != stats.Mapped || categoryTotal != stats.Total || categoryAvailable != stats.Available || trendTotal != stats.Total || trendAvailable != stats.Available {
		t.Fatalf("aggregate totals do not reconcile: %+v", stats)
	}
}

func TestReaderStatsRejectAmbiguousFiltersAndHandleEmptyArchive(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, filters := range []ReaderFilters{
		{District: "Maxvorstadt"}, {District: "munich:district:unknown"},
		{District: "munich:district:maxvorstadt", Area: "Maxvorstadt"},
		{District: location.DistrictOutside, Areas: []string{"Garching"}},
		{Text: "language-dependent"}, {DateField: "incident"}, {Category: "invalid"},
		{From: "2026-10-03", To: "2026-10-01"}, {Period: "week", From: "2026-10-01"},
	} {
		if _, err := db.ReaderStats(ctx, ReaderStatsQuery{Language: "en", SourceMode: "fixture", Filters: filters}); err == nil {
			t.Errorf("accepted ambiguous statistics filters: %+v", filters)
		}
	}
	if _, err := db.ReaderStats(ctx, ReaderStatsQuery{Language: "xx", SourceMode: "fixture"}); err == nil {
		t.Fatal("accepted unregistered coverage language")
	}
	if _, err := db.ReaderStats(ctx, ReaderStatsQuery{Language: "en", SourceMode: "invalid"}); err == nil {
		t.Fatal("accepted invalid source mode")
	}
	stats, err := db.ReaderStats(ctx, ReaderStatsQuery{Language: "de", SourceMode: "fixture"})
	if err != nil || stats.Total != 0 || stats.EarliestPublishedDate != "" || len(stats.Districts) != 25 || len(stats.Trend) != 0 {
		t.Fatalf("empty archive: %+v / %v", stats, err)
	}
}

func TestPublicationTrendIncludesGapsAndBoundsLongArchives(t *testing.T) {
	trend, width, err := publicationTrend(map[string]PublicationCount{"2026-09-13": {Total: 2, Available: 1}, "2026-09-28": {Total: 3, Available: 2}})
	if err != nil || width != 1 || len(trend) != 4 || trend[0].From != "2026-09-07" || trend[1].Total != 0 || trend[3].Total != 3 {
		t.Fatalf("weekly gaps: %+v / %d / %v", trend, width, err)
	}
	trend, width, err = publicationTrend(map[string]PublicationCount{"1600-01-01": {Total: 1}, "2600-01-01": {Total: 2}})
	if err != nil || width <= 1 || len(trend) > 104 || trend[0].Total != 1 || trend[len(trend)-1].Total != 2 {
		t.Fatalf("long archive bounds: %d / %d / %v", len(trend), width, err)
	}
}
