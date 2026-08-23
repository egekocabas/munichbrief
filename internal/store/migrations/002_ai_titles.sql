CREATE TABLE derivations_v2 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('title_de', 'summary_de', 'title_en', 'summary_en', 'district', 'category')),
    value TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    model_identity TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    generated_at TEXT NOT NULL,
    UNIQUE(incident_id, kind, source_hash, model_identity, prompt_version)
);

INSERT INTO derivations_v2 (
    id, incident_id, kind, value, source_hash, model_identity, prompt_version, generated_at
)
SELECT
    id, incident_id, kind, value, source_hash, model_identity, prompt_version, generated_at
FROM derivations;

DROP TABLE derivations;
ALTER TABLE derivations_v2 RENAME TO derivations;

CREATE INDEX derivations_incident_current_idx
    ON derivations(incident_id, source_hash, generated_at DESC);

CREATE INDEX processing_jobs_ready_idx
    ON processing_jobs(operation, status, next_retry_at, created_at);
