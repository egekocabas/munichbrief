package main

import (
	"bytes"
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

func TestDisabledAIUsesANilWebProcessor(t *testing.T) {
	if webProcessor(nil) != nil {
		t.Fatal("disabled AI produced a typed-nil web processor")
	}
}

func TestRunAIProcessQueuesNeverStartedIncident(t *testing.T) {
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
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsurePipelineSteps(ctx, processing.ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for _, step := range processing.StepKeys() {
		if err := database.SetPipelineStepModel(ctx, step, "qwen3.5:4b", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetPipelineStepModel(ctx, processing.TranslationModelStep, "qwen3.5:4b", now); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DatabasePath: databasePath, SourceMode: "fixture"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runAIProcess(ctx, logger, cfg, []string{"--incident", formatInt(records[0].ID)}); err != nil {
		t.Fatal(err)
	}

	database, err = store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	stats, err := database.PipelineSnapshot(ctx, "fixture", processing.StepKeys(), time.Now())
	if err != nil || len(stats.Steps) != 2 || stats.Steps[0].Queued != 1 {
		t.Fatalf("stats = %#v, err=%v", stats, err)
	}
}

func TestRunAIProcessRequiresExactlyOneSelector(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{DatabasePath: filepath.Join(t.TempDir(), "retry.db"), SourceMode: "fixture"}
	if err := runAIProcess(context.Background(), logger, cfg, nil); err == nil {
		t.Fatal("missing selector was accepted")
	}
	if err := runAIProcess(context.Background(), logger, cfg, []string{"--all", "--incident", "1"}); err == nil {
		t.Fatal("conflicting selectors were accepted")
	}
}

func TestRunAIProcessRequiresEverySavedStepModel(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unconfigured.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	err = runAIProcess(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{DatabasePath: path, SourceMode: "fixture"}, []string{"--all"})
	if err == nil || !strings.Contains(err.Error(), "AI pipeline models are not configured") {
		t.Fatalf("unconfigured ai-process error = %v", err)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "fixture")
	err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), []string{"unknown"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "unknown"`) {
		t.Fatalf("run() error = %v, want unknown command", err)
	}
}

func TestRunBackupStreamsRestorableSQLiteDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "source.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runBackup(ctx, config.Config{DatabasePath: path}, []string{"--output", "-"}, &output); err != nil {
		t.Fatalf("runBackup() error = %v", err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("backup prefix = %q, want SQLite header", output.Bytes()[:min(output.Len(), 16)])
	}
	if err := runBackup(ctx, config.Config{DatabasePath: path}, nil, io.Discard); err == nil {
		t.Fatal("runBackup() accepted missing --output")
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
