<script lang="ts">
  interface Props {
    version?: string | null;
  }
  const { version = null }: Props = $props();
</script>

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
