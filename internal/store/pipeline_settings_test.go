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

	if err := database.EnsurePipelineSteps(ctx, []string{"incident_metadata", ""}, now); err == nil {
		t.Fatal("EnsurePipelineSteps() accepted an empty key")
	}
	settings, err := database.PipelineStepSettings(ctx)
	if err != nil || len(settings) != 3 {
		// Migrations register all current steps before explicit initialization.
		t.Fatalf("initial settings = %#v, err=%v", settings, err)
	}
	steps := []string{"incident_metadata", "german_presentation", "english_translation"}
	if err := database.EnsurePipelineSteps(ctx, steps, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PreferredPipelineModels(ctx, steps); !errors.Is(err, ErrPipelineUnconfigured) {
		t.Fatalf("PreferredPipelineModels() error = %v, want ErrPipelineUnconfigured", err)
	}
	if err := database.SetPipelineStepModel(ctx, "missing", "model", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetPipelineStepModel(missing) error = %v, want ErrNotFound", err)
	}
	for step, model := range map[string]string{"incident_metadata": " qwen:4b ", "german_presentation": "qwen:4b", "english_translation": "translate:4b"} {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	models, err := database.PreferredPipelineModels(ctx, steps)
	if err != nil || models["incident_metadata"] != "qwen:4b" || models["german_presentation"] != "qwen:4b" || models["english_translation"] != "translate:4b" {
		t.Fatalf("preferred models = %#v, err=%v", models, err)
	}
}
