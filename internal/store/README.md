# Store

This package is the sole owner of the SQLite database. `Open` configures the
connection, applies embedded migrations, and seeds required singleton state.
Public methods either execute one bounded query or own the transaction needed to
preserve a state-machine invariant.

The files are grouped by responsibility:

- `store.go`, `live.go`, and `timeline.go`: source documents, incidents, sync
  state, and basic timeline reads.
- `presentation.go`: public/review visibility rules and localized presentation
  queries.
- `pipeline_types.go` and `pipeline_settings.go`: stable pipeline values and
  model preferences.
- `pipeline_cycles.go`, `pipeline_jobs.go`, and `pipeline_status.go`: cycle
  lifecycle, atomic job transitions, and operational snapshots.
- `post_processing.go`: the registry-neutral, run-scoped post-processing queue
  whose failures never roll back canonical publication.
- `backup.go`: SQLite online backup with destination safety checks.

Pipeline cycles freeze their ordered steps, prompt versions, models, and target
incident IDs when activated. Jobs are claimed and completed transactionally;
callers must not reproduce those transitions with separate reads and writes.
Stored AI values become visible only through the presentation queries and only
when their privacy and completion requirements are satisfied.

Schema changes belong in a new migration. See
[migrations/README.md](migrations/README.md) before changing tables or indexes.
Tests should use `t.TempDir()` and the real SQLite implementation so transaction,
constraint, and migration behavior remains covered.
