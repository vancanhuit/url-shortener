// Theme boot: applies the saved theme (or system preference if none)
// BEFORE the stylesheet is parsed so the page never paints with the
// wrong colours. Referenced as a synchronous (no defer/async) script
// from index.html so it runs before first paint. Must stay a plain
// script to avoid requiring 'unsafe-inline' in Content-Security-Policy.
(function () {
  try {
    var t = localStorage.getItem("theme");
    var dark =
      t === "dark" ||
      (t !== "light" &&
        window.matchMedia &&
        window.matchMedia("(prefers-color-scheme: dark)").matches);
    if (dark) document.documentElement.classList.add("dark");
  } catch {
    // localStorage may throw in private mode -- fall back to light.
  }
})();
