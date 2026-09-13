# Privacy — internal assessment and activation notes

This file replaces the original unpublished draft; `/de/privacy` and the other
13 language routes are public, with shared operator details. The confirmed
[processing inventory and decisions](contact-and-legal.md) supersede the earlier
unknown-identity and unknown-hosting statements. Account agreements and settings
still need operator verification before activation; no placeholder appears in
public pages and no contract acceptance is inferred from this implementation.

## Ordinary website and correspondence processing

Delivery/security, abuse protection, requested preferences, technical monitoring
and answering ordinary enquiries are assessed separately from editorial activity.
The public notice identifies legitimate interests under Article 6(1)(f) GDPR and
applicable legal obligations under Article 6(1)(c), with purposes and recipients.
Requested storage/security functions refer to §25(2)(2) TDDDG; this is not a blanket
analytics exemption. Retention is resolution-based: 90 days is the operator's
chosen policy, not a GDPR-mandated period. Read/unread does not affect it. Holds
require review, and copies outside SQLite require separate deletion decisions.

GDPR requests normally have the Article 12 one-month response period where those
rights apply. An absence of names in public summaries does not remove privacy
obligations for IP processing, correspondence or identifiable source material.
Backups are deferred, but losing messages does not automatically extinguish
applicable obligations. BayLDA is identified for ordinary non-public-sector
processing in Bavaria; the notice does not assign it every editorial complaint.

## Source data and editorial assessment

Police source text and RSS snapshots can concern identifiable people and alleged
offences even where public summaries omit names. Public availability alone is
not a sufficient legal basis; pseudonymization is not necessarily anonymization.
Article 10 GDPR restricts offence-related processing. Do not assume Article 6(1)(f)
alone authorizes all source records or that an AI-generated summary automatically
qualifies for a media exemption.

The service's selection, summarization, sourcing and corrections have an editorial
purpose. Article 85 GDPR, §23 MStV and applicable Bavarian press provisions may
provide a relevant framework, but whether this operator and each particular
processing activity qualify requires a service-specific assessment. Any privilege
is purpose-bound; it cannot simply be carried over to contact forms, infrastructure
logs, analytics or unrelated reuse. Document the scope and rationale with qualified
advice before claiming an exemption; this implementation makes no such claim.

Raw German sources and RSS snapshots currently have no automatic expiry. They
support local extraction, checking source changes and restricted review; public
pages expose resulting summaries, not stored originals. The existing policy is
unchanged by the contact migration. The need for each category and duration, Article
13/14 information duties, re-identification risks and correction/removal mechanisms
remain part of the source-specific assessment. Do not apply the inbox's 90-day
rule or monitoring's 30-day setting to source text by analogy. See the existing
[source policy](source-policy.md).

## Providers and transfers

The public notice distinguishes own German hardware/local AI from Cloudflare,
Google/Gmail, SMTP2GO and postal scanning. Linked provider notices are attributed
provider statements, not evidence of every account's region, contract or retention.
Confirm applicable processing agreements and transfer arrangements, including any
Article 46 safeguards, relevant adequacy coverage and ways to obtain details;
do not assert a provider/account certification without checking it. Personal Gmail
must not be described as a verified Workspace DPA arrangement. SMTP2GO's EU API
endpoint does not make Cloudflare routing, Gmail or other mail processing EU-only.

Cloudflare's supplied RUM setting excludes EU visitors. A page load without a
beacon cannot establish globally disabled analytics. Preserve and inspect the
restrictive CSP; distinguish configured injection, observed execution and edge
analytics. Zaraz/Google Tag Gateway remain inactive as reported. Do not add claims
about future tracking. Cloudflare retention is not established by Loki/Prometheus
configuration. COCENTER's six-month paper retention and scan-deletion criteria
are provider statements; arrange the relevant processing agreement and confirm
actual account options before accepting sensitive third-party postal data.

## Sources

- [GDPR, including Articles 5, 6, 10, 12–14, 28, 44–49 and 85](https://eur-lex.europa.eu/eli/reg/2016/679/oj)
- [Section 23 MStV](https://www.gesetze-bayern.de/Content/Document/MStV-23)
- [Article 11 Bavarian Press Act](https://www.gesetze-bayern.de/Content/Document/BayPrG-11)
- [Section 25 TDDDG](https://www.gesetze-im-internet.de/tdddg/__25.html)
- [BayLDA complaints](https://www.lda.bayern.de/de/beschwerde.html)
- [Cloudflare Web Analytics setup](https://developers.cloudflare.com/web-analytics/get-started/)
- [Cloudflare analytics FAQ](https://developers.cloudflare.com/analytics/faq/about-analytics/)
- [SMTP2GO API endpoints](https://developers.smtp2go.com/docs/endpoints)
- [SMTP2GO API authentication and key settings](https://developers.smtp2go.com/reference/authentication)
- [SMTP2GO rate limiting](https://developers.smtp2go.com/docs/rate-limiting)
- [SMTP2GO EU data center](https://support.smtp2go.com/hc/en-gb/articles/12974008254873-EU-Data-Center)
- [Google privacy](https://policies.google.com/privacy)
- [COCENTER / anschrift.net privacy](https://anschrift.net/datenschutzerklaerung/)
