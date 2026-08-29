DROP INDEX post_processing_jobs_one_active_idx;
DROP INDEX post_processing_jobs_ready_idx;
DROP INDEX post_processing_jobs_selection_idx;
DROP INDEX post_processing_jobs_history_idx;

ALTER TABLE post_processing_values RENAME TO post_processing_values_before_skips;
ALTER TABLE post_processing_jobs RENAME TO post_processing_jobs_before_skips;

CREATE TABLE post_processing_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    presentation_run_id INTEGER NOT NULL REFERENCES presentation_runs(id) ON DELETE CASCADE,
    processor_key TEXT NOT NULL CHECK (trim(processor_key) <> ''),
    scope_key TEXT NOT NULL CHECK (trim(scope_key) <> ''),
    request_kind TEXT NOT NULL CHECK (request_kind IN ('scheduled', 'manual', 'backfill', 'imported')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'needs_review', 'failed', 'skipped', 'superseded')),
    status_reason TEXT,
    status_detail TEXT,
    model_identity TEXT NOT NULL CHECK (trim(model_identity) <> ''),
    prompt_version TEXT NOT NULL CHECK (trim(prompt_version) <> ''),
    input_hash TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_retry_at TEXT,
    failure_kind TEXT,
    error_message TEXT,
    started_at TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (status <> 'skipped' OR trim(COALESCE(status_reason, '')) <> '')
);

INSERT INTO post_processing_jobs(
    id,presentation_run_id,processor_key,scope_key,request_kind,status,status_reason,status_detail,
    model_identity,prompt_version,input_hash,attempt_count,next_retry_at,failure_kind,
    error_message,started_at,completed_at,created_at,updated_at
)
SELECT id,presentation_run_id,processor_key,scope_key,request_kind,status,NULL,NULL,
    model_identity,prompt_version,input_hash,attempt_count,next_retry_at,failure_kind,
    error_message,started_at,completed_at,created_at,updated_at
FROM post_processing_jobs_before_skips;

CREATE UNIQUE INDEX post_processing_jobs_one_active_idx
ON post_processing_jobs(presentation_run_id,processor_key,scope_key)
WHERE status IN ('pending','running');

CREATE INDEX post_processing_jobs_ready_idx
ON post_processing_jobs(processor_key,status,request_kind,next_retry_at,created_at);

CREATE INDEX post_processing_jobs_selection_idx
ON post_processing_jobs(presentation_run_id,processor_key,scope_key,status,completed_at DESC,id DESC);

CREATE INDEX post_processing_jobs_history_idx
ON post_processing_jobs(julianday(updated_at) DESC,id DESC);

CREATE TABLE post_processing_values (
    job_id INTEGER NOT NULL REFERENCES post_processing_jobs(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (trim(kind) <> ''),
    value TEXT NOT NULL,
    PRIMARY KEY(job_id,kind)
);

INSERT INTO post_processing_values(job_id,kind,value)
SELECT job_id,kind,value FROM post_processing_values_before_skips;

DROP TABLE post_processing_values_before_skips;
DROP TABLE post_processing_jobs_before_skips;
