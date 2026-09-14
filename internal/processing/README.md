# Processing pipeline

This package turns extracted reports into German presentations, then runs
independent verification and translation jobs.

## Flow

1. **Metadata:** extract validated fields from minimized source text.
2. **German presentation:** generate and validate the canonical title and summary.
3. **Post-processing:** verify categories and public-assistance metadata, and
   translate the accepted German presentation into each selected language.

German can publish after step 2. Post-processing waits for the active canonical
cycle to finish; a failed translation does not block German or other languages.
All model calls are sequential.

## Key rules

- SQLite atomically claims jobs; each job retains its model, adapter, and prompt version.
- Prompts are immutable. Semantic changes need a new version.
- Valid JSON is only the first check: field limits, allowed values, grounding,
  language, and privacy rules must also pass.
- Translations use accepted German text with place-name protection and restoration.
- Automatic work follows scheduling controls. Manual requests still require
  available models and valid output.
- Add post-processors through the registry so scheduling, admin controls,
  history, and metrics use the shared lifecycle.

## Code map

| File | Responsibility |
| --- | --- |
| [steps.go](steps.go) | Canonical steps, schemas, and validation |
| [prompts.go](prompts.go) | Versioned prompt definitions |
| [post_processors.go](post_processors.go) | Independent processor registry |
| [pipeline_worker.go](pipeline_worker.go) | Claims, execution, and cycle advancement |
| [translation_adapters.go](translation_adapters.go) | Model-specific request formats |
| [place_protection.go](place_protection.go) | Protect and restore translated place names |

See [architecture](../../docs/architecture.md) and
[translation](../../docs/translation.md) for the wider system.
