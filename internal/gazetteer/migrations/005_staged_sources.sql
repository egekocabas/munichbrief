-- Validated downloads survive a later source failure without changing the
-- active generation. They are consumed only by an entirely successful refresh.
CREATE TABLE gazetteer_staged_sources (
    source_key TEXT PRIMARY KEY,
    definition_hash TEXT NOT NULL,
    fetched_at TEXT NOT NULL,
    snapshot_json TEXT NOT NULL
);
