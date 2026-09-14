package store

import (
	"context"
	"github.com/egekocabas/munichbrief/internal/sqlitebackup"
)

// Backup creates a consistent standalone database without overwriting a file.
func (s *Store) Backup(ctx context.Context, destination string) error {
	return sqlitebackup.Write(ctx, s.db, s.path, destination)
}
