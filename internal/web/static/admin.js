(() => {
  const dialog = document.querySelector("#processing-confirmation");
  const description = document.querySelector("#processing-confirmation-description");
  const request = document.querySelector("#processing-confirmation-request");
  const confirmButton = document.querySelector("#processing-confirmation-submit");
  if (!(dialog instanceof HTMLDialogElement) || !description || !request || !confirmButton) return;

  let activeForm = null;

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("[data-confirm-processing]")) return;

    const confirmed = form.elements.namedItem("confirmed");
    if (confirmed instanceof HTMLInputElement && confirmed.value === "true") return;

    event.preventDefault();
    activeForm = form;
    let message = form.dataset.confirmDescription || "Request immediate AI processing.";
    const incidentInputID = form.dataset.incidentInput;
    if (incidentInputID) {
      const incidentInput = document.getElementById(incidentInputID);
      if (incidentInput instanceof HTMLInputElement) message += ` Incident ID: ${incidentInput.value}.`;
    }
    const selections = [...form.querySelectorAll('select[name="scope"], select[name="model"], select[name^="model_"]')]
      .filter((field) => field instanceof HTMLSelectElement && field.value)
      .map((field) => {
        const label = field.labels?.[0]?.textContent?.trim() || field.name.replace("model_", "").replaceAll("_", " ");
        return `${label}: ${field.selectedOptions[0]?.textContent?.trim() || field.value}`;
      });
    if (selections.length) message += ` Selections: ${selections.join(", ")}.`;
    description.textContent = message;
    request.textContent = `${form.method.toUpperCase()} ${new URL(form.action).pathname}`;
    dialog.showModal();
  });

  confirmButton.addEventListener("click", () => {
    if (!activeForm) return;
    const confirmed = activeForm.elements.namedItem("confirmed");
    if (confirmed instanceof HTMLInputElement) confirmed.value = "true";
    const form = activeForm;
    activeForm = null;
    dialog.close();
    form.requestSubmit();
  });

  dialog.addEventListener("close", () => {
    if (!activeForm) return;
    const confirmed = activeForm.elements.namedItem("confirmed");
    if (confirmed instanceof HTMLInputElement) confirmed.value = "false";
    activeForm = null;
  });
})();

