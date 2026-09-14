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
application ingests feed entries from the current Berlin calendar day and the
six preceding days. It fetches only article URLs explicitly present in that
feed and does not crawl the general press listing or archive.

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

The metadata stage receives publication date, time, weekday, `Europe/Berlin`,
and direct lookup maps for recent relative days and weekdays. This lets the
model resolve phrases such as “Monday”, “yesterday afternoon”, or “this
morning” without application-side German phrase parsing. Publication time is
never substituted for incident time. The stage returns one primary date and,
when available, either a clock time or day part. Unknown or contradictory
timing remains absent. Reader pages default to publication-date grouping and also offer an incident-date view, while
displaying the time stated in the report separately.

Public-assistance metadata is set only for an explicit source appeal. Reader
pages show broad requested assistance types but never reproduce contact details,
case numbers, identifying descriptions, or instructions; readers are directed
to the official source. Missing- and wanted-person input continues to use a
generic identity-free replacement even when that reduces metadata completeness.

Only an accepted privacy-safe German title and summary cross into a translation
job. Translation records never contain retained source text, contact details,
or identity data. A translation failure cannot expose the original or delay the
canonical German presentation.

Translation place protection uses a separate rebuildable gazetteer derived
from Landeshauptstadt München – GeodatenService (`dl-de/by-2.0`), GeoNames
(`CC BY 4.0`), and OpenStreetMap contributors (`ODbL 1.0`). Only normalized
names, classifications, source identifiers, hashes, and request validators are
stored; raw downloads are bounded temporary inputs and are not retained. The
public About page carries the required source attribution.

The independent category verifier receives the same accepted German title and
summary plus the German display name mapped from the immutable broad category
code. It never receives internal category codes, retained source text,
identifiers, rejected output, or translation text. Its constrained German
result is mapped back to an application-owned code and may change only the
effective broad editorial category for that exact presentation run; the
original extracted value remains auditable. A failed or exhausted verification
does not express a verdict and leaves the prior effective category unchanged.

The presumption of innocence applies. A generated summary never replaces the
official source.

## Retention

The application currently retains extracted German incident text without an
automatic deletion deadline. This supports source-change detection, quality
review, and reproducible processing, but it is an explicit temporary policy,
not a conclusion that indefinite retention is legally appropriate.

RSS checks recorded after migration 015 additionally retain the extracted
release text before incident splitting and immutable parsed-output snapshots,
including extracted text from failed parses when available. Identical snapshots
are reused across checks by content hash. Raw HTML is not stored. These records
are protected operational data, excluded from public pages and logs, and have
no automatic expiry. Future deletion policies must cover historical snapshots
as well as current incidents and backups.

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

Separately, the [Bavarian Police usage terms](https://www.polizei.bayern.de/wir-ueber-uns/impressum/index.html),
checked on 13 September 2026, expressly permit reprinting and analysis of press
releases with source attribution. This supports the project's attributed
press-release reuse; it does not settle personal-information rights, unrelated
third-party material, source retention or automated-access restrictions. See the
[editorial and provider follow-up](legal-follow-up-2026-09.md).

## Repository data policy

Do not commit real source article copies, generated presentations, production
databases, credentials, logs containing source data, or private deployment
values. Test fixtures must be handcrafted structural equivalents with invented
content and no recoverable personal details.
