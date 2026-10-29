import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD, generatedItem } from "./fake-api";

const catalogue = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/catalogue/${name}`, import.meta.url), "utf8"));
const smokeClip = () => readFileSync(process.env.LISIMFLIX_M1_SMOKE!);

const MOVIES = "/libraries/lib-synthetic-movies";
const MOVIE = "/items/mov-synthetic-0001";
const MOVIE_PATH = "/api/v1/items/mov-synthetic-0001";

async function signIn(page: Page) {
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
}

async function open(page: Page, path = "/", setup?: (api: FakeAPI) => void, clip?: Buffer) {
  const api = await FakeAPI.attach(page, clip, { catalogue: true });
  setup?.(api);
  await page.goto(path);
  await signIn(page);
  return api;
}

const cards = (page: Page) => page.locator("a.card");
const json = (status: number, code: string, body: object = {}) => ({ status, contentType: "application/json", body: JSON.stringify({ code, message: `fixture ${code}`, requestId: "req-b", ...body }) });
const lib = (id: string, name: string, type: string, availability: string, scan: object = { state: "idle", filesSeen: 0, filesTotal: null }) => ({ id, name, type, scan, availability });
const libs = (...items: object[]) => ({ items, nextCursor: null });

test.describe("B1 expired session", () => {
  test("catalogue401ReturnsToLogin", async ({ page }) => {
    let expired = false;
    await open(page, MOVIES, (api) => api.overrides.push(async (route, call) => {
      if (!expired || !call.path.startsWith("/api/v1/items/")) return false;
      await route.fulfill(json(401, "unauthenticated"));
      return true;
    }));
    expired = true;
    await cards(page).first().click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    await expect(page.getByRole("alert")).toHaveText("Your sign-in has ended.");
  });

  test("signInWorksAfterA401", async ({ page }) => {
    let expired = false;
    const api = await open(page, MOVIES, (a) => a.overrides.push(async (route, call) => {
      if (!expired || !call.path.startsWith("/api/v1/items/")) return false;
      a.signedIn = false; // the server's session is gone
      expired = false;
      await route.fulfill(json(401, "unauthenticated"));
      return true;
    }));
    expired = true;
    await cards(page).first().click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    await signIn(page);
    await expect(page.getByRole("heading", { level: 1, name: "Synthetic Test Movie" })).toBeVisible();
    expect(api.of("POST", "/auth/login").at(-1)?.csrf).toMatch(/-2$/);
  });

  test("late401FromOldSessionIsIgnored", async ({ page }) => {
    // Home loads two rows at once: continue-watching fails 401 at once (session over), next-up answers 401 only after the viewer has signed in again.
    let early = false;
    let late = false;
    const api = await FakeAPI.attach(page, undefined, { catalogue: true });
    api.overrides.push(async (route, call) => {
      if (!early && call.path === "/api/v1/continue-watching") {
        early = true;
        api.signedIn = false;
        await route.fulfill(json(401, "unauthenticated"));
        return true;
      }
      if (!late && call.path === "/api/v1/next-up") {
        late = true;
        await new Promise((r) => setTimeout(r, 1500));
        await route.fulfill(json(401, "unauthenticated"));
        return true;
      }
      return false;
    });
    await page.goto("/");
    await signIn(page);
    await expect(page.getByRole("alert")).toHaveText("Your sign-in has ended.");
    await signIn(page);
    await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeAttached();
    await page.waitForTimeout(2000);
    expect(late).toBe(true);
    await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeAttached();
    expect(api.of("POST", "/auth/login")).toHaveLength(2);
  });

  test("player401ReturnsToLogin", async ({ page }) => {
    await open(page, MOVIE, (api) => api.overrides.push(async (route, call) => {
      if (call.method !== "POST" || call.path !== "/api/v1/playback/sessions") return false;
      await route.fulfill(json(401, "unauthenticated"));
      return true;
    }));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });
});

test.describe("B2-B3 player errors", () => {
  test("failedStateShowsDiagnosticId", async ({ page }) => {
    await open(page, MOVIE, (api) => api.overrides.push(async (route, call) => {
      if (call.method !== "POST" || call.path !== "/api/v1/playback/sessions") return false;
      await route.fulfill(json(500, "internal_error"));
      return true;
    }));
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("Diagnostic ID req-b");
  });

  test("episodeMediaErrorSaysEpisode", async ({ page }) => {
    await open(page, "/play/epi-synthetic-0002/ver-synthetic-0003", (api) => api.overrides.push(async (route, call) => {
      if (!call.path.endsWith("/content")) return false;
      await route.fulfill({ status: 200, contentType: "video/mp4", body: Buffer.from("not a video") });
      return true;
    }));
    await expect(page.getByRole("alert")).toHaveText("This episode couldn't be played.");
  });

  test("movieMediaErrorSaysMovie", async ({ page }) => {
    await open(page, "/play/mov-synthetic-0001/ver-synthetic-0001", (api) => api.overrides.push(async (route, call) => {
      if (!call.path.endsWith("/content")) return false;
      await route.fulfill({ status: 200, contentType: "video/mp4", body: Buffer.from("not a video") });
      return true;
    }));
    await expect(page.getByRole("alert")).toHaveText("This movie couldn't be played.");
  });
});

test.describe("B4 mark watched", () => {
  test("hiddenOnNotFound", async ({ page }) => {
    await open(page, MOVIE, (api) => api.overrides.push(async (route, call) => {
      if (call.method !== "PUT") return false;
      await route.fulfill(json(404, "not_found"));
      return true;
    }));
    await page.getByRole("button", { name: /Mark watched/ }).click();
    await expect(page.getByRole("button", { name: /Mark watched/ })).toHaveCount(0);
    await expect(page.getByText("Not found.")).toHaveCount(0);
  });

  test("disabledWhilePending", async ({ page }) => {
    const api = await open(page, MOVIE, (a) => a.overrides.push(async (_r, call) => {
      if (call.method === "PUT") await new Promise((r) => setTimeout(r, 600));
      return false;
    }));
    const button = page.getByRole("button", { name: /Mark watched/ });
    await button.click();
    await expect(button).toBeDisabled();
    await expect(button).toBeEnabled();
    expect(api.of("PUT", "/watched")).toHaveLength(1);
  });
});

test.describe("B5 swallowed errors", () => {
  test("facetsFailureOffersRetry", async ({ page }) => {
    let fail = true;
    await open(page, MOVIES, (api) => api.overrides.push(async (route, call) => {
      if (!fail || !call.path.endsWith("/facets")) return false;
      await route.fulfill(json(500, "internal_error"));
      return true;
    }));
    await expect(page.getByText("Couldn't load filters")).toBeVisible();
    fail = false;
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByText("Couldn't load filters")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /Genre/ })).toBeVisible();
  });

  test("upNextItemFailureOffersRetry", async ({ page }) => {
    let fail = true;
    await open(page, "/", (api) => api.overrides.push(async (route, call) => {
      if (!fail || call.path !== MOVIE_PATH) return false;
      await route.fulfill(json(500, "internal_error"));
      return true;
    }));
    const hero = page.getByRole("region", { name: "Up next" });
    await expect(hero.getByText("Couldn't load this item")).toBeVisible();
    fail = false;
    await hero.getByRole("button", { name: "Try again" }).click();
    await expect(hero.getByText("Couldn't load this item")).toHaveCount(0);
    await expect(hero.getByRole("button", { name: "Resume" })).toBeEnabled();
  });

  test("showSeasonFailureUsesErrorState", async ({ page }) => {
    let fail = true;
    await open(page, "/items/shw-synthetic-0001", (api) => api.overrides.push(async (route, call) => {
      if (!fail || call.path !== "/api/v1/items" || !call.query.includes("parentId")) return false;
      await route.fulfill(json(500, "internal_error"));
      return true;
    }));
    await expect(page.getByText("Diagnostic ID req-b")).toBeVisible();
    fail = false;
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByRole("combobox", { name: "Season" })).toBeVisible();
  });
});

test("B6 offlineEpisodeRowHasBadge", async ({ page }) => {
  const eps = catalogue("items-episodes.json");
  eps.items[1] = { ...eps.items[1], available: false };
  await open(page, "/items/shw-synthetic-0001", (api) => api.overrides.push(async (route, call) => {
    if (call.path !== "/api/v1/items" || !call.query.includes("parentId=sea-")) return false;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(eps) });
    return true;
  }));
  await expect(page.getByText("File offline")).toHaveCount(1);
});

test("B7 partialBannerFollowsTypeChip", async ({ page }) => {
  await open(page, "/");
  const banner = page.getByText("Some titles are offline");
  await expect(banner).toBeVisible(); // Movies is partial in the fixture
  await page.getByRole("button", { name: "Anime", exact: true }).click();
  await expect(banner).toHaveCount(0);
  await page.getByRole("button", { name: "Movies", exact: true }).click();
  await expect(banner).toBeVisible();
});

test("B8 emptyStaysUpWhileScanRefetches", async ({ page }) => {
  await page.clock.install();
  const anime = (seen: number) => libs(lib("lib-synthetic-anime", "Anime", "anime", "available", { state: "scanning", filesSeen: seen, filesTotal: 3410 }));
  const api = await open(page, "/libraries/lib-synthetic-anime", (a) => {
    a.generate(0, "lib-synthetic-anime");
    a.set("/api/v1/libraries", anime(100));
  });
  await expect(page.getByText("No anime yet")).toBeVisible();
  // Record whether the skeleton (or a missing Empty) is ever on screen during the refetch.
  await page.evaluate(() => {
    const w = window as unknown as { flicker: number };
    w.flicker = 0;
    new MutationObserver(() => { if (document.querySelector(".skeleton") || !document.querySelector(".empty")) w.flicker++; }).observe(document.body, { childList: true, subtree: true });
  });
  api.set("/api/v1/libraries", anime(200));
  let refetched = false;
  api.overrides.push(async (_r, call) => {
    if (call.path === "/api/v1/items") { refetched = true; await new Promise((r) => setTimeout(r, 800)); }
    return false;
  });
  await page.clock.runFor(5100);
  await expect.poll(() => refetched).toBe(true);
  await page.waitForTimeout(1200); // the refetch has answered (still empty)
  await expect(page.getByText("No anime yet")).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { flicker: number }).flicker)).toBe(0);
});

test("B9 backdropUrlIsQuoted", async ({ page }) => {
  const cw = catalogue("continue-watching.json");
  cw.items[0] = { ...cw.items[0], backdropUrl: "/api/v1/images/a(1).jpg" };
  await open(page, "/", (api) => api.set("/api/v1/continue-watching", cw));
  const hero = page.getByRole("region", { name: "Up next" });
  await expect(hero).toBeVisible();
  expect(await hero.evaluate((e) => getComputedStyle(e).backgroundImage)).toContain('a(1).jpg');
});

test("B10 slowProgressDoesNotPushStopPastLogout", async ({ page }) => {
  const api = await FakeAPI.attach(page, smokeClip(), { catalogue: true });
  let stopDone = 0;
  let logoutAt = 0;
  api.overrides.push(async (route, call) => {
    if (call.path.endsWith("/progress")) return true; // never answers
    if (call.method === "DELETE") {
      await new Promise((r) => setTimeout(r, 800));
      await route.fulfill({ status: 204 });
      stopDone = Date.now();
      return true;
    }
    if (call.path === "/api/v1/auth/logout") logoutAt = Date.now();
    return false;
  });
  await page.goto(MOVIE);
  await signIn(page);
  await page.getByRole("button", { name: "Play", exact: true }).click();
  await expect(page.locator("video")).toBeAttached();
  await expect.poll(() => page.locator("video").evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThan(0);
  await page.getByRole("button", { name: "Profile" }).click();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible({ timeout: 5_000 });
  expect(stopDone).toBeGreaterThan(0);
  expect(logoutAt).toBeGreaterThanOrEqual(stopDone);
});

test.describe("B11 focused card stays rendered", () => {
  test("focusSurvivesScrollOutAndArrowsStillWork", async ({ page }) => {
    await open(page, MOVIES, (api) => api.generate(5000));
    const first = page.getByRole("link", { name: /^Generated 0001/ });
    await first.focus();
    await page.evaluate(() => scrollTo(0, 30000));
    await expect.poll(() => page.locator("[data-index]").last().getAttribute("data-index").then(Number)).toBeGreaterThan(100); // the window moved
    expect(await page.evaluate(() => document.activeElement?.tagName)).toBe("A");
    await page.keyboard.press("ArrowRight");
    await expect(page.getByRole("link", { name: /^Generated 0002/ })).toBeFocused();
  });

  test("clickingElsewhereIsNotUndone", async ({ page }) => {
    await open(page, MOVIES, (api) => api.generate(5000));
    await page.getByRole("link", { name: /^Generated 0001/ }).focus();
    await page.evaluate(() => scrollTo(0, 30000));
    await expect.poll(() => page.locator("[data-index]").last().getAttribute("data-index").then(Number)).toBeGreaterThan(100);
    await page.evaluate(() => (document.activeElement as HTMLElement).blur()); // viewer clicks away
    await page.evaluate(() => scrollTo(0, 0));
    await expect(page.getByRole("link", { name: /^Generated 0001/ })).not.toBeFocused();
  });
});

test("B12 railClearsHeaderAndChips", async ({ page }) => {
  await page.setViewportSize({ width: 1100, height: 700 });
  await open(page, MOVIES);
  await expect(cards(page).first()).toBeVisible();
  const rail = (await page.getByRole("navigation", { name: "A–Z" }).boundingBox())!;
  const targets = [page.getByRole("button", { name: "Profile" }), ...(await page.getByRole("group", { name: "Filters" }).locator("> *").all())];
  for (const t of targets) {
    const b = (await t.boundingBox())!;
    const apart = b.x + b.width <= rail.x || b.y + b.height <= rail.y;
    expect(apart, `${await t.textContent()} overlaps the rail`).toBe(true);
  }
});

test.describe("B13 route focus is bounded", () => {
  const slowItem = (api: FakeAPI, ms: number) => api.overrides.push(async (_r, call) => {
    if (call.path === MOVIE_PATH) await new Promise((r) => setTimeout(r, ms));
    return false;
  });

  test("viewerInputCancelsFocusMove", async ({ page }) => {
    await open(page, MOVIES, (api) => slowItem(api, 800));
    await cards(page).first().click();
    await page.getByRole("button", { name: "Profile" }).focus();
    await page.keyboard.press("Shift+Tab");
    await expect(page.getByRole("heading", { level: 1, name: "Synthetic Test Movie" })).toBeVisible();
    await expect(page.getByRole("heading", { level: 1 })).not.toBeFocused();
  });

  test("lateH1AfterTimeoutDoesNotTakeFocus", async ({ page }) => {
    await open(page, MOVIES, (api) => slowItem(api, 2600));
    await cards(page).first().click();
    await expect(page.getByRole("heading", { level: 1, name: "Synthetic Test Movie" })).toBeVisible({ timeout: 6_000 });
    await expect(page.getByRole("heading", { level: 1 })).not.toBeFocused();
  });
});
