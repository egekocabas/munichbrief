(() => {
  const storageKey = "munichbrief-timeline-return";
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
          !/^\/[a-z][a-z0-9-]*$/.test(timeline.pathname) ||
          !/^(\?page=[1-9]\d*)?$/.test(timeline.search) || timeline.hash ||
          !new RegExp("^" + timeline.pathname + "/incidents/[1-9]\\d*$").test(incident.pathname) ||
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

  const updateNavigation = () => {
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
  document.addEventListener("htmx:afterSettle", updateNavigation);
  document.addEventListener("htmx:historyRestore", updateNavigation);
  window.addEventListener("pageshow", updateNavigation);
})();
