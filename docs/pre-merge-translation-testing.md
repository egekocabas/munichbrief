# Translation readiness: HY-MT2 first, then remaining languages

Agreed 2026-09-08 for PR #55. This roadmap supersedes earlier future-work
recommendations in [Translation model evaluation](translation-model-evaluation.md)
and PR comments. Preserve their evidence, harnesses, and checkpoints.

The frozen Q5_K_M overview, exact resume point, operating procedure, and the
separate Q6_K experiment are preserved in
[HY-MT2 quantization comparison](hy-mt2-quantization-comparison.md).

## Acceptance and stopping points

Aim for accurate, readable translations. Minor grammar/style issues are allowed;
changed facts, missing negation, misleading legal meaning, or unreadable output
are not. Mechanical acceptance does not establish meaning. Inspect every output,
record assistant assessment separately from fluent/native review, and seek
targeted help for uncertain consequential wording.

Complete a decision for every language; failed languages may remain unconfigured
and paused when the PR merges. Stop after the initial four-call checkpoint and
after every bounded batch and every language to discuss results before advancing.
Merge and deployment remain separate decisions.

### Bounded-batch evidence protocol

Treat A-C screening, D-F expansion, and A/B repetition as separate bounded
batches. After each batch:

1. Inspect every output and its persisted translation result.
2. Keep the per-call input, raw response, restored output, timing, exact request
   identity, checks, and attempt status in the ignored durable evaluation record.
3. Add the assistant's semantic/readability assessment to the separate review
   record, without overwriting earlier attempts or claiming native review.
4. Update this tracked roadmap with aggregate results and the next decision,
   commit and push it, and update PR #55 with a concise evidence table.
5. Stop for discussion before starting the next batch.

Raw responses, databases, and downloaded real fixtures stay uncommitted. The PR
records reproducible aggregate evidence, model/adapter/digest, structural and
meaning findings, timings, limitations, and the agreed next action.

## Step 1: prepare the production test path

- [x] Production Gazetteer protection uses typed placeholders; explicit opaque
  mode remains for historical experiments.
- [x] Keep HY-MT2 prompt/settings unchanged for the baseline. Effective context:
  HY-MT2 8192; TranslateGemma 2048. Record actual requests and model digests.
- [x] Exercise the protected provider and queued worker in disposable incident
  databases, including restoration, validation, persistence, and route provenance.
- [x] Use test-only recording/replay HTTP transport to capture responses before
  validation and resume completed requests without regenerating them.
- [x] Pin rendered request hashes, model digest, fixtures, frozen Gazetteer
  generation/content, code identity, and revision. A changed prompt must invalidate
  reuse even when the human-readable version name is unchanged.
- [x] Select language, fixture, repetition, and maximum new calls independently.
- [x] Test typed production requests, separate native fields, interrupted-summary
  resume, identity mismatch rejection, rejected-output capture, and persisted
  successful/failed jobs. Never weaken validators to improve scores.
- [x] Correct the PR description: per-language routes, typed protection,
  plain-text model output, and incident migration 016.

Native adapters translate title and summary separately and sequentially.
Romanian's existing Qwen structured adapter returns both in one request; test
that production behavior without another adapter.

## Step 2: freeze six incidents

Freeze the entire pack before tuning. Store downloaded text, model outputs,
databases, and checkpoints under ignored `.local/translation-evaluations/`.
Commit only synthetic fixtures, aggregate findings, and reproducibility guidance.

| Fixture | Coverage |
| --- | --- |
| A | Current plain-text transit/allegation summary plus title: street, stations, S-Bahnen/U-Bahn, attribution, uncertainty, medical examination, date/time/duration, witness request |
| B | Allegation versus established fact, unresolved involvement, no conviction, presumption of innocence, repeated names |
| C | Exact versus approximate time, date, duration, 110, speed/measurement, explicit medical examination versus police questioning, negation, causality |
| D-F | Three frozen public German MunichBrief incidents across different domains, ordinary/longer prose and varied entities |

Record a source-fact checklist: who did what, where, when, uncertainty, and
relationships. Do not demand one interpretation of ambiguous German. Preflight
plain text, field limits, and Gazetteer matches. Fix genuine source/parser/override
gaps separately from prompting. No sentence segmentation, generated Markdown,
old exhaustive spelling matrix, or unrelated model comparison.

## Step 3: HY-MT2

Model: `hf.co/mradermacher/Hy-MT2-7B-GGUF:Q5_K_M`; adapter: `hy-mt2`.

1. Translate A's title and summary into English, then Ukrainian: **four calls**.
   Inspect outputs and persisted results, report, and stop. Shared integration
   failure blocks expansion; Ukrainian-only quality failure gets focused work.
2. Complete languages in order: **English, Spanish, French, Italian, Polish,
   Turkish, Ukrainian, Chinese, Hindi, Russian**.
3. Per language, inspect A-C first. If materially accurate, run D-F, then repeat
   A and B once. Apply the bounded-batch evidence protocol and stop for discussion
   after each of those batches before advancing.

Normal budget: eight pairs / 16 native calls per language, 160 calls across ten.
Identical initial calls count toward the pack. Run across separate sessions.
Historical timing ranged from about one minute per field to three minutes per
summary; estimate the next batch using current measured calls.

Classify infrastructure, deterministic-validation, meaning, and readability
failures separately. Fix proven validation bugs with regression tests; revalidate
saved responses offline first. For the remaining-language session authorized on
2026-09-09, allow at most six focused prompt-revision rounds per failing
language. Test failures first, then complete the pack under the revised prompt.
Preserve every attempt; do not retry until a lucky pass. Prefer short, general
language-specific terminology/context guidance for local issues.
Shared prompt changes require affected languages to be reassessed. Pause
persistent material failures and continue only after discussing the result.

## Step 4: remaining languages

Start after all ten HY-MT2 targets have an evaluation decision.

| Languages | Starting candidate | Adapter |
| --- | --- | --- |
| Croatian, Bosnian, Greek | `hf.co/mradermacher/translategemma-12b-it-GGUF:Q3_K_S` | `translategemma` |
| Romanian | `hf.co/bartowski/Qwen_Qwen3.5-9B-GGUF:Q3_K_M` | `structured` |

Verify installed artifact, language-code support, template, and available memory.
First run Croatian A's two fields as a loading/translation gate. Record loading
time, request duration, completion, and memory evidence. The Q3_K_S artifact is
about 5.6 GB; runtime fit on the Pi is unproven. Prior IQ3 evidence does not
qualify Q3_K_S. Stop and report loading failure or repeated Ollama restarts.

Use the same eight-pair protocol, one language at a time. Romanian needs eight
structured requests; distinguish requests from field counts. Seed-X remains
excluded after its placeholder failure. Further fallback models need a separate
decision after reporting the selected candidate.

## Step 5: decisions and release evidence

- **Acceptable on this sample:** final eight pairs pass structure, preserve
  material facts/legal meaning, and are understandable; minor style/grammar
  issues allowed. This small sample is not a quality guarantee.
- **Needs focused work:** an identified next test within the two-round limit.
- **Paused:** unresolved material errors, unreadability, uncertain critical
  meaning, or hardware failure. Leave the route unconfigured.

After every call durably save input, raw response, restored output when available,
timing, identity, checks, and attempt status. Keep manual review in a separate
record never overwritten by resume. Transport interruptions are not model scores.
Update the table and PR after each bounded batch and each language before
continuing. Exact model names above plus resolved digests belong in the run
manifest and result report.

