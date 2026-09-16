package processing

import (
	"context"

	"github.com/egekocabas/munichbrief/internal/store"
)

const translationModelBatchLimit = 50

// PipelineExecutionStatus identifies the claimed request currently being handled.
type PipelineExecutionStatus struct {
	Key        string `json:"key"`
	Model      string `json:"model"`
	IncidentID int64  `json:"incident_id"`
}

// TranslationBatchStatus previews queued translations, not higher-priority
// canonical or verification work. The preview does not reserve any jobs.
type TranslationBatchStatus struct {
	Model           string `json:"model"`
	Attempts        int    `json:"attempts"`
	Limit           int    `json:"limit"`
	NextModel       string `json:"next_model"`
	NextScope       string `json:"next_scope"`
	NextRequestKind string `json:"next_request_kind"`
	NextSwitchModel string `json:"next_switch_model"`
}

func (w *PipelineWorker) translationBatchStatusLocked(ctx context.Context, allowScheduled, catalogAvailable bool) (TranslationBatchStatus, error) {
	status := TranslationBatchStatus{Model: w.translationBatch.model, Attempts: w.translationBatch.attempts, Limit: translationModelBatchLimit}
	if !catalogAvailable || !w.postProcessors.Ready(TranslationModelStep) {
		return status, nil
	}
	definition, found := w.postProcessors.Definition(TranslationModelStep)
	if !found {
		return status, nil
	}
	contracts := make(store.PostProcessingContract, len(definition.Scopes))
	for _, scope := range definition.Scopes {
		contracts[scope.Key] = store.PostProcessingScopeContract{PromptVersion: scope.Step.PromptVersion, InputKinds: scope.Step.InputKinds, OutputKinds: scope.Step.OutputKinds}
	}
	now := w.clock()
	options := store.PostProcessingClaimOptions{AllowScheduled: allowScheduled, BlockedModels: w.blockedModels(TranslationModelStep, now)}
	options.PreferredModel, options.YieldModel = w.translationBatch.modelHints(w.lastInvokedModel)
	next, found, err := w.repository.PeekPostProcessingJob(ctx, TranslationModelStep, contracts, options, now)
	if err != nil {
		return status, err
	}
	if found {
		status.NextModel, status.NextScope, status.NextRequestKind = next.ModelIdentity, next.ScopeKey, next.RequestKind
	}
	if status.Model != "" {
		options.PreferredModel, options.YieldModel = "", status.Model
		next, found, err = w.repository.PeekPostProcessingJob(ctx, TranslationModelStep, contracts, options, now)
		if err != nil {
			return status, err
		}
		if found && next.ModelIdentity != status.Model {
			status.NextSwitchModel = next.ModelIdentity
		}
	}
	return status, nil
}

type translationModelBatch struct {
	model    string
	attempts int
}

func (b translationModelBatch) modelHints(lastInvokedModel string) (preferred, yield string) {
	if b.model == "" {
		return lastInvokedModel, ""
	}
	if b.attempts >= translationModelBatchLimit {
		return "", b.model
	}
	return b.model, ""
}

// recordTranslationClaimLocked counts attempts, including retries and claims
// later deferred for an unavailable model. Call while holding executionMu.
func (w *PipelineWorker) recordTranslationClaimLocked(job store.PostProcessingJob, options store.PostProcessingClaimOptions) {
	previous := w.translationBatch
	if previous.model != job.ModelIdentity {
		reason := "eligible_model"
		if options.YieldModel != "" {
			reason = "batch_limit"
		} else if previous.model == "" && options.PreferredModel == job.ModelIdentity {
			reason = "last_invoked_model"
		}
		w.logger.Info("translation model batch started", "model", job.ModelIdentity,
			"previous_model", previous.model, "reason", reason,
			"previous_attempt_count", previous.attempts, "request_kind", job.RequestKind,
			"batch_limit", translationModelBatchLimit)
		w.translationBatch = translationModelBatch{model: job.ModelIdentity}
	}
	// Keep yielding once the budget is spent, even if the competing model only
	// becomes eligible after further jobs on this model have run.
	if w.translationBatch.attempts < translationModelBatchLimit {
		w.translationBatch.attempts++
	}
}

// recordModelInvocation is only a scheduling hint: other Ollama clients and
// memory pressure can change residency independently of this worker.
func (w *PipelineWorker) recordModelInvocation(ctx context.Context, model string) {
	w.executionMu.Lock()
	defer w.executionMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	w.lastInvokedModel = model
	if w.translationBatch.model != "" && w.translationBatch.model != model {
		w.translationBatch = translationModelBatch{}
	}
}
