// In-memory, per page load, scoped per history entry (decision 12): a fresh navigation refetches, Back reuses. Cleared on sign-out so one profile's rows never reach the next.
const store = new Map<string, Promise<unknown>>();

export function cached<T>(key: string, load: () => Promise<T>): Promise<T> {
  key = `${history.state?.key}:${key}`;
  let hit = store.get(key) as Promise<T> | undefined;
  if (!hit) {
    hit = load();
    store.set(key, hit);
    hit.catch(() => store.delete(key));
  }
  return hit;
}

// Mutable per-entry record for page state a refetch can't rebuild (loaded pages, scroll, focus); `entry` is the history key captured at mount.
const views = new Map<string, Record<string, unknown>>();

export function view<T>(entry: string | undefined, slot: string): Partial<T> | undefined {
  return views.get(`${entry}:${slot}`) as Partial<T> | undefined;
}

export function saveView<T>(entry: string | undefined, slot: string, patch: Partial<T>) {
  const k = `${entry}:${slot}`;
  views.set(k, { ...views.get(k), ...patch } as Record<string, unknown>);
}

// Drop fetched data but keep per-entry views, so Back after playback refetches while grid scroll/focus restore still works.
export function invalidate() {
  store.clear();
}

export function clearCache() {
  store.clear();
  views.clear();
}
