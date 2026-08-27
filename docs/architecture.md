# Architecture

MunichBrief is one Go process with a single SQLite writer. It combines source
discovery, bounded article fetching, deterministic parsing, asynchronous AI
processing, and server-rendered delivery without sharing the database between
replicas.

## Components

- `cmd/munichbrief` wires configuration, storage, HTTP servers, synchronization,
  processing, lifecycle, and operational commands.
- `internal/source` loads synthetic fixtures or the official RSS feed and
  same-origin articles with strict URL, redirect, timeout, type, and size
  controls.
- `internal/parser` converts one source document into one or more incident
  records and hashes the retained source representation.
- `internal/ingest` coordinates conditional synchronization, refresh policy,
  retries, persistence, and source metrics.
- `internal/store` owns migrations, SQLite transactions, presentation queries,
  processing cycles, and consistent backups.
- `internal/processing` defines the staged prompt registry, Ollama clients,
  privacy validation, scheduling, retries, circuit breaking, and sequential
  worker.
- `internal/web` serves localized reader, discovery, static, health, and
  optional protected administration routes.
- `internal/observability` exposes Prometheus-format application metrics on a
  separate listener.

## Source and incident model

An RSS entry represents a source document, not necessarily one incident. A
combined daily release is split into numbered incidents that retain the same
official source URL. A standalone release becomes one incident. Releases are
never merged solely because they share a publication date.

The canonical police article URL and numeric article ID form the external
document identity because the feed does not reliably include a GUID. Source
documents retain request validators and fetch state. Incidents retain the
parsed German source fields and content hash used to invalidate derived output
when the source changes.

## Processing pipeline

Canonical AI work is persisted as a frozen cycle with ordered steps. The
current pipeline is `incident-pipeline-v2` and performs two narrowly scoped
operations:

1. `incident_metadata` receives the privacy-minimised German source plus the
   release timestamp, its localized weekday, `Europe/Berlin`, and direct lookup
   maps for recent relative days and weekdays. It extracts category, broad area,
   one primary incident date with an optional clock time or day part, report
   kind, and any explicit public-assistance request. The application does not
   parse changing German time phrases itself.
2. `german_presentation` receives only the minimized source and validated
   metadata. It creates the canonical privacy-safe German title and summary.

When stage 2 succeeds, that presentation run becomes complete immediately.
Translations are a separate durable subsystem. Each registered language owns
an immutable prompt, schema, validator, generator, and enablement cutover, while
all languages share one preferred translation model. English is currently the
only target and receives only the accepted German title and summary.

Application code localizes metadata labels. Each step declares its ordered
input kinds; the worker passes only those values and hashes the actual inputs
with prompt version and model identity. A metadata change therefore invalidates
the German-stage input without exposing source text to the English stage. Each
job stores its prompt version, model identity, input hash, timestamps, status,
and safe failure category. Reader pages label extracted timing neutrally as a
time stated in the report.

The worker:

1. Activates one eligible cycle and freezes its target incidents and models.
2. Processes every job for the current step sequentially.
3. Validates schemas, temporal consistency and relative-date resolution,
   source-grounded areas and assistance requests, and privacy before storage.
4. Advances only when the step has no unfinished jobs.
5. Publishes each German presentation as soon as its stage-2 job succeeds and
   enqueues independent translations for that exact run.

Scheduled work starts inside the configured Europe/Berlin window. A frozen
cycle may finish after the window closes. Explicit admin or CLI requests persist
manual priority but retain validation, circuit breaking, and retry delays.

The v2 migration records an automatic-scheduling cutover. Incidents existing at
that boundary are not automatically upgraded, and unfinished v1 work is marked
superseded so the canonical worker cannot resume an incompatible snapshot.
Language enablement has its own cutover: new canonical runs translate
automatically, while historical translation requires an explicit admin
backfill. Completed v1 and legacy English values are imported into generic
translation records without rewriting their original audit values.

## Presentation boundary

`review` mode exposes retained German source text and processing state for
local or access-controlled quality review. `public` mode fails closed and lists
only incidents with a complete, privacy-safe presentation for the active source
hash and prompt lifecycle.

`MUNICHBRIEF_PUBLIC_HOSTS` applies that public scope and a route allowlist by
request hostname even when another listener uses review mode. A canonical HTTPS
origin is mandatory when public hosts are configured. Public HTML also exposes
canonical and language-alternate links, a sitemap, crawler policy, and a
privacy-safe Markdown representation.

German selection prefers the newest completed v2 run, then a completed v1 run,
then an imported legacy bilingual run. A target-language page selects the
newest canonical run with a completed translation for that language; a pending
or failed newer translation leaves an older translated run visible, and an
incident with no translated fallback is omitted. Metadata always comes from
the same canonical run as the selected translation. Timeline grouping and
pagination remain based on publication time; incident timing is display
metadata.

The optional admin routes contain retained originals and processing controls.
The application does not authenticate them; the ingress must protect both
`/admin*` and `/api/admin*`, and public ingress rules must omit them.

## Storage and lifecycle

Migrations are embedded and applied transactionally when the store opens.
SQLite uses one application process and one Kubernetes replica. Persistent
storage uses `ReadWriteOnce`, but that access mode does not by itself prevent
two pods on the same node, so the chart enforces one replica and a `Recreate`
strategy.

The `backup` command uses SQLite `VACUUM INTO` to produce a transactionally
consistent standalone database. Restore remains an explicit, offline operator
procedure documented in [Operations](operations.md).

## Trust boundaries

- Police feed and article HTML are untrusted network input.
- Source text is untrusted prompt input and is never treated as model
  instructions.
- Ollama output is untrusted until schema, length, language, and privacy checks
  pass.
- Public requests are host-scoped and cannot reach review or administration
  paths.
- Metrics bind separately so they need not be exposed through reader ingress.
- Logs exclude source bodies, prompts, generated text, and raw model responses.
