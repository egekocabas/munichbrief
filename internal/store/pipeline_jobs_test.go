package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestFailPipelineJobValidatesAndSchedulesRetry(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now)
	if err != nil || !found {
		t.Fatalf("ActivateNextPipelineCycle() = %#v, %t, %v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("ClaimPipelineJob() = %#v, %t, %v", job, found, err)
	}
	if err := database.FailPipelineJob(ctx, job, "invalid", "output", nil, now, errors.New("unsafe details")); err == nil {
		t.Fatal("FailPipelineJob() accepted an invalid status")
	}
	retryAt := now.Add(time.Minute)
	if err := database.FailPipelineJob(ctx, job, "pending", "transient", &retryAt, now, errors.New("temporary failure")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now.Add(30*time.Second)); err != nil || found {
		t.Fatalf("job became claimable before retry: found=%t err=%v", found, err)
	}
	retried, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, retryAt)
	if err != nil || !found || retried.ID != job.ID || retried.AttemptCount != 2 {
		t.Fatalf("retried job = %#v, found=%t, err=%v", retried, found, err)
	}
	if err := database.FailPipelineJob(ctx, retried, "needs_review", "privacy", nil, retryAt, errors.New("private output")); err != nil {
		t.Fatal(err)
	}
	if err := database.FailPipelineJob(ctx, retried, "failed", "output", nil, retryAt, errors.New("duplicate")); err == nil {
		t.Fatal("FailPipelineJob() updated a job that was no longer running")
	}
}
