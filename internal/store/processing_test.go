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

func TestRetryProcessingJobsResetsCurrentReviewJob(t *testing.T) {
	ctx := context.Background()
	database := oneProcessingIncident(t, ctx)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, "operation", now)
	if err != nil || !found {
		t.Fatalf("claim job = %t/%v", found, err)
	}
	if err := database.FailProcessingJob(ctx, job, "needs_review", "privacy", nil, now, context.Canceled); err != nil {
		t.Fatal(err)
	}
	count, err := database.RetryProcessingJobs(ctx, "operation", &job.IncidentID, now.Add(time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("retry count = %d, err=%v", count, err)
	}
	stats, err := database.ProcessingQueueStats(ctx, "operation", now.Add(time.Minute))
	if err != nil || stats.Queued != 1 || stats.NeedsReview != 0 {
		t.Fatalf("stats = %#v, err=%v", stats, err)
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
