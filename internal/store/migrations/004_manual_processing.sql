ALTER TABLE processing_jobs ADD COLUMN manual_requested_at TEXT;

CREATE INDEX processing_jobs_manual_ready_idx
    ON processing_jobs(operation, manual_requested_at, status, next_retry_at);
