package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsurePostProcessingScopes records the first automatic-enablement time and
// default gate for every registered scope without overwriting operator choices.
func (s *Store) EnsurePostProcessingScopes(ctx context.Context, scopes []PostProcessingScope, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin post-processing scope initialization: %w", err)
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope.ProcessorKey = strings.TrimSpace(scope.ProcessorKey)
		scope.ScopeKey = strings.TrimSpace(scope.ScopeKey)
		if scope.ProcessorKey == "" || scope.ScopeKey == "" {
			return errors.New("post-processing processor and scope are required")
		}
		identity := scope.ProcessorKey + "\x00" + scope.ScopeKey
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_processing_processor_controls(processor_key,enabled,enabled_updated_at) VALUES(?,1,?) ON CONFLICT(processor_key) DO NOTHING`, scope.ProcessorKey, formatted); err != nil {
			return fmt.Errorf("ensure post-processing processor %s: %w", scope.ProcessorKey, err)
		}
		enabled := scope.ProcessorKey != "translation" || scope.ScopeKey == "en"
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_processing_scopes(processor_key,scope_key,automatic_after,enabled,enabled_updated_at) VALUES(?,?,?,?,?) ON CONFLICT(processor_key,scope_key) DO NOTHING`, scope.ProcessorKey, scope.ScopeKey, formatted, boolInt(enabled), formatted); err != nil {
			return fmt.Errorf("ensure post-processing scope %s/%s: %w", scope.ProcessorKey, scope.ScopeKey, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit post-processing scope initialization: %w", err)
	}
	return nil
}

// PostProcessingProcessorSettings lists the processor-wide automatic-work
// gates without triggering discovery.
func (s *Store) PostProcessingProcessorSettings(ctx context.Context) ([]PostProcessingProcessorSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT processor_key,enabled,enabled_updated_at FROM post_processing_processor_controls ORDER BY processor_key`)
	if err != nil {
		return nil, fmt.Errorf("list post-processing processor settings: %w", err)
	}
	defer rows.Close()
	var settings []PostProcessingProcessorSetting
	for rows.Next() {
		var setting PostProcessingProcessorSetting
		var enabled int
		var updatedAt string
		if err := rows.Scan(&setting.ProcessorKey, &enabled, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan post-processing processor setting: %w", err)
		}
		setting.Enabled = enabled == 1
		setting.EnabledUpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse post-processing processor update time: %w", err)
		}
		settings = append(settings, setting)
	}
	return settings, rows.Err()
}

// SetPostProcessingProcessorEnabled changes the processor-wide automatic gate
// and terminalizes its queued automatic work on disable.
func (s *Store) SetPostProcessingProcessorEnabled(ctx context.Context, processorKey string, enabled bool, now time.Time) (int, error) {
	processorKey = strings.TrimSpace(processorKey)
	if processorKey == "" {
		return 0, errors.New("post-processing processor is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	result, err := tx.ExecContext(ctx, `UPDATE post_processing_processor_controls SET enabled=?,enabled_updated_at=? WHERE processor_key=?`, boolInt(enabled), formatted, processorKey)
	if err != nil {
		return 0, fmt.Errorf("set post-processing processor enabled: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return 0, ErrNotFound
	}
	var skipped int64
	if !enabled {
		result, err = tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='skipped',status_reason=?,status_detail=NULL,next_retry_at=NULL,failure_kind=NULL,error_message=NULL,started_at=NULL,completed_at=?,updated_at=? WHERE processor_key=? AND request_kind='scheduled' AND status='pending'`, PostProcessingStatusReasonProcessorDisabled, formatted, formatted, processorKey)
		if err != nil {
			return 0, fmt.Errorf("skip disabled post-processing processor jobs: %w", err)
		}
		skipped, _ = result.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(skipped), nil
}

