# EU AI transparency and compliance posture

This document records MunichBrief's current engineering response to the
transparency rules for AI-generated content. It is an implementation and review
aid, not legal advice, a conformity assessment, or a statement that the project
is certified or legally compliant. The operator remains responsible for
assessing the deployed service with qualified counsel and the competent
authority.

## Regulatory basis

Article 50(4) of Regulation (EU) 2024/1689 requires a deployer that publishes
AI-generated or manipulated text to inform the public on matters of public
interest to disclose that the text was artificially generated or manipulated.
The text-specific exception applies where the content underwent human review or
editorial control and a natural or legal person holds editorial responsibility.
Article 50(5) requires the information to be clear, distinguishable, accessible,
and provided no later than first exposure. These provisions apply from
2 August 2026.

MunichBrief publishes AI-generated summaries and translations of police press
releases. Its normal public pipeline validates structure, privacy, and source
grounding but does not rely on human editorial review before every publication.
The project therefore takes the conservative position of visibly labelling all
published AI presentations, without attempting to rely on the editorial-review
exception.

The European Commission lists AI-generated news summaries as an example for the
**Fully AI-Generated** label. Use of the Commission's icons is optional and does
not establish compliance by itself. MunichBrief currently uses the official
`eu-ai-generated-white-50.svg` variant. The other official variants remain in
the repository for provenance and future review but are not rendered on reader
pages.

The Code of Practice on Transparency of AI-generated Content is voluntary.
Article 50 itself is binding. MunichBrief does not claim to be a Code signatory,
and use of an EU icon must not be interpreted as adherence to the Code.

## Implemented controls

| Control | Implementation |
| --- | --- |
| First-exposure explanation | A non-modal bottom disclosure explains local AI summarisation, categorisation, translation, possible errors, source authority, independence, and the presumption of innocence. |
| Acknowledgement | “Got it” stores an HTTP-only, SameSite=Lax acknowledgement cookie for 30 days. Dismissing the explanation never removes per-content labels. |
| Permanent content label | The selected **AI GENERATED** icon appears immediately before every AI-generated homepage and incident headline. Unprocessed source text is not labelled as generated. |
| Accessibility | The icon has an equivalent localized ARIA label and screen-reader text. Disclosure controls remain keyboard accessible and the dock avoids a backdrop or focus trap. |
| Authoritative source | Each incident retains a link and attribution to the official police release; the About and disclosure copy state that the official source controls. |
| Reshared images | Generated incident social cards contain a visible rendered form of the selected label. Every social card embeds IPTC/XMP `compositeWithTrainedAlgorithmicMedia` provenance because its stylized Olympiapark background was generated with AI. Separate response headers preserve the distinction between an AI-generated background and AI-generated incident text. |
| Machine-readable provenance | HTML metadata and `data-*` attributes, JSON-LD, Markdown front matter, response headers, API JSON, and IPTC/XMP social-card metadata expose AI status and available model identifiers. |
| Material disclosure revisions | The acknowledgement cookie checks an exact disclosure version. A material wording or scope change must increment the version so the notice is shown again. |

The machine-readable fields are additional interoperability signals. They do
not guarantee that crawlers will honour the disclosure and do not by themselves
satisfy any provider-side marking or detectability requirement.

Provenance is attached to each final social-card PNG rather than only to the
source background. The renderer decodes and re-encodes that background, which
would discard source-file metadata. XMP metadata can itself be removed by
downstream platforms and is therefore a disclosure signal, not a tamper-proof
watermark or signed C2PA Content Credential.

## Publishing boundary

Public mode fails closed: only complete, privacy-safe presentations for the
active source and prompt lifecycle are published. Review mode may display
retained original text and is intended only for local or access-controlled
quality review. Its permanent red warning is operational safety information and
cannot be dismissed.

The large disclosure is intentionally absent from the About page because that
page contains the full explanation. Permanent labels remain attached to
AI-generated content after the 30-day acknowledgement cookie suppresses the
large disclosure.

## Change-management checklist

Review this posture whenever any of the following changes:

- the AI Act, Commission guidelines, Code of Practice, or relevant technical
  standards;
- the publication workflow or the presence of human editorial review;
- generated content types, models, languages, or social-preview behavior;
- the selected icon, label wording, placement, contrast, or accessibility;
- machine-readable provenance formats or their underlying standards;
- the disclosure's material meaning, which requires a cookie-version bump.

Before deployment, verify the label on timeline and detail pages in both
languages and themes, the 30-day cookie behavior, Markdown and API disclosures,
social-card metadata, and public-mode fail-closed behavior. Retain test results
and the deployed commit as implementation evidence.

## Official sources

- [Regulation (EU) 2024/1689 (Artificial Intelligence Act)](https://eur-lex.europa.eu/eli/reg/2024/1689/oj)
- [EU icons for labelling AI-generated content](https://digital-strategy.ec.europa.eu/en/policies/eu-icons-labelling-ai-generated-content)
- [Code of Practice on Transparency of AI-generated Content](https://digital-strategy.ec.europa.eu/en/policies/code-practice-ai-generated-content)

Last reviewed: 28 August 2026.
