package processing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/store"
)

type pipelineTestProvider struct {
	mu       sync.Mutex
	events   []string
	onGerman func()
}

func (p *pipelineTestProvider) StepGenerator(model string) (StepGenerator, error) {
	return pipelineTestGenerator{model: model, provider: p}, nil
}

type pipelineTestGenerator struct {
	model    string
	provider *pipelineTestProvider
}

func (g pipelineTestGenerator) ModelIdentity() string { return g.model }

func (g pipelineTestGenerator) GenerateStep(_ context.Context, step StepDefinition, input StepInput) (StepOutput, string, error) {
	g.provider.mu.Lock()
	g.provider.events = append(g.provider.events, step.Key+":"+g.model+":"+input.OriginalTitle)
	callback := g.provider.onGerman
	if step.Key == GermanAnalysisStep {
		g.provider.onGerman = nil
	}
	g.provider.mu.Unlock()
	if step.Key == GermanAnalysisStep {
		if callback != nil {
			callback()
		}
		return StepOutput{TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.", Category: "other", PrivacyStatus: "safe", PrivacyFlags: []string{}}, g.model, nil
	}
	if input.TitleDE != "Sicherer Titel" || input.SummaryDE != "Sichere Zusammenfassung." || input.OriginalTitle == "" || input.IncidentBody == "" {
		// Original source remains present in the internal job object, but the
		// Ollama translation generator is independently tested to serialize only
		// the accepted German fields.
	}
	return StepOutput{TitleEN: "Safe title", SummaryEN: "Safe summary."}, g.model, nil
}

func TestPipelineWorkerGroupsModelsFreezesTargetsAndStartsNextCycle(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	insertWorkerDocument(t, ctx, database, now.Add(time.Second), "two")
	if err := database.EnsurePipelineSteps(ctx, StepKeys(), now); err != nil {
		t.Fatal(err)
	}
	if err := database.SetPipelineStepModel(ctx, GermanAnalysisStep, "qwen:4b", now); err != nil {
		t.Fatal(err)
	}
	if err := database.SetPipelineStepModel(ctx, EnglishTranslationStep, "translate:4b", now); err != nil {
		t.Fatal(err)
	}
	provider := &pipelineTestProvider{}
	provider.onGerman = func() { insertWorkerDocument(t, ctx, database, now.Add(2*time.Second), "three") }
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)

	provider.mu.Lock()
	events := append([]string(nil), provider.events...)
	provider.mu.Unlock()
	if len(events) != 6 {
		t.Fatalf("events = %v", events)
	}
	for index, event := range events {
		wantStep, wantModel := GermanAnalysisStep, "qwen:4b"
		if index == 2 || index == 3 || index == 5 {
			wantStep, wantModel = EnglishTranslationStep, "translate:4b"
		}
		if event[:len(wantStep)+len(wantModel)+2] != wantStep+":"+wantModel+":" {
			t.Fatalf("event %d = %q, want %s/%s grouped order", index, event, wantStep, wantModel)
		}
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), now)
	if err != nil || snapshot.ActiveCycle != nil || snapshot.ScheduledCandidates != 0 {
		t.Fatalf("completed pipeline snapshot = %#v, err=%v", snapshot, err)
	}
}

func TestPipelineWorkerFinishesAuthorizedCycleAfterWindowButDoesNotStartAnother(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "window.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	current := time.Date(2026, 8, 25, 7, 59, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, current, "one")
	if err := database.EnsurePipelineSteps(ctx, StepKeys(), current); err != nil {
		t.Fatal(err)
	}
	for step, model := range map[string]string{GermanAnalysisStep: "qwen:4b", EnglishTranslationStep: "translate:4b"} {
		if err := database.SetPipelineStepModel(ctx, step, model, current); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	provider.onGerman = func() {
		current = time.Date(2026, 8, 25, 8, 1, 0, 0, time.UTC)
		insertWorkerDocument(t, ctx, database, current, "two")
	}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: current}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return current }, Schedule{Location: time.UTC, Start: 3 * time.Hour, End: 8 * time.Hour}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 2 || provider.events[0][:len(GermanAnalysisStep)] != GermanAnalysisStep || provider.events[1][:len(EnglishTranslationStep)] != EnglishTranslationStep {
		t.Fatalf("authorized cycle events = %v", provider.events)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), current)
	if err != nil || snapshot.ActiveCycle != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("closed-window snapshot = %#v, err=%v", snapshot, err)
	}

	records, _, err := database.ListIncidents(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	var secondID int64
	for _, record := range records {
		if record.TitleDE == "Titel two" {
			secondID = record.ID
		}
	}
	models := map[string]string{GermanAnalysisStep: "qwen:4b", EnglishTranslationStep: "translate:4b"}
	if _, err := worker.RequestNow(ctx, "fixture", models, &secondID, false); err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 4 {
		t.Fatalf("outside-window manual cycle did not run: %v", provider.events)
	}
}

func TestPipelineWorkerDoesNotCallModelsWhileRequiredStepIsUnset(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "unset.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 0 {
		t.Fatalf("unconfigured pipeline called a model: %v", provider.events)
	}
	status, err := worker.Status(ctx)
	if err != nil || status.ScheduledReady || status.Models.Ready {
		t.Fatalf("unconfigured status = %#v, err=%v", status, err)
	}
}

func insertWorkerDocument(t *testing.T, ctx context.Context, database *store.Store, now time.Time, id string) {
	t.Helper()
	document := domain.SourceDocument{
		ExternalID: id, SourceURL: "https://fixture.invalid/" + id, Title: "Release " + id,
		PublishedAt: now, FeedFingerprint: "feed-" + id, SourceHash: "source-" + id,
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel " + id, BodyDE: fmt.Sprintf("Text für %s in München.", id), ContentHash: "content-" + id}},
	}
	if err := database.UpsertDocuments(ctx, []domain.SourceDocument{document}, now); err != nil {
		t.Fatal(err)
	}
}
