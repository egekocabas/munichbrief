# Contact, legal pages and private inbox

When resuming this conversation, start with the
[current handoff and open-item summary](continuation-notes.md).

## Decision record — September 2026

This records the operator's decisions in the PR #67 discussion. Privacy and
Impressum are now public localized pages, replacing the earlier unpublished-draft
scope. The original reader redesign, search, timelines, source attribution and
About corrections remain in place. Stored AI translations are unchanged.

The operator explicitly authorized publication in the website and repository of:

> Ege Kocabaş  
> c/o COCENTER  
> Koppoldstr. 1  
> 86551 Aichach  
> Germany  
> contact@munichbrief.de

This is the purchased correspondence address, not the residence or server address.
The individual operator and editorially responsible person operates from Bavaria.
Website, database and all AI work run on the operator's hardware in Germany.
No home address, personal Gmail address, library address, print instructions,
COCENTER registration details, invented telephone, company or VAT number is published.
The common operator structure in `internal/web/legal.go` preserves the name's spelling.

Replies are in English; all valid submission languages are accepted. The email
chain is SMTP2GO → contact@munichbrief.de → Cloudflare Email Routing → the operator's
Gmail → Apple Mail. Replies use Apple Mail with SMTP2GO outgoing mail. The website
neither reads Gmail nor sends replies or acknowledgements to visitor addresses.

The supplied Cloudflare dashboard selected **“Enable, excluding visitor data in
the EU”** for Web Analytics/RUM. Zaraz consent management and Google Tag Gateway
were reported inactive; no other analytics service was reported. This is account
configuration evidence, not proof of execution in every browser. The restrictive
`script-src 'self'` policy remains unchanged. A browser can block Cloudflare's
injected beacon, while Cloudflare still processes delivery/security data and
produces server-side traffic statistics. Neither this PR nor its tests enables
tracking or changes Cloudflare settings. A fresh public `/en` browser load on
13 September 2026 returned Cloudflare headers and that CSP, loaded only self-hosted
scripts, and produced no observed analytics request. This single-load observation
does not establish behavior outside the EU or in every client.

Resolved messages have a 90-day deletion period; unresolved messages do not start
that clock. Gmail notifications and sent replies need separate manual review.
Automated backups, a backup provider and backup infrastructure are **deferred**.
SQLite protects against notification failure, not destruction of the database.
Gmail copies exist only when a notification actually reached that mailbox.

## Routes, storage and security

- `GET /{language}/impressum` and `/privacy` have localized HTML/Markdown, canonical
  URLs, language alternatives and sitemap entries across all 14 languages.
- `GET/POST /{language}/contact` uses a native form and post/redirect/get. Message
  contents never enter URLs, metadata, application logs or metric labels.
- `/admin/contact` and `/admin/contact/{id}` are inside the existing admin host
  restriction and ingress authentication boundary. Public hosts return 404.
  Do not expose the application port directly or treat an obscure hostname as
  authentication. There is no new public login or message API.
- Contact and admin pages use `private, no-store`; HTMX history snapshots are
  disabled. Error/receipt states and all admin pages are noindex.

Migration **024**, after the reader-index migration 023, adds `contact_messages`
and `contact_budgets`. Message and pending notification state occupy one atomic
SQLite insert. A unique hash of the signed submission token makes immediate
retries idempotent without storing another copy or sending another notification.
These tables are separate from incidents, AI jobs and the derived reader index.
No AI processing is run for correspondence.

The form accepts one email address, one enumerated topic and up to 5,000 Unicode
code points. Request bodies are bounded to 32 KiB. There are no attachments or
required names. Protection combines same-origin checks, a one-hour signed token
bound to an HttpOnly SameSite=Lax cookie, a hidden spam trap and three attempts
per IP per 15 minutes. Production cookies must be Secure. The five-minute receipt
cookie contains only a signed token, never correspondence. No consent checkbox
or external CAPTCHA is used. HTML is escaped, SQL is parameterized and email
recipients are fixed. A submitted sender address is not proof of identity.

