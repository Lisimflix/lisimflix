import type { components, paths } from "./schema";

export type AuthState = components["schemas"]["AuthState"];
export type Movie = components["schemas"]["Movie"];
export type PlaybackSession = components["schemas"]["PlaybackSession"];
export type LibraryList = components["schemas"]["LibraryList"];
export type Library = LibraryList["items"][number];
export type ItemPage = components["schemas"]["ItemPage"];
export type ItemSummary = ItemPage["items"][number];
export type ItemDetail = components["schemas"]["ItemDetail"];
export type LibraryFacets = components["schemas"]["LibraryFacets"];
export type UserItemState = components["schemas"]["UserItemState"];
export type Resume = components["schemas"]["Resume"];
export type MediaVersion = components["schemas"]["MediaVersion"];
export type ProgressEvent = components["schemas"]["ProgressEvent"];
export type ProgressResult = components["schemas"]["ProgressResult"];
// libraryId or parentId, plus paging, sort and filters (CTR-2 listItems).
export type ItemQuery = NonNullable<paths["/api/v1/items"]["get"]["parameters"]["query"]>;
type ErrorCode = components["schemas"]["APIError"]["code"];

// status 0 / "network_error" means no API response (offline, aborted or not JSON).
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode | "network_error",
    message: string,
    readonly requestId: string | null = null,
  ) {
    super(message);
  }
}

// Kept until LIB-2c retires M1App.
export type M1Client = Client;

export interface Client {
  session(signal?: AbortSignal): Promise<AuthState>;
  login(username: string, password: string, signal?: AbortSignal): Promise<AuthState>;
  logout(signal?: AbortSignal): Promise<void>;
  movies(signal?: AbortSignal): Promise<Movie[]>;
  libraries(signal?: AbortSignal): Promise<LibraryList>;
  items(q: ItemQuery, signal?: AbortSignal): Promise<ItemPage>;
  item(id: string, signal?: AbortSignal): Promise<ItemDetail>;
  facets(libraryId: string, signal?: AbortSignal): Promise<LibraryFacets>;
  continueWatching(cursor?: string, signal?: AbortSignal): Promise<ItemPage>;
  nextUp(cursor?: string, signal?: AbortSignal): Promise<ItemPage>;
  setWatched(id: string, watched: boolean, expectedRevision: number): Promise<UserItemState>;
  progress(sessionId: string, event: ProgressEvent): Promise<ProgressResult>;
  start(itemId: string, versionId: string, key: string, signal?: AbortSignal): Promise<PlaybackSession>;
  heartbeat(id: string, sequence: number, signal?: AbortSignal): Promise<void>;
  stop(id: string): Promise<void>;
}

// Random opaque key. crypto.randomUUID() needs a secure context, and M1 serves plain LAN HTTP.
export function newIdempotencyKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

// Same-origin only: relative paths, so a page can never be pointed at another host's API.
export function createClient(): Client {
  let csrfToken = "";

  async function call<T>(method: string, path: string, init: { body?: unknown; headers?: Record<string, string>; signal?: AbortSignal; keepalive?: boolean } = {}): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json", ...init.headers };
    if (method !== "GET") headers["X-CSRF-Token"] = csrfToken;
    if (init.body !== undefined) headers["Content-Type"] = "application/json";
    let res: Response;
    try {
      res = await fetch(path, {
        method,
        headers,
        body: init.body === undefined ? undefined : JSON.stringify(init.body),
        credentials: "same-origin",
        signal: init.signal,
        keepalive: init.keepalive,
      });
    } catch (err) {
      if (init.signal?.aborted) throw err;
      throw new ApiError(0, "network_error", "Can't reach the server.");
    }
    if (res.status === 204) return undefined as T;
    const data = await res.json().catch(() => undefined);
    if (!res.ok) {
      const code = typeof data?.code === "string" ? (data.code as ErrorCode) : "network_error";
      const message = typeof data?.message === "string" ? data.message : `Request failed (${res.status}).`;
      const requestId = typeof data?.requestId === "string" ? data.requestId : null;
      throw new ApiError(res.status, code, message, requestId);
    }
    if (data === undefined) throw new ApiError(res.status, "network_error", "Unexpected server response.");
    return data as T;
  }

  const page = (path: string, cursor: string | undefined, signal: AbortSignal | undefined) =>
    call<ItemPage>("GET", cursor === undefined ? path : `${path}?${new URLSearchParams({ cursor })}`, { signal });

  async function auth(promise: Promise<AuthState>): Promise<AuthState> {
    const state = await promise;
    csrfToken = state.csrfToken;
    return state;
  }

  return {
    session: (signal) => auth(call("GET", "/api/v1/auth/session", { signal })),
    login: (username, password, signal) =>
      auth(call("POST", "/api/v1/auth/login", { body: { username, password }, signal })),
    logout: (signal) => call("POST", "/api/v1/auth/logout", { signal }),
    movies: async (signal) => (await call<components["schemas"]["MovieList"]>("GET", "/api/v1/movies", { signal })).items,
    libraries: (signal) => call("GET", "/api/v1/libraries", { signal }),
    items: (q, signal) => {
      const params = new URLSearchParams();
      for (const [k, v] of Object.entries(q)) if (v !== undefined) params.set(k, String(v));
      return call("GET", `/api/v1/items?${params}`, { signal });
    },
    item: (id, signal) => call("GET", `/api/v1/items/${encodeURIComponent(id)}`, { signal }),
    facets: (libraryId, signal) => call("GET", `/api/v1/libraries/${encodeURIComponent(libraryId)}/facets`, { signal }),
    continueWatching: (cursor, signal) => page("/api/v1/continue-watching", cursor, signal),
    nextUp: (cursor, signal) => page("/api/v1/next-up", cursor, signal),
    // Planned: DATA-1 (detail page watched toggle, Task 6).
    setWatched: (id, watched, expectedRevision) =>
      call("PUT", `/api/v1/items/${encodeURIComponent(id)}/watched`, { body: { watched, expectedRevision } }),
    // Planned: DATA-1 (player progress reports, Task 8).
    progress: (sessionId, event) =>
      call("POST", `/api/v1/playback/sessions/${encodeURIComponent(sessionId)}/progress`, { body: event, keepalive: true }),
    start: async (itemId, versionId, key, signal) => {
      const session = await call<PlaybackSession>("POST", "/api/v1/playback/sessions", {
        body: { itemId, versionId },
        headers: { "Idempotency-Key": key },
        signal,
      });
      // The contract makes contentUrl same-origin and relative; never point the player anywhere else.
      if (!session.contentUrl.startsWith("/api/v1/playback/sessions/")) {
        throw new ApiError(502, "internal_error", "Unexpected server response.");
      }
      return session;
    },
    heartbeat: (id, sequence, signal) =>
      call("POST", `/api/v1/playback/sessions/${encodeURIComponent(id)}/heartbeat`, { body: { sequence }, signal }),
    // keepalive lets the stop survive page close; the server's idle expiry is the backstop.
    stop: (id) => call("DELETE", `/api/v1/playback/sessions/${encodeURIComponent(id)}`, { keepalive: true }),
  };
}
