# Contact operations and legal pages

MunichBrief serves localized Contact, Privacy and Impressum pages. The shared
operator information in `internal/web/legal.go` supplies the public identity and
correspondence address in HTML and Markdown. Keep it accurate and maintain the
address service while publishing that address. The public pages describe actual
processing; review them whenever providers, retention or operations change.

## Enable support without opening the form

Contact availability is managed by the two persistent admin controls. Configure
protected administration and `MUNICHBRIEF_CONTACT_SECRET` to make those controls
usable; adding `MUNICHBRIEF_SMTP2GO_API_KEY` also makes sending available.

1. Migration **026** closes both public receipt and email notifications once on
   upgrade, including databases used to preview migrations 024/025. It preserves
   correspondence and budgets, and cancels queued test notifications. Later
   restarts preserve the administrator's settings.
2. Open `/admin/contact` through the authenticated private ingress. Leave both
   controls off until ready. The public Contact page offers email instead; an
   already-open form cannot bypass the closed state.
3. Enable **Accept form submissions** when ready to accept enquiries. Enable
   **Send email notifications** separately after verifying the sender, key and
   limits. The test-email button requires both controls and remaining budget.

`MUNICHBRIEF_CONTACT_ENABLED` / Helm `application.contactEnabled` is retained as a
legacy opt-in prerequisite check: true requires the contact prerequisites at
startup/chart rendering. **False does not prevent admin activation of receipt or
sending.** Close the form or pause sending with the admin controls, which remain
authoritative across restarts. A missing signing secret or protected admin still
prevents contact operation; a missing SMTP2GO key prevents sending.

There is no public admin login: authentication is provided by ingress, and public
hosts return 404 for admin routes. Do not expose the application port directly to
untrusted clients. Before a production rollout, confirm provider agreements, the
published privacy notice, the ability to handle requests and ingress/secret
configuration. Merging code does not activate a deployment or establish provider
agreements.

## Configuration

| Environment variable | Meaning |
| --- | --- |
| `MUNICHBRIEF_CONTACT_ENABLED` | Legacy prerequisite check; default false; admin controls take precedence |
| `MUNICHBRIEF_CONTACT_SECRET` | At least 32 characters of random signing material |
| `MUNICHBRIEF_SMTP2GO_API_KEY` | Dedicated send-only server credential; optional for inbox-only operation |
| `MUNICHBRIEF_CONTACT_DAILY_LIMIT` | Notification attempts per UTC day; default 20 |
| `MUNICHBRIEF_CONTACT_MONTHLY_LIMIT` | Notification attempts per UTC month; default 300 |
| `MUNICHBRIEF_CONTACT_TRUSTED_PROXIES` | Comma-separated ingress CIDR allowlist; default trusts none |

Supply secrets through `application.existingSecret`; never commit their values.
Keep `application.secureCookies` enabled with HTTPS. Trust forwarded client
addresses only from configured ingress peers. Verify the ingress replaces
untrusted forwarding headers and preserves the browser's HTTPS scheme. Keep one
application replica for SQLite and the notification worker. Signing-key rotation
expires outstanding forms and resets IP-derived pseudonyms, not saved messages.

## Submission and privacy

The native form accepts an email address, topic and up to 5,000 Unicode code
points. It requires no name or attachment. Email validation checks syntax and a
public-style domain, not mailbox existence. Submitted text never enters URLs,
metadata, logs or metric labels. HTML output escapes correspondence.

Requests use bounded bodies, same-origin checks, signed one-hour tokens, a hidden
spam trap and three attempts per IP per 15-minute window. A stable browser
binding expires after its original hour; each form has a separate signed nonce
for CSRF and submission deduplication. Opening another tab does not invalidate
existing forms. Recoverable expiry and rate-limit responses preserve escaped
drafts; rate-limited retries retain their original submission nonce. IP-derived keyed hashes
remain in a bounded in-memory map; expiry is enforced at 15 minutes and entries
are removed at the next contact attempt or restart. No raw IP is stored with a
message. Visitors sharing an IP share the allowance. Metrics contain totals only.

