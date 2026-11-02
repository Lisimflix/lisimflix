import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD } from "./fake-api";

const catalogue = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/catalogue/${name}`, import.meta.url), "utf8"));

async function open(page: Page, path: string, setup?: (api: FakeAPI) => void) {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  setup?.(api);
  await page.goto(path);
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  return api;
}

const MOVIE = "/items/mov-synthetic-0001";
const MOVIE_PATH = "/api/v1/items/mov-synthetic-0001";
const versionSelect = (page: Page) => page.getByRole("combobox", { name: "Version" });

test.describe("detail pages", () => {
  test("movieDetailFromFixture", async ({ page }) => {
    await open(page, MOVIE);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Test Movie");
    await expect(page.getByText("TMDB 6.4")).toBeVisible();
    await expect(page.getByText("2026 · 90 min · Drama · TMDB 6.4")).toBeVisible();
    await expect(page.getByText("Synthetic Director")).toBeVisible();
    await expect(page.getByText("Synthetic Studio")).toBeVisible();
    await expect(page.getByText("20 Oct 2026")).toBeVisible();
    await expect(page.getByText("A made-up film used only in contract fixtures.")).toBeVisible();
    for (const chip of ["1080p", "H.264", "LC Stereo", "Direct play"]) await expect(page.locator(".qchips").getByText(chip, { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Play" })).toBeVisible();
  });

  test("versionPickerOnlyWhenMany", async ({ page }) => {
    await open(page, MOVIE);
    await expect(versionSelect(page).locator("option")).toHaveText([
      "1080p · H.264 · 1.1 GB",
      "Director's Cut · 4K · HEVC · 22.4 GB (offline)",
    ]);
    await page.goto("/items/epi-synthetic-0002");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Episode Two");
    await expect(versionSelect(page)).toHaveCount(0);
  });

  test("viewerSeesNoFilename", async ({ page }) => {
    await open(page, MOVIE);
    await expect(versionSelect(page).locator("option").first()).not.toContainText(".mp4");
    await expect(page.locator("body")).not.toContainText("synthetic.movie");
    // admin fixture: the file name rides along with version 1
    await page.unroute("**/api/v1/**");
    const api = await FakeAPI.attach(page, undefined, { catalogue: true });
    api.signedIn = true;
    api.set(MOVIE_PATH, catalogue("item-movie-admin.json"));
    await page.goto(MOVIE);
    await expect(versionSelect(page).locator("option").first()).toContainText("(synthetic.movie.1080p.mp4)");
  });

  test("offlineVersionLabelledNotDefault", async ({ page }) => {
    await open(page, MOVIE);
    const options = versionSelect(page).locator("option");
    await expect(options.nth(1)).toHaveText(/\(offline\)$/);
    await expect(versionSelect(page)).toHaveValue("ver-synthetic-0001");
    await versionSelect(page).selectOption("ver-synthetic-0002");
    await expect(page.locator(".qchips").getByText("4K", { exact: true })).toBeVisible();
    await expect(page.locator(".qchips").getByText("HDR10", { exact: true })).toBeVisible();
  });

  test("showListsSeasonsThenEpisodes", async ({ page }) => {
    const api = await open(page, "/items/shw-synthetic-0001");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Show");
    await expect(page.getByRole("combobox", { name: "Season" }).locator("option")).toHaveText(["Season 1"]);
    const rows = page.getByRole("listitem").filter({ hasText: "Synthetic Episode" });
    await expect(rows).toHaveCount(2);
    await expect(rows.first()).toContainText("1");
    await expect(rows.nth(1)).toContainText("Synthetic Episode Two");
    await expect(rows.first().getByRole("button", { name: "Play" })).toBeVisible();
    const parents = api.calls.filter((c) => c.path === "/api/v1/items").map((c) => new URLSearchParams(c.query).get("parentId"));
    expect(parents).toEqual(["shw-synthetic-0001", "sea-synthetic-0001"]);
  });

  test("sharedFileEpisodeHasNoDuration", async ({ page }) => {
    await open(page, "/items/epi-synthetic-0002");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Episode Two");
    await expect(page.getByText("min", { exact: false })).toHaveCount(0);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
  });

  test("episodeBarNeedsDurationAndResume", async ({ page }) => {
    const [one, two] = catalogue("items-episodes.json").items;
    const resume = { versionId: "ver-synthetic-0003", sourceRevision: "r", positionMs: 600000, durationMs: 1200000, updatedAt: "2026-10-29T00:00:00Z" };
    const episodes = [
      { ...one, durationMs: null, userState: { ...one.userState, resume } }, // shared file: resume but no duration
      { ...two, durationMs: 1200000, userState: { ...two.userState, resume } },
    ];
    await open(page, "/items/shw-synthetic-0001", (api) => {
      api.overrides.push(async (route, call) => {
        if (call.path !== "/api/v1/items" || !call.query.includes("parentId=sea-synthetic-0001")) return false;
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: episodes, nextCursor: null, total: null }) });
        return true;
      });
    });
    const rows = page.getByRole("listitem").filter({ hasText: "Synthetic Episode" });
    await expect(rows).toHaveCount(2);
    await expect(rows.first().getByRole("progressbar")).toHaveCount(0);
    await expect(rows.nth(1).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
  });

  // Decision 9 on the show page's row Play: another version's resume point goes to the detail page (the resume case is in play.spec).
  test("episodePlayConfirmsOtherVersion", async ({ page }) => {
    const base = catalogue("item-movie.json");
    const v = base.versions[0];
    const resume = { positionMs: 5000, durationMs: v.durationMs, versionId: "ver-other", sourceRevision: v.sourceRevision, updatedAt: "2026-10-29T00:00:00Z" };
    const detail = { ...base, item: { ...base.item, id: "epi-synthetic-0001", kind: "episode", userState: { ...base.item.userState, resume } } };
    await open(page, "/items/shw-synthetic-0001", (api) => api.set("/api/v1/items/epi-synthetic-0001", detail));
    await page.getByRole("listitem").filter({ hasText: "Synthetic Episode One" }).getByRole("button", { name: "Play" }).click();
    await expect(page).toHaveURL(/\/items\/epi-synthetic-0001$/);
  });

  test("seasonLinksToShowWithSeason", async ({ page }) => {
    await open(page, "/items/sea-synthetic-0001");
    await page.getByRole("link", { name: /Synthetic Show/ }).click();
    await expect(page).toHaveURL(/\/items\/shw-synthetic-0001\?season=sea-synthetic-0001/);
    await expect(page.getByRole("combobox", { name: "Season" })).toHaveValue("sea-synthetic-0001");
  });

  test("markWatchedSendsRevision", async ({ page }) => {
    const api = await open(page, MOVIE);
    const toggle = page.getByRole("button", { name: "Mark watched" });
    await expect(toggle).toHaveAttribute("aria-pressed", "false");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-pressed", "true");
    const put = api.calls.find((c) => c.method === "PUT")!;
    expect(put.path).toBe(`${MOVIE_PATH}/watched`);
    expect(put.body).toEqual({ watched: true, expectedRevision: catalogue("item-movie.json").item.userState.revision });
    expect(put.csrf).toBeTruthy();
  });

  test("markWatchedConflictRefetches", async ({ page }) => {
    const api = await open(page, MOVIE, (a) => {
      a.overrides.push(async (route, call) => {
        if (call.method !== "PUT") return false;
        await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ code: "revision_conflict", message: "stale", requestId: "r" }) });
        return true;
      });
    });
    await page.getByRole("button", { name: "Mark watched" }).click();
    await expect(page.getByRole("status")).toHaveText("Updated elsewhere, try again");
    await expect.poll(() => api.calls.filter((c) => c.method === "GET" && c.path === MOVIE_PATH).length).toBe(2);
    await expect(page.getByRole("button", { name: "Mark watched" })).toHaveAttribute("aria-pressed", "false");
  });

  test("noVersionsNoPlay", async ({ page }) => {
    await open(page, MOVIE, (a) => a.set(MOVIE_PATH, { ...catalogue("item-movie.json"), versions: [] }));
    await expect(page.getByText("No file available for this item")).toBeVisible();
    await expect(page.getByRole("button", { name: "Play" })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Play" })).toHaveCount(0);
  });

  test("backGoesToLibraryWhenNoPreviousEntry", async ({ page }) => {
    await open(page, MOVIE);
    await page.getByRole("button", { name: "Back" }).click();
    await expect(page).toHaveURL(/\/libraries\/lib-synthetic-movies$/);
  });

  test("backRestoresGridScrollAndFocus", async ({ page }) => {
    const api = await open(page, "/libraries/lib-synthetic-movies", (a) => a.generate(5000));
    await expect(page.getByText("5000 titles")).toBeVisible();
    const target = page.getByRole("link", { name: /^Generated 0300/ });
    await expect(async () => {
      await page.evaluate(() => { const g = document.querySelector(".vgrid")!; scrollTo(0, g.getBoundingClientRect().top + scrollY + (g.clientHeight * 299) / 5000 - 100); });
      await expect(target).toBeVisible({ timeout: 300 });
    }).toPass({ timeout: 30_000 });
    await target.focus();
    const yBefore = await page.evaluate(() => scrollY);
    const boxBefore = (await target.boundingBox())!;
    await target.click();
    await expect(page).toHaveURL(/\/items\/gen-0300$/);
    const loads = api.of("GET", "/api/v1/items").length;
    await page.goBack();
    await expect(target).toBeFocused({ timeout: 5000 });
    const rowH = 180 * 1.5 + 52 + 16;
    expect(Math.abs((await page.evaluate(() => scrollY)) - yBefore)).toBeLessThanOrEqual(rowH);
    expect(Math.abs((await target.boundingBox())!.y - boxBefore.y)).toBeLessThanOrEqual(rowH);
    expect(yBefore).toBeGreaterThan(1000);
    expect(api.of("GET", "/api/v1/items").length).toBe(loads);
  });
});
