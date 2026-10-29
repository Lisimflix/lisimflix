// A one-shot message from the player page to the detail page it returns to.
export type Notice = { itemId: string; versionId: string; kind: "unavailable" | "changed" | "error"; message: string };

let pending: Notice | null = null;

export const setNotice = (n: Notice) => { pending = n; };

export function takeNotice(itemId: string): Notice | null {
  const n = pending?.itemId === itemId ? pending : null;
  pending = null;
  return n;
}

export const clearNotice = () => { pending = null; };
