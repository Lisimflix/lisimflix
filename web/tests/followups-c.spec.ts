// #145: follow-ups from the #142 review.
import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { FakeAPI, PASSWORD } from "./fake-api";

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

// Item movie with a resume point on its first version; `revision` changes that version's file.
function resumeOnV1(revision?: string) {
  const d = catalogue("item-movie.json");
  const v = d.versions[0];
  d.item.userState = { ...d.item.userState, revision: 1, resume: { positionMs: 2000, durationMs: v.durationMs, versionId: v.id, sourceRevision: revision ?? v.sourceRevision, updatedAt: "2026-10-27T08:15:00Z" } };
  return d;
}

const lib = (id: string, name: string, type: string, scan: object) => ({ id, name, type, scan, availability: "available" });

test("listboxScrollsActiveOptionIntoView", async ({ page }) => {
  await open(page, MOVIES);
  await page.getByRole("button", { name: /^Year/ }).click();
  const list = page.getByRole("listbox", { name: "Year" });
  await page.keyboard.press("End");
  const last = list.getByRole("option").last();
  await expect(last).toHaveAttribute("data-active", "true");
  await expect(last).toBeInViewport();
  const box = (await list.boundingBox())!;
  const opt = (await last.boundingBox())!;
  expect(opt.y + opt.height).toBeLessThanOrEqual(box.y + box.height + 1); // inside the capped list, not just the page
});

test("resumeDialogHasCancel", async ({ page }) => {
  await open(page, MOVIE, (a) => a.set(MOVIE_PATH, resumeOnV1("f".repeat(64))));
  const button = page.getByRole("button", { name: "Resume from 0:02" });
  await button.click();
  const dialog = page.getByRole("dialog");
  // Same version, changed file: not "another version".
  await expect(dialog).toContainText("This file changed after your resume point. Resume at 0:02 anyway?");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`${MOVIE}$`));
});

test("progress401LeavesNoNoticeAfterSignIn", async ({ page }) => {
  await page.clock.install();
  const api = await open(page, MOVIE, undefined, smokeClip());
  api.overrides.push(async (route, call) => {
    if (!call.path.endsWith("/progress")) return false;
    api.signedIn = false;
    await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: "unauthenticated", message: "fixture", requestId: "req-c" }) });
    return true;
  });
  await page.getByRole("button", { name: "Play", exact: true }).click();
  await expect.poll(() => page.locator("video").evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(0.3);
  await page.clock.runFor(10_000); // the first progress tick answers 401
  await expect(page.getByRole("alert")).toHaveText("Your sign-in has ended."); // on the sign-in screen
  api.overrides.length = 0;
  await signIn(page); // no reload: the notice store lives in memory
  await expect(page).toHaveURL(new RegExp(`${MOVIE}$`));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Synthetic Test Movie");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("scanPollingAddsTitlesOnceSomeShow", async ({ page }) => {
  await page.clock.install();
  const anime = (seen: number) => ({ items: [lib("lib-synthetic-anime", "Anime", "anime", { state: "scanning", filesSeen: seen, filesTotal: 3410 })], nextCursor: null });
  const api = await open(page, "/libraries/lib-synthetic-anime", (a) => {
    a.generate(2, "lib-synthetic-anime");
    a.set("/api/v1/libraries", anime(100));
  });
  await expect(page.locator("a.card")).toHaveCount(2);
  await expect(page.getByText("2 titles")).toBeVisible();
  api.generate(5, "lib-synthetic-anime");
  api.set("/api/v1/libraries", anime(200));
  await page.clock.runFor(5100);
  await expect(page.locator("a.card")).toHaveCount(5);
  await expect(page.getByText("5 titles")).toBeVisible();
});

test("handEditedYearFromCountsAsAFilter", async ({ page }) => {
  await open(page, `${MOVIES}?yearFrom=2000`);
  await expect(page.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "false");
});

test("signInSendsOneRequestOnDoubleSubmit", async ({ page }) => {
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  api.overrides.push(async (_route, call) => {
    if (call.path === "/api/v1/auth/login") await new Promise((r) => setTimeout(r, 500));
    return false;
  });
  await page.goto("/");
  await page.getByLabel("Username").fill("viewer");
  await page.getByLabel("Password").fill(PASSWORD);
  const button = page.getByRole("button", { name: "Sign in" });
  await button.click();
  await expect(button).toBeDisabled();
  await page.getByLabel("Password").press("Enter");
  await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeAttached();
  expect(api.of("POST", "/auth/login")).toHaveLength(1);
});

test("startupErrorOffersTryAgain", async ({ page }) => {
  let fail = true;
  const api = await FakeAPI.attach(page, undefined, { catalogue: true });
  api.signedIn = true;
  api.overrides.push(async (route, call) => {
    if (!fail || call.path !== "/api/v1/libraries") return false;
    await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ code: "internal_error", message: "fixture internal_error", requestId: "req-c" }) });
    return true;
  });
  await page.goto("/");
  await expect(page.getByRole("alert")).toHaveText("fixture internal_error");
  fail = false;
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeAttached();
});
