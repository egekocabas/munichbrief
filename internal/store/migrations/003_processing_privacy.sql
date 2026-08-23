CREATE TABLE processing_jobs_v3 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    operation TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'needs_review')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_retry_at TEXT,
    failure_kind TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(incident_id, operation, source_hash)
);

INSERT INTO processing_jobs_v3 (
    id, incident_id, operation, source_hash, status, attempt_count,
    next_retry_at, failure_kind, error_message, created_at, updated_at
)
SELECT
    id, incident_id, operation, source_hash, status, attempt_count,
    next_retry_at,
    CASE WHEN status = 'failed' THEN 'legacy' ELSE NULL END,
    error_message, created_at, updated_at
FROM processing_jobs;

DROP TABLE processing_jobs;
ALTER TABLE processing_jobs_v3 RENAME TO processing_jobs;

CREATE INDEX processing_jobs_ready_idx
    ON processing_jobs(operation, status, next_retry_at, created_at);

CREATE INDEX processing_jobs_incident_state_idx
    ON processing_jobs(incident_id, operation, source_hash, status);
