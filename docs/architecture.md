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
Independent LLM work uses one registry-driven post-processing subsystem. Each
processor registration declares metadata, ordering, model setting, scopes,
inputs, named outputs, validation, automatic/manual capabilities, and bounded
aggregate counters. The generic worker and store provide queueing, claims,
retries, recovery, history, status, and metrics without processor branches.
Claims materialize only the input kinds declared for the selected processor
scope; source-backed inputs are never added to another processor's claimed job.
The claim contract includes the prompt version, so jobs left behind by a prompt
upgrade are claimed without newly declared inputs and handled by the worker's
configuration-failure path instead of blocking the queue.
The same contract carries the complete output-kind set. Persistence rejects
missing, duplicate, or undeclared outputs and a changed input hash before any
value is committed. Reader, admin, readiness, and modification-time queries
also select only complete successes for every verifier and translation scope,
which preserves atomic fallback even for legacy or manually corrupted rows.
Correction-style admin readers share one value-pair selection path for latest
complete success, original-value fallback, and latest-attempt state, so another
verifier does not require a new persistence algorithm.

The public-assistance verifier receives the immutable, parser-extracted German
police title and body plus the original metadata status and types. This is an
intentional exception to the minimized post-processing boundary: the configured
Ollama endpoint sees the unredacted German source so the verifier can check the
actual request for public help. Its German prompt returns only a strict verdict,
corrected status, and corrected type codes. The newest successful status/types
pair becomes effective atomically without rewriting the original metadata. A
pending, exhausted, unavailable, malformed, or failed replacement leaves the
previous successful pair effective, or falls back to the original pair when
none has succeeded.

The category verifier receives only the accepted German title, summary, and the
immutable metadata category represented by its German display name. The model
sees and returns only German category names; application-owned mappings convert
those names to stable internal codes before validation and persistence. It
returns a constrained verdict and category;
the newest successful result for that exact presentation run becomes effective
without rewriting the original value. Verification failure never blocks German
publication and is not interpreted as a verdict. A pending, exhausted, or
failed recheck leaves the previous successful result effective, or falls back
to the immutable original metadata category when no verification has succeeded.

Translation scopes are generated from registered languages. Each language owns
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
   enqueues public-assistance verification, category verification, and
   translations for that exact run.

At every job boundary the worker prioritizes canonical work, then registered
post-processors by priority: public-assistance verification, category
verification, then translation. The independent processors have separate model
settings and circuit breakers, so a missing model or failed response does not
pause another processor.

When a newer canonical run queues a language, pending translations for older
runs of the same incident and source revision are superseded. Running attempts
finish safely, while completed translations remain available for audit. A
replacement attempt for the current run leaves that run's prior success visible
until the replacement succeeds.

Scheduled work starts inside the configured Europe/Berlin window. A frozen
cycle may finish after the window closes. Explicit admin or CLI requests persist
manual priority but retain validation, circuit breaking, and retry delays.

The v2 migration records an automatic-scheduling cutover. Each registered
processor scope also has a persisted enablement time. The public-assistance
scope begins automatic work only for presentations completed after its first
deployment-time registration; operators use the admin “process all” action for
older presentations, with no automatic historical backfill or schema migration.
Existing translation and category-verification attempts are migrated into
unified jobs and named values for audit, including imported and superseded
records, but only complete current
`incident-pipeline-v2` runs are eligible for new work or reader selection. The
paginated admin history retains those imported attempts while excluding source
text, model output, and internal error text.

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

Reader-facing language behavior is declared in one compile-time registry. The
server derives routes, locale catalogs, date formatting, navigation, alternate
links, and sitemap entries from it, and startup verifies that every translated
reader registration matches a processing translation definition.

Reader queries choose content and metadata from one presentation run. The
effective category is the newest successful verification for that run, or its
immutable original category when no verification succeeded. A category result
from another run is never mixed into German or translated fallback content.

German selection uses the newest complete current v2 run. A target-language
page uses the newest successful translation for that same v2 run; a pending or
failed replacement leaves the previous successful value for that run visible.
V1, imported, and legacy presentations remain auditable but are never reader or
admin-selection fallbacks. An incident without a complete current v2
presentation is unprocessed and omitted publicly. Metadata always comes from
the selected canonical run. Timeline grouping and pagination remain based on
publication time; incident timing is display metadata.

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
