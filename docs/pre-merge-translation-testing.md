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
| Croatian, Bosnian, Greek | `hf.co/mradermacher/translategemma-12b-it-GGUF:Q2_K`, then `translategemma:4b-it-q8_0` when the 12B artifact cannot load | `translategemma` |
| Romanian | `hf.co/bartowski/Qwen_Qwen3.5-9B-GGUF:Q3_K_M` | `structured` |
| Russian and Croatian comparison | `hf.co/mradermacher/Seed-X-Instruct-7B-GGUF:Q5_K_M` | `seed-x` |
| Russian, Croatian, Greek, Hindi comparison | SalamandraTA v3 Q5_K_M; Q4_K_M only after repeated Q5 memory failures | `salamandra-ta` |

Verify installed artifact, language-code support, template, and available memory.
First run Croatian A's two fields as a loading/translation gate. Record loading
time, request duration, completion, and memory evidence. Q3_K_S was unavailable;
the later authorized fallback order is Q2_K, then 4B Q8 when the 12B artifact
cannot load. Stop and report loading failure or repeated Ollama restarts.

Use the same eight-pair protocol, one language at a time. Romanian needs eight
structured requests; distinguish requests from field counts. Seed-X may be
selected only for officially supported targets, and each exact artifact must
first pass a placeholder-free target-language control plus fixture A. Further
fallback models need a separate decision after reporting the selected candidate.

SalamandraTA allows at most eight focused improvements per language, but typed-
placeholder corruption is a hard stop for the remaining SalamandraTA batch. Q4
is a memory fallback only, not a retry for Q5 translation-quality failures.

### New-model tournament: LLaMAX3, EuroLLM, Tower+, and TowerInstruct

The next unlocked-language comparison uses only officially supported pairs:

| Model | Targets | Quantization order | Adapter |
| --- | --- | --- | --- |
| LLaMAX3-8B-Alpaca | `bs`, `hr`, `el`, `hi`, `ru` | Q4_K_M only | `llamax3` |
| EuroLLM-9B-Instruct-2512 | `hr`, `el`, `hi`, `ru` | Q4_K_M, Q4_K_S, Q3_K_L | `eurollm` |
| Tower-Plus-9B | `hi`, `ru` | Q4_K_M, Q4_K_S, Q3_K_L | `tower-plus` |
| TowerInstruct-7B-v0.2 | `ru` | Q6_K, Q5_K_M | `tower-instruct` |

Run fixture A first, grouped by model in the table order; within LLaMAX3 use
Bosnian, Croatian, Greek, Hindi, Russian, then use the displayed target order for
the other families. Placeholder corruption disqualifies only that model-language
pair and does not stop unrelated pairs. An unchanged-request retry is allowed
only for an isolated transport interruption. Two confirmed load/OOM failures on
one request, or two in the first four fields, move the entire family to the next
quantization and require its completed gates to be rerun there. Quality failures
never trigger quantization fallback.

Advance the best two structurally safe candidates per language to B/C; Bosnian
has only LLaMAX3. Rank material fact/legal fidelity before readability, then use
permissive licensing, infrastructure stability, and latency as tie-breakers.
Compare saved HY-MT2 Russian and Qwen Croatian evidence without regenerating it.
Complete Bosnian, Greek, Hindi, Croatian, then Russian through D-F and A/B repeat.

Allow ten prompt revisions per language across candidates. The leader receives
at most six; stop it earlier when one material error survives three consecutive
revisions or two revisions introduce critical regressions, then give a safe
runner-up the remaining budget. Test failed fixtures first, but qualify only by
running all eight pairs under one unchanged prompt/model/digest/quantization.
Keep every attempt. TowerInstruct and Tower+ remain evaluation-only pending an
explicit review of their noncommercial licensing.

### New-candidate extension: MADLAD-400, Gemma 4, and Tower+ Q3

The next comparison keeps every selected language/model route unchanged and
targets only paused Bosnian, Greek, Hindi, Croatian, and Russian. MADLAD-400 is
eligible for all five through its raw `<2xx> source` translation contract;
Gemma 4 is exploratory for all five through user-only chat with thinking off;
Tower+ remains limited to its officially listed Hindi and Russian targets. The
effective context is 2048 for all three families on the Pi.

Run the fixture-A gates grouped by model: MADLAD and Gemma 4 each use
`bs,el,hi,hr,ru`; Tower+ uses `hi,ru`. MADLAD has no prose prompt-tuning path.
Typed-placeholder corruption disqualifies only the affected pairing. For Gemma
4, use UD-Q4_K_XL then Q4_K_M only on confirmed memory failure. For Tower+, use
Q3_K_M then Q3_K_S only on confirmed memory failure, and rerun completed gates
after any fallback. A missing or incomplete Ollama tag is not a model failure.

Advance at most two structurally safe new candidates per language through B/C,
then run D-F and repeat A/B for the winner. Compare saved historical candidates
without regenerating them. The unchanged acceptance rules below apply, and all
requests, raw responses, restored output, identities, timings, validation, and
manual reviews remain in the readiness harness's durable records. The untuned
upper bound is 114 new native calls. Prompt revisions for Gemma 4 and Tower+ are
limited to ten per language across both models, with at most six for the leader.

#### MADLAD-400 fixture-A gate: 2026-09-10

The exact Q4_K_M artifact loaded successfully at 2048 requested context on
Ollama 0.33.3. Its metadata reported T5, 8.3B parameters, a 512 native context,
and digest `ce653ea2ea76`. The first load took about 60 seconds; completed field
calls then took about two to eight seconds. There was no OOM or transport error.

| Pair | Calls | Result |
| --- | ---: | --- |
| Bosnian | 2 | Title changed/dropped placeholders and contained no translation; summary was only `.` |
| Greek | 1 | Empty title |
| Hindi | 1 | Empty title |
| Croatian | 1 | Empty title |
| Russian | 1 | Empty title |

All five pairings are disqualified. These were HTTP 200 completed model outputs,
not infrastructure failures. Per the fixed MADLAD contract, no prose prompt
tuning or quantization retry applies; the remaining summaries were not called
after their titles failed. Raw requests, responses, timings, preflight metadata,
worker results, and pending manual-review records remain in the ignored local
`readiness-madlad400-q4km-v1` checkpoint.

#### Gemma 4 fixture-A gate: 2026-09-10

UD-Q4_K_XL loaded and completed all ten fields at 2048 context without an OOM or
transport failure. Ollama reported Gemma 4, 7.52B parameters, and digest
`cbde5d133210`. The first load took about 85 seconds; generation took about
18–105 seconds per field. Every pairing preserved and restored all typed
placeholders and passed the deterministic production validators.

| Pair | Structure | Editorial result / disposition |
| --- | --- | --- |
| Bosnian | Pass | Clear overall, but stated the alleged injury as fact; focused work |
| Greek | Pass | Allegation phrase contained a Chinese character and the title invented “intensive”; focused work |
| Hindi | Pass | Stated the alleged injury as fact; focused work |
| Croatian | Pass | Stated the allegation as fact and changed medical examination to receiving treatment; focused work |
| Russian | Pass | Stated the allegation as fact and changed medical examination to receiving treatment; focused work |

