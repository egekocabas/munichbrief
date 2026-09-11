ALTER TABLE post_processing_scopes RENAME TO post_processing_scopes_before_controls;

CREATE TABLE post_processing_scopes (
    processor_key TEXT NOT NULL CHECK (trim(processor_key) <> ''),
    scope_key TEXT NOT NULL CHECK (trim(scope_key) <> ''),
    automatic_after TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    enabled_updated_at TEXT NOT NULL,
    PRIMARY KEY(processor_key, scope_key)
);

INSERT INTO post_processing_scopes(
    processor_key,scope_key,automatic_after,enabled,enabled_updated_at
)
SELECT processor_key,scope_key,automatic_after,
    CASE WHEN processor_key='translation' AND scope_key<>'en' THEN 0 ELSE 1 END,
    automatic_after
FROM post_processing_scopes_before_controls;

DROP TABLE post_processing_scopes_before_controls;
