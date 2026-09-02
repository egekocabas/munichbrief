# Translation model evaluation

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
experimental `TranslateGemmaNativeAdapter` therefore expands Google's official
plain-text instruction in application code, sends one protected field per
request, requests plain output without a JSON schema, disables sampling, and
leaves restoration and validation to application code.

This adapter is experimental and is not selected by production routing. The
existing general-LLM translation step remains active until a controlled
evaluation demonstrates that the native adapter improves both fidelity and
language quality.

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

## Deterministic acceptance

Before persistence, a translation must preserve:

- the exact protected-place token multiset and occurrence count in each output
  field, while allowing target-language word order;
- URLs and supported Markdown decoration, reconstructed by application code
  where the decoration surrounds a protected place;
- numeric facts, with leading-zero and textual-month/numeric-month equivalence
  handled without accepting changed phone, speed, date, time, or unit values;
- valid Unicode, NFC normalization, field limits, and the expected target
  script for non-Latin targets.

Deterministic acceptance catches corruption but does not approve meaning or
grammar. Every model evaluation must retain raw output and separately note
omissions, additions, altered causality, attribution, uncertainty, negation,
police terminology, and presumption-of-innocence wording.

## Bounded comparison protocol

Compare candidates at temperature zero, sequentially per model, using identical
protected inputs. Start with difficult fixtures spanning repeated and reordered
places, dates and times, emergency numbers and measurements, Markdown and URLs,
non-Latin scripts, attribution, uncertainty, and legal framing. Only expand to
the complete language matrix when a candidate improves editorial quality rather
than merely its mechanical pass count.

Checkpoint after every request. A transport interruption must stop the run and
resume from the checkpoint rather than recording a model failure or repeating
completed requests. Live output is evaluation evidence and must not be committed
as reader content.

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