No Q4_K_M fallback applies because there was no confirmed memory failure. The
unchanged baseline is not acceptable for any of the five, but each remains
eligible for a focused general prompt revision after the Tower+ gate and
shortlist comparison. Evidence is retained in
`readiness-gemma4-udq4kxl-v1`.

The unchanged Gemma B/C screen kept placeholders intact. C preserved material
facts in all five languages; Bosnian C exposed a validator false positive for
the ordinary date form `29. avgusta`, now covered by a regression test. B again
lost allegation framing in every language; Greek mixed in a French word and
Croatian a Polish character, with additional grammar errors. The first focused
revision therefore adds only general target-language, allegation, and medical-
examination guidance and must re-pass A/B before expansion.

#### Tower+ Q3_K_M fixture-A gate at 2048 context: 2026-09-10

The Q3_K_M artifact loaded and completed all four fields without an OOM or
transport failure, establishing that the prior family failures were caused by
the larger quantization/context combination rather than an incompatible
runtime. Ollama reported Gemma 2, 9.24B parameters, and digest
`02a89d5b17a3`. Calls took about 38–150 seconds after a roughly 73-second load.

| Pair | Structure | Editorial result / disposition |
| --- | --- | --- |
| Hindi | Pass | Headline blurred/reversed the delay causality and summary stated the allegation as fact; focused work |
| Russian | Pass | Used “police raid” rather than neutral police operation and stated the allegation as fact; focused work |

Q3_K_S was not run because Q3_K_M had no confirmed memory failure. Both
pairings remain eligible for a focused general prompt revision. Evidence is
retained in `readiness-towerplus-q3km-2048-v1`.

The unchanged Tower+ B/C screen also preserved all placeholders. Hindi B kept
the allegation framing but translated the vehicle window as a bottle/vial, and
Hindi C lost the approximate nature of 04:20. Russian B again stated the alleged
damage as fact, while Russian C also made 04:20 exact. Gemma 4 remains the
leading new candidate for both languages; Tower+ is retained as the runner-up.

Gemma revision 1 fixed allegation and examination handling on A across all five
languages. It did not qualify a language yet: Bosnian and Croatian B called a
suspect formally accused; Greek B contaminated the presumption-of-innocence term;
Hindi A made the approximate 03:30 exact; and Russian's otherwise complete 8/8
screen weakened explicit crash causality in F's headline. Revision 2 adds only
the corresponding general legal-status, target-term, approximate-time, and
causality/gender distinctions. Failed fixtures run first before any full rerun.

Bosnian's complete revision-2 screen preserved structure but failed D through an
Indonesian word and weakened E's inpatient status. Revision 3 fixed the scheduled
bus and inpatient distinction but repeated the cross-language word and changed
the traffic-police role. Revision 4 fixed D/E, then regressed B's presumption of
innocence into a nonsensical term. Revision 5 explicitly retained the established
Bosnian legal term and completed the final eight-pair protocol. All eight pairs
preserved material facts and legal meaning; A/B repeats were identical. Awkward
grammar and occasional Serbian/Ekavian wording remain recorded caveats, so this
is an assistant-reviewed sample result rather than native approval.

Greek revision 2 preserved structure and fixed the earlier legal term, but the
full run changed neutral witness questioning into arrest and mixed Cyrillic into
a translated headline. Revision 3 fixed both targeted distinctions in C and D's
headline, then inserted the Polish word `piątku` into D's summary despite the
Greek-only instruction. Cross-language contamination therefore survived three
focused prompt rounds. The pairing is paused under the repeated-error rule; no
single final prompt completed the eight-pair protocol.

Hindi revision 2 preserved structure but C converted source numbers to
Devanagari and even mixed numeral scripts in `110`. Revision 3 fixed exact ASCII
numbers but lost A's approximate time. Revision 4 fixed both, then E omitted the
explicit female cyclist; revision 5 fixed gender but regressed A's allegation
framing. The sixth and final Gemma revision made the allegation explicit and
completed all eight pairs. Material facts, legal meaning, numbers, causality,
gender, and uncertainty passed; A/B repeats were identical. Minor awkward Hindi
wording remains an assistant-reviewed caveat rather than native approval.

Croatian revision 2 passed A-C but D lost the arrest and mistranslated the
apartment-building setting. Revision 3 fixed D and standard Croatian traffic
wording, then E weakened inpatient admission and used non-Croatian traffic-
police terminology. Revision 4 retained inpatient admission and the traffic-
police role and completed all eight pairs. Material facts and legal meaning
passed, and A/B repeats were identical. Uneven agreement and occasional register
issues remain recorded as non-material assistant-review caveats.

Russian revision 2 completed the full eight-pair protocol without another
prompt change. All placeholders and deterministic checks passed; allegation and
legal status, exact and approximate times, medical examination, neutral witness
questioning, female cyclist and inpatient status, arrest/release, explicit crash
causality, and substance indications survived. A/B repeats were identical. This
is acceptable on the assistant-reviewed sample without native approval.

Tower+ Q3_K_M Hindi remained structurally safe at 2048 context, but did not
advance beyond fixture A. Revision 1 preserved most facts while stating the
alleged injury as fact. Revision 2 appended an explanatory note to the title and
its summary ended with an isolated EOF. Revisions 3 and 4 removed the commentary
and preserved approximate time, transit causality, medical examination, and
Gazetteer placeholders, but again rendered the alleged act as established fact
despite increasingly explicit general evidential guidance. The material error
therefore survived three completed summary generations and the four-revision
Tower budget is exhausted. Hindi/Tower+ is paused; Q3_K_S was not used because
there was no repeated memory/load failure. Gemma 4 remains the qualified Hindi
candidate, and Tower+ remains evaluation-only under its noncommercial license.

Tower+ Q3_K_M Russian passed A-C under revision 1 except that A made the
approximate 03:30 exact-looking. Revision 2 fixed that distinction; A/B repeats
were byte-for-byte stable and C preserved its exact and approximate times,
numbers, examination, negation, and questioning. During the same-prompt
expansion, D translated neutral `Betroffene` as a victim and omitted "without
resistance." Revision 3 appended an alternate translation and commentary to D's
title. The fourth and final Tower revision removed the commentary and restored
"without resistance," but again inferred a victim role. Russian/Tower+ is
therefore paused without a complete eight-pair protocol. Q3_K_S was not used
because Q3_K_M had no repeated memory/load failure.

#### Gemma 4 versus Tower+ Q3 final comparison

| Language | Candidate | Structure | Material accuracy / readability | Repeats | Mean completed-call latency | Memory | Revisions | License / decision |
| --- | --- | --- | --- | --- | ---: | --- | ---: | --- |
| Hindi | Gemma 4 UD-Q4_K_XL | Final 8/8 pairs passed | 8/8 materially acceptable; minor awkward phrasing | A/B identical | 50.5 s | Stable at 2048 | 6 | Apache-2.0; acceptable on sample |
| Hindi | Tower+ Q3_K_M | Fixture A stayed recoverable; one title emitted commentary in an intermediate revision | Alleged injury became established fact in three completed summaries | Not run | 135.0 s in final revision | One isolated EOF; no repeated OOM/load failure | 4 | Noncommercial; paused |
| Russian | Gemma 4 UD-Q4_K_XL | Final 8/8 pairs passed | 8/8 materially acceptable | A/B identical | 41.1 s | Stable at 2048 | 2 | Apache-2.0; acceptable on sample |
| Russian | Tower+ Q3_K_M | A-C and targeted D stayed recoverable; revision 3 added commentary | A-C passed after time fix, but D repeatedly inferred a victim role | A/B identical under revision 2 | 101.5 s across revision-2 completed calls | One isolated EOF; no repeated OOM/load failure | 4 | Noncommercial; paused |