| Language | Candidate | Structural | Meaning/readability | Repeats | Decision / open issue | Next action |
| --- | --- | --- | --- | --- | --- | --- |
| English | HY-MT2 | Final protocol: 8/8 pairs | Accurate/readable on assistant review; minor A/B wording caveats | Revised A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Spanish A-C |
| Spanish | HY-MT2 | A-F: 6/6; focused B/E: 2/2 structural | 5/6 acceptable; revised E fixes inpatient care but changes bus into a route | Not run | Paused after two focused rounds; no native approval | Discuss result before French; no repetitions |
| French | HY-MT2 | Historical protocol: 8/8; final-prompt B/2 structural pass | 7/8 acceptable; final B fixes legal framing but invents windshield specificity | A/2 pass; B/2 meaning failure | Paused after two focused rounds; no native approval | Discuss result before Italian |
| Italian | HY-MT2 | Historical A-F: 6/6; third-guidance E/F: 2/2 structural | Revised E passes; revised F summary passes but title remains grammatically invalid; final prompt not run across full protocol | Not run | Paused after explicit third focused round; no native approval | Discuss result before Polish; no repetitions |
| Polish | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable; recurring grammar/spelling defects recorded | Revised A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Turkish A-C |
| Turkish | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable under the user-selected time threshold; recurring precision/grammar caveats recorded | Revised A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Ukrainian |
| Ukrainian | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable after two explicit user-approved extra rounds; minor grammar/style caveats recorded | Final A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Chinese A-C |
| Chinese | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable after three focused rounds; recurring awkward A wording recorded | Final A/B: 2/2 pass | Acceptable on this sample; no native approval | Continue autonomously with Hindi A-C |
| Hindi | HY-MT2 | Not run | Pending | Pending | Historical legal/role errors | After Chinese |
| Russian | HY-MT2 | Not run | Pending | Pending | Historical legal/role errors | After Hindi |
| Croatian | TranslateGemma Q3_K_S | Not run | Pending | Pending | Runtime fit unknown | After HY-MT2 decisions |
| Bosnian | TranslateGemma Q3_K_S | Not run | Pending | Pending | Runtime fit/quality unknown | After Croatian |
| Greek | TranslateGemma Q3_K_S | Not run | Pending | Pending | Runtime fit/quality unknown | After Bosnian |
| Romanian | Qwen 9B | Not run | Pending | Pending | Older baseline only | After Greek |

Before merge: review reader catalogs (especially disclosure, attribution, legal
copy), run the full non-Docker [development suite](development.md), verify admin,
upgrade migration, retained publications, and paused-language behavior using
disposable data/recorded responses. Confirm `homelab-infra` permits all public
prefixes. Update the PR with actual qualified routes, paused languages, evidence,
and limitations.

During later rollout configure only approved routes, explicitly review inherited
English settings, verify Gazetteer/model readiness, preserve future-only cutovers,
and keep historical backfill an operator action. No Docker; no automatic merge
or deployment.

## Four-call checkpoint: 2026-09-08

Completed exactly four HY-MT2 calls (English A title/summary, then Ukrainian A
title/summary) in **7m43s**, with no transport failure or generation retry.
Both pairs passed placeholder counts, numbers, plain text, target script,
restoration, and final production validation, and were persisted with the
expected model/adapter/prompt provenance in isolated databases.
Replaying those actual responses through the worker used zero new generation
calls and preserved the separate manual review records.

Model digest:
`24acc0f002f8c874f34e8b3e22da236405f7c3a47ae9d62d3744cf3c5b4bd693`.
Code-content identity:
`80038b4cabbc2a586f6bb858ee38c139e45271d6cec211036bb568e2693ac6ff`.
The initial test invocation omitted explicit VCS stamping; its manifest records
revision as unknown. The exact source-content hash was captured before inference,
and separate local provenance records parent revision `95965a2` with worktree
changes. Documented future commands use `-buildvcs=true`.

English retained the principal facts, allegation, negation, and transit roles.
Minor caveats: plural agreement, medical examination broadened to medical
attention, and contact wording broadened from the responsible police station
to relevant authorities. This is promising under the agreed readability bar,
not full-language approval.

Ukrainian retained names, numeric facts, medical examination, and transit roles.
Its phrasing of the alleged injury is ambiguous between reported allegation
and expected/obligatory action. That consequential uncertainty needs focused
wording review; no native approval or full semantic pass is claimed.

The frozen real Gazetteer has 12,136 names. Its station labels are generic
TRANSIT, unlike the older handcrafted TRAIN_STATION fixture. Garching-Hochbrück
also resolves to TRANSIT; review that contextual ambiguity before fixture E.
Real fixtures D-F use public incidents 1021, 1023, and 1024. Incident 1022 was
excluded for contradictory German source wording, not a translation failure.

Raw requests, outputs, source checklists, isolated databases, and separate manual
review records remain in `.local/translation-evaluations/readiness-v1/`.

## English B/C bounded batch: 2026-09-08

Completed exactly four sequential HY-MT2 calls for English B and C (separate
title and summary) in **4m33s**, with no retry or transport failure. Both pairs
passed typed-placeholder, numeric, plain-text, restoration, final validation,
and queued-worker persistence checks using adapter `hy-mt2` and prompt
`incident-translation-en-v3`.

| Fixture | Structural | Meaning/readability | Caveat |
| --- | --- | --- | --- |
| B: legal meaning | Pass | Acceptable for expansion | “Final judgment” is broader than “final conviction,” but allegation, uncertain involvement, no conviction, presumption of innocence, and witness request remain intact |
| C: precise facts and roles | Pass | Acceptable for expansion | No consequential issue found; exact/approximate times, numbers, causality, examination/questioning roles, injury negation, and 110 remain intact |

All place occurrences were restored, all results were persisted with the expected
model/adapter/prompt provenance, and the ignored durable records contain exact
requests, raw responses, timings, checks, and separate assistant review. This is
not native approval. English A-C now passes the agreed screening threshold;
D-F and the A/B repetition remain required for a full sample decision.

## English D-F bounded batch: 2026-09-08

Completed exactly six sequential HY-MT2 calls for real English fixtures D-F in
**3m51s**, with no retry or transport failure. Zero-call replay reused all six
saved responses. The initial mechanical result was 2/3 pairs: D converted
`20:00` to equivalent `8 p.m.`, which the number validator incorrectly rejected.
A narrow validator correction now recognizes equivalent 24-hour/12-hour clock
expressions while rejecting wrong hours, wrong meridiem, and added times. The
exact recorded D conversion passes offline after the fix; no model response was
regenerated.

| Fixture | Structural | Meaning/readability | Material issue |
| --- | --- | --- | --- |
| D: dispute/threat | Pass after offline validator fix | Fail | Neutral `der Betroffene` became “the victim,” who was then described as arrested |
| E: bus/e-bike collision | Pass | Fail | `stationär` became “for treatment,” omitting inpatient admission |
| F: police evasion/collision | Pass | Fail | Police `Anhaltesignale` became road “stop signs,” changing the ignored signal and its relationship to the police control |

All three outputs are readable and preserve most facts, but the role assignment,
medical detail, and police-signal relationship are material under the agreed
accuracy standard. English therefore **needs focused work** and is not ready for
the A/B repetition. Discuss one short, general English guidance revision, then
test D-F first; preserve this initial attempt and do not retry for a lucky pass.

## English focused-guidance D-F batch: 2026-09-08

Commit `b050f7b` adds two sentences of English-only HY-MT2 guidance: keep neutral
German person labels neutral unless the source explicitly identifies a victim,
and preserve explicit inpatient-admission and police-stop-signal meaning. Other
languages retain the standard fallback prompt and the source-data boundary is
unchanged. The prompt version remains `incident-translation-en-v3` because it is
unmerged; its changed rendered request and code hashes force a new checkpoint
identity regardless of that human-readable version.

The new isolated run reused the identical frozen fixtures and Gazetteer, but no
old model response. It completed exactly six sequential D-F field calls in
**5m45s**, with no retry or transport failure. Zero-call replay then reused,
validated, and persisted all six responses.

| Fixture | Original meaning failure | Revised output | Decision |
| --- | --- | --- | --- |
| D | Neutral person became “the victim” | “The individual involved” | Pass |
| E | Inpatient admission omitted | “Admitted to hospital as an inpatient” | Pass |
| F | Police stop signals became road stop signs | “Ignored police stop signals” | Pass |

