# Place-name gazetteer

MunichBrief protects Munich-area place names before translation. The gazetteer
is operational input, not editorial content: the application replaces matched
names with opaque tokens, validates the model's tokens, and restores the exact
source spelling before persistence.

## Verified sources

The production refresh uses fixed HTTPS endpoints and validates each download
before activating a complete generation:

| Source | Parsed contract | Licence and attribution |
| --- | --- | --- |
| Munich street-name WFS | GeoJSON `features[].properties.strassenname` | `dl-de/by-2.0`, Landeshauptstadt München – GeodatenService |
| Munich Stadtbezirke WFS | GeoJSON `features[].properties.sb_name` | `dl-de/by-2.0`, Landeshauptstadt München – GeodatenService |
| GeoNames `DE.zip` | UTF-8, 19-column TSV; feature class `P`; codes `PPLA`, `PPLA4`, `PPL`, `PPLX`; admin3 `09162` or `09184` | `CC BY 4.0`, GeoNames |
| OpenStreetMap Overpass | Split area queries for roads, places, transit, and selected landmarks; only `official_name`, `name`, `name:de`, `short_name`, and `alt_name` | `ODbL 1.0`, © OpenStreetMap contributors |

Live validation on 2026-09-02 parsed 6,300 official street features, 27
district geometries, 261 GeoNames places, and 60,179 OSM name occurrences. They
deduplicated to 11,934 active names after overrides. These are observations, not hardcoded
expected counts; broad minimum and maximum safety ranges detect empty, partial,
or unexpectedly expanded responses.

The official Munich address export was inspected but rejected as the primary
street source because it repeats addresses and abbreviates names such as
`Ingolstädter Str.`. `Stadtbezirksteile` was rejected because its published WFS
features have identifiers and geometry but no official names. BBBike CSV and
regional PBF downloads were rejected because they are substantially larger and
do not provide a safer bounded category filter for this application.

## Refresh and failure policy

The server loads an existing active generation before starting workers, then
refreshes immediately and weekly with jitter. Conditional ETag and
Last-Modified requests are used when a source supplies validators. Validators
are scoped to the fixed source URL and a versioned parsing contract; changing
either forces a full download, so a `304 Not Modified` cannot preserve entries
produced by obsolete parsing or filtering rules. Downloads, ZIP expansion,
parser rows, Unicode, and name lengths are bounded. OSM
categories are requested sequentially so one large combined Overpass response
is not required.

A candidate becomes active only after every source passes validation, names are
normalized to NFC, overrides are applied, entries are persisted, and the NFA
matcher builds successfully. Failure leaves the previous generation and
matcher active. With no valid generation, translation claims pause; canonical
German processing and the reader remain available.

The rebuildable database seeds narrowly scoped `context` overrides for names
that are also common German nouns. Operators may add further `context` or
`exclude` overrides after review; an override never invents a name absent from
the downloaded sources.

Use `munichbrief gazetteer status` without network access or
`munichbrief gazetteer refresh` for a one-shot refresh. The database is
rebuildable and deliberately excluded from incident backups.

## Matching contract

Matching is exact and case-sensitive, uses leftmost-longest Aho–Corasick
selection, and validates Unicode letter, number, and combining-mark boundaries
in MunichBrief rather than the matcher's byte-oriented whole-word option.
Official sources outrank GeoNames, which outranks OSM. The highly ambiguous
fire noun `Brand` is excluded, while `Haar` requires preceding location
context. Two-letter names such as `Au` also require location context by rule.
`U-Bahn` and `S-Bahn` are always present as static protected entries.

Each unique normalized spelling receives a stable `__MB_PLACE_####__` token,
which is reused for repeated occurrences. The output must preserve the exact
occurrence count in each field, but may reorder intact tokens for natural target
grammar and may place an apostrophe-delimited grammatical suffix after one. A
missing, duplicated, field-moved, modified, or invented token is invalid. Reader
titles and summaries are plain text, so Markdown and URLs are rejected before
persistence.
