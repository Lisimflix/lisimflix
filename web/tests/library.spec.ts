import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD } from "./fake-api";

const MOVIES = "/libraries/lib-synthetic-movies";

async function open(page: Page, path = MOVIES, setup?: (api: FakeAPI) => void) {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  setup?.(api);
  await page.goto(path);
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  return api;
}

const cards = (page: Page) => page.locator("a.card");
const itemRequests = (api: FakeAPI) => api.of("GET", "/api/v1/items");
const lastQuery = (api: FakeAPI) => new URLSearchParams(api.calls.filter((c) => c.path === "/api/v1/items").at(-1)!.query);

test.describe("library page", () => {
  test("showsTotalAndCards", async ({ page }) => {
    await open(page);
    await expect(page.getByText("412 titles")).toBeVisible();
    await expect(page.getByRole("link", { name: /^Synthetic Test Movie/ })).toBeVisible();
    await expect(page.getByRole("link", { name: /^Synthetic Unmatched Movie/ })).toBeVisible();
  });

  test("unmatchedCardHasBadgeNoFilename", async ({ page }) => {
    await open(page);
    const unmatched = page.getByRole("link", { name: /^Synthetic Unmatched Movie/ });
    await expect(unmatched.getByText("Unmatched", { exact: true })).toBeVisible();
    await expect(unmatched).toHaveAccessibleName(/unmatched/);
    await expect(unmatched).not.toContainText(".mkv");
    // matched: null shows no badge
    await expect(page.getByRole("link", { name: /^Synthetic Test Movie/ }).getByText("Unmatched", { exact: true })).toHaveCount(0);
  });

  test("offlineCardBadgeAndPartialBanner", async ({ page }) => {
    await open(page);
    await expect(page.getByRole("status")).toHaveText("Some titles are offline");
    await expect(page.getByRole("link", { name: /^Synthetic Unmatched Movie/ }).getByText("File offline")).toBeVisible();
    await expect(page.getByText("File offline")).toHaveCount(1);
  });

  test("cardShowsProgressAndMinutesLeft", async ({ page }) => {
    await open(page);
    const card = page.getByRole("link", { name: /^Synthetic Test Movie/ });
    await expect(card).toHaveAccessibleName("Synthetic Test Movie, 60 min left");
    await expect(card.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "34");
  });

  test("azRailStartsWithHash", async ({ page }) => {
    const api = await open(page);
    const rail = page.getByRole("navigation", { name: "A–Z" });
    await expect(rail.getByRole("button").first()).toHaveText("#");
    const labels = await rail.getByRole("button").allTextContents();
    expect(labels).toEqual(["#", ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("")]);
    await rail.getByRole("button", { name: "#" }).click();
    await expect(page).toHaveURL(/from=%23/);
    await expect.poll(() => api.calls.some((c) => c.path === "/api/v1/items" && c.query?.includes("from=%23"))).toBe(true);
  });

  test("totalKeptAcrossPages", async ({ page }) => {
    const api = await open(page);
    await expect(page.getByText("412 titles")).toBeVisible();
    // The fixture says there is a next page; the grid loads it (total: null) and the header stays.
    await expect.poll(() => itemRequests(api).length).toBeGreaterThanOrEqual(2);
    await expect(page.getByText("412 titles")).toBeVisible();
  });

  test("virtualGridBoundsDom", async ({ page }) => {
    await open(page, MOVIES, (api) => api.generate(5000));
    await expect(page.getByText("5000 titles")).toBeVisible();
    let max = 0;
    const sample = async () => { max = Math.max(max, await cards(page).count()); };
    await expect(cards(page).first()).toBeVisible();
    await expect(async () => {
      await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
      await sample();
      await expect(page.getByRole("link", { name: /^Generated 5000/ })).toBeVisible({ timeout: 500 });
    }).toPass({ timeout: 60_000 });
    await sample();
    expect(max).toBeGreaterThan(0);
    expect(max).toBeLessThanOrEqual(200);
  });

  test("noOverlappingPageLoads", async ({ page }) => {
    const api = await open(page, MOVIES, (a) => a.generate(2000, "lib-synthetic-movies", 40));
    await expect(cards(page).first()).toBeVisible();
    for (let i = 0; i < 20; i++) {
      await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
      await page.waitForTimeout(30);
    }
    expect(itemRequests(api).length).toBeGreaterThan(2);
    expect(api.maxInFlight).toBe(1);
  });

  test("sortAndFilterGoToServer", async ({ page }) => {
    const api = await open(page);
    await expect(cards(page).first()).toBeVisible();
    await page.getByRole("combobox", { name: "Sort" }).selectOption({ label: "Recently added" });
    await page.getByRole("button", { name: /^Genre/ }).click();
    await page.getByRole("option", { name: "Animation" }).click();
    await expect.poll(() => lastQuery(api).toString()).toContain("genre=Animation");
    const q = lastQuery(api);
    expect(q.get("sort")).toBe("added");
    expect(q.get("libraryId")).toBe("lib-synthetic-movies");
    await expect(page).toHaveURL(/sort=added/);
    await expect(page).toHaveURL(/genre=Animation/);
    await page.reload();
    await expect(page.getByRole("combobox", { name: "Sort" })).toHaveValue("added");
    await expect(page.getByRole("button", { name: "Genre: Animation" })).toBeVisible();
    await expect.poll(() => lastQuery(api).get("genre")).toBe("Animation");
  });

  test("filterMenusComeFromFacets", async ({ page }) => {
    await open(page);
    await page.getByRole("button", { name: /^Genre/ }).click();
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["Animation", "Drama"]);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("listbox")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^Genre/ })).toBeFocused();
    await page.getByRole("button", { name: /^Resolution/ }).click();
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["1080p", "4K"]);
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: /^Audio language/ }).click();
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["English", "Japanese"]);
  });

  test("azJumpRequestsFrom", async ({ page }) => {
    const api = await open(page);
    await expect(cards(page).first()).toBeVisible();
    await page.getByRole("navigation", { name: "A–Z" }).getByRole("button", { name: "M" }).click();
    await expect.poll(() => lastQuery(api).get("from")).toBe("M");
    expect(lastQuery(api).get("sort")).toBe("title");
    await expect(page.getByRole("link", { name: /^Synthetic Test Movie/ })).toBeVisible();
    await page.getByRole("button", { name: "Back to top" }).click();
    await expect.poll(() => lastQuery(api).get("from")).toBeNull();
    await expect(page.getByRole("button", { name: "Back to top" })).toHaveCount(0);
  });

  test("azHiddenWhenNotTitleSort", async ({ page }) => {
    await open(page, `${MOVIES}?sort=added`);
    await expect(cards(page).first()).toBeVisible();
    await expect(page.getByRole("navigation", { name: "A–Z" })).toHaveCount(0);
  });

  test("keyboardMovesBetweenCards", async ({ page }) => {
    await open(page);
    await expect(cards(page).first()).toBeVisible();
    await cards(page).first().focus();
    await page.keyboard.press("ArrowRight");
    await expect(cards(page).nth(1)).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/items\/mov-synthetic-0002$/);
  });

  test("arrowDownKeepsFocusOnARenderedCardInBigLibrary", async ({ page }) => {
    await open(page, MOVIES, (api) => api.generate(5000));
    await expect(cards(page).first()).toBeVisible();
    await cards(page).first().focus();
    for (let i = 0; i < 60; i++) await page.keyboard.press("ArrowDown");
    const focused = page.locator("a.card:focus");
    await expect(focused).toBeInViewport();
    expect(await cards(page).count()).toBeLessThanOrEqual(200);
  });

  test("firstPageVisibleUnder1sOnFixture", async ({ page }) => {
    await open(page, "/");
    await page.getByRole("navigation").getByRole("link", { name: "Movies" }).click();
    const t0 = Date.now();
    await expect(cards(page).first()).toBeVisible();
    expect(Date.now() - t0).toBeLessThan(1000);
  });
});
