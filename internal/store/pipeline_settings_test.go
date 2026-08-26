package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineStepSettingsValidateAndPersistModels(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)

	if err := database.EnsurePipelineSteps(ctx, []string{"german_analysis", ""}, now); err == nil {
		t.Fatal("EnsurePipelineSteps() accepted an empty key")
	}
	settings, err := database.PipelineStepSettings(ctx)
	if err != nil || len(settings) != 2 {
		// Migrations register both current steps before explicit initialization.
		t.Fatalf("initial settings = %#v, err=%v", settings, err)
	}
	if err := database.EnsurePipelineSteps(ctx, []string{"german_analysis", "english_translation"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PreferredPipelineModels(ctx, []string{"german_analysis", "english_translation"}); !errors.Is(err, ErrPipelineUnconfigured) {
		t.Fatalf("PreferredPipelineModels() error = %v, want ErrPipelineUnconfigured", err)
	}
	if err := database.SetPipelineStepModel(ctx, "missing", "model", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetPipelineStepModel(missing) error = %v, want ErrNotFound", err)
	}
	if err := database.SetPipelineStepModel(ctx, "german_analysis", " qwen:4b ", now); err != nil {
		t.Fatal(err)
	}
	if err := database.SetPipelineStepModel(ctx, "english_translation", "translate:4b", now); err != nil {
		t.Fatal(err)
	}
	models, err := database.PreferredPipelineModels(ctx, []string{"german_analysis", "english_translation"})
	if err != nil || models["german_analysis"] != "qwen:4b" || models["english_translation"] != "translate:4b" {
		t.Fatalf("preferred models = %#v, err=%v", models, err)
	}
}
