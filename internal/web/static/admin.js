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
