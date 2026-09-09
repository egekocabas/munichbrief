# Translation model evaluation

Current roadmap: [Translation readiness](pre-merge-translation-testing.md).
The dated experiments below are historical evidence, not current approvals.
Their future-work recommendations are superseded by that roadmap. Production
uses typed Gazetteer protection and durable per-language model/adapter settings.
Stored model output is plain text; older Markdown experiments do not describe
the current contract. The former Croatian/Bosnian/Greek Q3_K_S recommendation
is superseded: that artifact was hardware-blocked, while Q2_K and 4B Q8 loaded
successfully but failed the Bosnian fixture-A meaning/readability gate. See the
roadmap for exact evidence and current per-language status.

MunichBrief treats structural safety, semantic fidelity, and target-language
quality as separate acceptance dimensions. A structurally valid model response
is not evidence that its translation is accurate or publishable.

## TranslateGemma request contract

Google's TranslateGemma model card defines a specialized user message whose
content contains exactly one text item with `source_lang_code`,
`target_lang_code`, and `text`. Those fields use language codes such as `de`,
`tr`, and `uk`, not MunichBrief's regional website tags. The official chat
template expands that item to a user-only instruction naming both languages and
codes, followed by the source text after two blank lines. It asks for only the
target-language translation. The supported text input context is 2K tokens.

The GGUF models inspected through Ollama expose a generic Gemma 3 string-message
template. Ollama's `/api/chat` string content cannot carry the structured
language-code properties consumed by Google's original template. The
`TranslateGemmaNativeAdapter` therefore expands Google's official
plain-text instruction in application code, sends one protected field per
request, requests plain output without a JSON schema, disables sampling, and
leaves restoration and validation to application code.

The adapter is an explicit production routing option, but it is never selected
automatically. Operators choose it per language only after controlled
evaluation demonstrates acceptable fidelity and language quality. Unconfigured
upgrades retain structured-chat behavior.

Primary references:

- Google TranslateGemma 12B model card:
  <https://huggingface.co/google/translategemma-12b-it>
- TranslateGemma technical report:
  <https://arxiv.org/abs/2601.09012>
- Google Gemma prompt formatting:
  <https://ai.google.dev/gemma/docs/core/prompt-structure>

To inspect the exact local GGUF template without generating text, use Ollama's
`POST /api/show` endpoint with the installed model identity. Record the model
identity, parameter size, quantization, and template in evaluation notes; do
not assume that a third-party GGUF retained Google's structured chat template.

## Hy-MT2 request contract

Tencent's Hy-MT2 instructions require full language names in English prompts,
not BCP-47 tags or bare language codes. The `HyMT2NativeAdapter`
therefore maps MunichBrief's canonical German source and each supported reader
language to Tencent's exact English label. This matters for Chinese: the website
registry calls it `Simplified Chinese`, while Hy-MT2's supported-language table
calls `zh` `Chinese`.

The adapter sends one user message with no system prompt or JSON schema. Its
task, strict-rules, and source-data sections follow Tencent's structured-data
instruction pattern. The rules treat MunichBrief's exact placeholder pattern as
immutable, explain typed entity metadata, require grammar to be restructured
around protected entities, and explicitly preserve facts, negation, uncertainty,
attribution, and relationships. The protected text begins on the line immediately
after the source-data heading. The adapter applies Tencent's recommended
1.8B/7B settings through their Ollama equivalents:
`temperature=0.7`, `top_p=0.6`, `top_k=20`, `repeat_penalty=1.05`, and
`num_predict=4096`. The normal Ollama pipeline retains its existing settings.

Of MunichBrief's registered translated languages, Hy-MT2 officially supports
English, Turkish, Italian, Ukrainian, Chinese, Hindi, Spanish, French, Polish,
and Russian. It does not list Croatian, Bosnian, Greek, or Romanian; production
routing rejects those targets instead of attempting an undocumented fallback.
The adapter is available as an explicit per-language choice but has no automatic
default and still requires live and native quality review.

Primary references:

- Tencent Hy-MT2 7B model card (prompt forms, inference settings, languages):
  <https://huggingface.co/tencent/Hy-MT2-7B>
- Tencent Hy-MT2 source repository:
  <https://github.com/Tencent-Hunyuan/Hy-MT2>
- Ollama request option definitions:
  <https://github.com/ollama/ollama/blob/main/api/types.go>

## Seed-X request contract

