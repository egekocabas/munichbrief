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
    const seen = new Set();
    for (const [key, value] of params) {
      if (seen.has(key)) return false;
      seen.add(key);
      if (key === "page" && /^[1-9]\d{0,8}$/.test(value)) continue;
      if (key === "page_size" && /^(10|20|30|50)$/.test(value)) continue;
      if (key === "view" && /^(published|incident)$/.test(value)) continue;
      return false;
    }
    return true;
  }
  document.addEventListener("change", (event) => {
    if (event.target instanceof HTMLSelectElement && event.target.matches("[data-auto-submit]")) event.target.form?.requestSubmit();
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
  document.addEventListener("htmx:historyCacheMiss", () => {
    // Public history snapshots are disabled. A native restoration also keeps
    // every head tag and document attribute aligned with the server response.
    location.reload();
  });
  window.addEventListener("pageshow", updateNavigation);
})();
