package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestPreparedStatementsPreserveTransactionsAndBindings(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "statements.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.db.ExecContext(ctx, "CREATE TABLE statement_test(value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	const insert = "INSERT INTO statement_test(value) VALUES(?)"
	statements, err := database.prepareStatements(ctx, insert)
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		tx, err := database.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.StmtContext(ctx, statements[insert]).ExecContext(ctx, 42); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM statement_test WHERE value=42").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (commit && count != 1) || (!commit && count != 0) {
			t.Fatalf("commit=%t: persisted %d rows", commit, count)
		}
	}

	const selectValue = "SELECT ?"
	var workers sync.WaitGroup
	for value := range 8 {
		workers.Go(func() {
			prepared, err := database.prepareStatements(ctx, insert, selectValue)
			if err != nil {
				t.Error(err)
				return
			}
			if prepared[insert] != statements[insert] {
				t.Error("recompiled the same query")
			}
			var got int
			if err := prepared[selectValue].QueryRowContext(ctx, value).Scan(&got); err != nil || got != value {
				t.Errorf("concurrent bound value = %d/%v, want %d", got, err, value)
			}
		})
	}
	workers.Wait()
	if _, err := database.db.ExecContext(ctx, `
		CREATE TABLE statement_audit(value INTEGER);
		CREATE TRIGGER statement_audit_insert AFTER INSERT ON statement_test
		BEGIN INSERT INTO statement_audit(value) VALUES(new.value); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := statements[insert].ExecContext(ctx, 7); err != nil {
		t.Fatal(err)
	}
	var audited int
	if err := database.db.QueryRowContext(ctx, "SELECT value FROM statement_audit").Scan(&audited); err != nil || audited != 7 {
		t.Fatalf("cached statement ignored changed schema: %d/%v", audited, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := statements[insert].ExecContext(ctx, 99); err == nil {
		t.Fatal("cached statement executed after store close")
	}
}

func TestPreparedStatementsRecoverAfterPreparationFailure(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "statements.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := database.prepareStatements(canceled, "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare = %v", err)
	}
	const query = "SELECT COUNT(*) FROM later_table"
	if _, err := database.prepareStatements(ctx, "SELECT 1", query); err == nil {
		t.Fatal("prepared a query against a missing table")
	}
	if _, err := database.db.ExecContext(ctx, "CREATE TABLE later_table(value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	statements, err := database.prepareStatements(ctx, "SELECT 1", query)
	if err != nil {
		t.Fatalf("failed preparation poisoned later attempts: %v", err)
	}
	var count int
	if err := statements[query].QueryRowContext(ctx).Scan(&count); err != nil || count != 0 {
		t.Fatalf("query after schema change = %d/%v", count, err)
	}
}
