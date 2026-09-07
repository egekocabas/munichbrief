ALTER TABLE rss_sync_history ADD COLUMN details_recorded INTEGER NOT NULL DEFAULT 0 CHECK(details_recorded IN (0,1));

-- Immutable payloads are shared across repeated identical fetches. They contain
-- extracted text, never HTML, and are accessible only through protected admin.
CREATE TABLE rss_source_snapshots (
    id INTEGER PRIMARY KEY,
    content_hash TEXT NOT NULL UNIQUE,
    extracted_text TEXT NOT NULL,
    parsed_json TEXT NOT NULL
);
CREATE TABLE rss_sync_documents (
    id INTEGER PRIMARY KEY,
    check_id INTEGER NOT NULL REFERENCES rss_sync_history(id) ON DELETE CASCADE,
    source_document_id INTEGER REFERENCES source_documents(id) ON DELETE SET NULL,
    source_url TEXT NOT NULL,
    external_id TEXT NOT NULL,
    title TEXT NOT NULL,
    published_at TEXT NOT NULL,
    in_window INTEGER NOT NULL CHECK(in_window IN (0,1)),
    existed INTEGER NOT NULL CHECK(existed IN (0,1)),
    from_feed INTEGER NOT NULL CHECK(from_feed IN (0,1)),
    fetch_reason TEXT NOT NULL DEFAULT '',
    fetch_status TEXT NOT NULL CHECK(fetch_status IN ('not_needed','outside_window','pending','stored','fetch_failed','parse_failed','storage_failed','interrupted')),
    error_message TEXT NOT NULL DEFAULT '',
    snapshot_id INTEGER REFERENCES rss_source_snapshots(id),
    outcomes_json TEXT NOT NULL DEFAULT '[]',
    inserted INTEGER NOT NULL DEFAULT 0 CHECK(inserted >= 0),
    updated INTEGER NOT NULL DEFAULT 0 CHECK(updated >= 0),
    unchanged INTEGER NOT NULL DEFAULT 0 CHECK(unchanged >= 0),
    removed INTEGER NOT NULL DEFAULT 0 CHECK(removed >= 0),
    UNIQUE(check_id, source_url)
);
CREATE INDEX rss_sync_documents_check_idx ON rss_sync_documents(check_id,id);
