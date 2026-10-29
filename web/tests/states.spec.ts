import { expect, test, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { FakeAPI, PASSWORD, generatedItem } from "./fake-api";

const MOVIES = "/libraries/lib-synthetic-movies";
const ANIME = "/libraries/lib-synthetic-anime";

const lib = (id: string, name: string, type: string, scan: object, availability: string) => ({ id, name, type, scan, availability });
const idle = { state: "idle", filesSeen: 0, filesTotal: null };
const libraries = (...items: object[]) => ({ items, nextCursor: null });

async function open(page: Page, path: string, setup?: (api: FakeAPI) => void) {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  setup?.(api);
  await page.goto(path);
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  return api;
}

test.describe("states", () => {
  test("loadingSkeletonThenContent", async ({ page }) => {
    await open(page, MOVIES, (api) => {
      api.overrides.push(async (_r, call) => {
        if (call.path === "/api/v1/items") await new Promise((r) => setTimeout(r, 500));
        return false;
      });
    });
    const busy = page.locator('[aria-busy="true"]');
    await expect(busy).toBeVisible();
    const before = (await busy.boundingBox())!;
    const card = page.locator("a.card").first();
    await expect(card).toBeVisible();
    await expect(busy).toHaveCount(0);
    const after = (await card.boundingBox())!;
    expect(Math.abs(after.y - before.y)).toBeLessThan(after.height);
  });

  test("emptyWhileScanning", async ({ page }) => {
    await open(page, ANIME, (api) => api.generate(0, "lib-synthetic-anime"));
    await expect(page.getByText("No anime yet")).toBeVisible();
    await expect(page.getByText("This library's folder is being scanned for the first time. Titles appear here as they're found.")).toBeVisible();
    const bar = page.locator("progress");
    await expect(bar).toHaveAttribute("value", "1204");
    await expect(bar).toHaveAttribute("max", "3410");
    await expect(page.getByText("1,204 of 3,410 files")).toBeVisible();
  });

  test("emptyScanCountingTotal", async ({ page }) => {
    await open(page, ANIME, (api) => {
      api.generate(0, "lib-synthetic-anime");
      api.set("/api/v1/libraries", libraries(lib("lib-synthetic-anime", "Anime", "anime", { state: "scanning", filesSeen: 1204, filesTotal: null }, "available")));
    });
    await expect(page.getByText("1,204 files found")).toBeVisible();
    await expect(page.locator("progress")).not.toHaveAttribute("max", /.*/);
  });

  test("emptyIdleLibrary", async ({ page }) => {
    await open(page, MOVIES, (api) => {
      api.generate(0);
      api.set("/api/v1/libraries", libraries(lib("lib-synthetic-movies", "Movies", "movies", idle, "available")));
    });
    await expect(page.getByText("No movies yet")).toBeVisible();
    await expect(page.locator("progress")).toHaveCount(0);
    await expect(page.getByText("being scanned")).toHaveCount(0);
  });

  test("emptyShowsLibraryUsesShows", async ({ page }) => {
    await open(page, "/libraries/lib-tv", (api) => {
      api.generate(0, "lib-tv");
      api.set("/api/v1/libraries", libraries(lib("lib-tv", "TV", "tv", idle, "available")));
    });
    await expect(page.getByText("No shows yet")).toBeVisible();
  });

  test("offlineLibraryNotEmpty", async ({ page }) => {
    await open(page, MOVIES, (api) => {
      api.generate(0);
      api.set("/api/v1/libraries", libraries(lib("lib-synthetic-movies", "Movies", "movies", idle, "offline")));
    });
    await expect(page.getByText("This library's folder is offline")).toBeVisible();
    await expect(page.getByText("Titles come back when it's reconnected. Nothing has been removed.")).toBeVisible();
    await expect(page.getByText("No movies yet")).toHaveCount(0);
  });

  test("partialLibraryBanner", async ({ page }) => {
    await open(page, MOVIES);
    await expect(page.getByRole("status")).toHaveText("Some titles are offline");
    await expect(page.locator("a.card").first()).toBeVisible();
  });

  test("partialBannerOnHome", async ({ page }) => {
    await open(page, "/");
    await expect(page.getByRole("status")).toHaveText("Some titles are offline");
  });

  test("errorShowsDiagnosticId", async ({ page }) => {
    const api = await open(page, MOVIES, (a) => a.set("/api/v1/items", 500));
    const alert = page.getByRole("alert");
    await expect(alert).toContainText("Couldn't load this library");
    await expect(alert).toContainText("Diagnostic ID req-fixture");
    api.set("/api/v1/items", { items: [generatedItem(1)], nextCursor: null, total: 1 });
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByRole("link", { name: /^Generated 0001/ })).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
  });

  test("offlineKeepsData", async ({ page }) => {
    await open(page, MOVIES);
    await expect(page.getByRole("link", { name: /^Synthetic Test Movie/ })).toBeVisible();
    await page.route("**/api/v1/**", (r) => r.abort());
    await page.getByRole("combobox", { name: "Sort" }).selectOption("added");
    await expect(page.getByText("Can't reach the server. Showing what was loaded before.")).toBeVisible();
    await expect(page.getByRole("link", { name: /^Synthetic Test Movie/ })).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
  });

  test("detailErrorShowsDiagnosticId", async ({ page }) => {
    await open(page, "/items/does-not-exist", (api) => api.set("/api/v1/items/does-not-exist", 500));
    await expect(page.getByRole("alert")).toContainText("Couldn't load this item");
    await expect(page.getByRole("alert")).toContainText("Diagnostic ID req-fixture");
  });

  test("emptyPollStopsOnLeave", async ({ page }) => {
    await page.clock.install();
    const api = await open(page, ANIME, (a) => a.generate(0, "lib-synthetic-anime"));
    await expect(page.getByText("No anime yet")).toBeVisible();
    const n = () => api.of("GET", "/api/v1/libraries").length;
    const start = n();
    await page.clock.runFor(5100);
    await expect.poll(n).toBe(start + 1); // polling is live
    await page.getByRole("link", { name: "Home", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Home" })).toBeAttached();
    const left = n();
    await page.clock.runFor(6000);
    await page.waitForTimeout(200);
    expect(n()).toBe(left);
  });

  test("unavailableVersionShowsFileOffline", async ({ page }) => {
    const d = JSON.parse(readFileSync(new URL("../../testdata/contracts/catalogue/item-movie.json", import.meta.url), "utf8"));
    d.versions = d.versions.map((v: object) => ({ ...v, available: false }));
    await open(page, `/items/${d.item.id}`, (api) => api.set(`/api/v1/items/${d.item.id}`, d));
    await expect(page.getByRole("heading", { name: "File offline" })).toBeVisible();
    await expect(page.getByText("The drive holding this movie isn't connected. Your progress is kept. Play works again once it's back.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Play unavailable" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Play", exact: true })).toHaveCount(0);
  });
});