Gemma 4 is both materially stronger and roughly two to three times faster in
these final Hindi/Russian samples. Tower+ remains useful negative evidence but
is neither qualified nor licensable for production under the evaluated terms.

### Tournament fixture-A gate: 2026-09-09

The baseline gate used the production queued-worker path, typed Gazetteer
placeholders, separate sequential title/summary calls, and durable recording.
Every generated response was inspected; mechanically accepted output was not
treated as a meaning pass.

| Candidate | Pair | Structure | Editorial result / disposition |
| --- | --- | --- | --- |
| LLaMAX3 Q4_K_M | Bosnian | Pass | Lost allegation framing, mixed Croatian month name, awkward grammar, and reversed headline causality; tunable |
| LLaMAX3 Q4_K_M | Croatian | Pass | Lost allegation framing, untranslated headline term, malformed transit clause, and reversed headline causality; tunable |
| LLaMAX3 Q4_K_M | Greek | Fail | Summary omitted STREET and COMMUTER_TRAIN placeholders; pairing permanently disqualified |
| LLaMAX3 Q4_K_M | Hindi | Restoration passed; final title length failed | Lost allegation framing, changed the examined woman's gender/reference, and duplicated witness wording; tunable |
| LLaMAX3 Q4_K_M | Russian | Pass | Lost allegation framing, changed unaffected U-Bahn to closed, omitted the medical examination, and invented a rationale; tunable |
| EuroLLM Q4_K_M | Croatian | Fail | Emitted the instruction/placeholder regex and Markdown; pairing permanently disqualified |
| EuroLLM Q4_K_M | Greek | Fail | Invented an unknown placeholder for the time, breaking exact restoration; pairing permanently disqualified |
| EuroLLM Q4_K_M | Hindi | Pass | Lost police attribution/allegation and weakened location/contact details; tunable |
| EuroLLM Q4_K_M | Russian | Fail | Translated the instruction instead of the title; pairing permanently disqualified before summary generation |
| Tower+ Q4_K_M, Q4_K_S, Q3_K_L | Hindi/Russian | No output | Every quantization was kernel-OOM-killed while loading the required 8192 context; family hardware-blocked on the 7.7 GiB host |
| TowerInstruct Q6_K | Russian | Pass | Preserved all placeholders and most facts, but stated the alleged injury as fact and mistranslated `Polizeieinsatz` in the title; tunable |

No Tower+ quality score exists: its failures are infrastructure failures. No
lower quantization was tried for semantic or placeholder failures. The current
shortlist is LLaMAX3 alone for Bosnian and Croatian; LLaMAX3 plus EuroLLM for
Hindi; and LLaMAX3 plus TowerInstruct for Russian. Greek has no structurally safe
candidate from this tournament and remains paused unless a separate candidate is
authorized. Bosnian, Greek, Hindi, Croatian, and Russian were subsequently
completed in that order; their decisions are recorded below.

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

As of 2026-09-09, the user-selected intended candidates are HY-MT2 Q5_K_M for
English, Spanish, French, Polish, Turkish, Ukrainian, and Chinese; Italian is a
potential HY-MT2 selection pending the remaining caveat; Romanian is selected
for Qwen 9B structured. This records a routing decision with known limitations,
not native-language approval.

