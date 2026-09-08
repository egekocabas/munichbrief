# HY-MT2 quantization comparison

This record preserves the completed Q5_K_M release-readiness work and the
separate Q6_K experiment requested on 2026-09-08. The detailed chronological
evidence remains in [Pre-merge translation testing](pre-merge-translation-testing.md).
Raw requests, outputs, timings, isolated databases, manifests, and manual review
JSON remain in ignored `.local/translation-evaluations/` directories.

## Frozen Q5_K_M position

Model: `hf.co/mradermacher/Hy-MT2-7B-GGUF:Q5_K_M`  
Digest: `24acc0f002f8c874f34e8b3e22da236405f7c3a47ae9d62d3744cf3c5b4bd693`  
Adapter/context: `hy-mt2` / 8192

| Language | Coverage at the stopping point | Assistant decision | Important limitations |
| --- | --- | --- | --- |
| English | Final protocol 8/8 | Acceptable on this sample | Minor A/B wording caveats; no native approval |
| Spanish | A-F plus focused B/E | Paused, 5/6 acceptable | Final E changes a bus into a route; no repetitions |
| French | Historical 8/8 plus final B/2 | Paused, 7/8 acceptable | Final B invents windshield specificity |
| Italian | A-F plus focused E/F | Paused | Final F title remains grammatically invalid; no repetitions |
| Polish | Final protocol 8/8 | Acceptable on this sample | Recurring grammar/spelling defects; no native approval |
| Turkish | Final protocol 8/8 | Acceptable on this sample | User-approved time phrasing plus precision/grammar caveats; no native approval |
| Ukrainian | Final protocol 8/8 | Acceptable on this sample | Two explicit user-approved extra rounds fixed allegation, neutral-role, and evidence-strength failures; no native approval |
| Chinese, Hindi, Russian | Not run in the readiness protocol | Pending | Older screens are historical evidence only |

At the time of this comparison, the exact Q5_K_M resume point was **Ukrainian
focused work**, followed by Chinese, Hindi, and Russian. Ukrainian subsequently
completed two explicit user-approved extra rounds and is acceptable on the
final eight-pair sample; the current exact resume point is **Chinese A-C**, then
Hindi and Russian.
The saved Q5 checkpoints must not be reused for another model or changed prompt.

## Operating procedure preserved

For one language, run A-C repetition 1, then D-F repetition 1, then A/B
repetition 2. Title and summary are separate sequential native calls: eight
pairs and 16 calls per language. Every response is durably captured before
production validation and is then exercised through typed Gazetteer protection,
restoration, the queued translation worker, persistence, and model/adapter
provenance checks.

Inspect every output against its frozen source-fact checklist even when the
structural validator passes or rejects it. Record semantic/readability review in
the separate `*-review.json`; never overwrite an earlier completed attempt or
retry merely to obtain a pass. A zero-new-call replay must cover every completed
pair. Record transport/runtime failures separately from translation failures.

The manifest pins the fully rendered requests through the code-content identity,
model digest, settings, fixture content, and Gazetteer generation/content. Use a
new directory when any pinned identity changes. The exact commands and environment
variables are documented in [Development](development.md).

## Q6_K experiment

Model: `hf.co/mradermacher/Hy-MT2-7B-GGUF:Q6_K`  
Adapter/context: `hy-mt2` / 8192  
Checkpoint: `.local/translation-evaluations/readiness-hymt2-q6k-v1`

Run the full eight-pair protocol with the current branch prompts for English,
Spanish, French, Italian, Polish, and Turkish. Continue after structural or
meaning failures and retain them as evidence. Resume isolated transport failures.
Stop the overall experiment only when runtime evidence indicates a memory-fit
problem near the agreed threshold: roughly 60–70% of calls fail for resource
reasons. An isolated failure every three or four requests is resumable and does
not stop the experiment.

The Q5 history used prompts as they evolved and some paused languages never ran
the final prompt across all eight pairs. Therefore the final comparison must
show per-pair results but must not claim a controlled quantization-only result
where prompt identity or coverage differs.

| Language | Q6 structural | Q6 meaning/readability | Repeats | Runtime | Result |
| --- | --- | --- | --- | --- | --- |
| English | 8/8 | 5/8 acceptable | A pass; B repeats legal failure | 16m39s; 16/16 calls | Q5 remains preferred |
| Spanish | 8/8 | 4/8 acceptable | A/B both repeat failures | 19m29s; 16/16 calls | Q5 remains preferred |
| French | 8/8 | 4/8 acceptable | A/B both repeat failures | 20m13s; 16/16 calls | Q5 remains preferred |
| Italian | 8/8 | 4/8 acceptable | A fails; B passes | 22m20s; 16/16 calls | Q6 does not resolve Q5 weaknesses |
| Polish | 8/8 | 7/8 acceptable | A/B both pass | 23m28s; 16/16 calls plus one recovered EOF | Q5 remains preferred |
| Turkish | 8/8 | 8/8 acceptable | A/B both pass | 25m54s; 16/16 calls | Tie on score; Q5 remains operationally safer |