ByteDance's Seed-X-Instruct model has no chat template and should not receive a
multi-turn conversation. Its target-language tag is mandatory at the absolute
end of the prompt. The opt-in `SeedXNativeAdapter` therefore calls
Ollama's `/api/generate` endpoint with `raw=true`, no system prompt or schema,
and a prompt ending in the exact official tag, such as `<uk>` or `<zh>`. It
uses ByteDance's minimal documented instruction and single-newline boundary
without adding a custom placeholder preamble. The protected source text and
target tag remain on the final line, with the tag as the absolute final prompt
content. Placeholder preservation is enforced after generation. Migration 017
makes the adapter selectable per supported language, but no language should be
routed to a Seed-X artifact until that exact artifact has passed the readiness
protocol.

ByteDance recommends beam search with width four and a maximum of 512 output
tokens. Ollama does not expose beam width through its documented runtime
options, so the adapter uses ByteDance's documented greedy alternative with
`temperature=0` and `num_predict=512`. It caps the effective context at 4096
tokens, matching the released model's documented sliding window even when the
shared provider is configured with a larger context.

Of MunichBrief's translated reader languages, Seed-X officially supports
English, Turkish, Croatian, Italian, Ukrainian, Chinese, Spanish, French,
Romanian, Polish, and Russian. It does not list Bosnian, Hindi, or Greek; the
adapter rejects those targets. ByteDance recommends against unofficial
quantizations, so the downloaded third-party Q5_K_M file must remain an
evaluation candidate rather than a production default.

Primary references:

- ByteDance Seed-X-Instruct 7B model card (language tags, raw prompting,
  decoding, and supported languages):
  <https://huggingface.co/ByteDance-Seed/Seed-X-Instruct-7B>
- ByteDance Seed-X source repository:
  <https://github.com/ByteDance-Seed/Seed-X-7B>
- Ollama raw generation API:
  <https://docs.ollama.com/api/generate>
- Evaluated third-party Q5_K_M artifact:
  <https://huggingface.co/mradermacher/Seed-X-Instruct-7B-GGUF>

## Deterministic acceptance

Before persistence, a translation must preserve:

- the exact protected-place token multiset and occurrence count in each output
  field, while allowing target-language word order;
- plain-text output without Markdown or URLs;
- numeric facts, with leading-zero and textual-month/numeric-month equivalence
  handled without accepting changed phone, speed, date, time, or unit values;
- valid Unicode, NFC normalization, field limits, and the expected target
  script for non-Latin targets.

Deterministic acceptance catches corruption but does not approve meaning or
grammar. Every model evaluation must retain raw output and separately note
omissions, additions, altered causality, attribution, uncertainty, negation,
police terminology, and presumption-of-innocence wording.

## Bounded comparison protocol

Compare candidates sequentially per model, using identical protected inputs and
the decoding settings prescribed by each model when available. Deterministic
settings remain preferable when the model documents them; otherwise repeat the
same case to measure sampling stability. Start with difficult fixtures spanning
repeated and reordered places, dates and times, emergency numbers and
measurements, non-Latin scripts, attribution, uncertainty, and legal framing.
Only expand to the complete language matrix when a candidate
improves editorial quality rather than merely its mechanical pass count.

Checkpoint after every request. A transport interruption must stop the run and
resume from the checkpoint rather than recording a model failure or repeating
completed requests. Live output is evaluation evidence and must not be committed
as reader content.

## HY-MT2 and Seed-X critical screen

The opt-in `TestLiveNativeTranslationAdapterScreen` defines a 72-field-call
comparison for the two native adapters. Each adapter must first pass
`TestLiveNativeTranslationAdapterSmoke`; a failed adapter must be excluded
instead of spending the larger call budget. The three fixtures isolate transit and repeated
typed-place relationships; negation, attribution, uncertainty, and legal
framing; and numbers, dates, causality, public-assistance wording, and plain-text
output. Title and summary are separate calls, as they are in the application.

HY-MT2 covers the English control plus Ukrainian, Hindi, and Russian with two
repetitions at Tencent's sampling settings (48 field calls). Seed-X covers the
English control plus Croatian, Romanian, and Ukrainian with one greedy run (24
field calls). Bosnian and Greek are absent because neither model officially
supports them.

The run manifest pins requested model names and resolved Ollama digests, adapter
and prompt versions, generation settings, fixture content and hashes, languages,
and repetition counts. An append-only event file is synced after every field;
the snapshot can be reconstructed from it, and completed fields from HY-MT2 and
Seed-X have adapter-qualified keys so they cannot overwrite one another. Each
result records individual mechanical checks and leaves semantic relationships,
transit terminology, negation and uncertainty, attribution and legal framing,
additions and omissions, and grammar explicitly pending for human review.

