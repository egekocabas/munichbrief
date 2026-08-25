package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

func TestProcessingPrivacyMigrationPreservesExistingJobs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []struct {
		version int
		name    string
	}{{1, "migrations/001_initial.sql"}, {2, "migrations/002_ai_titles.sql"}} {
		contents, err := migrationFiles.ReadFile(migration.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(contents)); err != nil {
			t.Fatalf("apply migration %d: %v", migration.version, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, migration.version, "2026-08-23T10:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO source_documents (id, external_id, source_url, title, published_at, discovered_at, last_seen_at, feed_fingerprint, fetch_status, source_hash) VALUES (1, 'one', 'https://fixture.invalid/one', 'One', '2026-08-23T10:00:00Z', '2026-08-23T10:00:00Z', '2026-08-23T10:00:00Z', 'feed', 'fixture', 'source')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO incidents (id, source_document_id, incident_number, position, title_de, body_de, content_hash, created_at, updated_at) VALUES (1, 1, '1', 0, 'Titel', 'Text', 'content', '2026-08-23T10:00:00Z', '2026-08-23T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO processing_jobs (id, incident_id, operation, source_hash, status, attempt_count, created_at, updated_at) VALUES (1, 1, 'operation', 'content', 'failed', 5, '2026-08-23T10:00:00Z', '2026-08-23T10:00:00Z')`); err != nil {
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
	var status, failureKind string
	if err := database.db.QueryRowContext(ctx, `SELECT status, failure_kind FROM processing_jobs WHERE id = 1`).Scan(&status, &failureKind); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || failureKind != "legacy" {
		t.Fatalf("migrated job = %q/%q", status, failureKind)
	}
	var manualRequestedAt sql.NullString
	if err := database.db.QueryRowContext(ctx, `SELECT manual_requested_at FROM processing_jobs WHERE id = 1`).Scan(&manualRequestedAt); err != nil {
		t.Fatal(err)
	}
	if manualRequestedAt.Valid {
		t.Fatalf("migrated legacy job unexpectedly has manual request time %q", manualRequestedAt.String)
	}
}

func TestCompleteProcessingJobRequiresSafePrivacyStatus(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, "operation", time.Now())
	if err != nil || !found {
		t.Fatalf("claim job = %t/%v", found, err)
	}
	unsafe := AIPresentation{TitleDE: "Titel", SummaryDE: "Text", TitleEN: "Title", SummaryEN: "Text", PrivacyStatus: "review_required"}
	if err := database.CompleteProcessingJob(ctx, job, unsafe, "model", "prompt", time.Now()); err == nil {
		t.Fatal("unsafe presentation was persisted")
	}
}

func TestQueueClaimsExistingIncidentsNewestPublicationFirst(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "newest-first.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	documents := []domain.SourceDocument{
		{
			ExternalID: "older", SourceURL: "https://fixture.invalid/older", Title: "Older release",
			PublishedAt: now.Add(-24 * time.Hour), FeedFingerprint: "older-feed", SourceHash: "older-source",
			Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Older incident", BodyDE: "Older body", ContentHash: "older-content"}},
		},
		{
			ExternalID: "newer", SourceURL: "https://fixture.invalid/newer", Title: "Newer release",
			PublishedAt: now, FeedFingerprint: "newer-feed", SourceHash: "newer-source",
			Incidents: []domain.Incident{{Number: "2", Position: 0, TitleDE: "Newer incident", BodyDE: "Newer body", ContentHash: "newer-content"}},
		},
	}
	if err := database.UpsertDocuments(ctx, documents, now); err != nil {
		t.Fatal(err)
	}

	job, found, err := database.QueueAndClaimProcessingJob(ctx, "new-operation", now)
	if err != nil || !found {
		t.Fatalf("claim existing job = %t/%v", found, err)
	}
	if job.TitleDE != "Newer incident" {
		t.Fatalf("first existing job = %q, want newest incident", job.TitleDE)
	}
	stats, err := database.ProcessingQueueStats(ctx, "new-operation", now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Running != 1 || stats.Queued != 1 {
		t.Fatalf("existing backlog stats = %#v", stats)
	}
}

func TestRequestProcessingJobsCreatesMissingJobAndReportsCurrentStates(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	scope := PresentationScope{Operation: "operation", ModelIdentity: "model", PromptVersion: "prompt"}
	incidentID := int64(1)

	requested, err := database.RequestProcessingJobs(ctx, "fixture", scope, &incidentID, now)
	if err != nil || requested != (ProcessingRequestResult{Requested: 1}) {
		t.Fatalf("missing-job request = %#v, err=%v", requested, err)
	}
	job, found, err := database.ClaimManualProcessingJob(ctx, scope.Operation, now)
	if err != nil || !found || job.IncidentID != incidentID {
		t.Fatalf("manual claim = %#v/%t/%v", job, found, err)
	}
	running, err := database.RequestProcessingJobs(ctx, "fixture", scope, &incidentID, now.Add(time.Minute))
	if err != nil || running != (ProcessingRequestResult{AlreadyRunning: 1}) {
		t.Fatalf("running request = %#v, err=%v", running, err)
	}
	presentation := AIPresentation{TitleDE: "Titel", SummaryDE: "Text", TitleEN: "Title", SummaryEN: "Text", PrivacyStatus: "safe"}
	if err := database.CompleteProcessingJob(ctx, job, presentation, scope.ModelIdentity, scope.PromptVersion, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := database.RequestProcessingJobs(ctx, "fixture", scope, &incidentID, now.Add(3*time.Minute))
	if err != nil || current != (ProcessingRequestResult{AlreadyCurrent: 1}) {
		t.Fatalf("current request = %#v, err=%v", current, err)
	}
}

func TestRequestProcessingJobsResetsIncompleteAndPersistsManualIntent(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	scope := PresentationScope{Operation: "operation", ModelIdentity: "model", PromptVersion: "prompt"}
	job, found, err := database.QueueAndClaimProcessingJob(ctx, scope.Operation, now)
	if err != nil || !found {
		t.Fatalf("initial claim = %t/%v", found, err)
	}
	if err := database.FailProcessingJob(ctx, job, "needs_review", "privacy", nil, now, context.Canceled); err != nil {
		t.Fatal(err)
	}
	result, err := database.RequestProcessingJobs(ctx, "fixture", scope, nil, now.Add(time.Minute))
	if err != nil || result.Requested != 1 {
		t.Fatalf("bulk request = %#v, err=%v", result, err)
	}
	var status string
	var attempts int
	var manualRequestedAt sql.NullString
	if err := database.db.QueryRowContext(ctx, `SELECT status, attempt_count, manual_requested_at FROM processing_jobs WHERE id = ?`, job.ID).Scan(&status, &attempts, &manualRequestedAt); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 0 || !manualRequestedAt.Valid {
		t.Fatalf("reset job = status:%q attempts:%d manual:%#v", status, attempts, manualRequestedAt)
	}
	claimed, found, err := database.ClaimManualProcessingJob(ctx, scope.Operation, now.Add(time.Minute))
	if err != nil || !found || claimed.ID != job.ID {
		t.Fatalf("persisted manual claim = %#v/%t/%v", claimed, found, err)
	}
}

func TestRequestProcessingJobsRequeuesSucceededJobWithIncompleteDerivations(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	scope := PresentationScope{Operation: "operation", ModelIdentity: "model", PromptVersion: "prompt"}
	job, found, err := database.QueueAndClaimProcessingJob(ctx, scope.Operation, now)
	if err != nil || !found {
		t.Fatalf("claim = %t/%v", found, err)
	}
	presentation := AIPresentation{TitleDE: "Titel", SummaryDE: "Text", TitleEN: "Title", SummaryEN: "Text", PrivacyStatus: "safe"}
	if err := database.CompleteProcessingJob(ctx, job, presentation, scope.ModelIdentity, scope.PromptVersion, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM derivations WHERE incident_id = ? AND kind = 'title_en'`, job.IncidentID); err != nil {
		t.Fatal(err)
	}
	result, err := database.RequestProcessingJobs(ctx, "fixture", scope, &job.IncidentID, now.Add(time.Minute))
	if err != nil || result.Requested != 1 || result.AlreadyCurrent != 0 {
		t.Fatalf("incomplete presentation request = %#v, err=%v", result, err)
	}
	claimed, found, err := database.ClaimManualProcessingJob(ctx, scope.Operation, now.Add(time.Minute))
	if err != nil || !found || claimed.ID != job.ID {
		t.Fatalf("requeued incomplete job = %#v/%t/%v", claimed, found, err)
	}
}

func TestRequestProcessingJobsRejectsUnknownIncident(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	unknown := int64(376)
	_, err := database.RequestProcessingJobs(ctx, "fixture", PresentationScope{Operation: "operation", ModelIdentity: "model", PromptVersion: "prompt"}, &unknown, time.Now())
	if err != ErrNotFound {
		t.Fatalf("unknown incident error = %v, want ErrNotFound", err)
	}
}

func oneProcessingIncident(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "processing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.UTC)
	documents := []domain.SourceDocument{{
		ExternalID: "processing", SourceURL: "https://fixture.invalid/processing", Title: "Processing",
		PublishedAt: now, FeedFingerprint: "feed", SourceHash: "source",
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel", BodyDE: "Text", ContentHash: "content"}},
	}}
	if err := database.UpsertDocuments(ctx, documents, now); err != nil {
		t.Fatal(err)
	}
	return database
}
