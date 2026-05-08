<script lang="ts">
  import RecentItem from "./RecentItem.svelte";
  import type { Link } from "./api";

  interface Props {
    items: Link[];
    nextCursor: number | null;
    onLoadMore: () => void | Promise<void>;
    onDeleted: (code: string) => void | Promise<void>;
  }
  const { items, nextCursor, onLoadMore, onDeleted }: Props = $props();
</script>

{#if items.length > 0}
  <ul class="space-y-2">
    {#each items as link (link.code)}
      <RecentItem {link} {onDeleted} />
    {/each}
  </ul>
  <div class="mt-4 text-center">
    {#if nextCursor !== null}
      <button
        type="button"
        onclick={onLoadMore}
        class="text-sm font-medium text-indigo-600 hover:text-indigo-800 hover:underline dark:text-indigo-400 dark:hover:text-indigo-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 rounded transition-colors"
      >
        Load more
      </button>
    {/if}
  </div>
{:else}
  <p
    class="rounded-xl bg-white px-4 py-6 text-center text-sm text-slate-500 ring-1 ring-slate-200 dark:bg-slate-900 dark:text-slate-400 dark:ring-slate-800"
  >
    No links yet.
  </p>
{/if}
