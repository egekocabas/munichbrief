(() => {
  const dialog = document.querySelector("#processing-confirmation");
  const description = document.querySelector("#processing-confirmation-description");
  const request = document.querySelector("#processing-confirmation-request");
  const behavior = document.querySelector("#processing-confirmation-behavior");
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
    const selections = [...form.querySelectorAll('select[name="scope"], select[name="model"], select[name^="model_"], select[name="adapter"]')]
      .filter((field) => field instanceof HTMLSelectElement && field.value)
      .map((field) => {
        const label = field.labels?.[0]?.textContent?.trim() || field.name.replace("model_", "").replaceAll("_", " ");
        return `${label}: ${field.selectedOptions[0]?.textContent?.trim() || field.value}`;
      });
    if (selections.length) message += ` Selections: ${selections.join(", ")}.`;
    description.textContent = message;
    if (behavior) behavior.textContent = form.dataset.confirmBehavior || "Bypasses the configured time window, wakes the worker, and runs asynchronously. Jobs remain sequential and keep normal privacy validation, circuit breaking, and retry delays.";
    const action = new URL(form.getAttribute("action") || window.location.href, window.location.href);
    request.textContent = `${form.method.toUpperCase()} ${action.pathname}`;
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
  const automaticState = panel.querySelector("[data-automatic-state]");
  const automaticValue = panel.querySelector("[data-automatic-value]");
  const automaticButton = panel.querySelector("[data-automatic-button]");
  const events = panel.querySelector("[data-recent-events]");
  let timer = 0;
  let requestInFlight = false;
  let failures = 0;

  const setText = (element, value) => {
    if (element) element.textContent = String(value);
  };

  const postProcessingStatusReason = (reason, detail) => {
    if (reason !== "missing_input") return String(reason || "").replaceAll("_", " ");
    if (detail === "incident_body") return "Original incident text unavailable";
    if (!detail) return "Required input unavailable";
    return `Required input ${String(detail).replaceAll("_", " ")} unavailable`;
  };

  const formatDuration = (seconds) => {
    const value = Math.max(0, Math.floor(seconds));
    const days = Math.floor(value / 86400);
    const hours = Math.floor((value % 86400) / 3600);
    const minutes = Math.floor((value % 3600) / 60);
    const remainder = value % 60;
    if (days) return `${days}d ${hours}h ${minutes}m`;
    if (hours) return `${hours}h ${minutes}m`;
    return `${minutes}m ${remainder}s`;
  };

  const failureKindLabel = (kind) => ({
    transient: "temporary provider",
    configuration: "configuration",
    output: "invalid output",
    privacy: "privacy review",
  })[kind] || String(kind).replaceAll("_", " ");

  const schedule = (delay) => {
    window.clearTimeout(timer);
    if (!document.hidden) timer = window.setTimeout(refresh, delay);
  };

  const render = (status) => {
    const queue = status.queue || {};
    const cycle = queue.active_cycle;
    setText(connection, "Live");
    const running = status.running;
    const runningKey = running?.key || "";
    const runningSummary = panel.querySelector("[data-running-summary]");
    if (runningSummary) runningSummary.dataset.running = String(Boolean(running));
    setText(panel.querySelector("[data-running-key]"), runningKey || (cycle ? "Waiting for eligible work" : "Idle"));
    setText(panel.querySelector("[data-running-model]"), running ? `${running.model} · incident #${running.incident_id}` : "No request in progress");
    for (const card of panel.querySelectorAll("[data-execution-key]")) {
      const isRunning = card.dataset.executionKey === runningKey;
      card.dataset.running = String(isRunning);
      const badge = card.querySelector("[data-running-badge]");
      if (badge) badge.hidden = !isRunning;
    }
    const batch = status.translation_batch || {};
    setText(panel.querySelector("[data-batch-model]"), batch.model || "No active batch");
    setText(panel.querySelector("[data-batch-count]"), batch.limit ? `${batch.attempts || 0} / ${batch.limit} attempts` : "Batch status unavailable");
    const batchProgress = panel.querySelector("[data-batch-progress]");
    if (batchProgress instanceof HTMLProgressElement) {
      batchProgress.max = Number(batch.limit) || 1;
      batchProgress.value = Math.min(Number(batch.attempts) || 0, batchProgress.max);
    }
    setText(panel.querySelector("[data-batch-next]"), batch.next_model || "No eligible job");
    setText(panel.querySelector("[data-batch-next-detail]"), batch.next_model ? `${batch.next_scope} · ${batch.next_request_kind}` : "Waiting for queued work to become eligible");
    setText(panel.querySelector("[data-batch-switch]"), batch.next_switch_model || "No other eligible model");
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
    if (activeProgress instanceof HTMLProgressElement) activeProgress.value = activeTotal ? activeCompleted / activeTotal : 0;
    setText(progressText, `${completed} / ${total} stage jobs`);
    if (progress instanceof HTMLProgressElement) progress.value = total ? completed / total : 0;
    setText(manual, queue.manual_cycles || 0);
    setText(continuations, queue.continuation_cycles || 0);
    setText(candidates, queue.scheduled_candidates || 0);
    const automaticEnabled = Boolean(status.automatic_processing_enabled);
    setText(automaticState, automaticEnabled ? "Automatic AI processing enabled" : "Automatic AI processing disabled");
    if (automaticValue instanceof HTMLInputElement) automaticValue.value = automaticEnabled ? "false" : "true";
    setText(automaticButton, automaticEnabled ? "Disable automatic processing" : "Enable automatic processing");
    setText(windowState, !automaticEnabled
      ? `${status.window_open ? "Processing window open" : "Processing window closed"} · automatic processing disabled · manual requests remain available`
      : status.window_open
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
      setText(card.querySelector("[data-step-brief]"), `Queued ${active.queued || 0} · Retrying ${active.retrying || 0}`);
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
      setText(card.querySelector("[data-post-processing-brief]"), `Queued ${processor.pending || 0} · Retrying ${processor.retrying || 0}`);
      const counters = Object.entries(processor.counters || {}).map(([name, value]) => ` · ${name} ${value}`).join("");
      setText(card.querySelector("[data-post-processing-details]"), `Pending ${processor.pending || 0} · Running ${processor.running || 0} · Retrying ${processor.retrying || 0} · Review ${processor.needs_review || 0} · Failed ${processor.failed || 0} · Skipped ${processor.skipped || 0} · Completed ${processor.succeeded || 0}${counters}`);
      const runningStartedAt = processor.running_started_at && new Date(processor.running_started_at);
      if (runningStartedAt && !Number.isNaN(runningStartedAt.getTime())) {
        const seconds = Math.max(0, Math.floor((new Date(status.generated_at) - runningStartedAt) / 1000));
        setText(card.querySelector("[data-post-processing-elapsed]"), `Current job elapsed ${formatDuration(seconds)}`);
      } else {
        setText(card.querySelector("[data-post-processing-elapsed]"), "No running job");
      }
      const retrying = Number(processor.retrying || 0);
      const automaticRetrying = Number(processor.automatic_retrying || 0);
      const automaticRetryReady = Number(processor.automatic_retry_ready || 0);
      const retryReasons = Object.entries(processor.retry_failure_kinds || {})
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([name, value]) => `${failureKindLabel(name)} ${value}`)
        .join(" · ");
      const reasonSuffix = retryReasons ? ` · reasons: ${retryReasons}` : "";
      const attentionReasons = Object.entries(processor.attention_failure_kinds || {})
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([name, value]) => `${failureKindLabel(name)} ${value}`)
        .join(" · ");
      const nextRetryAt = processor.next_retry_at && new Date(processor.next_retry_at);
      let retryState = "No retry backlog";
      if (retrying) {
        if (automaticRetrying && !automaticEnabled) {
          retryState = `${automaticRetrying} automatic ${automaticRetrying === 1 ? "retry is" : "retries are"} paused because automatic processing is disabled${reasonSuffix}`;
        } else if (automaticRetrying && !status.window_open) {
          retryState = automaticRetryReady
            ? `${automaticRetryReady} of ${automaticRetrying} automatic ${automaticRetrying === 1 ? "retry is" : "retries are"} ready but paused until the next processing window${reasonSuffix}`
            : `${automaticRetrying} automatic ${automaticRetrying === 1 ? "retry is" : "retries are"} waiting for backoff or the next processing window${reasonSuffix}`;
        } else if (nextRetryAt && !Number.isNaN(nextRetryAt.getTime()) && nextRetryAt > new Date(status.generated_at)) {
          retryState = `Next retry after ${nextRetryAt.toLocaleString("en-GB")}${reasonSuffix}`;
        } else {
          retryState = `${retrying} ${retrying === 1 ? "retry is" : "retries are"} eligible for processing${reasonSuffix}`;
        }
      }
      if (attentionReasons) retryState += ` · review/failed reasons: ${attentionReasons}`;
      setText(card.querySelector("[data-post-processing-retry-state]"), retryState);
      const queueStartedAt = processor.queue_started_at && new Date(processor.queue_started_at);
      if (queueStartedAt && !Number.isNaN(queueStartedAt.getTime())) {
        const seconds = Math.max(0, Math.floor((new Date(status.generated_at) - queueStartedAt) / 1000));
        setText(card.querySelector("[data-post-processing-queue-age]"), `Oldest unfinished job queued ${formatDuration(seconds)} ago`);
      } else {
        setText(card.querySelector("[data-post-processing-queue-age]"), "No unfinished jobs");
      }
    }
    if (events) {
      events.replaceChildren(...(queue.recent_events || []).map((event) => {
        const item = document.createElement("li");
        const context = event.cycle_id ? `Cycle #${event.cycle_id}` : "Independent";
        const detail = event.status_reason
          ? postProcessingStatusReason(event.status_reason, event.status_detail)
          : event.failure_kind;
        const canceled = event.failure_kind === "operator_canceled" || event.status_reason === "operator_canceled";
        const eventStatus = canceled ? "Canceled" : event.status;
        const eventDetail = canceled ? "operator canceled" : detail;
        item.textContent = `${context} · incident #${event.incident_id} · ${event.execution_key || event.step_key} · ${eventStatus}${eventDetail ? ` (${eventDetail})` : ""}`;
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
      setText(panel.querySelector("[data-running-key]"), "Live status unavailable");
      setText(panel.querySelector("[data-running-model]"), "Showing the last received queue snapshot");
      for (const card of panel.querySelectorAll("[data-running]")) card.dataset.running = "false";
      for (const badge of panel.querySelectorAll("[data-running-badge]")) badge.hidden = true;
      setText(panel.querySelector("[data-batch-next]"), "Preview unavailable");
      setText(panel.querySelector("[data-batch-next-detail]"), "Waiting for connection");
      setText(panel.querySelector("[data-batch-switch]"), "Preview unavailable");
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

(() => {
  const history = document.querySelector("[data-pipeline-history], [data-rss-history], [data-gazetteer-history]");
  if (!(history instanceof HTMLElement)) return;

  const interval = Number(history.dataset.historyPollInterval) || 2000;
  let timer = 0;
  let requestInFlight = false;
  let failures = 0;

  const schedule = (delay) => {
    window.clearTimeout(timer);
    if (!document.hidden) timer = window.setTimeout(refresh, delay);
  };

  async function refresh() {
    if (document.hidden || requestInFlight) return;
    requestInFlight = true;
    try {
      const response = await fetch(window.location.href, {headers: {Accept: "text/html"}, cache: "no-store"});
      if (!response.ok) throw new Error(`status ${response.status}`);
      const parsedDocument = new DOMParser().parseFromString(await response.text(), "text/html");
      const nextHistory = parsedDocument.querySelector("[data-pipeline-history], [data-rss-history], [data-gazetteer-history]");
      if (!(nextHistory instanceof HTMLElement)) throw new Error("history content missing");
      if (history.innerHTML !== nextHistory.innerHTML) {
        if (history.matches("[data-rss-history], [data-gazetteer-history]")) {
          const gazetteer = history.matches("[data-gazetteer-history]");
          const rowSelector = gazetteer ? "[data-gazetteer-row]" : "[data-rss-row]";
          const rowKey = gazetteer ? "gazetteerRow" : "rssRow";
          const panelPrefix = gazetteer ? "gazetteer-run-" : "rss-check-";
          const tableSelector = gazetteer ? ".gazetteer-history-table" : ".rss-history-table";
          const focused = document.activeElement;
          const scrollX = window.scrollX;
          const scrollY = window.scrollY;
          const tableScroll = history.querySelector(tableSelector)?.scrollLeft || 0;
          // Reuse existing detail nodes: polling must not discard loaded snapshots.
          for (const row of history.querySelectorAll(rowSelector)) {
            const id = row.dataset[rowKey];
            const nextRow = nextHistory.querySelector(`${rowSelector.slice(0, -1)}="${id}"]`);
            const panel = history.querySelector(`#${panelPrefix}${id}`);
            if (nextRow) {
              for (let i = 1; i < row.cells.length; i += 1) {
                if (!row.cells[i].contains(focused) && row.cells[i].innerHTML !== nextRow.cells[i].innerHTML) {
                  row.cells[i].replaceChildren(...nextRow.cells[i].childNodes);
                }
              }
              nextRow.replaceWith(row);
              nextHistory.querySelector(`#${panelPrefix}${id}`)?.replaceWith(panel);
            } else if (panel && (!panel.hidden || row.contains(focused))) {
              // Keep a check being read even when a new check pushes it off the page.
              nextHistory.querySelector("tbody")?.append(row, panel);
            }
          }
          history.replaceChildren(...nextHistory.childNodes);
          const table = history.querySelector(tableSelector);
          if (table) table.scrollLeft = tableScroll;
          if (focused instanceof HTMLElement && focused.isConnected) focused.focus({preventScroll: true});
          window.scrollTo(scrollX, scrollY);
        } else {
          history.replaceChildren(...nextHistory.childNodes);
        }
      }
      failures = 0;
      schedule(interval);
    } catch (_error) {
      failures += 1;
      schedule(Math.min(30000, interval * (2 ** Math.min(failures, 4))));
    } finally {
      requestInFlight = false;
    }
  }

  document.addEventListener("visibilitychange", () => {
    window.clearTimeout(timer);
    if (!document.hidden) refresh();
  });
  schedule(interval);
})();

const initializeOperationsRefresh = ({regionSelector, intervalAttribute, connectionSelector, contentLabel}) => {
  const region = document.querySelector(regionSelector);
  if (!(region instanceof HTMLElement)) return;

  const interval = Number(region.dataset[intervalAttribute]) || 5000;
  const confirmation = document.querySelector("#processing-confirmation");
  let timer = 0;
  let requestInFlight = false;
  let failures = 0;

  const setConnection = (value) => {
    const connection = region.querySelector(connectionSelector);
    if (connection) connection.textContent = value;
  };

  const schedule = (delay) => {
    window.clearTimeout(timer);
    if (!document.hidden) timer = window.setTimeout(refresh, delay);
  };

  const shouldPause = () => {
    const focused = document.activeElement;
    return document.hidden
      || requestInFlight
      || (confirmation instanceof HTMLDialogElement && confirmation.open)
      || (focused instanceof Element && region.contains(focused) && focused.closest("form"));
  };

  async function refresh() {
    if (shouldPause()) {
      schedule(interval);
      return;
    }
    requestInFlight = true;
    try {
      const response = await fetch(window.location.href, {headers: {Accept: "text/html"}, cache: "no-store"});
      if (!response.ok) throw new Error(`status ${response.status}`);
      const parsedDocument = new DOMParser().parseFromString(await response.text(), "text/html");
      const nextRegion = parsedDocument.querySelector(regionSelector);
      if (!(nextRegion instanceof HTMLElement)) throw new Error(`${contentLabel} content missing`);
      const scrollX = window.scrollX;
      const scrollY = window.scrollY;
      region.replaceChildren(...nextRegion.childNodes);
      window.scrollTo(scrollX, scrollY);
      failures = 0;
      setConnection("Connected");
      schedule(interval);
    } catch (_error) {
      failures += 1;
      setConnection("Stale");
      for (const card of region.querySelectorAll("[data-running]")) card.dataset.running = "false";
      for (const badge of region.querySelectorAll("[data-running-badge]")) badge.hidden = true;
      const runningKey = region.querySelector("[data-running-key]");
      if (runningKey) runningKey.textContent = "Live status unavailable";
      const runningModel = region.querySelector("[data-running-model]");
      if (runningModel) runningModel.textContent = "Showing the last received queue snapshot";
      const nextDetail = region.querySelector("[data-batch-next-detail]");
      if (nextDetail) nextDetail.textContent = "Waiting for connection";
      for (const preview of region.querySelectorAll("[data-batch-next], [data-batch-switch]")) preview.textContent = "Preview unavailable";
      schedule(Math.min(30000, interval * (2 ** Math.min(failures, 3))));
    } finally {
      requestInFlight = false;
    }
  }

  document.addEventListener("visibilitychange", () => {
    window.clearTimeout(timer);
    if (!document.hidden) refresh();
  });
  schedule(interval);
};

initializeOperationsRefresh({
  regionSelector: "[data-translation-operations]",
  intervalAttribute: "translationPollInterval",
  connectionSelector: "[data-translation-connection]",
  contentLabel: "translation operations",
});

initializeOperationsRefresh({
  regionSelector: "[data-verification-operations]",
  intervalAttribute: "verificationPollInterval",
  connectionSelector: "[data-verification-connection]",
  contentLabel: "verification operations",
});


// Anchors provide full-page navigation without JavaScript; enhanced navigation
// loads protected HTML fragments only when a check/document is expanded.
document.addEventListener("click", async (event) => {
  const toggle = event.target.closest?.("[data-rss-toggle]");
  if (!toggle || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const panel = document.getElementById(toggle.getAttribute("aria-controls"));
  if (!panel) return;
  event.preventDefault();
  const open = toggle.getAttribute("aria-expanded") !== "true";
  toggle.setAttribute("aria-expanded", String(open));
  panel.hidden = !open;
  if (!open || panel.dataset.loading === "true") return;
  // Reopening refreshes running checks while preserving a loaded panel when closed.
  const content = panel.matches("[data-rss-content]") ? panel : panel.querySelector("[data-rss-content]");
  if (!content) return;
  panel.dataset.loading = "true";
  content.setAttribute("aria-busy", "true");
  if (!panel.dataset.loaded) content.textContent = "Loading details…";
  try {
    const url = new URL(toggle.href);
    url.searchParams.set("fragment", "1");
    const response = await fetch(url, {headers: {Accept: "text/html"}, cache: "no-store"});
    if (!response.ok) throw new Error("Details request failed");
    const html = new DOMParser().parseFromString(await response.text(), "text/html");
    content.replaceChildren(...html.body.childNodes);
    panel.dataset.loaded = "true";
  } catch (_error) {
    if (!panel.dataset.loaded) content.textContent = "Could not load details. Close and reopen to retry, or open the details link in a new tab.";
  } finally {
    panel.dataset.loading = "false";
    content.removeAttribute("aria-busy");
  }
});
