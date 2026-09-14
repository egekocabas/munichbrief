# Mailbox and Cloudflare review — 14 September 2026

Last reviewed: **14 September 2026**.

This internal research record supplements the
[provider correspondence record](legal-follow-up-2026-09.md). No subscription was
purchased, agreement accepted, support message sent, DNS record changed or live
email sent during this review. Full-message notifications remain unchanged.

## Recommendation and the limit of the earlier DPA advice

Do not require a mailbox subscription merely because a separate DPA has not been
found. The provider's role must be assessed for the specific processing.
Transport, mailbox storage and its own account/security processing need not have
the same role.

[BayLDA's current distinction guidance](https://www.lda.bayern.de/media/veroeffentlichungen/Abgrenzungshilfe_Auftragsverarbeitung.pdf)
withdraws the former blanket list of service categories and requires examining
who determines purposes and essential means. Storage performed on a customer's
instructions can be processing on behalf of that customer. A product name alone
does not determine the answer. This does not establish that the operator's
particular free-Gmail arrangement is either prohibited or fully assessed.

An explicit mailbox processing agreement is a practical way to clarify the
contractual arrangement if the operator chooses to change it. It is not a legal
certificate or a substitute for appropriate purposes, security, deletion and
transfer safeguards. Do not present the earlier personal-Gmail concern as an
established requirement to upgrade to Google Workspace.

Retain the current technical design. SMTP2GO's requested scope clarification has
been received and supports the described use under its normal terms; the BayLDA
and anschrift.net AVV enquiries remain pending. The postal service's identity
verification and activation were confirmed in the subsequently supplied order
update of 14 September, recorded in the correspondence notes. If a separate
project mailbox is wanted, IONOS Mail Basic is a
concrete low-cost candidate with an express AVV route and Apple Mail support.
This recommendation concerns documented fit, not an audit of its systems or the
operator's future contract. Subscription and migration remain an operator decision.

## Cloudflare: agreement documented, account settings separate

[Self-Serve Agreement §6.1](https://www.cloudflare.com/terms/) incorporates the DPA
for covered processing. The published
[DPA version 6.4, effective 3 April 2026](https://www.cloudflare.com/cloudflare-customer-dpa/),
was reviewed. Annex 1 describes customer content routed through its services and
anticipates special-category content submitted by end users, applying Annex 2
safeguards. Its wording expressly addresses this content; SMTP2GO has now
separately clarified the meaning of its schedules in the support reply recorded
in the linked correspondence notes. Neither establishes MunichBrief's own legal
basis for every message.

The DPA does not establish EU-only processing or universal 30-day retention. Its
twelve-month infrastructure-access logging measure is not a retention statement
for MunichBrief visitor traffic or routed email. No additional bespoke Cloudflare
agreement request is needed solely because its DPA is incorporated into standard
terms.

The supplied Web Analytics/RUM setting remains **“Enable, excluding visitor data
in the EU.”** Zaraz consent management and Google Tag Gateway remain reported
inactive. Earlier browser observations and restrictive CSP are recorded in the
[operational inventory](contact-and-legal.md); they do not prove analytics is
disabled globally. No new browser-execution test or account-settings audit was
performed in this mailbox review.

Email Routing remains a forwarding service; see
[Cloudflare's postmaster documentation](https://developers.cloudflare.com/email-service/reference/postmaster/).
The newer Email Sending beta is separate and is not part of this architecture.
Do not enable it or replace SMTP2GO as part of this work.

## Google: an explicit contractual route exists

Subsequent clarification on 14 September: Google's
[Privacy Help Center, Enterprise customer requests and resources](https://support.google.com/policies/answer/9581826?hl=en)
explicitly states that consumer Gmail has no DPA and that Google does not act as
a processor for that service. This is Google's published role assessment; it
does not bind a regulator's assessment of the actual operation. The remaining
question is whether the existing consumer-mailbox arrangement is suitable for
MunichBrief correspondence, not whether a hidden consumer DPA can be obtained.

That question is independent of SMTP2GO's clarification and anschrift.net's pending reply.
Neither provider can settle Google's role or contractual coverage. The earlier
statement that only waiting remained was too broad. BayLDA's
[advisory enquiry service](https://www.lda.bayern.de/de/beratung.html) is a route
for specific guidance without commissioning a private legal opinion; responses
may take time and should not be represented as advance certification. The
[German enquiry and receipt](baylda-mailbox-enquiry-draft.md) record its submission
on **14 September 2026 (Europe/Berlin)** after explicit operator approval. BayLDA's
page confirmed successful delivery. The website's name, domain and URL were
omitted as requested. **A response is pending**; no duplicate enquiry is needed.
Google also provides a [privacy enquiry form](https://support.google.com/policies/contact/general_privacy_form)
if clarification of its own service terms is needed.

[Workspace Individual terms §§2 and 4](https://workspace.google.com/terms/google-workspace-individual-terms/)
include Gmail and incorporate a processor agreement for non-household use. The
newer [Workspace Personal terms, dated 29 April 2026](https://workspace.google.com/terms/workspace-personal-terms/),
also describe a subscription for business use with a personal Gmail account.
These are possible routes, not terms inferred for the existing free Gmail account
or an unrelated Google One subscription.

The previously inaccessible legacy DPA link now redirects to the
[Google Data Processing Addendum, version 10, dated 7 May 2026](https://business.safety.google/processorterms/).
Its full text and appendices were read through the browser. It covers processing
on documented instructions, security, deletion, incidents and subprocessors.
Section 10 permits international processing; the regional appendix provides
transfer mechanisms. It is not an EU-only storage promise or evidence that a
particular entity's certification was independently checked. Additional products
outside the agreement require separate assessment. No acceptance for this
operator was recorded.

The [service-information table, dated 27 April 2026](https://business.safety.google/services/)
lists both Workspace Individual and Workspace Personal, including data submitted,
stored, sent or received through covered products for non-household use.
Eligibility, the precise available plan and the current German checkout price
were not verified in the operator's account. Do not quote an assumed price or
say an ordinary Gmail login alone activates these terms.

## Alternative mailbox findings

| Candidate | Verified public information | Practical consequence |
| --- | --- | --- |
| IONOS entry mailbox / Mail Basic | The [German product page](https://www.ionos.de/office-loesungen/eigene-email-adresse) advertises one 2 GB mailbox for €1.50/month, monthly cancellation. The [published AVV](https://www.ionos.de/hilfe/fileadmin/pdf/de_DE/Datenschutz/Vertrag_zur_Auftragsverarbeitung_AVV_.pdf), version 20210804, lists Mail Basic and Mail Business. | A separate project mailbox is possible. Check the actual order and current agreement before purchase; the public template is not an executed operator contract. |
| Posteo | The [provider homepage](https://posteo.de/en) advertises €1/month, 4 GB and IMAP access. Its [AVV FAQ](https://posteo.de/en/site/faq#avv) declines an AVV, asserting that its communications service is not processing on behalf of customers. | Do not recommend Posteo as supplying an AVV. Its stated position illustrates why absence of an AVV alone is not proof that professional mailbox use is prohibited. Do not generalize it to Google's different services. |

IONOS publishes an [AVV information and conclusion route](https://www.ionos.de/hilfe/datenschutz/allgemeine-informationen-zur-datenschutz-grundverordnung-dsgvo/vereinbarung-zur-auftragsverarbeitung-avv-mit-ionos-abschliessen/).
Its template addresses special-category safeguards in §3(2), with a separate
security annex and subprocessor information. Obtain the complete applicable
documents with any eventual order; this review does not confirm an account's
accepted version or every downstream processing location.

[IONOS's Apple Mail instructions](https://www.ionos.de/hilfe/e-mail/weitere-e-mail-programme/ionos-e-mail-konto-in-apple-mail-einrichten/)
provide the IMAP setup. Its [external-domain documentation](https://www.ionos.de/hilfe/domains/externe-domain-bei-11-ionos-einrichten-und-verwalten/externe-domain-bei-11-ionos-einrichten/)
allows keeping existing nameservers. A future migration could keep Cloudflare
DNS and the website proxy, change mail-delivery records, and read the project
mailbox in Apple Mail. Application notifications could continue through SMTP2GO
under its normal terms and the received clarification. Plan every existing domain mail route and
sender-authentication record before any DNS change; this affects more than one
forwarding address.

## Next steps and evidence still needed

1. **SMTP2GO scope clarification received:** Rick's reply dated 13 September 2026,
   22:52 UTC permits the described form/correspondence use under normal terms,
   with no additional special-category DPA or approval process. The
   [correspondence record](legal-follow-up-2026-09.md) contains the assessment.
   Keep full-message notifications; no duplicate support enquiry is needed.
2. Review the complete anschrift.net AVV, annexes and conclusion process when
   received. Identity verification and activation are now confirmed; this does
   not resolve the separate AVV request. Do not resend the support enquiry.
3. Review BayLDA's response to the submitted consumer-Gmail enquiry when supplied.
   This assessment is separate from the other providers' replies. If the
   operator chooses a mailbox subscription or migration, record its actual
   terms and settings, then update Privacy in all 14 locales to match operations.
   No purchase is needed to complete this research.
4. Complete existing SMTP2GO key/budget/tracking/retention and protected-inbox
   activation checks in [the runbook](contact-and-legal.md). Do not ask again for
   the already-recorded DPA acceptance or EU-hosting indicator.

No account or support mailbox was accessed. Replies must be supplied by the
operator; no background monitoring was created. No implementation, public-policy
date, deployment status or production feature flag changed.
