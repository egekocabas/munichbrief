package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ListPipelineHistory returns a stable, reverse-chronological page of persisted
// pipeline job states. Waiting jobs are omitted to match the dashboard preview.
func (s *Store) ListPipelineHistory(ctx context.Context, sourceMode string, limit int, before, after *PipelineHistoryCursor) (PipelineHistoryPage, error) {
	var page PipelineHistoryPage
	if sourceMode != "fixture" && sourceMode != "live" {
		return page, errors.New("source mode must be fixture or live")
	}
	if limit < 1 {
		return page, errors.New("pipeline history limit must be positive")
	}
	if before != nil && after != nil {
		return page, errors.New("pipeline history accepts only one cursor")
	}

	query := `SELECT j.id, j.updated_at, c.id, c.kind, c.status, ci.incident_id,
		j.step_key, j.status, j.attempt_count, COALESCE(j.failure_kind,''),
		j.model_identity, j.prompt_version
		FROM processing_step_jobs j
		JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id
		JOIN processing_cycles c ON c.id=ci.cycle_id
		WHERE c.source_mode=? AND j.status<>'waiting'`
	args := []any{sourceMode}
	ascending := false
	if before != nil {
		query += ` AND (julianday(j.updated_at)<julianday(?) OR (julianday(j.updated_at)=julianday(?) AND j.id<?))`
		formatted := formatTime(before.UpdatedAt.UTC())
		args = append(args, formatted, formatted, before.JobID)
	} else if after != nil {
		query += ` AND (julianday(j.updated_at)>julianday(?) OR (julianday(j.updated_at)=julianday(?) AND j.id>?))`
		formatted := formatTime(after.UpdatedAt.UTC())
		args = append(args, formatted, formatted, after.JobID)
		ascending = true
	}
	if ascending {
		query += ` ORDER BY julianday(j.updated_at), j.id LIMIT ?`
	} else {
		query += ` ORDER BY julianday(j.updated_at) DESC, j.id DESC LIMIT ?`
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("list pipeline history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry PipelineHistoryEntry
		var updatedAt string
		if err := rows.Scan(
			&entry.JobID, &updatedAt, &entry.CycleID, &entry.CycleKind, &entry.CycleStatus,
			&entry.IncidentID, &entry.StepKey, &entry.Status, &entry.AttemptCount,
			&entry.FailureKind, &entry.ModelIdentity, &entry.PromptVersion,
		); err != nil {
			return page, err
		}
		entry.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return page, fmt.Errorf("parse pipeline history time: %w", err)
		}
		page.Entries = append(page.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("iterate pipeline history: %w", err)
	}
	if ascending {
		for left, right := 0, len(page.Entries)-1; left < right; left, right = left+1, right-1 {
			page.Entries[left], page.Entries[right] = page.Entries[right], page.Entries[left]
		}
	}
	if len(page.Entries) == 0 {
		return page, nil
	}

	first := PipelineHistoryCursor{UpdatedAt: page.Entries[0].UpdatedAt, JobID: page.Entries[0].JobID}
	last := PipelineHistoryCursor{UpdatedAt: page.Entries[len(page.Entries)-1].UpdatedAt, JobID: page.Entries[len(page.Entries)-1].JobID}
	page.HasNewer, err = s.pipelineHistoryExists(ctx, sourceMode, first, true)
	if err != nil {
		return PipelineHistoryPage{}, err
	}
	page.HasOlder, err = s.pipelineHistoryExists(ctx, sourceMode, last, false)
	if err != nil {
		return PipelineHistoryPage{}, err
	}
	return page, nil
}

func (s *Store) pipelineHistoryExists(ctx context.Context, sourceMode string, cursor PipelineHistoryCursor, newer bool) (bool, error) {
	comparison := `<`
	idComparison := `<`
	if newer {
		comparison = `>`
		idComparison = `>`
	}
	formatted := formatTime(cursor.UpdatedAt.UTC())
	query := `SELECT EXISTS(SELECT 1 FROM processing_step_jobs j
		JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id
		JOIN processing_cycles c ON c.id=ci.cycle_id
		WHERE c.source_mode=? AND j.status<>'waiting'
		AND (julianday(j.updated_at)` + comparison + `julianday(?)
		OR (julianday(j.updated_at)=julianday(?) AND j.id` + idComparison + `?)))`
	var exists bool
	if err := s.db.QueryRowContext(ctx, query, sourceMode, formatted, formatted, cursor.JobID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check pipeline history boundary: %w", err)
	}
	return exists, nil
}
