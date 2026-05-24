<script lang="ts">
  import { onMount } from "svelte";
  import { listLinks, getVersion, type Link } from "./lib/api";
  import LinkForm from "./lib/LinkForm.svelte";
  import LinkResult from "./lib/LinkResult.svelte";
  import LinkError from "./lib/LinkError.svelte";
  import RecentList from "./lib/RecentList.svelte";
  import ThemeToggle from "./lib/ThemeToggle.svelte";

  let items: Link[] = $state([]);
  let nextCursor: number | null = $state(null);
  let version: string | null = $state(null);

  let result: { link: Link; created: boolean } | null = $state(null);
  let error: { message: string } | null = $state(null);

  // Pull the first page of recent links on mount. Errors here are
  // logged + surfaced as an empty list rather than a hard error so a
  // flaking database doesn't hide the create form.
  async function refreshFirstPage(): Promise<void> {
    try {
      const page = await listLinks({});
      items = page.items ?? [];
      nextCursor = page.next_cursor ?? null;
    } catch (e) {
      console.warn("recent list fetch failed", e);
      items = [];
      nextCursor = null;
    }
  }

  onMount(async () => {
    await refreshFirstPage();
    try {
      const info = await getVersion();
      version = info.version;
    } catch {
      // Version fetch is best-effort; don't surface failures to the user.
    }
  });

  async function onCreated(payload: { link: Link; created: boolean }): Promise<void> {
    result = payload;
    error = null;
    await refreshFirstPage();
  }

  function onError(err: { message: string }): void {
    error = err;
    result = null;
  }

  async function onDeleted(code: string): Promise<void> {
    items = items.filter((it) => it.code !== code);
    if (result?.link.code === code) {
      result = null;
    }
  }

  async function onLoadMore(): Promise<void> {
    if (nextCursor === null) return;
    try {
      const page = await listLinks({ before: nextCursor });
      items = [...items, ...(page.items ?? [])];
      nextCursor = page.next_cursor ?? null;
    } catch (e) {
      console.warn("recent list pagination failed", e);
    }
  }
</script>

<div class="flex-1 w-full mx-auto max-w-2xl px-4 sm:px-6 py-8 sm:py-12">
  <header class="mb-8 sm:mb-10 flex items-start gap-3">
    <span
      aria-hidden="true"
      class="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-500 to-violet-600 text-white shadow-sm ring-1 ring-indigo-500/20 dark:ring-indigo-400/30"
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        class="h-5 w-5"
      >
        <path d="M10 13a5 5 0 0 0 7.07 0l3-3a5 5 0 1 0-7.07-7.07l-1.5 1.5" />
        <path d="M14 11a5 5 0 0 0-7.07 0l-3 3a5 5 0 1 0 7.07 7.07l1.5-1.5" />
      </svg>
    </span>
    <div class="flex-1 min-w-0">
      <h1 class="text-2xl sm:text-3xl font-bold tracking-tight">URL Shortener</h1>
      <p class="text-sm sm:text-base text-slate-600 dark:text-slate-400 mt-0.5">
        Paste a long URL and get a short one back.
      </p>
    </div>
    <ThemeToggle />
  </header>

  <main>
    <LinkForm onSuccess={onCreated} onFailure={onError} />

    <div class="mt-6">
      {#if error}
        <LinkError message={error.message} />
      {:else if result}
        <LinkResult link={result.link} />
      {/if}
    </div>

    <section class="mt-10 sm:mt-12">
      <h2 class="text-base sm:text-lg font-semibold text-slate-800 dark:text-slate-200 mb-3">
        Recent
      </h2>
      <RecentList {items} {nextCursor} {onLoadMore} {onDeleted} />
    </section>
  </main>
</div>

<footer
  class="border-t border-slate-200 dark:border-slate-800 bg-white/50 dark:bg-slate-900/50 backdrop-blur"
>
  <div
    class="mx-auto max-w-2xl px-4 sm:px-6 py-5 text-xs sm:text-sm text-slate-500 dark:text-slate-400 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2"
  >
    <p>Built with</p>
    <ul class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <li>
        <a
          href="https://go.dev"
          target="_blank"
          rel="noopener noreferrer"
          class="font-medium text-slate-700 hover:text-indigo-600 dark:text-slate-300 dark:hover:text-indigo-400 underline-offset-4 hover:underline transition-colors"
          >Go</a
        >
      </li>
      <li>
        <a
          href="https://svelte.dev"
          target="_blank"
          rel="noopener noreferrer"
          class="font-medium text-slate-700 hover:text-indigo-600 dark:text-slate-300 dark:hover:text-indigo-400 underline-offset-4 hover:underline transition-colors"
          >Svelte</a
        >
      </li>
      <li>
        <a
          href="https://vite.dev"
          target="_blank"
          rel="noopener noreferrer"
          class="font-medium text-slate-700 hover:text-indigo-600 dark:text-slate-300 dark:hover:text-indigo-400 underline-offset-4 hover:underline transition-colors"
          >Vite</a
        >
      </li>
      <li>
        <a
          href="https://tailwindcss.com"
          target="_blank"
          rel="noopener noreferrer"
          class="font-medium text-slate-700 hover:text-indigo-600 dark:text-slate-300 dark:hover:text-indigo-400 underline-offset-4 hover:underline transition-colors"
          >Tailwind CSS</a
        >
      </li>
      {#if version}
        <li aria-hidden="true" class="text-slate-300 dark:text-slate-600 select-none">&middot;</li>
        <li><span class="font-mono">{version}</span></li>
      {/if}
    </ul>
  </div>
</footer>
