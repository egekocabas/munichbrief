package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/config"
	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/observability"
	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestRunAIRetryQueuesReviewRequiredJob(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "retry.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	documents := []domain.SourceDocument{{
		ExternalID: "one", SourceURL: "https://fixture.invalid/one", Title: "One",
		PublishedAt: now, FeedFingerprint: "feed", SourceHash: "source",
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel", BodyDE: "Text", ContentHash: "content"}},
	}}
	if err := database.UpsertDocuments(ctx, documents, now); err != nil {
		t.Fatal(err)
	}
	operation := processing.Operation("qwen3.5:4b")
	job, found, err := database.QueueAndClaimProcessingJob(ctx, operation, now)
	if err != nil || !found {
		t.Fatalf("claim = %t/%v", found, err)
	}
	if err := database.FailProcessingJob(ctx, job, "needs_review", "privacy", nil, now, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DatabasePath: databasePath, OllamaModel: "qwen3.5:4b"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runAIRetry(ctx, logger, cfg, []string{"--incident", formatInt(job.IncidentID)}); err != nil {
		t.Fatal(err)
	}

	database, err = store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	stats, err := database.ProcessingQueueStats(ctx, operation, time.Now())
	if err != nil || stats.Queued != 1 || stats.NeedsReview != 0 {
		t.Fatalf("stats = %#v, err=%v", stats, err)
	}
}

func TestRunAIRetryRequiresExactlyOneSelector(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{DatabasePath: filepath.Join(t.TempDir(), "retry.db"), OllamaModel: "qwen3.5:4b"}
	if err := runAIRetry(context.Background(), logger, cfg, nil); err == nil {
		t.Fatal("missing selector was accepted")
	}
	if err := runAIRetry(context.Background(), logger, cfg, []string{"--all", "--incident", "1"}); err == nil {
		t.Fatal("conflicting selectors were accepted")
	}
}

func TestRestoreFeedSuccessMetricUsesPersistedState(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	succeededAt := time.Unix(1234, 0)
	if err := database.RecordSyncSuccess(ctx, "etag", "modified", succeededAt, "ok"); err != nil {
		t.Fatal(err)
	}

	metrics := observability.NewMetrics("test", time.Now())
	if err := restoreFeedSuccessMetric(ctx, database, metrics); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), "munichbrief_last_feed_success_timestamp_seconds 1234") {
		t.Fatal("metrics did not expose the persisted feed success timestamp")
	}
}

func TestRandomizedLiveSyncDelayStaysAroundSixHours(t *testing.T) {
	tests := []struct {
		name   string
		offset time.Duration
		want   time.Duration
	}{
		{name: "minimum", offset: 0, want: 5*time.Hour + 30*time.Minute},
		{name: "center", offset: liveSyncJitter, want: 6 * time.Hour},
		{name: "maximum exclusive", offset: 2*liveSyncJitter - time.Second, want: 6*time.Hour + 30*time.Minute - time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := randomizedLiveSyncDelay(test.offset); got != test.want {
				t.Fatalf("randomizedLiveSyncDelay() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestLiveSyncRetryDelayUsesBoundedBackoff(t *testing.T) {
	tests := []struct {
		failedAttempts int
		want           time.Duration
	}{
		{failedAttempts: 1, want: time.Minute},
		{failedAttempts: 2, want: 2 * time.Minute},
		{failedAttempts: 5, want: 16 * time.Minute},
		{failedAttempts: 6, want: 30 * time.Minute},
		{failedAttempts: 20, want: 30 * time.Minute},
	}
	for _, test := range tests {
		t.Run(strconv.Itoa(test.failedAttempts), func(t *testing.T) {
			if got := liveSyncRetryDelay(test.failedAttempts); got != test.want {
				t.Fatalf("liveSyncRetryDelay(%d) = %s, want %s", test.failedAttempts, got, test.want)
			}
		})
	}
}

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
