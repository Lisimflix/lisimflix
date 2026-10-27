import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD } from "./fake-api";

// Short owned clip for real decoding (see the plan); module proof only, never the M1 movie.
function smokeClip(): Buffer {
  const path = process.env.LISIMFLIX_M1_SMOKE;
  if (!path) throw new Error("Set LISIMFLIX_M1_SMOKE to a short H.264/AAC MP4 (see the UI-1 plan).");
  return readFileSync(path);
}

async function signIn(page: Page, password = PASSWORD) {
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

async function play(page: Page) {
  await page.getByRole("button", { name: "Play" }).click();
}

test.describe("viewer shell", () => {
  test("loginRequiresValidCredentials", async ({ page }) => {
    await FakeAPI.attach(page);
    await page.goto("/");
    await expect(page.getByLabel("Username")).toHaveAttribute("maxlength", "64");
    await signIn(page, "wrong");
    await expect(page.getByRole("alert")).toHaveText("fixture unauthenticated");
    await signIn(page);
    await expect(page.getByRole("heading", { name: "Synthetic Test Movie" })).toBeVisible();
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
    expect(stored).not.toContain(PASSWORD);
  });

  test("csrfRotatesAfterLogin", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    expect(api.of("POST", "/auth/login")[0].csrf).toBe("csrf-synthetic-anon-0001");
    expect(api.of("POST", "/playback/sessions")[0].csrf).toBe("csrf-synthetic-auth-0002");
  });

  test("startUsesOneIdempotencyKeyOnRetry", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    let failures = 1;
    api.overrides.push(async (route, call) => {
      if (call.path !== "/api/v1/playback/sessions" || failures-- <= 0) return false;
      await route.fulfill({ status: 500, contentType: "application/json", body: '{"code":"internal_error","message":"fixture internal_error","requestId":"r"}' });
      return true;
    });
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.getByRole("alert")).toHaveText("fixture internal_error");
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    const starts = api.of("POST", "/playback/sessions");
    expect(starts).toHaveLength(2);
    expect(starts[0].key).toMatch(/^[0-9a-f]{32}$/);
    expect(starts[1].key).toBe(starts[0].key);
  });

  test("deadSessionKeyStartsAgainWithFreshKey", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    let conflicts = 1;
    api.overrides.push(async (route, call) => {
      if (call.path !== "/api/v1/playback/sessions" || conflicts-- <= 0) return false;
      await route.fulfill({ status: 409, contentType: "application/json", body: '{"code":"idempotency_conflict","message":"fixture idempotency_conflict","requestId":"r"}' });
      return true;
    });
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.getByRole("alert")).toHaveText("fixture idempotency_conflict");
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    const starts = api.of("POST", "/playback/sessions");
    expect(starts[1].key).not.toBe(starts[0].key);
  });

  test("crossOriginMediaURLIsRejected", async ({ page }) => {
    const api = await FakeAPI.attach(page);
    api.overrides.push(async (route, call) => {
      if (call.path !== "/api/v1/playback/sessions") return false;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ id: "p", movieId: "m", sourceRevision: "r", contentUrl: "https://evil.example/a.mp4", route: "direct", heartbeatIntervalSeconds: 10, idleTimeoutSeconds: 60 }),
      });
      return true;
    });
    const external: string[] = [];
    page.on("request", (r) => { if (!r.url().startsWith("http://127.0.0.1:5173")) external.push(r.url()); });
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.getByRole("alert")).toHaveText("Unexpected server response.");
    await expect(page.locator("video")).toHaveCount(0);
    expect(external).toEqual([]);
  });

  test("serverErrorLeavesRetryAvailable", async ({ page }) => {
    const api = await FakeAPI.attach(page);
    api.overrides.push(async (route, call) => {
      if (call.path !== "/api/v1/movies" || api.of("GET", "/movies").length > 1) return false;
      await route.fulfill({ status: 502, contentType: "text/html", body: "<html>proxy error</html>" });
      return true;
    });
    await page.goto("/");
    await signIn(page);
    await expect(page.getByRole("alert")).toHaveText("Request failed (502).");
    await expect(page.locator("body")).not.toContainText("proxy error");
    await page.reload();
    await expect(page.getByRole("heading", { name: "Synthetic Test Movie" })).toBeVisible();
  });

  test("lateStartCannotReplaceCurrentMovie", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    let release!: () => void;
    const held = new Promise<void>((r) => (release = r));
    api.overrides.push(async (_route, call) => {
      if (call.path === "/api/v1/playback/sessions") await held;
      return false;
    });
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.getByRole("button", { name: "Preparing…" })).toBeDisabled();
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    // Sign in again so the held start succeeds late, after the view it belonged to is gone.
    await signIn(page);
    await expect(page.getByRole("button", { name: "Play" })).toBeEnabled();
    release();
    await expect.poll(() => api.of("DELETE", "/playback/sessions/play-fixture-1").length).toBe(1);
    await expect(page.locator("video")).toHaveCount(0);
    expect(api.of("GET", "/content")).toHaveLength(0);
  });
});

