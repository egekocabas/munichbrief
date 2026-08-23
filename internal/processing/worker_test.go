package processing

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

type fakeGenerator struct{}

func (fakeGenerator) ModelIdentity() string { return "test-model" }

func (fakeGenerator) Generate(context.Context, string, string) (store.AIPresentation, string, error) {
	return store.AIPresentation{
		TitleDE:       "Kurzer deutscher Titel",
		SummaryDE:     "Eine kurze deutsche Zusammenfassung.",
		TitleEN:       "Short English title",
		SummaryEN:     "A short English summary.",
		PrivacyStatus: "safe",
	}, "test-model", nil
}

type sequenceGenerator struct {
	calls int
	fail  func(int) error
}

func (g *sequenceGenerator) ModelIdentity() string { return "test-model" }
func (g *sequenceGenerator) Generate(context.Context, string, string) (store.AIPresentation, string, error) {
	g.calls++
	if err := g.fail(g.calls); err != nil {
		return store.AIPresentation{}, "", err
	}
	return fakeGenerator{}.Generate(context.Background(), "", "")
}

func TestWorkerProcessesExistingIncidentsAndPersistsPresentation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatalf("UpsertDocuments() error = %v", err)
	}

	var logs bytes.Buffer
	worker, err := NewWorker(database, fakeGenerator{}, nil, slog.New(slog.NewTextHandler(&logs, nil)), time.Second, time.Now, Schedule{Immediate: true})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	worker.processAvailable(ctx)

	incidents, _, err := database.ListIncidents(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListIncidents() error = %v", err)
	}
	for _, incident := range incidents {
		if !incident.HasAI || incident.AITitleDE != "Kurzer deutscher Titel" || incident.AISummaryEN == "" {
			t.Errorf("incident %d has incomplete AI presentation: %#v", incident.ID, incident)
		}
		if incident.BodyDE == "" {
			t.Errorf("incident %d original body was removed before quality review", incident.ID)
		}
	}
	depth, err := database.ProcessingQueueDepth(ctx, worker.operation)
	if err != nil || depth != 0 {
		t.Errorf("queue depth = %d, err=%v; want 0", depth, err)
	}
	logText := logs.String()
	for _, expected := range []string{"AI request started", "AI response received and validated", "AI processing completed and persisted", "duration_seconds="} {
		if !strings.Contains(logText, expected) {
			t.Errorf("AI lifecycle logs do not contain %q: %s", expected, logText)
		}
	}
	if strings.Contains(logText, "Am Freitagvormittag") {
		t.Fatalf("AI lifecycle logs contain source body text: %s", logText)
	}
}

func TestReleaseSpecificFailureContinuesWithNextJobs(t *testing.T) {
	ctx := context.Background()
	database := fixtureProcessingStore(t, ctx)
	generator := &sequenceGenerator{fail: func(call int) error {
		if call == 1 {
			return errorOf(ErrorOutput, "synthetic malformed output")
		}
		return nil
	}}
	worker, err := NewWorker(database, generator, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, time.Now, Schedule{Immediate: true})
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if generator.calls != 28 {
		t.Fatalf("generator calls = %d, want one failed and 27 successful jobs", generator.calls)
	}
	stats, err := database.ProcessingQueueStats(ctx, worker.operation, time.Now())
	if err != nil || stats.Retrying != 1 || stats.Running != 0 {
		t.Fatalf("processing stats = %#v, err=%v", stats, err)
	}
}

func TestEndpointFailureOpensCircuitAndStopsQueue(t *testing.T) {
	ctx := context.Background()
	database := fixtureProcessingStore(t, ctx)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	generator := &sequenceGenerator{fail: func(int) error { return errorOf(ErrorTransient, "synthetic endpoint outage") }}
	worker, err := NewWorker(database, generator, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true})
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if generator.calls != 1 || !worker.circuitUntil.After(now) {
		t.Fatalf("calls/circuit = %d/%s", generator.calls, worker.circuitUntil)
	}
	worker.processAvailable(ctx)
	if generator.calls != 1 {
		t.Fatalf("open circuit made %d calls, want 1", generator.calls)
	}
}

func TestPrivacyFailureBecomesNeedsReviewAfterThreeAttempts(t *testing.T) {
	ctx := context.Background()
	database := singleIncidentProcessingStore(t, ctx)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	generator := &sequenceGenerator{fail: func(int) error { return errorOf(ErrorPrivacy, "synthetic privacy uncertainty") }}
	worker, err := NewWorker(database, generator, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true})
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)
	stats, err := database.ProcessingQueueStats(ctx, worker.operation, now)
	if err != nil || stats.NeedsReview != 1 || generator.calls != 3 {
		t.Fatalf("processing stats/calls = %#v/%d, err=%v", stats, generator.calls, err)
	}
}

func TestRetrySchedulesAndJitterBounds(t *testing.T) {
	if transientRetryDelay(1) != 30*time.Second || transientRetryDelay(99) != 2*time.Hour {
		t.Fatal("transient retry schedule is incorrect")
	}
	if configurationRetryDelay(1) != 10*time.Minute || contentRetryDelay(3) != time.Hour {
		t.Fatal("configuration or content retry schedule is incorrect")
	}
	base := 10 * time.Minute
	value := jitter(base, 42, 3)
	if value < 8*time.Minute || value > 12*time.Minute {
		t.Fatalf("jitter = %s, outside 20%% bound", value)
	}
}

func TestWorkerOnlyStartsJobsInsideProcessingWindow(t *testing.T) {
	ctx := context.Background()
	database := singleIncidentProcessingStore(t, ctx)
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, location)
	generator := &sequenceGenerator{fail: func(int) error { return nil }}
	schedule := Schedule{Location: location, Start: 3 * time.Hour, End: 8 * time.Hour}
	worker, err := NewWorker(database, generator, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, schedule)
	if err != nil {
		t.Fatal(err)
	}

	worker.processAvailable(ctx)
	if generator.calls != 0 {
		t.Fatalf("daytime processing calls = %d, want 0", generator.calls)
	}

	now = time.Date(2026, time.August, 24, 3, 0, 0, 0, location)
	worker.processAvailable(ctx)
	if generator.calls != 1 {
		t.Fatalf("overnight processing calls = %d, want 1", generator.calls)
	}
}

func TestScheduleSupportsOvernightWindowsAndImmediateMode(t *testing.T) {
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	schedule := Schedule{Location: location, Start: 22 * time.Hour, End: 6 * time.Hour}
	for _, test := range []struct {
		hour int
		want bool
	}{
		{hour: 21, want: false},
		{hour: 22, want: true},
		{hour: 5, want: true},
		{hour: 6, want: false},
	} {
		value := time.Date(2026, time.August, 23, test.hour, 0, 0, 0, location)
		if got := schedule.Allows(value); got != test.want {
			t.Errorf("Allows(%02d:00) = %t, want %t", test.hour, got, test.want)
		}
	}
	if !(Schedule{Immediate: true}).Allows(time.Time{}) {
		t.Fatal("immediate schedule blocked processing")
	}
}

func fixtureProcessingStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "processing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatal(err)
	}
	return database
}

func singleIncidentProcessingStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "single.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.UTC)
	documents := []domain.SourceDocument{{
		ExternalID: "one", SourceURL: "https://fixture.invalid/one", Title: "One",
		PublishedAt: now, FeedFingerprint: "feed", SourceHash: "source",
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel", BodyDE: "Text", ContentHash: "content"}},
	}}
	if err := database.UpsertDocuments(ctx, documents, now); err != nil {
		t.Fatal(err)
	}
	return database
}
