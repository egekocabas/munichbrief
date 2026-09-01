# Adding a reader language

MunichBrief has one canonical German presentation and any number of independent
translated reader languages. Adding a language is a code and operations change:
the application registry, immutable model prompt, UI catalog, public ingress,
and deployment verification must agree before the language is advertised.

This guide intentionally does not turn languages into runtime configuration.
The compiled registry remains the authority, so invalid or incomplete language
support fails during tests or application startup instead of partially
publishing.

## Choose stable identities

Choose these values before writing code:

- a normalized lowercase route code such as `fr` or `pt-br`; this is permanent
  in URLs, the preference cookie, and persisted translation scope keys, and
  must not be the reserved `api` prefix;
- the exact BCP-47 content tag, such as `fr-FR` or `pt-BR`, for negotiation,
  HTML, HTTP, Markdown, hreflang, structured data, and the TranslateGemma
  source/target code;
- the English model-facing language name used in the TranslateGemma prompt,
  such as `French` or `Portuguese`;
- the Open Graph locale, such as `fr_FR`;
- the language's own display name and localized language-switch/provenance
  labels; and
- a new immutable prompt version such as `incident-translation-fr-v1`.

Do not reuse a route code for another locale after jobs have been persisted. A
regional or script variant that requires different output should receive its
own stable code.

## Register application support

