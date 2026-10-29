import { readFileSync } from "node:fs";
import type { Page, Route } from "@playwright/test";

// Test-only stand-in for the M1 server. Bodies come from the shared CTR-1 fixtures.
const fixture = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/m1/${name}`, import.meta.url), "utf8"));

const catalogue = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/catalogue/${name}`, import.meta.url), "utf8"));

export const PASSWORD = "fixture-only-password";

// Item n (1-based) of the generated big library: "Generated 0300" is card #300.
export function generatedItem(n: number) {
  const base = catalogue("items-movies.json").items[0];
  const id = `gen-${String(n).padStart(4, "0")}`;
  return { ...base, id, title: `Generated ${String(n).padStart(4, "0")}`, sortTitle: `Generated ${String(n).padStart(4, "0")}`, primaryImageUrl: null, userState: null, available: true, matched: null };
}

// Season 1 of the fixture show, derived from its first episode (the catalogue fixtures have no season item).
export function season() {
  const ep = catalogue("items-episodes.json").items[0];
  return { ...ep, id: "sea-synthetic-0001", kind: "season", parentId: "shw-synthetic-0001", title: "Season 1", sortTitle: "Season 1", index: 1, childCount: 2, durationMs: null, seasonIndex: null, available: null, userState: null };
}

// 1x1 transparent PNG for artwork requests.
const PIXEL = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");

export interface Call {
  method: string;
  path: string;
  query: string;
  csrf: string | undefined;
  key: string | undefined;
  body: unknown;
}

type Override = (route: Route, call: Call) => Promise<boolean> | boolean;

export class FakeAPI {
  calls: Call[] = [];
  signedIn = false;
  overrides: Override[] = [];
  private sessionCount = 0;
  // items requests answered and not yet delivered (the test for overlapping page loads).
  inFlight = 0;
  maxInFlight = 0;
  // generate(): a big library of generated items, 50 per page, total on the first page only.
  private generated: { libraryId: string; total: number; delayMs: number } | null = null;

  // Replaced bodies (or bare statuses) by pathname; see set().
  private fixed = new Map<string, unknown>();

  // catalogue: serve the CTR-2 catalogue; off, those routes are unregistered (404), as before LIB-2c.
  constructor(private readonly clip?: Buffer, private readonly catalogueOn = false) {}

  static async attach(page: Page, clip?: Buffer, opts: { catalogue?: boolean } = {}): Promise<FakeAPI> {
    const api = new FakeAPI(clip, opts.catalogue ?? false);
    await page.route("**/api/v1/**", (route) => api.handle(route));
    return api;
  }

  // A number answers that status with an error body; anything else is a 200 body.
  set(path: string, body: unknown) {
    this.fixed.set(path, body);
  }

  generate(total: number, libraryId = "lib-synthetic-movies", delayMs = 0) {
    this.generated = { libraryId, total, delayMs };
  }

  of(method: string, suffix: string): Call[] {
    return this.calls.filter((c) => c.method === method && c.path.endsWith(suffix));
  }

