# Reader discovery and preferences

Public lists remain Go-rendered HTML, enhanced by HTMX. All controls have native
forms or links. Default published lists use 20 reports per page; readers can
choose 10, 20, 30 or 50. Page, view and size are navigation URL parameters. Search
criteria only travel in POST bodies and the bounded first-party search cookie.

## Search and chronology

`GET /{language}/search` restores the last criteria. `POST` applies them and
redirects to the results. `POST /{language}/search/clear` deletes them. Criteria
expire 30 days after application; GET navigation does not extend their lifetime.
A homepage visit with active criteria temporarily redirects to the search view.
Language changes retain criteria (without translating the query) and reset page 1.
Clear all, filter changes, page-size changes and view changes reset pagination.
The URL alone cannot reproduce someone else's saved search.

Free text uses Unicode-normalized literal terms combined with AND. FTS5 trigrams
cover terms of at least three Unicode code points; shorter terms use literal
substring matching, including short Chinese queries. No user-supplied FTS query
syntax is executed. Turkish dotted/dotless I match, German case folding handles
ß, and combining marks meaningful in scripts such as Devanagari are preserved.
Results follow the chosen chronological view, not a relevance ranking.

Date bounds are inclusive. Publication dates are interpreted in Europe/Berlin;
incident dates are the extracted local dates, never a publication-time fallback.
Incident view sorts dates newest first, then clock times newest first, then
reported day parts, then missing times. Day-part and missing-time groups use
publication order and a stable ID tie-breaker. Missing incident dates appear last.
“Clock time reported” is deliberately not “exact time”: extraction may normalize
an approximate expression such as “around 09:30” to 09:30.

## SQLite migration and recovery

Migration 023 adds a derived reader table, composite indexes and an external-content
FTS5 trigram index. Store startup compares the actual SQL projection and
transactional maintenance triggers against the current definitions. Changed,
missing or obsolete definitions cause a transactional rebuild and backfill before
returning ready; unchanged definitions are reused. Definitions incorporate the
language registry and public-readiness rules. Bump `readerIndexContract` when
registered reader SQL functions change semantics. This runs no AI jobs and changes
no source or generated report text.

Triggers refresh affected incidents when source/presentation/translation/verification
records change. Deletion removes corresponding search records. Reader queries
add current-run/public-readiness checks independently of the index. Only public
presentation titles and summaries enter the index, never raw source bodies.
Counts and pages use one database snapshot, with filtering before LIMIT/OFFSET.

Use the existing database backup procedure before upgrading. The index is derived
and is rebuilt on reopen when its projection or maintenance triggers are missing
or changed. Reopening unchanged schema does not force a backfill. SQLite migration 023 is additive;
roll back using a pre-upgrade backup rather than modifying migration history.
Do not use generic SQLite clients to mutate this derived schema: its projection
uses the application's registered Unicode/date functions.

## Discovery and privacy

Default publication pages have self-canonicals and crawlable pagination. Search,
incident-order and alternate-size lists are noindex and absent from the sitemap.
Incident URLs and article publication timestamps remain unchanged. Head metadata
is merged by a pinned, self-hosted HTMX extension. Public history snapshots are
disabled so search forms do not persist in HTMX's localStorage; history misses
reload the document. The existing return link restores the list and scroll.

Privacy and Impressum now have localized public routes and footer links. The
[contact and legal decision record](contact-and-legal.md) covers the confirmed
operator information, private inbox, retention and activation requirements.

## Local performance sample

On an Apple M1, the 10,000-report synthetic benchmark (three measured iterations)
returned 20 records plus the total in approximately 45 ms for publication order,
46 ms for incident order, 66 ms for trigram text search and 48 ms for a two-character
Chinese query. These are local query measurements, not a production latency SLA.
Run `go test ./internal/store -run '^$' -bench BenchmarkReaderIndex10000 -benchtime=3x`
to reproduce the synthetic check.

## Visual review

These screenshots use synthetic local reports, not production records:

- [Desktop, light theme](previews/reader-desktop-light.png)
- [Desktop, dark theme](previews/reader-desktop-dark.png)
- [Turkish mobile, light theme](previews/reader-mobile-light.png)
- [Turkish mobile, dark theme](previews/reader-mobile-dark.png)

Browser checks covered 320, 390 and 768 pixel layouts and desktop, both themes,
visible keyboard focus, expanded search, page-size changes, language switching,
native forms with JavaScript disabled, enhanced navigation, browser back/forward,
and returning from a report to the list's scroll position. All 14 localized
homepages and About/Contact pages were checked for horizontal overflow at 320px.
Locale validation checks all 264 keys and their interpolation placeholders.

The processing worker test's two-second deadlock guards were increased to ten
seconds to accommodate SQLite index maintenance under Go's race detector. Its
assertions about finishing only the current post-processing request are unchanged.

## Remembered timeline order — September 2026 follow-up

The last successfully visited public listing order is remembered for 30 days in
`munichbrief_timeline`, a versioned, bounded HttpOnly first-party cookie with
SameSite=Lax and Secure in production. Invalid, expired or unsupported values
fall back to publication order. Failed/invalid listing requests do not update it.
About and Privacy explain this preference in all 14 languages.

Home links preserve the selected view and active search; within a listing they
also retain its page size while returning to page 1. Information pages and a
bare visit to `/` restore the saved view, in the preferred/current language.
A direct localized listing URL remains authoritative: `/en` means publication
order, `/en?view=incident` means incident order. Explicit URLs, bookmarks,
pagination and browser history are not silently reinterpreted by the cookie.
Selecting publication order therefore also updates the remembered default.

Search stays at `/{language}/search`, with criteria in the existing cookie and
visible removal/Clear all controls. Article Back links have a server-rendered
saved-view/search fallback; existing per-tab return state restores the precise
originating page, page size and scroll position with JavaScript. Incident URLs
and canonicals do not acquire preference parameters. Personalized responses and
root redirects use private/no-store and Vary: Cookie; standard publication pages
remain indexable and alternative lists/search retain noindex.

Verification: full `internal/web` tests, focused reader/legal/navigation race
checks, Go vet, frontend generation, offline licence validation and documentation
checks passed. Browser checks with JavaScript enabled and disabled covered Home,
root redirection, search/clear, explicit publication URLs and native article
return. Enhanced article return restored the originating second page, page size
and scroll position. The 390px flow had no horizontal overflow. Both information
page disclosures were checked in all 14 locales.
