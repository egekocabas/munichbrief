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
MUNICHBRIEF_GAZETTEER_ENABLED=true \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
MUNICHBRIEF_ADMIN_ENABLED=true \
MUNICHBRIEF_PRESENTATION_MODE=review \
go run ./cmd/munichbrief
```

Select an installed model for every registered step in the protected/local
admin view. Enabling the gazetteer performs bounded public-source downloads;
omit it when testing only canonical German or verification processors, in which
case translation claims remain paused. The explicit live Ollama smoke test is:

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
presentation stages, use the same explicit opt-in. The test falls back to
`translategemma:4b`; `MUNICHBRIEF_OLLAMA_TRANSLATION_MODEL` may select any
installed schema-capable Ollama model:

```bash
MUNICHBRIEF_OLLAMA_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_TRANSLATION_MODEL=translategemma:4b \
go test -run 'TestLiveOllama(UnifiedTranslationPromptContract|RegisteredTranslationTargets)$' \
  -v ./internal/processing
```

The focused checks cover every registered target, language identities, Munich
district and street names, required terminology, attribution and uncertainty,
strict JSON, Han, Devanagari, Greek, and Cyrillic script output, and
instruction-like translated data without logging generated text.

See [Translation model evaluation](translation-model-evaluation.md) for the
official TranslateGemma request contract, the native-adapter boundary, separate
structural/editorial scoring, and the bounded comparison protocol.

The HY-MT2 and Seed-X critical screen never mutates production model routing. It
verifies the selected installed model identity before generation, always uses
typed placeholders, sends titles and summaries as separate requests, and
checkpoints every field under `.local/` so the same run can resume after
interruption. Seed-X is an opt-in per-language adapter, but the evaluated
third-party Q5_K_M artifact is not a qualified production model:

```bash
MUNICHBRIEF_NATIVE_ADAPTER_SMOKE_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
go test -timeout 60m -run TestLiveNativeTranslationAdapterSmoke \
  -v ./internal/processing
```

The smoke test makes one production-shaped English-control case per adapter,
with separate title and summary requests, and prints raw and restored output for
inspection. Set `MUNICHBRIEF_NATIVE_ADAPTER_SMOKE_ADAPTER` to `hy-mt2` or
`seed-x` for a targeted rerun. Run the smoke before the complete checkpointed
screen:

```bash
MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
go test -timeout 12h -run TestLiveNativeTranslationAdapterScreen \
  -v ./internal/processing
```

Set `MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_DIR` to use a different checkpoint
directory. Set `MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_ADAPTER` to `hy-mt2` or
`seed-x` to run only an adapter that passed its smoke gate. A directory is bound
to the exact selected adapter, model digest, prompt, settings, fixtures,
languages, and repetitions in its manifest; use a new directory when any of
those inputs changes. Targeted follow-ups may select comma-separated language
codes with `MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_LANGUAGES` and fixture names with
`MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_FIXTURES`. Generated output remains local
evaluation evidence and requires manual semantic review even when all
mechanical checks pass.

The full production-worker readiness harness also accepts
`MUNICHBRIEF_READINESS_ADAPTER=seed-x`, `salamandra-ta`, `llamax3`, `eurollm`,
`tower-plus`, or `tower-instruct`. Seed-X uses raw
`/api/generate`, its mandatory final language tag, greedy decoding, at most 512
output tokens, and a 4096-token effective context. SalamandraTA uses user-only
ChatML, greedy decoding, at most 1024 output tokens, and an 8192-token effective
context. LLaMAX3 uses raw Alpaca-format generation; EuroLLM, Tower+, and
TowerInstruct use user-only labelled-source ChatML. The evaluation adapters use
greedy decoding and at most 1024 output tokens; their effective contexts are
8192 except TowerInstruct at 2048. Use a fresh readiness
directory whenever the prompt, model, or target changes; recording/replay binds
saved responses to the exact rendered request and model digest.

The current release gate, resumable six-fixture matrix, per-language report,
and review criteria are in
[Pre-merge translation testing](pre-merge-translation-testing.md).

The production-worker readiness harness is the current evaluation entry point.
It requires an isolated directory containing a refreshed `gazetteer.db` and
`real-fixtures.json`: an array of three objects with IDs `D`, `E`, and `F`,
`title`, `summary`, `source` URL, a `facts` string array, and a `places` string
array. Synthetic A-C are defined in the harness. Freeze all six before running.
Do not copy an operational incident database into the evaluation directory.

```bash
MUNICHBRIEF_READINESS_LIVE_TEST=1 \
MUNICHBRIEF_READINESS_DIR=.local/translation-evaluations/readiness-v1 \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
MUNICHBRIEF_READINESS_MODEL=hf.co/mradermacher/Hy-MT2-7B-GGUF:Q5_K_M \
MUNICHBRIEF_READINESS_LANGUAGES=en,uk \
MUNICHBRIEF_READINESS_FIXTURES=A \
MUNICHBRIEF_READINESS_REPETITIONS=1 \
MUNICHBRIEF_READINESS_MAX_NEW_CALLS=4 \
go test -buildvcs=true -count=1 -timeout 70m -run '^TestLiveTranslationReadiness$' \
  -v ./internal/processing
