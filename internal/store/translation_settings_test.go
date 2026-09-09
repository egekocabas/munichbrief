package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestTranslationLanguageSettingsInheritLegacyModelWithoutOverwritingChoices(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	now := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	if err := database.EnsurePipelineSteps(ctx, []string{"translation"}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.SetPipelineStepModel(ctx, "translation", "legacy:4b", now); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureTranslationLanguageSettings(ctx, []string{"uk", "en", "en"}, "en", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	settings, err := database.TranslationLanguageSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 2 || settings[0].LanguageCode != "en" || settings[1].LanguageCode != "uk" {
		t.Fatalf("settings = %#v", settings)
	}
	if settings[0].PreferredModel != "legacy:4b" || settings[0].AdapterKey != "structured" {
		t.Fatalf("inherited English setting = %#v", settings[0])
	}
	if settings[1].PreferredModel != "" || settings[1].AdapterKey != "structured" {
		t.Fatalf("new Ukrainian setting = %#v", settings[1])
	}

	updated := now.Add(2 * time.Minute)
	if err := database.SetTranslationLanguageSetting(ctx, "uk", "hy-mt2:7b", "hy-mt2", updated); err != nil {
		t.Fatal(err)
	}
	if err := database.SetPipelineStepModel(ctx, "translation", "new-shared:12b", updated); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureTranslationLanguageSettings(ctx, []string{"uk", "fr"}, "en", updated.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	settings, err = database.TranslationLanguageSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byCode := make(map[string]TranslationLanguageSetting, len(settings))
	for _, setting := range settings {
		byCode[setting.LanguageCode] = setting
	}
	if got := byCode["uk"]; got.PreferredModel != "hy-mt2:7b" || got.AdapterKey != "hy-mt2" || !got.UpdatedAt.Equal(updated) {
		t.Fatalf("existing Ukrainian choice was overwritten: %#v", got)
	}
	if got := byCode["fr"]; got.PreferredModel != "" || got.AdapterKey != "structured" {
		t.Fatalf("new French choice = %#v", got)
	}
}

func TestSeedXAdapterMigrationPreservesTranslationSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "seedx-upgrade.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.Name() == "017_seedx_translation_adapter.sql" {
			break
		}
		contents, readErr := migrationFiles.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err := raw.Exec(string(contents)); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			t.Fatalf("parse migration %s: %v", entry.Name(), err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,'2026-09-09T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO translation_language_settings(language_code,preferred_model,adapter_key,updated_at) VALUES('hr','old:model','hy-mt2','2026-09-09T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err := database.TranslationLanguageSettings(ctx)
	if err != nil || len(settings) != 1 || settings[0].PreferredModel != "old:model" || settings[0].AdapterKey != "hy-mt2" {
		t.Fatalf("preserved settings = %#v/%v", settings, err)
	}
	if err := database.SetTranslationLanguageSetting(ctx, "hr", "seed-x:test", "seed-x", time.Now()); err != nil {
		t.Fatalf("select Seed-X after upgrade: %v", err)
	}
}

func TestTranslationLanguageSettingValidation(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	if err := database.EnsureTranslationLanguageSettings(ctx, []string{"en"}, "en", now); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTranslationLanguageSetting(ctx, "missing", "model", "structured", now); err != ErrNotFound {
		t.Fatalf("missing language error = %v, want ErrNotFound", err)
	}
	if err := database.SetTranslationLanguageSetting(ctx, "en", "model", "unknown", now); err == nil {
		t.Fatal("invalid adapter was accepted")
	}
	if err := database.SetTranslationLanguageSetting(ctx, "en", "", "structured", now); err == nil {
		t.Fatal("empty model was accepted")
	}
}
