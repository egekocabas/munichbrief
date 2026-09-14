# MunichBrief legal-page review — 13 September 2026

## Finding and scope

MunichBrief is an individually operated public incident-summary service, not a
newspaper company. That description does not decide which rules apply. Selecting,
summarizing and regularly publishing news can engage editorial duties without a
professional journalist title. Conversely, naming an editorially responsible person
does not establish a GDPR media exemption. These are separate legal questions.
[BLM guidance](https://www.blm.de/de/wir-regulieren/journ_sorgfaltspflicht.cfm)
and [MStV §18](https://www.gesetze-bayern.de/Content/Document/MStV-18) support this
working assessment. A definitive classification of this automated service remains
open; do not describe this review as certification that nothing is missing.

The review covers current public information pages and the known operation:
individual operator in Bavaria, own German hardware and local AI, public police
sources, no visitor accounts, subscriptions, advertising, public comments or shop,
and the existing contact/email/postal chain. It is not an audit of provider accounts,
all stored reports or every publication decision. No provider settings were changed.

The subsequent [editorial and provider follow-up](legal-follow-up-2026-09.md)
records the requested direct research, an applied working assessment, source reuse
permission, concrete provider-agreement routes and proposed operational safeguards.
It narrows the open questions below without claiming an official classification.

## Bavarian publisher comparison

Publisher pages are examples of what those publishers disclose, not authoritative
legal advice or evidence of MunichBrief's practices.

| Reference inspected | Useful comparison | MunichBrief decision |
| --- | --- | --- |
| [Süddeutsche Zeitung imprint](https://www.sueddeutsche.de/projekte/artikel/verlag/artikel-e287935/) | Provider identity and contact details. | Existing individual details retained. |
| [Merkur imprint](https://www.merkur.de/ueber-uns/impressum) | Identifies editorial responsibility and distinguishes multiple content providers; explains AI use. | MunichBrief has one operator. Keep that responsibility and its own accurate AI explanation; do not copy a claim of human checking. |
| [Merkur privacy](https://www.merkur.de/ueber-uns/datenschutz/) | Separates contact, technical operation, subscriptions and advertising; describes recipients and retention. | Use MunichBrief's actual smaller processing inventory. Do not import its contact fields, IP storage, advertising consent or transfer assumptions. |
| [Abendzeitung imprint](https://www.abendzeitung-muenchen.de/impressum/) | Separates digital and print providers and identifies editorially responsible people. | No separate print publisher or corporate officers to invent. Broad liability/copyright boilerplate is not added. |
| [Abendzeitung privacy](https://www.abendzeitung-muenchen.de/datenschutz/) | Covers many additional functions, including advertising, competitions and payments. | Those functions are not present here. Avoid a cookie-consent system or service disclosures merely because another publisher uses them. |

The SZ privacy PDF surfaced in search but could not be fetched reliably; no
conclusions about its full contents are claimed. The complete privacy comparisons
above are with Merkur and Abendzeitung.

## Applicable rules and implementation findings

| Area | Assessment for the current service | Page or operational consequence |
| --- | --- | --- |
| Operator information | A public incident site is not exclusively personal/family communication. MStV §18(1) is relevant; §18(2) adds editorial responsibility where applicable. DDG §5 has its own scope. | Name, serviceable correspondence address, email, form and responsible person already published. No invented company, VAT or telephone data. |
| Editorial care | MStV §19 includes qualifying online news services. Care concerns content, origin and truth **before** publication. | About correctly describes initial automated checks and later verification. A later AI check or disclaimer cannot cure inadequate initial care. Assess the actual publication safeguards, particularly allegations, injuries and identifiable people. |
| Corrections and replies | MStV §20 can require a formal right of reply, with conditions distinct from an ordinary correction request. | Add localized report-request guidance to Impressum, including the postal route where signed writing is required. Do not promise that the form satisfies every formal requirement or that every request must be published. |
| Source/editorial privacy | GDPR Article 85 and MStV §23/BayPrG Article 11 require purpose- and operator-specific analysis. Offence-related data engages Article 10 if ordinary GDPR rules apply. | Keep the public notice's separate source explanation and individual request handling. Do not claim all summaries or retained originals are anonymous, or a general journalism exemption. |
| Ordinary personal data | GDPR purposes, lawful bases, minimization, retention, transparency, security and rights remain relevant to contact, traffic and providers. | Existing Privacy covers these categories. Provider agreements, actual transfers and safeguards still need verification; links to provider policies alone do not complete this work. |
| Browser storage | TDDDG §25 concerns terminal storage/access independently of the GDPR processing basis. Requested essential functions must be distinguished from analytics. | Preserve requested preferences and security functions, restrictive CSP, and the precise Cloudflare EU-exclusion configuration. No blanket analytics exemption or globally-disabled claim. |
| AI transparency | AI Act Article 50 distinguishes public-facing disclosure and provider-side machine-readable marking. Current Commission guidance places law-enforcement/public-security news within public-interest text. | Retain visible AI labels and provenance. Automated checks are not human review. Assess whether developing/putting the own system into service also makes the operator a provider; JSON metadata or an icon alone is not a proven complete marking solution. |
| DPO and impact assessment | Staffing is not the only trigger. BDSG §38, GDPR Articles 35/37 and the scale/risk of offence-related processing matter if ordinary GDPR applies. | No invented DPO contact. Record a source-data/scale/risk assessment; do not conclude that operating alone guarantees an exemption. |
| Copyright and personality rights | Public access is not a licence or permission to republish identifiable allegations. UrhG §5 requires case-specific assessment; general personality-rights protection remains relevant. | Keep source attribution, data minimization, presumption of innocence and correction channels. Software's MIT licence does not settle every source or generated-content right. |
| Consumer and accessibility pages | VSBG §36 depends on trader/activity facts and contains a small-staff exception to one disclosure. BFSG §§1/3 concern specified services and a microenterprise service exception. | No evidence of current consumer sales or subscription functions requiring newspaper-style terms, cancellation UI or an accessibility declaration. Maintain accessibility and reassess on business-model changes; size is not a blanket exemption from all laws. |

## Matters that copy changes cannot settle

1. Obtain a documented assessment of the editorial classification, offence-related
   source processing and any media privilege, including whether the retained raw
   archive is necessary and proportionate. Assess DPIA/DPO triggers as part of that
   work. A new relevant judgment is **C-199/24, Legal Newsdesk Sweden, 9 July 2026**:
   merely offering a criminal-conviction database does not in principle establish
   journalistic purposes. MunichBrief summarizes police releases and is not that
   paid person-search database; the case nevertheless rules out a shortcut based
   only on making public documents available. [CJEU summary](https://curia.europa.eu/site/upload/docs/application/pdf/2026-07/cp260100en.pdf).
2. Verify the existing provider arrangements, especially personal Gmail versus a
   contracted business mailbox, the applicable processing roles/agreements,
   transfers, and provider retention. Update public Article 13/14 transfer details
   from verified facts before activating the contact deployment. See
   [privacy assessment](privacy-draft.md) and [activation instructions](contact-and-legal.md).
3. Review initial publication safeguards against editorial care, and the own AI
   system's provider/deployer roles. This PR does not introduce human approval,
   claim every source is independently verified, or claim Code of Practice membership.
4. Handle actual editorial/legal requests promptly and on their merits. Where
   MStV §23(3) applies, retain required replies, undertakings and decisions with the
   affected source data for its lifetime. Use a reasoned retention hold for related
   correspondence; the inbox's 90-day rule must not erase required evidence. The
   current inbox does not automatically attach a formal reply to an incident or
   publish one. Formal requests need an operator workflow and, where necessary,
   case-specific legal assistance. Do not treat deletion of an email as compliance.

## Public changes and date maintenance

Both Privacy and Impressum show a localized **Last updated: 13 September 2026**.
Each date is independently maintained in `legalPageUpdatedAt` in
`internal/web/legal.go`. Update only the affected page's date when its information
changes; a no-change legal recheck belongs in an internal dated review record.
Dates do not advance on requests, builds or deployments. They appear in HTML
`time` elements, Markdown, WebPage JSON-LD and sitemap `lastmod`. They do not set
incident publication timestamps or article-specific metadata on legal pages.
All 14 language versions are revised together in this change.

Impressum adds a concise route for correction, removal and right-of-reply requests.
Existing About explanations remain accurate; the rejected AI legal-compliance
warning is not restored. No separate Terms, disclaimer, cookie-policy or newspaper
membership page is added. No obsolete EU ODR-platform link is added: the platform
was discontinued on 20 July 2025. [Commission notice](https://consumer-redress.ec.europa.eu/site-relocation_en).

## Primary authorities checked

- [MStV §18: identification](https://www.gesetze-bayern.de/Content/Document/MStV-18),
  [§19: care](https://www.gesetze-bayern.de/Content/Document/MStV-19),
  [§20: reply](https://www.gesetze-bayern.de/Content/Document/MStV-20),
  [§23: editorial data](https://www.gesetze-bayern.de/Content/Document/MStV-23).
- [BayPrG Article 11](https://www.gesetze-bayern.de/Content/Document/BayPrG-11),
  [DDG §5](https://www.gesetze-im-internet.de/ddg/__5.html),
  [BLM editorial guidance](https://www.blm.de/de/wir-regulieren/journ_sorgfaltspflicht.cfm).
- [GDPR](https://eur-lex.europa.eu/eli/reg/2016/679/oj),
  [BDSG §38](https://www.gesetze-im-internet.de/bdsg_2018/__38.html),
  [BayLDA DPO criteria](https://www.lda.bayern.de/de/thema_datenschutzbeauftragter.html),
  [BayLDA impact assessments](https://www.lda.bayern.de/de/thema_dsfa.html),
  [TDDDG §25](https://www.gesetze-im-internet.juris.de/ttdsg/__25.html).
- [Commission Article 50 FAQ](https://digital-strategy.ec.europa.eu/en/faqs/transparency-obligations-under-article-50-ai-act)
  and [AI literacy, Article 4](https://ai-act-service-desk.ec.europa.eu/en/ai-act/article-4).
  The Commission's Article 50 explorer flags an unconsolidated text after Digital
  Omnibus amendments. The current FAQ distinguishes the 2 August 2026 disclosure
  date from limited legacy-provider marking relief until 2 December 2026. Do not
  use the latter as a general extension for public labels or assume it covers this
  system. Verify the operative amended text for any claimed transition entitlement.
- [UrhG §5](https://www.gesetze-im-internet.de/urhg/__5.html),
  [BGB §823](https://www.gesetze-im-internet.de/bgb/__823.html),
  [VSBG §36](https://www.gesetze-im-internet.de/vsbg/__36.html),
  [BFSG §1](https://www.gesetze-im-internet.de/bfsg/__1.html) and
  [§3](https://www.gesetze-im-internet.de/bfsg/__3.html).
