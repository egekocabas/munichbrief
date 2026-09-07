package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRSSSyncHistoryPersistsOutcomesAndPaginatesWithStableCursors(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "rss-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	start := time.Date(2026, time.September, 5, 6, 0, 0, 0, time.UTC)
	windowStart := start.Add(-6 * 24 * time.Hour)
	windowEnd := start.Add(24 * time.Hour)
	succeededID, err := database.RecordSyncAttempt(ctx, start, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	success := SyncRunResult{
		FeedDocuments: 6, Discovered: 6, Fetched: 6, Skipped: 0,
		DurationSeconds: 1.25, Summary: "not_modified=false documents=6 discovered=6 fetched=6 fetch_failures=0 parser_failures=0 skipped=0",
	}
	if err := database.RecordSyncSuccess(ctx, succeededID, `"feed-v1"`, "modified", start.Add(2*time.Second), success); err != nil {
		t.Fatal(err)
	}

	failedAt := start.Add(time.Hour)
	failedID, err := database.RecordSyncAttempt(ctx, failedAt, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	failure := SyncRunResult{FeedDocuments: 6, Discovered: 6, Fetched: 2, FetchFailures: 1, DurationSeconds: 0.75, Summary: "partial"}
	if err := database.RecordSyncFailure(ctx, failedID, failedAt.Add(time.Second), failure, errors.New("feed timeout\nretry later")); err != nil {
		t.Fatal(err)
	}

	runningAt := start.Add(2 * time.Hour)
	runningID, err := database.RecordSyncAttempt(ctx, runningAt, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}

	first, err := database.ListRSSSyncHistory(ctx, 2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.HasNewer || !first.HasOlder {
		t.Fatalf("first RSS history page = %#v", first)
	}
	if first.Entries[0].ID != runningID || first.Entries[0].Status != "running" || first.Entries[0].CompletedAt != nil {
		t.Fatalf("running RSS entry = %#v", first.Entries[0])
	}
	failed := first.Entries[1]
	if failed.ID != failedID || failed.Status != "failed" || failed.ErrorMessage != "feed timeout retry later" || failed.Fetched != 2 || failed.FetchFailures != 1 || failed.CompletedAt == nil {
		t.Fatalf("failed RSS entry = %#v", failed)
	}

	older, err := database.ListRSSSyncHistory(ctx, 2, &RSSSyncHistoryCursor{StartedAt: failed.StartedAt, ID: failed.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 1 || !older.HasNewer || older.HasOlder {
		t.Fatalf("older RSS history page = %#v", older)
	}
	succeeded := older.Entries[0]
	if succeeded.ID != succeededID || succeeded.Status != "succeeded" || succeeded.NotModified || succeeded.FeedDocuments != 6 || succeeded.Fetched != 6 || succeeded.DurationSeconds != 1.25 {
		t.Fatalf("succeeded RSS entry = %#v", succeeded)
	}

	newer, err := database.ListRSSSyncHistory(ctx, 2, nil, &RSSSyncHistoryCursor{StartedAt: succeeded.StartedAt, ID: succeeded.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(newer.Entries) != 2 || newer.Entries[0].ID != runningID || newer.Entries[1].ID != failedID {
		t.Fatalf("newer RSS history page = %#v", newer)
	}

	if _, err := database.ListRSSSyncHistory(ctx, 0, nil, nil); err == nil {
		t.Fatal("zero RSS history limit was accepted")
	}
	if _, err := database.ListRSSSyncHistory(ctx, 1, &RSSSyncHistoryCursor{ID: 1}, &RSSSyncHistoryCursor{ID: 2}); err == nil {
		t.Fatal("two RSS history cursors were accepted")
	}
}

func TestStartingRSSSyncClosesAnInterruptedAttempt(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "rss-interruption.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	startedAt := time.Date(2026, time.September, 5, 6, 0, 0, 0, time.UTC)
	windowStart := startedAt.Add(-6 * 24 * time.Hour)
	windowEnd := startedAt.Add(24 * time.Hour)
	interruptedID, err := database.RecordSyncAttempt(ctx, startedAt, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RecordSyncAttempt(ctx, startedAt.Add(time.Hour), windowStart, windowEnd); err != nil {
		t.Fatal(err)
	}

	history, err := database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 2 || history.Entries[0].Status != "running" {
		t.Fatalf("RSS interruption history = %#v", history)
	}
	interrupted := history.Entries[1]
	if interrupted.ID != interruptedID || interrupted.Status != "failed" || interrupted.ErrorMessage != "synchronization interrupted before completion" || interrupted.CompletedAt == nil || interrupted.DurationSeconds < 3599 {
		t.Fatalf("interrupted RSS attempt = %#v", interrupted)
	}
}
