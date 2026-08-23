package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/config"
	"github.com/egekocabas/munichbrief/internal/domain"
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

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
