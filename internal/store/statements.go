package store

import (
	"context"
	"database/sql"
	"sync"
)

type preparedStatementCache struct {
	mu      sync.Mutex
	byQuery map[string]*sql.Stmt
}

// prepareStatements reuses compiled SQL for a fixed set of hot queries. Call
// before BeginTx: the store has one connection, so preparing through the DB
// while holding a transaction would wait for that same connection. Bind writes
// with tx.StmtContext to retain the caller's transaction and rollback behavior.
// Only pass fixed queries or a finite set of application-defined variants,
// never SQL expanded from request values. Store.Close closes their connection.
func (s *Store) prepareStatements(ctx context.Context, queries ...string) (map[string]*sql.Stmt, error) {
	s.statements.mu.Lock()
	defer s.statements.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.statements.byQuery == nil {
		s.statements.byQuery = make(map[string]*sql.Stmt)
	}
	prepared := make(map[string]*sql.Stmt, len(queries))
	for _, query := range queries {
		statement := s.statements.byQuery[query]
		if statement == nil {
			var err error
			statement, err = s.db.PrepareContext(ctx, query)
			if err != nil {
				return nil, err
			}
			s.statements.byQuery[query] = statement
		}
		prepared[query] = statement
	}
	return prepared, nil
}
