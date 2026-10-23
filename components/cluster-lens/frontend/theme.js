(() => {
  const key = "kubephos-theme";
  const media = window.matchMedia?.("(prefers-color-scheme: dark)");
  const icons = {
    light: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M20.5 14.2A8.4 8.4 0 0 1 9.8 3.5 8.5 8.5 0 1 0 20.5 14.2z"/></svg>',
    dark: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42M2 12h2m16 0h2M4.93 19.07l1.42-1.42m11.3-11.3 1.42-1.42"/></svg>',
  };
  function preference() {
    try {
      const saved = window.localStorage.getItem(key);
      if (saved === "light" || saved === "dark") return saved;
    } catch {}
    return media?.matches ? "dark" : "light";
  }
  function apply(theme) {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    const button = document.getElementById("themeToggle");
    if (button) {
      button.setAttribute("aria-label", theme === "dark" ? "Use light mode" : "Use dark mode");
      button.setAttribute("aria-pressed", String(theme === "dark"));
      button.title = theme === "dark" ? "Light mode" : "Dark mode";
      button.innerHTML = icons[theme];
    }
    window.dispatchEvent(new CustomEvent("kubephos-theme-change", {detail: theme}));
  }
  apply(preference());
  document.addEventListener("DOMContentLoaded", () => {
    apply(preference());
    document.getElementById("themeToggle")?.addEventListener("click", () => {
      const theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
      try { window.localStorage.setItem(key, theme); } catch {}
      apply(theme);
    });
  }, {once: true});
  window.addEventListener("storage", event => {
    if (event.key === key || event.key === null) apply(preference());
  });
  media?.addEventListener("change", () => apply(preference()));
})();
