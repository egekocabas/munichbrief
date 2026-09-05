# Pre-merge translation testing

This document is the release gate for the reader-language pull request. The
application can preserve structure and place names deterministically, but those
checks cannot approve meaning, grammar, legal framing, or publication quality.
Do not merge the language change until every item below is recorded in the pull
request and the native-review items are complete.

## Freeze the candidate routes

For every translated reader language, record:

- the exact Ollama model name and resolved digest;
- the adapter (`structured`, `translategemma`, or `hy-mt2`), prompt version,
  context size, and generation settings;
- whether the adapter officially supports the source and target language;
- the model artifact, parameter size, and quantization;
- the reviewer and review date.

Configure these routes on the protected `/admin/translations` page only after
the candidate passes evaluation. A route change affects new jobs only: queued
jobs retain their frozen model, adapter, and prompt. Mechanical scores alone
must not select a production default.

## Deterministic checks

Run the complete non-networked validation suite in
[Development](development.md), including race tests, documentation checks,
frontend regeneration, Helm rendering, and application builds. In addition,
verify:

- registry order, language negotiation, cookies, dates, plural categories,
  catalogs, metadata, hreflang, sitemap, Markdown routes, and social cards;
- NFC normalization and representative Latin, Han, Devanagari, Greek, and
  Cyrillic characters;
- adapter request contracts, separate title and summary calls, exact model and
  adapter persistence, immutable queued-job routing, and audit provenance;
- exact typed-placeholder occurrence counts, field ownership, restoration,
  number preservation, plain-text output, target script, and title/summary
  limits;
- Gazetteer source bounds, source contracts, generation activation and
  fallback, matcher construction, overrides, and the protected admin status
  page;
- the translations admin page at narrow and desktop widths, including a
  missing model, unsupported adapter, active job, failed replacement, and
  retained successful publication.

## Resumable live matrix

Live tests are opt-in and must use a disposable database and checkpoint
directory. Use three privacy-minimised current MunichBrief German summaries and
three artificial edge cases. The six fixtures together must cover:

- transit, streets, districts, neighbourhoods, municipalities, landmarks,
  repeated names, and target-language word-order changes;
- dates, exact times, telephone numbers, speeds, measurements, and causality;
- attribution, uncertainty, negation, allegations, examination/questioning,
  public-assistance wording, and the presumption of innocence;
- short and near-limit titles, multi-sentence summaries, Unicode punctuation,
  and all target scripts.

Send title and summary as separate sequential calls through the same
production provider, Gazetteer protection, adapter, restoration, validation,
and persistence path used by the worker. Checkpoint the manifest before the
first request and append one durable event after every field call. Resume only
when the model digest, prompt, settings, fixture hashes, language set, and
adapter still match. A transport interruption is not a model failure.

Do not commit downloaded police text, raw model output, checkpoints, test
databases, or generated translations. It is acceptable to commit anonymized
fixture structure and aggregated findings.

## Review and report

Publish one table with a row per language and separate columns for structural
passes, semantic passes, fluent/native review, open failures, model, adapter,
and digest. Inspect every output, including outputs that passed mechanically.
A fluent reviewer must approve:

- natural grammar and idiom;
- exact facts, actors, relationships, dates, times, and numbers;
- neutral police terminology, attribution, uncertainty, allegations, and the
  presumption of innocence;
- AI disclosure, privacy, source attribution, navigation, and metadata copy.

Any failed or pending native review keeps that language route unapproved. The
application may retain an unconfigured language without blocking canonical
German processing; automatic translation for that language remains paused.

## Operational release checks

Before merge, confirm the public ingress repository already permits every new
language prefix. Before deployment, back up the incident database, confirm the
Gazetteer has a healthy active generation, confirm every approved model is
installed, and recheck the per-language routes in `/admin/translations`. On an
upgrade from the shared translation setting, deploy with automatic processing
disabled, review or replace the inherited English `structured` route, configure
each newly registered language, and enable the worker only after all intended
routes report ready.
Registration creates automatic work only for future presentations and does not
backfill history. Queue unpublished historical translations explicitly after
review, then monitor per-language coverage, failures, queue age, model capacity,
and Gazetteer refresh health.