This screen is test-only. Production uses the same typed-placeholder boundary,
but the screen does not choose per-language models or alter settings, jobs,
routes, persistence, or deployment.

### Adapter smoke gate on 2026-09-03

The production-shaped English control translated title and summary separately
through each Q5_K_M adapter. HY-MT2 preserved every typed placeholder, number,
Markdown decoration, URL, attribution, uncertainty marker, and negation. Its
restored output passed all deterministic checks and retained the source meaning,
so HY-MT2 is eligible for its 48-field-call screen.

Seed-X-Instruct corrupted typed placeholders in both prompt variants tested. A
custom preservation instruction changed letters, digits, underscores, or all
three; a literal placeholder example caused whitespace-only incomplete output.
ByteDance's minimal documented prompt completed, but changed
`__MB_COMMUTER_TRAIN_0001__` to `__MB_COMMUTE_TRAIN_0001__`, omitted the
district token from the summary, and changed important semantic relationships.
Seed-X therefore failed the prerequisite and must be excluded from the larger
screen for this unofficial GGUF. The adapter retains the official minimal prompt
so future artifacts can be reevaluated without treating a harmful custom prompt
as part of the model contract.

### Seed-X Russian and Croatian gate on 2026-09-09

The third-party Q5_K_M artifact was reevaluated through the production worker,
with separate title and summary calls, typed placeholders, durable raw-response
capture, restoration, validation, and persistence. Russian was tried with a
general strict instruction, a shorter general instruction, and finally the
exact official minimal prompt. Croatian was then gated with the official minimal
prompt. The artifact returned whitespace-only incomplete responses, wrong-script
text, unrelated text, or token loops; none of the attempted pairs was usable.

Two placeholder-free controls using the exact official form—`Die Polizei
ermittelt. <ru>` and `Die Polizei ermittelt. <hr>`—also returned the wrong script
or whitespace instead of Russian or Croatian. Ollama kept the approximately
6 GB model resident and completed generations, so this is not evidence of an
out-of-memory failure. It is a base artifact/runtime compatibility failure that
precedes prompt following or placeholder preservation. Testing stopped early
instead of spending all six permitted prompt revisions on a model that could not
perform the minimal translation prerequisite.

For these targets the evaluated Seed-X artifact is materially worse than the
previous candidates: HY-MT2 at least generated Russian and preserved structure,
while Qwen 9B produced substantially usable Croatian through fixture E. Both
languages remain paused, and this Seed-X artifact must not be configured. The
adapter remains available for reevaluating an official or otherwise verified
artifact without another database migration.

## SalamandraTA v3 request contract and Q5 gate

SalamandraTA-7b-instruct v3 officially supports German source translation into
Russian, Croatian, Greek, and Hindi. It uses a user-only ChatML conversation and
the fixed English general-translation form `Translate the following text from
{source} into {target}`, followed by labelled source text and the target label.
The `SalamandraTANativeAdapter` keeps that trained shape, adds one concise typed-
placeholder preservation rule, uses greedy decoding, and caps context at the
official 8192-token limit. Title and summary remain separate sequential calls.

Version 3 adds terminology-aware and structured-text translation capabilities.
Language-specific guidance is therefore kept as short, evidenced terminology
requirements rather than replacing the base task with a long shared instruction.
Migration 018 makes the adapter selectable for officially supported reader
languages without changing existing selections. Each exact artifact must still
qualify independently before configuration.

The evaluated Q5_K_M artifact loaded successfully and did not show a repeated
memory-failure pattern, so Q4 fallback was unnecessary. Russian preserved every
typed placeholder across the base and six focused rounds, but no one prompt
preserved both allegation framing and the headline's operation/delay relation.
Croatian produced relevant output through A-D, then corrupted fixture E's
`__MB_TRANSIT_0001__` into the literal regex-shaped
`__MB_[A-Z_]+_[0-9]{4}__`. Testing stopped immediately under the placeholder
hard gate; E summary, F, repeats, further Greek work, and Hindi tuning were not
run. Greek A-C and Hindi A preserved placeholders, but their quality decisions
remain incomplete. No SalamandraTA route is qualified by this experiment.

Primary references:

