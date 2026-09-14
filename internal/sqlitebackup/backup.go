package sqlitebackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Write creates a transactionally consistent standalone SQLite database.
// The destination must not already exist so an operator cannot accidentally
// overwrite a previous backup.
func Write(ctx context.Context, db *sql.DB, sourcePath, destination string) error {
	if destination == "" || destination == "-" {
		return errors.New("backup destination path is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup destination: %w", err)
	}
	if sourcePath != ":memory:" && !filepath.IsAbs(sourcePath) {
		absDatabase, err := filepath.Abs(sourcePath)
		if err != nil {
			return fmt.Errorf("resolve database path: %w", err)
		}
		if absDatabase == absDestination {
			return errors.New("backup destination must differ from the live database")
		}
	} else if sourcePath == absDestination {
		return errors.New("backup destination must differ from the live database")
	}
	if _, err := os.Stat(absDestination); err == nil {
		return errors.New("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absDestination), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", absDestination); err != nil {
		_ = os.Remove(absDestination)
		return fmt.Errorf("create sqlite backup: %w", err)
	}
	if err := os.Chmod(absDestination, 0o600); err != nil {
		return fmt.Errorf("restrict backup permissions: %w", err)
	}
	return nil
}