All three revised pairs pass structural and assistant meaning/readability review.
No material addition, omission, role change, factual reversal, or misleading
legal strengthening was found. The old failed attempt remains intact in its
original checkpoint directory. This is not native approval.

English A-F is now ready for the final A/B repetition under the revised prompt.
Because A/B were generated before the guidance change, the repetition must make
four new calls under the new identity rather than reusing those responses.

## English final A/B repetition and decision: 2026-09-08

Completed exactly four sequential revised-prompt calls for A/B repetition in
**5m12s**, with no retry or transport failure. Zero-call replay reused, validated,
and persisted all four responses.

| Fixture | Structural | Meaning/readability | Stability finding |
| --- | --- | --- | --- |
| A repeat | Pass | Pass with minor style caveat | Improved medical precision, S-Bahnen agreement, and police-contact wording; “lightly injured” remains understandable but less idiomatic than “slightly injured” |
| B repeat | Pass | Pass with minor legal-wording caveat | Identical to the earlier acceptable output; “final judgment” remains broader than “final conviction” without reversing the presumption-of-innocence safeguard |

Both pairs preserve typed placeholders, numbers, attribution, uncertainty,
negation, roles, causality, medical examination, and legal meaning. The focused
guidance did not regress A/B. It does not address any term in C, so C's existing
accepted result remains part of this bounded decision rather than adding calls
outside the agreed repetition batch; this is recorded as an evidence limitation.

Final assistant decision: **English is acceptable on this sample**. The eight
protocol positions (A-F plus A/B repetition) pass structural checks and the
accurate-and-readable threshold after the focused D-F correction. Minor wording
caveats remain. This is neither native approval nor a guarantee for unseen text.

## Spanish A-C bounded batch: 2026-09-08

Completed exactly six sequential HY-MT2 calls for Spanish A-C in **6m29s**, with
no retry or transport failure. The standard fallback prompt was used; English
guidance was absent.

Spanish A was initially rejected because the model-facing typed placeholders
made its title exceed 90 characters even though the restored reader title is 76
characters. Commit `c6b9dcc` defers only a protected field's length check until
Gazetteer restoration; UTF-8, controls, normalization, plain text, numbers,
script, and placeholder integrity are still checked before restoration, and the
ordinary 90/600 reader limits remain mandatory afterward. Tests cover the exact
Spanish title shape and reject an overlong restored title.

The source manifest and exact recordings were imported into a separate offline
revalidation directory. Request hashes matched, and zero-new-call replay under
the corrected code validated and persisted A-C without regenerating output.

| Fixture | Structural after fix | Meaning/readability | Decision |
| --- | --- | --- | --- |
| A: transit/allegation | Pass | Facts preserved; awkward `al S-Bahnen` title agreement and broader medical-attention wording | Pass with caveats |
| B: legal meaning | Pass | Generic vehicle `Scheibe` became `parabrisas` (windshield), adding unsupported specificity; `principio de inocencia` is less conventional than `presunción de inocencia` | Meaning failure |
| C: precise facts/roles | Pass | Facts and roles preserved; `interrogó` is somewhat stronger than neutral questioning | Pass with caveat |

Spanish is 3/3 structural after the deterministic fix and 2/3 on assistant
meaning/readability review. This is not native approval. Do not expand to D-F
yet. Discuss one short Spanish-specific guidance revision covering generic
vehicle glass/window, conventional presumption-of-innocence terminology, and
neutral police questioning; test B first and preserve this attempt.

## Spanish focused-guidance B batch: 2026-09-08

Commit `e84fe25` adds two Spanish-only HY-MT2 guidance sentences: keep generic
vehicle `Scheibe` generic unless the source explicitly identifies a windshield,
use conventional `presunción de inocencia`, and keep neutral police questioning
distinct from interrogation. English retains its separate guidance and languages
without a specialization retain the standard fallback prompt.

A fresh identity generated only B's title and summary: exactly two sequential
calls in **3m29s**, without retry or transport failure. Zero-call replay reused,
validated, and persisted both responses.

| Original issue | Revised output | Decision |
| --- | --- | --- |
| `Scheibe` became unsupported `parabrisas` | `cristal de un vehículo` | Fixed |
| Less conventional `principio de inocencia` | `presunción de inocencia` | Fixed |

The revised B preserves attribution, allegation, unresolved involvement, no
conviction, final judgment, witness request, street, and both district
occurrences. It passes structural and assistant meaning/readability review on
the first revised attempt. This is not native approval.

Spanish A-C is ready for D-F; A and C retain their recorded non-material grammar
and wording caveats. Preserve the original failed B attempt.

**Next proposed batch: Spanish D-F, six native calls, after discussion.**

## Spanish D-F expansion: 2026-09-08

Exactly six fresh sequential native field calls completed in **5m21s**, without
a retry or transport failure. All three pairs passed typed-placeholder checks,
restoration, final validation, and queued-worker persistence. A zero-new-call
replay then reused and persisted all six captured responses.

| Fixture | Structural | Assistant meaning/readability | Decision |
| --- | --- | --- | --- |
| D | Pass | Time, setting, dispute, threats, slight injury, uncertain danger, response, arrest, release, and continuing investigation preserved | Acceptable |
| E | Pass | `stationär in ein Krankenhaus gebracht` became `trasladada de forma permanente a un hospital`, incorrectly meaning taken permanently to hospital | Material failure |
| F | Pass | Evasion, signals, red lights, collision, arrest, and licence/substance indications preserved; awkward securing language and slightly strong wording recorded | Acceptable with caveats |

The failed E response remains durable evidence and must not be replaced by an
unchanged-prompt retry. Spanish is structurally 6/6 across A-F and 5/6 on
assistant meaning/readability review. This is not native approval.

Do not run repetitions yet. Discuss one short Spanish-specific clarification
that `stationär in ein Krankenhaus gebracht` means admitted or taken for
inpatient hospital care, never permanently. If agreed, update the prompt and
generate only E's two fields under a fresh identity before expanding again.

**Next proposed step: discuss focused Spanish medical guidance; make no further
live calls until agreed.**

## Spanish focused medical-guidance E batch: 2026-09-08

Commit `13579b6` adds a Spanish-only clarification that the German medical phrase
`stationär in ein Krankenhaus gebracht` means hospital admission/inpatient care,
never a permanent transfer. Prompt-isolation tests, the full Go suite, and
`go vet ./...` passed before inference.

A fresh identity generated only E's title and summary: exactly two sequential
calls in **1m17s**, without retry or transport failure. A zero-new-call replay
reused, validated, and persisted both captured responses.

| Evidence | Previous E | Focused E | Decision |
| --- | --- | --- | --- |
| Inpatient care | `trasladada de forma permanente a un hospital` | `fue ingresada en un hospital` | Fixed |
| Collision participant | `un autobús urbano` | `una ruta urbana` | New material failure |

The revised output corrects the targeted medical error, preserves the serious
injury, cyclist, place, investigation, and absence of blame, but mistranslates
`Linienbus` as an urban route rather than a scheduled bus. That removes a central
participant and makes the collision semantically incoherent. Preserve both E
attempts; do not retry the unchanged prompt. This is assistant review only.

Spanish remains 6/6 structural and 5/6 acceptable on meaning/readability across
A-F. It has used the roadmap's two focused prompt rounds (B and E), so mark it
**paused** and do not run repetitions. Its route should remain unconfigured for
rollout unless a later, separately agreed evaluation qualifies it.

**Next proposed step: discuss the paused Spanish decision before beginning
French A-C. No further live calls until agreed.**

## French A-C screening: 2026-09-08

The standard HY-MT2 fallback prompt generated exactly six sequential native
field calls in **6m21s**, without retry or transport failure. All three pairs
passed typed-placeholder checks, restoration, final validation, and queued-worker
persistence. A zero-new-call replay reused and persisted all six responses.

