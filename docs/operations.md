# Running MunichBrief

## Try it locally

Requires Go 1.26.6 or newer:

```bash
go run ./cmd/munichbrief
```

Open <http://127.0.0.1:8080>. The default fixture mode uses invented reports,
stays offline, and creates `.data/munichbrief.db`. Frontend assets are embedded
in the binary; Node.js is needed only to rebuild them.

## Deploy with Helm

```bash
helm install munichbrief ./charts/munichbrief \
  --namespace munichbrief --create-namespace
```

Defaults keep fixture mode enabled and ingress, AI, and admin disabled. Start
from [example values](../deploy/example-values.yaml) for a public deployment.
The [chart guide](../charts/munichbrief/README.md) covers ingress and auth settings.

| Setting | Purpose |
| --- | --- |
| `MUNICHBRIEF_SOURCE_MODE` | `fixture` or explicit `live` ingestion |
| `MUNICHBRIEF_PRESENTATION_MODE` | Local `review` or publication-only `public` |
| `MUNICHBRIEF_PUBLIC_HOSTS` | Hosts restricted to public content and routes |
| `MUNICHBRIEF_CANONICAL_ORIGIN` | Canonical HTTPS origin on a configured public host |
| `MUNICHBRIEF_AI_ENABLED` | Enables the processing worker |
| `MUNICHBRIEF_OLLAMA_BASE_URL` | Separately hosted model endpoint |
| `MUNICHBRIEF_DATABASE_PATH` | Main SQLite database |
| `MUNICHBRIEF_GAZETTEER_DATABASE_PATH` | Rebuildable place-name database |

See [configuration](../internal/config/config.go) and [chart values](../charts/munichbrief/values.yaml)
for the full settings. Keep credentials and personal deployment values outside
Git. Read [privacy and sources](source-policy.md) before enabling live ingestion.

## Access and storage

- Run one application replica: SQLite files are not shared between app writers.
- The chart uses a non-root container, a read-only root filesystem, and writable
  `/data` and `/tmp` mounts.
- Admin authentication is provided by the ingress. Protect both `/admin` and
  `/api/admin`; do not expose the application port directly to untrusted clients.
- Application health endpoints are `/healthz` and `/readyz`. Metrics use a separate
  listener, defaulting to `127.0.0.1:9090`.
- Readiness checks storage availability; it does not mean every model or
  translation is ready.

## Backups and upgrades

`munichbrief backup --output FILE` creates a consistent snapshot of the configured
main database. Add `--database gazetteer` for the separate place-name database.
The two snapshots are not an atomic pair.

Keep verified copies outside the application disk. Before restoring, stop every
writer, preserve the current database and its WAL/SHM files, then restore a
validated backup. Back up before upgrades that apply migrations.
Automated off-machine backups are not included.

For available commands, see [the CLI](../cmd/munichbrief/main.go); build and
validation steps are recorded in [CI](../.github/workflows/ci.yml).
