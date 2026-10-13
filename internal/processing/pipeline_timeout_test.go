package processing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestPipelineWorkerRequestRetryPolicy(t *testing.T) {
	for _, step := range []string{IncidentMetadataStep, GermanPresentationStep, TranslationStepKey("it")} {
		for _, policy := range []struct {
			name           string
			kind           ErrorKind
			outageAttempts int
		}{
			{name: "timeout", kind: ErrorTimeout},
			{name: "transient", kind: ErrorTransient},
			{name: "timeout_after_outage", kind: ErrorTimeout, outageAttempts: 4},
		} {
			t.Run(step+"/"+policy.name, func(t *testing.T) {
				kind := policy.kind
				ctx := context.Background()
				database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "retry-policy.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()
				// Exercise scheduled rediscovery after the migration's scope cutover.
				now := time.Now().UTC().Add(time.Minute)
				insertWorkerDocument(t, ctx, database, now, "retry-policy")
				if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
					t.Fatal(err)
				}
				for key, model := range pipelineTestModels() {
					if err := database.SetPipelineStepModel(ctx, key, model, now); err != nil {
						t.Fatal(err)
					}
				}
				provider := &pipelineTestProvider{fail: func(key string, call int) error {
					if key == step && call <= policy.outageAttempts {
						return errorOf(ErrorTransient, "synthetic prior outage")
					}
					if key == step && (kind == ErrorTimeout || call <= 4) {
						return errorOf(kind, "synthetic request failure")
					}
					return nil
				}}
				worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				configureAllTestTranslationRoutes(t, ctx, worker, "translate:4b")
				worker.processAvailable(ctx)
				if kind == ErrorTimeout && policy.outageAttempts == 0 && step == TranslationStepKey("it") {
					// A request-specific timeout must not block healthy languages on
					// the same model, even before the next polling tick.
					for _, translation := range RegisteredTranslations() {
						if calls := provider.callCount(translation.Step.Key); calls != 1 {
							t.Fatalf("%s calls after first timeout = %d, want 1", translation.Step.Key, calls)
						}
					}
				}
				for range 6 {
					now = now.Add(10 * time.Minute)
					worker.processAvailable(ctx)
				}
				wantAttempts, wantStatus, wantFailure := 5, "succeeded", ""
				if kind == ErrorTimeout {
					wantAttempts, wantStatus, wantFailure = contentMaxAttempts, "needs_review", string(ErrorTimeout)
					if policy.outageAttempts > 0 {
						wantAttempts = policy.outageAttempts + 1
					}
				}
				if calls := provider.callCount(step); calls != wantAttempts {
					t.Fatalf("calls = %d, want %d", calls, wantAttempts)
				}
				history, err := database.ListPipelineHistory(ctx, "fixture", 100, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				var jobs int
				for _, entry := range history.Entries {
					if entry.ExecutionKey == step {
						jobs++
						if entry.Status != wantStatus || entry.AttemptCount != wantAttempts || entry.FailureKind != wantFailure {
							t.Fatalf("job after retries = %#v", entry)
						}
					}
				}
				if jobs != 1 {
					t.Fatalf("scheduled discovery created %d jobs for unchanged work, want 1", jobs)
				}
				status, err := worker.Status(ctx)
				if err != nil || status.Running != nil || status.Queue.ActiveCycle != nil {
					t.Fatalf("worker did not become idle: %#v, err=%v", status, err)
				}
				if kind != ErrorTimeout || step != TranslationStepKey("it") {
					return
				}
				for _, stats := range status.Queue.PostProcessing {
					if stats.Pending != 0 || stats.Running != 0 || stats.Retrying != 0 {
						t.Fatalf("exhausted queue is still active: %#v", stats)
					}
				}
				records, _, err := database.ListIncidents(ctx, 1, 0)
				if err != nil || len(records) != 1 {
					t.Fatalf("incidents = %d, err=%v", len(records), err)
				}
				if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "de", PublicOnly: true}); err != nil {
					t.Fatalf("Italian timeout blocked German publication: %v", err)
				}
				if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "it", TranslationLanguage: "it", PublicOnly: true}); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("timed-out translation became public: %v", err)
				}
				provider.fail = nil
				queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: TranslationModelStep, ScopeKeys: []string{"it"}, IncidentID: &records[0].ID, Model: "translate:4b"})
				if err != nil || queued != 1 {
					t.Fatalf("explicit retry = %d, err=%v", queued, err)
				}
				worker.processAvailable(ctx)
				if calls := provider.callCount(step); calls != wantAttempts+1 {
					t.Fatalf("calls after explicit retry = %d", calls)
				}
				if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "it", TranslationLanguage: "it", PublicOnly: true}); err != nil {
					t.Fatalf("successful explicit retry did not publish: %v", err)
				}
			})
		}
	}
}
