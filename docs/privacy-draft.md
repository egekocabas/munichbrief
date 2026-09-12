# Privacy notice — unpublished draft

This is a review document, not a published or complete privacy notice. No public
route or footer link exposes this draft. Operator identity, serviceable address,
hosting jurisdiction, applicable legal bases, recipients/transfer arrangements,
Cloudflare account settings and retention must be resolved before publication.
Do not substitute a project name or an email address for required controller details.

## Current processing inventory

- The site provides summaries of public police reports. German source text and
  RSS text snapshots are retained for processing and restricted review. No
  automatic expiry currently applies to these source records. This requires a
  separate retention and lawful-basis assessment; public availability does not
  itself settle processing of offence-related personal data.
- The selected language uses a first-party cookie for one year. Dismissal of the
  AI notice uses a first-party cookie for 30 days. Theme selection uses browser
  storage until the visitor clears it.
- Applying a search saves only the latest criteria in a first-party HttpOnly
  cookie, expiring 30 days after application. Clearing the search deletes it.
  Criteria are transmitted with requests to this site, including through its
  Cloudflare proxy. They do not enter URLs or a server-side search-history table.
  This is a preference, not a promise of anonymity. HTMX localStorage history
  snapshots are disabled. Browser-native history behavior remains browser-controlled.
- Application access logs contain request method, path (without query string),
  response status, duration and request/correlation identifiers. The application
  logger does not record search forms, cookie values, visitor IPs or user agents.
  Separate infrastructure/provider logging must be assessed independently.
- Internal Prometheus metrics are aggregate operational measurements; Grafana
  displays internal metrics and Loki logs rather than embedding tracking in the
  reader website. Reviewed infrastructure configuration specifies 30-day metric
  and log retention. This is configuration evidence, not a live audit or a
  guarantee that each entry is deleted at an exact instant. Raw source storage
  and backups are separate from monitoring retention.
- Cloudflare proxying was observed on the live site during the September 2026
  inspection. No browser analytics beacon was observed in that page load.
  Cloudflare processes traffic data including IP addresses for delivery and
  protection, and can produce edge analytics without a browser beacon. Account
  features, recipients, transfers and provider retention remain to be verified.
- Email contact receives the information supplied by the sender. Mail-provider
  details, retention and handling procedures require operator confirmation.
  Public GitHub issues should not contain personal or sensitive information.

## Required completion before publication

Specify the controller and contacts; purposes and legal bases for each activity;
necessary-cookie assessment; recipients and any international transfers; actual
retention periods or criteria; relevant rights and their limits; the competent
supervisory authority and complaint route; and handling of data obtained from
public reports. Review GDPR Articles 13/14 and offence-related data requirements
for this particular service. Do not claim exemptions or compliance without review.

The operator has chosen not to supply personal details in this task. This draft
preserves that boundary and must not be presented as a complete legal notice.

## Sources

- [GDPR](https://eur-lex.europa.eu/eli/reg/2016/679/oj)
- [Cloudflare traffic analytics](https://developers.cloudflare.com/analytics/faq/about-analytics/)
- [Cloudflare Web Analytics collection](https://developers.cloudflare.com/web-analytics/data-metrics/data-origin-and-collection/)
- [Cloudflare cookies](https://developers.cloudflare.com/fundamentals/reference/policies-compliances/cloudflare-cookies/)
- [Source retention policy](source-policy.md)
