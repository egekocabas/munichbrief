CREATE TABLE ai_step_settings (
    step_key TEXT PRIMARY KEY,
    preferred_model TEXT CHECK (preferred_model IS NULL OR trim(preferred_model) <> ''),
    updated_at TEXT NOT NULL
);

INSERT INTO ai_step_settings(step_key, preferred_model, updated_at)
SELECT 'german_analysis', preferred_model, updated_at FROM ai_settings WHERE id = 1;

INSERT INTO ai_step_settings(step_key, preferred_model, updated_at)
SELECT 'german_analysis', NULL, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE NOT EXISTS (SELECT 1 FROM ai_step_settings WHERE step_key = 'german_analysis');

INSERT INTO ai_step_settings(step_key, preferred_model, updated_at)
VALUES ('english_translation', NULL, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

CREATE TABLE presentation_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    pipeline_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processing', 'complete', 'failed', 'superseded')),
    legacy INTEGER NOT NULL DEFAULT 0 CHECK (legacy IN (0, 1)),
    created_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE presentation_values (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    presentation_run_id INTEGER NOT NULL REFERENCES presentation_runs(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (trim(kind) <> ''),
    value TEXT NOT NULL,
    model_identity TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    generated_at TEXT NOT NULL,
    UNIQUE(presentation_run_id, kind)
);

CREATE TABLE processing_cycles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK (kind IN ('scheduled', 'manual', 'continuation')),
    source_mode TEXT NOT NULL CHECK (source_mode IN ('fixture', 'live')),
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'superseded')),
    active_step INTEGER NOT NULL DEFAULT 0 CHECK (active_step >= 0),
    window_authorized INTEGER NOT NULL DEFAULT 0 CHECK (window_authorized IN (0, 1)),
    requested_at TEXT,
    started_at TEXT,
    completed_at TEXT,
    failure_kind TEXT,
    request_key TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE cycle_step_models (
    cycle_id INTEGER NOT NULL REFERENCES processing_cycles(id) ON DELETE CASCADE,
    step_key TEXT NOT NULL,
    step_order INTEGER NOT NULL CHECK (step_order >= 0),
    model_identity TEXT NOT NULL CHECK (trim(model_identity) <> ''),
    prompt_version TEXT NOT NULL CHECK (trim(prompt_version) <> ''),
    PRIMARY KEY(cycle_id, step_key),
    UNIQUE(cycle_id, step_order)
);

CREATE TABLE processing_cycle_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cycle_id INTEGER NOT NULL REFERENCES processing_cycles(id) ON DELETE CASCADE,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    presentation_run_id INTEGER NOT NULL REFERENCES presentation_runs(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'superseded')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(cycle_id, incident_id, source_hash)
);

CREATE TABLE processing_step_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cycle_item_id INTEGER NOT NULL REFERENCES processing_cycle_items(id) ON DELETE CASCADE,
    step_key TEXT NOT NULL,
    step_order INTEGER NOT NULL CHECK (step_order >= 0),
    model_identity TEXT NOT NULL CHECK (trim(model_identity) <> ''),
    prompt_version TEXT NOT NULL CHECK (trim(prompt_version) <> ''),
    input_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('waiting', 'pending', 'running', 'succeeded', 'needs_review', 'failed', 'skipped', 'superseded')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_retry_at TEXT,
    failure_kind TEXT,
    error_message TEXT,
    started_at TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(cycle_item_id, step_key)
);

CREATE INDEX presentation_runs_current_idx
    ON presentation_runs(incident_id, source_hash, status, legacy, completed_at DESC);
CREATE INDEX processing_cycles_ready_idx
    ON processing_cycles(status, kind, requested_at, created_at);
CREATE UNIQUE INDEX processing_cycles_pending_manual_request_idx
    ON processing_cycles(request_key) WHERE kind = 'manual' AND status = 'queued' AND request_key IS NOT NULL;
CREATE UNIQUE INDEX processing_cycles_one_running_idx
    ON processing_cycles(status) WHERE status = 'running';
CREATE INDEX processing_step_jobs_ready_idx
    ON processing_step_jobs(status, step_order, next_retry_at, created_at);

-- Import complete legacy presentations into cohesive immutable runs. Historical
-- derivations remain untouched and continue to provide their original audit trail.
INSERT INTO presentation_runs(
    incident_id, source_hash, pipeline_version, status, legacy, created_at, completed_at
)
SELECT d.incident_id, d.source_hash, 'legacy/' || d.prompt_version || '/' || d.model_identity,
       'complete', 1, MIN(d.generated_at), MAX(d.generated_at)
FROM derivations d
GROUP BY d.incident_id, d.source_hash, d.prompt_version, d.model_identity
HAVING COUNT(DISTINCT CASE WHEN d.kind IN ('title_de', 'summary_de', 'title_en', 'summary_en') THEN d.kind END) = 4;

INSERT INTO presentation_values(
    presentation_run_id, kind, value, model_identity, prompt_version, generated_at
)
SELECT r.id, d.kind, d.value, d.model_identity, d.prompt_version, d.generated_at
FROM presentation_runs r
JOIN derivations d
  ON d.incident_id = r.incident_id
 AND d.source_hash = r.source_hash
 AND r.pipeline_version = 'legacy/' || d.prompt_version || '/' || d.model_identity
WHERE r.legacy = 1 AND d.kind IN ('title_de', 'summary_de', 'title_en', 'summary_en');
