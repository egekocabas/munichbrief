package gazetteer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	refreshHistoryLimit = 500
	refreshPageSize     = 20
	maxDiagnosticBytes  = 2048
)

func safeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	value := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, err.Error())
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > maxDiagnosticBytes {
		value = value[:maxDiagnosticBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func (s *Store) recoverInterruptedRefreshes(ctx context.Context, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	message := "gazetteer refresh interrupted before completion"
	formatted := formatTime(at)
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_sources SET last_checked_at=?,last_failure_at=?,last_error=?,consecutive_failures=consecutive_failures+1,last_failure_run_id=(SELECT attempt.run_id FROM gazetteer_refresh_sources attempt JOIN gazetteer_refresh_runs run ON run.id=attempt.run_id WHERE run.status='running' AND attempt.status='fetching' AND attempt.source_key=gazetteer_sources.source_key ORDER BY attempt.run_id DESC LIMIT 1) WHERE EXISTS (SELECT 1 FROM gazetteer_refresh_sources attempt JOIN gazetteer_refresh_runs run ON run.id=attempt.run_id WHERE run.status='running' AND attempt.status='fetching' AND attempt.source_key=gazetteer_sources.source_key)`, formatted, formatted, message); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_refresh_sources SET duration_seconds=CASE WHEN status='fetching' THEN MAX(0,(julianday(?)-julianday(started_at))*86400) ELSE 0 END,status=CASE WHEN status='fetching' THEN 'interrupted' ELSE 'not_run' END,completed_at=?,failure_stage=CASE WHEN status='fetching' THEN 'interruption' ELSE failure_stage END,error_message=CASE WHEN status='fetching' THEN ? ELSE error_message END WHERE run_id IN (SELECT id FROM gazetteer_refresh_runs WHERE status='running') AND status IN ('pending','fetching')`, formatted, formatted, message); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_refresh_runs SET status='interrupted',completed_at=?,duration_seconds=MAX(0,(julianday(?)-julianday(started_at))*86400),failure_stage='interruption',error_message=? WHERE status='running'`, formatted, formatted, message); err != nil {
		return err
	}
	if err := pruneRefreshHistoryTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BeginRefresh(ctx context.Context, trigger RefreshTrigger, sources []SourceDefinition, at, next time.Time) (int64, error) {
	switch trigger {
	case RefreshTriggerStartup, RefreshTriggerScheduled, RefreshTriggerRetry, RefreshTriggerManual:
	default:
		return 0, fmt.Errorf("invalid gazetteer refresh trigger %q", trigger)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_state SET last_attempt_at=?,next_refresh_at=? WHERE singleton=1`, formatTime(at), formatTime(next)); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_refresh_runs(trigger_kind,status,started_at,active_generation_before) VALUES (?,'running',?,(SELECT active_generation_id FROM gazetteer_state WHERE singleton=1))`, string(trigger), formatTime(at))
	if err != nil {
		return 0, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for index, source := range sources {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_sources(source_key,display_name,source_url,license,attribution) VALUES (?,?,?,?,?) ON CONFLICT(source_key) DO UPDATE SET etag=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.etag ELSE '' END,last_modified=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.last_modified ELSE '' END,content_sha256=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.content_sha256 ELSE '' END,display_name=excluded.display_name,source_url=excluded.source_url,license=excluded.license,attribution=excluded.attribution`, source.Key, source.DisplayName, source.URL, source.License, source.Attribution); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_refresh_sources(run_id,source_key,source_order,display_name,source_url,license,attribution,contract_version,minimum_rows,maximum_rows,maximum_size,status) VALUES (?,?,?,?,?,?,?,?,?,?,?,'pending')`, runID, source.Key, index, source.DisplayName, source.URL, source.License, source.Attribution, sourceContractVersion, source.MinimumRows, source.MaximumRows, source.MaximumSize); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return runID, nil
}

func (s *Store) StartRefreshSource(ctx context.Context, runID int64, key string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE gazetteer_refresh_sources SET status='fetching',started_at=? WHERE run_id=? AND source_key=? AND status='pending'`, formatTime(at), runID, key)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("gazetteer source attempt is not pending")
	}
	return nil
}

