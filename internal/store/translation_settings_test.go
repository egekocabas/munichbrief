package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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
