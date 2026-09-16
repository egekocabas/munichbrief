package processing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func newTranslationBatchFixture(t *testing.T, incidents int) (*PipelineWorker, *store.Store, *pipelineTestProvider, *time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC().Add(time.Minute)
	for i := range incidents {
		insertWorkerDocument(t, ctx, database, now, fmt.Sprintf("batch-%d", i))
	}
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"A", "B", "C", "base"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RequestNow(ctx, "fixture", map[string]string{IncidentMetadataStep: "base", GermanPresentationStep: "base"}, nil, false); err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	provider.events = nil
	return worker, database, provider, &now
}

func queueBatchLanguage(t *testing.T, worker *PipelineWorker, language, model string, want int) {
	t.Helper()
	count, err := worker.RequestPostProcessing(context.Background(), PostProcessingRequest{ProcessorKey: TranslationModelStep, ScopeKeys: []string{language}, Model: model})
	if err != nil || count != want {
		t.Fatalf("queue %s/%s = %d/%v, want %d", language, model, count, err, want)
	}
}

func batchModels(provider *pipelineTestProvider) []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	var result []string
	for _, event := range provider.events {
		result = append(result, strings.Split(event, ":")[1])
	}
	return result
}

func TestTranslationBatchesReduceModelSwitchesAcrossLanguages(t *testing.T) {
	worker, _, provider, _ := newTranslationBatchFixture(t, 2)
	for _, route := range [][2]string{{"en", "A"}, {"tr", "B"}, {"hr", "A"}, {"it", "C"}} {
		queueBatchLanguage(t, worker, route[0], route[1], 2)
	}
	worker.processAvailable(context.Background())
	want := []string{"A", "A", "A", "A", "B", "B", "C", "C"}
	if got := batchModels(provider); !slices.Equal(got, want) {
		t.Fatalf("model calls = %v, want %v", got, want)
	}
	// The same FIFO language sequence was A,A,B,B,A,A,C,C: three model
	// transitions. Batching completes the same eight jobs with two transitions.
	if worker.translationBatch.model != "" {
		t.Fatal("empty eligible queue retained batch")
	}
}

func TestTranslationBatchLimitAndLateCompetitor(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late=%t", late), func(t *testing.T) {
			worker, _, provider, _ := newTranslationBatchFixture(t, 52)
			queueBatchLanguage(t, worker, "en", "A", 52)
			if !late {
				queueBatchLanguage(t, worker, "tr", "B", 52)
			} else {
				calls := 0
				provider.generateContext = func(context.Context, StepDefinition, StepInput) (StepOutput, bool, error) {
					calls++
					if calls == 51 {
						queueBatchLanguage(t, worker, "tr", "B", 52)
					}
					return StepOutput{}, false, nil
				}
			}
			worker.processAvailable(context.Background())
			first := 50
			if late {
				first = 51
			}
			var want []string
			for range first {
				want = append(want, "A")
			}
			for range 50 {
				want = append(want, "B")
			}
			for range 52 - first {
				want = append(want, "A")
			}
			want = append(want, "B", "B")
			if got := batchModels(provider); !slices.Equal(got, want) {
				t.Fatalf("model calls = %v, want %v", got, want)
			}
		})
	}
}

func TestManualTranslationPreemptsAutomaticModelBatch(t *testing.T) {
	worker, database, provider, now := newTranslationBatchFixture(t, 3)
	plans, err := worker.postProcessors.Plans(TranslationModelStep, []string{"en"}, "A")
	if err != nil {
		t.Fatal(err)
	}
	if count, err := database.QueuePostProcessingForAll(context.Background(), "fixture", plans, false, *now); err != nil || count != 3 {
		t.Fatalf("automatic queue=%d/%v", count, err)
	}
	provider.onTranslation = func() { queueBatchLanguage(t, worker, "tr", "B", 3) }
	worker.processAvailable(context.Background())
	want := []string{"A", "B", "B", "B", "A", "A"}
	if got := batchModels(provider); !slices.Equal(got, want) {
		t.Fatalf("model calls=%v, want %v", got, want)
	}
}