| Language | Candidate | Structural | Meaning/readability | Repeats | Decision / open issue | Next action |
| --- | --- | --- | --- | --- | --- | --- |
| English | HY-MT2 | Final protocol: 8/8 pairs | Accurate/readable on assistant review; minor A/B wording caveats | Revised A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Spanish A-C |
| Spanish | HY-MT2 Q5_K_M | Two new focused revisions remained structural | E's scheduled bus was fixed; the full rerun then moved the incident from on Ingolstädter Straße to near it | Not completed | Unqualified after the two newly authorized HY revisions; no native approval | Prefer the safer Gemma evidence, but leave Spanish paused |
| Spanish | Gemma 4 E4B UD-Q4_K_XL | Typed gate and generated final-prompt pairs all passed structure | Police-office and generic-glass fixes worked; final E omitted inpatient admission | Final A/B repeats passed; protocol stopped at E | Best observed candidate, but paused after the total four-revision budget; no native approval | Do not change the preferred model or enable the route |
| French | HY-MT2 Q5_K_M | Final protocol: 8/8 pairs | 8/8 materially acceptable after glass and street-location revisions; minor redundant-name/article wording | Final A/B: stable pass | Acceptable on this sample; no native approval | Leading qualified candidate; seek native review |
| Italian | HY-MT2 | Historical A-F: 6/6; third-guidance E/F: 2/2 structural | Revised E passes; revised F summary passes but title remains grammatically invalid; final prompt not run across full protocol | Not run | Potentially locked to HY-MT2; final user decision/native review still needed | Keep current candidate and caveat visible |
| Polish | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable; recurring grammar/spelling defects recorded | Revised A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Turkish A-C |
| Turkish | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable under the user-selected time threshold; recurring precision/grammar caveats recorded | Revised A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Ukrainian |
| Ukrainian | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable after two explicit user-approved extra rounds; minor grammar/style caveats recorded | Final A/B: 2/2 pass | Acceptable on this sample; no native approval | Stop; discuss before Chinese A-C |
| Chinese | HY-MT2 | Final protocol: 8/8 pairs | 8/8 materially acceptable after three focused rounds; recurring awkward A wording recorded | Final A/B: 2/2 pass | Acceptable on this sample; no native approval | Continue autonomously with Hindi A-C |
| Hindi | HY-MT2 | Best revision: A-F 6/6 structural | Best revision 4/6 acceptable (B-E); A allegation and F substance uncertainty fail | Not run | Paused after six focused rounds; no native approval | Retain best observed prompt; evaluate another model separately |
| Hindi | SalamandraTA v3 Q5_K_M | Base A structurally passed | A changed delay to stopping transit and stated the alleged injury as fact | Not run | Incomplete; no native approval | Tuning/further fixtures stopped after Croatian placeholder corruption |
| Hindi | LLaMAX3 Q4_K_M | A-C and three focused A/B revisions remained structurally safe | C passed; A repeatedly lost allegation modality and later headlines regressed; B lost allegation/legal meaning | Not run | Paused by the three-consecutive-error rule; no native approval | HY-MT2 remains stronger on the recorded sample |
| Hindi | EuroLLM Q4_K_M | A passed structure; B title failed hard | B translated the placeholder instruction and emitted its regex | Not run | Pairing permanently disqualified; no native approval | Do not tune or resume |
| Hindi | Tower+ Q4_K_M / Q4_K_S / Q3_K_L | No output | Every installed quantization was kernel-OOM-killed at required 8192 context | Not run | Hardware-blocked on current host | Do not retry without more memory or an explicitly revised context experiment |
| Hindi | Gemma 4 E4B UD-Q4_K_XL | Final protocol: 8/8 pairs | 8/8 materially acceptable after the sixth and final Gemma revision; minor awkward wording recorded | Final A/B: identical pass | Acceptable on this sample; no native approval | Leading qualified candidate; seek native review |
| Hindi | Tower+ Q3_K_M at 2048 | Fixture A remained structurally safe through four prompt revisions | Alleged injury became established fact in revisions 1, 3, and 4; revision 2 also appended commentary and its summary was interrupted | Not run | Paused after the material error survived three completed generations; noncommercial license | Prefer qualified Gemma 4; do not resume or use Q3_K_S for semantic failure |
| Russian | HY-MT2 | All generated pairs structural except one revision-1 overlong title | No single final prompt passed A-F; latest B fixes legal meaning but implies multiple suspects | Not run | Paused after six focused rounds; no native approval | Retain latest legally safer prompt; evaluate another model separately |
| Russian | LLaMAX3 Q4_K_M | A-C preserved placeholders | B omitted explicit no-conviction status; C invented afternoon and changed one doctor to multiple; A lost allegation and changed U-Bahn status | Not run | Not selected for tuning; no native approval | TowerInstruct was stronger on B/C; HY-MT2 remains stronger overall |
| Russian | TowerInstruct Q6_K | A-C and three focused A/B revisions preserved placeholders | C passed; B legal status passed but allegation failed; A allegation failed every revision and final revision invented morning | Not run | Paused by the three-consecutive-error rule; no native approval | Do not resume; exact license also remains noncommercial |
| Russian | EuroLLM Q4_K_M | Fixture-A title failed hard | Translated the instruction instead of the source | Not run | Pairing permanently disqualified | Do not tune or resume |
| Russian | Tower+ Q4_K_M / Q4_K_S / Q3_K_L | No output | Every installed quantization was kernel-OOM-killed at required 8192 context | Not run | Hardware-blocked on current host | Do not retry without more memory or an explicitly revised context experiment |
| Russian | SalamandraTA v3 Q5_K_M | A preserved structure across base plus six focused rounds | No prompt preserved both allegation framing and the operation/delay headline relationship | Not run | Paused; no native approval | HY-MT2 remains the stronger observed candidate |
| Russian | Gemma 4 E4B UD-Q4_K_XL | Final protocol: 8/8 pairs | 8/8 materially acceptable under revision 2 | Final A/B: identical pass | Acceptable on this sample; no native approval | Leading qualified candidate; seek native review |
| Russian | Tower+ Q3_K_M at 2048 | A-C passed structure; focused D remained recoverable through revision 4 | A-C passed after the time fix, but D repeatedly changed a neutral person into a victim; revision 3 also emitted commentary | Revision-2 A/B identical | Paused after four Tower revisions without one complete protocol; noncommercial license | Prefer qualified Gemma 4; do not resume or use Q3_K_S for semantic failure |
| Russian | Seed-X 7B Q5_K_M | 0 usable pairs; minimal control failed before validation | General and official-minimal prompts produced incomplete whitespace, wrong scripts, unrelated text, or loops; placeholder-free `<ru>` control also failed | Not run | Paused; artifact/runtime prerequisite failure, not RAM; no native approval | Do not configure this artifact; HY-MT2 remains materially stronger |
| Croatian | TranslateGemma Q2_K / 4B Q8 | Q2 0/3 acceptable; Q8 no single prompt passed A-F | Q2 broadly malformed; Q8 unstable allegation/legal fidelity, Croatian/Serbian leakage, and missing detail | Not run under a qualifying prompt | Paused after six Q8 focused rounds; no native approval | Qwen was stronger on A-E, but both candidates remain paused |
| Croatian | Qwen 9B structured | Best complete screen: A-E pass; F rejected | No final prompt passed F; Serbian/malformed output or material fact changes persisted | Not run | Paused after six focused rounds; no native approval | Retain safest observed compact prompt; evaluate another model separately |
| Croatian | LLaMAX3 Q4_K_M | Baseline A-C preserved placeholders; focused runs remained recoverable | B changed conviction certainty and C changed approximate time; revision 1 looped with invented facts and revision 2 invented schedule claims | Not run | Paused after two critical-regression revisions; no native approval | Qwen remains the strongest observed Croatian candidate |
| Croatian | SalamandraTA v3 Q5_K_M | A-D preserved placeholders; E title corrupted the token | Relevant Croatian with legal/grammar caveats before replacing `__MB_TRANSIT_0001__` by literal regex text | Not run | Hard-gate failure; no native approval | Do not configure; Qwen remains strongest observed candidate |
| Croatian | Seed-X 7B Q5_K_M | 0 usable pairs; minimal control failed before validation | Official-minimal fixture A produced unrelated Chinese/wrong-script output and a token loop; placeholder-free `<hr>` control returned whitespace | Not run | Paused; artifact/runtime prerequisite failure, not RAM; no native approval | Do not configure this artifact; Qwen remains materially stronger |
| Croatian | Gemma 4 E4B UD-Q4_K_XL | Final protocol: 8/8 pairs | 8/8 materially acceptable after four focused revisions; uneven grammar/register recorded | Final A/B: identical pass | Acceptable on this sample; no native approval | Keep as leading Croatian candidate; compare with saved Qwen evidence |
| Bosnian | TranslateGemma Q2_K / 4B Q8 | Q2 A failed; Q8 gates completed through six focused rounds | No single prompt qualified A-F; mixed script, token mutation/omission, lost qualifiers, and changed uncertainty persisted | Not run under a qualifying prompt | Paused after six Q8 focused rounds; no native approval | Retain final concise guidance; evaluate another model separately |
| Bosnian | LLaMAX3 Q4_K_M | A remained structurally safe through three focused revisions | Bosnian month wording improved, but the model repeatedly reversed headline agency and stated an allegation as fact | Not run | Paused by the three-consecutive-error early-stop rule; no native approval | Do not spend remaining revision budget on this pairing |
| Bosnian | Gemma 4 E4B UD-Q4_K_XL | Final protocol: 8/8 pairs | 8/8 materially acceptable after five focused revisions; awkward grammar and occasional Serbian/Ekavian wording recorded | Final A/B: identical pass | Acceptable on this sample; no native approval | Keep as leading Bosnian candidate; seek native review |
| Greek | TranslateGemma 4B Q8 | Best round: A-D pass across accumulated gates; E/F fail | No single prompt passed A-F; scheduled-bus/traffic-police qualifiers and F escape/arrest/uncertainty remain unreliable | Round-3 A/B: 2/2 pass | Paused after six focused rounds; no native approval | Retain final safety guidance; evaluate another model separately |
| Greek | SalamandraTA v3 Q5_K_M | Base and focused A-C preserved all placeholders/numbers | A strong; B retained final-decision wording and C interrogation wording despite guidance | Not run | Incomplete/needs focused work; no native approval | Further work stopped after Croatian placeholder corruption |
| Greek | LLaMAX3 Q4_K_M / EuroLLM Q4_K_M | Both fixture-A gates failed structure | LLaMAX3 omitted two typed placeholders; EuroLLM invented a placeholder for a time | Not run | Both pairings permanently disqualified; no tournament candidate remains | Keep Greek paused; do not tune structurally unsafe pairings |
| Greek | Gemma 4 E4B UD-Q4_K_XL | All generated pairs preserved structure | C questioning was fixed, but Polish/Cyrillic contamination persisted through three prompt rounds | Not run under one qualifying prompt | Paused by the three-consecutive-error rule; no native approval | Do not continue Gemma prompt tuning for Greek |
| Romanian | Qwen 9B structured | Final protocol: 8/8 pairs | 8/8 materially acceptable after three focused rounds; recurring grammar/typing defects recorded | Final A/B: 2/2 pass | User-selected/locked to Qwen 9B; no native approval | Keep selected model/adapter; seek catalog/native review |

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

