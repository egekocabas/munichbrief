package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) PipelineSnapshot(ctx context.Context, sourceMode string, stepKeys []string, counterSpecs []PostProcessingCounterSpec, now time.Time) (PipelineSnapshot, error) {
	var snapshot PipelineSnapshot
	var scheduledAfter string
	if err := s.db.QueryRowContext(ctx, `SELECT scheduled_after FROM pipeline_cutovers WHERE pipeline_version = ?`, PipelineVersion).Scan(&scheduledAfter); err != nil {
		return snapshot, fmt.Errorf("read pipeline cutover: %w", err)
	}
	cutover, err := time.Parse(time.RFC3339Nano, scheduledAfter)
	if err != nil {
		return snapshot, fmt.Errorf("parse pipeline cutover: %w", err)
	}
	snapshot.ScheduledAfter = &cutover
	var cycle PipelineCycle
	var authorized int
	var started, completed string
	err = s.db.QueryRowContext(ctx, `SELECT id, kind, status, active_step, window_authorized, COALESCE(started_at,''), COALESCE(completed_at,'') FROM processing_cycles WHERE status = 'running' LIMIT 1`).Scan(&cycle.ID, &cycle.Kind, &cycle.Status, &cycle.ActiveStep, &authorized, &started, &completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return snapshot, err
	}
	if err == nil {
		cycle.WindowAuthorized = authorized == 1
		if started != "" {
			value, err := time.Parse(time.RFC3339Nano, started)
			if err != nil {
				return snapshot, fmt.Errorf("parse active cycle start: %w", err)
			}
			cycle.StartedAt = &value
		}
		snapshot.ActiveCycle = &cycle
		if err := s.db.QueryRowContext(ctx, `SELECT step_key, model_identity FROM cycle_step_models WHERE cycle_id = ? AND step_order = ?`, cycle.ID, cycle.ActiveStep).Scan(&snapshot.ActiveStepKey, &snapshot.ActiveModel); err != nil {
			return snapshot, fmt.Errorf("read active cycle step: %w", err)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN j.status IN ('succeeded','failed','needs_review','skipped','superseded') THEN 1 ELSE 0 END),0), COUNT(*)
			FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id WHERE ci.cycle_id = ?`, cycle.ID).Scan(&snapshot.CycleCompleted, &snapshot.CycleTotal); err != nil {
			return snapshot, fmt.Errorf("read active cycle progress: %w", err)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN j.status IN ('succeeded','failed','needs_review','skipped','superseded') THEN 1 ELSE 0 END),0), COUNT(*)
			FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id
			WHERE ci.cycle_id = ? AND j.step_order = ?`, cycle.ID, cycle.ActiveStep).Scan(&snapshot.ActiveStepCompleted, &snapshot.ActiveStepTotal); err != nil {
			return snapshot, fmt.Errorf("read active step progress: %w", err)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT ci.incident_id FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id WHERE ci.cycle_id=? AND j.status='running' LIMIT 1`, cycle.ID).Scan(&snapshot.CurrentIncidentID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return snapshot, fmt.Errorf("read active cycle incident: %w", err)
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_cycles WHERE status='queued' AND kind='manual'`).Scan(&snapshot.ManualCycles); err != nil {
		return snapshot, fmt.Errorf("count queued manual cycles: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_cycles WHERE status='queued' AND kind='continuation'`).Scan(&snapshot.ContinuationCycles); err != nil {
		return snapshot, fmt.Errorf("count queued continuation cycles: %w", err)
	}
	condition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return snapshot, err
	}
	query := `SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE ` + condition + ` AND COALESCE(i.body_de,'')<>'' AND julianday(i.created_at) > julianday((SELECT scheduled_after FROM pipeline_cutovers WHERE pipeline_version=?)) AND NOT EXISTS (SELECT 1 FROM presentation_runs r WHERE r.incident_id=i.id AND r.source_hash=i.content_hash AND r.pipeline_version=? AND r.status IN ('processing','complete','failed'))`
	if err := s.db.QueryRowContext(ctx, query, PipelineVersion, PipelineVersion).Scan(&snapshot.ScheduledCandidates); err != nil {
		return snapshot, fmt.Errorf("count scheduled pipeline candidates: %w", err)
	}
	for _, key := range stepKeys {
		active := StepQueueStats{StepKey: key}
		if snapshot.ActiveCycle != nil {
			err := s.db.QueryRowContext(ctx, `SELECT
				COALESCE(SUM(CASE WHEN j.status='waiting' THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='waiting' AND EXISTS (
					SELECT 1 FROM processing_step_jobs previous WHERE previous.cycle_item_id=j.cycle_item_id
					AND previous.step_order=j.step_order-1 AND previous.status='succeeded'
				) THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='pending' AND j.attempt_count=0 THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='running' THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='pending' AND j.attempt_count>0 THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='needs_review' THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='failed' THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN j.status='succeeded' THEN 1 ELSE 0 END),0)
				FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id
				WHERE ci.cycle_id=? AND j.step_key=?`, snapshot.ActiveCycle.ID, key).Scan(
				&active.Waiting, &active.ReadyAfterStage, &active.Queued, &active.Running,
				&active.Retrying, &active.NeedsReview, &active.Failed, &active.Succeeded,
			)
			if err != nil {
				return snapshot, fmt.Errorf("read active %s queue stats: %w", key, err)
			}
		}
		snapshot.ActiveSteps = append(snapshot.ActiveSteps, active)

		stat := StepQueueStats{StepKey: key}
		var durationSeconds float64
		var durationCount int
		var last string
		err := s.db.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN status='waiting' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='pending' AND attempt_count=0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='running' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='pending' AND attempt_count>0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='needs_review' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN completed_at<>'' AND started_at<>'' THEN (julianday(completed_at)-julianday(started_at))*86400 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN completed_at<>'' AND started_at<>'' THEN 1 ELSE 0 END),0),
			COALESCE(MAX(CASE WHEN status='succeeded' THEN completed_at END),'')
			FROM processing_step_jobs WHERE step_key=?`, key).Scan(&stat.Waiting, &stat.Queued, &stat.Running, &stat.Retrying, &stat.NeedsReview, &stat.Failed, &stat.Succeeded, &durationSeconds, &durationCount, &last)
		if err != nil {
			return snapshot, err
		}
		if durationCount > 0 {
			stat.AverageDuration = time.Duration(durationSeconds / float64(durationCount) * float64(time.Second))
			stat.AverageDurationSeconds = stat.AverageDuration.Seconds()
		}
		if last != "" {
			value, err := time.Parse(time.RFC3339Nano, last)
			if err != nil {
				return snapshot, fmt.Errorf("parse %s last-success time: %w", key, err)
			}
			stat.LastSuccess = &value
		}
		snapshot.Steps = append(snapshot.Steps, stat)
	}
	postRows, err := s.db.QueryContext(ctx, `SELECT scopes.processor_key,scopes.scope_key,
		COALESCE(SUM(CASE WHEN jobs.status='pending' AND jobs.attempt_count=0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN jobs.status='running' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN jobs.status='pending' AND jobs.attempt_count>0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN jobs.status='needs_review' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN jobs.status='failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN jobs.status='succeeded' THEN 1 ELSE 0 END),0)
		FROM post_processing_scopes scopes LEFT JOIN post_processing_jobs jobs
		ON jobs.processor_key=scopes.processor_key AND jobs.scope_key=scopes.scope_key
		GROUP BY scopes.processor_key,scopes.scope_key ORDER BY scopes.processor_key,scopes.scope_key`)
	if err != nil {
		return snapshot, fmt.Errorf("read post-processing queue stats: %w", err)
	}
	for postRows.Next() {
		var stat PostProcessingQueueStats
		if err := postRows.Scan(&stat.ProcessorKey, &stat.ScopeKey, &stat.Pending, &stat.Running, &stat.Retrying, &stat.NeedsReview, &stat.Failed, &stat.Succeeded); err != nil {
			postRows.Close()
			return snapshot, err
		}
		stat.Counters = make(map[string]int)
		snapshot.PostProcessing = append(snapshot.PostProcessing, stat)
	}
	if err := postRows.Err(); err != nil {
		postRows.Close()
		return snapshot, fmt.Errorf("iterate post-processing queue stats: %w", err)
	}
	if err := postRows.Close(); err != nil {
		return snapshot, err
	}
	for _, spec := range counterSpecs {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs job JOIN post_processing_values value ON value.job_id=job.id
			WHERE job.processor_key=? AND job.scope_key=? AND job.status='succeeded' AND value.kind=? AND value.value=?`, spec.ProcessorKey, spec.ScopeKey, spec.OutputKind, spec.EqualsValue).Scan(&count); err != nil {
			return snapshot, fmt.Errorf("read post-processing counter %s: %w", spec.CounterKey, err)
		}
		for index := range snapshot.PostProcessing {
			stat := &snapshot.PostProcessing[index]
			if stat.ProcessorKey == spec.ProcessorKey && stat.ScopeKey == spec.ScopeKey {
				stat.Counters[spec.CounterKey] = count
			}
		}
	}
	history, err := s.listPipelineHistoryEntries(ctx, sourceMode, 12, nil, nil)
	if err != nil {
		return snapshot, fmt.Errorf("read recent pipeline history: %w", err)
	}
	for _, entry := range history {
		snapshot.RecentEvents = append(snapshot.RecentEvents, PipelineEvent{
			At: entry.UpdatedAt, Kind: entry.Kind, CycleID: entry.CycleID,
			IncidentID: entry.IncidentID, StepKey: entry.StepKey, ProcessorKey: entry.ProcessorKey, ScopeKey: entry.ScopeKey, ExecutionKey: entry.ExecutionKey,
			Status: entry.Status, FailureKind: entry.FailureKind,
		})
	}
	return snapshot, nil
}
