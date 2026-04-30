// Copy-to-clipboard for the link-result panel. Wired up via
// `data-copy-target` so the link-result.html partial stays free of
// inline JS (cleaner CSP story, easier to maintain).
//
// Loaded once from layout.html with `defer`, and uses event delegation
// on document so it works even after htmx swaps the result panel in.
(function () {
  function handleClick(event) {
    const button = event.target.closest("[data-copy-target]");
    if (!button) return;

    const targetId = button.getAttribute("data-copy-target");
    const target = document.getElementById(targetId);
    if (!target) return;

    const value = target.value ?? target.textContent ?? "";
    navigator.clipboard.writeText(value).then(function () {
      const original = button.dataset.copyLabel || "Copy";
      button.textContent = "Copied";
      setTimeout(function () {
        button.textContent = original;
      }, 1500);
    });
  }

  document.addEventListener("click", handleClick);
})();
