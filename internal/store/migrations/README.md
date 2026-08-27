# Database migrations

Migrations are embedded and applied in filename order when `store.Open` starts.
Each migration runs once and is recorded in `schema_migrations`.

Rules for schema changes:

- Add a new, sequentially numbered `.sql` file; never rewrite a migration that
  may already have run in another installation.
- Keep each migration transactional and safe for data created by every earlier
  application version.
- Prefer explicit constraints and indexes over application-only assumptions.
- Do not add deployment-specific data, hostnames, credentials, or personal paths.
- Add an upgrade test that creates the prior shape, opens it with the current
  store, and verifies both preserved data and the new invariant.

SQLite migrations cannot assume a network service or external migration tool.
Back up operational databases before upgrading; see
[Operations](../../../docs/operations.md).
