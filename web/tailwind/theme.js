// Dark/light theme toggle. The actual theme application is performed
// inline in layout.html's <head> (before the stylesheet) to avoid a
// flash of the wrong theme on first paint. This file only wires up
// the toggle button, persists the user's choice, and keeps the page
// in sync with the OS preference until the user picks a side.
//
// Loaded once with `defer`; relies on event delegation so the button
// keeps working across htmx swaps (the toggle lives in the layout
// header, which htmx never replaces, but the delegation pattern
// matches copy.js and is robust to future moves).
(function () {
  const STORAGE_KEY = "theme";
  const root = document.documentElement;

  function currentTheme() {
    return root.classList.contains("dark") ? "dark" : "light";
  }

  function applyTheme(theme) {
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }

  function syncToggle(button) {
    if (!button) return;
    const theme = currentTheme();
    button.setAttribute("aria-checked", theme === "dark" ? "true" : "false");
    button.setAttribute(
      "aria-label",
      theme === "dark" ? "Switch to light theme" : "Switch to dark theme",
    );
  }

  function handleClick(event) {
    const button = event.target.closest("[data-theme-toggle]");
    if (!button) return;
    const next = currentTheme() === "dark" ? "light" : "dark";
    applyTheme(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch (_) {
      // Private mode / storage disabled: theme still applies for the
      // current page, just won't survive a reload. No-op on failure.
    }
    syncToggle(button);
  }

  // Track the OS preference so a user who never explicitly toggles
  // follows their system setting as it changes (e.g. day/night auto).
  function watchSystemPreference() {
    if (!window.matchMedia) return;
    const mql = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = function (e) {
      let stored = null;
      try {
        stored = localStorage.getItem(STORAGE_KEY);
      } catch (_) {
        // ignore
      }
      if (stored === "dark" || stored === "light") return;
      applyTheme(e.matches ? "dark" : "light");
      document
        .querySelectorAll("[data-theme-toggle]")
        .forEach(syncToggle);
    };
    if (mql.addEventListener) {
      mql.addEventListener("change", onChange);
    } else if (mql.addListener) {
      // Safari < 14 fallback.
      mql.addListener(onChange);
    }
  }

  document.addEventListener("click", handleClick);
  document.addEventListener("DOMContentLoaded", function () {
    document
      .querySelectorAll("[data-theme-toggle]")
      .forEach(syncToggle);
  });
  watchSystemPreference();
})();
