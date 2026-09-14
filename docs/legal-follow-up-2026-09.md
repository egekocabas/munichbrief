# Editorial and provider research — September 2026

Last reviewed: **14 September 2026**. The initial editorial review was performed
on 13 September; subsequent provider findings are dated below and in the
[mailbox and Cloudflare review](mail-provider-review-2026-09.md).

For the latest decisions, pending replies and task boundaries, read the
[continuation notes](continuation-notes.md) first.

## Practical assessment

This follow-up answers the operator's request to research the open questions in
English. It supplements the [earlier review](legal-review-2026-09.md). The working
recommendation is to operate MunichBrief as a small editorial news service: take
responsibility for the selection, accuracy and correction of its reports, without
claiming to be a newspaper company or claiming a blanket data-protection exemption.

The recommendations below are not new implemented features or retention settings.
The SMTP2GO review records acceptance evidence subsequently supplied by the operator;
the assistant did not accept any contract. No account settings, public copy,
source records or sending behaviour changed during this research.

## Editorial duties and the separate privacy question

The [media authorities' guidance hosted by BLM](https://www.blm.de/files/pdf2/merkblatt_medienanstalten_journalistische_sorgfalt_internet.pdf)
explains that a sustained public news service can be businesslike in the statutory
sense without making money. Regular selection and preparation of news are relevant
indicators. MunichBrief's continuing selection, summarization and multilingual
publication support treating editorial duties as applicable. This is an application
of that guidance to the project, not an official classification of MunichBrief.

The CJEU's [Buivids judgment, paragraph 55](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX%3A62017CJ0345)
confirms that not being a professional journalist does not itself exclude
journalistic processing. It interpreted the predecessor directive; current GDPR
case law and Bavarian provisions must also be considered.

In [C-199/24, paragraphs 61–72](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX%3A62024CJ0199),
the CJEU connects journalistic purposes with editorial decisions, sufficiently
reliable factual allegations and ethical standards. Necessary preparatory
processing can qualify, including material ultimately not selected. The judgment
does not establish an exemption for every database of public documents.

Applied here, the public-information purpose, edited summaries, restricted source
access, privacy rules and correction process support a journalistic-purpose
assessment. They do not settle whether this particular automated workflow and
operator qualify for every derogation under [MStV §23](https://www.gesetze-bayern.de/Content/Document/MStV-23)
and [BayPrG Article 11](https://www.gesetze-bayern.de/Content/Document/BayPrG-11).
No decision specifically resolving this workflow was located. Editorial duties
under §§18–20 and eligibility for §23 are separate tests.

The recommended internal position is therefore purpose-specific: source collection,
checking, summarization and necessary correction evidence are assessed as editorial
processing; correspondence, visitor preferences, traffic and security processing
retain their separate ordinary GDPR assessment. Do not use Article 6(1)(f) alone
as a substitute for the additional requirements for identifiable offence data
under Article 10. Removing names from the public summary does not remove the
personal data already collected in the source or guarantee that contextual details
cannot identify someone.

## Source reuse and publication checks

The [Bavarian Police's own usage terms](https://www.polizei.bayern.de/wir-ueber-uns/impressum/index.html)
expressly permit reprinting and analysis of press releases with source attribution.
This provides a more concrete basis for the project's press-release reuse than
assuming every release is copyright-free under UrhG §5. Preserve the source link
and attribution. This permission does not settle personality rights, unrelated
third-party material, retention or automated-access restrictions. The robots file
could not be fetched in this review; the historical caution in the
[source policy](source-policy.md) remains qualified as historical.

The code currently requires a model privacy verdict of `safe`, rejects unresolved
privacy uncertainty and applies deterministic presentation checks. The registered
background verification jobs check public-assistance metadata and categories;
they do not independently compare every sentence with the source. See
`validateGermanPresentation` and `validatePublicText` in
[`steps.go`](../internal/processing/steps.go), and the registrations in
[`post_processors.go`](../internal/processing/post_processors.go).

Recommended next implementation: a source-to-summary consistency check before
first publication, with uncertain or identifying results withheld for review.
Check that allegations remain allegations, roles and outcomes are not invented,
and combinations of location, age or circumstances do not unnecessarily identify
people. Apply equivalent protection before publishing translations. This is a
proposed safeguard, not a claim that a second model certifies truth or satisfies
every legal duty. The operator must still handle exceptions and corrections.
[MStV §19](https://www.gesetze-bayern.de/Content/Document/MStV-19) requires care before
dissemination; later label checks cannot replace that.

## Retention recommendation

No universal 30-day or 90-day deletion requirement for this source archive was
identified. Under the ordinary GDPR framework, retention follows necessity and
purpose, with deletion or review limits; see the
[Commission's storage-limitation guidance](https://commission.europa.eu/law/law-topic/data-protection/information-business-and-organisations/principles-gdpr_en).
A shorter period does not itself supply a missing legal basis. The contact inbox's
90 days after resolution remains a separate operator-selected policy.

Proposed source policy, subject to a deliberate retention implementation:

- Retain only the restricted evidence necessary to support a published summary,
  its corrections and a concrete legal hold. Public availability of the summary
  is a reason to assess continuing need, not automatic permission to retain all
  related raw material indefinitely.
- Give rejected material, obsolete snapshots and debugging outputs a separate
  short retention period. Select the period from actual reprocessing needs;
  do not describe an arbitrary 30 or 90 days as a statutory requirement.
- Review continuing necessity at a fixed interval and when an incident is removed,
  corrected or challenged. Delete or redact material no longer justified.
- Where MStV §23(3) applies, keep required replies, undertakings and decisions
  attached to the affected data for the same lifetime. The inbox alone does not
  implement this relationship.

These proposals must cover current incident text and historical RSS/parse
snapshots. No source deletion was performed or scheduled. Backups remain deferred.

## Provider findings and specific remaining steps

| Provider | Documented findings | Remaining action |
| --- | --- | --- |
| Cloudflare | [Self-Serve Agreement §6.1](https://www.cloudflare.com/terms/) incorporates its [DPA](https://www.cloudflare.com/cloudflare-customer-dpa/) for covered processing. Version 6.4, effective 3 April 2026, was reviewed; its annexes anticipate end-user special-category content and safeguards. | Verify account-specific settings separately; the DPA does not establish EU-only processing or 30-day retention. A separately negotiated contract is not inherently required under these standard terms. |
| SMTP2GO | DPA version 1.4 acceptance on **27 August 2026 at 19:45 UTC** and the EU-hosting indicator were supplied. Rick's support reply dated **13 September 2026, 22:52 UTC** permits the described contact-form/correspondence use under normal terms and confirms no extra special-category DPA or approval process is required. | The requested scope clarification is received; full-text notifications can remain as planned. Verify retention and dedicated-key settings separately. The reported `mail-eu.smtp2go.com` host covers EU/UK infrastructure; other providers' processing remains separate. |
| COCENTER / anschrift.net | [Privacy §5.5](https://anschrift.net/datenschutzerklaerung/) expressly offers an AVV for customer-directed handling of incoming post containing third-party personal data. Its own customer/billing processing has a different role. | The operator subsequently supplied a downloaded AVV; the document review below finds missing attachments and no evidence of account-specific conclusion in that copy. Obtain the complete applicable agreement. Provider-stated scan and paper retention remain separate from the application's deletion policy. |
| Google/Gmail | Google's Privacy Help Center explicitly says consumer Gmail has no DPA and that Google does not act as a processor for that service. Workspace Individual and newer Workspace Personal terms offer separate routes for non-household use; DPA version 10 was read on 14 September. | Google's stated role does not itself settle suitability for this workflow. A BayLDA enquiry was submitted with operator approval on 14 September; its page confirmed delivery. A response is pending independently of the other two providers. No subscription was purchased. See the [mailbox review and receipt](mail-provider-review-2026-09.md). |

AVV is the German name for a data-processing agreement: contractual obligations
when a provider processes personal data on the operator's instructions. It does
not require commissioning a bespoke legal opinion. Public contractual documents
can settle some questions; they cannot prove an account's subscription, acceptance
record or configured options.

Whether an AVV is required depends on the provider's actual processing role.
It is not a universal requirement for every communications provider. The
[14 September follow-up](mail-provider-review-2026-09.md) records current BayLDA
guidance and qualifies the earlier suggestion to move to a contracted mailbox.

An optional technical reduction is to email only a new-message alert and an admin
reference, keeping correspondence in the private inbox. This would reduce copies
of message bodies and sender addresses, but would not resolve processing of emails
sent directly to the contact address. It would change the currently agreed full
notification behaviour and has not been implemented.

The subsequent document review below records the operator's sent request and
the remaining postal-provider questions.

## Consequence for the public pages

Keep the published operator identity, contact routes, accurate processing inventory
and independently maintained revision dates. Do not add a claim that MunichBrief
is GDPR-exempt or publish recommendations as already active settings. Update the
relevant public notice only when an actual processing arrangement changes. The
main next work is publication safeguards, a reasoned source-retention policy and
specific provider records, rather than more general disclaimer paragraphs.

## Subsequently supplied anschrift.net contracts

On 13 September 2026 the operator supplied `Dienstleistungsvertrag (1).pdf`
(two pages, version 31 March 2026) and
`Vereinbarung_Auftragsverarbeitung (1).pdf` (eight PDF pages). Both were read in
full and visually inspected. The PDFs, extracted text and rendered images were
not copied into the repository. The files were reviewed as evidence, not signed
or accepted on the operator's behalf.

### Service contract

Sections 1(1), 1(3) and 1(6) expressly describe an address service for imprints,
postal correspondence, court summonses and official mail. Sections 1(1) and 1(4)
distinguish it from a company seat or branch. This supports the current public
description as a correspondence address; it is not an independent finding that
the address satisfies every possible statutory use.

Section 1(2) provides for opening and scanning mail, including personal or
confidential letters, into the protected account. Section 2 links commencement
to the order confirmation. The downloaded standard contract itself does not
identify the customer or establish the account's activation history. The operator
previously supplied the purchased address; retain the actual order confirmation
privately as the service record.

Sections 5 and 6 address termination and prohibit continued publication of the
address after the service ends. Section 6(4) includes conditional additional
charges and a EUR 1,500 contractual penalty provision for continued unauthorized
use; its enforceability was not assessed. Replace the address everywhere under
the operator's control before ending service.

### AVV: useful substance, incomplete evidence

- **Missing material:** the eight pages are numbered 2 through 9 of 11. The copy
  contains no page identifying the contracting parties, no security-measures
  appendix required by section 5(1), and no subcontractor list referenced in
  section 7(11). No replacement attachments were located in the public pages
  inspected. A pagination error alone would not settle completeness, but the
  referenced appendices are actually absent.
- **Conclusion not established:** the signature area is blank and there is no
  account-specific acceptance record in the PDF. This does not prove that the
  parties have no agreement elsewhere. Article 28(9) GDPR permits electronic
  form; the PDF also defines certain written declarations by reference to BGB
  §126. Ask the provider for its applicable acceptance/signature process and a
  record identifying the parties, version and effective date.
- **Scope:** section 2.1 fits receiving, opening, scanning, supplying and forwarding
  correspondence, with six-month retention of originals. Sections 3.3–3.4 list
  limited data categories and only customers as affected people. Request express
  coverage of correspondents and other people mentioned in letters, including
  correction/privacy requests and potentially sensitive information.
- **Protections:** the text addresses instructions and confidentiality (section
  4), deletion (6 and 11), subcontractors (7), rights assistance, audit access
  and breach notification without delay and within 24 hours of awareness (9).
  These are contractual promises, not a technical audit. The referenced missing
  security appendix prevents verification of the agreed minimum safeguards.
- **Locations:** section 4(10) generally limits processing to the EU/EEA and
  conditions third-country processing on authorization and safeguards. Obtain the
  actual subcontractor/processing-location attachment; do not infer EU-only
  operation. The published privacy notice lists Hetzner and Dropbox Business,
  but does not itself identify every recipient of MunichBrief postal content.
- **Roles:** the private-device/home-processing restriction in section 5 concerns
  the processor's handling of the outsourced service; it is not a general ban on
  the operator hosting MunichBrief on their own hardware.

The [GDPR Article 28 requirements](https://eur-lex.europa.eu/legal-content/EN/TXT/?qid=1558176381563&uri=CELEX%3A32016R0679)
are why the processing description, safeguards and agreement evidence matter.
No conclusion that this PDF is a completed or fully sufficient AVV is recorded.

### Website terms and current public copy

The [AGB](https://anschrift.net/agb/) describes the order/confirmation process and
incorporation of the service terms. Its checkout description does not establish
acceptance of this separate AVV. The
[withdrawal notice](https://anschrift.net/widerrufsbelehrung/) distinguishes ending
the subscription from its offered 14-day withdrawal process. These govern the
operator's purchase; they do not create a need to copy cancellation terms onto
MunichBrief.

The [provider privacy notice](https://anschrift.net/datenschutzerklaerung/) states
six-month paper retention and scan availability until account closure or requested
deletion, subject to justified retention. The existing MunichBrief privacy text
already attributes those statements to the provider and distinguishes postal
processing. No public copy change is warranted solely by these downloads, and
the app's 90-day correspondence rule does not automatically delete postal scans.

### Operator update and sent provider request

The operator clarified that the Bayern address service has been purchased and
paid for, but identity verification and activation are still pending. Purchase
must not be recorded as confirmation that postal receipt is active. Confirm
activation before deploying pages that rely on this correspondence address.

The operator then confirmed sending the following request to
`support@anschrift.net`, which is listed on the provider's
[contact page](https://anschrift.net/kontakt/). The download location was the AVV
section of [Vertragsunterlagen](https://anschrift.net/vertragsunterlagen/), which
requires login. Sending is operator-reported; receipt or a response has not been
independently verified. No agreement was signed or accepted by the assistant.

This sent version requests completeness and the agreement process. It does not
ask the earlier draft's additional scope questions; review the returned documents
for those points before deciding whether another message is necessary.

> Betreff: Fehlende Seiten und Anlagen im AVV-Download
>
> Guten Tag,
>
> ich habe Ihren Impressum-Service mit Anschrift in Bayern für meine Website
> munichbrief.de bestellt und bezahlt. Aktuell warte ich noch auf die Bestätigung
> meiner Identitätsprüfung und die Freischaltung.
>
> Auf Ihrer Seite Vertragsunterlagen (https://anschrift.net/vertragsunterlagen/)
> habe ich im Abschnitt „AVV“ die Vereinbarung zur Auftragsverarbeitung
> heruntergeladen.
>
> Die PDF-Datei enthält acht Seiten. Sie beginnt mit „Seite 2 von 11“ und endet mit
> „Seite 9 von 11“. Außerdem verweist der Vertrag auf einen Anhang 1 zu den
> Sicherheitsmaßnahmen und eine Anlage 2 zu den Unterauftragnehmern. Beide Anlagen
> sind in der heruntergeladenen Datei nicht enthalten.
>
> Könnten Sie mir bitte die vollständige aktuelle Vereinbarung einschließlich der
> Angaben zu den Vertragsparteien und sämtlicher Anlagen zusenden? Die
> heruntergeladene Datei habe ich zur Prüfung beigefügt.
>
> Bitte teilen Sie mir auch mit, wann und wie die Vereinbarung für mein Kundenkonto
> wirksam wird und ob ich sie dafür noch ausfüllen, unterschreiben oder anderweitig
> bestätigen muss.
>
> Falls möglich, würde ich mich über eine Antwort auf Englisch freuen, da ich kein
> Deutsch spreche.
>
> Vielen Dank für Ihre Unterstützung.
>
> Mit freundlichen Grüßen
> Ege Kocabaş

## Subsequently supplied SMTP2GO DPA

Reviewed on 13 September 2026: the operator's pasted dashboard agreement and
`SMTP2GO _ Data Format Settings.pdf` (29 pages), DPA **version 1.4, modified
15 July 2025**, with **Sand Dune Mail Limited, New Zealand**. The pasted text
records acceptance on **27 August 2026 at 19:45 UTC**. This is supplied account
evidence, not a fresh login or acceptance performed by the assistant. Keep the
original acceptance record privately; its account email and the original documents
are not copied into this repository.

The PDF includes the DPA and Schedules 1–5: security measures, processing details,
SCC arrangements, UK addendum and regional terms. It does not show the acceptance
timestamp or the account's hosting indicator, API-key permissions, quotas,
tracking or archiving options. The EU dashboard URL alone does not verify them.

### Contract coverage and limits

- Sections 3–6 address roles, lawful instructions, confidentiality and security.
  SMTP2GO acts as a processor for service operations, but section 3 also sets out
  independent-controller purposes, including security, billing and service
  improvement. Do not describe all its processing as solely on instructions.
- Section 7 provides for deletion or return after services end, subject to the
  stated procedure and legal retention. It is not an account-specific activity
  retention setting or a promise to remove copies held by Gmail.
- Section 8 incorporates the current
  [subprocessor list](https://support.smtp2go.com/hc/en-gb/articles/360018225773-Sub-Processor-List).
  Subscribe to its change notices to use the stated 14-day objection process.
  The list gives company headquarters and purposes; headquarters are not evidence
  of each account's data location or that every company receives message bodies.
- Section 10 and Schedule 3 provide international-transfer arrangements where
  applicable; the agreement does not promise exclusively EU processing.
- Section 12 requires breach notification without undue delay and within
  48 hours of awareness. Schedule 1 supplies security measures, so the missing
  security-annex issue in the postal-provider download does not recur here.

### Initial free-text concern and optional notification reduction

Schedule 2, section 6 says the services are not designed to process special
categories unless otherwise specified; Schedule 3 describes sensitive data as
not applicable. Section 3.5.5 also requires appropriate safeguards before sending
sensitive data. At the initial review these provisions left the described
free-text use insufficiently clear, leading to the support enquiry below. The
subsequently supplied response resolves that provider-scope question without
requiring an extra agreement or approval. For example, a correction enquiry can
contain health information.
Crime-related data has its own separate Article 10 assessment; it is not all
automatically Article 9 special-category data.

An earlier optional proposal would change notifications to a generic new-enquiry alert with
an internal reference and protected inbox link, omitting the message body, sender
address/Reply-To and topic. This reduces correspondence copied through SMTP2GO,
Cloudflare and Gmail. It **has not been approved or implemented** and changes the
previously agreed full-text notifications. Following Rick's clarification, do not
treat this proposal as required by SMTP2GO or as a condition for its permission
to use the described workflow. Keep the agreed full-text implementation. Minimal
alerts do not resolve separately received email or replies through these providers.

Clarification: the proposed alert would still be sent to the operator through
SMTP2GO's API and the existing Cloudflare Email Routing/Gmail chain. Only its
contents would change. The form first submits to the MunichBrief server and is
saved in SQLite; it does not submit directly from the browser to SMTP2GO. The
Cloudflare-proxied website still carries the form request, so this proposal does
not keep the full message out of Cloudflare's website-delivery path. It removes
the additional full-text email copy. Direct incoming emails continue through
Cloudflare Email Routing to Gmail, and replies (including quoted originals) sent
via Apple Mail continue through SMTP2GO. No notification change has been authorized.

### Support enquiry sent — 14 September 2026

The operator reports sending the SMTP2GO support enquiry by email following the
draft prepared in this conversation. The draft was addressed to
`ticket@smtp2go.com`, with the subject **DPA clarification: contact-form emails
that may contain sensitive information**. The final sent message and a ticket
identifier have not been supplied; the subject and contents here describe the
prepared draft, not a separately inspected sent email.

The draft explains full contact-form submissions sent through SMTP2GO's EU API
to `contact@munichbrief.de`, subsequent Cloudflare forwarding to Gmail, and
existing Apple Mail replies through SMTP2GO. It asks whether DPA version 1.4
covers occasional sensitive content, whether additional contractual terms or
processing schedules are needed, and which security settings or restrictions
apply. The subsequent wording clarification explicitly identifies the operator's
own contact address as the API notification recipient.

**Status: response received; requested scope clarification resolved.** The reply
below does not require an implementation change. Full-text notifications remain
unchanged. Shorter alerts remain an optional privacy reduction.

### Support response received — reviewed 14 September 2026

The operator pasted a reply from **Rick (SMTP2GO Support)** dated
**13 September 2026, 22:52 UTC** (14 September, 00:52 in Europe/Berlin). The
original ticket number and message headers were not supplied. This record relies
on the operator-supplied correspondence; no support mailbox was accessed.

Rick confirms that:

- SMTP2GO permits the contact form and correspondence described in the enquiry,
  provided use complies with its [Terms of Service](https://www.smtp2go.com/terms/).
- It does not provide a separate DPA or additional terms for special-category data.
- Schedule 2 describes the service's design limitations, not an additional DPA
  or approval process that the operator must complete.

Applied to the submitted use case, this supports keeping full-message API
notifications to the operator's own contact address and the described Apple Mail
correspondence. No further SMTP2GO enquiry is needed on that specific point.
Retain the original reply privately with the already-accepted DPA. Treat the reply
as the provider's clarification, not a replacement agreement or confirmation of
every message's lawfulness, Google's arrangement or account-specific settings.

The linked terms were checked on 14 September. Their normal authorized-recipient,
anti-abuse, lawful-content and valid-sender requirements still apply. They also
restrict automatic forwarding of existing third-party emails. The application
creates contact-form notifications to a fixed operator recipient; do not extend
this reviewed use case into a general email-forwarding service through SMTP2GO.
Cloudflare handles the separate incoming-email forwarding path.

The remaining external replies are **BayLDA on consumer Gmail** and
**anschrift.net on its complete AVV and service activation**. SMTP2GO key limits,
tracking/archiving and retention are ordinary account-configuration checks below,
not a still-pending special-category approval process. No deployment was performed.

### Remaining account evidence

The DPA acceptance item is documented; do not ask the operator to accept it again.
Account configuration remains separate:

- The operator subsequently confirmed seeing **Hosted in the EU** and using
  `mail-eu.smtp2go.com` for Apple Mail. Record the account assignment and SMTP host
  as operator-confirmed, without requesting the same evidence again. The provider's
  [EU data-center documentation](https://support.smtp2go.com/hc/en-gb/articles/12974008254873-EU-Data-Center)
  identifies this SMTP host as EU/UK and offers `mail-eu2.smtp2go.com` for EU-only
  SMTP connections. This does not establish the region of the entire email chain.
  The application already uses its separate EU API endpoint. No SMTP host was changed.
- Record the dedicated sending-only key's 300/month cap, tracking and archiving
  settings, and applicable activity retention. These were not visible in the
  supplied export; that does not mean they are necessarily unset.
- Retain the agreement and acceptance evidence privately and subscribe to
  subprocessor-change notices. No provider settings or subscriptions were changed.
