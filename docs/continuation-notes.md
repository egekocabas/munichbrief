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
- On 14 September 2026 the operator requested removing the English-writing
  request from every Contact page. HTML, Markdown and all 14 locale catalogues
  omit it. Valid submissions in every language remain accepted.
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

## Remembered timeline navigation — 14 September 2026

- User approved retaining `?view=incident`, `/search` and pagination URLs for
  predictable SEO, bookmarks and browser history. Do not remove these routes.
- A bounded, versioned first-party cookie remembers timeline order for 30 days.
  Home links and the bare root address restore the preference and active search;
  explicit localized listing URLs remain authoritative. `/en` selects publication
  order even when the previous preference was incident order.
- Native report-return links retain view/search; the existing enhanced return
  restores the originating page, size and scroll position. Home resets page one.
- About and Privacy disclose the preference in all 14 locales. Only Privacy's
  independent content-update date advances to 14 September 2026.
- Validation: full web tests, focused race tests, vet, frontend generation,
  offline licence checks and documentation checks; browser flows with and without
  JavaScript include search, clearing, Home/root, explicit URLs and report return.
- The user's own localhost:8080 preview must not be stopped. This follow-up uses
  temporary ports 18082/19092 and stops only its own preview before delivery.

## Privacy wording follow-up — 14 September 2026

- User approved simplifying correspondence, retention and source-data sections in
  all 14 languages. Remove public explanations of EU API endpoint limitations,
  notification toggles/backlog behaviour and redundant website-versus-mailbox
  deletion wording. Preserve overseas-processing/provider information and the
  separate review/deletion of Gmail copies and sent replies.
- Source retention now says there is currently no fixed deletion period for those
  records. This is disclosure of the current position, not approval of indefinite
  retention or a promise of an unimplemented review/deletion process.
- Replace the generic editorial-law reminder with a reader-facing explanation
  that reports can identify people without names and an invitation to report
  personally relevant content or errors. Individual assessment remains stated.
- These are copy changes only; operational controls and internal legal/source
  assessments remain unchanged. Privacy's existing 14 September update date
  already covers this revision.

## Translation coverage layout — 14 September 2026

- The translations overview uses the viewport width with 16px side margins rather
  than the shared 72rem page cap. Its header and content remain aligned.
- Coverage retains an independently scrollable, keyboard-focusable table region.
  Positioning that region also contains absolutely positioned screen-reader
  labels, preventing them from extending the document's horizontal scroll area.
- Browser checks with synthetic model routes passed at 320, 390, 768, 1440, 1920
  and 2560 pixels in both themes: no document overflow, table scrolling on small
  screens and all columns fitting from 1440px in the fixture. Admin translation
  tests, frontend build and licence checks passed. Only generated CSS changed
  among inventoried assets; its reviewed fingerprint was refreshed. No server
  was started and the operator's preview was untouched.

## Haar headline disambiguation — 14 September 2026

- User requested recognizing the final “– Haar” location in headlines as Haar
  near Munich while retaining the common-noun ambiguity restriction.
- The matcher accepts that title suffix only for an existing municipality entry
  named Haar. Spaced en/em dashes and ASCII hyphens are accepted, including
  horizontal Unicode spaces. Body text, longer names, compounds and unmatched
  catalogue entries do not gain this exception. Existing preposition-based
  context remains available.
- The placeholder restores the exact name Haar and preserves the dash; it does
  not insert “(bei München)” into stored or translated reports. The admin Haar
  override row explains the rule. No database migration or bulk retranslation
  is performed; the updated rule applies to future translation processing.
- Full gazetteer race tests and focused matcher/admin race checks cover matching,
  negative cases, typed placeholders and round-trip restoration. Frontend,
  licence and documentation checks cover the admin wording update.