Rate-limit keys are HMAC pseudonyms held in a bounded in-memory map with a
15-minute enforcement window starting at the first attempt. Expired
entries are removed on the next contact attempt or application restart; they
can remain in memory longer during inactivity. Raw IPs are not saved with
messages. Limits and aggregate form-attempt statistics reset on restart.
Only explicitly trusted immediate proxy networks allow forwarded client addresses;
the rightmost untrusted address in the X-Forwarded-For chain is used. An arbitrary
CF-Connecting-IP header is ignored. Do not trust all networks. Confirm that the
ingress appends/rewrites the chain, and restrict origin access to the ingress.

## Configuration and activation

The feature is off by default. Local tests use a fake transport. This PR does not
send live mail, configure accounts or activate production features.

| Environment variable | Meaning |
| --- | --- |
| `MUNICHBRIEF_CONTACT_ENABLED` | Deployment gate for contact receipt and sending; requires admin enabled |
| `MUNICHBRIEF_CONTACT_SECRET` | Random persistent signing secret, at least 32 bytes |
| `MUNICHBRIEF_SMTP2GO_API_KEY` | Dedicated sending-only server key; blank pauses email |
| `MUNICHBRIEF_CONTACT_DAILY_LIMIT` | Persistent UTC daily attempt budget; default 20 |
| `MUNICHBRIEF_CONTACT_MONTHLY_LIMIT` | Persistent UTC calendar-month budget; default 300 |
| `MUNICHBRIEF_CONTACT_TRUSTED_PROXIES` | Comma-separated exact ingress CIDRs; empty trusts none |

Use the existing Kubernetes Secret mechanism (`application.existingSecret`) for
both credentials. Never place values in Git, command logs or frontend assets.
Helm exposes `application.contactEnabled`, `contactDailyLimit`,
`contactMonthlyLimit` and `contactTrustedProxies`; enabling contact requires the
existing protected LAN admin ingress and an application Secret reference. Keep
`application.secureCookies` enabled on HTTPS. Keep a single application replica,
as with the existing SQLite deployment. Rotating the signing key expires open
forms and resets rate pseudonyms; it does not delete accepted messages.

Postal-service status update: the operator has purchased the Bayern service but
is awaiting identity approval and activation. The operator reports sending a
request for the complete AVV and its acceptance process. Confirm active postal
receipt before relying on this address in deployed legal pages; see the
[document review and correspondence record](legal-follow-up-2026-09.md).

