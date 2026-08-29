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

// PostProcessingStatusReasonMissingInput marks a processor as ineligible
// because one of its declared required inputs was unavailable.
const PostProcessingStatusReasonMissingInput = "missing_input"

// ProcessingStatusReasonOperatorCanceled identifies unfinished work explicitly
// canceled by an administrator without exposing any job input or output.
const ProcessingStatusReasonOperatorCanceled = "operator_canceled"

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
	ID               int64      `json:"id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	ActiveStep       int        `json:"active_step"`
	WindowAuthorized bool       `json:"window_authorized"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// PipelineJob is a claimed unit of work with its frozen input provenance.
type PipelineJob struct {
	ID                int64
	CycleID           int64
	CycleKind         string
	CycleItemID       int64
	PresentationRunID int64
	IncidentID        int64
	SourceHash        string
	StepKey           string
	StepOrder         int
	ModelIdentity     string
	PromptVersion     string
	InputHash         string
	AttemptCount      int
	InputValues       map[string]string
}

// PostProcessingPlan freezes one processor scope, prompt, model, and ordered
// required inputs for an independently executed job. A missing or blank
// declared input makes the job ineligible and is persisted as a skip.
type PostProcessingPlan struct {
	ProcessorKey  string
	ScopeKey      string
	PromptVersion string
	Model         string
	InputKinds    []string
}

// PostProcessingScope is one automatically enabled registry scope.
type PostProcessingScope struct {
	ProcessorKey string
	ScopeKey     string
}

// PostProcessingScopeContract identifies the current prompt and exact
// immutable required-input/output contract for one registered processor scope.
type PostProcessingScopeContract struct {
	PromptVersion string
	InputKinds    []string
	OutputKinds   []string
}

// PostProcessingContract maps each scope of one processor to its current
// prompt and immutable value contract.
type PostProcessingContract map[string]PostProcessingScopeContract

// PostProcessingJob is one independently claimed job for an immutable current
// canonical presentation.
type PostProcessingJob struct {
	ID                int64
	PresentationRunID int64
	IncidentID        int64
	ProcessorKey      string
	ScopeKey          string
	RequestKind       string
	ModelIdentity     string
	PromptVersion     string
	InputHash         string
	AttemptCount      int
	InputValues       map[string]string
	OutputKinds       []string
}

// PostProcessingQueueStats summarizes one registered processor scope.
type PostProcessingQueueStats struct {
	ProcessorKey     string         `json:"processor_key"`
	ScopeKey         string         `json:"scope_key"`
	Pending          int            `json:"pending"`
	Running          int            `json:"running"`
	Retrying         int            `json:"retrying"`
	NeedsReview      int            `json:"needs_review"`
	Failed           int            `json:"failed"`
	Skipped          int            `json:"skipped"`
	Succeeded        int            `json:"succeeded"`
	RunningStartedAt *time.Time     `json:"running_started_at,omitempty"`
	Counters         map[string]int `json:"counters,omitempty"`
}

type PostProcessingCounterSpec struct {
	ProcessorKey        string
	ScopeKey            string
	CounterKey          string
	OutputKind          string
	EqualsValue         string
	RequiredOutputKinds []string
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

// AIControlState is the durable operator-controlled automatic-processing gate.
// Manual work remains eligible while AutomaticProcessingEnabled is false.
type AIControlState struct {
	AutomaticProcessingEnabled bool      `json:"automatic_processing_enabled"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

// PipelineCancellationResult reports unfinished work terminalized by one
// operator request. Repeated requests return zero counts.
type PipelineCancellationResult struct {
	Cycles             int `json:"cycles"`
	CanonicalJobs      int `json:"canonical_jobs"`
	PostProcessingJobs int `json:"post_processing_jobs"`
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
	At           time.Time `json:"at"`
	Kind         string    `json:"kind"`
	CycleID      int64     `json:"cycle_id"`
	IncidentID   int64     `json:"incident_id"`
	StepKey      string    `json:"step_key"`
	ProcessorKey string    `json:"processor_key,omitempty"`
	ScopeKey     string    `json:"scope_key,omitempty"`
	ExecutionKey string    `json:"execution_key"`
	Status       string    `json:"status"`
	StatusReason string    `json:"status_reason,omitempty"`
	StatusDetail string    `json:"status_detail,omitempty"`
	FailureKind  string    `json:"failure_kind,omitempty"`
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
	ProcessorKey  string
	ScopeKey      string
	ExecutionKey  string
	RequestKind   string
	Status        string
	StatusReason  string
	StatusDetail  string
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
	ScheduledAfter      *time.Time                 `json:"scheduled_after,omitempty"`
	ActiveCycle         *PipelineCycle             `json:"active_cycle,omitempty"`
	ActiveStepKey       string                     `json:"active_step_key"`
	ActiveModel         string                     `json:"active_model"`
	CurrentIncidentID   int64                      `json:"current_incident_id,omitempty"`
	ActiveStepCompleted int                        `json:"active_step_completed"`
	ActiveStepTotal     int                        `json:"active_step_total"`
	CycleCompleted      int                        `json:"cycle_completed"`
	CycleTotal          int                        `json:"cycle_total"`
	ManualCycles        int                        `json:"manual_cycles"`
	ContinuationCycles  int                        `json:"continuation_cycles"`
	ScheduledCandidates int                        `json:"scheduled_candidates"`
	ActiveSteps         []StepQueueStats           `json:"active_steps"`
	Steps               []StepQueueStats           `json:"steps"`
	RecentEvents        []PipelineEvent            `json:"recent_events"`
	PostProcessing      []PostProcessingQueueStats `json:"post_processing"`
}
