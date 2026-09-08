# Translation readiness: HY-MT2 first, then remaining languages

Agreed 2026-09-08 for PR #55. This roadmap supersedes earlier future-work
recommendations in [Translation model evaluation](translation-model-evaluation.md)
and PR comments. Preserve their evidence, harnesses, and checkpoints.

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
saved responses offline first. Allow at most two focused prompt-revision rounds
per language. Test failures first, then complete the pack under the revised
prompt. Preserve every attempt; do not retry until a lucky pass. Prefer short,
general language-specific terminology/context guidance for local issues.
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
| French | HY-MT2 | A-F: 6/6; revised E: 1/1 | 6/6 acceptable after focused E; recorded style/legal caveats | Not run | Repetition pending; no native approval | Discuss, then repeat A/B: four calls |
| Italian | HY-MT2 | Not run | Pending | Pending | Current pipeline untested | After French |
| Polish | HY-MT2 | Not run | Pending | Pending | Current pipeline untested | After Italian |
| Turkish | HY-MT2 | Not run | Pending | Pending | Historical grammar/redundancy | After Polish |
| Ukrainian | HY-MT2 | A: 1/1 pair | Allegation wording ambiguous | Not run | Needs focused work; unapproved | Focused review when Ukrainian is reached |
| Chinese | HY-MT2 | Not run | Pending | Pending | Historical semantic errors | After Ukrainian |
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

## References

- [Tencent HY-MT2 contract](https://huggingface.co/tencent/Hy-MT2-7B)
- [Google TranslateGemma contract](https://huggingface.co/google/translategemma-12b-it)
- [Q3_K_S artifact](https://huggingface.co/mradermacher/translategemma-12b-it-GGUF)
