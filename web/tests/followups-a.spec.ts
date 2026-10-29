import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD, generatedItem } from "./fake-api";

const MOVIES = "/libraries/lib-synthetic-movies";

async function open(page: Page, path = "/", setup?: (api: FakeAPI) => void) {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  setup?.(api);
  await page.goto(path);
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  return api;
}

const cards = (page: Page) => page.locator("a.card");
const many = (n: number) => ({ items: Array.from({ length: n }, (_, i) => generatedItem(i + 1)), nextCursor: null, total: null });

test.describe("A1 profile menu", () => {
  test("escapeClosesAndReturnsFocus", async ({ page }) => {
    await open(page);
    const profile = page.getByRole("button", { name: "Profile" });
    await profile.click();
    await expect(page.getByRole("button", { name: "Sign out" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Sign out" })).toHaveCount(0);
    await expect(profile).toBeFocused();
  });

  test("outsideClickCloses", async ({ page }) => {
    await open(page);
    await page.getByRole("button", { name: "Profile" }).click();
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await page.mouse.click(5, 400);
    await expect(page.getByRole("button", { name: "Sign out" })).toHaveCount(0);
  });

  test("routeChangeCloses", async ({ page }) => {
    await open(page);
    await page.getByRole("navigation").getByRole("link", { name: "Movies" }).click();
    await page.getByRole("button", { name: "Profile" }).click();
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await page.evaluate(() => history.back());
    await expect(page.getByRole("button", { name: "Sign out" })).toHaveCount(0);
  });
});

test.describe("A2 route focus", () => {
  test("tabClickFocusesNewH1", async ({ page }) => {
    await open(page);
    await expect(page.getByRole("heading", { name: "Home" })).toBeAttached();
    await page.getByRole("navigation").getByRole("link", { name: "Movies" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Movies" })).toBeFocused();
  });

  test("detailH1FocusedAfterLoad", async ({ page }) => {
    await open(page, MOVIES);
    await cards(page).first().click();
    await expect(page.getByRole("heading", { level: 1 })).toBeFocused();
  });

  test("firstLoadDoesNotMoveFocus", async ({ page }) => {
    await open(page, MOVIES);
    await expect(page.getByRole("heading", { level: 1, name: "Movies" })).toBeVisible();
    await expect(page.getByRole("heading", { level: 1, name: "Movies" })).not.toBeFocused();
  });
});

test.describe("A3 grid focus", () => {
  test("rovingTabindexOneTabStop", async ({ page }) => {
    await open(page, MOVIES);
    await expect(cards(page).nth(1)).toBeVisible();
    await expect(page.locator('a.card[tabindex="0"]')).toHaveCount(1);
    await cards(page).first().focus();
    await page.keyboard.press("Tab");
    await expect(page.locator("a.card:focus")).toHaveCount(0);
    await page.keyboard.press("Shift+Tab");
    await expect(cards(page).first()).toBeFocused();
    await page.keyboard.press("ArrowRight");
    await expect(page.locator('a.card[tabindex="0"]')).toHaveAttribute("href", /0002$/);
  });

  test("focusedCardScrolledOutIsRestored", async ({ page }) => {
    await open(page, MOVIES, (api) => api.generate(5000));
    const first = page.getByRole("link", { name: /^Generated 0001/ });
    await first.focus();
    await page.evaluate(() => scrollTo(0, 30000));
    await page.evaluate(() => scrollTo(0, 0));
    await expect(first).toBeFocused();
  });
});

test.describe("A4 row arrows", () => {
  test("focusMovesWhenEndArrowHides", async ({ page }) => {
    await open(page, "/", (api) => api.set("/api/v1/continue-watching", many(30)));
    const left = page.getByRole("button", { name: "Scroll Continue Watching left" });
    const right = page.getByRole("button", { name: "Scroll Continue Watching right" });
    await right.focus();
    await page.locator("section.row .strip").first().evaluate((e) => e.scrollTo({ left: e.scrollWidth, behavior: "instant" }));
    await expect(right).toBeHidden();
    await expect(left).toBeFocused();
  });
});

test.describe("A5 A-Z rail", () => {
  test("targetsAtLeast24px", async ({ page }) => {
    await open(page, MOVIES);
    const buttons = page.getByRole("navigation", { name: "A–Z" }).getByRole("button");
    await expect(buttons).toHaveCount(27);
    for (const b of await buttons.all()) {
      const box = (await b.boundingBox())!;
      expect(box.width).toBeGreaterThanOrEqual(24);
      expect(box.height).toBeGreaterThanOrEqual(24);
    }
  });

  test("noOverlapWithCards", async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 900 });
    await open(page, MOVIES, (api) => api.generate(300));
    await expect(cards(page).first()).toBeVisible();
    const rail = (await page.getByRole("navigation", { name: "A–Z" }).boundingBox())!;
    for (const c of await cards(page).all()) {
      const box = (await c.boundingBox())!;
      expect(box.x + box.width).toBeLessThanOrEqual(rail.x);
    }
  });

  test("scrollsOnShortViewport", async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 360 });
    await open(page, MOVIES);
    const rail = page.getByRole("navigation", { name: "A–Z" });
    await expect(rail).toBeVisible();
    const box = (await rail.boundingBox())!;
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(box.y + box.height).toBeLessThanOrEqual(360);
    expect(await rail.evaluate((e) => e.scrollHeight > e.clientHeight)).toBe(true);
  });

  test("activeLetterDoesNotPushDuplicate", async ({ page }) => {
    await open(page, MOVIES);
    const m = page.getByRole("navigation", { name: "A–Z" }).getByRole("button", { name: "M" });
    await m.click();
    await expect(page).toHaveURL(/from=M/);
    const n = await page.evaluate(() => history.length);
    await m.click();
    expect(await page.evaluate(() => history.length)).toBe(n);
  });
});

test.describe("A6 phone tab bar", () => {
  test("manyLibrariesScrollInOneLine", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const libs = Array.from({ length: 12 }, (_, i) => ({
      id: `lib-${i}`, name: `Library number ${i}`, type: "movies",
      scan: { state: "idle", filesSeen: 0, filesTotal: null }, availability: "available",
    }));
    await open(page, "/", (api) => api.set("/api/v1/libraries", { items: libs, nextCursor: null }));
    const nav = page.getByRole("navigation", { name: "Libraries" });
    await expect(nav.getByRole("link")).toHaveCount(13);
    expect(await nav.evaluate((e) => e.scrollWidth > e.clientWidth && getComputedStyle(e).overflowX === "auto")).toBe(true);
    const heights = await nav.getByRole("link").evaluateAll((els) => els.map((e) => e.getBoundingClientRect().height));
    expect(Math.max(...heights)).toBeLessThan(50);
    const last = nav.getByRole("link").last();
    await last.focus();
    const box = (await last.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
  });
});

test.describe("A7 error heading", () => {
  test("libraryErrorIsH2", async ({ page }) => {
    await open(page, MOVIES, (api) => api.set("/api/v1/items", 500));
    await expect(page.getByRole("heading", { level: 2, name: /Couldn't load this library/ })).toBeVisible();
  });
});
