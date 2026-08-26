package store

import (
	"context"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

func testPipelinePlans() []PipelineStepPlan {
	return []PipelineStepPlan{
		{Key: "german_analysis", Order: 0, PromptVersion: "incident-analysis-de-v1", Model: "qwen:4b"},
		{Key: "english_translation", Order: 1, PromptVersion: "incident-translation-en-v1", Model: "translate:4b"},
	}
}

func insertPipelineDocuments(t *testing.T, ctx context.Context, database *Store, now time.Time, ids ...string) {
	t.Helper()
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
