package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// QueueCategoryVerificationForRun creates a focused independent verification
// for one completed presentation. Force permits a new manual attempt after a
// successful result while still deduplicating active work.
func (s *Store) QueueCategoryVerificationForRun(ctx context.Context, runID int64, plan CategoryVerificationPlan, requestKind string, force bool, now time.Time) (int, error) {
	if requestKind != "scheduled" && requestKind != "manual" && requestKind != "backfill" {
		return 0, errors.New("invalid category verification request kind")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var title, summary, category string
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='title_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='summary_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='category' LIMIT 1),'')
		FROM presentation_runs r WHERE r.id=? AND r.status='complete'`, runID).Scan(&title, &summary, &category); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(summary) == "" || strings.TrimSpace(category) == "" {
		return 0, ErrNotFound
	}
	queued, err := queueCategoryVerificationTx(ctx, tx, runID, title, summary, category, plan, requestKind, force, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if queued {
		return 1, nil
	}
	return 0, nil
}

// QueueIncidentCategoryVerification retries the newest canonical presentation
// for a current incident without changing its immutable metadata value.
func (s *Store) QueueIncidentCategoryVerification(ctx context.Context, incidentID int64, plan CategoryVerificationPlan, now time.Time) (int, error) {
	if incidentID < 1 {
		return 0, errors.New("incident ID must be positive")
	}
	var runID int64
	err := s.db.QueryRowContext(ctx, `SELECT r.id FROM presentation_runs r
		JOIN incidents i ON i.id=r.incident_id
		WHERE i.id=? AND r.source_hash=i.content_hash AND r.status='complete'
		AND (r.pipeline_version IN (?,?) OR r.legacy=1)
		ORDER BY CASE r.pipeline_version WHEN ? THEN 0 WHEN ? THEN 1 ELSE 2 END,
			r.completed_at DESC,r.id DESC LIMIT 1`, incidentID, PipelineVersion, PreviousPipelineVersion, PipelineVersion, PreviousPipelineVersion).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return s.QueueCategoryVerificationForRun(ctx, runID, plan, "manual", true, now)
}

// QueueMissingCategoryVerifications scans reader-selectable runs for the active
// source revision. Automatic work respects the deployment cutover; an explicit
// backfill covers older current runs, including translated fallbacks.
func (s *Store) QueueMissingCategoryVerifications(ctx context.Context, sourceMode string, plan CategoryVerificationPlan, backfill bool, now time.Time) (int, error) {
	condition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return 0, err
	}
	plan.Model = strings.TrimSpace(plan.Model)
	plan.PromptVersion = strings.TrimSpace(plan.PromptVersion)
	if plan.Model == "" || plan.PromptVersion == "" {
		return 0, errors.New("category verification model and prompt are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cutoverCondition := ""
	requestKind := "scheduled"
	if backfill {
		requestKind = "backfill"
	} else {
		cutoverCondition = ` AND julianday(r.completed_at) > julianday((SELECT automatic_after FROM category_verification_cutover WHERE id=1))`
	}
	query := `SELECT r.id,
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='title_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='summary_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='category' LIMIT 1),'')
		FROM presentation_runs r
		JOIN incidents i ON i.id=r.incident_id
		JOIN source_documents d ON d.id=i.source_document_id
		WHERE ` + condition + ` AND r.source_hash=i.content_hash AND r.status='complete'
		AND (r.pipeline_version IN ('` + PipelineVersion + `','` + PreviousPipelineVersion + `') OR r.legacy=1)
		AND EXISTS (SELECT 1 FROM presentation_values category_value
			WHERE category_value.presentation_run_id=r.id AND category_value.kind='category'
			AND category_value.value IN ('traffic','theft_burglary','robbery_extortion','violence','sexual_offense','fraud_cyber','drugs','fire_hazard','property_damage','missing_wanted','police_operation','other'))
		AND (r.id=(SELECT candidate.id FROM presentation_runs candidate
			WHERE candidate.incident_id=i.id AND candidate.source_hash=i.content_hash AND candidate.status='complete'
			AND (candidate.pipeline_version IN ('` + PipelineVersion + `','` + PreviousPipelineVersion + `') OR candidate.legacy=1)
			ORDER BY CASE candidate.pipeline_version WHEN '` + PipelineVersion + `' THEN 0 WHEN '` + PreviousPipelineVersion + `' THEN 1 ELSE 2 END,
			candidate.completed_at DESC,candidate.id DESC LIMIT 1)
			OR EXISTS (SELECT 1 FROM presentation_translations translated WHERE translated.presentation_run_id=r.id AND translated.status='succeeded'))` + cutoverCondition + `
		ORDER BY r.completed_at,r.id`
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		runID                    int64
		title, summary, category string
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.runID, &item.title, &item.summary, &item.category); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate category verification candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	total := 0
	for _, item := range candidates {
		queued, err := queueCategoryVerificationTx(ctx, tx, item.runID, item.title, item.summary, item.category, plan, requestKind, false, now)
		if err != nil {
			return 0, err
		}
		if queued {
			total++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

func queueCategoryVerificationTx(ctx context.Context, tx *sql.Tx, runID int64, title, summary, category string, plan CategoryVerificationPlan, requestKind string, force bool, now time.Time) (bool, error) {
	plan.Model = strings.TrimSpace(plan.Model)
	plan.PromptVersion = strings.TrimSpace(plan.PromptVersion)
	category = strings.TrimSpace(category)
	if plan.Model == "" || plan.PromptVersion == "" || strings.TrimSpace(title) == "" || strings.TrimSpace(summary) == "" || category == "" {
		return false, errors.New("category verification inputs, prompt, and model are required")
	}
	var active, succeeded int
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status IN ('pending','running') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0)
		FROM presentation_category_verifications WHERE presentation_run_id=?`, runID).Scan(&active, &succeeded); err != nil {
		return false, err
	}
	if active > 0 || (!force && succeeded > 0) {
		return false, nil
	}
	formatted := formatTime(now.UTC())
	inputHash := HashPipelineInput("title_de", title, "summary_de", summary, "category", category, plan.PromptVersion, plan.Model)
	_, err := tx.ExecContext(ctx, `INSERT INTO presentation_category_verifications(
		presentation_run_id,request_kind,status,input_category,model_identity,prompt_version,input_hash,created_at,updated_at)
		VALUES(?,?,'pending',?,?,?,?,?,?)`, runID, requestKind, category, plan.Model, plan.PromptVersion, inputHash, formatted, formatted)
	return err == nil, err
}

