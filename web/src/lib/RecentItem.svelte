<script lang="ts">
  import { deleteLink, type Link } from "./api";
  import { humanExpiry, plural } from "./time";
  import IconTrash from "./icons/IconTrash.svelte";
  import { linksStore } from "./links.svelte";

  interface Props {
    link: Link;
  }
  const { link }: Props = $props();

  let deleting = $state(false);
  let confirmPending = $state(false);

  async function handleDelete(): Promise<void> {
    if (deleting) return;
    deleting = true;
    confirmPending = false;
    try {
      await deleteLink(link.code);
      linksStore.removeItem(link.code);
    } catch (err) {
      console.warn("delete failed", err);
      deleting = false;
    }
  }

  let expiry = $derived(humanExpiry(link.expires_at));
</script>

<li
  class="rounded-xl bg-white px-3 py-2.5 sm:px-4 ring-1 ring-slate-200 hover:ring-slate-300 dark:bg-slate-900 dark:ring-slate-800 dark:hover:ring-slate-700 transition-colors flex flex-col sm:flex-row sm:items-center gap-1.5 sm:gap-3"
>
  <a
    href={link.short_url}
    target="_blank"
    rel="noopener"
    class="font-mono text-sm text-indigo-600 hover:underline shrink-0 dark:text-indigo-400"
    >{link.short_url}</a
  >
  <span class="truncate text-xs sm:text-sm text-slate-500 flex-1 min-w-0 dark:text-slate-400"
    >{link.target_url}</span
  >
  <span class="shrink-0 inline-flex flex-wrap items-center gap-1.5 text-xs">
    <span
      class="rounded-full bg-slate-100 px-2 py-0.5 font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300"
      title="{link.click_count} click{plural(link.click_count)}"
    >
      {link.click_count} click{plural(link.click_count)}
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
    {#if confirmPending}
      <span class="inline-flex items-center gap-1.5">
        <button
          type="button"
          onclick={handleDelete}
          disabled={deleting}
          class="rounded-full px-2 py-0.5 text-xs font-medium text-white bg-rose-600 hover:bg-rose-700 dark:bg-rose-500 dark:hover:bg-rose-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {deleting ? "…" : "Confirm"}
        </button>
        <button
          type="button"
          onclick={() => (confirmPending = false)}
          disabled={deleting}
          class="rounded-full px-2 py-0.5 text-xs font-medium text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-slate-500 transition-colors"
        >
          Cancel
        </button>
      </span>
    {:else}
      <button
        type="button"
        onclick={() => (confirmPending = true)}
        disabled={deleting}
        aria-label="Delete /{link.code}"
        class="rounded-full p-1 text-slate-500 hover:text-rose-600 hover:bg-rose-50 dark:text-slate-400 dark:hover:text-rose-300 dark:hover:bg-rose-500/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <IconTrash />
      </button>
    {/if}
  </span>
</li>