| Fixture | Structural | Assistant meaning/readability | Decision |
| --- | --- | --- | --- |
| A | Pass | Allegation, medical examination, timing, delay causality, unaffected U-Bahn, and places preserved; awkward entity articles and stronger-sounding witness instruction | Acceptable with caveats |
| B | Pass | Allegation, generic glass, unresolved involvement, no conviction, innocence safeguard, and repeated place preserved; nonstandard innocence term and awkward place grammar | Acceptable with caveats |
| C | Pass | All numbers, timing distinctions, speed, closure causality, medical examination, no injuries, witness, and 110 preserved; minor agreement/register and redundant street wording | Acceptable with caveats |

No material factual or legal reversal was found. Do not add focused guidance
from these stylistic caveats before seeing the real-incident expansion. Native
review should specifically check `principe de l’innocence`, the witness-request
wording, and grammar around immutable German place/transit names.

French A-C is **3/3 acceptable on assistant review** and ready for D-F. This is
not fluent/native approval.

**Next proposed batch: French D-F, six native calls, after discussion.**

## French D-F expansion: 2026-09-08

Exactly six fresh sequential native field calls completed in **5m22s**, without
retry or transport failure. E and F passed production structure immediately. D
was initially rejected because its accurate French `20 h` rendering did not
retain the source's literal `:00` digits.

Commit `db8c7ee` fixes that deterministic false positive by treating 24-hour
`h`/`heure(s)` notation as clock-equivalent. Regression tests accept equivalent
hours—with and without minutes—and continue rejecting changed hours, changed
minutes, and added times. The full Go suite and `go vet ./...` pass. The exact
saved D-F responses were imported under the corrected code identity and replayed
with zero new calls; all three then passed restoration, final validation, and
persistence. Preserve the original D rejection as evidence.

| Fixture | Structural after correction | Assistant meaning/readability | Decision |
| --- | --- | --- | --- |
| D | Pass | Time, setting, dispute, threats, slight injury, uncertain danger, response, arrest, release, and investigation preserved | Acceptable |
| E | Pass | Bus/e-bike collision, injury, inpatient care, and investigation preserved, but `Münchner Verkehrspolizei` becomes public-transport police rather than traffic/road police | Material failure |
| F | Pass | Evasion, police control, signals, red lights, collision, arrest, and licence/substance indications preserved; wording caveats recorded | Acceptable with caveats |

French is structurally 6/6 and 5/6 acceptable on assistant meaning/readability
review across A-F. The E agency-attribution error is material; preserve it and do
not retry under the unchanged prompt. This is not native approval.

Do not run repetitions yet. Discuss one short French-specific clarification that
`Verkehrspolizei` means traffic/road police (`police de la circulation` or
`police routière`), not public-transport police, together with natural inpatient
wording. If agreed, generate only E's two fields under a fresh identity.

**Next proposed step: discuss focused French guidance; make no further live calls
until agreed.**

## French focused-guidance E batch: 2026-09-08

Commit `13dd63b` adds general French police/medical terminology without referring
to fixture E or its people, place, or vehicles. It defines `Verkehrspolizei` as
traffic/road police rather than public-transport police, and `stationär in ein
Krankenhaus gebracht` as inpatient admission rather than permanent transfer.
Prompt-isolation tests, the full Go suite, and `go vet ./...` passed first.

A fresh identity generated only E's title and summary: exactly two sequential
calls in **3m09s**, without retry or transport failure. About 66 seconds of the
title call was model loading. A zero-new-call replay reused, validated, and
persisted both responses.

| Evidence | Previous E | Focused E | Decision |
| --- | --- | --- | --- |
| Responsible unit | `police des transports de Munich` | `police de la circulation de Munich` | Fixed |
| Inpatient care | `transportée en hospitalisation complète` | `admise à l’hôpital pour y recevoir des soins en hospitalisation` | Meaning clear; style remains awkward |
| Scheduled bus | `bus de ligne` | `bus de ligne` | Preserved |

The first focused round fixes the material agency-attribution error without a
replacement factual error. Serious injury, cyclist, inpatient admission, place,
continuing investigation, and absence of blame remain intact. Capitalization of
`Vélo Électrique` and redundant hospital wording are stylistic caveats. Preserve
the original failed E. This is assistant review only.

French A-F is now **6/6 acceptable on assistant review**. Do not declare the
language qualified yet: run the planned A/B repetition under the revised French
prompt to check transit, allegation, legal meaning, placeholders, and stability.

**Next proposed batch: French A/B repetition 2, four native calls, after
discussion.**

## French A/B repetition: 2026-09-08

The revised French prompt generated exactly four fresh sequential native field
calls in **4m14s**, without retry or transport failure. Both repeated pairs
passed typed-placeholder checks, restoration, final validation, and queued-worker
persistence. A zero-new-call replay reused and persisted all four responses.

| Fixture | Structural | Assistant meaning/readability | Decision |
| --- | --- | --- | --- |
| A/2 | Pass | All material facts preserved; the known entity-grammar and stronger witness-instruction caveats recur | Acceptable with caveats |
| B/2 | Pass | Incident facts, allegation, uncertainty, and no-conviction statement preserved, but innocence ends at any final decision rather than only a final conviction | Material legal-framing failure |

The B/2 source says `bis zu einer rechtskräftigen Verurteilung`; the output says
`jusqu’à ce qu’une décision définitive soit rendue`. A final decision may be an
acquittal, so this broadens the condition ending the presumption of innocence.
`principe de l’innocence` is also less conventional than `présomption
d’innocence`. Place-name grammar remains awkward but is not the failure.

Preserve B/2 and do not retry under the unchanged prompt. French completes the
eight-pair protocol at **8/8 structural and 7/8 acceptable on assistant review**,
but is not qualified. One of the roadmap's two focused rounds remains.

Discuss short, general French legal guidance: translate `Unschuldsvermutung` as
`présomption d’innocence`, and retain that it applies until a final conviction
(`condamnation définitive`), not merely any final decision or judgment. If
agreed, test B first under a fresh identity. This is not native approval.

**Next proposed step: discuss the second focused French guidance round; make no
further live calls until agreed.**

## French final legal-guidance B gate: 2026-09-08

Commit `477d30e` adds the second and final French-focused rule. It generally maps
`Unschuldsvermutung` to `présomption d’innocence` and preserves that the safeguard
lasts until a final conviction (`condamnation définitive`), not merely any final
decision or judgment. Prompt-isolation tests, the full Go suite, and
`go vet ./...` passed before inference.

A fresh identity generated B/2's title and summary first: exactly two sequential
calls in **2m01s**, without retry or transport failure. The pair passed typed
placeholder checks, restoration, final validation, and queued-worker persistence.
A zero-new-call replay reused and persisted both responses.

| Evidence | Previous B/2 | Final focused B/2 | Decision |
| --- | --- | --- | --- |
| Innocence term | `principe de l’innocence` | `présomption d’innocence` | Fixed |
| End condition | Any `décision définitive` | `condamnation définitive` | Fixed |
| Generic vehicle glass | `vitre` | `pare-brise` | New material failure |

The legal rule fixes the consequential legal error. The same output, however,
narrows generic `Scheibe` to a windshield without source support. This invents a
factual detail and fails the B gate. Preserve the attempt and do not retry the
unchanged prompt.

Per the user's explicit condition, the other seven final-prompt pairs were not
generated because B did not pass. French remains **7/8 acceptable on the prior
full protocol** and is **paused after two focused rounds**. Its route should
remain unconfigured unless a later, separately agreed model evaluation qualifies
it. This is assistant review only; no native approval.

**Next proposed step: discuss the paused French decision before beginning Italian
A-C. No further live calls until agreed.**

## Italian A-C screening: 2026-09-08

The standard HY-MT2 fallback prompt generated exactly six sequential native
field calls in **7m01s**, without retry or transport failure. All three pairs
passed typed-placeholder checks, restoration, final validation, and queued-worker
persistence. A zero-new-call replay reused and persisted all six responses.