## Bosnian tournament decision: 2026-09-09

LLaMAX3 Q4_K_M was the only eligible new candidate. Baseline A and two focused
revisions preserved placeholders but reversed the headline's grammatical roles,
making the S-Bahn the cause and the police intervention the delayed object. The
summaries also stated the reported injury as fact. Revision 2 corrected Croatian
`kolovoza` to Bosnian `augusta`, but not the material errors. Revision 3 explicitly
required stable active-clause roles and allegation wording; its title repeated
the identical reversal. The run was stopped before another summary generation
under the three-consecutive-error rule. Bosnian remains paused; no native approval
is claimed.

Greek has no structurally safe candidate from this tournament: LLaMAX3 omitted
typed placeholders and EuroLLM invented an unknown one. Both pairings remain
disqualified without prompt tuning, so the recorded Greek outcome is paused.

## Hindi tournament decision: 2026-09-09

LLaMAX3 and EuroLLM initially passed fixture A's structural checks. On B/C,
LLaMAX3 preserved structure and passed C's exact facts, negation, medical versus
police roles, causality, and `110`. It asserted B's damage allegation as fact and
weakened or omitted the presumption-of-innocence clause. EuroLLM then translated
its placeholder instruction and emitted the regex on B's title, permanently
disqualifying that pairing.

Three focused LLaMAX3 revisions improved isolated details (headline length,
female reference, and vehicle-window terminology), but all three continued to
state A's alleged injury as fact. Later revisions also made headlines incoherent,
and B's material legal clause remained missing. The final run was interrupted
after A and B's title when the early-stop rule was met. Hindi remains paused in
this tournament. The historical HY-MT2 result is still the strongest observed
Hindi candidate, with its recorded A allegation and F substance-uncertainty
failures; no native approval is claimed.

## Croatian tournament decision: 2026-09-09

LLaMAX3 Q4_K_M preserved placeholders on baseline A-C. It retained B's
allegation, involvement uncertainty, and presumption wording, but changed the
definite absence of a conviction into uncertainty about whether a conviction
existed. C preserved its events and numbers but changed approximate 04:20 into
an exact time. A continued to reverse headline agency and state an allegation
as fact.

The first focused revision then generated a 1,024-token B headline containing
invented bus-delay, enforcement, and culprit claims plus a repetition loop. A
shorter second revision generated another invented B headline about there being
no schedule delays. Both revisions were stopped before their remaining calls.
That meets the two-critical-regression early-stop rule, so LLaMAX3/Croatian is
paused. Historical Qwen remains the strongest observed Croatian candidate (A-E
passed in its best complete screen), but it remains unqualified because F still
failed materially. No native approval is claimed.

## Russian tournament decision: 2026-09-09

LLaMAX3 Q4_K_M and TowerInstruct Q6_K were the two structurally safe candidates.
LLaMAX3 preserved B's allegation but omitted the explicit no-conviction fact and
weakened the final-conviction boundary. Its C changed one doctor into multiple
doctors and invented that 03:30 was in the afternoon. TowerInstruct preserved
B's no-conviction and presumption meaning and passed C materially, making it the
leader despite losing allegation modality in A and B.

Three focused TowerInstruct revisions were tested on A/B. English guidance left
the allegations factual. Russian-language guidance changed one B output into
the misleading obligation `должен был`, while A remained factual. A final
Russian construction template still produced the factual injury claim, retained
the overly specific `рейд`, and invented that 03:30 was in the morning. The run
was stopped before the remaining B call under the three-consecutive-error rule.
One isolated Q6 prompt-cache OOM occurred between revisions; the exact request
was resumed successfully, so it did not trigger the two-failure quantization
fallback.

TowerInstruct/Russian remains paused, and its noncommercial license would also
block production selection without separate review. HY-MT2 remains the strongest
observed Russian candidate under the existing evidence, but is still paused for
its documented material failures. No native approval is claimed.

## New-model tournament completion and validation: 2026-09-09

Every officially eligible model-language pairing received a fixture-A gate or a
recorded hardware outcome. Structurally safe candidates advanced as specified;
focused work stopped under the three-consecutive-error or two-critical-regression
rules rather than consuming the maximum revision budget. No candidate qualified
a new language, no preferred model setting changed, and no failed route was
enabled. Locked-language selections were untouched.

The final non-Docker validation passed documentation links, whitespace and
formatting, module consistency, vet, Staticcheck v0.8.1, the complete race suite,
application build, govulncheck v1.7.0, dead-code analysis, Actionlint, reproducible
frontend build and committed-output comparison, npm audit with zero
vulnerabilities, strict Helm lint, default/example/multilingual renders, chart
assertions, and negative configuration cases. The current CI Repository checks
run also passes. ShellCheck and Gitleaks were unavailable locally; Docker was
intentionally skipped by request.

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

## Spanish HY-MT2 and Gemma 4 requalification: 2026-09-10

The newly authorized qualification used the frozen A-F pack, typed Gazetteer
protection, sequential native title/summary calls, disposable worker databases,
and exact-request checkpoints. No preferred production model or route state was
changed.

HY-MT2 Q5_K_M used two new general Spanish prompt revisions. The first added
the scheduled-bus distinction and fixed E. Its expansion preserved most facts
but translated `an der Ingolstädter Straße` as *cerca de Ingolstädter Straße*,
changing an on-street location into a nearby location. The second added the
final-conviction distinction and fixed focused B and E, but its expanded A
again used *cerca de*. HY-MT2 therefore did not qualify within its two-revision
share.

Gemma 4 UD-Q4_K_XL then passed the typed A/E structural gate. E preserved
`autobús de línea`, but baseline A rendered `zuständige Dienststelle` as a
service station. Gemma revision 1 fixed that with the general police-office
distinction; expansion then inferred a windshield from generic vehicle glass.
Gemma revision 2 fixed B with `cristal` and produced stable, materially sound
A/B repetitions. C and D preserved the fact checklists with minor wording
caveats. Final E, however, reduced inpatient admission to merely being taken to
a hospital:

| Final Gemma pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1, A/2 | Pass | Material facts stable; awkward inclusive-witness wording |
| B/1, B/2 | Pass | Allegation, generic glass, uncertainty, no conviction, and presumption of innocence preserved |
| C/1 | Pass | Facts preserved; `interrogó` is somewhat stronger than neutral questioning |
| D/1 | Pass | Material sequence preserved; awkward securing wording |
| E/1 | Pass | **Material failure:** `stationär` was omitted, leaving only transfer to hospital |