func postProcessingProcessorEnabledTx(ctx context.Context, tx *sql.Tx, processorKey string) (bool, error) {
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM post_processing_processor_controls WHERE processor_key=?`, processorKey).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	return enabled == 1, nil
}

// PostProcessingScopeSettings lists the automatic-work gates without changing
// their cutovers or triggering discovery.
func (s *Store) PostProcessingScopeSettings(ctx context.Context) ([]PostProcessingScopeSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT processor_key,scope_key,automatic_after,enabled,enabled_updated_at FROM post_processing_scopes ORDER BY processor_key,scope_key`)
	if err != nil {
		return nil, fmt.Errorf("list post-processing scope settings: %w", err)
	}
	defer rows.Close()
	var settings []PostProcessingScopeSetting
	for rows.Next() {
		var setting PostProcessingScopeSetting
		var automaticAfter, updatedAt string
		var enabled int
		if err := rows.Scan(&setting.ProcessorKey, &setting.ScopeKey, &automaticAfter, &enabled, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan post-processing scope setting: %w", err)
		}
		setting.Enabled = enabled == 1
		setting.AutomaticAfter, err = time.Parse(time.RFC3339Nano, automaticAfter)
		if err != nil {
			return nil, fmt.Errorf("parse post-processing automatic cutover: %w", err)
		}
		setting.EnabledUpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse post-processing enabled update time: %w", err)
		}
		settings = append(settings, setting)
	}
	return settings, rows.Err()
}