| Fixture | Structural | Assistant meaning/readability | Decision |
| --- | --- | --- | --- |
| A | Pass | Attribution, allegation, injury, medical checks, timing, delay causality, unaffected U-Bahn, and places preserved; witness and protected-name grammar caveats | Acceptable with caveats |
| B | Pass | Generic `Scheibe` becomes `parabrezza`, and final conviction becomes the broader `sentenza definitiva` | Material failure |
| C | Pass | All numbers, timing distinctions, speed, closure causality, medical examination, no injuries, witness, and 110 preserved; questioning-register caveat | Acceptable with caveat |

B retains police attribution, uncertainty, no-conviction wording, and the
innocence concept. It nevertheless adds unsupported windshield specificity and
changes `rechtskräftige Verurteilung` from a final conviction to any final
judgment. Preserve the output and do not retry under the unchanged prompt.

Italian A-C is **3/3 structural and 2/3 acceptable on assistant review**. Do not
expand to D-F yet. Discuss short, general Italian guidance: keep generic vehicle
`Scheibe` generic (`vetro` or `finestrino`) unless the source explicitly says
`Windschutzscheibe`; translate the legal safeguard conventionally as
`presunzione d’innocenza` until a final conviction (`condanna definitiva`), not
merely any final judgment. This is not native approval.

**Next proposed step: discuss the first focused Italian guidance round and test
B first under a fresh identity. No further live calls until agreed.**

## Italian focused-guidance B batch: 2026-09-08

Commit `f096454` adds a short general Italian rule without fixture-specific
people or places. It keeps generic vehicle `Scheibe` generic unless the source
explicitly identifies a windshield, uses conventional `presunzione d’innocenza`,
and distinguishes final conviction (`condanna definitiva`) from a generic final
judgment. Prompt-isolation tests, the full Go suite, and `go vet ./...` passed.

A fresh identity generated only B's title and summary: exactly two sequential
calls in **2m05s**, without retry or transport failure. The pair passed typed-
placeholder checks, restoration, final validation, and queued-worker persistence.
A zero-new-call replay reused and persisted both responses.

| Evidence | Initial B | Focused B | Decision |
| --- | --- | --- | --- |
| Generic vehicle glass | `parabrezza` | `vetro` | Fixed |
| Innocence term | `principio di presunzione di innocenza` | `presunzione d’innocenza` | Fixed |
| End condition | `sentenza definitiva` | `sentenza definitiva` | Material failure remains |

The first focused rule removes the unsupported windshield detail and improves
the innocence term. HY-MT2 nevertheless ignores the explicit final-conviction
distinction and again broadens `rechtskräftige Verurteilung` to any final
judgment. Preserve this attempt and do not retry the unchanged prompt.

Italian remains **3/3 structural and 2/3 acceptable on assistant review**. Do not
expand to D-F. One focused round remains. Discuss a shorter, stronger general
rule: render `bis zu einer rechtskräftigen Verurteilung` as `fino a una condanna
definitiva` (or `fino a una condanna passata in giudicato`), never `sentenza
definitiva`, because a final judgment may also be an acquittal. This is not native
approval.

**Next proposed step: discuss the second focused Italian guidance round and test
B first under a fresh identity. No further live calls until agreed.**

## Italian final legal-guidance B gate: 2026-09-08

Commit `7824724` makes the second and final Italian-focused rule shorter and more
direct. It defines `rechtskräftige Verurteilung` as `condanna definitiva` or
`condanna passata in giudicato`, explicitly rejecting `sentenza definitiva`
because a final judgment may be an acquittal. The generic-glass and conventional
innocence rules remain. Prompt-isolation tests, the full Go suite, and
`go vet ./...` passed before inference.

A fresh identity generated only B's title and summary: exactly two sequential
calls in **1m55s**, without retry or transport failure. The pair passed typed-
placeholder checks, restoration, final validation, and queued-worker persistence.
A zero-new-call replay reused and persisted both responses.

| Evidence | First focused B | Final focused B | Decision |
| --- | --- | --- | --- |
| Generic vehicle glass | `vetro` | `vetro` | Preserved |
| Innocence term | `presunzione d’innocenza` | `presunzione d’innocenza` | Preserved |
| End condition | `sentenza definitiva` | `condanna definitiva` | Fixed |

The final rule fixes the remaining legal condition without a replacement factual
error. `fino a quando non arriva` is colloquial rather than polished legal prose,
but it accurately keeps the presumption in force until a final conviction.
Preserve all earlier B attempts. This is assistant review only.

Italian A-C is now **3/3 acceptable on assistant review**. Both focused rounds
have been used. Run D-F under the final prompt next; any new material error will
pause Italian rather than trigger another prompt round.

**Next proposed batch: Italian D-F, six native calls, after discussion.**

## Italian D-F expansion: 2026-09-08

The final Italian prompt generated exactly six fresh sequential native field
calls for real fixtures D-F in **5m51s**, without retry or transport failure.
All three pairs passed typed-placeholder checks, restoration, final validation,
and queued-worker persistence. An exact replay reused and persisted all six
responses with `MUNICHBRIEF_READINESS_MAX_NEW_CALLS=0`.

| Fixture | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| D | Pass | Acceptable: facts, uncertain danger, arrest and release sequence, and continuing investigation are preserved |
| E | Pass | Material failure: ongoing `werden geführt` becomes completed `sono state condotte`, changing the investigation's status |
| F | Pass | Material/readability failure: `bei der Überprüfung` becomes the added police search `durante la perquisizione`; alcohol/drug indications are strengthened toward consumption; title `L’autista dell’auto fuga` is grammatically broken |

Italian is structurally **6/6** across A-F and **4/6 acceptable** on assistant
meaning/readability review. D adds no material issue. E changes an ongoing
investigation into a completed one. F adds a search where the source says a
check, strengthens evidentiary wording, and has an unpublishable title. Preserve
both failures without unchanged-prompt retries.

Both allowed focused prompt rounds were already used for fixture B. Italian is
therefore **paused**, and the planned A/B repetitions were not run. No fluent or
native approval is claimed.

**Next proposed step: discuss the paused Italian decision before beginning
Polish A-C. No further live calls until agreed.**

## Italian third-guidance E/F gate: 2026-09-08

The user explicitly approved one exception to the roadmap's two-round prompt
limit. Commit `36cf6c6` adds short, general Italian guidance to preserve ongoing
investigation status, distinguish a check from a search, retain evidentiary
uncertainty, and produce grammatically complete headlines. Prompt-isolation and
processing-package tests passed before inference.

Exactly four fresh sequential native calls completed for E and F in **5m47s**,
without retry or transport failure. Both pairs passed typed-placeholder checks,
restoration, final validation, and queued-worker persistence. An exact replay
reused and persisted all four responses with zero new calls.

| Fixture | Previous result | Third-guidance result | Decision |
| --- | --- | --- | --- |
| E | Ongoing investigation became completed | Present `sono condotte` retains ongoing status; `autobus di linea` accurately renders the scheduled bus | Pass |
| F | Check became search; evidence strengthened; broken title | Summary now uses `verifica` and retains uncertain signs, but title again uses noun `fuga` instead of finite verb `fugge` | Readability failure |

The third guidance fixes E and the material issues in F's summary, but F's title
remains grammatically invalid and is not publishable. Preserve this result
without an unchanged-prompt retry. Because the E/F gate required both pairs to
pass, the other six pairs in the eight-pair protocol were not generated. The
new prompt therefore has not completed the full protocol.

Italian remains **paused after the explicitly approved third focused round**.
No fluent or native approval is claimed.

**Next proposed step: discuss whether to keep Italian paused or evaluate a
different model before beginning Polish. No further live calls until agreed.**

## Polish A-C screening: 2026-09-08

The standard HY-MT2 fallback prompt generated exactly six fresh sequential
native field calls for Polish A-C in **7m26s**, without retry or transport
failure. All three pairs passed typed-placeholder checks, restoration, final
validation, and queued-worker persistence. An exact replay reused and persisted
all six responses with zero new calls.

