package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsureTranslationLanguages records the first automatic-enablement time for
// each registered language without moving an existing language's cutover.
func (s *Store) EnsureTranslationLanguages(ctx context.Context, languages []string, now time.Time) error {
	formatted := formatTime(now.UTC())
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			return errors.New("translation language is required")
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO translation_language_cutovers(language_code,automatic_after) VALUES(?,?) ON CONFLICT(language_code) DO NOTHING`, language, formatted); err != nil {
			return fmt.Errorf("ensure translation language %s: %w", language, err)
		}
	}
	return nil
}

// QueueTranslationsForRun creates independent jobs for one completed canonical
// presentation. Existing active or equivalent successful work is preserved,
// while pending translations for older canonical runs are superseded.
func (s *Store) QueueTranslationsForRun(ctx context.Context, runID int64, plans []TranslationPlan, requestKind string, now time.Time) (int, error) {
	return s.queueTranslationsForRun(ctx, runID, plans, requestKind, false, now)
}

func (s *Store) queueTranslationsForRun(ctx context.Context, runID int64, plans []TranslationPlan, requestKind string, force bool, now time.Time) (int, error) {
	if requestKind != "scheduled" && requestKind != "manual" && requestKind != "backfill" {
		return 0, errors.New("invalid translation request kind")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var title, summary string
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='title_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='summary_de' LIMIT 1),'')
		FROM presentation_runs r WHERE r.id=? AND r.status='complete'`, runID).Scan(&title, &summary); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	count := 0
	for _, plan := range plans {
		queued, err := queueTranslationTx(ctx, tx, runID, title, summary, plan, requestKind, force, now)
		if err != nil {
			return 0, err
		}
		if queued {
			count++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// QueueIncidentTranslation requests one immediate target-language translation
// for the newest completed canonical presentation of the current incident.
func (s *Store) QueueIncidentTranslation(ctx context.Context, incidentID int64, plan TranslationPlan, now time.Time) (int, error) {
	return s.QueueIncidentTranslations(ctx, incidentID, []TranslationPlan{plan}, false, now)
}

// QueueIncidentTranslations queues one or more translation languages for the
// newest completed presentation. Force permits a new manual attempt after a
// successful translation while still deduplicating active work.
func (s *Store) QueueIncidentTranslations(ctx context.Context, incidentID int64, plans []TranslationPlan, force bool, now time.Time) (int, error) {
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
	return s.queueTranslationsForRun(ctx, runID, plans, "manual", force, now)
}

// QueueMissingTranslations scans only the newest canonical presentation for
// each current incident. Automatic work respects each language's enablement
// cutover; explicit backfills intentionally ignore it.
func (s *Store) QueueMissingTranslations(ctx context.Context, sourceMode string, plans []TranslationPlan, backfill bool, now time.Time) (int, error) {
	return s.QueueAllTranslations(ctx, sourceMode, plans, backfill, false, now)
}

// QueueAllTranslations scans the newest completed presentation for every
// current incident. Force permits explicit operator requests to retranslate
// completed languages; active jobs remain deduplicated.
func (s *Store) QueueAllTranslations(ctx context.Context, sourceMode string, plans []TranslationPlan, backfill, force bool, now time.Time) (int, error) {
	condition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	requestKind := "scheduled"
	if force {
		requestKind = "manual"
	} else if backfill {
		requestKind = "backfill"
	}
	total := 0
	for _, plan := range plans {
		if strings.TrimSpace(plan.Language) == "" || strings.TrimSpace(plan.Model) == "" || strings.TrimSpace(plan.PromptVersion) == "" {
			return 0, errors.New("translation language, model, and prompt are required")
		}
		cutoverCondition := ""
		if !backfill {
			cutoverCondition = ` AND julianday(r.completed_at) > julianday((SELECT automatic_after FROM translation_language_cutovers WHERE language_code=?))`
		}
		query := `SELECT r.id,
			COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='title_de' LIMIT 1),''),
			COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='summary_de' LIMIT 1),'')
			FROM presentation_runs r
			JOIN incidents i ON i.id=r.incident_id
			JOIN source_documents d ON d.id=i.source_document_id
			WHERE ` + condition + ` AND r.source_hash=i.content_hash AND r.status='complete'
			AND (r.pipeline_version IN ('` + PipelineVersion + `','` + PreviousPipelineVersion + `') OR r.legacy=1)
			AND r.id=(SELECT candidate.id FROM presentation_runs candidate
				WHERE candidate.incident_id=i.id AND candidate.source_hash=i.content_hash AND candidate.status='complete'
				AND (candidate.pipeline_version IN ('` + PipelineVersion + `','` + PreviousPipelineVersion + `') OR candidate.legacy=1)
				ORDER BY CASE candidate.pipeline_version WHEN '` + PipelineVersion + `' THEN 0 WHEN '` + PreviousPipelineVersion + `' THEN 1 ELSE 2 END,
				candidate.completed_at DESC, candidate.id DESC LIMIT 1)` + cutoverCondition +
			` ORDER BY r.completed_at, r.id`
		var queryArgs []any
		if !backfill {
			queryArgs = append(queryArgs, plan.Language)
		}
		rows, err := tx.QueryContext(ctx, query, queryArgs...)
		if err != nil {
			return 0, err
		}
		type candidate struct {
			runID          int64
			title, summary string
		}
		var candidates []candidate
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.runID, &item.title, &item.summary); err != nil {
				rows.Close()
				return 0, err
			}
			candidates = append(candidates, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, fmt.Errorf("iterate %s translation candidates: %w", plan.Language, err)
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		for _, item := range candidates {
			queued, err := queueTranslationTx(ctx, tx, item.runID, item.title, item.summary, plan, requestKind, force, now)
			if err != nil {
				return 0, err
			}
			if queued {
				total++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

func queueTranslationTx(ctx context.Context, tx *sql.Tx, runID int64, title, summary string, plan TranslationPlan, requestKind string, force bool, now time.Time) (bool, error) {
	plan.Language = strings.TrimSpace(plan.Language)
	plan.PromptVersion = strings.TrimSpace(plan.PromptVersion)
	plan.Model = strings.TrimSpace(plan.Model)
	if plan.Language == "" || plan.PromptVersion == "" || plan.Model == "" {
		return false, errors.New("translation language, prompt, and model are required")
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(summary) == "" {
		return false, errors.New("canonical German presentation is incomplete")
	}
	formatted := formatTime(now.UTC())
	if err := supersedeOlderPendingTranslationsTx(ctx, tx, runID, plan.Language, formatted); err != nil {
		return false, err
	}
	inputHash := HashPipelineInput("title_de", title, "summary_de", summary, "language", plan.Language, plan.PromptVersion, plan.Model)
	var active, succeeded int
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status IN ('pending','running') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0)
		FROM presentation_translations WHERE presentation_run_id=? AND language_code=?`,
		runID, plan.Language).Scan(&active, &succeeded); err != nil {
		return false, err
	}
	if active > 0 || (!force && succeeded > 0) {
		return false, nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO presentation_translations(
		presentation_run_id,language_code,request_kind,status,model_identity,prompt_version,input_hash,created_at,updated_at
	) VALUES(?,?,?,'pending',?,?,?,?,?)`, runID, plan.Language, requestKind, plan.Model, plan.PromptVersion, inputHash, formatted, formatted)
	if err != nil {
		return false, fmt.Errorf("queue %s translation: %w", plan.Language, err)
	}
	return true, nil
}

// supersedeOlderPendingTranslationsTx prevents a completed canonical rerun
// from spending translation capacity on a presentation that can no longer be
// selected ahead of the newly queued run. Running and completed attempts remain
// untouched so in-flight workers and historical fallbacks stay valid.
func supersedeOlderPendingTranslationsTx(ctx context.Context, tx *sql.Tx, runID int64, language, formatted string) error {
	_, err := tx.ExecContext(ctx, `UPDATE presentation_translations
		SET status='superseded',completed_at=?,updated_at=?
		WHERE status='pending' AND language_code=? AND presentation_run_id IN (
			SELECT older.id FROM presentation_runs older
			JOIN presentation_runs current ON current.id=?
			WHERE older.id<>current.id AND older.incident_id=current.incident_id
			AND older.source_hash=current.source_hash AND older.status='complete'
			AND (older.pipeline_version IN (?,?) OR older.legacy=1)
			AND current.id=(SELECT candidate.id FROM presentation_runs candidate
				WHERE candidate.incident_id=current.incident_id AND candidate.source_hash=current.source_hash
				AND candidate.status='complete' AND (candidate.pipeline_version IN (?,?) OR candidate.legacy=1)
				ORDER BY CASE candidate.pipeline_version WHEN ? THEN 0 WHEN ? THEN 1 ELSE 2 END,
				candidate.completed_at DESC,candidate.id DESC LIMIT 1)
		)`, formatted, formatted, language, runID,
		PipelineVersion, PreviousPipelineVersion, PipelineVersion, PreviousPipelineVersion, PipelineVersion, PreviousPipelineVersion)
	if err != nil {
		return fmt.Errorf("supersede older pending %s translations: %w", language, err)
	}
	return nil
}

// ClaimTranslationJob atomically claims the next eligible translation while
// skipping model identities whose worker circuit is open. Stale source
// revisions are superseded before selection.
func (s *Store) ClaimTranslationJob(ctx context.Context, allowScheduled bool, blockedModels []string, now time.Time) (TranslationJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TranslationJob{}, false, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE presentation_translations SET status='superseded',completed_at=?,updated_at=?
		WHERE status='pending' AND presentation_run_id IN (
			SELECT r.id FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id WHERE r.source_hash<>i.content_hash OR r.status<>'complete'
		)`, formatted, formatted); err != nil {
		return TranslationJob{}, false, err
	}
	blockedCondition := ""
	queryArgs := []any{formatted, boolInt(allowScheduled)}
	if len(blockedModels) > 0 {
		blockedCondition = ` AND t.model_identity NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(blockedModels)), ",") + `)`
		for _, model := range blockedModels {
			queryArgs = append(queryArgs, model)
		}
	}
	var job TranslationJob
	err = tx.QueryRowContext(ctx, `SELECT t.id,t.presentation_run_id,r.incident_id,t.language_code,t.request_kind,
		t.model_identity,t.prompt_version,t.input_hash,t.attempt_count,
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='title_de' LIMIT 1),''),
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=r.id AND kind='summary_de' LIMIT 1),'')
		FROM presentation_translations t JOIN presentation_runs r ON r.id=t.presentation_run_id
		WHERE t.status='pending' AND (t.next_retry_at IS NULL OR t.next_retry_at<=?)
		AND (t.request_kind='manual' OR ?)`+blockedCondition+`
		ORDER BY CASE t.request_kind WHEN 'manual' THEN 0 WHEN 'scheduled' THEN 1 ELSE 2 END,t.created_at,t.id LIMIT 1`,
		queryArgs...).Scan(&job.ID, &job.PresentationRunID, &job.IncidentID, &job.Language, &job.RequestKind,
		&job.ModelIdentity, &job.PromptVersion, &job.InputHash, &job.AttemptCount, &job.TitleDE, &job.SummaryDE)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return TranslationJob{}, false, err
		}
		return TranslationJob{}, false, nil
	}
	if err != nil {
		return TranslationJob{}, false, err
	}
	job.AttemptCount++
	result, err := tx.ExecContext(ctx, `UPDATE presentation_translations SET status='running',attempt_count=?,next_retry_at=NULL,
		failure_kind=NULL,error_message=NULL,started_at=?,updated_at=? WHERE id=? AND status='pending'`, job.AttemptCount, formatted, formatted, job.ID)
	if err != nil {
		return TranslationJob{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return TranslationJob{}, false, errors.New("translation job was claimed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return TranslationJob{}, false, err
	}
	return job, true, nil
}

// CompleteTranslationJob publishes validated translated text only if the
// claimed attempt is still running.
func (s *Store) CompleteTranslationJob(ctx context.Context, job TranslationJob, title, summary, modelIdentity, inputHash string, now time.Time) error {
	formatted := formatTime(now.UTC())
	result, err := s.db.ExecContext(ctx, `UPDATE presentation_translations SET status='succeeded',title=?,summary=?,
		model_identity=?,input_hash=?,completed_at=?,updated_at=? WHERE id=? AND status='running'`,
		title, summary, modelIdentity, inputHash, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("translation job is no longer running")
	}
	return nil
}

// FailTranslationJob applies a retry or terminal review transition only to the
// currently running attempt and stores a sanitized error message.
func (s *Store) FailTranslationJob(ctx context.Context, job TranslationJob, status, failureKind string, retryAt *time.Time, now time.Time, processingError error) error {
	if status != "pending" && status != "needs_review" && status != "failed" {
		return errors.New("invalid translation failure status")
	}
	var retry any
	if retryAt != nil {
		retry = formatTime(retryAt.UTC())
	}
	formatted := formatTime(now.UTC())
	result, err := s.db.ExecContext(ctx, `UPDATE presentation_translations SET status=?,next_retry_at=?,failure_kind=?,error_message=?,
		completed_at=CASE WHEN ? IN ('failed','needs_review') THEN ? ELSE completed_at END,updated_at=? WHERE id=? AND status='running'`,
		status, retry, failureKind, sanitizedError(processingError), status, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("translation job is no longer running")
	}
	return nil
}

// RecoverTranslations makes attempts interrupted by worker shutdown claimable.
func (s *Store) RecoverTranslations(ctx context.Context, now time.Time) error {
	formatted := formatTime(now.UTC())
	_, err := s.db.ExecContext(ctx, `UPDATE presentation_translations SET status='pending',next_retry_at=?,failure_kind='transient',
		error_message='worker restarted during translation',updated_at=? WHERE status='running'`, formatted, formatted)
	return err
}
