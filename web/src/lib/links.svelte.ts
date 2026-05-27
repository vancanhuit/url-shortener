import { listLinks, type Link } from "./api";

function createLinksStore() {
  let items = $state<Link[]>([]);
  let nextCursor = $state<number | null>(null);

  return {
    get items() {
      return items;
    },
    get nextCursor() {
      return nextCursor;
    },

    async loadFirstPage(): Promise<void> {
      try {
        const page = await listLinks({});
        items = page.items ?? [];
        nextCursor = page.next_cursor ?? null;
      } catch (e) {
        console.warn("recent list fetch failed", e);
        items = [];
        nextCursor = null;
      }
    },

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
  };
}

export const linksStore = createLinksStore();
