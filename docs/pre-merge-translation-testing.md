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
| English | HY-MT2 | A: 1/1 pair | Usable gate; minor wording/grammar caveats | Not run | Full qualification pending | Discuss, then B/C: four calls |
| Spanish | HY-MT2 | Not run | Pending | Pending | Current pipeline untested | After English |
| French | HY-MT2 | Not run | Pending | Pending | Current pipeline untested | After Spanish |
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
No further live batch has started. **Next proposed batch: English B/C, four
native calls, after discussion.**

## References

- [Tencent HY-MT2 contract](https://huggingface.co/tencent/Hy-MT2-7B)
- [Google TranslateGemma contract](https://huggingface.co/google/translategemma-12b-it)
- [Q3_K_S artifact](https://huggingface.co/mradermacher/translategemma-12b-it-GGUF)
