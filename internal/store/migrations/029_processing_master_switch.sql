ALTER TABLE ai_runtime_control ADD COLUMN processing_enabled INTEGER NOT NULL DEFAULT 1 CHECK (processing_enabled IN (0, 1));
ALTER TABLE ai_runtime_control ADD COLUMN processing_updated_at TEXT NOT NULL DEFAULT '';
UPDATE ai_runtime_control SET processing_updated_at=updated_at;