```

Language/fixture/repetition selections and the new-call budget do not invalidate
completed requests. Select one language for later stages: A-C first, then D-F,
then A-B with repetition 2. Set `MUNICHBRIEF_READINESS_ADAPTER` to `hy-mt2`
(default), `translategemma`, `seed-x`, `salamandra-ta`, `llamax3`, `eurollm`,
`tower-plus`, `tower-instruct`, or `structured` for the roadmap's candidates.
`MUNICHBRIEF_READINESS_MODEL` may explicitly select an
installed native-adapter artifact for a controlled comparison; structured mode
uses its fixed candidate. Always use a new evaluation directory for a different
model or digest.
Set the new-call limit to zero to revalidate recorded responses without inference
(the installed digest is still verified). A transport interruption stops the run;
rerun the same command to replay saved fields and finish only missing calls.

The manifest pins code-content identity, revision provenance, model digest,
fixtures, and Gazetteer generation/content. Rendered request bodies are checked
before both replay and generation. Code/model/source changes require a new
directory; documentation-only commits do not invalidate identical requests.
The first live invocation also stores the complete Ollama `/api/show` response
and current `/api/ps` state in `artifact-preflight.json`. Append-only
`requests.jsonl` retains every attempt, including rejected raw
responses. Separate per-case databases and result files retain worker outcomes;
`*-review.json` is initialized once and never overwritten by resume. An incomplete
final event is retained separately before repairing its append boundary. Completed
invalid model responses are replayed, not silently replaced by another attempt.

The older native-adapter and summary screens below remain historical comparison
tools. They are not substitutes for the queued-worker readiness harness.

The smaller HY-MT2 plain-text screen sends one summary request for each of the
ten supported MunichBrief targets (`en`, `tr`, `it`, `uk`, `zh`, `hi`, `es`,
`fr`, `pl`, and `ru`). It uses its own configuration-bound checkpoint directory
and remains available for reproducing the older summary-only comparison:

```bash
MUNICHBRIEF_HYMT2_LANGUAGE_SCREEN_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_BASE_URL=http://127.0.0.1:11434 \
go test -timeout 4h -run TestLiveHyMT2SupportedReaderLanguageSummaryScreen \
  -v ./internal/processing
```

The screen records raw and restored output for manual editorial review and
rejects Markdown or URLs. It resumes completed languages after interruption;
set `MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_DIR` only when an explicit alternate
checkpoint location is needed.

The longer Munich place-name preservation matrix is independently gated so it
does not slow the normal live smoke suite. It checks every translation target
through the real placeholder wrapper with one dense request containing all 11
requested forms: transit labels, streets, districts, municipalities, and
hyphenated names. Every restored spelling must exactly match the NFC-normalized
source, the result must remain plain text without URLs, and model output is not
logged:

```bash
MUNICHBRIEF_OLLAMA_PLACE_NAMES_LIVE_TEST=1 \
MUNICHBRIEF_OLLAMA_TRANSLATION_MODEL=translategemma:4b \
go test -timeout 90m -run TestLiveOllamaMunichPlaceNamePreservationMatrix \
  -v ./internal/processing
```

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
