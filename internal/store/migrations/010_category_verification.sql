CREATE TABLE category_verification_cutover (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    automatic_after TEXT NOT NULL
);

INSERT INTO category_verification_cutover(id, automatic_after)
VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

CREATE TABLE presentation_category_verifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    presentation_run_id INTEGER NOT NULL REFERENCES presentation_runs(id) ON DELETE CASCADE,
    request_kind TEXT NOT NULL CHECK (request_kind IN ('scheduled', 'manual', 'backfill')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'needs_review', 'failed', 'superseded')),
    input_category TEXT NOT NULL CHECK (input_category IN (
        'traffic','theft_burglary','robbery_extortion','violence','sexual_offense','fraud_cyber',
        'drugs','fire_hazard','property_damage','missing_wanted','police_operation','other'
    )),
    is_correct INTEGER CHECK (is_correct IS NULL OR is_correct IN (0, 1)),
    corrected_category TEXT CHECK (corrected_category IS NULL OR corrected_category IN (
        'traffic','theft_burglary','robbery_extortion','violence','sexual_offense','fraud_cyber',
        'drugs','fire_hazard','property_damage','missing_wanted','police_operation','other'
    )),
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
    CHECK (status <> 'succeeded' OR (
        is_correct IS NOT NULL AND trim(COALESCE(corrected_category, '')) <> '' AND
        ((is_correct = 1 AND corrected_category = input_category) OR
         (is_correct = 0 AND corrected_category <> input_category))
    ))
);

CREATE UNIQUE INDEX presentation_category_verifications_one_active_idx
ON presentation_category_verifications(presentation_run_id)
WHERE status IN ('pending', 'running');

CREATE INDEX presentation_category_verifications_ready_idx
ON presentation_category_verifications(status, request_kind, next_retry_at, created_at);

CREATE INDEX presentation_category_verifications_selection_idx
ON presentation_category_verifications(presentation_run_id, status, completed_at DESC, id DESC);

ALTER TABLE processing_cycles ADD COLUMN category_verification_model_identity TEXT
CHECK (category_verification_model_identity IS NULL OR trim(category_verification_model_identity) <> '');
