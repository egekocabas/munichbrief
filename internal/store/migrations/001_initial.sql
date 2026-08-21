CREATE TABLE source_documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id TEXT NOT NULL UNIQUE,
    source_url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    published_at TEXT NOT NULL,
    discovered_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    feed_fingerprint TEXT NOT NULL,
    fetch_status TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    last_fetched_at TEXT,
    error_message TEXT
);

CREATE INDEX source_documents_published_at_idx
    ON source_documents(published_at DESC);

CREATE TABLE incidents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_document_id INTEGER NOT NULL REFERENCES source_documents(id) ON DELETE CASCADE,
    incident_number TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    title_de TEXT NOT NULL,
    body_de TEXT,
    content_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(source_document_id, position)
);

CREATE INDEX incidents_source_document_id_idx
    ON incidents(source_document_id);

CREATE TABLE derivations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('summary_de', 'summary_en', 'district', 'category')),
    value TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    model_identity TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    generated_at TEXT NOT NULL,
    UNIQUE(incident_id, kind, source_hash, model_identity, prompt_version)
);

CREATE TABLE processing_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    operation TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_retry_at TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(incident_id, operation, source_hash)
);

CREATE TABLE sync_state (
    source_name TEXT PRIMARY KEY,
    etag TEXT,
    last_modified TEXT,
    last_attempt_at TEXT,
    last_success_at TEXT,
    result TEXT
);
