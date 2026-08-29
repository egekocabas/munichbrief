package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PipelineVersion identifies the persisted semantics of the staged pipeline.
const PipelineVersion = "incident-pipeline-v2"

const PreviousPipelineVersion = "incident-pipeline-v1"

var ErrPipelineUnconfigured = errors.New("AI pipeline models are not configured")

// PipelineStepPlan freezes a registry step and selected model for one cycle.
type PipelineStepPlan struct {
	Key           string
	Order         int
	PromptVersion string
	Model         string
}

// StepSetting is the mutable preferred model for a registered step.
type StepSetting struct {
	StepKey        string    `json:"step_key"`
	PreferredModel string    `json:"preferred_model"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PipelineCycle is the persisted coordinator for an ordered set of step jobs.
type PipelineCycle struct {
	ID                        int64      `json:"id"`
	Kind                      string     `json:"kind"`
	Status                    string     `json:"status"`
	ActiveStep                int        `json:"active_step"`
	WindowAuthorized          bool       `json:"window_authorized"`
	StartedAt                 *time.Time `json:"started_at,omitempty"`
	CompletedAt               *time.Time `json:"completed_at,omitempty"`
	TranslationModel          string     `json:"translation_model,omitempty"`
	CategoryVerificationModel string     `json:"category_verification_model,omitempty"`
}

// PipelineJob is a claimed unit of work with its frozen input provenance.
type PipelineJob struct {
	ID                        int64
	CycleID                   int64
	CycleKind                 string
	CycleItemID               int64
	PresentationRunID         int64
	IncidentID                int64
	SourceHash                string
	StepKey                   string
	StepOrder                 int
	ModelIdentity             string
	PromptVersion             string
	InputHash                 string
	AttemptCount              int
	TranslationModel          string
	CategoryVerificationModel string
	InputValues               map[string]string
	// TitleDE and SummaryDE remain populated for compatibility with operational
	// callers while step execution consumes InputValues exclusively.
	TitleDE   string
	SummaryDE string
}

// TranslationPlan freezes one target language, prompt, and model for a job.
type TranslationPlan struct {
	Language      string
	PromptVersion string
	Model         string
}

// TranslationJob is an independently claimed translation of one accepted
// canonical German presentation.
type TranslationJob struct {
	ID                int64
	PresentationRunID int64
	IncidentID        int64
	Language          string
	RequestKind       string
	ModelIdentity     string
	PromptVersion     string
	InputHash         string
	AttemptCount      int
	TitleDE           string
	SummaryDE         string
}

// CategoryVerificationPlan freezes the focused verifier prompt and model.
type CategoryVerificationPlan struct {
	PromptVersion string
	Model         string
}

// CategoryVerificationJob checks one immutable canonical presentation category.
type CategoryVerificationJob struct {
	ID                int64
	PresentationRunID int64
	IncidentID        int64
	RequestKind       string
	InputCategory     string
	ModelIdentity     string
	PromptVersion     string
	InputHash         string
	AttemptCount      int
	TitleDE           string
	SummaryDE         string
}

// CategoryVerificationQueueStats summarizes the independent verifier queue.
type CategoryVerificationQueueStats struct {
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Retrying    int `json:"retrying"`
	NeedsReview int `json:"needs_review"`
	Failed      int `json:"failed"`
	Succeeded   int `json:"succeeded"`
	Corrected   int `json:"corrected"`
}

// TranslationQueueStats summarizes one target language independently from the
// canonical pipeline stage statistics.
type TranslationQueueStats struct {
	Language    string `json:"language"`
	Pending     int    `json:"pending"`
	Running     int    `json:"running"`
	Retrying    int    `json:"retrying"`
	NeedsReview int    `json:"needs_review"`
	Failed      int    `json:"failed"`
	Succeeded   int    `json:"succeeded"`
}

// PipelineValue is one validated presentation field produced by a step.
type PipelineValue struct {
	Kind  string
	Value string
}

// PipelineRequestResult reports newly queued work or an equivalent current cycle.
type PipelineRequestResult struct {
	CycleID   int64
	Requested int
	Current   int
}

// StepQueueStats summarizes bounded queue state for one registered step.
type StepQueueStats struct {
	StepKey                string        `json:"step_key"`
	Waiting                int           `json:"waiting"`
	ReadyAfterStage        int           `json:"ready_after_stage"`
	Queued                 int           `json:"queued"`
	Running                int           `json:"running"`
	Retrying               int           `json:"retrying"`
	NeedsReview            int           `json:"needs_review"`
	Failed                 int           `json:"failed"`
	Succeeded              int           `json:"succeeded"`
	AverageDuration        time.Duration `json:"-"`
	AverageDurationSeconds float64       `json:"average_duration_seconds"`
	LastSuccess            *time.Time    `json:"last_success,omitempty"`
}

// PipelineEvent is a sanitized recent transition for the review status view.
type PipelineEvent struct {
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	CycleID     int64     `json:"cycle_id"`
	IncidentID  int64     `json:"incident_id"`
	StepKey     string    `json:"step_key"`
	Language    string    `json:"language,omitempty"`
	Status      string    `json:"status"`
	FailureKind string    `json:"failure_kind,omitempty"`
}

// PipelineHistoryEntry is the review-facing persisted state of one canonical
// or independent post-processing job. It intentionally excludes incident text,
// generated values, and internal error messages.
type PipelineHistoryEntry struct {
	JobID         int64
	UpdatedAt     time.Time
	Kind          string
	CycleID       int64
	CycleKind     string
	CycleStatus   string
	IncidentID    int64
	StepKey       string
	Language      string
	RequestKind   string
	Status        string
	AttemptCount  int
	FailureKind   string
	ModelIdentity string
	PromptVersion string
}

// PipelineHistoryCursor identifies a stable boundary in reverse-chronological
// pipeline history.
type PipelineHistoryCursor struct {
	UpdatedAt time.Time
	Kind      string
	JobID     int64
}

// PipelineHistoryPage contains one bounded page and navigation availability.
type PipelineHistoryPage struct {
	Entries  []PipelineHistoryEntry
	HasNewer bool
	HasOlder bool
}

// AdvanceResult describes whether a cycle changed stage, completed, or must wait.
type AdvanceResult struct {
	Advanced  bool
	Completed bool
	Waiting   bool
	RetryAt   *time.Time
}

func validateStepPlans(steps []PipelineStepPlan) error {
	if len(steps) == 0 {
		return errors.New("at least one pipeline step is required")
	}
	seen := make(map[string]bool, len(steps))
	for index, step := range steps {
		if strings.TrimSpace(step.Key) == "" || strings.TrimSpace(step.PromptVersion) == "" || strings.TrimSpace(step.Model) == "" {
			return errors.New("pipeline step key, prompt version, and model are required")
		}
		if step.Order != index || seen[step.Key] {
			return errors.New("pipeline steps must have unique keys and contiguous order")
		}
		seen[step.Key] = true
	}
	return nil
}

// HashPipelineInput returns an unambiguous hash of the ordered step inputs.
func HashPipelineInput(values ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return fmt.Sprintf("%x", hash[:])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// PipelineSnapshot is an operational view of active, queued, and candidate work.
type PipelineSnapshot struct {
	ScheduledAfter            *time.Time                     `json:"scheduled_after,omitempty"`
	CategoryVerificationAfter *time.Time                     `json:"category_verification_after,omitempty"`
	ActiveCycle               *PipelineCycle                 `json:"active_cycle,omitempty"`
	ActiveStepKey             string                         `json:"active_step_key"`
	ActiveModel               string                         `json:"active_model"`
	CurrentIncidentID         int64                          `json:"current_incident_id,omitempty"`
	ActiveStepCompleted       int                            `json:"active_step_completed"`
	ActiveStepTotal           int                            `json:"active_step_total"`
	CycleCompleted            int                            `json:"cycle_completed"`
	CycleTotal                int                            `json:"cycle_total"`
	ManualCycles              int                            `json:"manual_cycles"`
	ContinuationCycles        int                            `json:"continuation_cycles"`
	ScheduledCandidates       int                            `json:"scheduled_candidates"`
	ActiveSteps               []StepQueueStats               `json:"active_steps"`
	Steps                     []StepQueueStats               `json:"steps"`
	RecentEvents              []PipelineEvent                `json:"recent_events"`
	Translations              []TranslationQueueStats        `json:"translations"`
	CategoryVerification      CategoryVerificationQueueStats `json:"category_verification"`
}
