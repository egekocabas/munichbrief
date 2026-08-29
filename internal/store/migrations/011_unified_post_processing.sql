CREATE TABLE post_processing_scopes (
    processor_key TEXT NOT NULL CHECK (trim(processor_key) <> ''),
    scope_key TEXT NOT NULL CHECK (trim(scope_key) <> ''),
    automatic_after TEXT NOT NULL,
    PRIMARY KEY(processor_key, scope_key)
);

INSERT INTO post_processing_scopes(processor_key, scope_key, automatic_after)
SELECT 'translation', language_code, automatic_after
FROM translation_language_cutovers;

INSERT INTO post_processing_scopes(processor_key, scope_key, automatic_after)
SELECT 'category_verification', 'default', automatic_after
FROM category_verification_cutover WHERE id = 1;

CREATE TABLE post_processing_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    presentation_run_id INTEGER NOT NULL REFERENCES presentation_runs(id) ON DELETE CASCADE,
    processor_key TEXT NOT NULL CHECK (trim(processor_key) <> ''),
    scope_key TEXT NOT NULL CHECK (trim(scope_key) <> ''),
    request_kind TEXT NOT NULL CHECK (request_kind IN ('scheduled', 'manual', 'backfill', 'imported')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'needs_review', 'failed', 'superseded')),
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
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX post_processing_jobs_one_active_idx
ON post_processing_jobs(presentation_run_id, processor_key, scope_key)
WHERE status IN ('pending', 'running');

CREATE INDEX post_processing_jobs_ready_idx
ON post_processing_jobs(processor_key, status, request_kind, next_retry_at, created_at);

CREATE INDEX post_processing_jobs_selection_idx
ON post_processing_jobs(presentation_run_id, processor_key, scope_key, status, completed_at DESC, id DESC);

CREATE INDEX post_processing_jobs_history_idx
ON post_processing_jobs(julianday(updated_at) DESC, id DESC);

CREATE TABLE post_processing_values (
    job_id INTEGER NOT NULL REFERENCES post_processing_jobs(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (trim(kind) <> ''),
    value TEXT NOT NULL,
    PRIMARY KEY(job_id, kind)
);

CREATE TABLE cycle_post_processing_plans (
    cycle_id INTEGER NOT NULL REFERENCES processing_cycles(id) ON DELETE CASCADE,
    processor_key TEXT NOT NULL CHECK (trim(processor_key) <> ''),
    scope_key TEXT NOT NULL CHECK (trim(scope_key) <> ''),
    model_identity TEXT NOT NULL CHECK (trim(model_identity) <> ''),
    prompt_version TEXT NOT NULL CHECK (trim(prompt_version) <> ''),
    PRIMARY KEY(cycle_id, processor_key, scope_key)
);

-- Keep original IDs for translations and offset category-verification IDs so
-- every historical attempt can be migrated without collisions.
CREATE TEMP TABLE post_processing_migration_offset(value INTEGER NOT NULL);
INSERT INTO post_processing_migration_offset(value)
SELECT COALESCE(MAX(id), 0) FROM presentation_translations;

INSERT INTO post_processing_jobs(
    id, presentation_run_id, processor_key, scope_key, request_kind, status,
    model_identity, prompt_version, input_hash, attempt_count, next_retry_at,
    failure_kind, error_message, started_at, completed_at, created_at, updated_at
)
SELECT id, presentation_run_id, 'translation', language_code, request_kind, status,
    model_identity, prompt_version, input_hash, attempt_count, next_retry_at,
    failure_kind, error_message, started_at, completed_at, created_at, updated_at
FROM presentation_translations;

INSERT INTO post_processing_values(job_id, kind, value)
SELECT id, 'title', title FROM presentation_translations
WHERE title IS NOT NULL;

INSERT INTO post_processing_values(job_id, kind, value)
SELECT id, 'summary', summary FROM presentation_translations
WHERE summary IS NOT NULL;

INSERT INTO post_processing_jobs(
    id, presentation_run_id, processor_key, scope_key, request_kind, status,
    model_identity, prompt_version, input_hash, attempt_count, next_retry_at,
    failure_kind, error_message, started_at, completed_at, created_at, updated_at
)
SELECT verification.id + offset.value, verification.presentation_run_id,
    'category_verification', 'default', verification.request_kind, verification.status,
    verification.model_identity, verification.prompt_version, verification.input_hash,
    verification.attempt_count, verification.next_retry_at, verification.failure_kind,
    verification.error_message, verification.started_at, verification.completed_at,
    verification.created_at, verification.updated_at
FROM presentation_category_verifications verification
CROSS JOIN post_processing_migration_offset offset;

INSERT INTO post_processing_values(job_id, kind, value)
SELECT verification.id + offset.value, 'is_correct',
    CASE verification.is_correct WHEN 1 THEN 'true' ELSE 'false' END
FROM presentation_category_verifications verification
CROSS JOIN post_processing_migration_offset offset
WHERE verification.is_correct IS NOT NULL;

INSERT INTO post_processing_values(job_id, kind, value)
SELECT verification.id + offset.value, 'corrected_category', verification.corrected_category
FROM presentation_category_verifications verification
CROSS JOIN post_processing_migration_offset offset
WHERE verification.corrected_category IS NOT NULL;

INSERT INTO cycle_post_processing_plans(cycle_id, processor_key, scope_key, model_identity, prompt_version)
SELECT cycle.id, 'translation', scope.scope_key, cycle.translation_model_identity,
    CASE scope.scope_key WHEN 'en' THEN 'incident-translation-en-v1' ELSE 'translation/' || scope.scope_key END
FROM processing_cycles cycle
JOIN post_processing_scopes scope ON scope.processor_key = 'translation'
WHERE cycle.translation_model_identity IS NOT NULL;

INSERT INTO cycle_post_processing_plans(cycle_id, processor_key, scope_key, model_identity, prompt_version)
SELECT id, 'category_verification', 'default', category_verification_model_identity,
    'incident-category-verification-v1'
FROM processing_cycles WHERE category_verification_model_identity IS NOT NULL;

DROP TABLE post_processing_migration_offset;
DROP TABLE presentation_translations;
DROP TABLE presentation_category_verifications;
DROP TABLE translation_language_cutovers;
DROP TABLE category_verification_cutover;

ALTER TABLE processing_cycles DROP COLUMN translation_model_identity;
ALTER TABLE processing_cycles DROP COLUMN category_verification_model_identity;
