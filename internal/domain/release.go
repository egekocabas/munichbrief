package domain

import "time"

// SourceDocument represents one official page discovered by a source provider.
// A source document can contain one or many incidents.
type SourceDocument struct {
	ExternalID      string
	SourceURL       string
	Title           string
	PublishedAt     time.Time
	FeedFingerprint string
	SourceHash      string
	Incidents       []Incident
}

// Incident represents one numbered report within a source document.
type Incident struct {
	Number      string
	Position    int
	TitleDE     string
	BodyDE      string
	ContentHash string
}
