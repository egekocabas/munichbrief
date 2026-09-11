ALTER TABLE gazetteer_sources ADD COLUMN last_failure_at TEXT;
ALTER TABLE gazetteer_sources ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE gazetteer_sources ADD COLUMN last_failure_run_id INTEGER;

CREATE TABLE gazetteer_refresh_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('startup', 'scheduled', 'retry', 'manual')),
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    duration_seconds REAL NOT NULL DEFAULT 0,
    active_generation_before INTEGER,
    active_generation_after INTEGER,
    entry_count INTEGER NOT NULL DEFAULT 0,
    changed INTEGER CHECK (changed IN (0, 1)),
    all_not_modified INTEGER CHECK (all_not_modified IN (0, 1)),
    failure_stage TEXT NOT NULL DEFAULT '',
    failed_source_key TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT ''
);

CREATE INDEX gazetteer_refresh_runs_started_idx ON gazetteer_refresh_runs(started_at DESC, id DESC);

CREATE TABLE gazetteer_refresh_sources (
    run_id INTEGER NOT NULL REFERENCES gazetteer_refresh_runs(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL,
    source_order INTEGER NOT NULL,
    display_name TEXT NOT NULL,
    source_url TEXT NOT NULL,
    license TEXT NOT NULL,
    attribution TEXT NOT NULL,
    contract_version TEXT NOT NULL,
    minimum_rows INTEGER NOT NULL,
    maximum_rows INTEGER NOT NULL,
    maximum_size INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'fetching', 'succeeded', 'not_modified', 'failed', 'not_run', 'interrupted')),
    started_at TEXT,
    completed_at TEXT,
    duration_seconds REAL NOT NULL DEFAULT 0,
    http_status INTEGER,
    response_bytes INTEGER NOT NULL DEFAULT 0,
    row_count INTEGER NOT NULL DEFAULT 0,
    content_sha256 TEXT NOT NULL DEFAULT '',
    failure_stage TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, source_key)
);

CREATE INDEX gazetteer_refresh_sources_run_order_idx ON gazetteer_refresh_sources(run_id, source_order);
