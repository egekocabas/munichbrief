package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineSnapshotSeparatesActiveWaitingWorkAndNewCandidates(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "pipeline-status.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one", "two")
	plans := testPipelinePlans()
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, "translate:4b", nil, true, now)
	if err != nil || request.Requested != 2 {
		t.Fatalf("create manual cycle = %#v/%v", request, err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, now)
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("activate manual cycle = %#v/%t/%v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim German job = %#v/%t/%v", job, found, err)
	}
	values := []PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, job, values, job.ModelIdentity, HashPipelineInput(job.SourceHash), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	stepKeys := []string{"incident_metadata", "german_presentation"}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", stepKeys, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveStepCompleted != 1 || snapshot.ActiveStepTotal != 2 || snapshot.CycleCompleted != 1 || snapshot.CycleTotal != 4 {
		t.Fatalf("active progress = step %d/%d pipeline %d/%d", snapshot.ActiveStepCompleted, snapshot.ActiveStepTotal, snapshot.CycleCompleted, snapshot.CycleTotal)
	}
	if snapshot.ScheduledCandidates != 0 {
		t.Fatalf("in-flight items reported as new candidates: %d", snapshot.ScheduledCandidates)
	}
	if len(snapshot.ActiveSteps) != 2 || len(snapshot.Steps) != 2 {
		t.Fatalf("step stats lengths = active:%d all:%d", len(snapshot.ActiveSteps), len(snapshot.Steps))
	}
	if metadata := snapshot.ActiveSteps[0]; metadata.Queued != 1 || metadata.Succeeded != 1 || metadata.Waiting != 0 {
		t.Fatalf("active metadata stats = %#v", metadata)
	}
	if german := snapshot.ActiveSteps[1]; german.Waiting != 2 || german.ReadyAfterStage != 1 || german.Queued != 0 {
		t.Fatalf("active German stats = %#v", german)
	}
	if snapshot.Steps[0].Succeeded != 1 || snapshot.Steps[1].Waiting != 2 {
		t.Fatalf("all-cycle stats = %#v", snapshot.Steps)
	}

	insertPipelineDocuments(t, ctx, database, now.Add(2*time.Minute), "three")
	snapshot, err = database.PipelineSnapshot(ctx, "fixture", stepKeys, now.Add(2*time.Minute))
	if err != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("new candidate outside frozen cycle = %#v/%v", snapshot, err)
	}
}