// ClaimCategoryVerificationJob atomically claims the next eligible verifier
// job and drops work belonging to obsolete source revisions.
func (s *Store) ClaimCategoryVerificationJob(ctx context.Context, allowScheduled bool, blockedModels []string, now time.Time) (CategoryVerificationJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CategoryVerificationJob{}, false, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE presentation_category_verifications SET status='superseded',completed_at=?,updated_at=?
		WHERE status='pending' AND presentation_run_id IN (
			SELECT r.id FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id WHERE r.source_hash<>i.content_hash OR r.status<>'complete'
		)`, formatted, formatted); err != nil {
		return CategoryVerificationJob{}, false, err
	}
	blockedCondition := ""
	args := []any{formatted, boolInt(allowScheduled)}
	if len(blockedModels) > 0 {
		blockedCondition = ` AND verification.model_identity NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(blockedModels)), ",") + `)`
		for _, model := range blockedModels {
			args = append(args, model)
		}
	}
	var job CategoryVerificationJob
	err = tx.QueryRowContext(ctx, `SELECT verification.id,verification.presentation_run_id,r.incident_id,verification.request_kind,
		verification.input_category,verification.model_identity,verification.prompt_version,verification.input_hash,verification.attempt_count,
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='title_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='summary_de' LIMIT 1),'')
		FROM presentation_category_verifications verification JOIN presentation_runs r ON r.id=verification.presentation_run_id
		WHERE verification.status='pending' AND (verification.next_retry_at IS NULL OR verification.next_retry_at<=?)
		AND (verification.request_kind='manual' OR ?)`+blockedCondition+`
		ORDER BY CASE verification.request_kind WHEN 'manual' THEN 0 WHEN 'scheduled' THEN 1 ELSE 2 END,verification.created_at,verification.id LIMIT 1`, args...).Scan(
		&job.ID, &job.PresentationRunID, &job.IncidentID, &job.RequestKind, &job.InputCategory,
		&job.ModelIdentity, &job.PromptVersion, &job.InputHash, &job.AttemptCount, &job.TitleDE, &job.SummaryDE,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return CategoryVerificationJob{}, false, err
		}
		return CategoryVerificationJob{}, false, nil
	}
	if err != nil {
		return CategoryVerificationJob{}, false, err
	}
	job.AttemptCount++
	result, err := tx.ExecContext(ctx, `UPDATE presentation_category_verifications SET status='running',attempt_count=?,next_retry_at=NULL,
		failure_kind=NULL,error_message=NULL,started_at=?,updated_at=? WHERE id=? AND status='pending'`, job.AttemptCount, formatted, formatted, job.ID)
	if err != nil {
		return CategoryVerificationJob{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return CategoryVerificationJob{}, false, errors.New("category verification job was claimed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return CategoryVerificationJob{}, false, err
	}
	return job, true, nil
}

func (s *Store) CompleteCategoryVerificationJob(ctx context.Context, job CategoryVerificationJob, isCorrect bool, correctedCategory, modelIdentity, inputHash string, now time.Time) error {
	formatted := formatTime(now.UTC())
	result, err := s.db.ExecContext(ctx, `UPDATE presentation_category_verifications SET status='succeeded',is_correct=?,corrected_category=?,
		model_identity=?,input_hash=?,completed_at=?,updated_at=? WHERE id=? AND status='running'`,
		boolInt(isCorrect), correctedCategory, modelIdentity, inputHash, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("category verification job is no longer running")
	}
	return nil
}

func (s *Store) FailCategoryVerificationJob(ctx context.Context, job CategoryVerificationJob, status, failureKind string, retryAt *time.Time, now time.Time, processingError error) error {
	if status != "pending" && status != "needs_review" && status != "failed" {
		return errors.New("invalid category verification failure status")
	}
	var retry any
	if retryAt != nil {
		retry = formatTime(retryAt.UTC())
	}
	formatted := formatTime(now.UTC())
	result, err := s.db.ExecContext(ctx, `UPDATE presentation_category_verifications SET status=?,next_retry_at=?,failure_kind=?,error_message=?,
		completed_at=CASE WHEN ? IN ('failed','needs_review') THEN ? ELSE completed_at END,updated_at=? WHERE id=? AND status='running'`,
		status, retry, failureKind, sanitizedError(processingError), status, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("category verification job is no longer running")
	}
	return nil
}

func (s *Store) RecoverCategoryVerifications(ctx context.Context, now time.Time) error {
	formatted := formatTime(now.UTC())
	_, err := s.db.ExecContext(ctx, `UPDATE presentation_category_verifications SET status='pending',next_retry_at=?,failure_kind='transient',
		error_message='worker restarted during category verification',updated_at=? WHERE status='running'`, formatted, formatted)
	return err
}
