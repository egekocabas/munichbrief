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

UPDATE ai_step_settings
SET step_key = 'translation'
WHERE step_key = 'english_translation';

ALTER TABLE processing_cycles ADD COLUMN translation_model_identity TEXT
CHECK (translation_model_identity IS NULL OR trim(translation_model_identity) <> '');

CREATE TABLE translation_language_cutovers (
    language_code TEXT PRIMARY KEY CHECK (trim(language_code) <> ''),
    automatic_after TEXT NOT NULL
);

INSERT INTO translation_language_cutovers(language_code, automatic_after)
SELECT 'en', scheduled_after FROM pipeline_cutovers WHERE pipeline_version = 'incident-pipeline-v2';

CREATE TABLE presentation_translations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    presentation_run_id INTEGER NOT NULL REFERENCES presentation_runs(id) ON DELETE CASCADE,
    language_code TEXT NOT NULL CHECK (trim(language_code) <> ''),
    request_kind TEXT NOT NULL CHECK (request_kind IN ('scheduled', 'manual', 'backfill', 'imported')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'needs_review', 'failed', 'superseded')),
    title TEXT,
    summary TEXT,
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
    CHECK (status <> 'succeeded' OR (trim(COALESCE(title, '')) <> '' AND trim(COALESCE(summary, '')) <> ''))
);

CREATE UNIQUE INDEX presentation_translations_one_active_idx
ON presentation_translations(presentation_run_id, language_code)
WHERE status IN ('pending', 'running');

CREATE INDEX presentation_translations_ready_idx
ON presentation_translations(status, request_kind, next_retry_at, created_at);

CREATE INDEX presentation_translations_selection_idx
ON presentation_translations(presentation_run_id, language_code, status, completed_at DESC);

-- Preserve complete English v1 and legacy output as language-neutral
-- translation records. Original presentation_values remain immutable.
INSERT INTO presentation_translations(
    presentation_run_id, language_code, request_kind, status, title, summary,
    model_identity, prompt_version, input_hash, attempt_count,
    started_at, completed_at, created_at, updated_at
)
SELECT r.id, 'en', 'imported', 'succeeded',
    (SELECT value FROM presentation_values WHERE presentation_run_id = r.id AND kind = 'title_en' LIMIT 1),
    (SELECT value FROM presentation_values WHERE presentation_run_id = r.id AND kind = 'summary_en' LIMIT 1),
    (SELECT model_identity FROM presentation_values WHERE presentation_run_id = r.id AND kind = 'summary_en' LIMIT 1),
    (SELECT prompt_version FROM presentation_values WHERE presentation_run_id = r.id AND kind = 'summary_en' LIMIT 1),
    '', 0, r.created_at, r.completed_at, r.created_at, COALESCE(r.completed_at, r.created_at)
FROM presentation_runs r
WHERE r.status = 'complete'
  AND EXISTS (SELECT 1 FROM presentation_values WHERE presentation_run_id = r.id AND kind = 'title_en' AND trim(value) <> '')
  AND EXISTS (SELECT 1 FROM presentation_values WHERE presentation_run_id = r.id AND kind = 'summary_en' AND trim(value) <> '');

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
