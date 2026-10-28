# UI-2 Browse-to-Play (Layout C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the one-movie M1 page with layout C: a Home page, library grids, a detail page and the existing player. A viewer can pick any movie or episode they're allowed to see, choose a version, and resume or start from the beginning.

**Architecture:** The plan ships as three PRs:
- **PR A: contract additions.** These are the shapes the mockups need that CTR-2 left out: home rows, filters, sort, A–Z jump, totals, facets, scan progress, availability of source folders and files, credits, rating, size and filename. They follow CTR-2's pattern: OpenAPI, Go types, fixtures, and checks in Ajv, Go round-trip and TypeScript. Every new operation is `planned` and names its producer.
- **PR B: playback by item and version.** `PlaybackSessionRequest` becomes `{itemId, versionId}`. The playback server resolves each request through a `Resolver`, so LIB-2c can plug in the catalogue. M1's single configured movie becomes one resolver.
- **PR C: the web app.** React with no new runtime libraries, apart from the two bundled font packages. It has a History-API router, a hand-rolled virtual grid and a shared row component, plus the §5.7 states. Module acceptance runs in Playwright against the fixture fake API.

**Tech Stack:**
- **Web:** React 19.3.1, Vite 8.3.5, TypeScript 5.9.3, Playwright 1.64.1 (all existing). New: `@fontsource-variable/inter` and `@fontsource-variable/outfit`, exact versions at least 14 days old (#78).
- **Server:** Go 1.27.3 standard library.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §5.1 layouts, §5.2 pages, §5.5 phones, §5.6 accessibility, §5.7 states
- §3.8 resume and progress, §10.1 and §12 (no paths in public shapes)
- §13.1 PERF-05 and SCALE-02, §9.4 UI-2 row, §14 D13

Mockups: [`docs/design/ui-2/`](../../design/ui-2/README.md). Notes from their review: #95.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **UI-2**: browse-to-play, layout C. P0 / G1. Also the PLY-4 request-shape item that CTR-2 decision 12 assigned to PLY-4 (Pete, 2026-10-28). The rest of PLY-4 (seek generations, pause, job cleanup) stays unclaimed |
| Issue | #113 (claimed by Pete). #95's checklist closes with PR A + C |
| Owner / reviewers | Pete (claimant). Lammy reviews all three PRs: he owns CTR-2 and wrote PLY-2. Ondo reviews PR C |
| Acceptance | §5.1–5.2 (layout C pages in scope below), §5.5 phone tab bar, §5.6 keyboard and labels, §5.7 states, §3.8 resume confirmation. **Module:** Playwright against fixtures, including a 5,000-movie generated library. **Integrated:** PERF-05 / SCALE-02 on Pete's library (about 5,000 movies and 1,000 shows) once LIB-2c serves it |
| File scope | **PR A:** `docs/contracts/openapi.json`, `server/internal/contracts/{catalogue,userdata,types}.go` + tests, `testdata/contracts/catalogue/**`, `web/contract-tests/*`, generated `web/src/api/schema.d.ts`.<br>**PR B:** `server/internal/playback/*`, `server/cmd/lisimflix/serve.go`, the contract's two playback schemas, the OPS-5 plan's event table (`docs/superpowers/plans/2026-10-27-ops-5-baseline-logging.md`, `movie_id` → `item_id` + `version_id`; Lammy asked for this on #114), `testdata/contracts/m1/playback.json`, `web/src/api/client.ts` (`start`), `web/src/player/M1App.tsx`, `web/tests/{fake-api,player.spec}.ts`.<br>**PR C:** `web/src/**` (new `app/`, `browse/`, `ui/` folders), `web/index.html`, `web/public/**`, `web/package.json` + lockfile, `web/tests/**`, `README.md` |
| Consumes | CTR-2 shapes (merged, #87). UI-1's `VideoPlayer` and client (merged, #28) |
| Produces | The contract additions in PR A, `playback.Resolver` (PR B), and the browse app (PR C) |
| Start dependency | None. All three PRs run against fixtures. PR B and PR C each start once PR A merges. PR C's player task needs PR B |
| Standalone test | `go -C server test -race -count=1 ./internal/contracts/ ./internal/playback/ ./cmd/...` (A, B). `npm --prefix web run test:contracts && npm --prefix web run test:browser` (all). Then `make check` |
| Integration proof | Needs real producers: LIB-2c (libraries, items, facets, scan status, availability, a catalogue `Resolver`), DATA-1 (progress, watched, Continue Watching), DATA-2 (Next Up), META-1 (credits, rating, `matched`). Task 9 is the actual-scale run. Record "module accepted" on the UI-2 issue when PR C merges, and "integrated" after Task 9 |

## Global Constraints

- **No file paths for viewers (§10.1, §12).** `MediaVersion.fileName` is a bare file name, non-null only for admins. The web app shows it only when non-null.
- **Same-origin, relative URLs only.** Image URLs match `^/api/v1/`, and `contentUrl` must start with `/api/v1/playback/sessions/` (existing client check).
- **No third-party requests at runtime.** Fonts are bundled, not loaded from Google Fonts (#95 note 3). The OFL licence texts ship in `web/public/fonts/`.
- Theme tokens are copied from the mockup README: ground `#0E0F12`, accent `#7C9CFF`, scrollbar thumb `#FFFFFF2E`, Outfit for headings, Inter for text.
- **Rows:** no visible native scrollbar. The `<`/`>` buttons fade in on hover or keyboard focus and hide at the row's ends. A vertical wheel scrolls the row sideways and passes through to the page at either end.
- **Keyboard-complete (§5.6).** Every action is a `<button>` or `<a>` with a name. No action is hover-only. Focus is visible. Reduced motion turns off fades and smooth scrolling.
- **Contract rules (CTR-2):** every property is required, and unknown values are `null`. New operations carry `x-lisimflix: {owner, status: "planned"}`. A producer flips status in the PR that registers its handler.
- Code with no production caller gets `// Planned: <Feature ID> (<caller>).` (#60).
- Dependencies: exact versions, at least 14 days old (#78). No router, data-fetching or virtualisation library. They would replace about 150 lines total.
- Branches and PR titles:
  - `feat/ui-2-contracts`: `feat(api): UI-2 browse contract additions`
  - `feat/ui-2-playback-items`: `feat(playback): start playback by item and version`
  - `feat/ui-2-browse`: `feat(web): layout C browse-to-play (UI-2)`

## Decisions taken in this plan (please review)

1. **Filenames are for admins only** (Pete, 2026-10-28, #95 note 2). Viewers see the item's title (LIB-2a's parsed title when unmatched) and version labels such as `1080p · H.264 · 1.1 GB`.
2. **Home rows are separate operations, not one `/home` blob.**
   - Continue Watching: `GET /api/v1/continue-watching`, owner **DATA-1**. It's derived from resume state (§3.8: played ≥30 s, not watched, not hidden).
   - Next Up: `GET /api/v1/next-up`, owner **DATA-2** (personal rows).
   - Recently Added isn't a new operation: it's `listItems?libraryId=…&sort=added`.
   - Row order and hiding (§5.2) are DATA-2 preferences, out of scope here.
3. **Filters and sort are `listItems` query parameters**, owner **LIB-2** (LIB-2c):
   - `sort` = `title` (default, A→Z), `added` (newest first) or `year` (newest first)
   - `genre`, `yearFrom`, `yearTo`, `resolution` (`sd`, `720p`, `1080p`, `4k`), `hdr`, `audioLanguage`, `watched`
   - `audioLanguage` and the facets' languages use **ISO 639-2/T** (`deu`, `fra`, `zho`). The producer maps /B tags (`ger`, `fre`, `chi`) to /T, so German is listed once (Lammy, #114). `AudioStream.language` stays "as tagged" (CTR-2).
   - `watched` is LIB-2c's parameter but reads DATA-1's data. Until DATA-1 is live, LIB-2c answers it with `400 invalid_request`, and the Unwatched chip hides on that 400.
   - **Title collation, fixed in the contract** (Lammy, #114): titles compare by `sortTitle` folded to lower case with accents removed (Unicode NFKD, combining marks dropped, so `Élite` sorts and jumps as `elite`). Ties break by item ID.
   - `from` (A–Z jump): one letter `A`–`Z` or `#`, only valid with `sort=title`. A letter starts the page at the first item whose folded `sortTitle` begins with that letter or a later one. `#` covers every item whose folded `sortTitle` doesn't start with `a`–`z`: digits, symbols and non-Latin titles such as Japanese. These **sort before A**, so `#` is the first page.
   - A combination the server can't apply is `400 invalid_request`.
   - Filters apply only to `libraryId` listings. With `parentId`, any filter is a 400.
4. **`ItemPage.total`** (`integer | null`, required) is the count after permission and filters (§5.7: no leaks through counts). It's an integer on the first page of a `listItems` call (no `cursor`) and null on later pages and for the home-row operations. That way the count runs once per listing, not per page (Lammy, #114). The library header shows it ("412 titles").
5. **`GET /api/v1/libraries/{libraryId}/facets`**, owner LIB-2: genres, year range, audio languages, resolutions and an HDR flag present in the permitted items. It fills the filter menus without guessing.
6. **Scan progress and availability.** A library has one or more source folders (§3.1; for example `/store/tv` and `/mnt/temp/tv`), and LIB-2b tracks each one's state. So availability is per file, with only a summary per library (Pete, #114). Owner LIB-2 throughout:
   - **`Library.scan`** shows a first scan in progress: `{state: "idle" | "scanning", filesSeen, filesTotal}`, with `filesTotal` null while still counting. It's for the "No anime yet · 1,204 of 3,410 files" empty state. LIB-2b records the counters as it goes (Lammy, #114).
   - **`Library.availability`** = `available` | `partial` | `offline`: all, some or none of its source folders are readable. `partial` shows the banner "Some titles are offline". `offline` with no items shows "This library's folder is offline", never "No movies yet". Folder paths are never sent to viewers.
   - **`MediaVersion.available`** (boolean) is false when the version's source folder is offline or its file is missing. The version picker labels it "(offline)", and Play on it shows the **File offline** state without starting a session.
   - **`ItemSummary.available`** (`boolean | null`) is true if any version can play and null for shows and seasons. Cards show a **File offline** badge on `false`.
   - `degraded` (a scan failed part-way, but the files are still readable) isn't a viewer state. It belongs to the admin scan status (UI-6, LIB-2c).
7. **Item additions:**
   - `ItemSummary`, all owner LIB-2 except `matched`:
     - `addedAt`
     - `childCount`: seasons of a show, episodes of a season, otherwise null
     - `showTitle` and `seasonIndex`: set on episodes, and `showTitle` also on seasons; null otherwise. Cards in mixed rows read "[Show] · S1 E4" from these.
     - `matched` (META-1, `boolean | null`): null means matching hasn't run (all items until META-1 lands), and only `false` shows the **Unmatched** badge (Lammy, #114).
   - `ItemDetail` (META-1): `communityRating` `{source, value}` or null, `directors[]`, `studios[]`.
   - `MediaVersion` (LIB-2): `sizeBytes` (null if unknown) and `fileName` (decision 1).
8. **Playback takes `{itemId, versionId}`; `movieId` is removed, not deprecated.** Web and server ship in one image, so there are no old clients. `PlaybackSession` returns `itemId` and `versionId`. The grant binds both. M1 maps its configured movie to `itemId = versionId = movie.id` with `fileId = movie.id`, so existing source revisions don't change.
9. **Resume rule (§3.8).**
   - If the chosen version's ID **and** `sourceRevision` equal `userState.resume`'s, show **Resume from m:ss** and start there.
   - If the resume point belongs to a different version or revision, the button opens a confirmation instead: "Your resume point is from another version. Resume at 7:42 anyway?", with **Resume anyway** and **Play from start**.
   - The player sends `reportProgress` (planned, DATA-1) every 10 s and on pause, seek and stop. A 404 means DATA-1 isn't live yet; other failures are ignored, except 401 (sign-in ended) and 409 `source_changed` (ends playback with its message).
10. **Until LIB-2c serves `listLibraries`, the app falls back to M1.** `main.tsx` calls `listLibraries` once. A 404 `not_found` (route not registered) renders `M1App`; anything else renders the browse app.
    - `// shortcut: M1 fallback until LIB-2c registers listLibraries; delete M1App, /movies and Movie in LIB-2c's PR` (CTR-2 decision 12 gives that retirement to UI-2/PLY-2; LIB-2c's PR does it once items are served).
11. **A–Z jump restarts the grid at that letter.** It doesn't scroll back through unloaded pages. A "Back to top" button clears it.
    - `// shortcut: jump restarts the list; upgrade to bidirectional paging if Pete finds it awkward at 5k titles`.
12. **Scroll and focus restore** (§5.7) use an in-memory cache keyed by `history.state.key`. It holds the loaded pages, cursor, scroll position and focused item ID. Back restores them without refetching. A reload refetches the first page.
13. **Out of scope, each with its owner:**
    - Search and the header search button (no search feature ID is claimed; it's hidden until one is)
    - Who's watching / profile picker (ACC-1)
    - Watchlist `+` button (DATA-2)
    - "More from Blender Studio" related row (UI-7 / META-3)
    - Fix-match action (META-1 / UI-6; viewers just see the badge)
    - Layouts A/B and themes (UI-7)
    - TV mode and D-pad (UI-5)
    - PWA (UI-5)
    - Remove from Continue Watching (DATA-1 write; the row honours `hiddenFromContinueWatching`)

## Review Focus

1. **A profile switch or sign-out mid-browse** must not show the old profile's cached rows. The cache is cleared on logout; the test is in Task 5.
2. **A source folder that goes offline** (for example `/mnt/temp` unmounted while `/store` stays up) must affect only its own titles. Their cards and versions say File offline, the library shows the partial banner, and the rest still plays. If it drops between loading the detail page and pressing Play, the `503 source_unavailable` from start becomes the same File offline state, not a spinner. Tasks 4, 7 and 8.
3. **An episode whose file holds two episodes** (`segment` null) has no duration. Its card shows no progress bar and no "min left", and doesn't crash on `durationMs: null`. Task 6.
4. **A huge library with fast scrolling or a jump** must not render thousands of cards or start overlapping page fetches. Task 4 caps DOM cards and in-flight requests.
5. **A resume position past the end** (a file replaced by a shorter one, or same version but a stale revision) must take the confirmation path, never seek past the end. Task 8.

---

## PR A: contract additions (`feat/ui-2-contracts`)

### Task 1: Contract additions with fixtures

**Files:**
- Modify: `docs/contracts/openapi.json`
- Modify: `server/internal/contracts/catalogue.go`, `userdata.go`, `catalogue_test.go`, `userdata_test.go`
- Modify: `testdata/contracts/catalogue/{libraries,items-movies,items-episodes,item-movie,item-episode-shared-file,item-show}.json`, `README.md`
- Create: `testdata/contracts/catalogue/{facets,continue-watching,next-up}.json`
- Modify: `web/contract-tests/fixtures.test.mjs`, `web/contract-tests/schema.test.ts`
- Regenerate: `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`)

**Interfaces:**
- Produces (OpenAPI → Go):
  - `Library.scan`: `LibraryScan{State string; FilesSeen int64; FilesTotal *int64}`, with `ScanIdle = "idle"` and `ScanScanning = "scanning"`.
  - `Library.availability`: `Availability string`, with `AvailabilityAvailable = "available"`, `AvailabilityPartial = "partial"` and `AvailabilityOffline = "offline"`.
  - `ItemSummary` gains `AddedAt time.Time`, `ChildCount *int64`, `ShowTitle *string`, `SeasonIndex *int64`, `Matched *bool` and `Available *bool`.
  - `ItemPage` becomes its own type `ItemPage{Items []ItemSummary; NextCursor *string; Total *int64}` and encodes nil `Items` as `[]`. `Page[T]` stays for libraries and profiles.
  - `ItemDetail` gains `CommunityRating *CommunityRating{Source string; Value float64}` (source pattern `^[a-z][a-z0-9]{1,31}$`, value 0–10), `Directors []string` and `Studios []string`.
  - `MediaVersion` gains `SizeBytes *int64`, `Available bool` and `FileName *string` (maxLength 255, no `/` or `\`: pattern `^[^/\\\\]+$`).
  - `LibraryFacets{Genres []string; Years *YearRange{Min, Max int64}; AudioLanguages []string; Resolutions []string; HDR bool}`, with resolution constants `ResolutionSD = "sd"`, `Resolution720 = "720p"`, `Resolution1080 = "1080p"`, `Resolution4K = "4k"`.
  - Sort constants `SortTitle = "title"`, `SortAdded = "added"`, `SortYear = "year"`.
- Produces operations:

  | Operation | Owner | Summary |
  |---|---|---|
  | `GET /api/v1/libraries/{libraryId}/facets` `getLibraryFacets` → `LibraryFacets` | LIB-2 | 404 for a library the profile may not see |
  | `GET /api/v1/continue-watching` `listContinueWatching` (`cursor`, `limit`) → `ItemPage` | DATA-1 | Movies and episodes with `resume`, not watched, not hidden; newest `lastPlayedAt` first |
  | `GET /api/v1/next-up` `listNextUp` (`cursor`, `limit`) → `ItemPage` | DATA-2 | The next unwatched episode per show in progress, in the library's order; specials are never inserted |

  `listItems` gains the query parameters in decision 3. Its summary adds the 400 rules for `from` without `sort=title` and filters with `parentId`, and states decision 3's collation and the `#` rule word for word, so LIB-2c and the web app sort the same way.
  - `ItemSummary` and `ItemDetail` descriptions cite decisions 1 and 7.
  - `MediaVersion.fileName` reads: "Bare file name, admins only; null for other viewers. Never a path."

- [ ] **Step 1: Update the fixtures and add the three new ones**

  Existing fixtures gain the new required fields with realistic values:
  - `items-movies.json`: `total: 412`. One item has `matched: false` and `available: false`, and another has `matched: null`.
  - `items-episodes.json`: `showTitle` and `seasonIndex: 1` on both episodes, and `total: null` (a later page).
  - `item-show.json`: `childCount: 2`.
  - `item-movie.json`: version 1 has `fileName: null` (a viewer's response) and `sizeBytes: 1181116006`. Version 2 has `fileName: "synthetic.directors.cut.2160p.mkv"` (an admin's), `sizeBytes: 24000000000` and `available: false` (its folder is offline). Version 1 has `available: true`. It also gets `communityRating: {"source": "tmdb", "value": 6.4}`, `directors: ["Synthetic Director"]` and `studios: ["Synthetic Studio"]`.
  - `libraries.json`: the anime library has `scan: {"state": "scanning", "filesSeen": 1204, "filesTotal": 3410}` and `availability: "available"`. The movie library has `{"state": "idle", "filesSeen": 0, "filesTotal": null}` and `availability: "partial"`.

  New fixtures:
  - `facets.json`: two genres, years 2006–2023, `["eng", "jpn"]`, `["1080p", "4k"]`, `hdr: true`.
  - `continue-watching.json`: one movie with a resume, and one episode with `showTitle` and a resume.
  - `next-up.json`: one episode, fresh state.

- [ ] **Step 2: Add rejection cases to `web/contract-tests/fixtures.test.mjs`**

```js
["path in fileName", "item-movie.json", (d) => { d.versions[1].fileName = "/media/movies/x.mkv"; }],
["windows path in fileName", "item-movie.json", (d) => { d.versions[1].fileName = "C:\\media\\x.mkv"; }],
["negative total", "items-movies.json", (d) => { d.total = -1; }],
["unknown scan state", "libraries.json", (d) => { d.items[0].scan.state = "paused"; }],
["degraded is not a viewer availability", "libraries.json", (d) => { d.items[0].availability = "degraded"; }],
["show with availability", "item-show.json", (d) => { d.item.available = true; }],
["version without available", "item-movie.json", (d) => { delete d.versions[0].available; }],
["rating above 10", "item-movie.json", (d) => { d.communityRating.value = 11; }],
["unknown facet resolution", "facets.json", (d) => { d.resolutions = ["8k"]; }],
["movie with a showTitle", "items-movies.json", (d) => { d.items[0].showTitle = "Synthetic Show"; }],
["negative childCount", "item-show.json", (d) => { d.item.childCount = -1; }],
```

  Register `facets.json` → `LibraryFacets`, and both row fixtures → `ItemPage`, so "every catalogue fixture is validated" still passes.

- [ ] **Step 3: Add the TypeScript checks to `schema.test.ts`**

```ts
// @ts-expect-error total is required on an item page (null on later pages)
export const noTotal: components["schemas"]["ItemPage"] = { items: [], nextCursor: null };
// @ts-expect-error scan states are a closed set
export const badScan: components["schemas"]["LibraryScan"] = { state: "paused", filesSeen: 0, filesTotal: null };
```

- [ ] **Step 4: Run the checks and watch them fail**

  Run `npm --prefix web run test:contracts` and `go -C server test ./internal/contracts/`.
  Expected: FAIL. The fixtures carry properties that aren't in OpenAPI or the Go types yet (`additionalProperties: false`, strict decode).

- [ ] **Step 5: Edit `openapi.json` and the Go types per the Interfaces block**

  - `showTitle` and `seasonIndex` join the `ItemSummary` `allOf`: both are null for movies and shows; `seasonIndex` is null for seasons.
  - Add `"LibraryScan"`, `"ItemPage"` and `"LibraryFacets"` to the round-trip and the enum checks (`checkEnum(t, "LibraryScan", "state", ScanIdle, ScanScanning)`, and the same for facets' resolutions and `sort`).

- [ ] **Step 6: Regenerate types and run all contract checks**

  Run: `npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck && go -C server test -race ./internal/contracts/`
  Expected: PASS, including `TestOpenAPIMatchesGoTypes`, `TestCatalogueFixturesRoundTrip` and `TestEmptyPagesEncodeItemsAsArray` (extended to `ItemPage`).

- [ ] **Step 7: Update the fixture README table and commit**

```bash
git add docs/contracts server/internal/contracts testdata/contracts web/contract-tests web/src/api/schema.d.ts
git commit -m "feat(api): UI-2 browse contract additions"
```

Then run `make check` and open PR A (Lammy reviews; tick #95's checklist items 1 and 2).

---

## PR B: playback by item and version (`feat/ui-2-playback-items`)

### Task 2: `Resolver` and the item + version request

**Files:**
- Modify: `docs/contracts/openapi.json`. `PlaybackSessionRequest` is now `{itemId, versionId}` and `PlaybackSession` replaces `movieId` with `itemId` + `versionId`. The `Grant` description says "item, version and source revision".
- Modify: `testdata/contracts/m1/playback.json`, `web/contract-tests/*` (the "missing field" case deletes `itemId`)
- Modify: `server/internal/contracts/types.go`
- Modify: `server/internal/playback/{source,sessions,http}.go` + `sessions_test.go`, `source_test.go`, `helpers_test.go`
- Modify: `server/cmd/lisimflix/serve.go`
- Modify: `web/src/api/client.ts`, `web/src/player/M1App.tsx`, `web/tests/fake-api.ts`, `web/tests/player.spec.ts`

**Interfaces:**
- Produces:
  ```go
  // Source is one playable file: what a session plays and where it lives under the media root.
  type Source struct {
      ItemID, VersionID, FileID string
      RelativePath              string
      SourceRevision            string
  }
  // Resolver maps a request to a Source the principal may play. ErrNotFound
  // for an unknown or forbidden item/version (same 404 either way).
  type Resolver interface {
      Resolve(p contracts.Principal, itemID, versionID string) (Source, error)
  }
  var ErrNotFound = errors.New("playback: no such item or version")
  // Single is M1's resolver: one movie, itemID == versionID == movie.ID.
  func Single(movie contracts.Movie, relativePath string, authorize func(contracts.Principal, string) bool) Resolver
  ```
  - `Options.Source` and `Options.Authorize` are replaced by `Options.Resolver`.
  - Each session stores its own `Source`. `statSource`/`open` take the session's `Source`, and the revision is `contracts.SourceRevision(src.FileID, size, mtimeNs)`.
  - `listMovies` and `Check()` move to a small M1 type returned by `Single`. `serve.go` keeps calling `pb.Check()` through it: `single.Check(root)` and `single.Movies(p)`, wired where `pb` is used today.
  - `// Planned: LIB-2c (a catalogue Resolver replaces Single).`
- Web: `M1Client.start(itemId: string, versionId: string, key: string, signal?)`. `M1App` calls `start(movie.id, movie.id, …)`.

- [ ] **Step 1: Write the failing tests in `server/internal/playback/sessions_test.go`**

```go
func TestStartByItemAndVersion(t *testing.T)            // 201; body itemId/versionId echo the request
func TestStartUnknownVersionIs404(t *testing.T)          // versionId != movie.ID → 404 not_found
func TestStartForbiddenItemIs404LikeUnknown(t *testing.T) // authorize false → same 404 body as unknown
func TestStartMissingVersionIs400(t *testing.T)          // {"itemId":"x"} → 400 invalid_request
func TestStartRejectsLegacyMovieID(t *testing.T)         // {"movieId":"x"} → 400 (unknown field, strict decode)
func TestReplaySameKeyDifferentVersionIs409(t *testing.T) // idempotency covers the version too
func TestGrantBindsVersion(t *testing.T)                 // content with a grant signed for another version → 403
func TestSessionsOfTwoSourcesStayApart(t *testing.T)     // fake Resolver with two files; each session serves its own bytes
```

  The last test uses a test `Resolver` over two temp files. That's the multi-source behaviour LIB-2c will rely on.

- [ ] **Step 2: Run them and watch them fail**

  Run: `go -C server test -race ./internal/playback/`
  Expected: FAIL to compile (`Resolver`, `Single` undefined).

- [ ] **Step 3: Implement**

  Make the `Resolver` change per Interfaces. The idempotency fingerprint becomes `itemID + "\x00" + versionID`. The `playback.start` log event gains `version_id` and keeps `movie_id` renamed to `item_id`. Update `TestEventsHaveStableFields` if it pins names.

- [ ] **Step 4: Update the contract, fixture, client and fake API**

  `FakeAPI` checks the body is exactly `{itemId, versionId}`, and returns 400 otherwise. In `player.spec.ts`, any assertion on `movieId` in the request body changes to the two fields.

- [ ] **Step 5: Run everything and watch it pass**

  Run: `go -C server test -race -count=1 ./internal/playback/ ./internal/contracts/ ./cmd/... && LISIMFLIX_M1_SMOKE=<clip> npm --prefix web run test:browser && make check`
  Expected: PASS. Every UI-1 browser test passes unchanged apart from the request body.

- [ ] **Step 6: Commit and open PR B**

```bash
git commit -am "feat(playback): start playback by item and version"
```

  Lammy reviews. The PR body names the M1 mapping (decision 8) and the integration obligation: LIB-2c supplies a catalogue `Resolver`.

---

## PR C: the browse app (`feat/ui-2-browse`)

Folder layout under `web/src/`:

| Path | Responsibility |
|---|---|
| `api/client.ts` | Adds catalogue calls to the client (renamed `Client`; `M1Client` stays as a type alias until LIB-2c). `ApiError` gains `requestId` |
| `app/router.ts` | `useRoute()`, `navigate()`, `Link` on the History API |
| `app/App.tsx` | Sign-in gate, header/tab bar, route switch, logout cache clear |
| `app/cache.ts` | Per-history-entry page cache (decision 12), `clearCache()` |
| `ui/{Row,VirtualGrid,Card,States,Chips}.tsx` | Shared components |
| `browse/{Home,Library,Detail,Show}.tsx` | Pages |
| `browse/format.ts` | Pure formatters: duration, "min left", version label, stream chips, bytes |
| `player/PlayerPage.tsx` | Wraps `VideoPlayer` with the start, resume and progress logic |

### Task 3: Client, router, shell, theme and fonts

**Files:**
- Modify: `web/src/api/client.ts`, `web/src/main.tsx`, `web/src/styles.css`, `web/index.html`, `web/package.json` + `package-lock.json`
- Create: `web/src/app/{router.ts,App.tsx,cache.ts}`, `web/public/logo.svg`, `web/public/fonts/{OFL-Inter.txt,OFL-Outfit.txt}`
- Test: `web/tests/browse.spec.ts`, `web/tests/fake-api.ts`

**Interfaces:**
- Produces, on `Client`, each returning the generated schema type:
  ```ts
  libraries(signal?): Promise<LibraryList>
  items(q: ItemQuery, signal?): Promise<ItemPage>        // ItemQuery = libraryId|parentId + cursor/limit/sort/from/filters
  item(id: string, signal?): Promise<ItemDetail>
  facets(libraryId: string, signal?): Promise<LibraryFacets>
  continueWatching(cursor?: string, signal?): Promise<ItemPage>
  nextUp(cursor?: string, signal?): Promise<ItemPage>
  setWatched(id: string, watched: boolean, expectedRevision: number): Promise<UserItemState>
  progress(sessionId: string, event: ProgressEvent): Promise<ProgressResult>
  ```
  - `ApiError` gains `readonly requestId: string | null`, read from the error body.
  - `items()` builds its query with `URLSearchParams`, omitting undefined values.
- `router.ts`: `type Route = {name: "home"} | {name: "library"; id: string} | {name: "item"; id: string} | {name: "play"; itemId: string; versionId: string; startMs: number | null} | {name: "notFound"}`. `useRoute(): Route`, `navigate(path: string, opts?: {replace?: boolean})`, and `Link` (an `<a>` that calls `navigate` on a plain left click).
  - Paths: `/`, `/libraries/:id`, `/items/:id`, `/play/:itemId/:versionId?t=<ms>`.
- `FakeAPI` serves the catalogue fixtures by path, and lets a test replace any body (`api.set(path, body | status)`).

- [ ] **Step 1: Write the failing tests in `browse.spec.ts`**

```ts
test("fallsBackToM1WhenLibrariesNotServed", ...)  // libraries → 404 not_found ⇒ "Synthetic Test Movie" + Play (M1App)
test("shellShowsLibrariesAsTabs", ...)            // tabs Home, Synthetic Movies, Synthetic Anime from libraries.json; Home is aria-current="page"
test("tabNavigationUsesHistory", ...)             // click Synthetic Movies ⇒ URL /libraries/lib-synthetic-movies; goBack ⇒ Home aria-current
test("deepLinkLoadsDetail", ...)                  // goto /items/mov-synthetic-0001 ⇒ heading is the item title
test("unknownPathShowsNotFound", ...)             // goto /nope ⇒ heading "Page not found" + link Home
test("fontsAreSameOrigin", ...)                   // every request during load has the page's origin
test("signOutClearsCachedRows", ...)              // load Home, sign out, sign in as fixture second viewer ⇒ no request served from cache (items calls repeat)
test("phoneWidthUsesBottomTabBar", ...)           // viewport 390x844 ⇒ nav is position:fixed at bottom; tabs still reachable by Tab
```

- [ ] **Step 2: Run them and watch them fail**

  Run: `npm --prefix web run test:browser -- browse.spec.ts`
  Expected: FAIL (no browse app).

- [ ] **Step 3: Implement**

  - **Client additions** per Interfaces.
  - **`main.tsx`:** the fallback per decision 10.
  - **`App.tsx` shell:**
    - The logo links to `/`. Tabs are Home plus one per library.
    - The profile button opens a small menu with **Sign out**, which reuses M1App's logout order: stop playback, logout, then refetch the session.
    - Logout calls `clearCache()`.
  - **Styles:** CSS custom properties for the theme tokens, scrollbar styling (thin, transparent track, `#FFFFFF2E` thumb on hover), and `@media (max-width: 640px)` for the bottom tab bar with `env(safe-area-inset-bottom)` padding.
  - **Fonts:** install `@fontsource-variable/inter` and `@fontsource-variable/outfit` with `--save-exact`, import their CSS in `main.tsx`, and copy each package's `LICENSE` into `web/public/fonts/`.
  - **`logo.svg`:** redraw the rounded-square L-and-play mark from `docs/design/ui-2/logo.png` in `#7C9CFF`, with the two-tone wordmark in text (Outfit). It's also the favicon.

- [ ] **Step 4: Run the tests and watch them pass**

  Run the same command. Expected: PASS. All of `player.spec.ts` still passes, because M1 mode is unchanged.

- [ ] **Step 5: Commit**

  `git commit -m "feat(web): UI-2 shell, router, theme and bundled fonts"`

### Task 4: Library page with virtual grid, filters, sort and A–Z

**Files:**
- Create: `web/src/ui/VirtualGrid.tsx`, `web/src/ui/Card.tsx`, `web/src/ui/Chips.tsx`, `web/src/browse/Library.tsx`, `web/src/browse/format.ts`, `web/tests/format.test.ts`. A new script `"test:unit": "node --test --experimental-strip-types tests/*.test.ts"` runs it, and `Makefile`'s `check` runs it after `test:contracts`. Playwright's default `testMatch` also matches `*.test.ts`, so set `testMatch: "**/*.spec.ts"` in `web/playwright.config.ts`
- Test: `web/tests/library.spec.ts`

**Interfaces:**
- `VirtualGrid<T>({items, total, hasMore, loadMore, renderCard, cardWidth = 180, gap = 16, cardAspect = 1.5, captionHeight = 52, overscanRows = 2})`.
  - Window-scroll based. Columns are `floor((width + gap) / (cardWidth + gap))`.
  - It renders only rows inside the viewport plus `overscanRows`, inside a spacer whose height comes from `total`.
  - It calls `loadMore()` when the last rendered row is within 2 rows of the loaded end. It never calls it again while a load is pending (it receives `loading: boolean`).
  - Arrow keys move focus card to card. Focus moving onto an unrendered row scrolls it into view first.
- `Card({item, href, size: "poster" | "wide"})`:
  - It shows artwork or a "No artwork" block, then the title, then the year, or for an episode `S{seasonIndex} E{index}`.
  - A ✓ badge when `userState?.watched?.value`, a progress bar when `resume` and `durationMs` are both present, an **Unmatched** badge only when `matched === false`, and a **File offline** badge when `available === false`.
  - The accessible name is the title plus the state words ("watched", "unmatched", "file offline", "{n} min left").
- `format.ts`:
  ```ts
  minutesLeft(resume: Resume | null, durationMs: number | null): number | null
  formatClock(ms: number): string            // 462000 → "7:42", 3723000 → "1:02:03"
  versionLabel(v: MediaVersion): string      // "1080p · H.264 · 1.1 GB" + (fileName ? ` (${fileName})` : ""); label first when non-null
  resolutionName(v: MediaVersion): "4K" | "1080p" | "720p" | "SD" | null
  formatBytes(n: number): string             // 1181116006 → "1.1 GB"
  ```
- `Library` reads its filter and sort state from the URL query (`?sort=added&genre=Sci-fi&from=M`), so back, forward and reload keep it.

- [ ] **Step 1: Write the failing unit tests (`format.test.ts`)**

```ts
assert.equal(formatClock(462000), "7:42");
assert.equal(formatClock(3723000), "1:02:03");
assert.equal(minutesLeft({ positionMs: 480000, durationMs: 720000 } as Resume, 720000), 4);
assert.equal(minutesLeft(null, 720000), null);
assert.equal(minutesLeft(resumeOf(600000), null), null);   // shared-file episode: no duration
assert.equal(formatBytes(1181116006), "1.1 GB");
assert.equal(versionLabel(fixture("item-movie.json").versions[0]), "1080p · H.264 · 1.1 GB");
assert.equal(versionLabel(fixture("item-movie.json").versions[1]), "Director's Cut · 4K · HEVC · 22.4 GB (synthetic.directors.cut.2160p.mkv)");
```

  (Adjust the expected strings to the fixture's actual label and codec after Task 1; the shape is what's pinned.)

- [ ] **Step 2: Write the failing browser tests (`library.spec.ts`)**

```ts
test("showsTotalAndCards", ...)                // "412 titles"; first card names from items-movies.json
test("unmatchedCardHasBadgeNoFilename", ...)   // matched:false ⇒ badge "Unmatched"; matched:null ⇒ no badge; card text has no ".mkv"
test("offlineCardBadgeAndPartialBanner", ...)  // available:false ⇒ "File offline" badge; library partial ⇒ status "Some titles are offline"; other cards unbadged
test("azRailStartsWithHash", ...)             // rail order is #, A … Z; click # ⇒ from=%23
test("totalKeptAcrossPages", ...)             // second page total:null ⇒ header still "412 titles"
test("virtualGridBoundsDom", ...)              // fake library of 5,000 generated items (50/page, total 5000): scroll to bottom ⇒ ≤ 200 cards in DOM at any sample; last item title visible
test("noOverlappingPageLoads", ...)            // during fast scroll, at most 1 items request in flight (fake API records concurrency)
test("sortAndFilterGoToServer", ...)           // choose Sort "Recently added", Genre "Sci-fi" ⇒ request has sort=added&genre=Sci-fi; URL query matches; reload keeps them
test("filterMenusComeFromFacets", ...)         // Genre menu lists exactly facets.json genres; Resolution menu exactly its resolutions
test("azJumpRequestsFrom", ...)                // click "M" ⇒ request from=M&sort=title, grid restarts; "Back to top" clears from
test("azHiddenWhenNotTitleSort", ...)          // sort=added ⇒ no A–Z rail
test("keyboardMovesBetweenCards", ...)         // focus first card, ArrowRight ⇒ second card focused; Enter opens its detail
test("firstPageVisibleUnder1sOnFixture", ...)  // module proxy for PERF-05: click tab → first card visible; record ms, assert < 1000
```

- [ ] **Step 3: Run them and watch them fail**

  Run: `npm --prefix web run test:unit && npm --prefix web run test:browser -- library.spec.ts`
  Expected: FAIL.

- [ ] **Step 4: Implement the components and page per Interfaces**

  The filter chips are All, Unwatched, Genre ▾, Year ▾, Resolution ▾, HDR and Audio language ▾. Each menu is a `<button aria-haspopup="listbox">` with a listbox, closed by Escape. The language list shows names from `Intl.DisplayNames(["en"], {type: "language"})`, mapping ISO 639-2 (/T, plus the /B forms that ffprobe tags carry, since `AudioStream.language` is as tagged) to 639-1 with a table for the common codes, falling back to the raw code.
  `// shortcut: 639-2→1 table covers common codes only; widen when a library shows raw codes`.

- [ ] **Step 5: Run them and watch them pass**

  Run the same command. Expected: PASS.

- [ ] **Step 6: Commit**

  `git commit -m "feat(web): UI-2 library grid with filters, sort and A–Z"`

### Task 5: Home page and the shared row

**Files:**
- Create: `web/src/ui/Row.tsx`, `web/src/browse/Home.tsx`
- Test: `web/tests/home.spec.ts`

**Interfaces:**
- `Row({title, seeAllHref?, items, renderCard, loadMore?})`:
  - An `overflow-x: auto` strip with its scrollbar hidden.
  - `<` and `>` are `<button aria-label="Scroll {title} left/right">`. They're visible on hover or focus-within (opacity), and get `hidden` when at that end.
  - Each scrolls by the strip's client width, minus one card.
  - A `wheel` listener (non-passive) converts `deltaY` to `scrollLeft` unless the strip is already at the end in that direction, in which case the page scrolls.
- `Home`:
  - The **Up next** card is the first Continue Watching item. It fetches `item(id)` for chips and genre, and has **Resume** and **Details**.
  - Chips filter which rows show: All, Movies, TV, Anime (by the library's `type`) and Unwatched (hides rows' watched items client-side).
  - Rows: Continue Watching, Next Up, and Recently Added · {library name} for each library, from `items({libraryId, sort: "added", limit: 20})`.
  - A row whose request returns 404 `not_found` (operation not live yet) is left out silently. Any other error shows that row's error state; the other rows still render.

- [ ] **Step 1: Write the failing tests**

```ts
test("rowsInOrder", ...)                       // headings: Continue Watching, Next Up, Recently Added · Synthetic Movies, Recently Added · Synthetic Anime
test("upNextIsFirstContinueItem", ...)          // hero heading = continue-watching.json items[0].title; Resume button present
test("episodeCardShowsShowAndNumber", ...)      // "Synthetic Show · S1 E2"
test("rowArrowsHideAtEnds", ...)                // at start: left arrow hidden; click right ⇒ left visible
test("wheelScrollsRowThenPage", ...)            // wheel over row: scrollLeft grows, page scrollY unchanged; at end: page scrollY grows
test("rowsAbsentWhenNotLive", ...)              // continue-watching → 404 ⇒ no Continue Watching heading, no error
test("rowErrorIsLocal", ...)                    // next-up → 500 ⇒ that row shows "Couldn't load" + Try again; other rows present
test("chipFiltersRows", ...)                    // click Anime ⇒ only Synthetic Anime rows
test("arrowsReachableByKeyboard", ...)          // Tab into row ⇒ right arrow visible (focus-within)
test("reducedMotionNoSmoothScroll", ...)        // emulateMedia reducedMotion ⇒ strip computed scroll-behavior is auto
```

- [ ] **Step 2: Run them and watch them fail**, then implement per Interfaces.

- [ ] **Step 3: Run them and watch them pass**

  Run: `npm --prefix web run test:browser -- home.spec.ts`. Expected: PASS.

- [ ] **Step 4: Commit**

  `git commit -m "feat(web): UI-2 home rows and shared row component"`

### Task 6: Detail pages: movie, show, season and episode

**Files:**
- Create: `web/src/browse/Detail.tsx`, `web/src/browse/Show.tsx`
- Test: `web/tests/detail.spec.ts`

**Interfaces:**
- Consumes: `Client.item`, `Client.items({parentId})`, `Client.setWatched`, `versionLabel`, `formatClock`, `minutesLeft`.
- `Detail` branches on `item.kind`:
  - **Movie or episode:**
    - Backdrop, then the poster, title, original title (if it differs), and a meta line: year · duration · first genre · `{SOURCE} {value}`.
    - Quality chips from the selected version: resolution, codec, the default audio's profile or codec plus "Stereo" or "5.1", HDR, and "Direct play".
    - The play buttons (Task 8).
    - Mark watched ✓, an `aria-pressed` toggle that calls `setWatched` with `expectedRevision`. On 409 `revision_conflict` it refetches the item and says "Updated elsewhere, try again".
    - The overview, then the version `<select>` (only when more than one version). Unavailable versions are labelled "(offline)". The default selection is the first available version. The side panel has Director, Studio, Audio, Subtitles and Added.
  - **Show:** a season picker, then the episode list for that season. Each episode row has its number, title, progress bar and Play.
  - **Season:** links to the show with that season preselected.
  - An item with `versions: []` shows "No file available for this item" and no Play.
- The Back button (top left) calls `history.back()` when the previous entry is in-app, otherwise it navigates to the item's library.

- [ ] **Step 1: Write the failing tests**

```ts
test("movieDetailFromFixture", ...)              // title, "TMDB 6.4", Director Synthetic Director, Studio Synthetic Studio, Added date
test("versionPickerOnlyWhenMany", ...)           // item-movie (2 versions) ⇒ select with both labels; episode fixture (1) ⇒ no select
test("viewerSeesNoFilename", ...)                // version 1 option text has no filename; version 2 (admin fixture) shows it
test("offlineVersionLabelledNotDefault", ...)    // version 2 option ends "(offline)"; selected by default is version 1
test("showListsSeasonsThenEpisodes", ...)        // item-show ⇒ season select; items?parentId calls; episode rows with numbers
test("sharedFileEpisodeHasNoDuration", ...)      // item-episode-shared-file ⇒ no "min" text, no progress bar, no crash
test("markWatchedSendsRevision", ...)            // click ✓ ⇒ PUT body {watched:true, expectedRevision:<fixture revision>}; aria-pressed true
test("markWatchedConflictRefetches", ...)        // PUT → 409 revision_conflict ⇒ GET item repeated; status message shown
test("noVersionsNoPlay", ...)                    // versions [] ⇒ text "No file available"; no Play button
test("backRestoresGridScrollAndFocus", ...)      // library: scroll to card #300, open it, Back ⇒ same scrollY ±1 row, card #300 focused, no items refetch
```

- [ ] **Step 2: Run them and watch them fail**, then implement per Interfaces, including the `cache.ts` restore from decision 12.

- [ ] **Step 3: Run them and watch them pass**

  Run: `npm --prefix web run test:browser -- detail.spec.ts`. Expected: PASS.

- [ ] **Step 4: Commit**

  `git commit -m "feat(web): UI-2 detail, show and episode pages"`

### Task 7: §5.7 states everywhere

**Files:**
- Create: `web/src/ui/States.tsx`
- Modify: `Home.tsx`, `Library.tsx`, `Detail.tsx` (swap the interim states in)
- Test: `web/tests/states.spec.ts`

**Interfaces:**
- `Skeleton({kind: "row" | "grid" | "detail"})`: placeholder shapes at the final sizes, with `aria-busy` on the region.
- `Empty({library})`:
  - "No {type} yet", where `type` is movies, shows or anime.
  - While `library.scan.state === "scanning"`, add "This library's folder is being scanned for the first time. Titles appear here as they're found." plus a `<progress>` and "1,204 of 3,410 files", or "1,204 files found" while `filesTotal` is null.
  - It re-polls `libraries()` every 5 s while scanning and stops on unmount.
- `ErrorState({error: ApiError, onRetry})`:
  - "Couldn't load this {thing}", plus "Diagnostic ID {requestId}" when present, plus **Try again**. The heading is in a `role="alert"` region.
- `OfflineBanner`:
  - When a refetch fails with status 0 but cached data exists, keep the data and show "Can't reach the server. Showing what was loaded before." in a `role="status"` region.
- `OfflineLibrary`: the library's `availability === "offline"` and no items. It shows "This library's folder is offline" and "Titles come back when it's reconnected. Nothing has been removed.", instead of `Empty`.
- `PartialBanner`: `availability === "partial"` shows "Some titles are offline" in a `role="status"` region above the grid and on Home.
- `SourceUnavailable` is the detail state for an unavailable version, or for a 503 `source_unavailable` from start (Task 8).

- [ ] **Step 1: Write the failing tests**

```ts
test("loadingSkeletonThenContent", ...)       // delay items 500 ms ⇒ aria-busy region visible, then cards; no layout shift > 1 card height
test("emptyWhileScanning", ...)               // anime library items [] + scan scanning ⇒ "No anime yet", progress value 1204 max 3410, text "1,204 of 3,410 files"
test("emptyScanCountingTotal", ...)           // filesTotal null ⇒ "1,204 files found", no max
test("emptyIdleLibrary", ...)                 // items [] + idle + available ⇒ "No movies yet" and no progress
test("offlineLibraryNotEmpty", ...)          // items [] + availability offline ⇒ "This library's folder is offline"; no "No movies yet"
test("partialLibraryBanner", ...)            // availability partial ⇒ status "Some titles are offline"; cards still render
test("errorShowsDiagnosticId", ...)           // items → 500 {requestId:"req-fixture"} ⇒ alert, "Diagnostic ID req-fixture", Try again refetches
test("offlineKeepsData", ...)                 // load grid, then abort all routes, trigger refetch (sort change) ⇒ banner + old cards still shown
test("emptyPollStopsOnLeave", ...)            // scanning library, navigate Home ⇒ no further /libraries calls after 6 s
```

- [ ] **Step 2: Run them and watch them fail**, then implement, then run them and watch them pass.

  Run: `npm --prefix web run test:browser -- states.spec.ts`. Expected: PASS.

- [ ] **Step 3: Commit**

  `git commit -m "feat(web): UI-2 loading, empty, error and offline states"`

### Task 8: Play, resume and progress

**Files:**
- Create: `web/src/player/PlayerPage.tsx`
- Modify: `web/src/player/VideoPlayer.tsx` (two optional props), `web/src/browse/Detail.tsx`
- Test: `web/tests/play.spec.ts` (needs `LISIMFLIX_M1_SMOKE`, like `player.spec.ts`)

**Interfaces:**
- `resumeChoice(state: UserItemState | null, version: MediaVersion): {kind: "none"} | {kind: "resume"; positionMs: number} | {kind: "confirm"; positionMs: number}`, a pure function in `format.ts`:
  - `none` with no resume.
  - `resume` when the version ID and `sourceRevision` both match and `positionMs < (version.durationMs ?? Infinity)`.
  - `confirm` otherwise.
- Detail buttons:
  - `none`: **Play**.
  - `resume`: **Resume from m:ss** and **Play from start**.
  - `confirm`: **Resume from m:ss** opens a `<dialog>` with the decision 9 text, **Resume anyway** and **Play from start**. Escape closes it, and focus returns to the button.
- Play navigates to `/play/:itemId/:versionId?t=<ms>`.
- `PlayerPage`:
  - It starts the session with `client.start(itemId, versionId, key)`, reusing M1App's idempotency and generation logic (move those helpers to `player/start.ts` so both use one copy).
  - A version with `available: false` never starts a session. Detail shows **SourceUnavailable** for it directly.
  - It renders `VideoPlayer` with `startMs` and `onProgress`.
  - On end it goes back to the detail page, with an error message if there was one.
  - Errors:
    - 503 `source_unavailable` → back to detail in the **SourceUnavailable** state: "File offline", "The drive holding this {movie | episode} isn't connected. Your progress is kept. Play works again once it's back.", and a disabled **Play unavailable**.
    - 409 `source_changed` → detail refetch, then "This file changed. Press Play to start again."
    - 503 `capacity_unavailable` → its message, with **Try again**.
- `VideoPlayer` gets new props `startMs?: number` (set `currentTime` once on `loadedmetadata`) and `onProgress?: (e: ProgressEvent) => void`, called every 10 s while playing (`tick`), on `pause`, on `seeking` (`seek`, with `fromPositionMs` = the last reported position) and at stop (`stop`). The player keeps the sequence.
- `PlayerPage` sends each event with `client.progress`. It ignores 404 and network errors, ends playback on 401 or 409, and ignores everything else.

- [ ] **Step 1: Write the failing unit tests (`format.test.ts`)**

```ts
assert.deepEqual(resumeChoice(null, v1), { kind: "none" });
assert.deepEqual(resumeChoice(stateAt(462000, v1.id, v1.sourceRevision), v1), { kind: "resume", positionMs: 462000 });
assert.deepEqual(resumeChoice(stateAt(462000, v2.id, v2.sourceRevision), v1), { kind: "confirm", positionMs: 462000 });
assert.deepEqual(resumeChoice(stateAt(462000, v1.id, "f".repeat(64)), v1), { kind: "confirm", positionMs: 462000 });
assert.deepEqual(resumeChoice(stateAt(v1.durationMs! + 1, v1.id, v1.sourceRevision), v1), { kind: "confirm", positionMs: v1.durationMs! + 1 });
```

- [ ] **Step 2: Write the failing browser tests**

```ts
test("playSendsItemAndVersion", ...)            // choose version 2, Play ⇒ POST body {itemId, versionId: v2.id}; URL /play/…
test("resumeStartsAtPosition", ...)             // fixture resume 2000 ms on v1 ⇒ video.currentTime ≥ 2 within 3 s
test("otherVersionNeedsConfirm", ...)           // resume on v2, selected v1 ⇒ dialog; Play from start ⇒ currentTime < 1
test("progressTickPauseSeekStop", ...)          // clock.install; play 10 s ⇒ tick; pause ⇒ pause; seek ⇒ seek with fromPositionMs; Back ⇒ stop; sequences strictly increase
test("progress404IsSilent", ...)                // progress → 404 ⇒ playback continues, no alert
test("sourceUnavailableOnStart", ...)           // start → 503 source_unavailable ⇒ detail with "File offline", Play unavailable disabled
test("offlineVersionNoSession", ...)            // select the offline version ⇒ "File offline", Play unavailable disabled, no POST /playback/sessions
test("backFromPlayerStopsSession", ...)         // Back ⇒ DELETE session sent; detail page shown
test("logoutDuringPlaybackStopsFirst", ...)     // same order assertion as player.spec's logout test, from the browse shell
```

- [ ] **Step 3: Run them and watch them fail**, then implement per Interfaces.

- [ ] **Step 4: Run every web check and watch it pass**

  Run: `npm --prefix web run test:unit && LISIMFLIX_M1_SMOKE=<clip> npm --prefix web run test:browser && make check`
  Expected: PASS, including all of `player.spec.ts` (M1 fallback).

- [ ] **Step 5: Update the README and commit**

  In `README.md`, the Status line says browsing is built and runs against fixtures until LIB-2c, and the layout row adds `web/src/{app,browse,ui}`. Then commit `"feat(web): UI-2 play, resume and progress"`, open PR C (reviewers Lammy, Ondo) and tick #95 items 3–5.

### Task 9: Integration and actual-scale qualification (after the producers land)

Not code in this plan. It runs once LIB-2c (with a catalogue `Resolver`), DATA-1, DATA-2 and META-1 report `implemented`.

- [ ] **Step 1:** Each producer's PR flips `x-lisimflix.status`. The UI-2 fixture tests keep passing against the real responses (the producer's own schema test).
- [ ] **Step 2:** On Pete's server with the real library (record the exact movie, show, episode and file totals, the host, and the browser and version):
  - Run 20+ timed interactions each for opening a library, sorting, filtering, A–Z jumps, opening a detail, and Back.
  - Do this at idle, during a rescan and during a metadata update (SCALE-02).
  - Report p95 browser-visible time against PERF-05 (≤1 s), with the server's query time reported separately.
- [ ] **Step 3:** A real movie, a real episode and a multi-version item play from the browse app. Resume works across a reload.
- [ ] **Step 3a:** Unmount one source folder (e.g. a `/mnt/temp/tv`-style folder) while another stays up. Only its titles show File offline, the library shows the partial banner, the other titles still play, and nothing disappears from the catalogue. The A–Z rail reaches an accented title (`É…` under E) and a non-Latin title under `#`.
- [ ] **Step 4:** Record "integrated" on the UI-2 issue with the evidence in `docs/testing/`. If PERF-05 fails, file issues by measured cause (SCALE-04), not by guess.

---

## Self-review

- **Spec coverage:**
  - §5.1 C: Tasks 3–5.
  - §5.2: Home (5), Library (4), Detail (6), Player (8, reusing UI-1). Who's watching, Search and the other pages are out of scope by decision 13.
  - §5.5 tab bar: 3. §5.6: keyboard tests in 3–8.
  - §5.7: 7 and 8, plus restore in 6, pagination in 4, and no-leak totals in decision 4. Offline sources: decision 6, Tasks 4, 6, 7, 8 and 9.
  - §3.8 resume confirmation: 8.
  - PERF-05 / SCALE-02: module proxy in 4, real run in 9.
  - #95's items 1–3 are covered by decisions 1, 7 and the fonts in Task 3.
- **Types:** `ItemQuery`, `resumeChoice` and `versionLabel` are each defined once (Tasks 3, 8, 4) and used by name elsewhere. `Resolver` / `Single` are in Task 2 only.
- **Proportion:** contracts and tests are pinned. Component bodies are left to the implementer, except the wheel, overscan and resume rules, which the tests alone wouldn't determine.
