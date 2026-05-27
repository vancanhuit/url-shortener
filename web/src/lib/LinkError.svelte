<script lang="ts">
  import IconAlert from "./icons/IconAlert.svelte";

  interface Props {
    message: string;
  }
  const { message }: Props = $props();
</script>

<div
  class="rounded-xl bg-rose-50 p-4 ring-1 ring-rose-200 dark:bg-rose-500/10 dark:ring-rose-500/30 flex items-start gap-2"
>
  <IconAlert />
  <p class="text-sm font-medium text-rose-700 dark:text-rose-300">{message}</p>
</div>
