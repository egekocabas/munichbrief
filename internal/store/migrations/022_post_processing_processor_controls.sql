CREATE TABLE post_processing_processor_controls (
    processor_key TEXT PRIMARY KEY CHECK (trim(processor_key) <> ''),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    enabled_updated_at TEXT NOT NULL
);

INSERT INTO post_processing_processor_controls(processor_key, enabled, enabled_updated_at)
SELECT processor_key, 1, MIN(enabled_updated_at)
FROM post_processing_scopes
GROUP BY processor_key;
