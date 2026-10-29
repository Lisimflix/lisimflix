// In-memory, per page load. Cleared on sign-out so one profile's rows never reach the next.
const store = new Map<string, Promise<unknown>>();

export function cached<T>(key: string, load: () => Promise<T>): Promise<T> {
  let hit = store.get(key) as Promise<T> | undefined;
  if (!hit) {
    hit = load();
    store.set(key, hit);
    hit.catch(() => store.delete(key));
  }
  return hit;
}

export function clearCache() {
  store.clear();
}
