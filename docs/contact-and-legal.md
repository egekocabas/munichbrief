# Contact, legal pages and private inbox

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

Rate-limit keys are HMAC pseudonyms held in a bounded in-memory map and expire
after 15 minutes; raw IPs are not saved with messages. Limits reset on restart.
Only explicitly trusted immediate proxy networks allow forwarded client addresses;
the rightmost untrusted address in the X-Forwarded-For chain is used. An arbitrary
CF-Connecting-IP header is ignored. Do not trust all networks. Confirm that the
ingress appends/rewrites the chain, and restrict origin access to the ingress.

## Configuration and activation

The feature is off by default. Local tests use a fake transport. This PR does not
send live mail, configure accounts or activate production features.

| Environment variable | Meaning |
| --- | --- |
| `MUNICHBRIEF_CONTACT_ENABLED` | Enable public receipt; requires admin enabled |
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

Before production activation, the operator must verify and record:

1. Applicable provider processing agreements, recipient/transfer arrangements and
   account-specific retention/location settings for Cloudflare, SMTP2GO, Gmail
   and the postal scanning service. Do not assume a personal Gmail account has
   Google Workspace contractual terms. COCENTER offers a processing agreement on
   request. These agreements and account settings have **not** been verified by
   this implementation. The public copy states known operations and attributes
   provider policies; it contains no invented account facts or placeholders.
2. A verified `contact@munichbrief.de` sender, a dedicated SMTP2GO key restricted
   to `/email/send`, **300/month provider-side key cap**, and open tracking, click
   tracking and optional archiving disabled for this key. The client uses
   `https://eu-api.smtp2go.com/v3/email/send`; verify the account's actual region
   separately. An EU endpoint is not an EU-only guarantee for the email chain.
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
