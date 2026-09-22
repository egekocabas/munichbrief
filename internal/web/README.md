# Web package

The web server exposes one route tree with two presentations:

- Public routes contain only records that pass the configured presentation and
  privacy rules. Public-host requests are additionally restricted to public
  paths by `accessBoundary`.
- Review/admin routes expose operational state and mutations. They are disabled
  unless explicitly configured and are expected to sit behind the deployment's
  authentication middleware.

File ownership is intentionally narrow:

- `server.go`: dependencies, template loading, and route registration
- `public.go` and `discovery.go`: public pages and crawler metadata
- `admin.go`, `admin_translations.go`, and `admin_gazetteer.go`: review pages,
  per-language routing, bounded Gazetteer operations, and mutation handlers
- `middleware.go`: host/path boundary, request metadata, and security headers
- `localization.go`: embedded message lookup and pluralization
- `templates/`, `assets/`, and `static/`: embedded UI sources and generated files

Public routes and discovery metadata consume the shared compile-time language
registry. Follow [Language support](../../docs/translation.md#languages) so
catalog, processing, SEO, and ingress contracts remain synchronized.

Timeline article links use canonical incident URLs without pagination queries.
`static/navigation.js` remembers the originating timeline and scroll position in
tab-local session storage, including HTMX navigation. Direct visits and browsers
without JavaScript retain an ordinary Back link to the language homepage. Existing
incident URLs with `?page=` still preserve their server-rendered Back destination.

Keep routes and template data explicit. Never pass database or model structs
directly to templates without applying the presentation rules. Template output
must remain escaped; client-side code must not insert incident content as raw
HTML.

AI-generated summaries and translations use the centrally selected dark- and light-theme
labels returned by `selectedAIGeneratedAssetURL` and
`selectedAILightThemeAssetURL`. Do not choose label variants independently in
a template. AI disclosure and provenance are summarized in
[AI disclosure](../../docs/source-policy.md#ai-disclosure).

After changing templates or frontend sources, run `npm run build` and commit the
generated files under `static/`. Add boundary tests for public-host routing,
admin-disabled behavior, method/origin checks on mutations, and escaped content.

Social cards use `static/social-brand.svg`, which references the current favicon
and matches the reader header's Georgia wordmark. Regenerate its transparent PNG
with Georgia installed after changing the logo or wordmark:

```sh
rsvg-convert internal/web/static/social-brand.svg -o internal/web/static/social-brand.png
```

The server embeds the PNG, so it needs no system fonts or SVG renderer. Branding
assets and the renderer design version feed both image ETags and the versioned
Open Graph/Twitter image URLs. After regenerating the PNG, update the asset hashes
with `node scripts/licenses.mjs --record-inputs --write`.

Homepage cards reuse the localized `HeroTitle` and `HeroCopy` shown on the
homepage. The subtitle wraps separately from the headline; all registered
languages are checked for complete copy, glyph coverage, and safe-area fit.
