CREATE INDEX processing_step_jobs_history_idx
ON processing_step_jobs(julianday(updated_at) DESC, id DESC)
WHERE status <> 'waiting';
