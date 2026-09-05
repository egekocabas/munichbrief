CREATE TABLE rss_sync_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_name TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    window_start TEXT NOT NULL,
    window_end TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    not_modified INTEGER CHECK (not_modified IN (0, 1)),
    feed_documents INTEGER NOT NULL DEFAULT 0 CHECK (feed_documents >= 0),
    discovered INTEGER NOT NULL DEFAULT 0 CHECK (discovered >= 0),
    fetched INTEGER NOT NULL DEFAULT 0 CHECK (fetched >= 0),
    fetch_failures INTEGER NOT NULL DEFAULT 0 CHECK (fetch_failures >= 0),
    parser_failures INTEGER NOT NULL DEFAULT 0 CHECK (parser_failures >= 0),
    skipped INTEGER NOT NULL DEFAULT 0 CHECK (skipped >= 0),
    duration_seconds REAL NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    summary TEXT NOT NULL DEFAULT '',
    error_message TEXT,
    CHECK (
        (status = 'running' AND completed_at IS NULL) OR
        (status IN ('succeeded', 'failed') AND completed_at IS NOT NULL)
    )
);

CREATE INDEX rss_sync_history_source_started_idx
ON rss_sync_history(source_name, julianday(started_at) DESC, id DESC);
