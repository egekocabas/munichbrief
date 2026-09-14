package gazetteer

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshHistoryRecordsSuccessFailureAndUnchangedSources(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	responseStatus := http.StatusOK
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if responseStatus == http.StatusNotModified && request.Header.Get("If-None-Match") == "\"one\"" {
			return &http.Response{StatusCode: http.StatusNotModified, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: responseStatus, Header: http.Header{"Etag": []string{"\"one\""}}, Body: io.NopCloser(strings.NewReader(`{"features":[{"id":"1","properties":{"strassenname":"Ganghoferstraße"}}]}`))}, nil
	})}
	definition := SourceDefinition{Key: "munich_streets", DisplayName: "Official streets", URL: "https://example.test/source", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 1024, Parse: parseMunichStreets}
	fetcher, _ := NewFetcher(client, "MunichBrief/test", store)
	manager, err := NewManager(ctx, store, fetcher, []SourceDefinition{definition}, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Refresh(ctx)
	if err != nil || first.GenerationID == 0 || first.NotModified {
		t.Fatalf("first refresh = %#v, %v", first, err)
	}
	responseStatus = http.StatusNotModified
	second, err := manager.Refresh(ctx)
	if err != nil || !second.NotModified || second.GenerationID != first.GenerationID {
		t.Fatalf("unchanged refresh = %#v, %v", second, err)
	}
	responseStatus = http.StatusBadGateway
	if _, err := manager.Refresh(ctx); err == nil {
		t.Fatal("failed refresh succeeded")
	}
	history, err := store.RefreshHistory(ctx, 1)
	if err != nil || len(history.Entries) != 3 {
		t.Fatalf("history = %#v, %v", history, err)
	}
	failed := history.Entries[0]
	if failed.Status != "failed" || failed.FailureStage != "http_status" || failed.FailedSourceKey != definition.Key || failed.ActiveGenerationAfter != first.GenerationID || failed.SourcesFailed != 1 {
		t.Fatalf("failed history = %#v", failed)
	}
	unchanged := history.Entries[1]
	if unchanged.Status != "succeeded" || unchanged.Changed == nil || *unchanged.Changed || unchanged.AllNotModified == nil || !*unchanged.AllNotModified {
		t.Fatalf("unchanged history = %#v", unchanged)
	}
	details, err := store.RefreshDetails(ctx, unchanged.ID)
	if err != nil || len(details.Sources) != 1 || details.Sources[0].Status != "not_modified" || details.Sources[0].HTTPStatus != http.StatusNotModified || details.Sources[0].RowCount != 1 {
		t.Fatalf("unchanged details = %#v, %v", details, err)
	}
	snapshot, err := store.AdminSnapshot(ctx)
	if err != nil || snapshot.LatestRun == nil || snapshot.LatestRun.ID != failed.ID || len(snapshot.KindCounts) != 1 || snapshot.KindCounts[0].Kind != KindStreet || snapshot.KindCounts[0].Count != 1 || snapshot.Sources[0].LastError == "" || snapshot.Sources[0].LastFailureRunID != failed.ID {
		t.Fatalf("admin snapshot = %#v, %v", snapshot, err)
	}
	responseStatus = http.StatusOK
	if _, err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.AdminSnapshot(ctx)
	if err != nil || recovered.Sources[0].ConsecutiveFailures != 0 || recovered.Sources[0].LastError != "" || recovered.Sources[0].LastFailureRunID != 0 || recovered.LatestRun == nil || recovered.LatestRun.Status != "succeeded" {
		t.Fatalf("recovered source health = %#v, %v", recovered, err)
	}
	history, err = store.RefreshHistory(ctx, 1)
	if err != nil || len(history.Entries) != 4 || history.Entries[1].ID != failed.ID || history.Entries[1].ErrorMessage == "" {
		t.Fatalf("retained failed history = %#v, %v", history, err)
	}
}

