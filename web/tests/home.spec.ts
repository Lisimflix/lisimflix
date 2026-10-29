import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD, generatedItem } from "./fake-api";

async function open(page: Page, setup?: (api: FakeAPI) => void) {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  setup?.(api);
  await page.goto("/");
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  return api;
}

const rowHeadings = (page: Page) => page.locator("section.row h2");
const many = (n: number) => ({ items: Array.from({ length: n }, (_, i) => generatedItem(i + 1)), nextCursor: null, total: null });
const strip = (page: Page) => page.locator("section.row .strip").first();

test.describe("home", () => {
  test("rowsInOrder", async ({ page }) => {
    await open(page);
    await expect(rowHeadings(page)).toHaveText(["Continue Watching", "Next Up", "Recently Added · Movies", "Recently Added · Anime"]);
  });

  test("upNextIsFirstContinueItem", async ({ page }) => {
    await open(page);
    await expect(page.locator(".upnext").getByRole("heading")).toHaveText("Synthetic Test Movie");
    await expect(page.locator(".upnext .meta")).toContainText("Drama");
    await expect(page.getByRole("button", { name: "Resume" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Details" })).toBeVisible();
  });

  test("episodeCardShowsShowAndNumber", async ({ page }) => {
    await open(page);
    await expect(page.getByRole("link", { name: /^Synthetic Episode Two/ }).first()).toContainText("Synthetic Show · S1 E2");
  });

  test("rowArrowsHideAtEnds", async ({ page }) => {
    await open(page, (api) => api.set("/api/v1/continue-watching", many(30)));
    const left = page.getByRole("button", { name: "Scroll Continue Watching left" });
    const right = page.getByRole("button", { name: "Scroll Continue Watching right" });
    await expect(right).toBeVisible();
    await expect(left).toBeHidden();
    await right.click();
    await expect(left).toBeVisible();
  });

  test("wheelScrollsRowThenPage", async ({ page }) => {
    await page.setViewportSize({ width: 1000, height: 400 });
    await open(page, (api) => api.set("/api/v1/continue-watching", many(30)));
    const s = strip(page);
    await expect(page.getByText("Loading…")).toHaveCount(0); // layout settled before measuring
    await page.evaluate(() => document.fonts.ready);
    await s.scrollIntoViewIfNeeded();
    const box = (await s.boundingBox())!;
    await page.mouse.move(box.x + 200, box.y + 40);
    const y0 = await page.evaluate(() => scrollY);
    await page.mouse.wheel(0, 300);
    await expect.poll(() => s.evaluate((e) => e.scrollLeft)).toBeGreaterThan(0);
    expect(await page.evaluate(() => scrollY)).toBe(y0);
    await s.evaluate((e) => e.scrollTo({ left: e.scrollWidth, behavior: "instant" }));
    await expect.poll(() => s.evaluate((e) => e.scrollWidth - e.clientWidth - e.scrollLeft)).toBeLessThan(2);
    await page.mouse.wheel(0, 300);
    await expect.poll(() => page.evaluate(() => scrollY)).toBeGreaterThan(y0);
  });

  test("rowsAbsentWhenNotLive", async ({ page }) => {
    await open(page, (api) => api.set("/api/v1/continue-watching", 404));
    await expect(rowHeadings(page)).toHaveText(["Next Up", "Recently Added · Movies", "Recently Added · Anime"]);
    await expect(page.getByText("Couldn't load")).toHaveCount(0);
  });

  test("rowErrorIsLocal", async ({ page }) => {
    await open(page, (api) => api.set("/api/v1/next-up", 500));
    await expect(page.getByText("Couldn't load")).toHaveCount(1);
    await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
    await expect(rowHeadings(page)).toHaveText(["Continue Watching", "Next Up", "Recently Added · Movies", "Recently Added · Anime"]);
    await expect(page.getByRole("link", { name: /^Synthetic Test Movie/ }).first()).toBeVisible();
  });

  test("chipFiltersRows", async ({ page }) => {
    await open(page);
    await expect(rowHeadings(page)).toHaveCount(4);
    await page.getByRole("button", { name: "Anime" }).click();
    await expect(rowHeadings(page)).toHaveText(["Recently Added · Anime"]);
  });

  test("unwatchedChipHidesWatchedItems", async ({ page }) => {
    await open(page, (api) => api.set("/api/v1/next-up", { items: [{ ...generatedItem(1), userState: { ...generatedItem(1).userState, watched: { value: true } } }, generatedItem(2)], nextCursor: null, total: null }));
    await expect(page.getByRole("link", { name: /^Generated 0001/ })).toBeVisible();
    await page.getByRole("button", { name: "Unwatched" }).click();
    await expect(page.getByRole("link", { name: /^Generated 0001/ })).toHaveCount(0);
    await expect(page.getByRole("link", { name: /^Generated 0002/ })).toBeVisible();
  });

  test("arrowsReachableByKeyboard", async ({ page }) => {
    await open(page, (api) => api.set("/api/v1/continue-watching", many(30)));
    const right = page.getByRole("button", { name: "Scroll Continue Watching right" });
    await expect(right).toHaveCSS("opacity", "0");
    await strip(page).getByRole("link").first().focus();
    await expect(right).toHaveCSS("opacity", "1");
  });

  test("reducedMotionNoSmoothScroll", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await open(page);
    await expect(strip(page)).toHaveCSS("scroll-behavior", "auto");
  });
});
