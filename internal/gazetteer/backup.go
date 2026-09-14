package gazetteer

import (
	"context"
	"github.com/egekocabas/munichbrief/internal/sqlitebackup"
)

// Backup creates a consistent snapshot of the gazetteer, including refresh history.
func (s *Store) Backup(ctx context.Context, destination string) error {
	return sqlitebackup.Write(ctx, s.db, s.path, destination)
}
