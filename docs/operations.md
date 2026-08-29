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
go run ./cmd/munichbrief ai-process --incident 123

MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief ai-process --all
```

The reader listens on `127.0.0.1:8080` and metrics listen separately on
`127.0.0.1:9090`. The reader provides `/healthz` and `/readyz`; only the
metrics listener provides `/metrics`.

The AI worker automatically considers incidents created after the persisted v2
cutover. It freezes a canonical cycle, processes all metadata-extraction jobs,
and switches to German-presentation jobs. Each German result becomes available
immediately; registered post-processing jobs enter a unified durable queue.
Only complete current `incident-pipeline-v2` presentations are selectable.
Incidents with only v1, imported, or legacy output are unprocessed and remain
hidden publicly until v2 completes.
The five-second AI interval is an idle queue check, while the ten-minute AI
timeout bounds a single Ollama request. Unless immediate mode is enabled, new
Ollama requests start only during the configured Europe/Berlin processing
window; a frozen canonical cycle is allowed to finish after the window closes.
An explicit admin or `ai-process` request persists manual intent and bypasses
only this window. Processing remains sequential and retains privacy validation,
the circuit breaker, and normal retry delays. A command-line request is picked
up by the running server on its next idle worker check.

The protected admin dashboard stores one preferred model for each canonical
step and each registered post-processor. Translation scopes share their
processor model setting. Fresh
databases start unconfigured; upgraded databases migrate the former German
preference to both canonical steps and rename the former English preference to
the shared translation setting. A missing category or translation model pauses
only that independent processor without pausing German processing. The dashboard
displays the automatic v2 and registered processor-scope cutovers. The server refreshes Ollama's
`/api/tags` every 30 seconds; scheduled processing pauses until all required
models are installed, while priority manual cycles retain their per-step model
choices. Catalog failure pauses all AI calls but does not affect reader
readiness. The ten-minute generation timeout already accommodates model loading
delays of roughly 30 seconds.

Structured logs identify AI job and incident IDs, attempts, safe failure
categories, RSS synchronization stages, and press-release document stages.
Completed stages include both a human-readable `duration` and numeric
`duration_seconds`. Source bodies, prompts, generated text, and model responses
are deliberately excluded.

`review` presentation mode displays stored German source text and processing
states and is intended for local fixture development. `public` mode fails closed: it
lists only incidents with a privacy-safe presentation from the active source
hash and supported pipeline lifecycle, and never renders stored originals. Only
a complete current v2 run is eligible; v1 and imported legacy output are
retained for audit but never selected. Back up SQLite
before deploying a migration. German and English pages use explicit `/de` and
`/en` paths; visiting either path refreshes one one-year, HTTP-only preference
cookie used by the root and legacy-route redirects. Enable secure cookies
behind TLS.

`MUNICHBRIEF_PUBLIC_HOSTS` forces each listed hostname into public presentation
and restricts it to reader-safe paths. A public deployment also requires
`MUNICHBRIEF_CANONICAL_ORIGIN`; it must be an HTTPS origin using one of those
hostnames. Discovery documents and canonical links use this single origin even
when an alternate public hostname serves the request. Public reader pages
negotiate a privacy-safe Markdown representation through
`Accept: text/markdown`; review-mode pages remain HTML.

The optional `/admin` dashboard shows live cycle and per-step queue state,
independently paginated incident lists, retained German originals, and both
generated languages. It also shows raw and formatted metadata, canonical and
registry-driven post-processing provenance and queue states, and scheduling
cutovers. Its confirmed actions can
create a canonical cycle for one incident, every canonically unprocessed
incident, or every current incident. Registry-generated post-processing cards
can process one incident or every eligible current v2 presentation with a
selected installed model. Multi-scope processors such as translation can select
one scope or all registered scopes; a single default scope stays hidden. These
manual-priority actions rerun successful work and leave the prior successful
value effective until replacement succeeds. Per-incident retry forms use the
same generic endpoint. There are no model-card history backfill controls. The
application does not authenticate users itself: enable the dashboard only when
the ingress protects `/admin*` and `/api/admin*`, and keep both prefixes absent
from public ingress.

Category verification is correction-only. Its model receives the privacy-safe
German title and summary plus a German category label. German labels are mapped
to stable internal category codes by the application. A retry exhaustion,
unavailable model, malformed response, or terminal failure does not change the
reader category and does not require publication to stop.

An Ollama endpoint using unencrypted HTTP must remain on a restricted network
with narrowly scoped egress. Public presentation mode does not reduce the need
to protect this hop with HTTPS or an encrypted tunnel.

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
outside the application node and PVC; keeping another file on the same disk is
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

- Pull requests run offline tests, race detection, vulnerability and dead-code
  checks, frontend verification, Go builds, and Helm validation.
- Trusted `main` commits publish `sha-<full-commit>` images to GHCR.
- Published images expose their exact source commit and build time in the reader
  footer and carry OCI labels and manifest/index annotations for source, project
  and documentation URLs, license, vendor, version, revision, and creation time.
- Reader and admin HTML use `private, no-store`. Embedded static assets use
  content-fingerprinted URLs with `public, max-age=31556952, immutable`, so a new
  build immediately references new asset URLs while unchanged assets stay cached.
  Any reverse proxy or CDN, including Cloudflare, must respect the origin cache
  headers for this policy to work as intended.
- Container publishing attaches a BuildKit-generated software bill of materials
  (SBOM) to each image.
- Tags matching `vMAJOR.MINOR.PATCH` additionally publish that exact tag and
  normalized semantic-version image tags.
- The deployment repository pins the desired image by tag and digest.
- Dependency automation proposes grouped repository updates; deployment image
  updates remain owned by the deployment repository.
- GitOps reconciles the merged desired state. Application CI does not receive a
  kubeconfig or deploy directly to a cluster.