// SetPostProcessingScopeEnabled changes automatic eligibility and terminalizes
// queued automatic work on disable. Running and manual work are left intact.
func (s *Store) SetPostProcessingScopeEnabled(ctx context.Context, processorKey, scopeKey string, enabled bool, now time.Time) (int, error) {
	processorKey, scopeKey = strings.TrimSpace(processorKey), strings.TrimSpace(scopeKey)
	if processorKey == "" || scopeKey == "" {
		return 0, errors.New("post-processing processor and scope are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	result, err := tx.ExecContext(ctx, `UPDATE post_processing_scopes SET enabled=?,enabled_updated_at=? WHERE processor_key=? AND scope_key=?`, boolInt(enabled), formatted, processorKey, scopeKey)
	if err != nil {
		return 0, fmt.Errorf("set post-processing scope enabled: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return 0, ErrNotFound
	}
	skipped := int64(0)
	if !enabled {
		result, err = tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='skipped',status_reason=?,status_detail=NULL,next_retry_at=NULL,failure_kind=NULL,error_message=NULL,started_at=NULL,completed_at=?,updated_at=? WHERE processor_key=? AND scope_key=? AND request_kind='scheduled' AND status='pending'`, PostProcessingStatusReasonScopeDisabled, formatted, formatted, processorKey, scopeKey)
		if err != nil {
			return 0, fmt.Errorf("skip disabled post-processing jobs: %w", err)
		}
		skipped, _ = result.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(skipped), nil
}

func postProcessingScopeEnabledTx(ctx context.Context, tx *sql.Tx, processorKey, scopeKey string) (bool, error) {
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM post_processing_scopes WHERE processor_key=? AND scope_key=?`, processorKey, scopeKey).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	return enabled == 1, nil
}

// QueuePostProcessingForRun creates independent jobs for one completed current
// canonical presentation. The store enforces the durable switch for scheduled
// callers; the worker separately authorizes their time window at its clock
// boundary because schedule configuration is not persisted here.
func (s *Store) QueuePostProcessingForRun(ctx context.Context, runID int64, plans []PostProcessingPlan, requestKind string, force bool, now time.Time) (int, error) {
	if requestKind != "scheduled" && requestKind != "manual" {
		return 0, errors.New("invalid post-processing request kind")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if requestKind == "scheduled" {
		enabled, err := automaticProcessingEnabledTx(ctx, tx)
		if err != nil {
			return 0, err
		}
		if !enabled {
			if err := tx.Commit(); err != nil {
				return 0, err
			}
			return 0, nil
		}
	}
	var incidentID int64
	var sourceHash string
	if err := tx.QueryRowContext(ctx, `SELECT r.incident_id,r.source_hash FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id WHERE r.id=? AND r.status='complete' AND r.pipeline_version=? AND r.legacy=0 AND r.source_hash=i.content_hash`, runID, PipelineVersion).Scan(&incidentID, &sourceHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	count := 0
	for _, plan := range plans {
		if requestKind == "scheduled" {
			enabled, err := postProcessingProcessorEnabledTx(ctx, tx, plan.ProcessorKey)
			if err != nil {
				return 0, err
			}
			if !enabled {
				continue
			}
			enabled, err = postProcessingScopeEnabledTx(ctx, tx, plan.ProcessorKey, plan.ScopeKey)
			if err != nil {
				return 0, err
			}
			if !enabled {
				continue
			}
		}
		queued, err := queuePostProcessingTx(ctx, tx, runID, incidentID, sourceHash, plan, requestKind, force, now)
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

// QueueIncidentPostProcessing queues forced manual jobs for the newest current
// v2 presentation of one incident.
func (s *Store) QueueIncidentPostProcessing(ctx context.Context, incidentID int64, plans []PostProcessingPlan, now time.Time) (int, error) {
	if incidentID < 1 {
		return 0, errors.New("incident ID must be positive")
	}
	var runID int64
	err := s.db.QueryRowContext(ctx, `SELECT r.id FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id
		WHERE i.id=? AND r.source_hash=i.content_hash AND r.status='complete' AND r.pipeline_version=? AND r.legacy=0
		ORDER BY r.completed_at DESC,r.id DESC LIMIT 1`, incidentID, PipelineVersion).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return s.QueuePostProcessingForRun(ctx, runID, plans, "manual", true, now)
}

// QueuePostProcessingForAll scans the newest current v2 presentation for each
// incident. Automatic work respects scope cutovers; forced manual work does not.
func (s *Store) QueuePostProcessingForAll(ctx context.Context, sourceMode string, plans []PostProcessingPlan, force bool, now time.Time) (int, error) {
	requestKind := "scheduled"
	if force {
		requestKind = "manual"
	}
	return s.queuePostProcessingForAll(ctx, sourceMode, plans, requestKind, force, !force, false, now)
}

// QueueUnpublishedPostProcessingForAll creates manual translation work only
// where no successful job with a complete title and summary exists. It bypasses
// automatic cutovers and the scheduling master switch without replacing
// publishable results.
func (s *Store) QueueUnpublishedPostProcessingForAll(ctx context.Context, sourceMode string, plans []PostProcessingPlan, now time.Time) (int, error) {
	return s.queuePostProcessingForAll(ctx, sourceMode, plans, "manual", true, false, true, now)
}

func (s *Store) queuePostProcessingForAll(ctx context.Context, sourceMode string, plans []PostProcessingPlan, requestKind string, force, respectCutover, unpublishedTranslationsOnly bool, now time.Time) (int, error) {
	condition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if requestKind == "scheduled" {
		enabled, err := automaticProcessingEnabledTx(ctx, tx)
		if err != nil {
			return 0, err
		}
		if !enabled {
			if err := tx.Commit(); err != nil {
				return 0, err
			}
			return 0, nil
		}
	}
	total := 0
	for _, plan := range plans {
		if err := validatePostProcessingPlan(plan); err != nil {
			return 0, err
		}
		if unpublishedTranslationsOnly && plan.ProcessorKey != "translation" {
			return 0, errors.New("unpublished selection requires the translation processor")
		}
		if requestKind == "scheduled" {
			enabled, err := postProcessingProcessorEnabledTx(ctx, tx, plan.ProcessorKey)
			if err != nil {
				return 0, err
			}
			if !enabled {
				continue
			}
			enabled, err = postProcessingScopeEnabledTx(ctx, tx, plan.ProcessorKey, plan.ScopeKey)
			if err != nil {
				return 0, err
			}
			if !enabled {
				continue
			}
		}
		cutover := ""
		args := []any{PipelineVersion}
		if respectCutover {
			cutover = ` AND julianday(r.completed_at)>julianday((SELECT automatic_after FROM post_processing_scopes WHERE processor_key=? AND scope_key=?))`
			args = append(args, plan.ProcessorKey, plan.ScopeKey)
		}
		// Satisfied runs need no input loading or hashing. Keep runs with older
		// pending jobs eligible so queuePostProcessingTx still supersedes those
		// jobs, even when the newest run already has active or successful work.
		unsatisfied := ""
		if !force {
			unsatisfied = ` AND (NOT EXISTS (
				SELECT 1 FROM post_processing_jobs job
				WHERE job.presentation_run_id=r.id AND job.processor_key=? AND job.scope_key=?
				AND job.status IN ('pending','running','succeeded')
			) OR EXISTS (
				SELECT 1 FROM presentation_runs older JOIN post_processing_jobs job ON job.presentation_run_id=older.id
				WHERE older.id<>r.id AND older.incident_id=r.incident_id AND older.source_hash=r.source_hash
				AND older.pipeline_version=? AND older.legacy=0
				AND job.processor_key=? AND job.scope_key=? AND job.status='pending'
			))`
			args = append(args, plan.ProcessorKey, plan.ScopeKey, PipelineVersion, plan.ProcessorKey, plan.ScopeKey)
		}
		query := `SELECT r.id,r.incident_id,r.source_hash FROM presentation_runs r
			JOIN incidents i ON i.id=r.incident_id JOIN source_documents d ON d.id=i.source_document_id
			WHERE ` + condition + ` AND r.source_hash=i.content_hash AND r.status='complete' AND r.pipeline_version=? AND r.legacy=0
			AND r.id=(SELECT candidate.id FROM presentation_runs candidate
				WHERE candidate.incident_id=i.id AND candidate.source_hash=i.content_hash AND candidate.status='complete'
				AND candidate.pipeline_version='` + PipelineVersion + `' AND candidate.legacy=0
				ORDER BY candidate.completed_at DESC,candidate.id DESC LIMIT 1)` + cutover + unsatisfied + ` ORDER BY r.completed_at,r.id`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		type candidate struct {
			runID, incidentID int64
			sourceHash        string
		}
		var candidates []candidate
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.runID, &item.incidentID, &item.sourceHash); err != nil {
				rows.Close()
				return 0, err
			}
			candidates = append(candidates, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		for _, item := range candidates {
			if unpublishedTranslationsOnly {
				published, err := publishableTranslationExistsTx(ctx, tx, item.runID, plan.ScopeKey)
				if err != nil {
					return 0, err
				}
				if published {
					continue
				}
			}
			queued, err := queuePostProcessingTx(ctx, tx, item.runID, item.incidentID, item.sourceHash, plan, requestKind, force, now)
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

func publishableTranslationExistsTx(ctx context.Context, tx *sql.Tx, runID int64, scopeKey string) (bool, error) {
	var published bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM post_processing_jobs job
		WHERE job.presentation_run_id=? AND job.processor_key='translation' AND job.scope_key=? AND job.status='succeeded'
		AND EXISTS (SELECT 1 FROM post_processing_values value WHERE value.job_id=job.id AND value.kind='title' AND trim(value.value)<>'')
		AND EXISTS (SELECT 1 FROM post_processing_values value WHERE value.job_id=job.id AND value.kind='summary' AND trim(value.value)<>'')
	)`, runID, scopeKey).Scan(&published)
	if err != nil {
		return false, fmt.Errorf("check publishable translation: %w", err)
	}
	return published, nil
}

func queuePostProcessingTx(ctx context.Context, tx *sql.Tx, runID, incidentID int64, sourceHash string, plan PostProcessingPlan, requestKind string, force bool, now time.Time) (bool, error) {
	if err := validatePostProcessingPlan(plan); err != nil {
		return false, err
	}
	values, unavailableKind, err := postProcessingInputValuesTx(ctx, tx, runID, plan.InputKinds)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	formatted := formatTime(now.UTC())
	inputHash := postProcessingInputHash(plan.ProcessorKey, plan.ScopeKey, plan.InputKinds, values, plan.PromptVersion, plan.Model, plan.AdapterKey)
	if _, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='superseded',completed_at=?,updated_at=?
		WHERE status='pending' AND processor_key=? AND scope_key=? AND presentation_run_id IN (
			SELECT older.id FROM presentation_runs older
			WHERE older.id<>? AND older.incident_id=? AND older.source_hash=? AND older.pipeline_version=? AND older.legacy=0
		)`, formatted, formatted, plan.ProcessorKey, plan.ScopeKey, runID, incidentID, sourceHash, PipelineVersion); err != nil {
		return false, fmt.Errorf("supersede older pending post-processing jobs: %w", err)
	}
	var active, succeeded, matchingSkip int
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status IN ('pending','running') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='skipped' AND status_reason=? AND input_hash=? THEN 1 ELSE 0 END),0)
		FROM post_processing_jobs WHERE presentation_run_id=? AND processor_key=? AND scope_key=?`, PostProcessingStatusReasonMissingInput, inputHash, runID, plan.ProcessorKey, plan.ScopeKey).Scan(&active, &succeeded, &matchingSkip); err != nil {
		return false, err
	}
	if active > 0 || (!force && (succeeded > 0 || matchingSkip > 0)) {
		return false, nil
	}
	if unavailableKind != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO post_processing_jobs(
			presentation_run_id,processor_key,scope_key,request_kind,status,status_reason,status_detail,
			model_identity,adapter_key,prompt_version,input_hash,completed_at,created_at,updated_at
		) VALUES(?,?,?,?,'skipped',?,?,?,?,?,?,?,?,?)`, runID, plan.ProcessorKey, plan.ScopeKey, requestKind,
			PostProcessingStatusReasonMissingInput, unavailableKind, plan.Model, normalizedAdapterKey(plan.AdapterKey), plan.PromptVersion, inputHash, formatted, formatted, formatted)
		if err != nil {
			return false, fmt.Errorf("record skipped %s/%s: %w", plan.ProcessorKey, plan.ScopeKey, err)
		}
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO post_processing_jobs(
		presentation_run_id,processor_key,scope_key,request_kind,status,model_identity,adapter_key,prompt_version,input_hash,created_at,updated_at
	) VALUES(?,?,?,?,'pending',?,?,?,?,?,?)`, runID, plan.ProcessorKey, plan.ScopeKey, requestKind, plan.Model, normalizedAdapterKey(plan.AdapterKey), plan.PromptVersion, inputHash, formatted, formatted)
	if err != nil {
		return false, fmt.Errorf("queue %s/%s: %w", plan.ProcessorKey, plan.ScopeKey, err)
	}
	return true, nil
}

func validatePostProcessingPlan(plan PostProcessingPlan) error {
	if strings.TrimSpace(plan.ProcessorKey) == "" || strings.TrimSpace(plan.ScopeKey) == "" || strings.TrimSpace(plan.PromptVersion) == "" || strings.TrimSpace(plan.Model) == "" || len(plan.InputKinds) == 0 {
		return errors.New("post-processing processor, scope, prompt, model, and inputs are required")
	}
	seen := make(map[string]struct{}, len(plan.InputKinds))
	for _, kind := range plan.InputKinds {
		if strings.TrimSpace(kind) == "" {
			return errors.New("post-processing input kind is required")
		}
		if _, duplicate := seen[kind]; duplicate {
			return errors.New("post-processing input kinds must be unique")
		}
		seen[kind] = struct{}{}
	}
	return nil
}

func normalizedAdapterKey(adapter string) string {
	if adapter = strings.TrimSpace(adapter); adapter != "" {
		return adapter
	}
	return "structured"
}

func postProcessingInputHash(processorKey, scopeKey string, kinds []string, values map[string]string, promptVersion, model, adapter string) string {
	hashParts := []string{"processor", processorKey, "scope", scopeKey}
	for _, kind := range kinds {
		hashParts = append(hashParts, kind, values[kind])
	}
	hashParts = append(hashParts, promptVersion, model)
	if adapter = normalizedAdapterKey(adapter); adapter != "structured" {
		hashParts = append(hashParts, "adapter", adapter)
	}
	return HashPipelineInput(hashParts...)
}

func postProcessingInputValuesTx(ctx context.Context, tx *sql.Tx, runID int64, kinds []string) (map[string]string, string, error) {
	values := make(map[string]string, len(kinds))
	present := make(map[string]bool, len(kinds))
	needOriginalTitle, needIncidentBody := false, false
	presentationKinds := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		switch kind {
		case "original_title":
			needOriginalTitle = true
		case "incident_body":
			needIncidentBody = true
		default:
			presentationKinds = append(presentationKinds, kind)
		}
	}
	if needOriginalTitle || needIncidentBody {
		var originalTitle, incidentBody string
		if err := tx.QueryRowContext(ctx, `SELECT i.title_de,COALESCE(i.body_de,'') FROM presentation_runs r
			JOIN incidents i ON i.id=r.incident_id AND i.content_hash=r.source_hash WHERE r.id=?`, runID).Scan(&originalTitle, &incidentBody); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "", ErrNotFound
			}
			return nil, "", err
		}
		if needOriginalTitle {
			values["original_title"], present["original_title"] = originalTitle, true
		}
		if needIncidentBody {
			values["incident_body"], present["incident_body"] = incidentBody, true
		}
	}
	if len(presentationKinds) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(presentationKinds)), ",")
		args := make([]any, 0, len(presentationKinds)+1)
		args = append(args, runID)
		for _, kind := range presentationKinds {
			args = append(args, kind)
		}
		rows, err := tx.QueryContext(ctx, `SELECT kind,value FROM presentation_values WHERE presentation_run_id=? AND kind IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, "", err
		}
		for rows.Next() {
			var kind, value string
			if err := rows.Scan(&kind, &value); err != nil {
				rows.Close()
				return nil, "", err
			}
			values[kind] = value
			present[kind] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, "", err
		}
		if err := rows.Close(); err != nil {
			return nil, "", err
		}
	}
	for _, kind := range kinds {
		if !present[kind] || strings.TrimSpace(values[kind]) == "" {
			return values, kind, nil
		}
	}
	return values, "", nil
}

// ClaimPostProcessingJob atomically claims the next eligible job for a
// registered processor. Inputs are materialized only when the queued prompt
// still matches the registered contract, allowing stale jobs to be claimed and
// failed by the worker's prompt-version guard after a deployment.
func (s *Store) ClaimPostProcessingJob(ctx context.Context, processorKey string, contracts PostProcessingContract, options PostProcessingClaimOptions, now time.Time) (PostProcessingJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PostProcessingJob{}, false, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='superseded',completed_at=?,updated_at=?
		WHERE status='pending' AND presentation_run_id IN (
			SELECT r.id FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id
			WHERE r.source_hash<>i.content_hash OR r.status<>'complete' OR r.pipeline_version<>? OR r.legacy<>0
		)`, formatted, formatted, PipelineVersion); err != nil {
		return PostProcessingJob{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='skipped',status_reason=?,status_detail=NULL,
		next_retry_at=NULL,failure_kind=NULL,error_message=NULL,started_at=NULL,completed_at=?,updated_at=?
		WHERE processor_key=? AND request_kind='scheduled' AND status='pending'
		AND EXISTS (SELECT 1 FROM post_processing_scopes scope WHERE scope.processor_key=post_processing_jobs.processor_key
			AND scope.scope_key=post_processing_jobs.scope_key AND scope.enabled=0)`,
		PostProcessingStatusReasonScopeDisabled, formatted, formatted, processorKey); err != nil {
		return PostProcessingJob{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='skipped',status_reason=?,status_detail=NULL,
		next_retry_at=NULL,failure_kind=NULL,error_message=NULL,started_at=NULL,completed_at=?,updated_at=?
		WHERE processor_key=? AND request_kind='scheduled' AND status='pending'
		AND EXISTS (SELECT 1 FROM post_processing_processor_controls processor
			WHERE processor.processor_key=post_processing_jobs.processor_key AND processor.enabled=0)`,
		PostProcessingStatusReasonProcessorDisabled, formatted, formatted, processorKey); err != nil {
		return PostProcessingJob{}, false, err
	}
	for {
		job, err := selectPostProcessingJobTx(ctx, tx, processorKey, options, now, nil)
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.Commit(); err != nil {
				return PostProcessingJob{}, false, err
			}
			return PostProcessingJob{}, false, nil
		}
		if err != nil {
			return PostProcessingJob{}, false, err
		}
		contract, registered := contracts[job.ScopeKey]
		if !registered || contract.PromptVersion == "" || len(contract.InputKinds) == 0 || len(contract.OutputKinds) == 0 {
			return PostProcessingJob{}, false, fmt.Errorf("post-processing scope %s/%s has no valid registered value contract", job.ProcessorKey, job.ScopeKey)
		}
		job.InputValues = make(map[string]string)
		if contract.PromptVersion == job.PromptVersion {
			job.OutputKinds = append([]string(nil), contract.OutputKinds...)
			var unavailableKind string
			job.InputValues, unavailableKind, err = postProcessingInputValuesTx(ctx, tx, job.PresentationRunID, contract.InputKinds)
			if err != nil {
				return PostProcessingJob{}, false, fmt.Errorf("load claimed post-processing inputs: %w", err)
			}
			if unavailableKind != "" {
				inputHash := postProcessingInputHash(job.ProcessorKey, job.ScopeKey, contract.InputKinds, job.InputValues, job.PromptVersion, job.ModelIdentity, job.AdapterKey)
				result, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='skipped',status_reason=?,status_detail=?,input_hash=?,
				next_retry_at=NULL,failure_kind=NULL,error_message=NULL,completed_at=?,updated_at=? WHERE id=? AND status='pending'`,
					PostProcessingStatusReasonMissingInput, unavailableKind, inputHash, formatted, formatted, job.ID)
				if err != nil {
					return PostProcessingJob{}, false, err
				}
				if affected, _ := result.RowsAffected(); affected != 1 {
					return PostProcessingJob{}, false, errors.New("post-processing job was claimed concurrently")
				}
				// Keep selecting in the same transaction so found=false means there
				// is no eligible job, rather than only a skipped candidate.
				continue
			}
		}
		job.AttemptCount++
		result, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='running',attempt_count=?,next_retry_at=NULL,
		failure_kind=NULL,error_message=NULL,started_at=?,updated_at=? WHERE id=? AND status='pending'`, job.AttemptCount, formatted, formatted, job.ID)
		if err != nil {
			return PostProcessingJob{}, false, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return PostProcessingJob{}, false, errors.New("post-processing job was claimed concurrently")
		}
		if err := tx.Commit(); err != nil {
			return PostProcessingJob{}, false, err
		}
		return job, true, nil
	}
}

// CompletePostProcessingJob stores validated named values and succeeds the job
// atomically, leaving older successful attempts available until this commit.
func (s *Store) CompletePostProcessingJob(ctx context.Context, job PostProcessingJob, values []PipelineValue, modelIdentity, inputHash string, now time.Time) error {
	if err := validatePostProcessingOutputs(values, job.OutputKinds); err != nil {
		return err
	}
	if strings.TrimSpace(modelIdentity) == "" || strings.TrimSpace(inputHash) == "" {
		return errors.New("post-processing model identity and input hash are required")
	}
	if inputHash != job.InputHash {
		return errors.New("post-processing input hash changed after claim")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, value := range values {
		if strings.TrimSpace(value.Kind) == "" || strings.TrimSpace(value.Value) == "" {
			return errors.New("post-processing output kind and value are required")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_processing_values(job_id,kind,value) VALUES(?,?,?)`, job.ID, value.Kind, value.Value); err != nil {
			return fmt.Errorf("store post-processing output %s: %w", value.Kind, err)
		}
	}
	formatted := formatTime(now.UTC())
	result, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='succeeded',model_identity=?,input_hash=?,completed_at=?,updated_at=? WHERE id=? AND status='running'`, modelIdentity, inputHash, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrJobNotRunning
	}
	return tx.Commit()
}

func validatePostProcessingOutputs(values []PipelineValue, expectedKinds []string) error {
	if len(expectedKinds) == 0 || len(values) != len(expectedKinds) {
		return errors.New("post-processing output values must exactly match the registered contract")
	}
	expected := make(map[string]struct{}, len(expectedKinds))
	for _, kind := range expectedKinds {
		if strings.TrimSpace(kind) == "" {
			return errors.New("post-processing output contract contains an empty kind")
		}
		if _, duplicate := expected[kind]; duplicate {
			return fmt.Errorf("post-processing output contract kind %q is duplicated", kind)
		}
		expected[kind] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Kind) == "" || strings.TrimSpace(value.Value) == "" {
			return errors.New("post-processing output kind and value are required")
		}
		if _, declared := expected[value.Kind]; !declared {
			return fmt.Errorf("post-processing output kind %q is not declared", value.Kind)
		}
		if _, duplicate := seen[value.Kind]; duplicate {
			return fmt.Errorf("post-processing output kind %q is duplicated", value.Kind)
		}
		seen[value.Kind] = struct{}{}
	}
	return nil
}

func (s *Store) FailPostProcessingJob(ctx context.Context, job PostProcessingJob, status, failureKind string, retryAt *time.Time, now time.Time, processingError error) error {
	if status != "pending" && status != "needs_review" && status != "failed" {
		return errors.New("invalid post-processing failure status")
	}
	var retry any
	if retryAt != nil {
		retry = formatTime(retryAt.UTC())
	}
	formatted := formatTime(now.UTC())
	result, err := s.db.ExecContext(ctx, `UPDATE post_processing_jobs SET status=?,next_retry_at=?,failure_kind=?,error_message=?,
		completed_at=CASE WHEN ? IN ('failed','needs_review') THEN ? ELSE completed_at END,updated_at=? WHERE id=? AND status='running'`,
		status, retry, failureKind, sanitizedError(processingError), status, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrJobNotRunning
	}
	return nil
}

func (s *Store) RecoverPostProcessing(ctx context.Context, now time.Time) error {
	formatted := formatTime(now.UTC())
	_, err := s.db.ExecContext(ctx, `UPDATE post_processing_jobs SET status='pending',next_retry_at=?,failure_kind='transient',
		error_message='worker restarted during post-processing',updated_at=? WHERE status='running'`, formatted, formatted)
	return err
}

// CyclePostProcessingPlans returns the concrete processor scopes frozen for a
// manual or continuation cycle.
func (s *Store) CyclePostProcessingPlans(ctx context.Context, cycleID int64) ([]PostProcessingPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT processor_key,scope_key,model_identity,adapter_key,prompt_version FROM cycle_post_processing_plans WHERE cycle_id=? ORDER BY processor_key,scope_key`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plans []PostProcessingPlan
	for rows.Next() {
		var plan PostProcessingPlan
		if err := rows.Scan(&plan.ProcessorKey, &plan.ScopeKey, &plan.Model, &plan.AdapterKey, &plan.PromptVersion); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

// PeekPostProcessingJob previews the next runnable candidate without claiming,
// skipping, or changing any job. It uses the claim's selection order and ignores
// candidates whose current inputs or registered contracts cannot run.
func (s *Store) PeekPostProcessingJob(ctx context.Context, processorKey string, contracts PostProcessingContract, options PostProcessingClaimOptions, now time.Time) (PostProcessingJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PostProcessingJob{}, false, err
	}
	defer tx.Rollback()
	var excluded []int64
	for {
		job, err := selectPostProcessingJobTx(ctx, tx, processorKey, options, now, excluded)
		if errors.Is(err, sql.ErrNoRows) {
			return PostProcessingJob{}, false, nil
		}
		if err != nil {
			return PostProcessingJob{}, false, err
		}
		contract, registered := contracts[job.ScopeKey]
		if registered && contract.PromptVersion == job.PromptVersion && len(contract.InputKinds) > 0 && len(contract.OutputKinds) > 0 {
			_, missing, err := postProcessingInputValuesTx(ctx, tx, job.PresentationRunID, contract.InputKinds)
			if err != nil {
				return PostProcessingJob{}, false, err
			}
			if missing == "" {
				return job, true, nil
			}
		}
		excluded = append(excluded, job.ID)
	}
}