  private async handle(route: Route) {
    const req = route.request();
    const url = new URL(req.url());
    const call: Call = {
      method: req.method(),
      path: url.pathname,
      query: url.search,
      csrf: req.headers()["x-csrf-token"],
      key: req.headers()["idempotency-key"],
      body: req.postData() ? JSON.parse(req.postData()!) : undefined,
    };
    this.calls.push(call);
    for (const override of this.overrides) if (await override(route, call)) return;

    const json = (status: number, body: unknown) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    const auth = fixture("authenticated.json");
    const anon = fixture("anonymous.json");
    const error = (status: number, code: string) => json(status, { code, message: `fixture ${code}`, requestId: "req-fixture" });

    const isCatalogue = /^\/api\/v1\/(libraries|items|continue-watching|next-up)(\/|$)/.test(call.path);
    if (isCatalogue && !this.catalogueOn) return error(404, "not_found");

    if (call.method !== "GET" && call.method !== "HEAD") {
      const expected = this.signedIn ? auth.csrfToken : anon.csrfToken;
      if (call.csrf !== expected) return error(403, "csrf_failed");
    }
    if (call.path === "/api/v1/auth/session") return json(200, this.signedIn ? auth : anon);
    if (call.path === "/api/v1/auth/login") {
      const { username, password } = call.body as { username: string; password: string };
      if (username !== auth.viewer.username || password !== PASSWORD) return error(401, "unauthenticated");
      this.signedIn = true;
      return json(200, auth);
    }
    if (!this.signedIn) return error(401, "unauthenticated");
    if (call.path === "/api/v1/auth/logout") {
      this.signedIn = false;
      return route.fulfill({ status: 204 });
    }
    if (isCatalogue) {
      if (this.fixed.has(call.path)) {
        const body = this.fixed.get(call.path);
        return typeof body === "number" ? error(body, body === 404 ? "not_found" : "internal_error") : json(200, body);
      }
      const url = new URL(req.url());
      if (call.path === "/api/v1/libraries") return json(200, catalogue("libraries.json"));
      if (call.path === "/api/v1/continue-watching") return json(200, catalogue("continue-watching.json"));
      if (call.path === "/api/v1/next-up") return json(200, catalogue("next-up.json"));
      if (call.path.endsWith("/facets")) return json(200, catalogue("facets.json"));
      if (call.path.endsWith("/images/primary")) return route.fulfill({ status: 200, contentType: "image/png", body: PIXEL });
      if (call.path === "/api/v1/items") {
        const q = url.searchParams;
        const g = this.generated;
        if (g && q.get("libraryId") === g.libraryId) {
          this.maxInFlight = Math.max(this.maxInFlight, ++this.inFlight);
          try {
            await new Promise((r) => setTimeout(r, g.delayMs));
            const cursor = q.get("cursor");
            const start = cursor ? Number(cursor.slice(1)) : 0;
            const count = Math.min(50, g.total - start);
            const items = Array.from({ length: count }, (_, i) => generatedItem(start + i + 1));
            return await json(200, { items, nextCursor: start + count < g.total ? `c${start + count}` : null, total: cursor ? null : g.total });
          } finally {
            this.inFlight--;
          }
        }
        if (q.get("parentId") === "shw-synthetic-0001") return json(200, { items: [season()], nextCursor: null, total: null });
        if (q.has("parentId")) return json(200, catalogue("items-episodes.json"));
        // Later pages of the fixture listing are empty, with total null as the contract says.
        if (q.has("cursor")) return json(200, { items: [], nextCursor: null, total: null });
        return json(200, catalogue("items-movies.json"));
      }
      if (call.method === "PUT" && call.path.endsWith("/watched")) {
        const b = call.body as { watched: boolean; expectedRevision: number };
        const at = "2026-10-29T00:00:00Z";
        return json(200, { itemId: "x", revision: b.expectedRevision + 1, resume: null, watched: b.watched ? { value: true, origin: "manual", at } : null, playCount: 0, lastPlayedAt: null, hiddenFromContinueWatching: false });
      }
      const id = call.path.slice("/api/v1/items/".length);
      if (id === "sea-synthetic-0001") return json(200, { item: season(), originalTitle: null, overview: null, genres: [], communityRating: null, directors: [], studios: [], externalIds: [], versions: [] });
      for (const f of ["item-movie", "item-show", "item-episode-shared-file"]) {
        const detail = catalogue(`${f}.json`);
        if (detail.item.id === id) return json(200, detail);
      }
      return error(404, "not_found");
    }
    if (call.path === "/api/v1/movies") return json(200, fixture("movies.json"));
    if (call.path === "/api/v1/playback/sessions") {
      const b = call.body as { itemId?: unknown; versionId?: unknown } | undefined;
      const keys = b && typeof b === "object" ? Object.keys(b).sort().join() : "";
      if (keys !== "itemId,versionId" || typeof b!.itemId !== "string" || typeof b!.versionId !== "string") return error(400, "invalid_request");
      const id = `play-fixture-${++this.sessionCount}`;
      return json(201, { ...fixture("playback.json"), id, itemId: b!.itemId, versionId: b!.versionId, contentUrl: `/api/v1/playback/sessions/${id}/content?grant=g-${id}` });
    }
    if (call.path.endsWith("/heartbeat")) return route.fulfill({ status: 204 });
    if (call.path.endsWith("/content")) {
      if (!this.clip) return error(503, "source_unavailable");
      // Byte ranges as the real server sends them; browsers need them to seek.
      const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers()["range"] ?? "");
      if (!range) return route.fulfill({ status: 200, contentType: "video/mp4", headers: { "Accept-Ranges": "bytes" }, body: this.clip });
      const start = Number(range[1]);
      const end = range[2] ? Math.min(Number(range[2]), this.clip.length - 1) : this.clip.length - 1;
      return route.fulfill({
        status: 206,
        contentType: "video/mp4",
        headers: { "Accept-Ranges": "bytes", "Content-Range": `bytes ${start}-${end}/${this.clip.length}` },
        body: this.clip.subarray(start, end + 1),
      });
    }
    if (call.method === "DELETE") return route.fulfill({ status: 204 });
    return error(404, "not_found");
  }
}