func TestTranslationBatchUsesLastInvokedModelAndInvalidatesOldBatch(t *testing.T) {
	worker, _, provider, _ := newTranslationBatchFixture(t, 1)
	queueBatchLanguage(t, worker, "en", "A", 1)
	queueBatchLanguage(t, worker, "tr", "B", 1)
	worker.translationBatch = translationModelBatch{model: "A", attempts: 49}
	// Simulate an intervening canonical/verification call through the same hook.
	worker.recordModelInvocation(context.Background(), "B")
	if worker.translationBatch.model != "" {
		t.Fatal("intervening model retained old batch")
	}
	worker.processAvailable(context.Background())
	if got := batchModels(provider); !slices.Equal(got, []string{"B", "A"}) {
		t.Fatalf("model calls=%v", got)
	}
}

func TestTranslationBatchCountsFailedAttemptsAndEligibleRetries(t *testing.T) {
	worker, _, provider, now := newTranslationBatchFixture(t, 51)
	queueBatchLanguage(t, worker, "en", "A", 51)
	queueBatchLanguage(t, worker, "tr", "B", 51)
	provider.fail = func(step string, call int) error {
		if step == EnglishTranslationStep && call == 1 {
			return errorOf(ErrorOutput, "fixture invalid output")
		}
		return nil
	}
	worker.processAvailable(context.Background())
	got := batchModels(provider)
	if len(got) != 102 || got[49] != "A" || got[50] != "B" || got[100] != "A" || got[101] != "B" {
		t.Fatalf("failure batch calls=%v", got)
	}
	*now = now.Add(time.Hour)
	worker.processAvailable(context.Background())
	got = batchModels(provider)
	if len(got) != 103 || got[102] != "A" {
		t.Fatalf("eligible retry calls=%v", got)
	}
}

