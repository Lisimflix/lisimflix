import { readFileSync } from "node:fs";
import type { Page, Route } from "@playwright/test";

// Test-only stand-in for the M1 server. Bodies come from the shared CTR-1 fixtures.
const fixture = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/m1/${name}`, import.meta.url), "utf8"));

export const PASSWORD = "fixture-only-password";

export interface Call {
  method: string;
  path: string;
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

  constructor(private readonly clip?: Buffer) {}

  static async attach(page: Page, clip?: Buffer): Promise<FakeAPI> {
    const api = new FakeAPI(clip);
    await page.route("**/api/v1/**", (route) => api.handle(route));
    return api;
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
