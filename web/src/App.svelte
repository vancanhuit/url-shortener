<script lang="ts">
  import { onMount } from "svelte";
  import { getVersion, type Link } from "./lib/api";
  import { linksStore } from "./lib/links.svelte";
  import LinkForm from "./lib/LinkForm.svelte";
  import LinkResult from "./lib/LinkResult.svelte";
  import LinkError from "./lib/LinkError.svelte";
  import RecentList from "./lib/RecentList.svelte";
  import ThemeToggle from "./lib/ThemeToggle.svelte";
  import IconLink from "./lib/icons/IconLink.svelte";
  import Footer from "./lib/Footer.svelte";

  let version: string | null = $state(null);

  let result: { link: Link; created: boolean } | null = $state(null);
  let error: { message: string } | null = $state(null);

  onMount(async () => {
    await linksStore.loadFirstPage();
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
    await linksStore.loadFirstPage();
  }

  function onError(err: { message: string }): void {
    error = err;
    result = null;
  }
</script>

<div class="flex-1 w-full mx-auto max-w-2xl px-4 sm:px-6 py-8 sm:py-12">
  <header class="mb-8 sm:mb-10 flex items-start gap-3">
    <span
      aria-hidden="true"
      class="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-500 to-violet-600 text-white shadow-sm ring-1 ring-indigo-500/20 dark:ring-indigo-400/30"
    >
      <IconLink />
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
      <RecentList />
    </section>
  </main>
</div>

<Footer {version} />
