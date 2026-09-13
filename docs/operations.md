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

go run ./cmd/munichbrief gazetteer status

go run ./cmd/munichbrief gazetteer refresh

MUNICHBRIEF_SOURCE_MODE=live \
MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief sync

MUNICHBRIEF_AI_ENABLED=true \
MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief ai-process --incident 123

MUNICHBRIEF_AI_ENABLED=true \
MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief ai-process --all
```

The reader listens on `127.0.0.1:8080` and metrics listen separately on
`127.0.0.1:9090`. The reader provides `/healthz` and `/readyz`; only the
metrics listener provides `/metrics`.

Live mode enables the separate place-name gazetteer by default. It refreshes
immediately at startup and weekly thereafter, using conditional HTTP requests
and retaining the last successful generation on any failure. Set
`MUNICHBRIEF_GAZETTEER_ENABLED=false` only for offline diagnostics; production
servers pause the translation processor while the gazetteer is explicitly
disabled so place-name protection cannot be bypassed. Canonical German and
other independent processors can continue. `gazetteer status` is network-free, while
`gazetteer refresh` performs one bounded refresh regardless of the scheduler
setting. The rebuildable database defaults to
`.data/munichbrief-gazetteer.db`; deleting it removes no incident or translated
content, but new translation claims pause until the next successful refresh.

The AI worker automatically considers incidents created after the persisted v2
cutover. It freezes a canonical cycle, processes all metadata-extraction jobs,
and switches to German-presentation jobs. Each German result becomes available
immediately; registered post-processing jobs enter a unified durable queue.
Only complete current `incident-pipeline-v2` presentations are selectable.
Incidents with only v1, imported, or legacy output are unprocessed and remain
hidden publicly until v2 completes.
The default fifteen-second AI interval is an idle queue check; manual actions
wake the worker immediately, and available jobs run without an interval between
them. The fifteen-minute AI timeout bounds a single Ollama request.
Unless immediate mode is enabled, new Ollama requests start only during the
configured Europe/Berlin processing window; a frozen canonical cycle is allowed to finish after the window closes.
When its final German result completes outside the window, scheduled
post-processing discovery waits for the next open window rather than creating
new automatic jobs immediately. Continuation cycles retain scheduled
post-processing provenance and do not gain the manual bypass.
An explicit admin or `ai-process` request persists manual intent and bypasses
only this window. The CLI still requires the deployment-level
`MUNICHBRIEF_AI_ENABLED=true` gate. Processing remains sequential and retains
privacy validation, the circuit breaker, and normal retry delays. A command-line
request is picked up by the running server on its next idle worker check.
Each request freezes the configured model and adapter for every selected reader
language; languages without a selected model remain paused.

The protected admin dashboard also has a durable automatic-processing master
switch. Disabling it overrides an open window for scheduled canonical work and
all registered post-processors, but does not block explicit admin or
`ai-process` requests. A running automatic Ollama request is allowed to finish;
the cycle then waits with its frozen models, prompts, progress, and original
window authorization, including when the request ends in a retryable provider
or model-configuration failure. Re-enabling wakes the worker, and an
already-authorized cycle may resume outside the window under the same
finish-after-close rule.
If a scheduled or continuation cycle is waiting on a retry or circuit breaker,
a queued explicit canonical request takes its running lease and the automatic
cycle resumes afterward with its accepted results intact.

The Verifications and Translations pages provide narrower durable gates below
the global runtime switch. The Translations page has a translation-wide switch
and one switch per reader language. Automatic translation runs only when the
global, translation-wide, and language switches are all enabled. Processor
gates start enabled; verification scopes and English translation start enabled,
while all other translation languages start disabled. Disabling the
translation-wide gate marks pending and retrying scheduled translations as
skipped with `processor_disabled`; disabling one language uses `scope_disabled`.
Running jobs finish, unrelated processors are unaffected, and explicit manual
rechecks or translations remain available. Enabling either narrow gate does not
wake the worker or enqueue inside the admin request. The next normal discovery
pass uses unchanged cutovers and queues eligible work accumulated while the gate
was disabled.

“Cancel all unfinished work” is the immediate-stop operation. It disables
automatic processing, interrupts the current Ollama request, and terminalizes
waiting, queued, retrying, and running canonical and post-processing jobs as
operator-canceled. It preserves completed publications, successful correction
and translation values, and audit history. The action is idempotent. Manual work
can be requested immediately afterward; automatic eligibility is rediscovered
only after the master switch is enabled again.

The protected admin dashboard stores one preferred model for each canonical
step and ordinary registered post-processor. `/admin/translations` stores an
independent model and adapter for every reader language. Fresh databases start
unconfigured; on upgrade, only the pre-existing English target inherits the
former shared translation model under the structured adapter. Newly registered
languages remain paused until an operator chooses a reviewed route, and later
startup checks never overwrite that choice. New jobs freeze the route, while
queued jobs do not change when a preference changes. A missing public-assistance, category, or
language-specific translation model pauses only that independent processor
without pausing German processing or another processor. The dashboard
displays the automatic v2 and registered processor-scope cutovers. The server refreshes Ollama's
`/api/tags` every 30 seconds; scheduled processing pauses until all required
models are installed, while priority manual cycles retain their per-step model
choices. Catalog failure pauses all AI calls but does not affect reader
readiness. The ten-minute generation timeout already accommodates model loading
delays of roughly 30 seconds.

Structured logs identify AI job and incident IDs, attempts, safe failure
categories, queue discovery and manual queueing, RSS synchronization stages,
and press-release document stages.
Completed stages include both a human-readable `duration` and numeric
`duration_seconds`. Source bodies, prompts, generated text, and model responses
are deliberately excluded.

Every live RSS synchronization also creates a durable database history row
before contacting the source and completes it with the feed document count,
conditional-response state, seven-day window count, fetched and skipped totals,
separate fetch and parser failures, duration, and a sanitized terminal error.
The protected `/admin/rss-history` page shows this history in stable,
reverse-chronological cursor pages. Starting the next check marks any prior
`running` row as a failed interruption, while the active row remains visible
during the current check. History begins with the first sync after migration
014; the previous single-row synchronization state remains the source for
request validators and last-success metrics.

After migration 015, each check also records document observations and fetch
outcomes. The table shows distinct in-window new/existing documents (including
existing articles selected for retry/refresh) and inserted/updated/unchanged
incident counts. “Parsed and stored” counts articles whose parsing and database
writes succeeded. Expand a check, then a document, for its extracted German
text, parser output, and database outcomes at that time. Detail pages support
cursor pagination and work without JavaScript; polling preserves open panels.
Reopen a panel to refresh its details while a check is running.

Document identity uses the existing source URL; incident identity uses the
existing source-document/position pair. These counts describe storage changes,
not semantic matching across reports. Out-of-window feed documents are listed
separately, and malformed/discarded feed entries retain only the source client's
aggregate skipped count. A 304 response can still produce article retries.
Older checks show “Details not recorded” and unknown counts rather than inferred
historical results. Interrupted checks retain completed document outcomes.

Extracted text and parsed results are immutable snapshots shared by content hash.
History does not read mutable incident text. Raw HTML is never retained, and
snapshots are served only through protected admin routes with no-store headers.
Snapshots have no automatic expiry and increase database/backup storage; see
[retention policy](source-policy.md#retention).

Gazetteer metrics report attempts, failures, last success, next refresh, active
entry count, and refresh duration. Source responses and errors are logged
without downloaded payloads. The fixed sources are Landeshauptstadt München –
GeodatenService (`dl-de/by-2.0`), GeoNames (`CC BY 4.0`), and OpenStreetMap
contributors (`ODbL 1.0`). Public HTTPS egress must remain enabled for refresh.
The protected `/admin/gazetteer` view shows the same operational state plus
the latest health diagnosis, active entry counts by type, paginated refresh
history, and per-source outcomes. Failed attempts show the exact sanitized
stage and public-source diagnostic while confirming whether the previous valid
generation remains active. Startup recovers an abandoned attempt as
`interrupted`, preserving completed sources and marking sources that were never
reached. The newest 500 completed attempts and every running attempt are
retained in the rebuildable Gazetteer database. Diagnostics are limited to 2
KiB and never include response bodies, downloaded place-name payloads,
credentials or incident text. It also shows bounded source provenance,
retained generations, and overrides, but never the complete name set. An
authenticated operator can request a refresh from the page. The request is
asynchronous, uses the same serialized manager and durable history as scheduled
refreshes, and coalesces duplicate requests while work is running or queued.
The `munichbrief gazetteer refresh` command remains available for a synchronous
one-shot operational attempt.

`review` presentation mode displays stored German source text and processing
states and is intended for local fixture development. `public` mode fails closed: it
lists only incidents with a privacy-safe presentation from the active source
hash and supported pipeline lifecycle, and never renders stored originals. Only
a complete current v2 run is eligible; v1 and imported legacy output are
retained for audit but never selected. Back up SQLite
before deploying a migration. Reader pages use explicit `/de`, `/en`, `/tr`,
`/hr`, `/it`, `/uk`, `/bs`, `/zh`, `/hi`, `/es`, `/fr`, `/ro`, `/pl`,
and `/ru` paths; visiting any registered path refreshes a one-year, HTTP-only
preference cookie used by the root and legacy-route
redirects. Enable secure cookies behind TLS.
Additional languages use the same registry-driven contract and require an
explicit public ingress prefix. Follow [Adding a reader language](adding-a-language.md)
for the safe ingress-first rollout and manual historical backfill.

`MUNICHBRIEF_PUBLIC_HOSTS` forces each listed hostname into public presentation
and restricts it to reader-safe paths. A public deployment also requires
`MUNICHBRIEF_CANONICAL_ORIGIN`; it must be an HTTPS origin using one of those
hostnames. Discovery documents and canonical links use this single origin even
when an alternate public hostname serves the request. Public reader pages
negotiate a privacy-safe Markdown representation through
`Accept: text/markdown`; review-mode pages remain HTML.

The optional `/admin` dashboard shows live cycle and per-step queue state,
independently paginated incident lists, retained German originals, compact
translation rollups, raw and formatted metadata, canonical and registry-driven
post-processing provenance and queue states, and scheduling cutovers. Its
confirmed actions can
create a canonical cycle for one incident, every canonically unprocessed
incident, or every current incident. Registry-generated post-processing cards
can process one incident or every eligible current v2 presentation with a
selected installed model. Multi-scope processors such as translation can select
one scope or all registered scopes; a single default scope stays hidden. These
manual-priority actions rerun successful work and leave the prior successful
value effective until replacement succeeds. There are no model-card history
backfill controls.

Each independent post-processing queue separates active execution from waiting
time. **Current job elapsed** is shown only while a worker owns a job. **Oldest
unfinished job queued** is queue age, not continuous execution time. Retrying
jobs show their failure-class counts and whether automatic work is still in
backoff, ready but paused outside the processing window, or paused by the
automatic-processing switch. Raw model errors and incident content are not
included in runtime status. **Review** is terminal for the attempt: output or
privacy validation remained unsuccessful after three tries, so an operator
should investigate the failure class before explicitly reprocessing it.

The dedicated `/admin/translations` page is the operational view for every
registered noncanonical language. Its coverage denominator is the current,
complete German v2 presentation set. A translation is **published** only when a
successful job for that exact current German run has both normalized `title`
and `summary` values; every other eligible presentation is **unpublished**.
**Never queued** means no translation attempt exists for that language and run.
**Active** includes pending and running jobs. **Attention** includes
review-required, failed, and skipped latest attempts. These counters overlap on
purpose: when a replacement fails, the retained earlier success remains
published while the newest attempt also appears under attention and the
replacement-warning count.

The translation overview shows the translation-wide automatic gate and the
independent gate for each language. Model and adapter preferences remain stored
while either gate is disabled, and neither switch removes published
translations.

Language drill-down filters are `all`, `published`, `unpublished`,
`never_queued`, `active`, and `attention`. Incident drill-down shows the German
canonical presentation plus each registered language's effective output,
published model/adapter/prompt provenance, latest-attempt provenance, and retry
action. The overview stores one preferred installed model and supported adapter
per language; unavailable routes are visibly paused. **Queue unpublished**
transactionally queues current German presentations that have no publishable
translation, excluding active and already-published work. **Rerun all** creates
manual replacements for every eligible presentation except active work. Both
bulk actions and an incident retry require an installed model, supported
adapter, and explicit confirmation; they bypass the automatic-processing switch
and cutover without changing either. Historical work is therefore always an
operator decision.

The translation operations region refreshes from its current URL every five
seconds, preserving its filter, page, incident view, and scroll position. It
pauses while the page is hidden, a form has focus, the confirmation dialog is
open, or a refresh is already running. Failed refreshes mark the view stale and
back off to at most 30 seconds.

The application does not authenticate users itself: enable administration only
when the ingress protects `/admin*` and `/api/admin*`, and keep both prefixes
absent from public ingress.

Runtime status reports both the configured window and the durable automatic
switch so an open window is not mistaken for runnable scheduled work. Switch
changes and cancellation counts are logged without source text, prompts, or
model output.

Public-assistance verification is correction-only and has the highest
post-processing priority after the canonical metadata and German-presentation
stages. Its German prompt receives the unredacted, parser-extracted German title
and body plus the original assistance status/types through the configured
Ollama endpoint. It never receives a generated or translated presentation and
returns only the constrained verdict/status/types JSON. This raw-source decision
means the Ollama network hop must be treated as sensitive. Logs and paginated
history continue to exclude incident text, prompts, model output, and internal
error text. A retry exhaustion, unavailable model, malformed response, or
terminal failure leaves the prior successful assistance result effective, or
the original metadata when there has been no success, and does not block
category verification, translation, or German publication.

The verifier scope uses the registry's persisted cutover and automatic-work
gate. New presentations are queued automatically once its preferred model is
configured and the scope is enabled.
Existing presentations are not backfilled automatically; use the protected
post-processing “process all” control in the admin dashboard. Migration `021`
adds per-scope gates, and migration `022` adds processor-wide gates; neither
changes existing cutovers or job history.

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

### AI request timeout and retry delays

AI requests default to a 15-minute timeout (`MUNICHBRIEF_AI_TIMEOUT`, or
`application.aiTimeout` in Helm). Transient retries start at 30 seconds, then
2 minutes, then 5 minutes. Configuration retries use 5 minutes; output and
privacy retries start at 1 minute and then use 5 minutes, retaining the existing
three-attempt review limit. Jitter never extends an AI retry delay past 5 minutes.
Existing persisted retry timestamps remain unchanged; the cap applies when the
next failure is recorded. Queue ordering and canonical-cycle completion can
still delay when an eligible retry actually runs.

## Contact and legal pages

See [contact operations and decisions](contact-and-legal.md) for feature activation,
SMTP2GO credentials and budgets, proxy trust, private inbox access, retention,
monitoring and failure recovery. Automated backups remain deferred.
