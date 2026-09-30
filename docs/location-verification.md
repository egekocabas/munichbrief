# Location verification

Location verification is an independent post-canonical processor. It reads the
original incident title/body and captured release-section context. It never
changes the initial metadata prompt, blocks publication, or rewrites summaries.

## Effective public location

A current, grounded `confirmed` or `corrected` assessment can supply one public
area name/type. Otherwise the canonical metadata remains the fallback, including
an absent area. A pending, failed, or unresolved replacement does not erase an
applicable previous correction. A source/context change invalidates its override.
Cards, detail pages, Markdown, search, filters, and modification timestamps use
the same selection. Original extraction values remain in presentation history.

`ambiguous`, `no_location`, `withheld`, and `source_problem` are completed
assessments, not model failures. They appear in admin Attention and do not cause
retry loops. Source-only checks explicitly mark summary wording as not assessed.
Historical v1 wording-conflict flags remain visible without regenerating text.
A legitimate multi-scene report may use one source-supported representative
area as its public label; this does not assert that every scene is there.
Unresolved routes retain the original public label. New protected assessments retain at most one primary location and its evidence.
Historical multi-location assessments remain readable.

## Geographic resolution

The model selects one explicit primary area or named venue, or abstains. It sees
the full original title/body, report number, verified section context and a small
list of source-backed catalog candidates, never the accepted summary or extracted
area. Its compact response contains only a decision and one candidate ID (or
null). Code copies the exact evidence and field from the selected candidate; the
model supplies no quotations, geographic types, or street-to-district guesses.
An unknown candidate ID is invalid output. `unresolved` is a completed ambiguous
assessment, including street-only reports with no supported area candidate. The deterministic catalog in
`internal/location` supplies geographic identity, public type, aliases, and
explicit parent relationships. It is separate from the translation spelling
dictionary and is versioned with the application. Updating its interpretation
requires a new catalog version. Existing successful assessments from another
catalog version are not effective overrides.

The catalog covers the 25 official Munich districts, the 29 county municipalities,
selected source-named localities, and a small number of supported venues/natural
areas. Each entry carries geographic-source references. Unknown entities are
left unresolved; the model cannot create catalog entries. A locality such as
Schwabing does not establish one official district. Source population-place
codes and dictionary name collisions are not administrative evidence.

Explicit vehicle evasion over multiple named streets is conservatively unresolved
even when the model selects a heading area: street containment is not established.
Ordinary collisions with approach streets and generic flight are not sufficient
for this guard. Unknown catalog names remain unresolved; evidence is not fuzzily
matched or silently corrected.

Candidate matching uses whole names and reviewed aliases; longer compound
names take precedence. County references do not become city candidates, and an
unrecognised qualified locality is not reduced to its base spelling. Only
explicit catalog areas/venues or a generic venue with verified festival context
are offered. Genitive tent spellings are explicit aliases, not fuzzy matching.

A conservative source guard compares a recognised trailing heading area with
explicit body scene-area phrases. Incompatible areas yield an ambiguous result
and a protected source-conflict flag, even if the model selects one of them.
Catalog parent/child relationships remain compatible. Residence and earlier-event
markers are excluded from this guard. Pattern coverage is intentionally limited;
this is not a complete parser of every possible geographic contradiction.

Official street point features retain `sb_name` as `DistrictHint`. This field
never proves that an entire street lies in a district and is not an automatic
resolution rule. Street/address geometry and broader venue coverage are deferred.
Festival context allows generic festival-ground/tent references to resolve only
inside a captured Wiesn section. The parser recognises singular/plural Wiesn
headings in report-detail content and a narrowly recognised dedicated Wiesn
release title (including the dialect edition). Contents headings in mixed daily
releases do not establish context. Context resets at section/heading boundaries;
its versioned hash changes independently of the original title/body.

Oktoberfest/Wiesn aliases become venue candidates only with a trailing venue
heading or a supported locative phrase, excluding topic/safety/briefing phrases.
Section membership alone is never a scene. Caption-only attachments with no
narrative return `source_problem` through the normal decoder/validator without
an Ollama call; model provenance is `local:source-guard-v1` and the assessment
records `decision_origin=source_guard`. No positive keyword-only fast path is
used. Missing context does not justify reconstructing a district.

A selected parent area cannot replace a canonical child area when explicit
body scene wording supports that child. This conservative guard retains the
canonical fields, including their original type; direct supported child
selection may still correct the type. Residence and previous-scene mentions do
not qualify. Street phrases can supply explicit textual evidence for an area
named next to them, but never infer an absent area from street geography.

