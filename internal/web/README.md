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

Reader routes and discovery metadata consume the shared compile-time language
registry. Follow [Language support](../../docs/translation.md#languages) so
catalog, processing, SEO, and ingress contracts remain synchronized.

Timeline article links use canonical incident URLs without pagination queries.
`static/navigation.js` remembers the originating timeline and scroll position in
tab-local session storage, including HTMX navigation. Direct visits and readers
without JavaScript retain an ordinary Back link to the language homepage. Existing
incident URLs with `?page=` still preserve their server-rendered Back destination.

Keep routes and template data explicit. Never pass database or model structs
directly to templates without applying the presentation rules. Template output
must remain escaped; client-side code must not insert incident content as raw
HTML.

AI-generated reader output uses the centrally selected dark- and light-theme
labels returned by `selectedAIGeneratedAssetURL` and
`selectedAILightThemeAssetURL`. Do not choose label variants independently in
a template. Reader disclosure and provenance are summarized in
[AI disclosure](../../docs/source-policy.md#ai-disclosure).

After changing templates or frontend sources, run `npm run build` and commit the
generated files under `static/`. Add boundary tests for public-host routing,
admin-disabled behavior, method/origin checks on mutations, and escaped content.
