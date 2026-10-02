# Map and archive statistics

`/{language}/map` explores currently published MunichBrief reports across Munich's
25 official city districts. It combines a district map, publication periods,
category and district filters, language availability, category and district
rankings, and a publication trend. An equivalent district list and the trend's
date/count list make the information available without using the map itself.
The page works without JavaScript.

## What is counted

The count basis is the current public German archive. Each report ID contributes
once, regardless of the number of translations, presentation attempts, or
verification jobs. A follow-up report can describe the same real-world event as
another report, and one report can describe several scenes. These figures are
report counts, not counts of unique incidents.

Statistics reuse the reader's current-source, current-pipeline public selection.
Original-only reports, obsolete presentations, and source-invalidated output
are excluded. Retained original police text and protected verifier evidence are
never used as public fallbacks. The category and area are the same effective
values displayed in the reader.

Changing the interface language keeps the canonical total unchanged. The
availability count shows how many of those reports have an accepted current
version in the selected language. The page links separately to that language's
available reports and, where applicable, to all matching German reports. A
translation from an older presentation does not make its replacement available.
AI processing and translation checks can delay or prevent publication.

The earliest publication date is calculated across the complete current public
canonical archive, independently of the selected filters. It is not the date
MunichBrief started collecting reports. It can change if the earliest public
report is invalidated or removed. The feed and archive are a selection of police
press reports, not a complete historic crime dataset.

## District assignment

The map's shapes come from a bundled, simplified official boundary snapshot;
see [boundary sources and reproduction](munich-map-boundaries.md). The polygons
do not geocode reports or determine their district membership.

The [location catalog](../internal/location/catalog.go) supplies reviewed names,
aliases, and explicit containment. The
[district grouping function](../internal/location/districts.go) follows those
relationships from the effective public area. For example, Neuperlach and
Ramersdorf-Perlach both contribute to Ramersdorf-Perlach, while a supported venue
can resolve through its neighborhood to a district.

Broad names such as Schwabing, Giesing, Innenstadt, and Am Harthof do not identify
one official district and remain unspecified. Unknown names also remain
unspecified. A known municipality outside Munich, or a locality explicitly
contained by it, contributes to **Outside Munich city**. Natural areas without
an explicit outside-city relationship remain unspecified; the map does not
invent one from their name. Street hints, substring matches, and exact-address
inference are not assignment rules.

Each report contributes to exactly one of these groups:

```text
Total = reports in the 25 districts + outside Munich city + district unspecified
```

Location verification follows the existing
[effective public location policy](location-verification.md#effective-public-location).
A pending, failed, or unresolved replacement retains an applicable previous
correction. A changed source or context invalidates that override, causing the
same fallback in the reader and statistics. The map does not queue verification,
change catalog interpretation, or call an LLM.

## Filters and dates

The map's state lives in its URL. Saved reader searches and saved neighborhoods
do not silently restrict it, and visiting the map does not overwrite those
preferences. The existing language preference still applies.

| Map parameter | Meaning |
| --- | --- |
| `period=all` or no period | All available publication dates |
| `period=week` | Last 7 Munich calendar days, including today |
| `period=month` | Last 30 Munich calendar days, including today |
| `period=previous_month` | Complete preceding calendar month |
| `from`, `to` | Inclusive custom publication dates; either end may be omitted |
| `category` | One existing report category |
| `district` | One official catalog district ID, `outside`, or `unassigned` |

Dates use the report's publication time in `Europe/Berlin`, not its incident
date or AI processing timestamp. Calendar arithmetic preserves the intended
range across daylight-saving changes. A supplied custom date replaces a preset;
the original values are still validated. Unknown parameters, repeated
parameters, invalid categories or districts, malformed dates, and reversed
ranges are rejected.

All map totals, ranks, and charts follow the same selected filters. They are
calculated before pagination within one database read snapshot. Map color
intensity is relative to the largest district count in the current selection;
it does not represent a population-adjusted rate or a fixed risk scale.

The trend uses Monday-aligned weekly bins, including zero-report weeks between
the first and last matching dates. Very long archives widen the bins to keep
at most 104. Exact ranges and counts are available in the data list. The first
and last range are clipped to requested date limits, so selecting a trend bar
cannot widen that selection. A short boundary period can contain fewer days.

Linked reader searches use concrete publication dates and the same district
grouping function. This prevents a district count from being linked to an
exact-area search that would omit its neighborhoods. The reader preserves the
district when changing non-geographic advanced filters. Entering an exact area
or applying saved neighborhoods replaces the district; a district is never
combined with those conflicting geographic selections. Other advanced reader
filters remain available after opening the report list.

## Public rendering and storage

The SVG geometry, styles, fonts, and interface are served by MunichBrief. The
map needs no remote tiles, geocoding requests, browser location permission, or
third-party map scripts. It introduces no map-preference cookie or local-storage
entry. Source and license attribution remain visible beside the map.

The unfiltered localized pages are discoverable through navigation and the
sitemap. Filtered pages use `noindex,follow`. Both HTML and `text/markdown` use
the site's canonical links, content negotiation, and public-response headers.
Report links retain the current selection and explicitly select their language.

## Validation evidence

During implementation, an isolated copy of the production backup taken on
2026-10-03 in Munich time produced the following aggregate checks:

| Check | Result |
| --- | ---: |
| Public canonical reports | 282 |
| Available English versions | 223 |
| Assigned to a city district | 211 |
| Outside Munich city | 41 |
| District unspecified | 30 |
| Earliest public publication date | 2026-08-20 |

All 25 district totals matched their German and English reader searches: 50
comparisons. Ludwigsvorstadt-Isarvorstadt, for example, contained 62 reports
after neighborhood and venue rollup, with 41 available in English. The backup
itself and private report content are not committed to the repository; these
figures are a dated validation snapshot, not hardcoded page data.

Automated store tests cover canonical and translated list parity, stale source
and obsolete-pipeline exclusion, superseded translations, replacement
presentations, category corrections, effective location changes, source-mode
isolation, empty selections, clipped dates, zero weeks, and bounded long-archive
trends. Web tests cover calendar presets, invalid URLs, saved-reader-filter
isolation, localized rendering, report links, and discovery.

The figures inherit the archive's coverage and classification limits. They are
not adjusted for population, visitor numbers, district area, reporting behavior,
or changes in press publication practices. AI-derived categories and locations
can be wrong. Neither district rankings nor map colors establish neighborhood
safety.
