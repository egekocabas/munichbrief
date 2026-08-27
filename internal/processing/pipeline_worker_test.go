package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/store"
)

type pipelineTestProvider struct {
	mu            sync.Mutex
	events        []string
	onGerman      func()
	onTranslation func()
	calls         map[string]int
	fail          func(string, int) error
}

type testModelCatalog struct{ snapshot ModelCatalogSnapshot }

func (c testModelCatalog) Snapshot() ModelCatalogSnapshot { return c.snapshot }

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
	g.provider.events = append(g.provider.events, step.Key+":"+g.model+":"+input.Value("original_title"))
	if g.provider.calls == nil {
		g.provider.calls = make(map[string]int)
	}
	g.provider.calls[step.Key]++
	call := g.provider.calls[step.Key]
	var failure error
	if g.provider.fail != nil {
		failure = g.provider.fail(step.Key, call)
	}
	callback := g.provider.onGerman
	if step.Key == GermanPresentationStep {
		g.provider.onGerman = nil
	}
	translationCallback := g.provider.onTranslation
	if step.Key == EnglishTranslationStep {
		g.provider.onTranslation = nil
	}
	g.provider.mu.Unlock()
	if failure != nil {
		return StepOutput{}, "", failure
	}
	if step.Key == IncidentMetadataStep {
		return StepOutput{
			Category: "other", ReportKind: "incident", PublicAssistanceStatus: "not_requested", PublicAssistanceTypes: []string{},
		}, g.model, nil
	}
	if step.Key == GermanPresentationStep {
		if callback != nil {
			callback()
		}
		return StepOutput{TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.", PrivacyStatus: "safe", PrivacyFlags: []string{}}, g.model, nil
	}
	if input.Value("title_de") != "Sicherer Titel" || input.Value("summary_de") != "Sichere Zusammenfassung." {
		return StepOutput{}, "", fmt.Errorf("translation received incomplete German presentation")
	}
	if translationCallback != nil {
		translationCallback()
	}
	return StepOutput{TitleEN: "Safe title", SummaryEN: "Safe summary."}, g.model, nil
}

