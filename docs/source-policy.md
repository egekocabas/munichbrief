# Source, privacy, and retention

MunichBrief processes official Munich Police press releases, which can contain
sensitive personal information and allegations. Technical availability is not
treated as blanket permission to crawl, retain, transform, or republish every
field.

This document records the project's operational policy, not legal advice.

## Discovery policy

Live discovery uses the dedicated Munich Police RSS feed:

<https://www.polizei.bayern.de/rss/polizeiprasidium-munchen.xml>

The feed is a rolling discovery window rather than a historical archive. The
application fetches only article URLs explicitly present in that feed. It does
not crawl the general press listing or archive.

Requests are low-rate and identifiable. Article URLs must use HTTPS on the
expected Bavarian Police host, redirects remain same-origin, and response time,
type, and size are bounded. Conditional request validators reduce unnecessary
traffic.

The source site's robots policy has historically disallowed automated crawling.
The narrow RSS-linked policy is a risk decision for the operator, not a claim
that all automated reuse is permitted. Reassess the current source policy
before enabling live ingestion.

## Public presentation

Retained source text is operational input, not public output. Public mode
requires a current complete presentation that passes privacy validation. It
does not render original incident bodies, review state, prompts, raw model
responses, or protected administration data.

Generated output must remain neutral, preserve uncertainty, avoid inventing
identity or culpability, and use broad editorial categories rather than legal
conclusions. Every incident links to its authoritative official release and
states that MunichBrief is independent and unofficial.

The presumption of innocence applies. A generated summary never replaces the
official source.

## Retention

The application currently retains extracted German incident text without an
automatic deletion deadline. This supports source-change detection, quality
review, and reproducible processing, but it is an explicit temporary policy,
not a conclusion that indefinite retention is legally appropriate.

Any deletion, anonymization, or review schedule requires deliberate product,
legal, and operator approval. Database backups contain the same sensitive
material and require equivalent access controls and lifecycle management.

## Operator responsibilities

Before public deployment, review:

- current automated-access and robots policies;
- copyright, source attribution, and modification requirements;
- personal-information minimization and correction handling;
- imprint, privacy notice, and contact obligations;
- whether written confirmation from the source owner is appropriate;
- encrypted transport and access controls for model processing;
- backup storage, retention, and deletion procedures.

German Copyright Act section 5 requires a case-specific assessment for
qualifying official works and does not establish that every police release may
be reused without restriction.

## Repository data policy

Do not commit real source article copies, generated presentations, production
databases, credentials, logs containing source data, or private deployment
values. Test fixtures must be handcrafted structural equivalents with invented
content and no recoverable personal details.