| Fixture | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A | Pass | Acceptable: facts and uncertainty survive; `świadki` should be accusative `świadków`, and the transit wording is awkward but understandable |
| B | Pass | Material failure: `rechtskräftige Verurteilung` becomes `prawomocne orzeczenie` (final ruling), not `prawomocne skazanie` (final conviction); `no conviction` also becomes broader `no judgment` |
| C | Pass | Acceptable: exact facts, roles, negation, medical examination, police questioning, and causality are preserved |

Polish is **3/3 structural and 2/3 acceptable** on assistant review. Preserve B
without an unchanged-prompt retry. The model correctly retains allegation and
unresolved involvement, and uses `domniemanie niewinności`, but changes the
legal endpoint: a final ruling can be an acquittal, whereas the source requires
a final conviction.

Do not expand to D-F yet. Discuss one short, general Polish rule distinguishing
`Verurteilung` (`skazanie`) and `rechtskräftige Verurteilung` (`prawomocne
skazanie`) from the broader `wyrok` or `orzeczenie`. No fluent or native approval
is claimed.

**Next proposed step: discuss the first focused Polish B rerun. No further live
calls until agreed.**

## Polish focused-guidance B gate: 2026-09-08

Commit `6fcc522` adds one short Polish-specific rule: `Verurteilung` means
`skazanie`, and `rechtskräftige Verurteilung` means `prawomocne skazanie`, not
the broader `wyrok` or `orzeczenie`, because a judgment or ruling may be an
acquittal. Prompt-isolation and processing-package tests passed before
inference.

A fresh request identity generated exactly two sequential native calls in
**2m08s**, without retry or transport failure. The pair passed typed-placeholder
checks, restoration, final validation, and queued-worker persistence. A
zero-new-call replay reused and persisted both responses.

| Evidence | Initial B | Focused B | Decision |
| --- | --- | --- | --- |
| No conviction | `nie ma żadnego wyroku` | `nie ma żadnego skazania` | Fixed |
| Final-conviction threshold | `prawomocne orzeczenie` | `prawomocne skazanie` | Fixed |
| Presumption of innocence | Preserved | Preserved | Pass |

`Do chwili wydania prawomocnego skazania` is stylistically awkward; `do czasu
prawomocnego skazania` would be more natural, but the generated wording is
accurate and understandable under this roadmap's threshold. No replacement
material error was found. Preserve the initial and focused attempts. This is
assistant review only.

Polish A-C is now **3/3 acceptable on assistant review** and ready for D-F. One
focused prompt round has been used; no native approval is claimed.

**Next proposed batch: Polish D-F, six native calls, after discussion.**

## Polish D-F expansion: 2026-09-08

The focused Polish prompt generated exactly six fresh sequential native field
calls for real fixtures D-F in **6m46s**, without retry or transport failure.
All three pairs passed typed-placeholder checks, restoration, final validation,
and queued-worker persistence. An exact replay reused and persisted all six
responses with zero new calls.

| Fixture | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| D | Pass | Material role failure: neutral `Betroffene` becomes `Poszkodowany` (injured party/victim); title also misspells `Interwencja` as `Intervencja` |
| E | Pass | Acceptable: facts, inpatient hospital meaning, and ongoing investigation survive; bus and hospital wording is awkward |
| F | Pass | Material evidentiary failure: tentative `Hinweise` becomes `dowody` (evidence/proof), and uncertain abnormalities become an intoxication state |

Polish is structurally **6/6** across A-F and **4/6 acceptable** on assistant
meaning/readability review. Preserve D and F without unchanged-prompt retries.
D otherwise retains the complete event sequence and continuing investigation;
F otherwise retains the traffic-control and collision sequence.

Do not run repetitions yet. One focused round remains under the roadmap.
Discuss a short, general Polish clarification that neutral `Betroffener` must not
become a victim or injured party and that `Hinweise`/`Auffälligkeiten` remain
uncertain indications rather than proof or confirmed intoxication. The D title's
spelling error is recorded as a secondary readability issue. No fluent or native
approval is claimed.

**Next proposed step: discuss the second focused Polish D/F gate. No further
live calls until agreed.**

## Polish second-guidance gate and final protocol: 2026-09-08

Commit `63655d9` extends the Polish guidance with three general safeguards:
neutral `Betroffener`/`Betroffene` must not become a victim or injured party;
`Hinweise`/`Auffälligkeiten` remain indications rather than proof or confirmed
intoxication; and headlines use standard Polish spelling. Existing conviction
guidance remains unchanged. Prompt-isolation and processing-package tests passed
before inference.

The focused D/F gate generated four fresh native calls in **5m45s**. Both
material failures were fixed: D uses neutral `Osoba, której dotyczy sprawa`, and
F uses `przesłanki` and `oznaki typowe` rather than proof or confirmed findings.
The D title still misspells `Interwencja` as `Intervencja`, and `zduszenia
konfliktu` is forceful wording for de-escalation; these remain understandable,
non-material errors under the roadmap threshold.

After the gate passed, A, B, C, and E repetition 1 completed in **8m53s**, then A
and B repetition 2 completed in **4m49s**. The final prompt therefore produced
exactly 16 fresh sequential native calls for eight pairs in **19m27s**, without
retry or transport failure. Exact zero-new-call replays succeeded for the D/F
gate, the remaining first-pass fixtures, and both repetitions.

| Pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1 | Pass | Material facts preserved; recurring `świadki` case error and awkward transit wording |
| B/1 | Pass | Conviction and presumption-of-innocence meaning preserved; awkward legal style |
| C/1 | Pass | Exact facts, negation, roles, and causality preserved |
| D/1 | Pass | Neutral role fixed; title spelling and de-escalation style remain weak |
| E/1 | Pass | Facts and ongoing investigation preserved; `autobusem linowym` should be `autobusem liniowym` |
| F/1 | Pass | Evidentiary uncertainty fixed; traffic and arrest sequence preserved |
| A/2 | Pass | Material facts stable; witness-case error repeats |
| B/2 | Pass | Legal meaning stable across repetition |

Polish completes the final protocol at **8/8 structurally valid and 8/8
materially acceptable on assistant review**. This meets the agreed
accurate-and-readable threshold, which permits minor grammar, spelling, and style
errors. It does not establish polished native prose: `świadki`, `Intervencja`,
and `autobusem linowym` are reproducible defects requiring fluent review. No
native approval is claimed.

**Next proposed step: stop and discuss the Polish result before Turkish A-C.**

## Turkish A-C screening: 2026-09-08

The standard HY-MT2 fallback prompt generated exactly six fresh sequential
native field calls for Turkish A-C in **8m16s**, without retry or transport
failure. All three pairs passed typed-placeholder checks, restoration, final
validation, and queued-worker persistence. An exact replay reused and persisted
all six responses with zero new calls.

| Fixture | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A | Pass | Acceptable: attribution, medical examination, transit relationships, duration, and witness request survive; restored S-Bahn wording is redundant and has number/agreement awkwardness |
| B | Pass | Material failure: `beschädigt` becomes stronger `kırdığı` (broke), and final conviction becomes broader `kesinleşmiş bir hüküm` (final judgment/ruling) |
| C | Pass | Material failure: exact 03:30 becomes contradictory `tam olarak 03:30 sularında` (exactly around 03:30), and neutral `befragte` becomes stronger `sorguladı` (interrogated) |

Turkish is **3/3 structural and 1/3 acceptable** on assistant review. Preserve B
and C without unchanged-prompt retries. A retains all material facts despite
minor grammar and redundancy around the immutable transit name.

Do not expand to D-F yet. Discuss one concise, general Turkish rule covering
damage versus breakage (`zarar vermek` rather than `kırmak` when breakage is not
stated), final conviction rather than final judgment, exact versus approximate
time expressions, and neutral police questioning (`ifadesini almak`/`soru
sormak`) rather than interrogation (`sorgulamak`). No fluent or native approval
is claimed.