## Operation and evaluation

The processor key is `location_verification`, scope `default`, priority 10.
Lower priorities run first: location verification (10), category verification
(20), public-assistance verification (25), then translations (30). The same
registry order is used for admin model controls, verification sections, and
queue status. This orders eligible post-processing work; it does not interrupt
an already running request or bypass canonical presentation generation.

The admin dashboard provides a location model preference and a location-only
manual processing action. `/admin/verifications` has a dedicated Location
verification section; its default-scope drill-down provides per-request model
selection, rechecks, coverage counts, and protected assessment details.
Its active immutable prompt is `incident-location-verification-v4`; v1, v2 and v3 remain
retired prompts for history. Candidate generation is versioned separately as
`source-area-candidates-v2`, and the resolver/catalog is `munich-areas-v3`. Startup creates an
unconfigured model preference; no location inference runs until an operator
selects an installed model. It uses the usual automatic controls and manual
recheck actions. Automatic discovery respects the scope's initial presentation-
completion cutover; existing presentations are not automatically backfilled.

Before selecting a production model, evaluate it on an isolated backup copy:

```sh
MUNICHBRIEF_OLLAMA_BASE_URL=http://localhost:11434 munichbrief location-evaluate \
  --snapshot /private/path/evaluation-copy.db \
  --model INSTALLED_MODEL \
  --ids 5564,5570,5242,3486,2951,1493,5243 \
  --output /private/path/new-results.jsonl
```

This command **makes LLM requests**. Run it only for an explicitly authorized
evaluation. Omit `--ids` only for a deliberate
full evaluation. Empty selections and requested IDs without a current eligible
presentation are rejected before any model request. The snapshot may receive
schema migrations; use a copy, never the live database. The command does not queue or apply corrections. Output is
created exclusively with mode 0600 and contains protected evidence. Historical
snapshots without section context cannot evaluate context-based venue recovery
until context has been prepared on the isolated copy.

Review every proposed correction against original sources. Cover the audit's
87 under-specific areas, wrong-place and type errors, related/recovery scenes,
multiple areas, withheld records, source defects, and ambiguous streets. Record
recovery and abstention rates separately; synthetic tests alone do not establish
model precision. Keep automatic production use unconfigured until that review.

Admin shows original/effective locations plus the latest protected assessment,
proposed location, evidence, geographic sources, roles, and wording conflicts.
Confirmed/corrected, unresolved, source-problem, and wording-conflict counts
supplement the existing queue/history metrics. Unresolved, source-problem, and
wording-conflict counts track the latest completed assessment and remain visible
during pending or failed rechecks. Disabling the processor stops future
automatic work without removing accepted results.

## Targeted source repair

The parser recognizes bold numbered paragraph headings in the detail stream,
excluding the contents list. Section context is stored separately and is not
injected into canonical extraction. Numbered incidents retain identity by report
number **within a source document**; numbers are not globally unique. Reordering
or inserting a report cannot repurpose an existing incident URL. Ambiguous
identities are rejected transactionally.

Release section labels immediately before a numbered report in the same HTML
section are excluded from the preceding incident's body. Internal subheadings,
witness appeals, multi-case labels, and headings with intervening source content
are preserved. Removing a misplaced section label is a source-body change, not
a context-only update. If that label previously triggered privacy minimization,
repair the source and reprocess canonically; do not weaken the privacy minimizer.

Preview a repair using retained official HTML and the existing document ID:

```sh
munichbrief source-repair --document-id DOCUMENT_ID --html /private/source.html
```

The preview lists report numbers, existing IDs, insertions, updates, and removals.
Context-only additions can be reported as updates without changing the canonical
content hash. To commit an inspected repair, repeat with `--apply` and
`--expected-source-hash CURRENT_DOCUMENT_HASH`; a concurrent source change aborts
it. The command retains a protected parser snapshot. Normal ingestion records
per-report outcomes using the same identity matching.

For the audited release, the required mapping is: incident 5243 remains report
1481, report 1482 is inserted, and incident 5244 remains report 1483. Validate
this mapping on a backup copy before production repair. A changed source body
requires normal canonical reprocessing before its new version can publish;
location verification does not bypass existing source-version safety checks.
Production application and subsequent generation are separate from opening the
implementation PR. There is no automatic archive repair or verification backfill.