- BSC SalamandraTA v3 model card (release, supported languages and tasks):
  <https://huggingface.co/BSC-LT/salamandraTA-7b-instruct>
- Official usage guide (ChatML, prompts, decoding and context):
  <https://huggingface.co/BSC-LT/salamandraTA-7b-instruct/blob/main/usage_guide.md>

### HY-MT2 screen on 2026-09-03

The HY-MT2-only screen completed all 48 field calls in about 52 minutes: English,
Ukrainian, Hindi, and Russian across three fixtures, two repetitions, and
separate title/summary requests. Every typed placeholder, number, Markdown
decoration, URL, and target script passed its dedicated check. The original
summary reported English 6/6, Hindi 6/6, Ukrainian 4/6, and Russian 4/6.

The four apparent failures were deterministic false positives, not model token
failures. Russian and Ukrainian naturally placed the date immediately after
`Ganghoferstraße`; the address detector interpreted day `29` as a house number.
The detector now exempts only complete localized calendar forms with a valid day,
recognized month, and four-digit year (plus year-first Chinese dates). It still
rejects bare numbers, alphanumeric house numbers, invalid day numbers, and bare
years after street names. A fresh eight-call targeted rerun of the affected
fixture passed Ukrainian 2/2 and Russian 2/2, making the adjusted structural
result 24/24 cases.

Structural success did not imply editorial approval. Manual inspection found a
stable English plural-agreement error and one examination/questioning error;
Ukrainian transit plurality and direction problems plus weakened legal terms;
Hindi agreement, legal-meaning, and examination/questioning errors; and Russian
transit grammar, weakened legal terms, altered exact times, and
examination/interrogation errors. Sampling sometimes improved a repetition, but
the observed legal and event-role changes prevent automatic publication in the
tested target languages without further model or prompt work.

### HY-MT2 supported-language summary screen on 2026-09-03

A summary-only screening run covered the ten MunichBrief targets officially
supported by HY-MT2: English, Turkish, Italian, Ukrainian, Chinese, Hindi,
Spanish, French, Polish, and Russian. All ten saved responses preserved their
typed placeholders, numbers, and target scripts. Spanish was the strongest
editorial result; Ukrainian, Chinese, Hindi, and Russian retained grammatical
or semantic problems requiring targeted evaluation.

The first screening fixture included synthetic Markdown that reader summaries
do not use. Its link-related observations are therefore excluded from the
editorial verdict. The reusable fixture and checkpoint directory now use plain
text and explicitly reject Markdown and URLs. Its changed configuration cannot
reuse the earlier generated responses, and no replacement live calls were made
as part of this correction.

## Bounded comparison on 2026-09-02/03

The native adapter was run against the same three German fixtures in Turkish,
Simplified Chinese, and Ukrainian. The fixtures covered numeric facts and an
emergency number, legal uncertainty, and protected place names with transit,
Markdown, and a URL. The JSON checkpoint and raw output remained in `/tmp` and
were not committed.

| Model | Mechanical passes | Total generation time |
| --- | ---: | ---: |
| `translategemma:4b` (Q4_K_M) | 3/9 | 7m 56s |
| `translategemma:4b-it-q8_0` (Q8_0) | 4/9 | 12m 32s |
| `translategemma-12b-it-i1` (IQ3_M) | 7/9 | 53m 37s |

The Q8 4B model improved the Q4 mechanical score by only one case. The IQ3 12B
model produced noticeably better legal-uncertainty prose and generally more
coherent translations, but it repeatedly omitted `110` in Ukrainian and both
transit tokens in Chinese. Manual inspection also found facts that mechanical
validation cannot detect: the 4B models invented warnings, treated 93 km/h as
the amount over the limit, strengthened allegations, changed a drone into a
helicopter, or described S-Bahn and U-Bahn services as vehicle brands.

All three 12B Turkish results passed mechanical validation, but the transit case
reordered intact tokens into the wrong semantic roles. This demonstrates that a
per-field token multiset permits necessary target-language word order but cannot
prove that each protected name retained its relationship to the surrounding
sentence. A prior exploratory run using regional tags also produced different
4B pass/fail outcomes despite temperature zero. Neither temperature zero nor a
single mechanical score should therefore be treated as reproducibility evidence.

This sample supports continuing evaluation of the 12B model, but not changing
production routing yet. Its seven mechanical passes are not seven editorial
approvals, the sample covers only three languages, native-language review is
still required, and its measured generation time was about 6.8 times the Q4 4B
total on the same host.