func (s *Store) CompleteRefreshSource(ctx context.Context, runID int64, snapshot SourceSnapshot, notModified bool, diagnostic SourceFetchDiagnostic, at time.Time) error {
	status := "succeeded"
	if notModified {
		status = "not_modified"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE gazetteer_refresh_sources SET status=?,completed_at=?,duration_seconds=?,http_status=NULLIF(?,0),response_bytes=?,row_count=?,content_sha256=? WHERE run_id=? AND source_key=? AND status='fetching'`, status, formatTime(at), diagnostic.Duration.Seconds(), diagnostic.HTTPStatus, diagnostic.ResponseSize, len(snapshot.Entries), snapshot.ContentHash, runID, snapshot.Definition.Key)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("gazetteer source attempt is not running")
	}
	definition := snapshot.Definition
	if _, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_sources(source_key,display_name,source_url,license,attribution,last_checked_at,last_success_at,consecutive_failures) VALUES (?,?,?,?,?,?,?,0) ON CONFLICT(source_key) DO UPDATE SET etag=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.etag ELSE '' END,last_modified=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.last_modified ELSE '' END,content_sha256=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.content_sha256 ELSE '' END,display_name=excluded.display_name,source_url=excluded.source_url,license=excluded.license,attribution=excluded.attribution,last_checked_at=excluded.last_checked_at,last_success_at=excluded.last_success_at,consecutive_failures=0,last_failure_at=NULL,last_error='',last_failure_run_id=NULL`, definition.Key, definition.DisplayName, definition.URL, definition.License, definition.Attribution, formatTime(at), formatTime(at)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailRefresh(ctx context.Context, runID int64, source *SourceDefinition, stage string, diagnostic SourceFetchDiagnostic, failure error, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	message := safeDiagnostic(failure)
	key := ""
	if source != nil {
		key = source.Key
		if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_refresh_sources SET status='failed',completed_at=?,duration_seconds=?,http_status=NULLIF(?,0),response_bytes=?,row_count=?,failure_stage=?,error_message=? WHERE run_id=? AND source_key=? AND status IN ('pending','fetching')`, formatTime(at), diagnostic.Duration.Seconds(), diagnostic.HTTPStatus, diagnostic.ResponseSize, diagnostic.RowCount, stage, message, runID, key); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_sources(source_key,display_name,source_url,license,attribution,last_checked_at,last_failure_at,last_error,last_failure_run_id,consecutive_failures) VALUES (?,?,?,?,?,?,?,?,?,1) ON CONFLICT(source_key) DO UPDATE SET etag=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.etag ELSE '' END,last_modified=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.last_modified ELSE '' END,content_sha256=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.content_sha256 ELSE '' END,display_name=excluded.display_name,source_url=excluded.source_url,license=excluded.license,attribution=excluded.attribution,last_checked_at=excluded.last_checked_at,last_failure_at=excluded.last_failure_at,last_error=excluded.last_error,last_failure_run_id=excluded.last_failure_run_id,consecutive_failures=gazetteer_sources.consecutive_failures+1`, source.Key, source.DisplayName, source.URL, source.License, source.Attribution, formatTime(at), formatTime(at), message, runID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_refresh_sources SET status='not_run',completed_at=? WHERE run_id=? AND status='pending'`, formatTime(at), runID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gazetteer_refresh_runs SET status='failed',completed_at=?,duration_seconds=MAX(0,(julianday(?)-julianday(started_at))*86400),active_generation_after=(SELECT active_generation_id FROM gazetteer_state WHERE singleton=1),failure_stage=?,failed_source_key=?,error_message=? WHERE id=? AND status='running'`, formatTime(at), formatTime(at), stage, key, message, runID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("gazetteer refresh attempt is not running")
	}
	if err := pruneRefreshHistoryTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func pruneRefreshHistoryTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_sources SET last_failure_run_id=NULL WHERE last_failure_run_id IN (SELECT id FROM gazetteer_refresh_runs WHERE status!='running' AND id NOT IN (SELECT id FROM gazetteer_refresh_runs WHERE status!='running' ORDER BY id DESC LIMIT ?))`, refreshHistoryLimit); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM gazetteer_refresh_runs WHERE status!='running' AND id NOT IN (SELECT id FROM gazetteer_refresh_runs WHERE status!='running' ORDER BY id DESC LIMIT ?)`, refreshHistoryLimit)
	return err
}

func (s *Store) RefreshHistory(ctx context.Context, page int) (RefreshHistoryPage, error) {
	if page < 1 {
		return RefreshHistoryPage{}, errors.New("gazetteer history page must be positive")
	}
	if page > refreshHistoryLimit/refreshPageSize+1 {
		return RefreshHistoryPage{Page: page, HasNewer: true}, nil
	}
	offset := (page - 1) * refreshPageSize
	rows, err := s.db.QueryContext(ctx, refreshRunSelect+` ORDER BY run.id DESC LIMIT ? OFFSET ?`, refreshPageSize+1, offset)
	if err != nil {
		return RefreshHistoryPage{}, err
	}
	defer rows.Close()
	result := RefreshHistoryPage{Page: page, HasNewer: page > 1}
	for rows.Next() {
		entry, err := scanRefreshRun(rows)
		if err != nil {
			return RefreshHistoryPage{}, err
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return RefreshHistoryPage{}, err
	}
	if len(result.Entries) > refreshPageSize {
		result.HasOlder = true
		result.Entries = result.Entries[:refreshPageSize]
	}
	return result, nil
}

const refreshRunSelect = `SELECT run.id,run.trigger_kind,run.status,run.started_at,COALESCE(run.completed_at,''),run.duration_seconds,COALESCE(run.active_generation_before,0),COALESCE(run.active_generation_after,0),run.entry_count,run.changed,run.all_not_modified,run.failure_stage,run.failed_source_key,run.error_message,COUNT(source.source_key),COALESCE(SUM(CASE WHEN source.status IN ('succeeded','not_modified','failed','not_run','interrupted') THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN source.status='failed' THEN 1 ELSE 0 END),0) FROM gazetteer_refresh_runs run LEFT JOIN gazetteer_refresh_sources source ON source.run_id=run.id GROUP BY run.id`

type rowScanner interface{ Scan(...any) error }

func scanRefreshRun(row rowScanner) (RefreshRunStatus, error) {
	var result RefreshRunStatus
	var started, completed string
	var durationSeconds float64
	var changed, notModified sql.NullBool
	err := row.Scan(&result.ID, &result.Trigger, &result.Status, &started, &completed, &durationSeconds, &result.ActiveGenerationBefore, &result.ActiveGenerationAfter, &result.EntryCount, &changed, &notModified, &result.FailureStage, &result.FailedSourceKey, &result.ErrorMessage, &result.SourcesTotal, &result.SourcesCompleted, &result.SourcesFailed)
	if err != nil {
		return result, err
	}
	result.StartedAt, result.CompletedAt = parseTime(started), parseTime(completed)
	result.Duration = time.Duration(durationSeconds * float64(time.Second))
	if changed.Valid {
		value := changed.Bool
		result.Changed = &value
	}
	if notModified.Valid {
		value := notModified.Bool
		result.AllNotModified = &value
	}
	return result, nil
}

func (s *Store) RefreshDetails(ctx context.Context, id int64) (RefreshRunDetails, error) {
	if id < 1 {
		return RefreshRunDetails{}, errors.New("invalid gazetteer refresh id")
	}
	run, err := scanRefreshRun(s.db.QueryRowContext(ctx, refreshRunSelect+` HAVING run.id=?`, id))
	if err != nil {
		return RefreshRunDetails{}, err
	}
	result := RefreshRunDetails{Run: run}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,source_key,source_order,display_name,source_url,license,attribution,contract_version,minimum_rows,maximum_rows,maximum_size,status,COALESCE(started_at,''),COALESCE(completed_at,''),duration_seconds,COALESCE(http_status,0),response_bytes,row_count,content_sha256,failure_stage,error_message FROM gazetteer_refresh_sources WHERE run_id=? ORDER BY source_order`, id)
	if err != nil {
		return RefreshRunDetails{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var source RefreshSourceStatus
		var started, completed string
		var durationSeconds float64
		if err := rows.Scan(&source.RunID, &source.Key, &source.Order, &source.DisplayName, &source.URL, &source.License, &source.Attribution, &source.ContractVersion, &source.MinimumRows, &source.MaximumRows, &source.MaximumSize, &source.Status, &started, &completed, &durationSeconds, &source.HTTPStatus, &source.ResponseSize, &source.RowCount, &source.ContentHash, &source.FailureStage, &source.ErrorMessage); err != nil {
			return RefreshRunDetails{}, err
		}
		source.StartedAt, source.CompletedAt = parseTime(started), parseTime(completed)
		source.Duration = time.Duration(durationSeconds * float64(time.Second))
		result.Sources = append(result.Sources, source)
	}
	return result, rows.Err()
}
