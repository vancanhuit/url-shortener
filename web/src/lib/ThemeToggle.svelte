<script lang="ts">
  import { onMount } from "svelte";

  // The actual theme application happens inline in index.html's <head>
  // before the stylesheet is parsed, to avoid a flash of the wrong
  // theme on first paint. This component just owns the toggle button,
  // persists the user's choice, and keeps the page in sync with the
  // OS preference until the user picks a side.
  const STORAGE_KEY = "theme";

  let isDark = $state(false);

  function applyTheme(theme: "dark" | "light"): void {
    if (theme === "dark") {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
    isDark = theme === "dark";
  }

  function toggle(): void {
    const next = isDark ? "light" : "dark";
    applyTheme(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Private mode / storage disabled: theme still applies for the
      // current page, just won't survive a reload.
    }
  }

  onMount(() => {
    isDark = document.documentElement.classList.contains("dark");
    if (!window.matchMedia) return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = (e: MediaQueryListEvent): void => {
      try {
        if (localStorage.getItem(STORAGE_KEY)) return;
      } catch {
        // ignore
      }
      applyTheme(e.matches ? "dark" : "light");
    };
    media.addEventListener("change", handler);
    return () => media.removeEventListener("change", handler);
  });
</script>

<button
  type="button"
  role="switch"
  aria-checked={isDark}
  aria-label={isDark ? "Switch to light theme" : "Switch to dark theme"}
  onclick={toggle}
  class="group relative shrink-0 mt-1 inline-flex h-7 w-12 items-center rounded-full bg-slate-200 ring-1 ring-slate-300 transition-colors hover:bg-slate-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2 focus-visible:ring-offset-slate-50 dark:bg-slate-800 dark:ring-slate-700 dark:hover:bg-slate-700 dark:focus-visible:ring-offset-slate-950"
>
  <span
    aria-hidden="true"
    class="pointer-events-none absolute inset-0 flex items-center justify-between px-1.5 text-amber-500 dark:text-indigo-300"
  >
    <svg viewBox="0 0 20 20" fill="currentColor" class="h-3.5 w-3.5">
      <path
        d="M10 2a.75.75 0 0 1 .75.75v1.5a.75.75 0 0 1-1.5 0v-1.5A.75.75 0 0 1 10 2Zm5.657 2.343a.75.75 0 0 1 0 1.06l-1.06 1.061a.75.75 0 1 1-1.061-1.06l1.06-1.061a.75.75 0 0 1 1.061 0ZM18 10a.75.75 0 0 1-.75.75h-1.5a.75.75 0 0 1 0-1.5h1.5A.75.75 0 0 1 18 10Zm-2.343 5.657a.75.75 0 0 1-1.06 0l-1.061-1.06a.75.75 0 1 1 1.06-1.061l1.061 1.06a.75.75 0 0 1 0 1.061ZM10 18a.75.75 0 0 1-.75-.75v-1.5a.75.75 0 0 1 1.5 0v1.5A.75.75 0 0 1 10 18Zm-5.657-2.343a.75.75 0 0 1 0-1.06l1.06-1.061a.75.75 0 1 1 1.061 1.06l-1.06 1.061a.75.75 0 0 1-1.061 0ZM2 10a.75.75 0 0 1 .75-.75h1.5a.75.75 0 0 1 0 1.5h-1.5A.75.75 0 0 1 2 10Zm2.343-5.657a.75.75 0 0 1 1.06 0l1.061 1.06a.75.75 0 1 1-1.06 1.061L4.343 5.404a.75.75 0 0 1 0-1.061ZM10 6a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z"
      />
    </svg>
    <svg viewBox="0 0 20 20" fill="currentColor" class="h-3.5 w-3.5">
      <path
        fill-rule="evenodd"
        d="M7.455 2.004a.75.75 0 0 1 .26.77 7 7 0 0 0 9.958 7.967.75.75 0 0 1 1.067.853A8.5 8.5 0 1 1 6.647 1.921a.75.75 0 0 1 .808.083Z"
        clip-rule="evenodd"
      />
    </svg>
  </span>
  <span
    aria-hidden="true"
    class="relative z-10 inline-block h-5 w-5 translate-x-1 rounded-full bg-white shadow ring-1 ring-slate-300 transition-transform dark:translate-x-6 dark:bg-slate-200 dark:ring-slate-400"
  ></span>
</button>