func TestTranslationBatchStateAcrossPollingCancellationAndRestart(t *testing.T) {
	worker, database, provider, now := newTranslationBatchFixture(t, 2)
	queueBatchLanguage(t, worker, "en", "A", 2)
	queueBatchLanguage(t, worker, "tr", "B", 2)
	provider.fail = func(step string, call int) error {
		if step == EnglishTranslationStep && call == 1 {
			return errorOf(ErrorTransient, "fixture temporary failure")
		}
		return nil
	}
	worker.processAvailable(context.Background())
	if worker.translationBatch.model != "A" || worker.translationBatch.attempts != 1 {
		t.Fatalf("polling lost batch: %#v", worker.translationBatch)
	}
	// The blocked A model does not prevent B from running on the next poll.
	worker.processAvailable(context.Background())
	if got := batchModels(provider); !slices.Equal(got, []string{"A", "B", "B"}) {
		t.Fatalf("blocked model calls=%v", got)
	}
	*now = now.Add(time.Hour)
	restarted, err := NewPipelineWorker(database, provider, worker.catalog, worker.postProcessors, nil, worker.logger, time.Second, worker.clock, worker.schedule, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if restarted.translationBatch.model != "" || restarted.lastInvokedModel != "" {
		t.Fatal("restart retained in-memory hints")
	}
	restarted.processAvailable(context.Background())
	if got := batchModels(provider); !slices.Equal(got, []string{"A", "B", "B", "A", "A"}) {
		t.Fatalf("restart calls=%v", got)
	}
	queueBatchLanguage(t, restarted, "en", "A", 2)
	provider.onTranslation = func() {
		if _, err := restarted.CancelAll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The English-only callback fires on the next English invocation.
	restarted.processAvailable(context.Background())
	if restarted.translationBatch.model != "" || restarted.lastInvokedModel != "" {
		t.Fatal("cancel retained model hints")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	restarted.recordModelInvocation(canceled, "A")
	if restarted.lastInvokedModel != "" {
		t.Fatal("canceled request restored stale hint")
	}
}

func TestTranslationSingleModelContinuesPastBatchLimit(t *testing.T) {
	worker, _, provider, _ := newTranslationBatchFixture(t, 52)
	queueBatchLanguage(t, worker, "en", "A", 52)
	worker.processAvailable(context.Background())
	got := batchModels(provider)
	if len(got) != 52 {
		t.Fatalf("calls=%d, want 52", len(got))
	}
	for _, model := range got {
		if model != "A" {
			t.Fatalf("unexpected model %s", model)
		}
	}
}

func TestVerificationPrecedesTranslationsAndUpdatesModelHint(t *testing.T) {
	worker, _, provider, _ := newTranslationBatchFixture(t, 2)
	queueBatchLanguage(t, worker, "en", "A", 2)
	queueBatchLanguage(t, worker, "tr", "B", 2)
	provider.onTranslation = func() {
		count, err := worker.RequestPostProcessing(context.Background(), PostProcessingRequest{ProcessorKey: CategoryVerificationStep, Model: "B"})
		if err != nil || count != 2 {
			t.Fatalf("queue verification=%d/%v", count, err)
		}
	}
	worker.processAvailable(context.Background())
	want := []string{"A", "B", "B", "B", "B", "A"}
	if got := batchModels(provider); !slices.Equal(got, want) {
		t.Fatalf("models=%v, want %v", got, want)
	}
	for _, index := range []int{1, 2} {
		if !strings.HasPrefix(provider.events[index], CategoryVerificationStep+":") {
			t.Fatalf("verification did not precede translations: %v", provider.events)
		}
	}
}

func TestTranslationBatchStatusPreviewsRunningAndNextModels(t *testing.T) {
	for _, atLimit := range []bool{false, true} {
		t.Run(fmt.Sprintf("limit=%t", atLimit), func(t *testing.T) {
			worker, _, provider, _ := newTranslationBatchFixture(t, 2)
			queueBatchLanguage(t, worker, "en", "A", 2)
			queueBatchLanguage(t, worker, "tr", "B", 2)
			ctx := context.Background()
			initial, err := worker.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if initial.Running != nil || initial.TranslationBatch.Limit != 50 || initial.TranslationBatch.NextModel != "A" {
				t.Fatalf("initial status=%#v", initial.TranslationBatch)
			}
			if atLimit {
				worker.translationBatch = translationModelBatch{model: "A", attempts: 49}
			}
			observed := false
			provider.onTranslation = func() {
				status, err := worker.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				observed = true
				if status.Running == nil || status.Running.Key != "translation/en" || status.Running.Model != "A" || status.Running.IncidentID == 0 {
					t.Fatalf("running=%#v", status.Running)
				}
				wantCount, wantNext := 1, "A"
				if atLimit {
					wantCount, wantNext = 50, "B"
				}
				batch := status.TranslationBatch
				if batch.Model != "A" || batch.Attempts != wantCount || batch.NextModel != wantNext || batch.NextSwitchModel != "B" || batch.NextRequestKind != "manual" {
					t.Fatalf("batch=%#v", batch)
				}
			}
			worker.processAvailable(ctx)
			if !observed {
				t.Fatal("running callback not observed")
			}
			final, err := worker.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if final.Running != nil || final.TranslationBatch.Model != "" || final.TranslationBatch.Attempts != 0 || final.TranslationBatch.NextModel != "" {
				t.Fatalf("idle status=%#v", final)
			}
		})
	}
}

func TestTranslationBatchPreviewSkipsRemovedModels(t *testing.T) {
	worker, _, provider, now := newTranslationBatchFixture(t, 1)
	queueBatchLanguage(t, worker, "en", "A", 1)
	queueBatchLanguage(t, worker, "tr", "B", 1)
	queueBatchLanguage(t, worker, "it", "C", 1)
	worker.translationBatch = translationModelBatch{model: "A", attempts: 50}
	// B was installed when queued, but has since been removed.
	worker.catalog = testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"A", "C", "base"}, CheckedAt: *now}}
	before := worker.translationBatch
	status, err := worker.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.TranslationBatch.NextModel != "A" || status.TranslationBatch.NextSwitchModel != "C" {
		t.Fatalf("preview includes removed model: %#v", status.TranslationBatch)
	}
	if worker.translationBatch != before || len(worker.blockedModels(TranslationModelStep, *now)) != 0 {
		t.Fatal("preview changed batch or circuit state")
	}
	worker.processAvailable(context.Background())
	if got := batchModels(provider); !slices.Equal(got, []string{"A", "C"}) {
		t.Fatalf("model invocations = %v, want A,C", got)
	}
}
