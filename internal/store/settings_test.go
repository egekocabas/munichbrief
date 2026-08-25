package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestPreferredModelIsSeededOnceAndAdminOverridePersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	preferred, err := database.InitializePreferredModel(ctx, "qwen3.5:4b", now)
	if err != nil || preferred != "qwen3.5:4b" {
		t.Fatalf("initial preference = %q/%v", preferred, err)
	}
	if err := database.SetPreferredModel(ctx, "granite4:3b", "incident-presentation/prompt/", "incident-presentation/prompt/granite4:3b", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	database.Close()

	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	preferred, err = database.InitializePreferredModel(ctx, "translategemma:4b", now.Add(2*time.Minute))
	if err != nil || preferred != "granite4:3b" {
		t.Fatalf("persisted preference = %q/%v", preferred, err)
	}
}

func TestPreferredModelRetargetsOnlyPendingAutomaticJobs(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	if _, err := database.InitializePreferredModel(ctx, "old:latest", now); err != nil {
		t.Fatal(err)
	}
	oldOperation := "incident-presentation/incident-presentation-v2/old:latest"
	if _, _, err := database.QueueAndClaimProcessingJob(ctx, oldOperation, now); err != nil {
		t.Fatal(err)
	}
	// Return the automatic job to pending, then create an explicit manual job
	// for another model. Only the automatic job should be retargeted.
	if _, err := database.db.ExecContext(ctx, `UPDATE processing_jobs SET status = 'pending' WHERE operation = ?`, oldOperation); err != nil {
		t.Fatal(err)
	}
	manualScope := PresentationScope{Operation: "incident-presentation/incident-presentation-v2/manual:latest", ModelIdentity: "manual:latest", PromptVersion: "incident-presentation-v2"}
	incidentID := int64(1)
	if _, err := database.RequestProcessingJobs(ctx, "fixture", manualScope, &incidentID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	newOperation := "incident-presentation/incident-presentation-v2/new:latest"
	if err := database.SetPreferredModel(ctx, "new:latest", "incident-presentation/incident-presentation-v2/", newOperation, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var automatic, manual int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE operation = ? AND model_identity = ? AND manual_requested_at IS NULL`, newOperation, "new:latest").Scan(&automatic); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE operation = ? AND model_identity = ? AND manual_requested_at IS NOT NULL`, manualScope.Operation, manualScope.ModelIdentity).Scan(&manual); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if automatic != 1 || manual != 1 {
		t.Fatalf("retargeted jobs automatic=%d manual=%d", automatic, manual)
	}
}