**Next proposed step: discuss the first focused Turkish B/C gate. No further
live calls until agreed.**

## Turkish focused-guidance B/C gate: 2026-09-08

Commit `4000d25` adds one general Turkish rule covering degree of damage,
conviction versus judgment, exact versus approximate time expressions, and
ordinary questioning versus interrogation. Prompt-isolation and
processing-package tests passed before inference.

A fresh request identity generated exactly four sequential native calls in
**5m36s**, without retry or transport failure. Both pairs passed typed-
placeholder checks, restoration, final validation, and queued-worker
persistence. A zero-new-call replay reused and persisted all four responses.

| Fixture | Initial result | Focused result | Decision |
| --- | --- | --- | --- |
| B | Damage became breakage; final conviction became final judgment | `zarar verdiği`, `mahkûmiyet`, and `kesinleşmiş mahkûmiyet` preserve all three concepts | Pass with style caveat |
| C | Exact time became exact/around; questioning became interrogation | `ifadesine başvurdu` restores neutral questioning, but `tam olarak 03:30 sularında` still contradicts the exact time | Material time-precision failure |

Turkish A-C improves from **1/3 to 2/3 acceptable** on assistant review. The
first focused rule fixes B completely and fixes C's police-action error without
a replacement material error. It does not fix the exact-time phrase even though
the prompt explicitly forbids combining `tam olarak` with `sularında`.

Preserve C without an unchanged-prompt retry and do not expand to D-F. One
focused round remains. Discuss a shorter, direct rule giving the exact rendering
`um genau 03:30 Uhr` → `tam olarak saat 03:30'da`, while keeping `gegen 04:20
Uhr` approximate. No fluent or native approval is claimed.

**Next proposed step: discuss the second and final Turkish C-only gate. No
further live calls until agreed.**

## Turkish user threshold and D-F expansion: 2026-09-08

The user explicitly accepted `tam olarak 03:30 sularında` for this Turkish
quality threshold. C is therefore accepted without regeneration: its neutral
questioning, medical action, numbers, negation, and other relationships are
correct. Turkish A-C becomes **3/3 acceptable under the user-selected
threshold** using the existing focused prompt identity.

The same prompt then generated exactly six fresh sequential native field calls
for real fixtures D-F in **7m07s**, without retry or transport failure. All three
pairs passed typed-placeholder checks, restoration, final validation, and
queued-worker persistence. An exact replay reused and persisted all six
responses with zero new calls.

| Fixture | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| D | Pass | Material role failure: neutral `Betroffene` becomes `mağdur` (victim) |
| E | Pass | Material medical omission: inpatient admission becomes only transport to hospital for treatment |
| F | Pass | Material control/evidence failure: police stop signals become road stop signs, and licence indications become evidence/proof |

Turkish is structurally **6/6** and **3/6 acceptable** across A-F under the
user-selected time threshold. Preserve D-F without unchanged-prompt retries.
The A/B repetitions were not run because the expansion introduced material
failures.

One focused round remains under the roadmap. Discuss concise, general Turkish
guidance for neutral person roles (`Betroffener` is not automatically `mağdur`),
inpatient admission (`hastaneye yatırıldı`), police stop signals rather than road
signs, and uncertain `Hinweise` rather than proof. No fluent or native approval
is claimed.

**Next proposed step: discuss the second focused Turkish D/E/F gate. No further
live calls until agreed.**

## Turkish second-guidance gate and final protocol: 2026-09-08

Commit `898b87f` adds concise, general Turkish guidance for neutral incident
roles, inpatient admission, police stop commands, and tentative indications.
Prompt-isolation and processing-package tests passed before inference. The run
used HY-MT2 digest
`24acc0f002f8c874f34e8b3e22da236405f7c3a47ae9d62d3744cf3c5b4bd693`,
the native adapter, an 8192-token context, typed placeholders, and the frozen
Gazetteer/fixture identities recorded in the durable manifest.

The failed D-F fixtures were the gate. Exactly six fresh sequential native
calls completed in **7m50s**, without retry or transport failure. All three
material issues were fixed: D retains a neutral `ilgili kişi`, E explicitly
says `hastaneye yatırıldı`, and F distinguishes police stop commands and
tentative indications from road signs and proof.

Because the gate passed, A-C repetition 1 completed in **8m32s**, followed by
A/B repetition 2 in **5m45s**. The final-prompt protocol therefore contains
exactly 16 fresh calls over eight title/summary pairs and took **22m07s** in
aggregate. All pairs passed typed-placeholder checks, restoration, final
validation, and queued-worker persistence. Separate zero-new-call replays
verified all six first-pass fixtures and both repetitions.

| Pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1 | Pass | Material facts preserved; injury wording is less precise and S-Bahn grammar is awkward |
| B/1 | Pass | Allegation, uncertainty, conviction, and presumption of innocence preserved |
| C/1 | Pass | Facts, roles, causality, negation, and emergency number preserved; accepted time-phrase caveat |
| D/1 | Pass | Neutral role and full event sequence preserved; accepted time-phrase caveat |
| E/1 | Pass | Inpatient admission restored; scheduled-bus specificity and place suffix remain minor caveats |
| F/1 | Pass | Stop commands and evidentiary uncertainty restored; redundant evidence phrasing remains |
| A/2 | Pass | Stable material pass with the same injury/transit caveats |
| B/2 | Pass | Stable legal-meaning pass; `masumiyet varsayımı` is less conventional than `masumiyet karinesi` |