After all six languages (or a documented memory stop), compare Q5 and Q6 by
pair: structural validity, material meaning, readability, repeat stability,
failure class, and elapsed time. Neither assistant review nor this small fixture
pack is native approval or a general quality guarantee.

### Q6_K per-pair result

`Pass` means materially acceptable under the roadmap threshold; it may still
contain the documented grammar/style defects. All 48 pairs passed structural,
placeholder, restoration, validator, queued-worker persistence, and provenance
checks.

| Language | A/1 | B/1 | C/1 | D/1 | E/1 | F/1 | A/2 | B/2 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| English | Pass | Fail | Pass | Pass | Pass | Fail | Pass | Fail |
| Spanish | Fail | Fail | Pass | Pass | Pass | Pass | Fail | Fail |
| French | Fail | Fail | Pass | Pass | Pass | Pass | Fail | Fail |
| Italian | Fail | Pass | Pass | Pass | Fail | Fail | Fail | Pass |
| Polish | Pass | Pass | Pass | Pass | Fail | Pass | Pass | Pass |
| Turkish | Pass | Pass | Pass | Pass | Pass | Pass | Pass | Pass |

Material failures observed:

- English B twice broadens final conviction to final judgment; F changes an
  absent licence into an invalid licence.
- Spanish A twice moves the event to near the street and changes contact into
  appearing at the station; B twice broadens final conviction to final judgment.
- French A twice changes contact into appearing at the station and invents
  plural doctors; B twice changes generic window damage into windshield breakage.
- Italian A twice changes the location/witness action and expands witness
  categories; E invents intensive care and loses inpatient admission; F has the
  invalid published-title construction `L’autista ... fuga`.
- Polish E changes the scheduled bus into `autobus linowy` and inpatient
  admission into transport for observation.
- Turkish has no material failure under the user's accepted time-phrase
  threshold. Q6 improves the A title agreement and D time phrasing, but still
  weakens slight injury to slight harm and retains several grammar/style issues.

Q6 totals **32/48 materially acceptable pairs** and **48/48 structurally valid
pairs**. Every intended title/summary request completed: 96/96. One additional
Polish A-title attempt ended in EOF immediately after a language switch; exact
checkpoint resume reloaded the model and completed that field. Thus one of 97
attempts (about 1.0%) had a transport failure, far below the agreed resource-stop
threshold. Zero-new-call replay succeeded for all six languages and all 48 pairs.

Generation wall time was about **2h08m04s** in aggregate, excluding near-instant
replays. The installed artifact is 6,164,484,438 bytes, versus 5,371,236,694
bytes for Q5_K_M. After the final call Ollama reported a 7,491,289,087-byte
CPU-loaded footprint at context 8192 (`size_vram: 0`). The host reported
8,317,272,064 bytes total RAM, only 169,623,552 bytes available, and no swap.
Q6 therefore fits this isolated test, but leaves too little headroom to call it
operationally safe alongside normal workloads; the recovered EOF is consistent
with that pressure even though it does not prove an out-of-memory kill.

### Q5_K_M versus Q6_K

| Language | Frozen Q5 evidence | Q6 current-prompt evidence | Comparison |
| --- | --- | --- | --- |
| English | 8/8 acceptable | 5/8 acceptable | Q5 clearly stronger on this pack |
| Spanish | 5/6 acceptable; no repeats | 4/8 acceptable; 4/6 first pass | Q5 stronger, with coverage/prompt caveat |
| French | 7/8 acceptable across evolving prompts | 4/8 acceptable | Q5 stronger; Q6 repeats B and adds stable A failure |
| Italian | Paused mixed-prompt evidence; final F title failed | 4/8 acceptable; F title still fails | No Q6 improvement; exact score is not controlled |
| Polish | 8/8 acceptable | 7/8 acceptable | Q5 stronger; Q6 adds a material E failure |
| Turkish | 8/8 acceptable | 8/8 acceptable | Score tie; Q6 has some nicer phrasing but much less RAM headroom |

The evidence does **not** support replacing Q5_K_M with Q6_K for these languages.
Q6 produces more material errors in five of six comparisons, offers no score
gain for Turkish, runs with critically low memory headroom, and is generally
slower in this run. Ukrainian subsequently completed its final Q5 protocol and
is acceptable on that sample; keep the Q5 resume point at Chinese A-C. If Q6 is
revisited, use this checkpoint only for exact replay; changed prompts or model
digests need a new directory.
