import { createElement, useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";

export type Route =
  | { name: "home" }
  | { name: "library"; id: string }
  | { name: "item"; id: string }
  | { name: "play"; itemId: string; versionId: string; startMs: number | null }
  | { name: "notFound" };

export function parseRoute(pathname: string, search: string): Route {
  const parts = pathname.split("/").filter(Boolean).map((p) => {
    try { return decodeURIComponent(p); } catch { return null; }
  });
  if (parts.includes(null)) return { name: "notFound" };
  const [a, b, c] = parts as string[];
  if (parts.length === 0) return { name: "home" };
  if (parts.length === 2 && a === "libraries") return { name: "library", id: b };
  if (parts.length === 2 && a === "items") return { name: "item", id: b };
  if (parts.length === 3 && a === "play") {
    const t = new URLSearchParams(search).get("t");
    const ms = t !== null && /^\d+$/.test(t) ? Number(t) : null;
    return { name: "play", itemId: b, versionId: c, startMs: ms };
  }
  return { name: "notFound" };
}

// The first entry needs a key too (decision 12: caches are scoped per history entry).
// `depth` counts in-app entries, so Back knows whether history.back() stays in the app.
if (!history.state?.key) history.replaceState({ ...history.state, key: Math.random().toString(36).slice(2), depth: 0 }, "");

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((l) => l());
window.addEventListener("popstate", notify);

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// The URL string is the snapshot, so React compares it by value.
const snapshot = () => location.pathname + location.search;

export function useRoute(): Route {
  const url = useSyncExternalStore(subscribe, snapshot);
  const q = url.indexOf("?");
  return parseRoute(q < 0 ? url : url.slice(0, q), q < 0 ? "" : url.slice(q));
}

// The query string, for pages whose state lives in the URL (library filters and sort).
export function useSearch(): string {
  return useSyncExternalStore(subscribe, () => location.search);
}

// Each entry gets a random key, which the page cache (decision 12) uses to restore Back.
export function navigate(path: string, opts: { replace?: boolean } = {}) {
  const depth: number = history.state?.depth ?? 0;
  const state = { key: Math.random().toString(36).slice(2), depth: opts.replace ? depth : depth + 1 };
  if (opts.replace) history.replaceState(state, "", path);
  else history.pushState(state, "", path);
  notify();
}

export function Link({ href, onClick, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  return createElement("a", {
    ...rest,
    href,
    onClick: (e: MouseEvent<HTMLAnchorElement>) => {
      onClick?.(e);
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || rest.target === "_blank") return;
      e.preventDefault();
      navigate(href);
    },
  });
}