SQLite stores correspondence and notification state in one transaction before
acknowledgement. A token identifies one submission: a repeated accepted request
cannot create another message or notification. A storage failure returns an
error. SMTP failure never changes a successfully stored message into a failed
submission. Contact pages use `private, no-store` and disable HTMX snapshots.

Closing the form leaves email available. A valid outstanding form receives HTTP
503 with a clear not-sent message and an escaped draft to copy. Already-accepted
retries retain acknowledgement. Turning off receipt does not cancel real queued
notifications; turn off email notifications as well to pause those.

## Notification delivery

The worker is independent of AI scheduling and runs every 15 seconds. It uses
`https://eu-api.smtp2go.com/v3/email/send`, fixed sender and destination
`contact@munichbrief.de`, visitor Reply-To and plain-text correspondence. There
are no visitor autoresponses, attachments or user-selected recipients.

Configure a verified sender and a dedicated key restricted to sending with a
matching 300/month provider-side cap. Disable open/click tracking and optional
archiving in the provider account. The application cannot verify those account
settings. Form notifications and manual replies share the account allowance;
a separate key does not create a separate plan quota.

Reservations count against persistent UTC budgets before sending, including
ambiguous attempts. Temporary failures back off; permanent rejection stops that
message. Ambiguous transport outcomes and interrupted sends require admin review.
Success means **accepted by SMTP2GO**, not confirmed delivery to a mailbox. Admin
retry still respects controls and budgets. Check problems in admin and monitoring
without relying on email to report its own outage.

Disabling notifications pauses existing queued messages. New enquiries are saved
as inbox-only and are not automatically emailed later. Re-enabling resumes the
previous queue. The admin switch displays the saved choice separately from
configuration availability. If credentials are missing, an enabled saved choice
can still be switched off, preventing automatic resumption when credentials
return. A send already claimed may finish. Disabling either control
cancels queued tests; a synthetic test uses the same worker, recipient and budget.

## Inbox and retention

The protected inbox supports filters, pagination, read/unread, resolve/reopen,
retry, confirmed deletion and a mail reply link. It has no reply composer or
mailbox synchronization. Admin mutations require signed tokens and same-origin
checks. Message contents and sender addresses are visible only inside the
protected inbox and outgoing notification.

- Resolution starts a 90-day deletion period; reading alone does not.
- Reopening cancels the period; resolving again starts a new one.
- Unresolved messages need periodic review; age indicators highlight old items.
- A retention hold requires a reason and a review date within 90 days. An overdue
  hold remains in effect until explicitly reviewed and released.
- Startup/hourly cleanup deletes eligible correspondence and notification data.
  Messages being sent are not deleted mid-send.
- Mailbox notifications, sent replies and postal copies require separate review
  and deletion. The app cannot delete provider-held copies.

Contact retention does not apply to source records or monitoring logs. Metrics
include receipts, notification failures, queue age and cleanup failures; see
[operations](operations.md). Application logs omit correspondence and credentials.

Automated backups are not provided. The inbox survives notification failure and
application restarts, but not deletion or loss of its database. Arrange and test
an independent database copy before upgrades. Mailbox copies exist only for
notifications that actually reached the mailbox.

## Migrations and verification

Migration 024 adds contact messages and budgets, 025 adds persistent controls and
test markers, and 026 closes receipt/sending once for deliberate activation.
Applied migrations remain immutable. No migration regenerates AI reports or
changes source data.

Regression tests cover disabled receipt, stale forms, idempotency, validation,
CSRF, host restrictions, quota persistence, ambiguous sends, retention boundaries,
holds and localized HTML/Markdown legal pages. Tests use synthetic correspondence
and a fake mail transport; no live SMTP calls are required.

Keep private correspondence, account evidence and review screenshots in ignored
local storage, outside the repository and container build context. Before making
an existing repository public, review its history and hosted PR descriptions as
well as the current checkout: deleting a file does not erase earlier commits.
