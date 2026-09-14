# Conversation handoff — read first when resuming

Last updated: **14 September 2026 (Europe/Berlin)**.

The operator asked for persistent notes before changing topics. This is the
starting point for resuming the reader/contact/legal work; detailed evidence and
implementation notes are linked below. Preserve distinctions between implemented
behaviour, supplied evidence, research conclusions and proposed changes.

## Current work and delivery boundary

- Working branch: `codex/reader-discovery`; licence additions build on `fea34b7`.
- Existing [MunichBrief PR #67](https://github.com/egekocabas/munichbrief/pull/67)
  title: **`feat(web): improve reader experience, search, and incident timelines`**.
  Continue that PR; do not create a duplicate.
- The explicit licence implementation instruction authorizes conventional commits
  and pushing this completed update. The earlier local provider/correspondence
  notes are preserved with this update; their evidence/status boundaries remain.
- No merge, deployment or wait for PR CI is authorized by the existing PR task.
  Live SMTP sending/account changes are outside that implementation task unless
  separately requested. Do not infer account configuration from application code.
- Keep **port 8080 closed**. Use temporary preview ports and stop them after use.
- The conversation also included a companion `homelab-infra` PR for configuration
  and secrets, including Traefik/proxy handling. Its current PR number, branch,
  checks and merge status are not verified in these local notes. Resolve the
  existing PR before editing it; do not invent a status or open a duplicate.

## Provider and correspondence status

| Item | Current status | Resume action |
| --- | --- | --- |
| SMTP2GO special-category wording | **Clarification received and resolved.** Rick's reply of 13 September 2026, 22:52 UTC permits the described contact form and correspondence under normal terms. No additional DPA or special approval process is required. | Keep full-message notifications. Do not ask the same support question again. Original reply and DPA evidence should be retained privately. |
| SMTP2GO thanks/ticket closure | A short English thanks message, including thanks for the quick reply, was drafted in chat. | Sending that thanks or provider-side ticket closure has **not been confirmed**. Do not claim the assistant sent it. |
| BayLDA / consumer Gmail | **Enquiry submitted; response pending.** The assistant submitted the reviewed German form with explicit approval on 14 September. The page confirmed successful delivery; no reference number appeared. | Review the reply when the operator supplies it. Do not resubmit. This question is independent of SMTP2GO and the postal service. |
| anschrift.net / COCENTER | **Reply and activation confirmation pending.** Bayern service purchased; latest supplied status was awaiting ID approval/activation. The operator already sent a request for complete AVV documents and how to conclude them. | Review the complete applicable AVV, security/subprocessor annexes, acceptance process and postal-receipt activation when supplied. Do not resend the enquiry. |
| Cloudflare | Incorporated standard DPA documented. Supplied analytics settings are recorded separately from browser observations and account retention. | Preserve settings and CSP. Account-specific facts still require evidence; do not infer EU-only processing or a universal 30-day retention period. |

Google explicitly states that consumer Gmail has no DPA and that it does not act
as a processor for that consumer service. Whether this arrangement is suitable
for the particular website-correspondence workflow is the question sent to BayLDA.
Do not conclude either that free Gmail is prohibited solely because no DPA exists,
or that it has been conclusively approved. Workspace/IONOS/Posteo research is
comparative only: **no mailbox purchase or migration was approved or performed**.

No support mailbox has been connected or checked, and no recurring monitoring was
created. Replies arrive through the operator. “SMTP2GO clarification resolved”
does not mean its account settings or the full deployment have been verified.

## Decisions and preferences to preserve

- Calm, fast Go SSR with progressive enhancement; pleasant mobile layouts in both
  themes. Preserve the existing reader redesign, search, publication/incident
  timelines, pagination, SEO and natural interface wording across 14 languages.
- Privacy and Impressum have **public localized routes in the PR**, despite older
  filenames containing `draft`. Do not restore the obsolete unpublished-page scope
  or infer that PR code has been deployed.
- Public operator identity and purchased correspondence address remain the exact
  details in [the decision record](contact-and-legal.md). The private postal
  address and Gmail address supplied for the BayLDA form are **not authorization
  to replace the public legal identity/contact block or publish them in Git**.
- The BayLDA enquiry deliberately omitted the website name, URL and domain email
  address. Correspondence with the German authority can be in German; translate
  and explain its reply for the operator. No English response was requested.
- The public Contact page still asks visitors to write in English so the operator
  can reply, while accepting valid submissions in other languages. The authority
  correspondence preference does not change this website policy.
- Form messages are stored in SQLite before success. Full-text notifications go
  through SMTP2GO to the project contact address, then Cloudflare Email Routing
  and the operator's Gmail. Apple Mail reads Gmail; replies use SMTP2GO. No visitor
  autoresponder or public message relay is authorized. Minimal alerts were only
  an optional proposal and remain unimplemented.
- SMTP2GO DPA v1.4 acceptance on 27 August 2026, 19:45 UTC, the **Hosted in the EU**
  indicator and Apple Mail's `mail-eu.smtp2go.com` setting are already supplied.
  Do not request the same evidence again. That SMTP hostname covers EU/UK; the
  application's API endpoint is separately configured for the EU.
- Notifications have application budgets of **20/day and 300/month** and share
  the account allowance with replies. Retain form/sending controls, admin budget
  displays, test notification behaviour and abuse protections documented in the
  runbook. Fixed notification recipients and no AI processing of correspondence
  remain important boundaries.
- Resolution starts the agreed **90-day** inbox deletion period. Reading alone
  does not; reopening cancels it. Holds need a reason and review date. Gmail and
  sent-mail copies require separate manual review. **Automated backups remain
  deferred**; SQLite does not protect against deletion of the database.
- Cloudflare Web Analytics/RUM setting: **“Enable, excluding visitor data in the
  EU.”** Zaraz consent management and Google Tag Gateway reported inactive; no
  other analytics service reported. Preserve restrictive CSP and distinguish this
  configuration from observed browser execution and server-side analytics.
- Keep About's initial-publication/background-verification sequence accurate.
  Do not restore the rejected AI-icon legal-compliance sentence, repeated source
  warning on cards, “not in the URL” search explanation, or detailed expiry wording
  in About. Detailed retention belongs in Privacy; source attribution is a list.

## Still open beyond waiting for replies

Before production activation, verify the dedicated sending-only key and provider
monthly cap, tracking/archiving and activity-retention settings, secret injection,
protected admin access, public-host restrictions, trusted proxy chain and the
other checks in [the activation runbook](contact-and-legal.md). Their unverified
status is not evidence they are necessarily configured incorrectly.

Source-to-summary consistency checks before first publication, a differentiated
raw-source/RSS-snapshot retention implementation and further source/editorial
assessment remain **research recommendations, not implemented features or agreed
deletion schedules**. They are described in the legal follow-up. Do not silently
delete source data, claim human review, or treat contact retention as source policy.

## Licence implementation completed in this PR update

- A reviewed inventory covers 54 components, 55 full notice texts and exact
  records for 17 installed artefacts observed on Ollama 0.33.3. Models remain
  separately installed; none were selected, removed, downloaded or changed.
- Nine Apache-model artefacts are reviewed for internal generation, two have
  custom/inherited conditions, four Tower variants remain unresolved, and two
  SalamandraTA variants are explicitly outside intended use. The operator's
  correction was **not going to use SalamandraTA**; do not reverse it.
- Public credits/downloads exist across all 14 locales, with original legal texts,
  metadata, sitemap links and an independent content-update date. Unresolved and
  excluded models are absent from public model credits. The admin overview and
  inline warnings compare exact cached digests without blocking processing.
- Offline CLI and release images include notices. Both Linux architectures have
  been built and inspected, preserving Debian notices and exact corresponding
  sources for relevant base packages. CI checks inventory drift and each image.
- Fresh validation includes full Go race tests, focused final licence race tests,
  frontend generation/audit, docs, formatting/modules, vet/staticcheck,
  govulncheck/deadcode, actionlint/ShellCheck, build and Helm checks. Browser
  verification covers 112 public locale/width/theme combinations plus eight
  synthetic admin combinations, native disclosures, focus and HTMX history.
- No inference, live email, provider change, merge or deployment was performed.
  Temporary previews are stopped before delivery; port 8080 remains closed.

## Where the detail lives

- [Third-party licensing review](licensing-review.md): implemented inventory,
  notices and credits, exact model review, unresolved terms and maintenance steps.
- [Reader discovery](reader-discovery.md): SSR behaviour, search persistence,
  chronology, migration 023, SEO, benchmarks and visual verification.
- [Contact/legal decision record and runbook](contact-and-legal.md): authorized
  public identity, all contact/admin decisions, migrations 024/025, settings,
  retention, abuse protection, operational limitations and prior validation.
- [Editorial/provider follow-up](legal-follow-up-2026-09.md): legal reasoning,
  source-policy proposals, original provider-document findings, sent enquiries
  and Rick's received SMTP2GO clarification.
- [Mailbox/Cloudflare review](mail-provider-review-2026-09.md): Google role
  distinction, alternatives, contract evidence and remaining steps.
- [Submitted BayLDA text and receipt](baylda-mailbox-enquiry-draft.md): generic
  German enquiry, submission confirmation and privacy boundary. The historical
  filename is retained for links; the enquiry is already submitted.
- [Legal-page comparison](legal-review-2026-09.md),
  [Privacy assessment](privacy-draft.md), [Impressum notes](impressum-draft.md),
  [source policy](source-policy.md): public-copy scope and assessment limits.
