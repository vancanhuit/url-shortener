<script lang="ts">
  import { humanExpiry } from "./time";
  import type { Link } from "./api";
  import IconCheck from "./icons/IconCheck.svelte";

  interface Props {
    link: Link;
  }
  const { link }: Props = $props();

  let copied = $state(false);

  async function copyShortUrl(): Promise<void> {
    try {
      await navigator.clipboard.writeText(link.short_url);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      // navigator.clipboard can throw in non-secure contexts; fall
      // back to a manual selection so the user can still copy.
      const el = document.getElementById("short-url");
      if (el instanceof HTMLInputElement) {
        el.focus();
        el.select();
      }
    }
  }

  let expiry = $derived(humanExpiry(link.expires_at));
</script>

<div
  class="rounded-xl bg-emerald-50 p-4 sm:p-5 ring-1 ring-emerald-200 dark:bg-emerald-500/10 dark:ring-emerald-500/30"
>
  <div class="flex items-center gap-2">
    <IconCheck />
    <p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">Created.</p>
  </div>
  <div class="mt-3 flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
    <input
      id="short-url"
      readonly
      value={link.short_url}
      class="flex-1 min-w-0 rounded-lg border border-emerald-200 bg-white px-3 py-2 font-mono text-sm text-indigo-700 select-all focus:outline-none focus:ring-2 focus:ring-emerald-500 dark:border-emerald-500/30 dark:bg-slate-900 dark:text-indigo-300 dark:focus:ring-emerald-400/50"
    />
    <button
      type="button"
      onclick={copyShortUrl}
      class="inline-flex items-center justify-center rounded-lg bg-emerald-600 px-4 py-2 text-white font-medium shadow-sm hover:bg-emerald-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 focus-visible:ring-offset-2 focus-visible:ring-offset-emerald-50 dark:focus-visible:ring-offset-slate-900 active:bg-emerald-700 transition-colors"
    >
      {copied ? "Copied" : "Copy"}
    </button>
  </div>
  <p class="mt-3 truncate text-xs text-slate-500 dark:text-slate-400">
    &rarr; {link.target_url}
  </p>
  {#if expiry}
    <p class="mt-1 text-xs font-medium text-amber-700 dark:text-amber-400">
      Expires: {expiry}
    </p>
  {/if}
</div>