The run stopped after E because the four total new revisions were exhausted;
an already-started F title was durably recorded as interrupted work and was not
used to claim a completed pair. Spanish is **paused**. Gemma 4 is the best
observed candidate in this round, but no identity completed eight acceptable
pairs and native approval remains outstanding. All raw responses, partial runs,
databases, timings, hashes, and separate manual assessments remain in the local
checkpoints.

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

## French HY-MT2 requalification: 2026-09-10

Two newly authorized, general French prompt revisions were evaluated through the
production worker path. Revision 1 added the missing generic-glass distinction;
focused B then used `vitre` and preserved the allegation, uncertainty, explicit
absence of conviction, final-conviction condition, and presumption of innocence.
Its first expansion changed German `an der Ingolstädter Straße` to `près de`, so
the run stopped rather than mixing a materially changed location into the final
evidence.

Revision 2 clarified the general street-location relation. A then kept the event
on the street, and the complete A-F plus repeated A/B protocol ran under one
unchanged prompt, Q5_K_M digest, and settings:

| Pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1, A/2 | Pass | Allegation, approximate time, on-street location, examination, delay route/duration, unaffected U-Bahn, and witness request preserved; redundant nouns around protected names |
| B/1, B/2 | Pass | Generic `vitre`, allegation, uncertainty, no conviction, final-conviction condition, and presumption preserved |
| C/1 | Pass | Exact/approximate times, speed, closure cause, examination, one witness, negation, and `110` preserved |
| D/1 | Pass | Uncertain danger, response, securing, arrest/release, and continuing investigation preserved |
| E/1 | Pass | Scheduled bus, e-bike, serious injury, inpatient admission, female cyclist, and traffic-police investigation preserved; capitalization caveat |
| F/1 | Pass | Evasion, signals, red lights, collision, securing/arrest, and tentative licence/alcohol/drug indications preserved; awkward headline wording |

French is **acceptable on this sample** at 8/8 structurally valid and 8/8
materially acceptable pairs. Minor grammar and style issues remain within the
agreed threshold. Raw responses, restored outputs, request identities, timings,
worker databases, and separate assistant assessments remain in the durable local
checkpoint. This is not native approval or a guarantee for unseen incidents.

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

## Hindi baseline and interrupted first revision: 2026-09-09

The shared HY-MT2 prompt generated all six baseline A-C fields in about
**14m08s**. All three pairs passed deterministic validation and worker
persistence. C was materially acceptable. A stated the reported injury as an
established act despite police attribution. B changed alleged completed damage
into an attempted break and rendered final conviction as a formal sentence.
Baseline outcome: **3/3 structural, 1/3 materially acceptable**.

Focused round one added explicit Hindi allegation, damage-degree, and
final-conviction distinctions. Its A/B gate passed all three issues in four calls
lasting about **10m00s**. When expanded, C preserved its source facts but appended
two unrelated sentences derived from the new guidance. This material addition is
a model/prompt failure, not a validator failure. The next D request returned one
transport `EOF` before producing output, after which the Ollama endpoint stopped
responding. The interrupted D is not scored as a translation failure.

Focused round two is prepared and offline-tested. It shortens the same terminology
rules, makes them conditional on corresponding German wording, and explicitly
forbids emitting glossary rules or examples. No live revision-2 request has been
made. The exact durable resume point is **Hindi v3, fixture C title and summary**.
If C contains no guidance leakage, reassess A and B under the identical prompt;
then continue D-F and A/B repeats only if those gates pass. The endpoint must be
healthy first. No native review is claimed.

The endpoint recovered and the autonomous session continued. Revision two fixed
the C leakage and passed A-C, but D changed the neutral involved person to a
victim and definite danger, while E changed traffic police to transport police.
F passed. Revision three fixed those distinctions but changed a multi-family
building to a multi-storey building and a scheduled bus to an ordinary bus.

Revision four fixed both noun distinctions. Across its complete A-F pass, B-E
were materially acceptable; A again lost explicit allegation and F strengthened
tentative substance abnormalities into problems. This was the best observed
configuration: **6/6 structural and 4/6 materially acceptable**.

Revision five added target-language examples for A/F. A and F passed, but the
example contaminated B, D, and E: a vehicle window was “lightly injured,” an
established injury became alleged, and E gained an unsupported alleged-injury
sentence. Revision six replaced all examples with compact English semantic rules,
but A still lost allegation and B regressed to breakage/formal-sentence wording.
The following D request encountered the second session-level Ollama `EOF`; it is
recorded as infrastructure, not translation quality. No unchanged-prompt retry
was made.

All six user-authorized focused rounds are exhausted. No A/B repetitions were
run because no final prompt passed the six fixtures. Hindi is **paused**. Code
retains revision four, the best observed prompt, rather than the poorer fifth or
sixth attempt. The durable records preserve every attempt and two distinct Ollama
outages. Future Hindi work should compare another model or adapter rather than
continue growing this HY-MT2 prompt. This is assistant review only.

## Russian HY-MT2 decision: 2026-09-09

The shared-prompt A-C baseline passed structure but exposed three material
issues: A's headline changed a police operation to patrolling and used singular
train grammar; B broadened final conviction to a final decision; C changed
ordinary questioning to interrogation.

Six focused rounds were used. They successively corrected police-operation and
plural terminology, final-conviction and presumption wording, neutral
questioning, exact street relation, headline length, explicit allegation,
medical examination, witness count, neutral involved-person wording, inpatient
admission, and scheduled-bus terminology. The complete revision-four A-F screen
had A and F materially acceptable; B weakened “no conviction” to “no final
conviction,” C implied multiple witnesses, D called the neutral person a victim,
and E lost inpatient status. Revision five fixed C/D/E and the no-conviction
clause, but B's end condition regressed to any final verdict. Revision six fixed
that legal condition exactly.

The final B gate nevertheless rendered one suspect as “one of the suspects,”
implying additional suspects absent from the source. That is a material factual
addition. The final pair passed structure, placeholders, restoration, and worker
persistence, but failed assistant meaning review. No unchanged-prompt retry was
made.

Russian is **paused after six focused rounds**. A/B repeats were not run because
no single final prompt qualified all six fixtures. Code retains revision six,
which is legally safer than revision five, but the route should remain
unconfigured. Future work should compare another model/adapter rather than add
more HY-MT2 guidance. This is assistant review only.

## TranslateGemma Q3_K_S runtime gate: 2026-09-09

The remaining-language session started the 12B TranslateGemma candidate with
Bosnian fixture A. The installed artifact was
`hf.co/mradermacher/translategemma-12b-it-GGUF:Q3_K_S`, digest
`02e145b72bfccbc1ae6a0e0567cc96b6c2e2d0d88ab03bcb686aba9a6c3caa05`,
size 6,052,659,143 bytes, using the native adapter and effective 2048 context.

The title call completed in 2m23s, including an 81s load. It preserved both
typed placeholders, but the Bosnian was not acceptable: `Policijski interventsi`
is malformed and `kasni za` makes the police operation, rather than the trains,
late. The immediately following summary call returned `EOF` and Ollama unloaded
the model. Exact-identity resume replayed the completed title without generation,
but the summary returned another `EOF` after 2m34s and again left no loaded model.

