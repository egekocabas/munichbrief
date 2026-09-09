ALTER TABLE translation_language_settings RENAME TO translation_language_settings_before_evaluation_adapters;

CREATE TABLE translation_language_settings (
    language_code TEXT PRIMARY KEY CHECK (trim(language_code) <> ''),
    preferred_model TEXT CHECK (preferred_model IS NULL OR trim(preferred_model) <> ''),
    adapter_key TEXT NOT NULL DEFAULT 'structured'
        CHECK (adapter_key IN ('structured', 'translategemma', 'hy-mt2', 'seed-x', 'salamandra-ta', 'llamax3', 'eurollm', 'tower-plus', 'tower-instruct')),
    updated_at TEXT NOT NULL
);

INSERT INTO translation_language_settings(language_code,preferred_model,adapter_key,updated_at)
SELECT language_code,preferred_model,adapter_key,updated_at
FROM translation_language_settings_before_evaluation_adapters;

DROP TABLE translation_language_settings_before_evaluation_adapters;
