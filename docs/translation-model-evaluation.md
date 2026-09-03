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

## Hy-MT2 request contract

Tencent's Hy-MT2 instructions require full language names in English prompts,
not BCP-47 tags or bare language codes. The experimental `HyMT2NativeAdapter`
therefore maps MunichBrief's canonical German source and each supported reader
language to Tencent's exact English label. This matters for Chinese: the website
registry calls it `Simplified Chinese`, while Hy-MT2's supported-language table
calls `zh` `Chinese`.

The adapter sends one user message with no system prompt or JSON schema. It uses
Tencent's default translation wording, adds the documented constraint that
code tags and variable placeholders must remain unchanged, and separates the
instruction from the protected source with two newline characters. It applies
Tencent's recommended 1.8B/7B settings through their Ollama equivalents:
`temperature=0.7`, `top_p=0.6`, `top_k=20`, `repeat_penalty=1.05`, and
`num_predict=4096`. The normal Ollama pipeline retains its existing settings.

Of MunichBrief's registered translated languages, Hy-MT2 officially supports
English, Turkish, Italian, Ukrainian, Chinese, Hindi, Spanish, French, Polish,
and Russian. It does not list Croatian, Bosnian, Greek, or Romanian; the adapter
rejects those targets instead of attempting an undocumented fallback. It is not
selected by production routing and requires live quality evaluation after the
local GGUF download completes.

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
end of the prompt. The experimental `SeedXNativeAdapter` therefore calls
Ollama's `/api/generate` endpoint with `raw=true`, no system prompt or schema,
and a prompt ending in the exact official tag, such as `<uk>` or `<zh>`. It
places two newline characters between the instruction and protected source and
adds a concise instruction to preserve code tags and variable placeholders.

ByteDance recommends beam search with width four and a maximum of 512 output
tokens. Ollama does not expose beam width through its documented runtime
options, so the adapter uses ByteDance's documented greedy alternative with
`temperature=0` and `num_predict=512`.

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
