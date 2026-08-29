package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsurePostProcessingScopes records the first automatic-enablement time for
// every registered processor scope without moving an existing cutover.
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_processing_scopes(processor_key,scope_key,automatic_after) VALUES(?,?,?) ON CONFLICT(processor_key,scope_key) DO NOTHING`, scope.ProcessorKey, scope.ScopeKey, formatted); err != nil {
			return fmt.Errorf("ensure post-processing scope %s/%s: %w", scope.ProcessorKey, scope.ScopeKey, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit post-processing scope initialization: %w", err)
	}
	return nil
}

// QueuePostProcessingForRun creates independent jobs for one completed current
// canonical presentation.
func (s *Store) QueuePostProcessingForRun(ctx context.Context, runID int64, plans []PostProcessingPlan, requestKind string, force bool, now time.Time) (int, error) {
	if requestKind != "scheduled" && requestKind != "manual" {
		return 0, errors.New("invalid post-processing request kind")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
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
	}
	total := 0
	for _, plan := range plans {
		if err := validatePostProcessingPlan(plan); err != nil {
			return 0, err
		}
		cutover := ""
		args := []any{PipelineVersion}
		if !force {
			cutover = ` AND julianday(r.completed_at)>julianday((SELECT automatic_after FROM post_processing_scopes WHERE processor_key=? AND scope_key=?))`
			args = append(args, plan.ProcessorKey, plan.ScopeKey)
		}
		query := `SELECT r.id,r.incident_id,r.source_hash FROM presentation_runs r
			JOIN incidents i ON i.id=r.incident_id JOIN source_documents d ON d.id=i.source_document_id
			WHERE ` + condition + ` AND r.source_hash=i.content_hash AND r.status='complete' AND r.pipeline_version=? AND r.legacy=0
			AND r.id=(SELECT candidate.id FROM presentation_runs candidate
				WHERE candidate.incident_id=i.id AND candidate.source_hash=i.content_hash AND candidate.status='complete'
				AND candidate.pipeline_version='` + PipelineVersion + `' AND candidate.legacy=0
				ORDER BY candidate.completed_at DESC,candidate.id DESC LIMIT 1)` + cutover + ` ORDER BY r.completed_at,r.id`
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

func queuePostProcessingTx(ctx context.Context, tx *sql.Tx, runID, incidentID int64, sourceHash string, plan PostProcessingPlan, requestKind string, force bool, now time.Time) (bool, error) {
	if err := validatePostProcessingPlan(plan); err != nil {
		return false, err
	}
	values, err := postProcessingInputValuesTx(ctx, tx, runID, plan.InputKinds)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='superseded',completed_at=?,updated_at=?
		WHERE status='pending' AND processor_key=? AND scope_key=? AND presentation_run_id IN (
			SELECT older.id FROM presentation_runs older
			WHERE older.id<>? AND older.incident_id=? AND older.source_hash=? AND older.pipeline_version=? AND older.legacy=0
		)`, formatted, formatted, plan.ProcessorKey, plan.ScopeKey, runID, incidentID, sourceHash, PipelineVersion); err != nil {
		return false, fmt.Errorf("supersede older pending post-processing jobs: %w", err)
	}
	var active, succeeded int
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status IN ('pending','running') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0)
		FROM post_processing_jobs WHERE presentation_run_id=? AND processor_key=? AND scope_key=?`, runID, plan.ProcessorKey, plan.ScopeKey).Scan(&active, &succeeded); err != nil {
		return false, err
	}
	if active > 0 || (!force && succeeded > 0) {
		return false, nil
	}
	hashParts := []string{"processor", plan.ProcessorKey, "scope", plan.ScopeKey}
	for _, kind := range plan.InputKinds {
		hashParts = append(hashParts, kind, values[kind])
	}
	hashParts = append(hashParts, plan.PromptVersion, plan.Model)
	inputHash := HashPipelineInput(hashParts...)
	_, err = tx.ExecContext(ctx, `INSERT INTO post_processing_jobs(
		presentation_run_id,processor_key,scope_key,request_kind,status,model_identity,prompt_version,input_hash,created_at,updated_at
	) VALUES(?,?,?,?,'pending',?,?,?,?,?)`, runID, plan.ProcessorKey, plan.ScopeKey, requestKind, plan.Model, plan.PromptVersion, inputHash, formatted, formatted)
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

func postProcessingInputValuesTx(ctx context.Context, tx *sql.Tx, runID int64, kinds []string) (map[string]string, error) {
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
				return nil, ErrNotFound
			}
			return nil, err
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
			return nil, err
		}
		for rows.Next() {
			var kind, value string
			if err := rows.Scan(&kind, &value); err != nil {
				rows.Close()
				return nil, err
			}
			values[kind] = value
			present[kind] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	for _, kind := range kinds {
		if !present[kind] || kind != "incident_body" && strings.TrimSpace(values[kind]) == "" {
			return nil, ErrNotFound
		}
	}
	return values, nil
}

// ClaimPostProcessingJob atomically claims the next eligible job for a
// registered processor. Inputs are materialized only when the queued prompt
// still matches the registered contract, allowing stale jobs to be claimed and
// failed by the worker's prompt-version guard after a deployment.
func (s *Store) ClaimPostProcessingJob(ctx context.Context, processorKey string, contracts PostProcessingContract, allowScheduled bool, blockedModels []string, now time.Time) (PostProcessingJob, bool, error) {
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
	blockedCondition := ""
	args := []any{processorKey, formatted, boolInt(allowScheduled)}
	if len(blockedModels) > 0 {
		blockedCondition = ` AND job.model_identity NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(blockedModels)), ",") + `)`
		for _, model := range blockedModels {
			args = append(args, model)
		}
	}
	var job PostProcessingJob
	err = tx.QueryRowContext(ctx, `SELECT job.id,job.presentation_run_id,r.incident_id,job.processor_key,job.scope_key,job.request_kind,
		job.model_identity,job.prompt_version,job.input_hash,job.attempt_count
		FROM post_processing_jobs job JOIN presentation_runs r ON r.id=job.presentation_run_id
		JOIN incidents i ON i.id=r.incident_id AND i.content_hash=r.source_hash
		WHERE job.processor_key=? AND job.status='pending' AND (job.next_retry_at IS NULL OR job.next_retry_at<=?)
		AND (job.request_kind='manual' OR ?)`+blockedCondition+`
		ORDER BY CASE job.request_kind WHEN 'manual' THEN 0 WHEN 'scheduled' THEN 1 ELSE 2 END,job.created_at,job.id LIMIT 1`, args...).Scan(
		&job.ID, &job.PresentationRunID, &job.IncidentID, &job.ProcessorKey, &job.ScopeKey, &job.RequestKind,
		&job.ModelIdentity, &job.PromptVersion, &job.InputHash, &job.AttemptCount,
	)
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
		job.InputValues, err = postProcessingInputValuesTx(ctx, tx, job.PresentationRunID, contract.InputKinds)
		if err != nil {
			return PostProcessingJob{}, false, fmt.Errorf("load claimed post-processing inputs: %w", err)
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
		return errors.New("post-processing job is no longer running")
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
		return errors.New("post-processing job is no longer running")
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
	rows, err := s.db.QueryContext(ctx, `SELECT processor_key,scope_key,model_identity,prompt_version FROM cycle_post_processing_plans WHERE cycle_id=? ORDER BY processor_key,scope_key`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plans []PostProcessingPlan
	for rows.Next() {
		var plan PostProcessingPlan
		if err := rows.Scan(&plan.ProcessorKey, &plan.ScopeKey, &plan.Model, &plan.PromptVersion); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}
