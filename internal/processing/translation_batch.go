package processing

import (
	"context"

	"github.com/egekocabas/munichbrief/internal/store"
)

const translationModelBatchLimit = 50

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
