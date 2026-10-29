import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD } from "./fake-api";

const catalogue = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/catalogue/${name}`, import.meta.url), "utf8"));

function smokeClip(): Buffer {
  const path = process.env.LISIMFLIX_M1_SMOKE;
  if (!path) throw new Error("Set LISIMFLIX_M1_SMOKE to a short H.264/AAC MP4 (see the UI-1 plan).");
  return readFileSync(path);
}

const MOVIE = "/items/mov-synthetic-0001";
const MOVIE_PATH = "/api/v1/items/mov-synthetic-0001";
const V1 = "ver-synthetic-0001";
const V2 = "ver-synthetic-0002";

// The fixture movie with both versions playable and the given resume point (on a version, default v1).
function movie(opts: { resumeOn?: string; positionMs?: number; v2Available?: boolean } = {}) {
  const d = catalogue("item-movie.json");
  if (opts.v2Available) d.versions[1].available = true;
  if (opts.resumeOn) {
    const v = d.versions.find((x: { id: string }) => x.id === opts.resumeOn);
    d.item.userState = { ...d.item.userState, revision: 1, resume: { positionMs: opts.positionMs ?? 2000, durationMs: v.durationMs, versionId: v.id, sourceRevision: v.sourceRevision, updatedAt: "2026-10-27T08:15:00Z" } };
  }
  return d;
}

async function open(page: Page, path: string, setup?: (api: FakeAPI) => void) {
  const api = await FakeAPI.attach(page, smokeClip(), { catalogue: true });
  setup?.(api);
  await page.goto(path);
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  return api;
}

const time = (page: Page) => page.locator("video").evaluate((v: HTMLVideoElement) => v.currentTime);
const fail = (api: FakeAPI, status: number, code: string, times = Infinity) => {
  let left = times;
  api.overrides.push(async (route, call) => {
    if (call.method !== "POST" || call.path !== "/api/v1/playback/sessions" || left-- <= 0) return false;
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ code, message: `fixture ${code}`, requestId: "r" }) });
    return true;
  });
};

test.describe("play, resume and progress", () => {
  test("playSendsItemAndVersion", async ({ page }) => {
    const api = await open(page, MOVIE, (a) => a.set(MOVIE_PATH, movie({ v2Available: true })));
    await page.getByRole("combobox", { name: "Version" }).selectOption(V2);
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.locator("video")).toBeAttached();
    expect(new URL(page.url()).pathname).toBe(`/play/mov-synthetic-0001/${V2}`);
    expect(api.of("POST", "/playback/sessions")[0].body).toEqual({ itemId: "mov-synthetic-0001", versionId: V2 });
  });

  test("resumeStartsAtPosition", async ({ page }) => {
    await open(page, MOVIE, (a) => a.set(MOVIE_PATH, movie({ resumeOn: V1 })));
    await expect(page.getByRole("button", { name: "Play from start" })).toBeVisible();
    await page.getByRole("button", { name: "Resume from 0:02" }).click();
    expect(page.url()).toContain("?t=2000");
    await expect.poll(() => time(page), { timeout: 3_000 }).toBeGreaterThanOrEqual(2);
  });

  test("otherVersionNeedsConfirm", async ({ page }) => {
    await open(page, MOVIE, (a) => a.set(MOVIE_PATH, movie({ resumeOn: V2, v2Available: true })));
    const button = page.getByRole("button", { name: "Resume from 0:02" });
    await button.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Your resume point is from another version. Resume at 0:02 anyway?");
    await expect(dialog.getByRole("button", { name: "Resume anyway" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(button).toBeFocused();
    await button.click();
    await page.getByRole("dialog").getByRole("button", { name: "Play from start" }).click();
    await expect.poll(() => page.locator("video").evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThan(0);
    expect(page.url()).not.toContain("?t=");
    expect(await time(page)).toBeLessThan(1);
  });

  test("resumeAnywayStartsAtPosition", async ({ page }) => {
    await open(page, MOVIE, (a) => a.set(MOVIE_PATH, movie({ resumeOn: V2, v2Available: true })));
    await page.getByRole("button", { name: "Resume from 0:02" }).click();
    await page.getByRole("button", { name: "Resume anyway" }).click();
    await expect.poll(() => time(page), { timeout: 3_000 }).toBeGreaterThanOrEqual(2);
  });

  test("progressTickPauseSeekStop", async ({ page }) => {
    await page.clock.install();
    const api = await open(page, MOVIE);
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect.poll(() => time(page)).toBeGreaterThan(0.3);
    await page.clock.runFor(10_000);
    await expect.poll(() => api.of("POST", "/progress").length).toBe(1);
    await page.locator("video").evaluate((v: HTMLVideoElement) => v.pause());
    await expect.poll(() => api.of("POST", "/progress").length).toBe(2);
    await page.locator("video").evaluate((v: HTMLVideoElement) => { v.currentTime = 1; });
    await expect.poll(() => api.of("POST", "/progress").length).toBe(3);
    await page.getByRole("button", { name: "← Back" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Test Movie");
    await expect.poll(() => api.of("DELETE", "/playback/sessions/play-fixture-1").length).toBe(1);

    const events = api.of("POST", "/progress").map((c) => c.body as { sequence: number; kind: string; positionMs: number; fromPositionMs: number | null });
    expect(events.map((e) => e.kind)).toEqual(["tick", "pause", "seek", "stop"]);
    expect(events.map((e) => e.sequence)).toEqual([1, 2, 3, 4]);
    expect(events[0].positionMs).toBeGreaterThan(300);
    expect(events[2].positionMs).toBe(1000);
    expect(events[2].fromPositionMs).toBe(events[1].positionMs);
    expect(events[0].fromPositionMs).toBeNull();
    expect(events[3].positionMs).toBe(1000);
    const order = api.calls.map((c) => `${c.method} ${c.path}`);
    expect(order.lastIndexOf("POST /api/v1/playback/sessions/play-fixture-1/progress")).toBeLessThan(order.indexOf("DELETE /api/v1/playback/sessions/play-fixture-1"));
  });

  test("progress404IsSilent", async ({ page }) => {
    const api = await open(page, MOVIE, (a) =>
      a.overrides.push(async (route, call) => {
        if (!call.path.endsWith("/progress")) return false;
        await route.fulfill({ status: 404, contentType: "application/json", body: '{"code":"not_found","message":"fixture not_found","requestId":"r"}' });
        return true;
      }));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect.poll(() => time(page)).toBeGreaterThan(0.3);
    await page.locator("video").evaluate((v: HTMLVideoElement) => v.pause());
    await expect.poll(() => api.of("POST", "/progress").length).toBe(1);
    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect(page.locator("video")).toBeAttached();
    const at = await time(page);
    await page.locator("video").evaluate((v: HTMLVideoElement) => v.play());
    await expect.poll(() => time(page)).toBeGreaterThan(at);
  });

  test("progress409EndsPlayback", async ({ page }) => {
    await open(page, MOVIE, (a) =>
      a.overrides.push(async (route, call) => {
        if (!call.path.endsWith("/progress")) return false;
        await route.fulfill({ status: 409, contentType: "application/json", body: '{"code":"source_changed","message":"fixture source_changed","requestId":"r"}' });
        return true;
      }));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect.poll(() => time(page)).toBeGreaterThan(0.3);
    await page.locator("video").evaluate((v: HTMLVideoElement) => v.pause());
    await expect(page.getByRole("alert")).toHaveText("This file changed. Press Play to start again.");
    await expect(page.locator("video")).toHaveCount(0);
  });

  test("sourceUnavailableOnStart", async ({ page }) => {
    await open(page, MOVIE, (a) => fail(a, 503, "source_unavailable"));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.getByRole("heading", { name: "File offline" })).toBeVisible();
    await expect(page.getByText("The drive holding this movie isn't connected. Your progress is kept. Play works again once it's back.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Play unavailable" })).toBeDisabled();
    expect(new URL(page.url()).pathname).toBe(MOVIE);
  });

  test("sourceChangedRefetchesDetail", async ({ page }) => {
    const api = await open(page, MOVIE, (a) => fail(a, 409, "source_changed"));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.getByRole("alert")).toHaveText("This file changed. Press Play to start again.");
    expect(api.of("GET", MOVIE_PATH)).toHaveLength(2);
    expect(new URL(page.url()).pathname).toBe(MOVIE);
  });

  test("capacityUnavailableOffersTryAgain", async ({ page }) => {
    await open(page, MOVIE, (a) => fail(a, 503, "capacity_unavailable", 1));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("fixture capacity_unavailable");
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(page.locator("video")).toBeAttached();
  });

  test("offlineVersionNoSession", async ({ page }) => {
    const api = await open(page, MOVIE);
    await page.getByRole("combobox", { name: "Version" }).selectOption(V2);
    await expect(page.getByRole("heading", { name: "File offline" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Play unavailable" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Play", exact: true })).toHaveCount(0);
    expect(api.of("POST", "/playback/sessions")).toHaveLength(0);
  });

  test("backFromPlayerStopsSession", async ({ page }) => {
    const api = await open(page, MOVIE);
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.locator("video")).toBeAttached();
    await page.getByRole("button", { name: "← Back" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Test Movie");
    await expect(page.locator("video")).toHaveCount(0);
    await expect.poll(() => api.of("DELETE", "/playback/sessions/play-fixture-1").length).toBe(1);
  });

  test("episodeRowPlayResumes", async ({ page }) => {
    const base = movie({ resumeOn: V1 });
    const detail = { ...base, item: { ...base.item, id: "epi-synthetic-0001", kind: "episode" } };
    await open(page, "/items/shw-synthetic-0001", (a) => a.set("/api/v1/items/epi-synthetic-0001", detail));
    await page.getByRole("listitem").filter({ hasText: "Synthetic Episode One" }).getByRole("button", { name: "Play" }).click();
    await expect(page).toHaveURL(/\/play\/epi-synthetic-0001\/ver-synthetic-0001\?t=2000$/);
    await expect.poll(() => time(page), { timeout: 3_000 }).toBeGreaterThanOrEqual(2);
  });

  test("backToHomeRefetchesResumeData", async ({ page }) => {
    const api = await open(page, "/");
    const cw = () => api.of("GET", "/api/v1/continue-watching").length;
    await expect.poll(cw).toBe(1);
    await page.getByRole("button", { name: "Resume" }).click();
    await expect(page.locator("video")).toBeAttached();
    await page.getByRole("button", { name: "← Back" }).click();
    await expect(page.locator(".upnext")).toBeVisible();
    await expect.poll(cw).toBe(2); // not the pre-playback rows
  });

  test("logoutDuringPlaybackStopsFirst", async ({ page }) => {
    const api = await open(page, MOVIE);
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.locator("video")).toBeAttached();
    await page.getByRole("button", { name: "Profile" }).click();
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    const order = api.calls.map((c) => `${c.method} ${c.path}`);
    const stop = order.indexOf("DELETE /api/v1/playback/sessions/play-fixture-1");
    const logout = order.indexOf("POST /api/v1/auth/logout");
    expect(stop).toBeGreaterThan(-1);
    expect(stop).toBeLessThan(logout);
    expect(order.indexOf("GET /api/v1/auth/session", logout)).toBeGreaterThan(logout);
    expect(api.of("DELETE", "/playback/sessions/play-fixture-1")).toHaveLength(1);
  });

  test("pageCloseSendsStopWithoutWaitingForProgress", async ({ page }) => {
    // Progress never answers, as when the tab is closing.
    const api = await open(page, MOVIE, (a) => a.overrides.push(async (_route, call) => call.path.endsWith("/progress")));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect.poll(() => time(page)).toBeGreaterThan(0.3);
    await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent("pagehide")));
    // Well under the 1.5 s the in-app stop waits for a report.
    await expect.poll(() => api.of("DELETE", "/playback/sessions/play-fixture-1").length, { timeout: 700 }).toBe(1);
    expect(api.of("POST", "/progress").map((c) => (c.body as { kind: string }).kind)).toEqual(["stop"]);
  });

  test("leavingPlayerDoesNotDuplicateDetailInHistory", async ({ page }) => {
    const api = await open(page, "/libraries/lib-synthetic-movies", () => undefined);
    await page.getByRole("link", { name: /Synthetic Test Movie/ }).first().click();
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.locator("video")).toBeAttached();
    await page.locator("video").evaluate((v: HTMLVideoElement) => { v.currentTime = v.duration - 0.3; });
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Test Movie", { timeout: 5_000 });
    expect(new URL(page.url()).pathname).toBe(MOVIE);
    await page.getByRole("button", { name: "← Back" }).click();
    expect(new URL(page.url()).pathname).toBe("/libraries/lib-synthetic-movies");
    expect(api.of("DELETE", "/playback/sessions/play-fixture-1")).toHaveLength(1);
  });
});