SMTP2GO agreement update: the operator supplied DPA version 1.4 and dashboard text
recording acceptance on 27 August 2026 at 19:45 UTC. The agreement was reviewed.
The operator subsequently confirmed **Hosted in the EU** and Apple Mail's
`mail-eu.smtp2go.com` host (the provider's EU/UK SMTP service); key settings were
not shown in the export. The operator supplied Rick's support reply dated
13 September 2026, 22:52 UTC: SMTP2GO permits the described contact-form and
correspondence use under its normal terms, with no additional special-category
DPA or approval process. The requested clarification is resolved. Full-message
notifications remain as agreed; shorter alerts are optional, not a requirement
arising from this wording. See the linked correspondence record; do not request
the same clarification again. Other providers and account settings remain separate.

The [14 September mailbox and Cloudflare review](mail-provider-review-2026-09.md)
documents Cloudflare's incorporated DPA and Google's subscription agreements.
Provider roles must be assessed for each processing operation; a missing
standalone mailbox DPA is not by itself a finding of unlawful use. Mailbox
alternatives are proposals only. The Gmail/Apple Mail setup and full-message
notifications remain unchanged.

Before production activation, the operator must verify and record:

1. Applicable provider roles and agreements, recipient/transfer arrangements and
   account-specific retention/location settings for Cloudflare, SMTP2GO, Gmail
   and the postal scanning service. Do not assume a personal Gmail account has
   Google Workspace contractual terms. COCENTER offers a processing agreement on
   request. SMTP2GO's supplied acceptance evidence is recorded above; remaining
   agreements and account settings must be checked individually. The public copy
   states known operations and attributes provider policies; it contains no
   invented account facts or placeholders.
2. A verified `contact@munichbrief.de` sender, a dedicated SMTP2GO key restricted
   to `/email/send`, **300/month provider-side key cap**, and open tracking, click
   tracking and optional archiving disabled for this key. The client uses
   `https://eu-api.smtp2go.com/v3/email/send`; the operator has confirmed the
   account's EU hosting indicator. An EU endpoint is not an EU-only guarantee
   for the email chain, and the reported Apple Mail SMTP host also covers the UK.
3. A working protected admin route, correct public-host allowlist, trusted proxy
   chain, secret injection and regular independent inbox/monitoring checks.
   A form needs an effective response process; keeping receipt enabled is not a
   substitute for responding. Email quotas must not silently disable receipt.
4. Review the source-data assessment in [privacy review notes](privacy-draft.md).
   Reconcile changed operational facts in every locale before activation.

Dedicated-key budgets preserve room for normal replies but do not create another
SMTP2GO account allowance. Both automated notifications and Apple Mail replies
consume the account's plan quota. Account/key caps must be configured by the
operator; the application cannot verify or change them.

## Admin controls, budgets and test delivery

Migration 025 adds a singleton `contact_settings` row and an `is_test` marker on
contact messages. Both admin controls default to on, preserving existing behavior;
`MUNICHBRIEF_CONTACT_ENABLED` remains the deployment gate, and sending still
requires a configured SMTP2GO key. No additional environment variables or infra
secrets are needed for these controls. They survive restarts in SQLite.

The private Messages page provides two independent native POST controls:

- **Accept form submissions:** closing the form leaves the email address visible.
  Valid forms opened before closure receive HTTP 503, explicitly say the message
  was not sent, and display escaped, read-only fields for copying into an email.
  No correspondence is put in URLs, browser storage or metadata. Requests still
  undergo origin, token, size and rate-limit checks. A retry of an already saved
  enquiry acknowledges its original receipt even after closure.
- **Send email notifications:** disabling pauses already queued notifications.
  New enquiries are saved as inbox-only (`cancelled`, `notifications_disabled`)
  and are not automatically mailed after re-enabling. Existing pending/retry work
  resumes when enabled. Closing the form alone does not pause ordinary queued mail.

Settings and message admission/notification claims share the SQLite transaction
boundary. A message already admitted or email already claimed may complete before
an admin change takes effect; a provider request cannot be recalled. Pending test
emails are cancelled when either control is disabled, preventing unexpected later
sends. All admin POSTs require the protected host, same-origin checks and signed
CSRF tokens; responses are private/no-store.

**Email budget** cards show configured totals, attempts used, remaining attempts
and the next UTC reset for the daily and monthly periods. These are the persistent
application budgets, not a live SMTP2GO account balance: Apple Mail replies consume
provider allowance but are not counted here. Failed/uncertain attempts and tests
consume reservations. Lowering a limit never displays negative remaining quota.
A separate abuse-protection section shows the per-IP 3-attempt/15-minute policy,
active windows and aggregate passed/blocked attempts since process start, without
exposing raw IPs or pseudonymous identifiers. Passing the limit is not receipt.

**Send test email** creates a clearly marked synthetic inbox entry and uses the
normal asynchronous worker, fixed envelope and SMTP2GO implementation. Both admin
controls and deployment support must be enabled, the key must be configured, and
budget must remain. Only one test can be queued/sending at once. Repeated POSTs
with the same action token reuse the test entry; the worker reserves quota at send
time. Other work may use the remaining budget before the test is claimed. The
result remains visible as pending, accepted by SMTP2GO, retry, failed or uncertain;
acceptance is not confirmation of Gmail delivery. A test creates no public form
receipt or received-enquiry metric and follows normal resolution/retention rules.
Local verification uses a fake sender; no live test email is sent during development.

## Delivery, retention and recovery

The contact worker runs independently of AI schedules and pause controls. It
recovers interrupted `sending` work as `uncertain`, runs retention on startup
and hourly, then checks for notification work every 15 seconds. Cleanup still
runs when public receipt is disabled. Keep the signing secret and protected
admin access configured to inspect existing correspondence during such a pause.

Each send reserves daily and monthly budget in SQLite before contacting the
provider. Reservations survive restarts and include failed or uncertain attempts.
A success means **accepted by SMTP2GO**, not delivered to Gmail. Explicit temporary
failures use bounded exponential backoff and Retry-After; permanent rejection
requires manual retry. Ambiguous timeouts, malformed responses or interrupted
sends require review rather than automatic duplicate delivery. Before retrying an
uncertain message, check Gmail and provider activity; retries still use budget.
Fixed sender/destination prevent the contact form becoming a relay.

Unread/read state and resolution are independent. Resolving starts the 90-day
period and cancels obsolete pending notifications. Reopening cancels the deletion
deadline; resolving again starts a new one. A hold requires a reason and review
date within 90 days; an expired review date prompts review rather than silently
removing the hold. Cleanup deletes correspondence and its notification content
together. Aggregate budget rows contain no correspondence and are removed after
two months. Mutations during an in-flight send are refused; retry after it ends.

Review ageing unresolved messages and overdue holds regularly. Delete Gmail
notifications and sent correspondence separately once no longer needed under the
same resolution-based policy, allowing documented exceptions for continuing
obligations/disputes. Website deletion does not remove provider activity records,
Gmail copies, postal scans or an operator's independently made database copy.
Provider retention and existing 30-day monitoring are separate; the contact rule
does not delete police source records. No automatic backup is promised or added.
If the database is lost, restore only from an independently available copy; do not
claim unsent messages can be recovered. Handle missed deadlines and requests
individually rather than assuming data loss cancels an obligation.

## Monitoring

The private metrics endpoint exposes aggregate `munichbrief_contact_*` series:
received total, notification failures total, cleanup failures total, pending count,
problem count, oldest pending seconds and notifications-paused state. No message
IDs, topics, addresses, tokens or bodies enter metric labels. Existing Prometheus
scraping includes these series; no browser analytics integration is added.

Use independent monitoring for increasing failures, paused sending with pending
work, ageing backlog and cleanup failures. Admin shows persistent warnings and a
notification-problem filter. Do not rely on SMTP2GO to alert on its own outage.
Check ingress authentication and admin availability separately. Aggregate counters
may reset on process restart; sending-budget counters are persisted in SQLite.

## Validation

Use repository checks plus the contact store, worker, HTTP and config tests. All
transport tests are fake. `BenchmarkContactInbox10000` measures a 10,000-message
synthetic inbox with pagination and total count; no SMTP call is on the SSR path.
Visual evidence uses synthetic correspondence only. See the PR for current local
results; screenshots are under [previews](previews/).

A local three-iteration synthetic sample on Apple M1 returned an inbox page and
count from 10,000 messages in approximately 3.3 ms. Reader-index regression samples
were 56–58 ms for chronological/short-Chinese queries and 96 ms for text search
while other verification tasks were running. These are local samples, not SLAs.

Final browser validation covered 336 combinations of the 14 locales, three public
pages, four viewport widths (320/390/768/1440) and both themes with no horizontal
overflow. Native form submission succeeded with JavaScript enabled and disabled;
keyboard focus, clean redirects, admin resolution, HTMX metadata navigation and
back restoration were checked. All 264 locale keys and placeholders match.

Selected screenshots contain synthetic correspondence only:

- [Contact, mobile light](previews/contact-390-light.png)
- [Contact, Turkish mobile dark](previews/contact-turkish-mobile-dark.png)
- [Legal notice, desktop](previews/impressum-1440-light.png)
- [Privacy, desktop dark](previews/privacy-1440-dark.png)
- [Private inbox](previews/contact-inbox-desktop.png)
- [Private message, mobile dark](previews/contact-message-mobile-dark.png)

### Admin controls follow-up verification

The full repository race suite passed after introducing the controls, with the
web package completing in 402 seconds. Final focused race tests additionally
covered native settings actions, deployment gates, CSRF/public-host restrictions,
closed-form draft preservation, already-accepted retries, inbox-only receipt,
settings persistence, test cancellation/idempotency, UTC budget resets and all
14 Privacy HTML/Markdown copies. The final Go analysis, dependency audit,
formatting/module checks, documentation links, workflow/shell checks, application
build and Helm contact/strict lint checks passed.

Synthetic browser checks found no horizontal overflow at 320, 390, 768 and 1440
pixels in either admin theme. Controls and test queuing worked with JavaScript
disabled; a separate visitor tab retained its draft after the operator closed the
form. The fake sender progressed a test from pending to accepted without network
email. Inbox paging over 10,000 synthetic messages measured approximately 1.2 ms
per page/count query on Apple M1; this is a local sample, not an SLA.
Temporary port 18087 was stopped and port 8080 remained closed.

- [Contact controls and budgets, desktop](previews/contact-controls-desktop.png)
- [Contact controls, mobile dark theme](previews/contact-controls-mobile-dark.png)