func selectPostProcessingJobTx(ctx context.Context, tx *sql.Tx, processorKey string, options PostProcessingClaimOptions, now time.Time, excluded []int64) (PostProcessingJob, error) {
	formatted := formatTime(now.UTC())
	blockedCondition := ""
	args := []any{processorKey, PipelineVersion, formatted, boolInt(options.AllowScheduled)}
	if len(options.BlockedModels) > 0 {
		blockedCondition = ` AND job.model_identity NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(options.BlockedModels)), ",") + `)`
		for _, model := range options.BlockedModels {
			args = append(args, model)
		}
	}
	if len(excluded) > 0 {
		blockedCondition += ` AND job.id NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(excluded)), ",") + `)`
		for _, id := range excluded {
			args = append(args, id)
		}
	}
	args = append(args, options.YieldModel, options.PreferredModel)
	var job PostProcessingJob
	err := tx.QueryRowContext(ctx, `SELECT job.id,job.presentation_run_id,r.incident_id,job.processor_key,job.scope_key,job.request_kind,
		job.model_identity,job.adapter_key,job.prompt_version,job.input_hash,job.attempt_count
		FROM post_processing_jobs job JOIN presentation_runs r ON r.id=job.presentation_run_id
		JOIN incidents i ON i.id=r.incident_id AND i.content_hash=r.source_hash
		WHERE job.processor_key=? AND r.status='complete' AND r.pipeline_version=? AND r.legacy=0 AND job.status='pending' AND (job.next_retry_at IS NULL OR job.next_retry_at<=?)
		AND (job.request_kind='manual' OR (? AND (SELECT automatic_processing_enabled FROM ai_runtime_control WHERE id=1)=1
			AND EXISTS (SELECT 1 FROM post_processing_processor_controls processor WHERE processor.processor_key=job.processor_key AND processor.enabled=1)
			AND EXISTS (SELECT 1 FROM post_processing_scopes scope WHERE scope.processor_key=job.processor_key AND scope.scope_key=job.scope_key AND scope.enabled=1)))`+blockedCondition+`
		ORDER BY CASE job.request_kind WHEN 'manual' THEN 0 WHEN 'scheduled' THEN 1 ELSE 2 END,
			CASE WHEN job.model_identity=? THEN 1 ELSE 0 END,
			CASE WHEN job.model_identity=? THEN 0 ELSE 1 END,job.created_at,job.id LIMIT 1`, args...).Scan(
		&job.ID, &job.PresentationRunID, &job.IncidentID, &job.ProcessorKey, &job.ScopeKey, &job.RequestKind,
		&job.ModelIdentity, &job.AdapterKey, &job.PromptVersion, &job.InputHash, &job.AttemptCount,
	)
	return job, err
}
