CREATE TABLE pipeline_cutovers (
    pipeline_version TEXT PRIMARY KEY,
    scheduled_after TEXT NOT NULL
);

INSERT INTO pipeline_cutovers(pipeline_version, scheduled_after)
VALUES ('incident-pipeline-v2', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

INSERT INTO ai_step_settings(step_key, preferred_model, updated_at)
SELECT 'incident_metadata', preferred_model, updated_at
FROM ai_step_settings
WHERE step_key = 'german_analysis';

UPDATE ai_step_settings
SET step_key = 'german_presentation'
WHERE step_key = 'german_analysis';

UPDATE processing_step_jobs
SET status = 'superseded', completed_at = COALESCE(completed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status IN ('waiting', 'pending', 'running');

UPDATE processing_cycle_items
SET status = 'superseded', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'pending';

UPDATE presentation_runs
SET status = 'superseded', completed_at = COALESCE(completed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE status = 'processing';

UPDATE processing_cycles
SET status = 'superseded', completed_at = COALESCE(completed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status IN ('queued', 'running');
