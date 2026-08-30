CREATE TABLE ai_runtime_control (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    automatic_processing_enabled INTEGER NOT NULL CHECK (automatic_processing_enabled IN (0, 1)),
    updated_at TEXT NOT NULL
);

INSERT INTO ai_runtime_control(id, automatic_processing_enabled, updated_at)
VALUES (1, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
