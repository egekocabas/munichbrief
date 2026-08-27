(() => {
  const storageKey = "munichbrief-theme";
  const root = document.documentElement;
  const systemPreference = window.matchMedia("(prefers-color-scheme: dark)");

  const storedTheme = () => {
    try {
      const value = window.localStorage.getItem(storageKey);
      return value === "light" || value === "dark" ? value : null;
    } catch (_error) {
      return null;
    }
  };

  const updateControls = () => {
    const dark = root.dataset.theme === "dark";
    document.querySelectorAll("[data-theme-toggle]").forEach((control) => {
      control.setAttribute("aria-pressed", String(dark));
    });
  };

  const applyTheme = (theme, persist = false) => {
    root.dataset.theme = theme;
    if (persist) {
      try {
        window.localStorage.setItem(storageKey, theme);
      } catch (_error) {
        // The selected theme still applies when storage is unavailable.
      }
    }
    updateControls();
  };

  applyTheme(storedTheme() || (systemPreference.matches ? "dark" : "light"));

  document.addEventListener("click", (event) => {
    if (!(event.target instanceof Element)) return;
    const control = event.target.closest("[data-theme-toggle]");
    if (!(control instanceof HTMLButtonElement)) return;
    applyTheme(root.dataset.theme === "dark" ? "light" : "dark", true);
  });

  document.addEventListener("DOMContentLoaded", updateControls);
  document.addEventListener("htmx:afterSettle", updateControls);
  systemPreference.addEventListener("change", (event) => {
    if (!storedTheme()) applyTheme(event.matches ? "dark" : "light");
  });
})();
