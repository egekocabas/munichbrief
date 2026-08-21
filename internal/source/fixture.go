package source

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

//go:embed fixtures/releases.json
var fixtureData []byte

// FixtureProvider loads deterministic synthetic releases for local development.
type FixtureProvider struct{}

type fixtureDocument struct {
	ExternalID  string            `json:"external_id"`
	SourceURL   string            `json:"source_url"`
	Title       string            `json:"title"`
	PublishedAt string            `json:"published_at"`
	Incidents   []fixtureIncident `json:"incidents"`
}

type fixtureIncident struct {
	Number  string `json:"number"`
	TitleDE string `json:"title_de"`
	BodyDE  string `json:"body_de"`
}

func NewFixtureProvider() FixtureProvider {
	return FixtureProvider{}
}

func (FixtureProvider) Load(ctx context.Context) ([]domain.SourceDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var fixtures []fixtureDocument
	if err := json.Unmarshal(fixtureData, &fixtures); err != nil {
		return nil, fmt.Errorf("decode release fixtures: %w", err)
	}

	documents := make([]domain.SourceDocument, 0, len(fixtures))
	for _, fixture := range fixtures {
		publishedAt, err := time.Parse(time.RFC3339, fixture.PublishedAt)
		if err != nil {
			return nil, fmt.Errorf("parse fixture publication time for %s: %w", fixture.ExternalID, err)
		}

		document := domain.SourceDocument{
			ExternalID:      fixture.ExternalID,
			SourceURL:       fixture.SourceURL,
			Title:           fixture.Title,
			PublishedAt:     publishedAt,
			FeedFingerprint: digest(fixture.ExternalID, fixture.Title, fixture.PublishedAt),
		}

		for position, incident := range fixture.Incidents {
			contentHash := digest(incident.Number, incident.TitleDE, incident.BodyDE)
			document.Incidents = append(document.Incidents, domain.Incident{
				Number:      incident.Number,
				Position:    position,
				TitleDE:     incident.TitleDE,
				BodyDE:      incident.BodyDE,
				ContentHash: contentHash,
			})
		}

		incidentHashes := make([]string, 0, len(document.Incidents))
		for _, incident := range document.Incidents {
			incidentHashes = append(incidentHashes, incident.ContentHash)
		}
		document.SourceHash = digest(document.Title, strings.Join(incidentHashes, ":"))
		documents = append(documents, document)
	}

	return documents, nil
}

func digest(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", hash[:])
}
