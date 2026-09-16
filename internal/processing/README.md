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

## Translation ordering

Eligible manual translations run before automatic translations at each job
boundary. Within that priority group, the worker keeps using the current model
across languages for up to 50 claimed attempts, including retries. Jobs for the
same model run oldest first, with job ID breaking timestamp ties. After 50
attempts, another eligible model gets a turn, selected by its oldest waiting job.
If no alternative is ready, the current model continues; its counter stays at
50 so a newly eligible competitor gets the next turn. Retry-delayed jobs and
models with open failure circuits do not hold up other models.

Model batches are in memory and reset on restart, cancellation, an empty eligible
translation queue, or a model change. A new batch prefers the last model invoked
by this worker when it has eligible work in the selected priority group. This is
a scheduling hint, not a guarantee that Ollama still has the model loaded.
Canonical and verification work retain precedence and can change that hint.
Existing model identities, adapters, prompts, and Ollama's ten-minute keep-alive
remain unchanged. Batch-start logs record the model, selection reason, and prior
attempt count; provider call logs show the resulting sequence.

The admin homepage and translations page show the current batch model and attempt
count (including the in-flight attempt), the next queued translation, and the next
model switch. These read-only previews use the claim ordering and eligibility
rules without reserving jobs. Uninstalled models are omitted from the preview;
the next-translation preview accounts for the batch reset when their queued
attempts are deferred. New requests and higher-priority work can change
the order. Live operations highlights the claimed canonical stage, verification
scope, or translation language; detailed counts and history are expandable.

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
