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
retry loops. A summary contradiction can accompany a clear area correction;
it appears in Attention without regenerating German text or translations.
Multiple independent scenes and unresolved routes retain the original public
label. The protected assessment retains the individual locations and roles.

## Geographic resolution

The model identifies source mentions and roles. The deterministic catalog in
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

Official street point features retain `sb_name` as `DistrictHint`. This field
never proves that an entire street lies in a district and is not an automatic
resolution rule. Street/address geometry and broader venue coverage are deferred.
Festival context allows generic festival-ground/tent references to resolve only
inside a captured Wiesn section. Named places elsewhere are not inferred from
visitors' residences or the investigating authority.

## Operation and evaluation

The processor key is `location_verification`, scope `default`, priority 25.
Its immutable prompt is `incident-location-verification-v1`. Startup creates an
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

This command **makes LLM requests**. It is explicitly deferred for the current
implementation review at the user's request. Omit `--ids` only for a deliberate
full evaluation. The snapshot may receive schema migrations; use a copy, never
the live database. The command does not queue or apply corrections. Output is
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
supplement the existing queue/history metrics. Disabling the
processor stops future automatic work without removing accepted results.

## Targeted source repair

The parser recognizes bold numbered paragraph headings in the detail stream,
excluding the contents list. Section context is stored separately and is not
injected into canonical extraction. Numbered incidents retain identity by report
number **within a source document**; numbers are not globally unique. Reordering
or inserting a report cannot repurpose an existing incident URL. Ambiguous
identities are rejected transactionally.

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