test.describe("playback lifecycle", () => {
  test("playbackAdvancesOnRealShortClip", async ({ page }) => {
    await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    const video = page.locator("video");
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 10_000 }).toBeGreaterThan(1);
    expect(await video.evaluate((v: HTMLVideoElement) => v.videoWidth)).toBe(1280);
  });

  test("autoplayRejectionShowsPlayControl", async ({ page }) => {
    await page.addInitScript(() => {
      const real = HTMLMediaElement.prototype.play;
      let first = true;
      HTMLMediaElement.prototype.play = function () {
        if (first) {
          first = false;
          return Promise.reject(new DOMException("blocked", "NotAllowedError"));
        }
        return real.call(this);
      };
    });
    await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    const resume = page.locator(".player").getByRole("button", { name: "Play" });
    await expect(resume).toBeVisible();
    await resume.click();
    await expect(resume).toHaveCount(0);
    await expect.poll(() => page.locator("video").evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(0);
  });

  test("heartbeatsContinueWithoutReplacingVideoSource", async ({ page }) => {
    await page.clock.install();
    const api = await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    const src = await page.locator("video").evaluate((v: HTMLVideoElement) => v.src);
    await page.clock.runFor(35_000);
    await expect.poll(() => api.of("POST", "/heartbeat").map((c) => (c.body as { sequence: number }).sequence)).toEqual([1, 2, 3]);
    expect(await page.locator("video").evaluate((v: HTMLVideoElement) => v.src)).toBe(src);
  });

  test("expiredSessionShowsPlayAgain", async ({ page }) => {
    await page.clock.install();
    const api = await FakeAPI.attach(page, smokeClip());
    api.overrides.push(async (route, call) => {
      if (!call.path.endsWith("/heartbeat")) return false;
      await route.fulfill({ status: 410, contentType: "application/json", body: '{"code":"playback_expired","message":"expired","requestId":"r"}' });
      return true;
    });
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    await page.clock.runFor(10_000);
    await expect(page.getByRole("alert")).toHaveText("Playback has ended. Press Play to start again.");
    await expect(page.locator("video")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Play" })).toBeEnabled();
  });

  test("endedStopsSession", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    const video = page.locator("video");
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThan(0);
    await video.evaluate((v: HTMLVideoElement) => { v.currentTime = v.duration - 0.3; });
    await expect(video).toHaveCount(0, { timeout: 5_000 });
    expect(api.of("DELETE", "/playback/sessions/play-fixture-1")).toHaveLength(1);
  });

  test("logoutStopsPlaybackBeforeSigningOut", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    const order = api.calls.map((c) => `${c.method} ${c.path}`);
    const stop = order.indexOf("DELETE /api/v1/playback/sessions/play-fixture-1");
    const logout = order.indexOf("POST /api/v1/auth/logout");
    expect(stop).toBeGreaterThan(-1);
    expect(stop).toBeLessThan(logout);
    expect(api.of("DELETE", "/playback/sessions/play-fixture-1")).toHaveLength(1);
  });

  test("logoutAndUnmountStopTimers", async ({ page }) => {
    await page.clock.install();
    const api = await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    await page.clock.runFor(10_000);
    await expect.poll(() => api.of("POST", "/heartbeat").length).toBe(1);
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    expect(api.of("DELETE", "/playback/sessions/play-fixture-1")).toHaveLength(1);
    await page.clock.runFor(30_000);
    expect(api.of("POST", "/heartbeat")).toHaveLength(1);
  });

  test("bfcacheRestoreShowsPlay", async ({ page }) => {
    await FakeAPI.attach(page, smokeClip());
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    await page.evaluate(() => {
      window.dispatchEvent(new PageTransitionEvent("pagehide", { persisted: true }));
      window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true }));
    });
    await expect(page.locator("video")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Play" })).toBeEnabled();
  });

  test("logoutDoesNotWaitForAHungStop", async ({ page }) => {
    const api = await FakeAPI.attach(page, smokeClip());
    // Never answer the stop request.
    api.overrides.push(async (_route, call) => call.method === "DELETE");
    await page.goto("/");
    await signIn(page);
    await play(page);
    await expect(page.locator("video")).toBeAttached();
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible({ timeout: 4_000 });
    expect(api.of("POST", "/auth/logout")).toHaveLength(1);
  });
});
