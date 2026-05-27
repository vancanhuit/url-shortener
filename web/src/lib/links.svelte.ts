import { listLinks, type Link } from "./api";

const POLL_INTERVAL_MS = 5_000;

function createLinksStore() {
  let items = $state<Link[]>([]);
  let nextCursor = $state<number | null>(null);
  let timer: ReturnType<typeof setInterval> | undefined;

  async function loadFirstPage(): Promise<void> {
    try {
      const page = await listLinks({});
      items = page.items ?? [];
      nextCursor = page.next_cursor ?? null;
    } catch (e) {
      console.warn("recent list fetch failed", e);
      items = [];
      nextCursor = null;
    }
  }

  return {
    get items() {
      return items;
    },
    get nextCursor() {
      return nextCursor;
    },

    loadFirstPage,

    async loadMore(): Promise<void> {
      if (nextCursor === null) return;
      try {
        const page = await listLinks({ before: nextCursor });
        items = [...items, ...(page.items ?? [])];
        nextCursor = page.next_cursor ?? null;
      } catch (e) {
        console.warn("recent list pagination failed", e);
      }
    },

    removeItem(code: string): void {
      items = items.filter((it) => it.code !== code);
    },

    startPolling(): void {
      if (timer !== undefined) return;
      timer = setInterval(() => {
        void loadFirstPage();
      }, POLL_INTERVAL_MS);
    },

    stopPolling(): void {
      if (timer !== undefined) {
        clearInterval(timer);
        timer = undefined;
      }
    },
  };
}

export const linksStore = createLinksStore();
