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
- `admin.go`: review pages and mutation handlers
- `middleware.go`: host/path boundary, request metadata, and security headers
- `localization.go`: embedded message lookup and pluralization
- `templates/`, `assets/`, and `static/`: embedded UI sources and generated files

Keep routes and template data explicit. Never pass database or model structs
directly to templates without applying the presentation rules. Template output
must remain escaped; client-side code must not insert incident content as raw
HTML.

AI-generated reader output uses the centrally selected dark- and light-theme
labels returned by `selectedAIGeneratedAssetURL` and
`selectedAILightThemeAssetURL`. Do not choose label variants independently in
a template. The disclosure scope, legal caveats, and machine-readable
provenance map are documented in
[EU AI transparency and compliance posture](../../docs/eu-ai-transparency.md).

After changing templates or frontend sources, run `npm run build` and commit the
generated files under `static/`. Add boundary tests for public-host routing,
admin-disabled behavior, method/origin checks on mutations, and escaped content.
