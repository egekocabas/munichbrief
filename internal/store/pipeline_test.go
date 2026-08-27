package store

import (
	"context"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

func testPipelinePlans() []PipelineStepPlan {
	return []PipelineStepPlan{
		{Key: "incident_metadata", Order: 0, PromptVersion: "incident-metadata-v1", Model: "qwen:4b"},
		{Key: "german_presentation", Order: 1, PromptVersion: "incident-presentation-de-v2", Model: "qwen:4b"},
	}
}

func insertPipelineDocuments(t *testing.T, ctx context.Context, database *Store, now time.Time, ids ...string) {
	t.Helper()
	if _, err := database.db.ExecContext(ctx, `UPDATE pipeline_cutovers SET scheduled_after='1970-01-01T00:00:00Z' WHERE pipeline_version=?`, PipelineVersion); err != nil {
		t.Fatal(err)
	}
	documents := make([]domain.SourceDocument, 0, len(ids))
	for index, id := range ids {
		documents = append(documents, domain.SourceDocument{
			ExternalID: id, SourceURL: "https://fixture.invalid/" + id, Title: "Release " + id,
			PublishedAt: now.Add(time.Duration(index) * time.Second), FeedFingerprint: "feed-" + id, SourceHash: "source-" + id,
			Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel " + id, BodyDE: "Text " + id, ContentHash: "content-" + id}},
		})
	}
	if err := database.UpsertDocuments(ctx, documents, now); err != nil {
		t.Fatal(err)
	}
}
