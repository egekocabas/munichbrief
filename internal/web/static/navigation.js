(() => {
  const storageKey = "munichbrief-timeline-return";
  document.documentElement.classList.add("reader-js");
  const currentURL = () => location.pathname + location.search;

  const readReturn = () => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(storageKey));
      if (!saved) return null;
      const timeline = new URL(saved.timeline, location.origin);
      const incident = new URL(saved.incident, location.origin);
      const referrer = document.referrer ? new URL(document.referrer) : null;
      const navigationType = performance.getEntriesByType("navigation")[0]?.type;
      if (timeline.origin !== location.origin || incident.origin !== location.origin ||
          !/^\/[a-z][a-z0-9-]*(?:\/search)?$/.test(timeline.pathname) ||
          !validListingQuery(timeline.searchParams) || timeline.hash ||
          !new RegExp("^" + timeline.pathname.replace(/\/search$/, "") + "/incidents/[1-9]\\d*$").test(incident.pathname) ||
          incident.search || incident.hash ||
          !Number.isFinite(saved.scrollY) || saved.scrollY < 0) return null;
      // A fresh direct visit must not inherit an unrelated earlier visit's Back link.
      if (navigationType !== "reload" && navigationType !== "back_forward" &&
          referrer?.href !== timeline.href && referrer?.href !== incident.href) return null;
      return saved;
    } catch (_error) {
      return null;
    }
  };

  function validListingQuery(params) {
    if (params.toString().length > 8192) return false;
    const seen = new Set();
    let neighborhoods = 0;
    let neighborhoodBytes = 0;
    for (const [key, value] of params) {
      if (seen.has(key) && key !== "neighborhood") return false;
      seen.add(key);
      if (key === "neighborhood" && value.trim() && [...value].length <= 200 && !value.includes("\0")) {
        neighborhoods++;
        neighborhoodBytes += new TextEncoder().encode(value).length;
        if (neighborhoods > 10 || neighborhoodBytes > 1000) return false;
        continue;
      }
      if (key === "period" && /^(today|week)$/.test(value)) continue;
      if (key === "page" && /^[1-9]\d{0,8}$/.test(value)) continue;
      if (key === "page_size" && /^(10|20|30|50)$/.test(value)) continue;
      if (key === "view" && /^(published|incident)$/.test(value)) continue;
      if (["q", "area", "number"].includes(key) && [...value].length <= 200 && !value.includes("\0")) continue;
      if (key === "category" && /^(traffic|theft_burglary|robbery_extortion|violence|sexual_offense|fraud_cyber|drugs|fire_hazard|property_damage|missing_wanted|police_operation|other)$/.test(value)) continue;
      if (key === "assistance" && /^(yes|no)$/.test(value)) continue;
      if (key === "date_field" && /^(published|incident)$/.test(value)) continue;
      if ((key === "from" || key === "to") && /^\d{4}-\d{2}-\d{2}$/.test(value)) continue;
      return false;
    }
    return true;
  }
  document.addEventListener("change", (event) => {
    if (event.target instanceof HTMLSelectElement && event.target.matches("[data-auto-submit]")) event.target.form?.requestSubmit();
    const field = event.target;
    const form = field.form;
    if (form?.matches(".reader-search-form")) {
      // Keep visible controls consistent before submitting. Native forms
      // without JavaScript still use the documented custom-range precedence.
      if (field.name === "period") {
        form.elements.from.value = "";
        form.elements.to.value = "";
        form.elements.date_field.value = "published";
      } else if (["from", "to", "date_field"].includes(field.name)) {
        form.elements.period.value = "";
      }
    }
    if (form?.matches("[data-neighborhood-limit]")) {
      const choices = [...form.querySelectorAll('[name="saved_neighborhood"]')];
      const selected = choices.filter(input => input.checked);
      const bytes = selected.reduce((total, input) => total + new TextEncoder().encode(input.value).length, 0);
      choices.forEach(input => input.setCustomValidity(""));
      if (selected.length > 10 || bytes > 1000) field.setCustomValidity(form.dataset.neighborhoodLimit);
    }
  });
  let returnTo = readReturn();
  const saveReturn = () => {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(returnTo));
    } catch (_error) {
      // Boosted navigation still retains the return destination in memory.
    }
  };
  saveReturn();

  const updateBackLink = (back) => {
    back.href = back.dataset.timelineBack;
    if (returnTo?.incident === location.pathname && !location.search) {
      back.href = returnTo.timeline;
    }
  };

  const updateDocumentLanguage = () => {
    // HTMX replaces the body contents, so keep the document language in sync
    // for readers and language-specific typography after switching languages.
    const language = document.querySelector("#content")?.dataset.languageTag;
    if (language) document.documentElement.lang = language;
    const aiState = document.querySelector("#content")?.dataset.aiState;
    if (aiState) document.documentElement.dataset.aiGenerated = aiState;
  };

  const updateNavigation = () => {
    updateDocumentLanguage();
    const back = document.querySelector("[data-timeline-back]");
    if (back) updateBackLink(back);
    if (returnTo?.restore && currentURL() === returnTo.timeline) {
      const { timeline, scrollY } = returnTo;
      returnTo.restore = false;
      saveReturn();
      // HTMX applies its default scroll after afterSettle; restore on the next frame.
      requestAnimationFrame(() => {
        if (currentURL() === timeline) window.scrollTo({ top: scrollY, behavior: "instant" });
      });
    }
  };

  document.addEventListener("click", (event) => {
    if (event.defaultPrevented || event.button !== 0 || !(event.target instanceof Element)) return;
    const link = event.target.closest("a");
    if (!link || link.origin !== location.origin) return;
    const timeline = link.closest("[data-timeline-url]");
    if (link.hasAttribute("data-incident-link") && timeline) {
      returnTo = {
        incident: link.pathname,
        timeline: timeline.dataset.timelineUrl,
        scrollY: window.scrollY,
        restore: false,
      };
      saveReturn();
    } else if (link.hasAttribute("data-timeline-back") &&
               returnTo?.incident === location.pathname &&
               link.pathname + link.search === returnTo.timeline) {
      returnTo.restore = true;
      saveReturn();
    }
  }, true);

  // Boosted anchors capture their destination when HTMX initializes them.
  document.addEventListener("htmx:beforeProcessNode", (event) => {
    if (event.detail.elt.matches("[data-timeline-back]")) updateBackLink(event.detail.elt);
  });
  document.addEventListener("DOMContentLoaded", updateNavigation);
  document.addEventListener("htmx:afterSwap", updateDocumentLanguage);
  document.addEventListener("htmx:afterSettle", updateNavigation);
  document.addEventListener("htmx:historyRestore", updateNavigation);
  document.addEventListener("htmx:historyCacheMiss", (event) => {
    // Browser Back (including trackpad gestures) bypasses the All reports click.
    // Carry the saved position through the full-page history reload as well.
    if (returnTo && currentURL() === returnTo.timeline) {
      returnTo.restore = true;
      saveReturn();
    }
    // Public history snapshots are disabled. A native restoration also keeps
    // every head tag and document attribute aligned with the server response.
    event.preventDefault();
    location.reload();
  });
  window.addEventListener("pageshow", updateNavigation);
})();
