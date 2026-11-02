import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD } from "./fake-api";

async function signIn(page: Page) {
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
}

async function browse(page: Page, path = "/") {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  await page.goto(path);
  await signIn(page);
  return api;
}

const nav = (page: Page) => page.getByRole("navigation");

test.describe("browse shell", () => {
  test("fallsBackToM1WhenLibrariesNotServed", async ({ page }) => {
    await FakeAPI.attach(page);
    await page.goto("/");
    await signIn(page);
    await expect(page.getByRole("heading", { name: "Synthetic Test Movie" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Play" })).toBeVisible();
  });

  test("shellShowsLibrariesAsTabs", async ({ page }) => {
    await browse(page);
    await expect(nav(page).getByRole("link")).toHaveText(["Home", "Movies", "Anime"]);
    await expect(nav(page).getByRole("link", { name: "Home" })).toHaveAttribute("aria-current", "page");
  });

  test("tabNavigationUsesHistory", async ({ page }) => {
    await browse(page);
    await nav(page).getByRole("link", { name: "Movies" }).click();
    await expect(page).toHaveURL(/\/libraries\/lib-synthetic-movies$/);
    await expect(nav(page).getByRole("link", { name: "Movies" })).toHaveAttribute("aria-current", "page");
    await page.goBack();
    await expect(nav(page).getByRole("link", { name: "Home" })).toHaveAttribute("aria-current", "page");
  });

  test("deepLinkLoadsDetail", async ({ page }) => {
    await browse(page, "/items/mov-synthetic-0001");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Test Movie");
  });

  test("unknownPathShowsNotFound", async ({ page }) => {
    await browse(page, "/nope");
    await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
    await expect(page.getByRole("main").getByRole("link", { name: "Home" })).toBeVisible();
  });

  test("fontsAreSameOrigin", async ({ page }) => {
    const origins = new Set<string>();
    const fonts: string[] = [];
    page.on("request", (r) => {
      origins.add(new URL(r.url()).origin);
      if (r.resourceType() === "font") fonts.push(r.url());
    });
    await browse(page);
    await expect(nav(page)).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    expect(fonts.length).toBeGreaterThan(0);
    expect([...origins]).toEqual([new URL(page.url()).origin]);
  });

  test("signOutClearsCachedRows", async ({ page }) => {
    const api = await browse(page);
    await expect(nav(page)).toBeVisible();
    await expect.poll(() => api.of("GET", "/api/v1/items").length).toBe(2);
    await page.getByRole("button", { name: "Profile" }).click();
    await page.getByRole("button", { name: "Sign out" }).click();
    await signIn(page);
    await expect.poll(() => api.of("GET", "/api/v1/items").length).toBe(4);
  });

  test("phoneWidthUsesBottomTabBar", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await browse(page);
    const bar = nav(page);
    await expect(bar).toHaveCSS("position", "fixed");
    const box = (await bar.boundingBox())!;
    expect(box.y + box.height).toBeGreaterThan(844 - 2);
    await page.getByRole("link", { name: "Home" }).first().focus();
    await page.keyboard.press("Tab");
    await expect(page.getByRole("link", { name: "Movies" })).toBeFocused();
  });
});
