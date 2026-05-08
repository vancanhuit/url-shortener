<script lang="ts">
  import { createLink, isApiError, type Link } from "./api";
  import { EXPIRES_PRESETS, resolveExpiresAt } from "./expires";

  interface Props {
    onSuccess: (payload: { link: Link; created: boolean }) => void | Promise<void>;
    onFailure: (err: { message: string }) => void;
  }
  const { onSuccess, onFailure }: Props = $props();

  let targetUrl = $state("");
  let code = $state("");
  let expiresIn = $state("");
  let pending = $state(false);

  // Reusable Tailwind class string for inputs/selects so the focus
  // ring + colour scheme doesn't drift between fields. The Go-side
  // template used to pre-compute this; we just keep it as a constant.
  const inputClass =
    "mt-1 block w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 placeholder:text-slate-400 shadow-sm focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:placeholder:text-slate-500 dark:focus:border-indigo-400 dark:focus:ring-indigo-400/40 transition-colors";

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (pending) return;
    pending = true;
    try {
      const expiresAt = resolveExpiresAt(expiresIn);
      const result = await createLink({
        target_url: targetUrl.trim(),
        code: code.trim() || undefined,
        expires_at: expiresAt,
      });
      await onSuccess(result);
    } catch (err) {
      onFailure({ message: friendlyMessage(err) });
    } finally {
      pending = false;
    }
  }

  /**
   * Translate an `ApiError` into the user-facing message strings the
   * old Go HTML route produced. Anything outside the well-known set
   * falls back to the server's `message` field.
   */
  function friendlyMessage(err: unknown): string {
    if (!isApiError(err)) return "Request failed.";
    if (err.code === "code_taken") return "That code is already in use.";
    if (err.code === "internal_error") return "Something went wrong. Try again.";
    return err.message || "Request failed.";
  }
</script>

<div
  class="rounded-2xl bg-white shadow-sm ring-1 ring-slate-200 p-5 sm:p-6 dark:bg-slate-900 dark:ring-slate-800"
>
  <form onsubmit={handleSubmit} class="space-y-4">
    <label class="block">
      <span class="text-sm font-medium text-slate-700 dark:text-slate-300">URL</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input
        bind:value={targetUrl}
        name="target_url"
        type="url"
        required
        autofocus
        placeholder="https://example.com/some/long/path"
        class={inputClass}
      />
    </label>
    <details class="group text-sm">
      <summary
        class="cursor-pointer select-none text-slate-600 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 transition-colors flex items-center gap-1.5"
      >
        <svg
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
          class="h-3.5 w-3.5 transition-transform group-open:rotate-90"
        >
          <path
            fill-rule="evenodd"
            d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z"
            clip-rule="evenodd"
          />
        </svg>
        Custom code (optional)
      </summary>
      <input
        bind:value={code}
        name="code"
        type="text"
        pattern="[A-Za-z0-9]{'{4,64}'}"
        placeholder="mylink"
        class="{inputClass} font-mono"
      />
    </details>
    <label class="block text-sm">
      <span class="font-medium text-slate-700 dark:text-slate-300">Expires</span>
      <select bind:value={expiresIn} name="expires_in" class={inputClass}>
        {#each EXPIRES_PRESETS as preset (preset.value)}
          <option value={preset.value}>{preset.label}</option>
        {/each}
      </select>
    </label>
    <button
      type="submit"
      disabled={pending}
      class="inline-flex w-full sm:w-auto items-center justify-center rounded-lg bg-indigo-600 px-4 py-2.5 text-white font-medium shadow-sm hover:bg-indigo-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2 focus-visible:ring-offset-white dark:focus-visible:ring-offset-slate-900 active:bg-indigo-700 transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
    >
      {pending ? "Shortening…" : "Shorten"}
    </button>
  </form>
</div>