func (p *pipelineTestProvider) callCount(step string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[step]
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
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
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
	if len(events) != 9 {
		t.Fatalf("events = %v", events)
	}
	want := []struct{ step, model string }{
		{IncidentMetadataStep, "qwen:4b"}, {IncidentMetadataStep, "qwen:4b"},
		{GermanPresentationStep, "qwen:4b"}, {GermanPresentationStep, "qwen:4b"},
		{IncidentMetadataStep, "qwen:4b"}, {GermanPresentationStep, "qwen:4b"},
		{EnglishTranslationStep, "translate:4b"}, {EnglishTranslationStep, "translate:4b"}, {EnglishTranslationStep, "translate:4b"},
	}
	for index, event := range events {
		wantStep, wantModel := want[index].step, want[index].model
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
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), current); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
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
	if len(provider.events) != 2 || provider.events[0][:len(IncidentMetadataStep)] != IncidentMetadataStep || provider.events[1][:len(GermanPresentationStep)] != GermanPresentationStep {
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
	models := pipelineTestModels()
	if _, err := worker.RequestNow(ctx, "fixture", models, &secondID, false); err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 5 {
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

func TestMissingTranslationModelDoesNotBlockCanonicalGerman(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "canonical-without-translation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{IncidentMetadataStep, GermanPresentationStep} {
		if err := database.SetPipelineStepModel(ctx, step, "qwen:4b", now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != 1 || provider.callCount(EnglishTranslationStep) != 0 {
		t.Fatalf("calls with translation unset = metadata:%d german:%d translation:%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), provider.callCount(EnglishTranslationStep))
	}
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatal(err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true}); err != nil {
		t.Fatalf("canonical German was not public: %v", err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{PromptVersion: PipelineVersion, Language: "en", PublicOnly: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("English was visible without a translation: %v", err)
	}
	status, err := worker.ModelStatus(ctx)
	if err != nil || !status.Ready || status.TranslationReady {
		t.Fatalf("split model readiness = %#v/%v", status, err)
	}
}

func TestCanonicalWorkPreemptsTranslationsAtJobBoundaries(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "canonical-preemption.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	provider.onTranslation = func() { insertWorkerDocument(t, ctx, database, now.Add(time.Second), "two") }
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)

	provider.mu.Lock()
	events := append([]string(nil), provider.events...)
	provider.mu.Unlock()
	want := []string{IncidentMetadataStep, GermanPresentationStep, EnglishTranslationStep, IncidentMetadataStep, GermanPresentationStep, EnglishTranslationStep}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for index, step := range want {
		if !strings.HasPrefix(events[index], step+":") {
			t.Fatalf("event %d = %q, want %s", index, events[index], step)
		}
	}
}

func TestTranslationFailureDoesNotChangeCanonicalCompletion(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "translation-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == EnglishTranslationStep {
			return errorOf(ErrorOutput, "synthetic malformed translation")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("incidents = %d, err=%v", len(records), err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true}); err != nil {
		t.Fatalf("canonical German was not public after translation failure: %v", err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{PromptVersion: PipelineVersion, Language: "en", PublicOnly: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed English translation became public: %v", err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), now)
	if err != nil || len(snapshot.Translations) != 1 || snapshot.Translations[0].NeedsReview != 1 {
		t.Fatalf("translation failure snapshot = %#v, err=%v", snapshot.Translations, err)
	}
}

func TestPipelineWorkerRetriesPrivacyFailurePerStepThenRequiresReview(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "privacy-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "privacy")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == GermanPresentationStep {
			return errorOf(ErrorPrivacy, "synthetic privacy uncertainty")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != contentMaxAttempts || provider.callCount(EnglishTranslationStep) != 0 {
		t.Fatalf("step calls = metadata:%d german:%d translation:%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), provider.callCount(EnglishTranslationStep))
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveCycle != nil || snapshot.ScheduledCandidates != 0 || len(snapshot.Steps) != 2 || snapshot.Steps[1].NeedsReview != 1 {
		t.Fatalf("privacy retry snapshot = %#v", snapshot)
	}
}

func TestPipelineWorkerFailsClosedWhenMetadataRemainsInvalid(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "metadata-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "metadata")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == IncidentMetadataStep {
			return errorOf(ErrorOutput, "synthetic malformed metadata")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	if provider.callCount(IncidentMetadataStep) != contentMaxAttempts || provider.callCount(GermanPresentationStep) != 0 || provider.callCount(EnglishTranslationStep) != 0 {
		t.Fatalf("step calls = metadata:%d german:%d translation:%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), provider.callCount(EnglishTranslationStep))
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveCycle != nil || len(snapshot.Steps) != 2 || snapshot.Steps[0].NeedsReview != 1 {
		t.Fatalf("metadata fail-closed snapshot = %#v", snapshot)
	}
}

func TestPipelineWorkerTransientFailureOpensPerStepModelCircuit(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "circuit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "circuit")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == GermanPresentationStep {
			return errorOf(ErrorTransient, "synthetic endpoint outage")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != 1 || !worker.circuitOpen(GermanPresentationStep, "qwen:4b", now) {
		t.Fatalf("first transient failure calls/circuit = metadata:%d german:%d/%t", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), worker.circuitOpen(GermanPresentationStep, "qwen:4b", now))
	}
	worker.processAvailable(ctx)
	if provider.callCount(GermanPresentationStep) != 1 {
		t.Fatalf("open circuit made %d model calls", provider.callCount(GermanPresentationStep))
	}
	now = now.Add(time.Minute)
	worker.processAvailable(ctx)
	if provider.callCount(GermanPresentationStep) != 2 {
		t.Fatalf("expired circuit calls = %d, want 2", provider.callCount(GermanPresentationStep))
	}
}

func TestTranslationTransientFailureBacksOffFailedModel(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "translation-circuit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == EnglishTranslationStep {
			return errorOf(ErrorTransient, "synthetic translation endpoint outage")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 1 || len(worker.blockedTranslationModels(now)) != 1 {
		t.Fatalf("translation circuit = calls:%d blocked:%v", provider.callCount(EnglishTranslationStep), worker.blockedTranslationModels(now))
	}
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 1 {
		t.Fatalf("open translation circuit made %d model calls", provider.callCount(EnglishTranslationStep))
	}
	now = now.Add(time.Minute)
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 2 {
		t.Fatalf("expired translation circuit calls = %d, want 2", provider.callCount(EnglishTranslationStep))
	}
}

func pipelineTestModels() map[string]string {
	return map[string]string{
		IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b", TranslationModelStep: "translate:4b",
	}
}

func insertWorkerDocument(t *testing.T, ctx context.Context, database *store.Store, now time.Time, id string) {
	t.Helper()
	document := domain.SourceDocument{
		ExternalID: id, SourceURL: "https://fixture.invalid/" + id, Title: "Release " + id,
		PublishedAt: now, FeedFingerprint: "feed-" + id, SourceHash: "source-" + id,
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel " + id, BodyDE: fmt.Sprintf("Text für %s in München.", id), ContentHash: "content-" + id}},
	}
	// created_at must be newer than the real migration-time v2 cutover; the
	// document publication time still follows the deterministic worker clock.
	if err := database.UpsertDocuments(ctx, []domain.SourceDocument{document}, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
