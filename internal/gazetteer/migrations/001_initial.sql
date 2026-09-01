CREATE TABLE gazetteer_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    active_generation_id INTEGER,
    last_attempt_at TEXT,
    last_success_at TEXT,
    next_refresh_at TEXT
);

INSERT INTO gazetteer_state(singleton) VALUES (1);

CREATE TABLE gazetteer_sources (
    source_key TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    source_url TEXT NOT NULL,
    license TEXT NOT NULL,
    attribution TEXT NOT NULL,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL DEFAULT '',
    last_checked_at TEXT,
    last_success_at TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE gazetteer_generations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    status TEXT NOT NULL CHECK (status IN ('candidate', 'active', 'superseded', 'rejected')),
    aggregate_sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    activated_at TEXT,
    entry_count INTEGER NOT NULL
);

CREATE TABLE gazetteer_generation_sources (
    generation_id INTEGER NOT NULL REFERENCES gazetteer_generations(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL REFERENCES gazetteer_sources(source_key),
    content_sha256 TEXT NOT NULL,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    fetched_at TEXT NOT NULL,
    row_count INTEGER NOT NULL,
    PRIMARY KEY(generation_id, source_key)
);

CREATE TABLE gazetteer_names (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    generation_id INTEGER NOT NULL REFERENCES gazetteer_generations(id) ON DELETE CASCADE,
    value TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    kind TEXT NOT NULL,
    priority INTEGER NOT NULL,
    requires_context INTEGER NOT NULL DEFAULT 0 CHECK (requires_context IN (0, 1)),
    UNIQUE(generation_id, normalized_value)
);

CREATE INDEX gazetteer_names_generation_idx ON gazetteer_names(generation_id, normalized_value);

CREATE TABLE gazetteer_name_sources (
    name_id INTEGER NOT NULL REFERENCES gazetteer_names(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL REFERENCES gazetteer_sources(source_key),
    external_id TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_priority INTEGER NOT NULL,
    requires_context INTEGER NOT NULL DEFAULT 0 CHECK (requires_context IN (0, 1)),
    PRIMARY KEY(name_id, source_key, external_id)
);

CREATE TABLE gazetteer_overrides (
    normalized_value TEXT PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('exclude', 'context')),
    reason TEXT NOT NULL
);

INSERT INTO gazetteer_overrides(normalized_value, action, reason) VALUES
    ('Brand', 'exclude', 'Highly ambiguous German fire noun'),
    ('Haar', 'context', 'German common noun as well as a municipality');