Turkish completes the protocol at **8/8 structurally valid and 8/8 materially
acceptable on assistant review** under the agreed accurate-and-readable
threshold. This result applies the user's explicit acceptance of `tam olarak
03:30 sularında` and the equivalent D time construction. It permits the minor
precision, grammar, and style issues listed above; it is neither native approval
nor a general quality guarantee. Each raw response and separate manual review
remains in the ignored durable evaluation directory.

**Next proposed step: stop and discuss the Turkish result before the focused
Ukrainian stage.**

## Q6_K comparison experiment: 2026-09-08

The requested full-current-prompt Q6_K comparison is complete for English,
Spanish, French, Italian, Polish, and Turkish. It generated 96/96 intended calls
and 48/48 structurally valid pairs; assistant review accepted 32/48 pairs. One
additional Polish attempt ended in EOF and recovered by exact checkpoint resume.
All 48 pairs subsequently replayed with zero new calls.

Q6_K does not replace the Q5_K_M recommendation: its per-language acceptable
counts were English 5/8, Spanish 4/8, French 4/8, Italian 4/8, Polish 7/8, and
Turkish 8/8. The loaded model used about 7.49 GB on the 8.32 GB host, leaving
about 170 MB available with no swap. Detailed per-pair results, failures,
timings, comparison limitations, the frozen Q5 position, and the exact resume
procedure are in [HY-MT2 quantization comparison](hy-mt2-quantization-comparison.md).

The Q5_K_M roadmap subsequently resumed at Ukrainian; its outcome is recorded
below. Q6 checkpoints remain comparison evidence only and must not be reused for
Q5 or changed prompts.

## Ukrainian focused rounds: 2026-09-08

The initial A result had rendered German reported allegation `soll ... verletzt
haben` as Ukrainian `мав ... поранити`, which can instead express an obligation
or expectation. The first focused round added a general reported-allegation
distinction. Prompt-isolation tests passed before inference. A then generated
exactly two fresh sequential Q5_K_M calls in **5m12s** and passed: `чоловік
нібито легко поранив` unambiguously retains the allegation. Its typed names,
numbers, attribution, medical examination, transit relationships, and negation
also survived. An exact replay used zero new calls.

B and C then generated four fresh calls in **5m37s**. Both were structurally
valid, but each exposed a different material issue. B changed conviction and
final conviction into the broader verdict and final-decision concepts. C
changed ordinary questioning into interrogation and moved the collision from
on Ganghoferstraße to merely near it.

The second roadmap-bounded focused round added reusable Ukrainian distinctions for
conviction versus verdict/decision, presumption of innocence, neutral
questioning versus interrogation, and on-street versus near-street location.
Prompt-isolation tests passed before another four-call B/C gate, which completed
in **6m24s** without retry or transport failure. Exact replay of the final gate
used zero new calls.

| Fixture / round | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A / first | Pass | Pass: the allegation is explicit and all material facts survive; time and U-Bahn phrasing remain awkward but understandable |
| B / first | Pass | Fail: allegation is clear, but conviction becomes verdict and final conviction becomes final decision |
| C / first | Pass | Fail: ordinary questioning becomes interrogation and on-street becomes near-street |
| B / second | Pass | Fail: conviction and presumption terminology are fixed, but `мав пошкодити` regresses the alleged act into obligation-like wording |
| C / second | Pass | Pass: neutral questioning and exact street relation are restored; all facts, roles, numbers, causality, and negation survive |

All **10/10 requested native field calls** completed and all five generated
pairs passed deterministic validation, restoration, and queued-worker
persistence. There were no retries or transport/resource failures. However,
the final B output still contains a consequential allegation ambiguity even
though the prompt explicitly prohibits that construction. It is preserved as
the first attempt rather than retried for a favorable sample.

Ukrainian is therefore **paused after two focused prompt rounds**. D-F and A/B
repetitions were not run, and no native approval is claimed. The guidance is
still retained because it demonstrably fixes A's allegation, B's legal terms,
and C's questioning/location errors, but this evidence does not qualify the
language for rollout.

That was the roadmap decision at this checkpoint. The user subsequently approved
two explicit additional rounds; their complete results supersede the paused
decision and are recorded below.

## Ukrainian user-approved allegation round: 2026-09-08

The third focused round made the reported-allegation rule shorter and mandatory:
German `soll ... haben` must use explicit Ukrainian allegation wording and must
not use `мав`/`мала`/`мали` plus an infinitive. B was the gate and passed on its
first v3 attempt, preserving allegation, unresolved involvement, conviction,
final conviction, and presumption of innocence.

The same prompt then completed A, C, and real fixtures D-F plus A/B repetitions.
All **16/16 intended native calls** completed in about **26m05s**, without retry,
transport, or resource failure. All eight pairs were structurally valid. A-C,
E, and both repetitions passed assistant meaning review; D and F exposed new
material issues:

- D changed neutral `Betroffene` into `Постраждалого`, identifying the arrested
  person as injured/a victim without source support.
- F changed tentative `Hinweise` into `докази` (evidence/proof) and strengthened
  alcohol/drug-typical observations toward actual influence.

These first attempts were preserved. At this stage v3 was **6/8 materially
acceptable**, with no native approval.

## Ukrainian neutral-role/evidence round and final protocol: 2026-09-09

The user approved one further focused round for the newly discovered D/F issues.
The v4 prompt adds two general distinctions: neutral `Betroffener`/`Betroffene`
must not become an injured/victim label without source support, and tentative
`Hinweise`/`Auffälligkeiten` must not become proof, confirmed intoxication, or
confirmed substance use. Prompt-isolation tests passed before inference.

D and F were the gate. Their four fresh calls completed in **7m34s** and both
passed: D used the neutral `Особу, якої це стосується`; F used `ознаки` and
`підозри` without proof or confirmed impairment. The remaining six pairs then
completed under the identical v4 request identity.

One combined invocation hit Go's default ten-minute test timeout while B/1's
summary was in flight. The durable recorder showed A/1, A/2, and B/1's title as
complete. Resume replayed the saved B title and generated only the missing
summary under the exact same request hash. This was an invocation/harness
timeout, not a model, validation, memory, or translation failure. Subsequent
batches used `go test -timeout 15m`.

| Pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1 | Pass | Allegation, attribution, medical examination, transit relationships, duration, and witness request preserved; time/U-Bahn phrasing is awkward but understandable |
| B/1 | Pass | Allegation, unresolved involvement, conviction, final conviction, and presumption of innocence preserved; minor grammatical awkwardness |
| C/1 | Pass | Exact/approximate times, street relation, speed, causality, medical examination, neutral questioning, negation, and `110` preserved |
| D/1 | Pass | Neutral person role restored; injury, response, arrest without resistance, release, and ongoing investigation preserved |
| E/1 | Pass | Scheduled bus/e-bike, serious injury, inpatient treatment, and ongoing traffic-police investigation preserved |
| F/1 | Pass | Stop signals and tentative licence/alcohol/drug indications preserved without proof or confirmed influence |
| A/2 | Pass | Stable material pass; output is effectively identical to A/1 |
| B/2 | Pass | Stable allegation and legal-meaning pass |

The v4 checkpoint contains **16/16 completed intended native calls** and **8/8
structurally valid, materially acceptable pairs** on assistant review. Successful
call durations total about **26m18s**; the interrupted B-summary attempt adds
roughly two minutes of discarded wall time but no accepted output. A complete
zero-new-call replay restored, validated, and persisted all eight pairs through
the queued worker path.

Ukrainian is now **acceptable on this sample** under the agreed
accurate-and-readable threshold. Minor grammatical and stylistic awkwardness is
documented; this is not native approval or a general quality guarantee.

**Next proposed step: stop and discuss the Ukrainian result before Chinese
A-C. The exact Q5_K_M resume point is Chinese, followed by Hindi and Russian.**

## Chinese final protocol: 2026-09-09

The initial A-C screen passed structure, but B broadened final conviction to a
formal judgment. Focused round one corrected the legal condition and exposed a
generic vehicle window translated as a windshield; round two corrected that
distinction. Under round two, A-C and E were materially acceptable. D was also
semantically acceptable, but deterministic validation rejected the legitimate
conversion of German `20:00` to Chinese `20点`. F strengthened tentative
alcohol/drug abnormalities into actual drinking/drug problems.

The validator now recognizes equivalent Chinese `点` time notation while still
rejecting changed minutes, changed hours, and added times. Regression tests cover
hour-only and hour/minute forms. Focused round three adds only the evidenced
uncertainty distinction: `Hinweise`/`Auffälligkeiten` remain tentative signs or
observations and must not establish consumption or impairment.

F was the v4 gate and passed. The final prompt then generated A-E and the A/B
repetitions. All **16/16 intended native calls** completed without retry,
transport, memory, or model failure. Successful call durations total about
**11m47s**. Exact zero-new-call replay restored, validated, and persisted all
eight pairs through the queued worker path.

| Pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1 | Pass | Allegation, attribution, medical examination, transit relationships, duration, and witness request preserved; injury grammar remains awkward |
| B/1 | Pass | Generic vehicle window, unresolved involvement, conviction, final conviction, and presumption of innocence preserved |
| C/1 | Pass | Exact/approximate times, speed, closure, examination, neutral questioning, negation, and `110` preserved |
| D/1 | Pass | Localized `20点` accepted; unclear danger, response, neutral person, arrest/release, and continuing investigation preserved |
| E/1 | Pass | Bus/e-bike, serious injury, inpatient treatment, and continuing traffic-police investigation preserved |
| F/1 | Pass | Licence and alcohol/drug indications remain tentative; evasion, collision, check, and arrest sequence preserved |
| A/2 | Pass | Stable material pass with the same minor injury-grammar weakness |
| B/2 | Pass | Stable generic-window and final-conviction pass |

Chinese is **acceptable on this sample**: **8/8 structural and 8/8 materially
acceptable pairs** under the agreed threshold. Minor wording and redundancy are
recorded, and no native approval or general quality guarantee is claimed.

The user authorized the remaining session to continue autonomously with durable
checkpoints and PR updates. **Next: Hindi A-C with HY-MT2 Q5_K_M.**

## References

- [Tencent HY-MT2 contract](https://huggingface.co/tencent/Hy-MT2-7B)
- [Google TranslateGemma contract](https://huggingface.co/google/translategemma-12b-it)
- [Q3_K_S artifact](https://huggingface.co/mradermacher/translategemma-12b-it-GGUF)
