CREATE TABLE translation_language_settings (
    language_code TEXT PRIMARY KEY CHECK (trim(language_code) <> ''),
    preferred_model TEXT CHECK (preferred_model IS NULL OR trim(preferred_model) <> ''),
    adapter_key TEXT NOT NULL DEFAULT 'structured'
        CHECK (adapter_key IN ('structured', 'translategemma', 'hy-mt2')),
    updated_at TEXT NOT NULL
);

ALTER TABLE post_processing_jobs ADD COLUMN adapter_key TEXT NOT NULL DEFAULT 'structured'
    CHECK (trim(adapter_key) <> '');

ALTER TABLE cycle_post_processing_plans ADD COLUMN adapter_key TEXT NOT NULL DEFAULT 'structured'
    CHECK (trim(adapter_key) <> '');
