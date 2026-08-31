# Processing pipeline

AI work has two independent layers. The canonical pipeline has two ordered
stages:

1. `incident_metadata` receives minimized source text and publication context,
   then extracts validated language-neutral metadata.
2. `german_presentation` receives the minimized source and accepted metadata,
   then creates the canonical privacy-safe German presentation.

After stage 2 succeeds, the German presentation is complete and publishable.
Independent work is registered in `PostProcessorRegistry`. Each processor
declares its key, display metadata, priority, model setting, scopes, immutable
step definitions, inputs, named outputs, automatic/manual capabilities, and
optional aggregate counters. Adding a processor therefore extends scheduling,
execution, admin controls, status, history, and metrics through registration.

The category verifier receives only `title_de`, `summary_de`, and
the immutable `category` mapped to its German display name. Its German prompt
and schema accept only German category names; application-owned mappings convert
the validated result back to a stable internal code. The strict result contains
only `is_correct` and an allowed `corrected_category`; application validation
requires true/unchanged or false/changed. Successful results overlay category
selection for their exact presentation run without mutating canonical values.
Failures are not verdicts and leave the previous successful correction, or the
original category when no correction succeeded, effective.

The translation registration generates one processor scope per target
language. English uses `incident-translation-en-v2`; every translation receives
only the accepted German presentation and never blocks canonical completion.
TranslateGemma prompts derive the English model-facing language names and exact
BCP-47 codes from the shared language registry. They use the model's recommended
single-user-message shape, with the complete instruction followed by the JSON
payload after a blank line; Ollama's structured-output schema still constrains
the response to the registered title and summary fields. Each target keeps one
active immutable prompt, and retired prompt versions remain registered for
audit. The generic factory derives its schema, decoder, step, and scope; follow
[Adding a reader language](../../docs/adding-a-language.md) instead of adding
worker or store branches.

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

The durable automatic-processing control gates scheduled discovery and claims
without gating manual admin or CLI work. Disabling takes effect after the current
automatic provider call; cancel-all is the separate immediate operation that
terminalizes unfinished work before canceling the provider context. Claim and
completion transactions remain the authority in either race ordering.

When adding a canonical step:

1. Register a stable key, order, prompt version, schema, input generator,
   validator, and persistence mapping in `steps.go`.
2. Add the immutable prompt definition in `prompts.go`.
3. Extend presentation logic only if readers consume the new named values.
4. Add tests for registry order, input minimization, invalid output, privacy
   rejection, cycle advancement, retries, and recovery.

Do not send raw database rows or previously rejected model output to a model.
Do not weaken validation to accommodate one model's response shape.

When adding a post-processor, register its metadata, scopes, immutable prompt,
schema, generator, validator, inputs, and named outputs. Use generic lifecycle
tests with an injected processor to verify that no store, worker-loop, status,
history, metrics, or handler branch is required.

Automatic jobs run only for complete current `incident-pipeline-v2`
presentations created after a scope's persisted enablement time. Focused manual
actions can rerun one incident or all current v2 presentations. Do not add
source text, explanations, confidence values, or unbounded model output to a
post-processor.
