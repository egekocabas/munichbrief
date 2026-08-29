# Processing pipeline

AI work has two independent layers. The canonical pipeline has two ordered
stages:

1. `incident_metadata` receives minimized source text and publication context,
   then extracts validated language-neutral metadata.
2. `german_presentation` receives the minimized source and accepted metadata,
   then creates the canonical privacy-safe German presentation.

After stage 2 succeeds, the German presentation is complete and publishable.
The independent category verifier receives only `title_de`, `summary_de`, and
the immutable `category` mapped to its German display name. Its German prompt
and schema accept only German category names; application-owned mappings convert
the validated result back to a stable internal code. The strict result contains
only `is_correct` and an allowed `corrected_category`; application validation
requires true/unchanged or false/changed. Successful results overlay category
selection for their exact presentation run without mutating canonical values.
Failures are not verdicts and leave the previous successful correction, or the
original category when no correction succeeded, effective.

The translation registry then creates one durable job per enabled target
language. English uses `incident-translation-en-v1`; every translation receives
only the accepted German presentation and never blocks canonical completion.

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

When adding a canonical step:

1. Register a stable key, order, prompt version, schema, input generator,
   validator, and persistence mapping in `steps.go`.
2. Add the immutable prompt definition in `prompts.go`.
3. Extend store migration/state handling if the step produces a new value kind.
4. Add tests for registry order, input minimization, invalid output, privacy
   rejection, cycle advancement, retries, and recovery.

Do not send raw database rows or previously rejected model output to a model.
Do not weaken validation to accommodate one model's response shape.

When adding a language, register its code, display name, immutable prompt,
schema, generator, validator, and automatic-enablement timestamp. Use the shared
translation model setting, and require an explicit admin backfill for older
canonical presentations.

Category verification has its own preferred model and cutover. Automatic jobs
run only for new presentations; use the protected admin backfill for older
reader-selectable runs. Do not add source text, explanations, confidence values,
or unbounded model output to this processor.
