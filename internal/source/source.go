package source

import (
	"context"

	"github.com/egekocabas/munichbrief/internal/domain"
)

// Provider supplies normalized source documents to the ingestion workflow.
type Provider interface {
	Load(context.Context) ([]domain.SourceDocument, error)
}