Thus **2/3 new request attempts failed at transport/runtime level (66.7%)**, and
the only completed output materially failed. This meets the user-defined high
failure-rate stop condition and the roadmap rule to stop after repeated Ollama
restarts. Bosnian is hardware-blocked before a complete pair; prompt revisions
would not address the blocker. Greek and the Croatian TranslateGemma leg inherit
this candidate-level hardware blocker on the same host and are not repeatedly
loaded. Their quality remains unassessed, not failed. The durable checkpoint
preserves the completed response, both EOF attempts, exact requests, timings,
digest, fixtures, Gazetteer identity, and interrupted worker state.

The session continues with candidates that fit the host: Romanian and Croatian
through the Qwen 9B structured adapter. A smaller TranslateGemma artifact or
different hardware requires a separate decision.

## TranslateGemma fallback gates: 2026-09-09

Two smaller installed artifacts were tested through the unchanged production
TranslateGemma adapter, typed-placeholder protection, 2048 context, separate
title/summary calls, restoration, validation, and disposable queued-job path.
Each candidate used a fresh durable checkpoint and the same Bosnian fixture A.

`hf.co/mradermacher/translategemma-12b-it-GGUF:Q2_K` resolved to digest
`19bc732a76c240ceaf3f48b26a859399503389cf9fcc0a1c67279c3c0aaad1d6`
and size 5,362,565,063 bytes. Both calls completed without EOF: the title took
2m08s, including about 73s loading, and the summary took 3m00s. It therefore
fits this host better than Q3_K_S. Structural validation passed, but assistant
review failed the pair. The title changed a police operation into a patrol. The
summary contained malformed and mixed Serbian/Bosnian wording, unreliable
subject agreement in the alleged-injury clause, and an incorrect rendering of
the unaffected U-Bahn. This is a translation-quality failure, not a hardware
failure.

The requested fallback `translategemma:4b-it-q8_0` resolved to digest
`69729cbbfd3587b37aa4cc2b14cb0cca36b1edcc12add30fc43a3f9bc4be18ec`
and size 4,946,529,337 bytes. Its title and summary completed in 1m30s and 1m40s
respectively, again without a transport failure. The title reversed causality,
saying the police team was delayed because of S-Bahnen. The summary used
Serbian Cyrillic rather than the expected Bosnian Latin script, strengthened
the reported allegation into a direct assertion, and contained malformed
phrases. It also passed deterministic structure but failed meaning/readability.

This initial gate paused both fallback artifacts for Bosnian. That decision was
later superseded by the user-authorized six-round Q8 evaluation documented
below; the baseline remains historical evidence rather than the final Bosnian
decision. These Bosnian results did not establish Greek or Croatian quality;
those languages were evaluated independently. The test-only
readiness selector now permits an explicit TranslateGemma comparison model so
future artifacts retain exact model/digest/request identities without changing
production language settings.

## Greek TranslateGemma decision: 2026-09-09

Greek was evaluated independently after the Bosnian gate. Q2_K completed A-C
without runtime failure but was unusable: A strengthened the allegation and
mislabeled S-Bahnen as metro, B invented pending accusations and corrupted the
legal safeguards with non-words, and C contained malformed Greek. The candidate
was not expanded.

The 4B Q8 baseline completed A-C. B preserved the complete legal meaning and C
preserved the precision facts, but A inserted Arabic script and broke the
allegation/actor relationship. Six focused Greek-only guidance rounds then used
the same native adapter and exact request-identity rules:

| Round | Gate / result |
| --- | --- |
| 1 | A removed script leakage but still broke the allegation/actor relationship and broadened examination to treatment |
| 2 | A fixed allegation, roles, examination, and transit; witness noun became a non-word |
| 3 | A passed and repeated exactly; B passed and repeated exactly; C-F exposed one-witness, operational, scheduled-bus/traffic-police, and escape/arrest failures |
| 4 | C still lost one witness; D partly improved; E regressed inpatient care; F remained overlong and materially wrong |
| 5 | C passed; D retained most details but omitted scene securing; E retained inpatient care but lost route/police qualifiers; F changed Friday to Saturday and confirmed tentative facts |
| 6 | D passed with grammar caveats; E still lost scheduled-route and traffic-police qualifiers; F still claimed successful escape, was overlong, and did not reliably preserve the arrest/uncertainty details |

All calls completed without EOF or model unloading. Deterministic validation
correctly rejected overlong F titles, while editorial review independently
identified their semantic failures. The best results across attempts were A-D,
but **no single prompt identity passed A-F**. Because round 6 still failed E and
F, A-C and the repeats were not regenerated under that identity merely to add
calls after the decision was already negative.

Greek is **paused after six focused rounds**. The code retains the final
safety-oriented Greek guidance, but that is not route qualification. Every raw
response, result, and separate assistant review remains in its immutable local
checkpoint. This is assistant review only, not native approval.

## Croatian TranslateGemma comparison: 2026-09-09

Croatian was tested with both requested TranslateGemma fallbacks in addition to
the previously completed Qwen 9B structured leg. Q2_K completed A-C without
runtime failures but produced **0/3 acceptable pairs**. Its output contained
non-words, malformed or alternative text, corrupted legal meaning, an invented
loss-of-control detail, and unreliable Croatian. It was not prompt-tuned.

The 4B Q8 baseline was materially better: C preserved the factual checklist
despite grammar errors, while A and B strengthened allegations into facts. Six
focused rounds then tested short English guidance, direct phrase mappings, and
Croatian-language guidance:

| Round | Gate / result |
| --- | --- |
| 1 | B preserved the allegation but omitted the explicit no-conviction fact; A remained overlong and lost the allegation |
| 2 | A title and B legal distinction improved, but both allegations were still lost |
| 3 | B allegation improved but no-conviction fact was again omitted; A ignored a literal mandatory `navodno` rule |
| 4 | Croatian-language guidance made A materially acceptable with grammar errors; B still omitted no conviction |
| 5 | B finally passed all legal facts; expansion regressed A and exposed C-F grammar, terminology, inpatient, arrest, and signal-detail failures |
| 6 | A title and E inpatient detail improved, but A again lost allegation; D/F retained Serbian `uhapšen`; E lost route/traffic qualifiers; F singularized red traffic lights and weakened securing |

All Q2_K and 4B Q8 calls completed without EOF or unloading. The failures are
quality and instruction-following issues, not hardware. **No single 4B Q8
prompt identity passed A-F**, so A/B repeats were not run after the final
negative gate. The final Croatian safety guidance remains in code but does not
qualify the route.

Compared with TranslateGemma, the Qwen 9B structured candidate remains the
stronger observed Croatian model because one Qwen identity passed A-E and failed
only F. However, Qwen also exhausted six rounds without a complete qualifying
prompt. Croatian is therefore **paused on both model tracks**. This comparison
is assistant review only, not native approval; all raw attempts and separate
reviews remain in durable local checkpoints.

## Romanian Qwen 9B final protocol: 2026-09-09

