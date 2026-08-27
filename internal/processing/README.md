# Processing pipeline

The AI pipeline enriches stored incidents in three ordered stages:

1. `incident_metadata` receives minimized source text and publication context,
   then extracts validated language-neutral metadata.
2. `german_presentation` receives the minimized source and accepted metadata,
   then creates the canonical privacy-safe German presentation.
3. `english_translation` receives only the accepted German presentation and
   creates its English counterpart.

`steps.go` is the registry and validation boundary. A model response is not
publishable merely because it matches JSON: field limits, allowed values,
source grounding, and privacy checks must also pass. Prompts are immutable,
versioned records in `prompts.go`; retire an old prompt instead of editing its
meaning after results have been persisted.

`PipelineWorker` is a single logical worker. The store atomically claims jobs,
freezes the model and prompt versions for a cycle, and advances a cycle only
after every job in the active stage reaches a terminal state. Automatic work
obeys the configured schedule; manual review requests can wake the worker but do
not bypass model availability or output validation.

When adding a step:

1. Register a stable key, order, prompt version, schema, input generator,
   validator, and persistence mapping in `steps.go`.
2. Add the immutable prompt definition in `prompts.go`.
3. Extend store migration/state handling if the step produces a new value kind.
4. Add tests for registry order, input minimization, invalid output, privacy
   rejection, cycle advancement, retries, and recovery.

Do not send raw database rows or previously rejected model output to a model.
Do not weaken validation to accommodate one model's response shape.
