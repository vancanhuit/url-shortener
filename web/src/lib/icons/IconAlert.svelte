<script lang="ts">
  interface Props {
    class?: string;
  }
  const { class: className = "h-4 w-4 mt-0.5 shrink-0 text-rose-600 dark:text-rose-400" }: Props =
    $props();
</script>

<svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" class={className}>
  <path
    fill-rule="evenodd"
    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm-1-5a1 1 0 1 1 2 0 1 1 0 0 1-2 0Zm.25-7.25a.75.75 0 0 1 1.5 0v4a.75.75 0 0 1-1.5 0v-4Z"
    clip-rule="evenodd"
  />
</svg>
