# MunichBrief operations

## Local commands

The default command and source mode remain safe for offline development:

```bash
go run ./cmd/munichbrief
```

Use a separate database for live ingestion:

```bash
MUNICHBRIEF_SOURCE_MODE=live \
MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief serve
```

Operational commands use the same environment configuration:

```bash
go run ./cmd/munichbrief migrate

MUNICHBRIEF_SOURCE_MODE=live \
MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief sync

MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
MUNICHBRIEF_OLLAMA_MODEL=qwen3.5:4b \
go run ./cmd/munichbrief ai-retry --incident 123

MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
MUNICHBRIEF_OLLAMA_MODEL=qwen3.5:4b \
go run ./cmd/munichbrief ai-retry --all
```

The reader listens on `127.0.0.1:8080` and metrics listen separately on
`127.0.0.1:9090`. The reader provides `/healthz` and `/readyz`; only the
metrics listener provides `/metrics`.

The AI worker discovers both existing and newly synchronized incidents with
stored German text. It processes ready incidents newest-first, one at a time.
The five-second AI interval is an idle queue check, while the ten-minute AI
timeout bounds a single Ollama request.

`review` presentation mode displays stored German source text and processing
states and must remain behind access control. `public` mode fails closed: it
lists only incidents with a privacy-safe presentation from the active source
hash, model, and prompt, and never renders stored originals. Back up SQLite
before deploying a migration. The language selector uses one one-year,
HTTP-only preference cookie; enable secure cookies behind TLS.

The configured pi8 endpoint currently uses unencrypted HTTP on a restricted
LAN. Limit egress to `192.168.178.102/32:11434` and do not enable public mode
until this hop is protected by HTTPS or an encrypted tunnel and the legal
launch review is complete.

## Production-style container

Build and run locally with a read-only root filesystem and the same writable
paths used by Kubernetes:

```bash
docker build --build-arg VERSION=local -t munichbrief:local .

docker run --rm \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m,mode=1777 \
  --volume munichbrief-data:/data \
  --publish 8080:8080 \
  --publish 127.0.0.1:9090:9090 \
  --env MUNICHBRIEF_ADDR=:8080 \
  --env MUNICHBRIEF_METRICS_ADDR=:9090 \
  --env MUNICHBRIEF_DATABASE_PATH=/data/munichbrief.db \
  --env MUNICHBRIEF_SOURCE_MODE=live \
  munichbrief:local
```

## Consistent SQLite backup

`backup` uses SQLite `VACUUM INTO`, producing a transactionally consistent,
standalone database without copying WAL files. It refuses to overwrite an
existing destination.

```bash
mkdir -p backups

MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief backup \
  --output backups/munichbrief-2026-08-22.db
```

The source application may remain running during this operation. Verify the
result before treating it as recoverable:

```bash
MUNICHBRIEF_DATABASE_PATH=backups/munichbrief-2026-08-22.db \
go run ./cmd/munichbrief migrate
```

For Kubernetes, stream the backup directly off the PVC:

```bash
umask 077
kubectl -n munichbrief exec deployment/munichbrief -- \
  /munichbrief backup --output - \
  > munichbrief-$(date +%Y%m%d-%H%M%S).db
```

Check the command exit status and validate the downloaded database with the
`migrate` command. A scheduled job must eventually send backups to storage
outside pi16 and the application PVC; keeping another file on the same NVMe is
not disaster recovery.

## Restore

Restoration is intentionally an operator-controlled action:

1. Stop every MunichBrief process that can access the database. In Kubernetes,
   pause Argo CD reconciliation for the Application and scale the Deployment to
   zero first.
2. Validate the backup with `munichbrief migrate` against the backup path.
3. Move the current database and any `-wal`/`-shm` companions to a recovery
   directory instead of deleting them.
4. Copy the validated backup to the configured database path with mode `0600`.
5. Start one application replica and confirm `/readyz`, the timeline, and the
   latest synchronization state before resuming Argo CD reconciliation.

Do not restore while the application is running. Do not place two SQLite
writers on the PVC; `ReadWriteOnce` still permits multiple pods on one node.

## Release and deployment ownership

- Pull requests run offline tests, race detection, Helm validation, and a
  non-publishing AMD64/ARM64 image build.
- Trusted `main` commits publish `sha-<full-commit>` images to GHCR.
- Tags matching `vMAJOR.MINOR.PATCH` additionally publish that exact tag and
  normalized semantic-version image tags.
- The `homelab-infra` repository pins the desired image by tag and digest.
- Renovate will eventually propose release/digest updates in that repository.
- Argo CD reconciles the merged desired state. GitHub Actions never receives a
  kubeconfig and never runs `kubectl` against pi16.
