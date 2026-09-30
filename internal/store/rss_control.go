package store

import (
	"context"
	"database/sql"
	"fmt"
)

// RSSSyncEnabled returns the durable gate for scheduled and one-shot syncs.
func (s *Store) RSSSyncEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	if err := s.db.QueryRowContext(ctx, `SELECT enabled FROM rss_runtime_control WHERE id=1`).Scan(&enabled); err != nil {
		return false, fmt.Errorf("read RSS synchronization control: %w", err)
	}
	return enabled, nil
}

func (s *Store) SetRSSSyncEnabled(ctx context.Context, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE rss_runtime_control SET enabled=? WHERE id=1`, boolInt(enabled))
	if err != nil {
		return fmt.Errorf("set RSS synchronization control: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
