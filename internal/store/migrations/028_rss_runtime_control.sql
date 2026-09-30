CREATE TABLE rss_runtime_control (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1))
);

INSERT INTO rss_runtime_control(id, enabled) VALUES (1, 1);
