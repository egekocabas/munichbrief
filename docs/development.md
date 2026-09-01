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
MUNICHBRIEF_OLLAMA_LIVE_TEST=1 go test -run TestLiveOllamaPrivacySafeMetadataFirstPresentation -v ./internal/processing
```

The smoke suite uses handcrafted timing, ambiguity, assistance, and privacy
cases. `TestLiveOllamaTemporalMetadataMatrix` covers relative days, weekday
compounds, explicit dates, clock times, day parts, overnight starts, and absent
timing. `TestLiveOfficialRSSFormatInventory` inventories current official
wording without logging report text, while `TestLiveOllamaOfficialRSSMetadataAndGermanPresentation`
validates metadata and German summaries for two current, privacy-minimised
incidents. Never commit downloaded source text, model output, or test databases,
and never write to a production database during verification.

For a focused translation prompt check without running the German metadata and
presentation stages, use the same explicit opt-in against TranslateGemma:

```bash
MUNICHBRIEF_OLLAMA_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_TRANSLATION_MODEL=translategemma:4b \
go test -run 'TestLiveOllama(TranslateGemmaPromptContract|RegisteredTranslationTargets)$' \
  -v ./internal/processing
```

The focused checks cover every registered target, language identities, Munich
district and street names, required terminology, attribution and uncertainty,
strict JSON, Han, Devanagari, Greek, and Cyrillic script output, and
instruction-like translated data without logging generated text.

The real-RSS Qwen check requires its separate explicit opt-in:

```bash
MUNICHBRIEF_OLLAMA_RSS_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
go test -timeout 25m \
  -run TestLiveOllamaOfficialRSSMetadataAndGermanPresentation \
  -v ./internal/processing
```

## Validation

The checks are intentionally layered so changes are validated at the file,
package, boundary, and assembled-application levels:

| Area | Checks |
| --- | --- |
| All tracked changes | `git diff --check` and full-history Gitleaks before publication |
| Go files and packages | `gofmt`, module consistency, `go vet`, Staticcheck, dead-code analysis, vulnerability reachability, race tests, and a binary build |
| Markdown | Local file and heading links through `scripts/check-docs.mjs` |
| Shell and workflows | ShellCheck and Actionlint |
| Frontend | Reproducible build, committed-output diff, and npm audit |
| Helm/deployment | Strict lint, negative configuration tests, and assertions over both default and public example renders |
| Container/application | Local multi-stage image build plus package-level and boundary tests |

Before opening a pull request, run:

```bash
git diff --check
node scripts/check-docs.mjs
test -z "$(gofmt -l .)"
go mod tidy -diff
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go run golang.org/x/tools/cmd/deadcode@v0.49.0 -test ./...
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
shellcheck scripts/*.sh
npm ci
npm run build
git diff --exit-code -- internal/web/static
npm audit --audit-level=high
helm lint --strict charts/munichbrief
helm lint --strict charts/munichbrief --values deploy/example-values.yaml
helm template munichbrief charts/munichbrief --namespace munichbrief \
  > /tmp/munichbrief-default.yaml
bash ./scripts/check-default-chart.sh /tmp/munichbrief-default.yaml
helm template munichbrief charts/munichbrief --namespace munichbrief \
  --values deploy/example-values.yaml > /tmp/munichbrief-rendered.yaml
./scripts/check-chart.sh /tmp/munichbrief-rendered.yaml
helm template munichbrief charts/munichbrief --namespace munichbrief \
  --values deploy/example-values.yaml \
  --set 'ingress.public.languageCodes={de,en,pt-br}' \
  > /tmp/munichbrief-multilingual.yaml
grep -F -- 'path: /pt-br' /tmp/munichbrief-multilingual.yaml
docker build --build-arg VERSION=local -t munichbrief:local .
```

CI restores Go module and build caches from the latest compatible revision and
saves newly compiled code, including race builds, under a revision-specific key.
A cold cache still needs a full compile; cached test results retain Go's normal
input-based invalidation.

Store, processing, and web tests copy an empty migrated SQLite snapshot into a
separate temporary database for each test. Migration, initial-default, backup,
and reopen tests continue to exercise fresh or persisted databases directly.
Web tests run in parallel with independent handlers and database connections.
To compare execution times without reusing test results, run
`go test -race -count=1 ./...`; use `-shuffle=on -parallel=2` to check isolation
under a different execution order and concurrency limit.

Routine unit tests remain offline. A contribution that requires network access
in the default suite is not acceptable. See [the internal package map](../internal/README.md)
and [repository check notes](../scripts/README.md) when deciding where new tests
or validations belong.
