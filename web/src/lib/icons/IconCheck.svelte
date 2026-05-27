<script lang="ts">
  interface Props {
    class?: string;
  }
  const { class: className = "h-4 w-4 text-emerald-600 dark:text-emerald-400" }: Props = $props();
</script>

<svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" class={className}>
  <path
    fill-rule="evenodd"
    d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm3.707-9.293a1 1 0 0 0-1.414-1.414L9 10.586 7.707 9.293a1 1 0 0 0-1.414 1.414l2 2a1 1 0 0 0 1.414 0l4-4Z"
    clip-rule="evenodd"
  />
</svg>
