<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { getLink, deleteLink, isApiError, type Link } from "./api";
  import { humanExpiry, plural } from "./time";

  interface Props {
    link: Link;
    onDeleted: (code: string) => void | Promise<void>;
  }
  const { link, onDeleted }: Props = $props();

  // Local copy so the row can refresh its click count + (eventually
  // soft-deleted) state without round-tripping through the parent.
  // The component is keyed by `link.code`, so we never need to react
  // to prop mutations: a delete unmounts the row, a fresh first-page
  // fetch swaps in a new instance. Hence we deliberately seed local
  // state from the prop once and ignore Svelte's locally-referenced
  // warning for this seed-only pattern.
  // svelte-ignore state_referenced_locally
  let current: Link = $state({ ...link });
  let deleting = $state(false);

  // Periodic poll for click-count + expiry updates. The Go-side
  // template used to do this with htmx; the Svelte translation just
  // re-fetches the same JSON shape on the same cadence.
  const POLL_INTERVAL_MS = 5_000;
  let timer: number | undefined;

  async function refresh(): Promise<void> {
    try {
      current = await getLink(current.code);
    } catch (err) {
      // 410 (link_expired / link_deleted) and 404 are terminal --
      // stop polling once we've seen them; the parent re-fetches
      // the recent list on its own cadence to drop the row.
      if (isApiError(err) && (err.status === 404 || err.status === 410)) {
        stopPolling();
      }
    }
  }

  function startPolling(): void {
    timer = window.setInterval(refresh, POLL_INTERVAL_MS);
  }

  function stopPolling(): void {
    if (timer !== undefined) {
      window.clearInterval(timer);
      timer = undefined;
    }
  }

  onMount(startPolling);
  onDestroy(stopPolling);

  async function handleDelete(): Promise<void> {
    if (deleting) return;
    if (!window.confirm(`Delete /${current.code}?`)) return;
    deleting = true;
    try {
      await deleteLink(current.code);
      stopPolling();
      await onDeleted(current.code);
    } catch (err) {
      console.warn("delete failed", err);
      deleting = false;
    }
  }

  let expiry = $derived(humanExpiry(current.expires_at));
</script>

<li
  class="rounded-xl bg-white px-3 py-2.5 sm:px-4 ring-1 ring-slate-200 hover:ring-slate-300 dark:bg-slate-900 dark:ring-slate-800 dark:hover:ring-slate-700 transition-colors flex flex-col sm:flex-row sm:items-center gap-1.5 sm:gap-3"
>
  <a
    href={current.short_url}
    target="_blank"
    rel="noopener"
    class="font-mono text-sm text-indigo-600 hover:underline shrink-0 dark:text-indigo-400"
    >{current.short_url}</a
  >
  <span
    class="truncate text-xs sm:text-sm text-slate-500 flex-1 min-w-0 dark:text-slate-400"
    >{current.target_url}</span
  >
  <span class="shrink-0 inline-flex flex-wrap items-center gap-1.5 text-xs">
    <span
      class="rounded-full bg-slate-100 px-2 py-0.5 font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300"
      title="{current.click_count} click{plural(current.click_count)}"
    >
      {current.click_count} click{plural(current.click_count)}
    </span>
    {#if expiry}
      <span
        class="rounded-full px-2 py-0.5 font-medium {expiry === 'expired'
          ? 'bg-rose-100 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300'
          : 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'}"
      >
        {expiry}
      </span>
    {/if}
    <button
      type="button"
      onclick={handleDelete}
      disabled={deleting}
      aria-label="Delete /{current.code}"
      class="rounded-full px-2 py-0.5 font-medium text-slate-500 hover:text-rose-600 hover:bg-rose-50 dark:text-slate-400 dark:hover:text-rose-300 dark:hover:bg-rose-500/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
    >
      {deleting ? "…" : "Delete"}
    </button>
  </span>
</li>
