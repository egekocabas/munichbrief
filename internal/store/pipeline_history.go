package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	pipelineHistoryKindCycle          = "cycle"
	pipelineHistoryKindPostProcessing = "post_processing"
)

// ListPipelineHistory returns a stable, reverse-chronological page of persisted
// canonical and independent post-processing job states. Waiting canonical jobs
// are omitted to match the dashboard preview.
func (s *Store) ListPipelineHistory(ctx context.Context, sourceMode string, limit int, before, after *PipelineHistoryCursor) (PipelineHistoryPage, error) {
	var page PipelineHistoryPage
	if limit < 1 {
		return page, errors.New("pipeline history limit must be positive")
	}
	if before != nil && after != nil {
		return page, errors.New("pipeline history accepts only one cursor")
	}

	entries, err := s.listPipelineHistoryEntries(ctx, sourceMode, limit, before, after)
	if err != nil {
		return page, err
	}
	page.Entries = entries
	if len(entries) == 0 {
		return page, nil
	}

	first := pipelineHistoryCursor(entries[0])
	last := pipelineHistoryCursor(entries[len(entries)-1])
	newer, err := s.listPipelineHistoryEntries(ctx, sourceMode, 1, nil, &first)
	if err != nil {
		return PipelineHistoryPage{}, err
	}
	older, err := s.listPipelineHistoryEntries(ctx, sourceMode, 1, &last, nil)
	if err != nil {
		return PipelineHistoryPage{}, err
	}
	page.HasNewer = len(newer) > 0
	page.HasOlder = len(older) > 0
	return page, nil
}

func (s *Store) listPipelineHistoryEntries(ctx context.Context, sourceMode string, limit int, before, after *PipelineHistoryCursor) ([]PipelineHistoryEntry, error) {
	sourceCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return nil, err
	}
	query := `WITH history AS (
		SELECT j.id AS job_id, j.updated_at, 'cycle' AS kind, 0 AS kind_order,
			c.id AS cycle_id, c.kind AS cycle_kind, c.status AS cycle_status,
			ci.incident_id, j.step_key, '' AS processor_key, '' AS scope_key,
			j.step_key AS execution_key, '' AS request_kind,
			j.status, j.attempt_count, COALESCE(j.failure_kind,'') AS failure_kind,
			j.model_identity, j.prompt_version
		FROM processing_step_jobs j
		JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id
		JOIN processing_cycles c ON c.id=ci.cycle_id
		WHERE c.source_mode=? AND j.status<>'waiting'
		UNION ALL
		SELECT post.id AS job_id, post.updated_at, 'post_processing' AS kind, 1 AS kind_order,
			COALESCE(c.id,0) AS cycle_id, COALESCE(c.kind,'') AS cycle_kind,
			COALESCE(c.status,'') AS cycle_status,r.incident_id,
			'' AS step_key,post.processor_key,post.scope_key,
			post.processor_key || '/' || post.scope_key AS execution_key,
			post.request_kind,post.status,post.attempt_count,
			COALESCE(post.failure_kind,'') AS failure_kind,post.model_identity,post.prompt_version
		FROM post_processing_jobs post
		JOIN presentation_runs r ON r.id=post.presentation_run_id
		JOIN incidents i ON i.id=r.incident_id
		JOIN source_documents d ON d.id=i.source_document_id
		LEFT JOIN processing_cycle_items ci ON ci.id=(
			SELECT linked.id FROM processing_cycle_items linked
			WHERE linked.presentation_run_id=r.id ORDER BY linked.id DESC LIMIT 1
		)
		LEFT JOIN processing_cycles c ON c.id=ci.cycle_id
		WHERE ` + sourceCondition + `
	)
	SELECT job_id, updated_at, kind, cycle_id, cycle_kind, cycle_status,
		incident_id, step_key, processor_key, scope_key, execution_key, request_kind, status, attempt_count,
		failure_kind, model_identity, prompt_version, kind_order
	FROM history WHERE 1=1`
	args := []any{sourceMode}
	ascending := false
	if before != nil {
		kindOrder, err := pipelineHistoryKindOrder(before.Kind)
		if err != nil {
			return nil, err
		}
		query += ` AND (julianday(updated_at)<julianday(?) OR
			(julianday(updated_at)=julianday(?) AND (kind_order<? OR (kind_order=? AND job_id<?))))`
		formatted := formatTime(before.UpdatedAt.UTC())
		args = append(args, formatted, formatted, kindOrder, kindOrder, before.JobID)
	} else if after != nil {
		kindOrder, err := pipelineHistoryKindOrder(after.Kind)
		if err != nil {
			return nil, err
		}
		query += ` AND (julianday(updated_at)>julianday(?) OR
			(julianday(updated_at)=julianday(?) AND (kind_order>? OR (kind_order=? AND job_id>?))))`
		formatted := formatTime(after.UpdatedAt.UTC())
		args = append(args, formatted, formatted, kindOrder, kindOrder, after.JobID)
		ascending = true
	}
	if ascending {
		query += ` ORDER BY julianday(updated_at), kind_order, job_id LIMIT ?`
	} else {
		query += ` ORDER BY julianday(updated_at) DESC, kind_order DESC, job_id DESC LIMIT ?`
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pipeline history: %w", err)
	}
	defer rows.Close()
	entries := make([]PipelineHistoryEntry, 0, limit)
	for rows.Next() {
		var entry PipelineHistoryEntry
		var updatedAt string
		var kindOrder int
		if err := rows.Scan(
			&entry.JobID, &updatedAt, &entry.Kind, &entry.CycleID, &entry.CycleKind,
			&entry.CycleStatus, &entry.IncidentID, &entry.StepKey, &entry.ProcessorKey, &entry.ScopeKey, &entry.ExecutionKey,
			&entry.RequestKind, &entry.Status, &entry.AttemptCount, &entry.FailureKind,
			&entry.ModelIdentity, &entry.PromptVersion, &kindOrder,
		); err != nil {
			return nil, err
		}
		entry.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse pipeline history time: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pipeline history: %w", err)
	}
	if ascending {
		for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
			entries[left], entries[right] = entries[right], entries[left]
		}
	}
	return entries, nil
}

func pipelineHistoryCursor(entry PipelineHistoryEntry) PipelineHistoryCursor {
	return PipelineHistoryCursor{UpdatedAt: entry.UpdatedAt, Kind: entry.Kind, JobID: entry.JobID}
}

func pipelineHistoryKindOrder(kind string) (int, error) {
	switch kind {
	case pipelineHistoryKindCycle:
		return 0, nil
	case pipelineHistoryKindPostProcessing:
		return 1, nil
	default:
		return 0, fmt.Errorf("invalid pipeline history kind %q", kind)
	}
}
