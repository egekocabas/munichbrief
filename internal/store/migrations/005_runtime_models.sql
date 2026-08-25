CREATE TABLE ai_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    preferred_model TEXT NOT NULL CHECK (trim(preferred_model) <> ''),
    updated_at TEXT NOT NULL
);

ALTER TABLE processing_jobs ADD COLUMN model_identity TEXT NOT NULL DEFAULT '';

UPDATE processing_jobs
SET model_identity = substr(
    substr(operation, instr(operation, '/') + 1),
    instr(substr(operation, instr(operation, '/') + 1), '/') + 1
)
WHERE model_identity = '';

CREATE INDEX processing_jobs_model_ready_idx
    ON processing_jobs(model_identity, status, next_retry_at, manual_requested_at);
