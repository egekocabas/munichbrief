# Development

## Requirements

- Go 1.26.6 or newer
- Node.js 24 when changing frontend sources or dependencies
- Helm 4 for chart validation
- Docker for local container verification

## Offline development

The default command is safe and deterministic:

```bash
go run ./cmd/munichbrief
```

It uses synthetic fixtures and `.data/munichbrief.db`. Open
<http://127.0.0.1:8080>; metrics are available on
<http://127.0.0.1:9090/metrics>.

Use temporary or purpose-specific database paths for tests and experiments.
Never mix fixture and live source records in the same operational database.

## Frontend assets

Templates, CSS source, and JavaScript live under `internal/web`. Production CSS
and the pinned HTMX build are committed because the Go binary embeds them.

```bash
nvm use
npm ci
npm run build
git diff --exit-code -- internal/web/static
```

Use `npm run watch:css` while editing templates or CSS. The browser loads no
third-party assets.

## Live source checks

Use a separate database when running live ingestion:

```bash
MUNICHBRIEF_SOURCE_MODE=live \
MUNICHBRIEF_DATABASE_PATH=.data/munichbrief-live.db \
go run ./cmd/munichbrief
```

Two networked regression checks remain opt-in and must not run in routine CI:

```bash
MUNICHBRIEF_LIVE_TEST=1 go test -run TestLiveMunichSource -v ./internal/source
MUNICHBRIEF_KNOWN_PAGE_TEST=1 go test -run TestLiveKnownPageShapes -v ./internal/source
```

Parser fixtures are handcrafted, minimal, anonymized structural equivalents.
Do not commit complete police articles.

## Ollama development

Start with fixture data and explicitly enable processing:

```bash
MUNICHBRIEF_AI_ENABLED=true \
MUNICHBRIEF_AI_IMMEDIATE=true \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
MUNICHBRIEF_ADMIN_ENABLED=true \
MUNICHBRIEF_PRESENTATION_MODE=review \
go run ./cmd/munichbrief
```

Select an installed model for every registered step in the protected/local
admin view. The explicit live Ollama smoke test is:

```bash
MUNICHBRIEF_OLLAMA_LIVE_TEST=1 go test -run TestLiveOllamaPrivacySafeBilingualPresentation -v ./internal/processing
```

## Validation

Before opening a pull request, run:

```bash
test -z "$(gofmt -l .)"
go mod tidy -diff
go vet ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go run golang.org/x/tools/cmd/deadcode@v0.49.0 -test ./...
npm ci
npm run build
git diff --exit-code -- internal/web/static
npm audit
helm lint --strict charts/munichbrief
helm lint --strict charts/munichbrief --values deploy/example-values.yaml
helm template munichbrief charts/munichbrief --namespace munichbrief \
  > /tmp/munichbrief-default.yaml
bash ./scripts/check-default-chart.sh /tmp/munichbrief-default.yaml
helm template munichbrief charts/munichbrief --namespace munichbrief \
  --values deploy/example-values.yaml > /tmp/munichbrief-rendered.yaml
./scripts/check-chart.sh /tmp/munichbrief-rendered.yaml
docker build --build-arg VERSION=local -t munichbrief:local .
```

Routine unit tests remain offline. A contribution that requires network access
in the default suite is not acceptable.