(() => {
  const panel = document.querySelector("[data-pipeline-status]");
  if (!(panel instanceof HTMLElement)) return;

  const connection = panel.querySelector("[data-live-connection]");
  const worker = panel.querySelector("[data-worker-state]");
  const activeStep = panel.querySelector("[data-active-step]");
  const elapsed = panel.querySelector("[data-cycle-elapsed]");
  const progress = panel.querySelector("[data-cycle-progress]");
  const progressText = panel.querySelector("[data-progress-text]");
  const activeProgress = panel.querySelector("[data-active-step-progress]");
  const activeProgressText = panel.querySelector("[data-active-progress-text]");
  const manual = panel.querySelector("[data-manual-cycles]");
  const continuations = panel.querySelector("[data-continuations]");
  const candidates = panel.querySelector("[data-candidates]");
  const windowState = panel.querySelector("[data-window-state]");
  const events = panel.querySelector("[data-recent-events]");
  let timer = 0;
  let requestInFlight = false;
  let failures = 0;

  const setText = (element, value) => {
    if (element) element.textContent = String(value);
  };

  const schedule = (delay) => {
    window.clearTimeout(timer);
    if (!document.hidden) timer = window.setTimeout(refresh, delay);
  };

  const render = (status) => {
    const queue = status.queue || {};
    const cycle = queue.active_cycle;
    setText(connection, "Live");
    setText(worker, cycle ? `Cycle #${cycle.id} · ${cycle.kind} · ${cycle.status}` : "Idle");
    const incident = queue.current_incident_id ? ` · incident #${queue.current_incident_id}` : "";
    setText(activeStep, queue.active_step_key ? `${queue.active_step_key} · ${queue.active_model}${incident}` : "No active model");
    if (cycle?.started_at) {
      const seconds = Math.max(0, Math.floor((new Date(status.generated_at) - new Date(cycle.started_at)) / 1000));
      const minutes = Math.floor(seconds / 60);
      setText(elapsed, `Elapsed ${minutes}m ${seconds % 60}s`);
    } else {
      setText(elapsed, "No active cycle");
    }
    const completed = Number(queue.cycle_completed || 0);
    const total = Number(queue.cycle_total || 0);
    const activeCompleted = Number(queue.active_step_completed || 0);
    const activeTotal = Number(queue.active_step_total || 0);
    setText(activeProgressText, `${activeCompleted} / ${activeTotal} incidents`);
    if (activeProgress instanceof HTMLElement) activeProgress.style.width = `${activeTotal ? Math.min(100, (activeCompleted / activeTotal) * 100) : 0}%`;
    setText(progressText, `${completed} / ${total} stage jobs`);
    if (progress instanceof HTMLElement) progress.style.width = `${total ? Math.min(100, (completed / total) * 100) : 0}%`;
    setText(manual, queue.manual_cycles || 0);
    setText(continuations, queue.continuation_cycles || 0);
    setText(candidates, queue.scheduled_candidates || 0);
    setText(windowState, status.window_open
      ? (status.scheduled_ready ? "Processing window open · scheduled starts ready" : "Processing window open · model configuration incomplete")
      : "Processing window closed · active/manual cycles may continue");
    for (const step of status.models?.steps || []) {
      const card = document.querySelector(`[data-step-card="${CSS.escape(step.key)}"]`);
      if (!card) continue;
      setText(card.querySelector("[data-step-model]"), step.preferred || "Not configured");
      setText(card.querySelector("[data-step-availability]"), step.preferred_available ? "✓ Installed and ready" : "✕ Select an installed model before scheduled processing can start");
    }
    for (const processor of status.models?.post_processors || []) {
      const card = document.querySelector(`[data-step-card="${CSS.escape(processor.key)}"]`);
      if (card) {
        setText(card.querySelector("[data-step-model]"), processor.preferred || "Not configured");
        setText(card.querySelector("[data-step-availability]"), processor.preferred_available ? "✓ Installed and ready" : "✕ Automatic processing is paused for this processor");
      }
    }
    const activeSteps = new Map((queue.active_steps || []).map((step) => [step.step_key, step]));
    for (const step of queue.steps || []) {
      const card = panel.querySelector(`[data-step-stat="${CSS.escape(step.step_key)}"]`);
      if (!card) continue;
      const active = activeSteps.get(step.step_key) || {};
      const average = Number(step.average_duration_seconds || 0).toFixed(1);
      const last = step.last_success ? new Date(step.last_success).toLocaleString() : "never";
      setText(card.querySelector("[data-active-step-stats]"), cycle
        ? `Waiting ${active.waiting || 0} · Ready after stage ${active.ready_after_stage || 0} · Queued ${active.queued || 0} · Running ${active.running || 0} · Retrying ${active.retrying || 0} · Review ${active.needs_review || 0} · Failed ${active.failed || 0} · Succeeded ${active.succeeded || 0}`
        : "No active cycle");
      setText(card.querySelector("[data-all-step-stats]"), `Succeeded ${step.succeeded || 0} · Review ${step.needs_review || 0} · Failed ${step.failed || 0} · Avg ${average}s · Last success ${last}`);
    }
    for (const processor of queue.post_processing || []) {
      const key = `${processor.processor_key}/${processor.scope_key}`;
      const card = panel.querySelector(`[data-post-processing-stat="${CSS.escape(key)}"]`);
      if (!card) continue;
      const counters = Object.entries(processor.counters || {}).map(([name, value]) => ` · ${name} ${value}`).join("");
      setText(card.querySelector("[data-post-processing-details]"), `Pending ${processor.pending || 0} · Running ${processor.running || 0} · Retrying ${processor.retrying || 0} · Review ${processor.needs_review || 0} · Failed ${processor.failed || 0} · Completed ${processor.succeeded || 0}${counters}`);
    }
    if (events) {
      events.replaceChildren(...(queue.recent_events || []).map((event) => {
        const item = document.createElement("li");
        const context = event.cycle_id ? `Cycle #${event.cycle_id}` : "Independent";
        item.textContent = `${context} · incident #${event.incident_id} · ${event.execution_key || event.step_key} · ${event.status}${event.failure_kind ? ` (${event.failure_kind})` : ""}`;
        return item;
      }));
      if (!events.childElementCount) {
        const item = document.createElement("li");
        item.textContent = "No pipeline events yet.";
        events.append(item);
      }
    }
  };

  async function refresh() {
    if (document.hidden || requestInFlight) return;
    requestInFlight = true;
    try {
      const response = await fetch(panel.dataset.statusUrl, {headers: {Accept: "application/json"}, cache: "no-store"});
      if (!response.ok) throw new Error(`status ${response.status}`);
      render(await response.json());
      failures = 0;
      schedule(2000);
    } catch (_error) {
      failures += 1;
      setText(connection, failures > 1 ? "Disconnected" : "Stale");
      schedule(Math.min(30000, 2000 * (2 ** Math.min(failures, 4))));
    } finally {
      requestInFlight = false;
    }
  }

  document.addEventListener("visibilitychange", () => {
    window.clearTimeout(timer);
    if (!document.hidden) refresh();
  });
  refresh();
})();