func TestOpenRecoversInterruptedRefreshAndUntouchedSources(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gazetteer.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sources := []SourceDefinition{
		{Key: "one", DisplayName: "One", URL: "https://example.test/one", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 10, Parse: func([]byte) ([]Entry, error) { return nil, nil }},
		{Key: "two", DisplayName: "Two", URL: "https://example.test/two", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 10, Parse: func([]byte) ([]Entry, error) { return nil, nil }},
	}
	runID, err := store.BeginRefresh(ctx, RefreshTriggerScheduled, sources, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartRefreshSource(ctx, runID, "one", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.recoverInterruptedRefreshes(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	details, err := reopened.RefreshDetails(ctx, runID)
	if err != nil || details.Run.Status != "interrupted" || details.Run.FailureStage != "interruption" || details.Sources[0].Status != "interrupted" || details.Sources[1].Status != "not_run" {
		t.Fatalf("recovered details = %#v, %v", details, err)
	}
	snapshot, err := reopened.AdminSnapshot(ctx)
	if err != nil || len(snapshot.Sources) != 2 || snapshot.Sources[0].Key != "one" || snapshot.Sources[0].ConsecutiveFailures != 1 || snapshot.Sources[0].LastError == "" || snapshot.Sources[0].LastFailureRunID != runID || snapshot.Sources[1].ConsecutiveFailures != 0 {
		t.Fatalf("interrupted source health = %#v, %v", snapshot.Sources, err)
	}
}

func TestSourceFailureMarksLaterSourcesNotRun(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sources := []SourceDefinition{{Key: "one", DisplayName: "One", URL: "https://example.test/one", License: "test", Attribution: "test"}, {Key: "two", DisplayName: "Two", URL: "https://example.test/two", License: "test", Attribution: "test"}}
	runID, err := store.BeginRefresh(ctx, RefreshTriggerRetry, sources, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartRefreshSource(ctx, runID, "one", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.FailRefresh(ctx, runID, &sources[0], "download", SourceFetchDiagnostic{}, errors.New("timeout"), time.Now()); err != nil {
		t.Fatal(err)
	}
	details, err := store.RefreshDetails(ctx, runID)
	if err != nil || details.Sources[0].Status != "failed" || details.Sources[1].Status != "not_run" || details.Run.Trigger != string(RefreshTriggerRetry) || details.Run.SourcesCompleted != 0 || details.Run.SourcesFailed != 1 || details.Run.SourcesSkipped != 1 {
		t.Fatalf("failure details = %#v, %v", details, err)
	}
}

func TestCompletedSourceUpdatesHealthBeforeRunActivation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sources := []SourceDefinition{
		{Key: "one", DisplayName: "One", URL: "https://example.test/one", License: "test", Attribution: "test"},
		{Key: "two", DisplayName: "Two", URL: "https://example.test/two", License: "test", Attribution: "test"},
	}
	failedRun, err := store.BeginRefresh(ctx, RefreshTriggerScheduled, sources, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartRefreshSource(ctx, failedRun, "one", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.FailRefresh(ctx, failedRun, &sources[0], "download", SourceFetchDiagnostic{}, errors.New("timeout"), time.Now()); err != nil {
		t.Fatal(err)
	}

	succeededAt := time.Now().Add(time.Minute).UTC()
	activeRun, err := store.BeginRefresh(ctx, RefreshTriggerRetry, sources, succeededAt, succeededAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartRefreshSource(ctx, activeRun, "one", succeededAt); err != nil {
		t.Fatal(err)
	}
	snapshot := SourceSnapshot{Definition: sources[0], ContentHash: "hash", FetchedAt: succeededAt, Entries: []Entry{{Name: "Schwabing", Kind: KindNeighbourhood}}}
	if err := store.CompleteRefreshSource(ctx, activeRun, snapshot, false, SourceFetchDiagnostic{RowCount: 1}, succeededAt); err != nil {
		t.Fatal(err)
	}

	health, err := store.AdminSnapshot(ctx)
	if err != nil || len(health.Sources) != 2 {
		t.Fatalf("source health = %#v, %v", health.Sources, err)
	}
	if health.Sources[0].LastSuccess.IsZero() || !health.Sources[0].LastSuccess.Equal(succeededAt) || health.Sources[0].ConsecutiveFailures != 0 || health.Sources[0].LastError != "" {
		t.Fatalf("completed source health = %#v", health.Sources[0])
	}
}

func TestRefreshDiagnosticsAreSanitizedAndBounded(t *testing.T) {
	value := safeDiagnostic(errors.New("failure\nwith\tcontrols " + strings.Repeat("x", 3000)))
	if strings.ContainsAny(value, "\n\t") || len(value) > maxDiagnosticBytes || !strings.HasPrefix(value, "failure with controls") {
		t.Fatalf("safe diagnostic length=%d value=%q", len(value), value)
	}
}

func TestFailedRefreshWithChangedSourceDoesNotReuseOldValidators(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old := SourceDefinition{Key: "source", DisplayName: "Source", URL: "https://example.test/old", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 10}
	entry := Entry{Name: "Schwabing", Kind: KindNeighbourhood, Priority: 10, Sources: []EntrySource{{Key: old.Key, ExternalID: "1", Kind: KindNeighbourhood, Priority: 10}}}
	snapshot := SourceSnapshot{Definition: old, ContentHash: "old-hash", ETag: "old-etag", LastModified: "old-date", FetchedAt: time.Now(), Entries: []Entry{entry}}
	if _, _, err := store.Activate(ctx, []SourceSnapshot{snapshot}, []Entry{entry}, "aggregate", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	changed := old
	changed.URL = "https://example.test/new"
	runID, err := store.BeginRefresh(ctx, RefreshTriggerManual, []SourceDefinition{changed}, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartRefreshSource(ctx, runID, changed.Key, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.FailRefresh(ctx, runID, &changed, "download", SourceFetchDiagnostic{}, errors.New("connection refused"), time.Now()); err != nil {
		t.Fatal(err)
	}
	etag, modified, hash, sourceURL, contract, err := store.SourceValidators(ctx, changed.Key)
	if err != nil || etag != "" || modified != "" || hash != "" || sourceURL != changed.URL || contract != sourceContractVersion {
		t.Fatalf("changed source validators = %q/%q/%q/%q/%q, %v", etag, modified, hash, sourceURL, contract, err)
	}
}

func TestGazetteerMigrationThreePreservesExistingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gazetteer.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/001_initial.sql", "migrations/002_source_contract.sql"} {
		contents, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES (1,'2026-01-01T00:00:00Z'),(2,'2026-01-01T00:00:00Z'); INSERT INTO gazetteer_sources(source_key,display_name,source_url,license,attribution,etag,last_modified,content_sha256,contract_version) VALUES ('source','Source','https://example.test','test','test','etag','modified','hash','contract'); INSERT INTO gazetteer_generations(id,status,aggregate_sha256,created_at,activated_at,entry_count) VALUES (7,'active','aggregate','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',1); INSERT INTO gazetteer_names(generation_id,value,normalized_value,kind,priority) VALUES (7,'Schwabing','Schwabing','neighbourhood',10); INSERT INTO gazetteer_generation_sources(generation_id,source_key,content_sha256,fetched_at,row_count) VALUES (7,'source','hash','2026-01-01T00:00:00Z',1); INSERT INTO gazetteer_name_sources(name_id,source_key,external_id,source_kind,source_priority) SELECT id,'source','external','neighbourhood',10 FROM gazetteer_names; UPDATE gazetteer_state SET active_generation_id=7`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	status, err := store.Status(ctx)
	if err != nil || status.ActiveGeneration != 7 || status.EntryCount != 1 {
		t.Fatalf("preserved status = %#v, %v", status, err)
	}
	if history, err := store.RefreshHistory(ctx, 1); err != nil || len(history.Entries) != 0 {
		t.Fatalf("new history = %#v, %v", history, err)
	}
	entries, err := store.ActiveEntries(ctx)
	if err != nil || len(entries) != 1 || entries[0].Name != "Schwabing" || entries[0].Kind != KindNeighbourhood {
		t.Fatalf("preserved entries = %#v, %v", entries, err)
	}
	etag, modified, hash, sourceURL, contract, err := store.SourceValidators(ctx, "source")
	if err != nil || etag != "etag" || modified != "modified" || hash != "hash" || sourceURL != "https://example.test" || contract != "contract" {
		t.Fatalf("preserved source = %q/%q/%q/%q/%q, %v", etag, modified, hash, sourceURL, contract, err)
	}
	overrides, err := store.Overrides(ctx)
	if err != nil || overrides["Brand"] != "exclude" || overrides["Haar"] != "context" {
		t.Fatalf("preserved overrides = %#v, %v", overrides, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshHistoryRetentionAndPaginationAreBounded(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for index := 0; index < refreshHistoryLimit+1; index++ {
		at := started.Add(time.Duration(index) * time.Second)
		runID, err := store.BeginRefresh(ctx, RefreshTriggerManual, nil, at, at.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FailRefresh(ctx, runID, nil, "matcher", SourceFetchDiagnostic{}, errors.New("matcher failed"), at.Add(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gazetteer_refresh_runs`).Scan(&count); err != nil || count != refreshHistoryLimit {
		t.Fatalf("retained runs = %d, %v", count, err)
	}
	first, err := store.RefreshHistory(ctx, 1)
	if err != nil || len(first.Entries) != refreshPageSize || first.HasNewer || !first.HasOlder {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	last, err := store.RefreshHistory(ctx, refreshHistoryLimit/refreshPageSize)
	if err != nil || len(last.Entries) != refreshPageSize || !last.HasNewer || last.HasOlder {
		t.Fatalf("last page = %#v, %v", last, err)
	}
}
