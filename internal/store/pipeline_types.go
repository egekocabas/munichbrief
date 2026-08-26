package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

const PipelineVersion = "incident-pipeline-v1"

var ErrPipelineUnconfigured = errors.New("AI pipeline models are not configured")

type PipelineStepPlan struct {
	Key           string
	Order         int
	PromptVersion string
	Model         string
}

type StepSetting struct {
	StepKey        string    `json:"step_key"`
	PreferredModel string    `json:"preferred_model"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PipelineCycle struct {
	ID               int64      `json:"id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	ActiveStep       int        `json:"active_step"`
	WindowAuthorized bool       `json:"window_authorized"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

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
	OriginalTitle     string
	OriginalBody      string
	TitleDE           string
	SummaryDE         string
}

type PipelineValue struct {
	Kind  string
	Value string
}

type PipelineRequestResult struct {
	CycleID   int64
	Requested int
	Current   int
}

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

type PipelineEvent struct {
	At          time.Time `json:"at"`
	CycleID     int64     `json:"cycle_id"`
	IncidentID  int64     `json:"incident_id"`
	StepKey     string    `json:"step_key"`
	Status      string    `json:"status"`
	FailureKind string    `json:"failure_kind,omitempty"`
}

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

type PipelineSnapshot struct {
	ActiveCycle         *PipelineCycle   `json:"active_cycle,omitempty"`
	ActiveStepKey       string           `json:"active_step_key"`
	ActiveModel         string           `json:"active_model"`
	CurrentIncidentID   int64            `json:"current_incident_id,omitempty"`
	ActiveStepCompleted int              `json:"active_step_completed"`
	ActiveStepTotal     int              `json:"active_step_total"`
	CycleCompleted      int              `json:"cycle_completed"`
	CycleTotal          int              `json:"cycle_total"`
	ManualCycles        int              `json:"manual_cycles"`
	ContinuationCycles  int              `json:"continuation_cycles"`
	ScheduledCandidates int              `json:"scheduled_candidates"`
	ActiveSteps         []StepQueueStats `json:"active_steps"`
	Steps               []StepQueueStats `json:"steps"`
	RecentEvents        []PipelineEvent  `json:"recent_events"`
}