Romanian used the production structured adapter with
`hf.co/bartowski/Qwen_Qwen3.5-9B-GGUF:Q3_K_M`, digest
`14349d99100f6a9ea1082670dc15fddc65cabe888161ad35cf59d6c83d89fcf9`,
and context 8192. Each request returned title and summary together as JSON.

The baseline A-C screen passed deterministic validation. A and B were materially
acceptable, while C changed German `an der … Straße` from on/at the street to
near the street. Three focused rounds were used:

1. General Romanian street-location guidance fixed C. The complete A-F screen
   then exposed `Kriminalpolizei` as forensic police in D.
2. A criminal-investigation-police distinction fixed D. Reassessment exposed a
   singular `S-Bahnen` in A and a missing closing `__` on F's title placeholder;
   restoration rejected F before persistence.
3. A plural-transit rule and explicit closing-underscore check fixed both A and
   F. The final prompt then completed the full protocol.

| Pair | Structural | Assistant meaning/readability review |
| --- | --- | --- |
| A/1 | Pass | Allegation, examination, plural S-Bahnen, delay route/duration, unaffected U-Bahn, and witness request preserved; recurring `biroulul` typo |
| B/1 | Pass | Allegation, unresolved involvement, generic vehicle glass, no conviction, final-conviction condition, and presumption of innocence preserved |
| C/1 | Pass | On-street relation, exact/approximate times, speed, closure cause, examination, one witness, negation, and `110` preserved |
| D/1 | Pass | Unclear danger, neutral involved person, response, arrest/release, and Munich judicial-police investigation preserved |
| E/1 | Pass | Scheduled bus/e-bike, serious injury, inpatient admission, and traffic-police investigation preserved; article/gender errors remain |
| F/1 | Pass | Exact placeholders, evasion/collision/arrest sequence, and tentative licence/alcohol/drug indications preserved |
| A/2 | Pass | Stable material repeat of A/1 with the same minor typo |
| B/2 | Pass | Stable legal-meaning repeat of B/1 |

The final identity contains **8/8 completed structured requests**, **8/8
structurally valid pairs**, and **8/8 materially acceptable pairs** on assistant
review. Model-reported durations total about **28m29s**. A zero-new-call replay
restored, validated, and persisted all eight pairs through the queued production
path. There were no transport or memory failures in the final identity.

Romanian is **acceptable on this sample** under the agreed threshold. Its output
is understandable but has recurring grammar and typing defects, so this is not
native approval or a general quality guarantee. All earlier prompt identities,
including the rejected malformed placeholder, remain in separate durable
checkpoints.

The subsequent Croatian Qwen and TranslateGemma comparisons are recorded below.

## Croatian Qwen 9B decision: 2026-09-09

Croatian used the same Qwen 9B digest, structured adapter, and 8192 context as
Romanian. The baseline A-C calls all completed. A lost the reported-allegation
status, changed the subsequent police operation into police endangerment, and
singularized S-Bahnen. B called the neutral suspect a perpetrator and changed
one parked vehicle to multiple vehicles. C preserved all material facts, but
deterministic validation falsely treated leading `29. kolovoza 2026.` as an
ordered Markdown list.

The validator now distinguishes a localized day-month-year prefix from an
ordered list. Regression tests accept Croatian and German leading dates while
still rejecting genuine numbered Markdown items. The saved C output was
therefore classified as materially acceptable rather than prompting the model
to work around a validator defect.

Six focused Croatian prompt rounds were used:

| Round | Gate / result |
| --- | --- |
| 1 | A fixed allegation and police-operation meaning but retained singular S-Bahnen; B fixed neutral suspect and one-vehicle meaning with poor case agreement |
| 2 | A fixed plural agreement; the complete A-E set was materially acceptable, but F truncated its municipality placeholder and produced garbled Serbian-heavy prose |
| 3 | F preserved the placeholder but remained malformed and predominantly Serbian |
| 4 | F improved some Croatian vocabulary but corrupted the municipality placeholder again and remained grammatically poor |
| 5 | A shorter contract preserved F's placeholders, facts, sequence, and uncertainty, but the output was still predominantly Serbian and not acceptable Croatian |
| 6 | Croatian-language guidance produced more Croatian vocabulary, but F lost the driver subject, omitted the securing step, and strengthened tentative observations into alcoholization and signs of drug use |

The one immediate Qwen `EOF` before round 1 was preserved as infrastructure;
exact-identity resume completed the request and later calls remained stable. It
is not counted as a translation failure. Every generated attempt remains in a
separate checkpoint. No unchanged-prompt retry was used to obtain a favorable
sample.

Croatian/Qwen is **paused after six focused rounds**. No single prompt qualified
A-F, so A/B repetitions were not run. The code retains round 5's compact English
guidance because it produced the safest observed F semantics and exact tokens;
the later Croatian-language round improved vocabulary but materially changed
facts. Retaining it does not qualify the route. The later Q2_K and 4B Q8
TranslateGemma comparison also ended paused after six Q8 rounds. This is
assistant review only, not native approval.

## Bosnian TranslateGemma final decision: 2026-09-09

After the Q2_K and 4B Q8 fixture-A baseline, Bosnian Q8 received six bounded,
evidence-led prompt rounds. One round-3 request ended with `EOF` while Ollama had
no loaded model; exact-identity resume completed the case, and later calls were
stable. The interruption is recorded separately from model quality.

Rounds 1 and 2 failed fixture A by omitting the allegation and producing a
malformed unaffected-U-Bahn clause. Round 3 made A materially usable and
preserved B's legal facts, though both retained grammar and Serbian-form
caveats. C exposed two deterministic validator defects and one translation
issue: Bosnian `avgust` was missing from localized-month recognition, a leading
localized date followed by a comma was mistaken for an ordered Markdown list,
and the model initially dropped exact-time precision. The two validator defects
now have regression tests; fresh queued-path runs verified their fixes, and the
next prompt restored `tačno u 03:30`.

Expansion then showed persistent model problems:

| Round | Gate / result |
| --- | --- |
| 4 | D lost de-escalation/arrest details; E omitted its place token and inpatient/traffic-police qualifiers; F changed signals, securing, and tentative substance observations |
| 5 | D/F translated the English placeholder type itself; E again dropped its place token and qualifiers |
| 6 | E/F were structurally valid; D's mixed Cyrillic/Latin was caught after adding the missing Latin-target script check. E still omitted scheduled-bus, inpatient, and traffic-police details; F still changed plural/control details and implied substance use |

Bosnian is **paused after six focused Q8 rounds**. No single prompt qualified
A-F, so the A/B repetitions were not run. The final concise guidance is retained
as the safest production starting point, not as route qualification. Q2_K and
4B Q8 both fit the host; this is a translation/instruction-following limitation,
not a memory blocker. Assistant review only; no native approval.

This completes every runnable candidate in the authorized remaining-language
session. Bosnian, Greek, Croatian/Qwen, and Croatian/TranslateGemma remain
paused for another model or native-guided evaluation rather than further prompt
growth on the current candidates.

## References

- [Tencent HY-MT2 contract](https://huggingface.co/tencent/Hy-MT2-7B)
- [Google TranslateGemma contract](https://huggingface.co/google/translategemma-12b-it)
- [Q3_K_S artifact](https://huggingface.co/mradermacher/translategemma-12b-it-GGUF)
