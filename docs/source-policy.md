# Privacy and sources

MunichBrief is independent and unofficial. The original police release is the
authoritative source; reports describe an investigation at publication time,
and the presumption of innocence applies.

## Source access

- Discovery uses the official [Munich Police RSS feed](https://www.polizei.bayern.de/rss/polizeiprasidium-munchen.xml).
- Only feed-linked articles in the current seven-day window are fetched.
- Requests enforce the expected HTTPS host, same-origin redirects, timeouts,
  content types, and size limits. Conditional requests avoid repeat downloads.
- Source availability does not settle every permission to collect or republish
  material. Operators must review current source terms and access policies.

## Public and protected data

| Public reader | Protected operational storage |
| --- | --- |
| Accepted German summaries and successful translations | Extracted original German text |
| Broad categories and available incident timing | Processing attempts and provenance |
| Official source links and AI disclosure | Historical extracted-text and parser snapshots |

Public hosts cannot serve admin routes or fall back to retained originals.
Generated results must match the current source and supported pipeline before
publication. Validation checks structure, length, and privacy rules; it does
not guarantee that every generated statement is correct.

## AI disclosure

Reader pages identify AI-generated text and link to the source. HTML metadata,
Markdown, and share images carry corresponding disclosure or provenance.
These controls explain how content was made; they are not a certification of
accuracy or legal compliance.

Canonical processing and public-assistance verification send original source
text to the configured Ollama endpoint. Translation and category verification
use the accepted German presentation. Protect that endpoint and its transport.
Logs exclude source bodies, prompts, generated text, and raw model responses.

## Retention

Original incident text and historical extracted-text snapshots currently have
no automatic expiry. Backups contain the same sensitive material and need
appropriate access, retention, and deletion procedures. Raw article HTML is
not retained in those snapshots.

## Contact and corrections

Use the [contact page](https://munichbrief.de/en/contact) for operational issues
or content corrections, and [SECURITY.md](../SECURITY.md) for vulnerabilities.

The optional contact form stores messages before attempting email delivery.
Receipt and notifications have separate admin controls. Resolved messages are
normally deleted after 90 days; holds, unresolved messages, and external mail
copies require separate handling. The deployed privacy notice describes the
operator and providers; see the [contact implementation](../internal/contact/worker.go).
