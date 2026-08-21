package source

import (
	"context"
	"testing"
)

func TestFixtureProviderLoadsBundleAndStandaloneRelease(t *testing.T) {
	documents, err := NewFixtureProvider().Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(documents) != 2 {
		t.Fatalf("len(documents) = %d, want 2", len(documents))
	}
	if len(documents[0].Incidents) != 2 {
		t.Errorf("daily incident count = %d, want 2", len(documents[0].Incidents))
	}
	if len(documents[1].Incidents) != 1 {
		t.Errorf("standalone incident count = %d, want 1", len(documents[1].Incidents))
	}
	if documents[0].SourceHash == "" || documents[0].Incidents[0].ContentHash == "" {
		t.Error("fixture hashes must not be empty")
	}
}