1. Add one noncanonical definition to the registry in
   `internal/languages/languages.go`. Supply its exact tag, catalog filename,
   English model-facing translation name, Open Graph locale, message IDs, and
   date formatters. Keep German as the only canonical entry. Confirm the exact
   BCP-47 tag is listed by the
   [Ollama TranslateGemma prompt guide](https://ollama.com/library/translategemma:4b)
   before registration.
2. Add a target-specific immutable translation prompt to
   `internal/processing/prompts.go`. Set its translation language to the new
   registry code and its step key to `translation/<code>`. Build its user-only
   template with the versioned TranslateGemma helper so source/target names,
   exact BCP-47 codes, output field names, and payload spacing cannot drift.
   Target-specific terminology guidance may be supplied without changing the
   shared request shape. The generic factory creates the schema, decoder,
   validator, queue scope, admin control, history, metrics contract, and a row
   in the translation operations dashboard. No dashboard template branch is
   needed for the new language.
3. Add `internal/web/locales/active.<code>.toml`. It must contain every message
   ID in the existing catalogs, including plural forms, category/report/time
   metadata, disclosure text, language switching, and processing provenance.
   Add the new language's switch and step labels to every existing catalog too.
   Supply every plural category used by the locale (for example `one`, `few`,
   `many`, and `other`) and test representative counts through the actual
   localization matcher.
4. Add native-language tests for representative singular/plural values, dates,
   metadata labels, navigation, disclosure text, and long mobile labels. Do not
   rely on machine translation as the only review of legal, privacy, or source
   attribution copy.
5. Confirm the compact language menu remains keyboard accessible and fits at
   320px, tablet, and desktop widths in light and dark modes. Header controls
   may wrap to a second row; language names must not be clipped.

No database migration is normally required. Translation jobs and values are
already keyed by processor and language scope, and startup records a durable
automatic-enablement cutover for every newly registered scope.

## Validate AI output and privacy

The new prompt receives only the accepted privacy-safe German title and
summary. It must preserve subjects, claims, uncertainty, Munich place names,
and the presumption of innocence without adding explanations or source details.
Define “preserve” with a fluent reviewer for the target script: retaining the
official Latin spelling and applying a standard local-script transliteration
can both preserve identity. An idiomatic target-language rendering of a generic
street-type word or grammatical case ending can also be acceptable when every
proper-name component and the complete street identity remain recognizable;
translating a proper name's literal meaning, dropping a component, or inventing
a district qualifier does not. Smoke fixtures must cover more than a district
list, including representative streets, squares, stations, parks, and
municipalities; do not hardcode an application allowlist and assume it covers
future source wording.
Keep the existing title and summary limits and strict two-field JSON output.
Generated fields are normalized to Unicode NFC before character-count
validation and persistence. Tests should include decomposed accents and the
target alphabet so equivalent text has one stable stored and cache identity.
For Greek, review tonos and dialytika; for Romanian, require comma-below `ș`
and `ț` rather than cedilla variants; for Polish, cover its complete extended
Latin alphabet; and for Russian, distinguish its Cyrillic repertoire (including
`Ё`) from Ukrainian. Exercise every CLDR plural category used by the locale,
including `few` and `many` where applicable.
TranslateGemma supports only user and assistant roles in its native template;
MunichBrief therefore sends the complete translation instruction and payload as
one user message while retaining Ollama's JSON schema constraint. The model
card documents accepted language-code forms and the native template contract:
[Google TranslateGemma model card](https://huggingface.co/google/translategemma-4b-it).

Add tests for valid output, missing/extra fields, length limits, unsafe public
text, and prompt-injection-like input. Then run the opt-in Ollama smoke check
with handcrafted anonymized cases and have a fluent reviewer compare the
translation with the accepted German presentation. Never commit model output,
real police article copies, or a production database.

Review `docs/eu-ai-transparency.md` whenever a language changes disclosure,
label placement, generated content, social previews, or machine-readable
provenance.

## Verify reader and discovery behavior

Exercise the timeline, about page, one translated incident, Markdown responses,
and social cards. Confirm:

- the root redirect honors the new BCP-47 language and the preference cookie;
- HTML `lang`, `Content-Language`, Markdown front matter, canonical URLs,
  hreflang, Open Graph locales, and JSON-LD use the registered identities;
- the sitemap contains the language's static pages and includes incident URLs
  only after their translations succeed;
- an incident without a successful translation remains absent from that public
  language, while German and other successful languages remain available; and
- public-host path filtering still rejects admin and unknown routes.

Social cards use embedded fonts, so confirm glyph coverage for the complete
target alphabet before registration. Scripts with contextual forms, conjuncts,
or reordered marks require a shaping engine; rune-by-rune glyph drawing is not
acceptable. CJK titles also require line-breaking tests that do not assume
spaces between words. Bundle the font's license, include its bytes in the card
cache identity, and test actual rendered pixels in addition to nominal glyph
coverage. Test locale-aware casing (especially Turkish dotted and dotless I),
title wrapping, and the text safe area. Keep
website text unchanged; any substitution for an unsupported punctuation mark,
such as modifier apostrophes or non-breaking hyphens, belongs only in the
social-image renderer and must have a focused test.

Before release, a fluent native reviewer must approve navigation and metadata,
AI disclosure, privacy language, source attribution, neutral police
terminology, uncertainty, and presumption-of-innocence copy. Keep the pull
request in draft until that legal-sensitive checklist is complete.

Run the complete validation suite in `docs/development.md` before opening the
application pull request.

## Coordinate deployment

The bundled chart keeps a defense-in-depth language allowlist. Add the route
code to `ingress.public.languageCodes` and verify the rendered Prefix path.
Deployments that do not use the chart must make the equivalent explicit ingress
change. In `homelab-infra`, add the prefix under the shared MunichBrief public
paths inside the marked `reader-language-prefixes` block; static verification
derives its expectations from that block.

Use this rollout order:

1. Merge and reconcile the ingress prefix first. The old application safely
   returns 404 for the not-yet-registered language.
2. Deploy the application image containing the registry, prompt, and catalog.
3. Confirm the shared translation model is configured and available, review new
   automatic translations, and check queue/failure metrics and logs.
4. From `/admin/translations`, review the new language's coverage and explicitly
   use **Queue unpublished** for current presentations only after quality
   review. Use **Rerun all** only when every existing success should be
   replaced. Registration never launches an automatic historical backfill.
5. Recheck the canonical host's sitemap, hreflang set, social cards, Markdown,
   and representative public pages.

To roll back, stop new work by removing or reverting the application
registration and image. Completed language-scoped jobs remain auditable and do
not affect German or another translation. Remove the public ingress prefix only
after the application rollback is serving 404 for it; do not delete stored jobs
or values.
