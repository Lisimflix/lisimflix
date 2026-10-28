# LIB-2c Catalogue API and Catalogue Playback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve the catalogue that LIB-2a builds and LIB-2b keeps current: libraries, items, facets and item details through CTR-2's four `LIB-2` operations, and play any catalogue file through a catalogue `Resolver`, so UI-2 can browse and play real libraries.

**Architecture:** New files in the `library` package (it owns the catalogue tables, so it owns their queries):
- **Derived columns:** a migration adds a folded sort key and display overrides to items, and a resolution, HDR flag, duration and audio languages per file. They're filled when an item is placed or a probe is stored, and backfilled once at startup. Every filter and sort is then a plain indexed SQL query, with no JSON parsing per request.
- **Queries:** `Store.Libraries`, `Store.Items`, `Store.Item` and `Store.Facets` take a `Grant` (what the caller may see) and return CTR-2's Go types. Pages use keyset cursors.
- **HTTP:** `library.API` serves `GET /api/v1/libraries`, `/libraries/{id}/facets`, `/items` and `/items/{id}`, mounted by `app` behind `auth.Require`.
- **Playback:** `library.Resolver` implements PR B's `playback.Resolver` (#130) over the catalogue. Until UI-2's browse app replaces `M1App`, serve chains it with M1's `Single`, so the M1 movie keeps playing.

**Tech Stack:** Go 1.27.3 standard library, `modernc.org/sqlite` through OPS-2's `storage`. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md): §3.1 (logical items and versions), §3.6 (offline roots, availability), §5.1–5.2 (what browsing shows), §6 (permissions and no leaks), §9.4 (the LIB-2 row), §13 (SCALE-01, SCALE-02, PERF-05). The contract is [`docs/contracts/openapi.json`](../../contracts/openapi.json) (CTR-2, plus UI-2 PR A #128).

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-2** (resilient catalogue, P0 / G1), slice **2c** of 4. 2a: catalogue store and offline-safe scans (#111). 2b: watcher, schedule and serve wiring (#134). **2c (this plan):** the viewer catalogue API and catalogue playback. **2d:** the admin API (scan status, duplicates #104, retirement, probe retry, scan now), with UI-6's contract additions (Decision 1) |
| Issue | #77 |
| Owner / reviewers | Lammy (claimant) / CODEOWNERS (Pete, Lammy and one backup) |
| Acceptance | CTR-2's `listLibraries`, `getLibraryFacets`, `listItems` and `getItem` move from `planned` to `implemented`, with every rule in their contract text: one of `libraryId`/`parentId`, the 400 rules, title collation and the `#` group, the A–Z jump, filters, `total` on the first page only, 404 for libraries and items the caller may not see, and no leaks through counts. **§3.6:** an offline root's items stay listed with `available: false`, and the library reads `partial` or `offline`. **Catalogue playback:** any present file of a permitted item plays through PR B's session API. **SCALE-02 / PERF-05:** module timing in tests (Task 6), then actual scale (Task 8) |
| File scope | **New:** `server/internal/textfold/{fold.go,fold_test.go}`; `server/internal/library/{derive,browse,detail,access,resolver,api}.go` and their tests; migration `server/internal/library/migrations/20261029120000_library_browse.sql`. **Modified:** `server/internal/metadata/score.go` (calls `textfold`), `server/internal/library/{scan,probes,store}.go` (fill derived columns), `server/internal/app/app.go` (+ test), `server/cmd/lisimflix/serve.go`, `docs/contracts/openapi.json` (statuses, collation text), `README.md` |
| Consumes | LIB-2a/2b: the catalogue tables, `Store`, `Scheduler.Status`. PR B (#130): `playback.Resolver`, `Source`, `ErrNotFound`, `Single`, `M1`. META-1a (#135): `Normalize`, which Task 1 moves into `textfold`. CTR-2: `contracts.Library`, `LibraryScan`, `LibraryFacets`, `ItemSummary`, `ItemPage`, `ItemDetail`, `MediaVersion` and the stream types, `Page[T]`, `WriteError`, `PrincipalFrom`. OPS-2: `storage.DB`. LIB-1: `probe.Result` |
| Produces | `textfold.Title`, `textfold.Group`. `library.Grant`, `Access`, `M1Access`. `Store.Libraries`, `Facets`, `Items`, `Item`, `SetDisplay`, `BackfillDerived`. `library.API`, `NewAPI`. `library.Resolver`, `NewResolver`, `playback.Chain` |
| Start dependency | **#134 (LIB-2b), #135 (META-1a) and #130 (UI-2 PR B) merged.** Task 2 is pure and can start against #134 alone |
| Standalone test | `go -C server test -race -count=1 ./internal/textfold/ ./internal/library/... ./internal/app/ ./cmd/lisimflix/`, then `make check` |
| Module proof | Real catalogues scanned from temporary media trees with fake probe results, a real database, and `httptest` against `library.API`. Contract shapes checked with the CTR-2 fixtures harness |
| Integration proof | Task 7 plays a catalogue file end to end through `serve`. Task 8 is the actual-scale run (SCALE-01/02, PERF-05) on zraid's library and Pete's. Record "module accepted" on #77 when the PR merges, and "integrated" after Task 8 |

## Global Constraints

- Go 1.27.3. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`. Run `make check` before pushing.
- **Read-only media (SAFE-01).** Nothing here writes under the media root. File access goes through `os.Root`.
- **Contract is the source of truth.** Response shapes are CTR-2's Go types, marshalled as they are; every status flip and text change goes through `docs/contracts/openapi.json` in the same PR, and the contract tests must pass.
- **No leaks (§6):** a library or item the caller may not see is `404 not_found`, the same body as an unknown ID. `total`, `childCount` and facets count only what the caller may see. Viewers never see paths; `fileName` is admin-only and never a path.
- **Short transactions (§10.2):** reads use one read transaction per request; writes (derived columns) stay inside the existing 500-file batches.
- **Bounded responses:** `limit` 1–200, default 50. A cursor is at most 512 bytes.
- Migrations: one file, module `library`, `STRICT` tables with explicit `ON DELETE`, timestamp after META-1a's `20261028140000`. Never edit a merged migration.
- Code without a production caller yet carries `// Planned: <Feature ID> (<what will call it>).` (#60).
- Workflow: branch `feat/lib-2c-catalogue-api`, PR title `feat(library): catalogue API and catalogue playback (LIB-2c)`, labels `area:library`, `area:api`, `area:playback`. Don't request reviewers (CODEOWNERS does).

## Decisions taken in this plan (please review)

1. **LIB-2 gets a fourth slice, 2d, for the admin API.** The `// Planned: LIB-2c (the admin …)` tags on `Duplicates`, `Decide`, `RetryProbe`, `Retire` and `RetirementPreview`, and on the scheduler's `ScanNow`, `Cancel`, `Pause`, `Resume` and `Status`, become `LIB-2d`. None of those operations is in the contract yet. They need UI-6's screens and new admin contract shapes, including #104's folder and root per duplicate side. 2c is everything a viewer needs, and stays about LIB-2b's size.
2. **Access until ACC-2: the M1 viewer sees everything and is the admin.** `Grant{All bool; LibraryIDs []string; Admin bool}` is the only input the queries take about the caller. `M1Access(viewer)` gives the configured M1 account `{All: true, Admin: true}` and anyone else nothing. It is the owner who ran `init` (spec §1, "Owner"), and today `auth.Require` only ever authenticates that one account. ACC-2's evaluator replaces `M1Access` behind the same `Access` func type. Rating rules arrive with it; until then no item is hidden by rating.
3. **Title collation lives in a shared package, `textfold`.** `listItems` must fold titles exactly as META-1a's `Normalize` does (contract text, UI-2 decision 3). `library` can't import `metadata`: `metadata`'s tests import `library`, so that would be an import cycle. Task 1 moves the function to `server/internal/textfold` as `Title`, and `metadata.Normalize` becomes a one-line call to it.
   - **Contract fix:** #135 drops apostrophes with no space (`Ocean's` → `oceans`) so titles saved without them still match. The `listItems` collation text says "other punctuation becomes a space". Task 1 adds the apostrophe rule to that text so the sort, the A–Z jump and matching stay one rule.
4. **Sort keys are stored, not computed per query.**
   - `library_items` gains `sort_group` (0 for the `#` group, 1 for a–z) and `sort_key` (the folded sort title).
   - Both are written by Go whenever an item is placed, whenever `SetDisplay` changes its sort title, and by a one-time backfill at startup.
   - SQLite compares text bytewise, so folded Latin sorts a–z and other scripts sort "as written" by their UTF-8 bytes. That's what the contract asks.
   - Ties break by item ID.
5. **Display values: parsed now, META-1c later, through library's API.**
   - Items show LIB-2a's parsed title, sort title and year.
   - `library_items` gains `display_title`, `display_sort_title` and `display_year` (NULL = use the parsed value), plus a table `library_item_genres(item_id, genre)`. META-1c sets them with `Store.SetDisplay(ctx, itemID, Display)`, which also refreshes `sort_key` and `sort_group`.
   - This keeps META-1a's rule that `metadata` never writes catalogue tables (it calls library's API instead), and lets sorting and the genre filter use metadata values without a join per row.
   - Until 1c: genres are empty, `contentRating` is null, `matched` is null, and `primaryImageUrl`, `backdropUrl` and `userState` are null.
6. **Facet data per file is derived once, when a probe is stored.**
   - `library_files` gains `resolution` (`sd`/`720p`/`1080p`/`4k`, NULL until probed), `hdr` (0/1, NULL until probed) and `duration_ms`.
   - A table `library_file_audio(file_id, lang)` holds each distinct audio language, mapped to ISO 639-2/T.
   - **Resolution** is the default video stream's bucket, the larger of the width bucket and the height bucket:
     - **width:** ≥ 3200 is 4k; ≥ 1600 is 1080p; ≥ 1100 is 720p;
     - **height:** ≥ 2000 is 4k; ≥ 1000 is 1080p; ≥ 700 is 720p;
     - otherwise `sd`.
     - So a 1920×800 scope film is 1080p.
   - **HDR** means the default video stream's `probe.HDRType` isn't SDR.
   - **/B → /T language codes:**
     - `alb→sqi`, `arm→hye`, `baq→eus`, `bur→mya`, `chi→zho`
     - `cze→ces`, `dut→nld`, `fre→fra`, `geo→kat`, `ger→deu`
     - `gre→ell`, `ice→isl`, `mac→mkd`, `mao→mri`, `may→msa`
     - `per→fas`, `rum→ron`, `slo→slk`, `tib→bod`, `wel→cym`
   - Untagged and `und` aren't stored.
   - An item matches a filter if **any** of its present files matches. A show or season matches if any of its episodes does.
7. **`listItems` details the contract leaves to the producer:**
   - **What a `libraryId` listing contains:** movies for a `movies` library, shows for `tv` and `anime` (the top-level items, `parent_id IS NULL`).
   - **Seasons under a show (`parentId`):** in `idx` order with specials (`0`) last, then by ID.
   - **Episodes under a season:** in `idx` order, then `air_date`, then ID. A NULL `idx` or `air_date` sorts last.
   - **`sort` with `parentId`:** an explicit `sort` with `parentId` is `400 invalid_request` (a combination the server can't apply). Omitting it means the chosen order.
   - **`sort=added`:** `created_at` descending, then ID.
   - **`sort=year`:** the displayed year descending, NULL last, then ID.
   - **`watched`** is always `400 invalid_request` until DATA-1 is live (contract).
   - **Hidden items:** an item with no present file and no present descendant stays listed with `available: false`. §3.6 keeps offline data visible, and retirement is the only removal.
8. **Cursors are keyset, opaque and bound to the query.**
   - A cursor is base64url JSON `{"v":1,"q":<first 8 bytes of SHA-256 of the canonical query>,"k":[<sort values>,<id>]}`.
   - It's refused with `400 invalid_request` if it doesn't decode, its version or query hash differs, or it's longer than 512 bytes.
   - Keyset paging means a scan adding items between pages never repeats or skips the items already passed (Review Focus 5).
   - `total` is computed on the first page only, as the contract requires.
9. **Availability (§3.6):**
   - **A file is available** when it's `present` and its root's state isn't `offline`.
   - **`ItemSummary.available`:**
     - for a movie or episode, any of its versions is available;
     - for shows and seasons, it is `null` (contract).
   - **`Library.availability`:**
     - `available` when no root is `offline`;
     - `offline` when every root is;
     - `partial` otherwise.
     - `degraded` reads as available to viewers (UI-2 decision 5).
   - **`Library.scan`:**
     - `scanning`, with the running run's `seen`, while the library's **first** whole-root scan runs (no root has `last_ok_at` yet);
     - `idle` otherwise, with `filesSeen` 0;
     - `filesTotal` is always null, because the walk doesn't count ahead.
10. **`getItem` versions.**
    - **Order:** available first, then unlabelled, then by resolution (4k first), then by version ID. The first one is the default (it sets `durationMs` on the summary).
    - **Streams:** mapped from the stored `probe.Result`. `language` is null for untagged or `und`. `hdr` maps `""→sdr`, `HDR10→hdr10`, `HDR10+→hdr10plus`, `HLG→hlg` and `DV→dolbyVision`. A subtitle's `kind` is `image` for `hdmv_pgs_subtitle`, `dvd_subtitle` and `dvb_subtitle`, `ass` for `ass`/`ssa`, and `text` otherwise.
    - **Other fields:**
      - `segment` is null (LIB-3 locates shared-file episodes);
      - `container` is the stored container, or `""` before the first probe;
      - `fileName` is the path's last element for admins, and null for everyone else.
    - **When a version is listed:** shows and seasons have no versions. A movie or episode lists every version, available or not. `MediaVersion.available` and `ItemSummary.available` carry the state; the contract's "Empty … for a movie or episode with no available file" is read as "no versions to play", and the client shows File offline from `available`.
    - **`externalIds`** stays empty until META-1c: name hints aren't validated, so they aren't served as IDs.
11. **Catalogue playback, and keeping M1 playing.**
    - **What `library.Resolver` resolves:** `Resolve(p, itemID, versionID)` returns the version's file as a `playback.Source`. The fields are:
      - `RelativePath` = root path + `/` + file path, relative to the media root;
      - `FileID` = the file ID;
      - `SourceRevision` = `contracts.SourceRevision(fileID, size, mtime_ns)`.
    - **Refusals:** it refuses with `ErrNotFound` when:
      - the item isn't in a granted library;
      - the version isn't the item's;
      - the file isn't present;
      - the root is offline.
    - **It is one indexed query.** That answers #131 note 1: the per-range-request `Resolve` costs one primary-key join, which Task 8 measures under load.
    - **The M1 movie keeps playing.** `playback.Chain(a, b)` tries `a`, and on `ErrNotFound` tries `b`. Serve uses `Chain(catalogue, Single)`, so `M1App`, which still calls `start(movie.id, movie.id)`, keeps playing the configured M1 movie until UI-2 PR C ships the browse app.
    - **Retiring M1 waits for PR C.** Removing `Single`, `GET /api/v1/movies`, `Movie` and `M1App` is the follow-up UI-2's plan assigns to "LIB-2c's PR once items are served". It needs PR C merged, so it's its own small PR with this plan's issue as owner, opened when PR C merges.
12. **Serve wiring.**
    - `app.New` takes the catalogue handler next to the playback one and mounts `/api/v1/libraries` and `/api/v1/items` behind `am.Require`.
    - Serve builds `library.NewAPI(store, library.M1Access(cfg.Viewer))` when storage started.
    - **When storage is down:** the routes answer `503` through the existing readiness path. They are not registered, so the browse app's "404 → M1" fallback (UI-2 decision 10) still works.
    - `Store.BackfillDerived` runs once before the scheduler starts, in batches of 500.

## Review Focus

1. **A disk is unplugged while someone browses:**
   - its items stay listed, show File offline, and Play doesn't start a session;
   - the library reads `partial`;
   - nothing errors.
   
   Tests: `TestOfflineRootItemsStayListedUnavailable`, `TestLibraryAvailabilityPartialAndOffline`, `TestResolverRefusesOfflineRoot`.
2. **A cursor that's edited, truncated, or reused with different filters** is a 400, never a 500 and never another query's page → `TestCursorRejectedWhenTamperedOrReused`.
3. **Probing for something hidden:**
   - an unknown library or item, and one the caller may not see, return byte-identical 404s;
   - `total`, `childCount` and facets don't count hidden items.
   
   Tests: `TestHiddenLibraryIs404LikeUnknown`, `TestCountsOnlyCountGrantedItems`.
4. **Titles that don't start with a–z** (numbers, symbols, Japanese, `Élite`, `The Matrix`):
   - they land in `#` or under the right letter;
   - `from=#` is the first page;
   - `from=Z` on a library with nothing after Z returns an empty page with a null cursor.
   
   Tests: `TestTitleSortGroupsAndJump`, `TestJumpPastTheEnd`.
5. **A scan adds items between two pages:** paging on doesn't repeat or skip items already passed → `TestCursorStableWhileScanAdds`.

---

### Task 1: `textfold`, and the collation text

**Files:**
- Create: `server/internal/textfold/fold.go`, `server/internal/textfold/fold_test.go`
- Modify: `server/internal/metadata/score.go` (`Normalize` calls `textfold.Title`; its fold table and helpers move), `server/internal/metadata/score_test.go` (`TestNormalize` moves to `textfold`, a one-case test stays to pin the delegation), `docs/contracts/openapi.json` (`listItems` collation text)

**Interfaces:**
- Consumes: META-1a's `Normalize` body, as merged in #135.
- Produces: `func Title(s string) string` (exactly META-1a's `Normalize`). `func Group(folded string) int`, which returns `1` when `folded` starts with an ASCII letter a–z, and `0` otherwise (the `#` group, which sorts first).

- [ ] **Step 1:** Move `TestNormalize`'s cases into `fold_test.go` as `TestTitle`, unchanged, and add `TestGroup`:

```go
func TestGroup(t *testing.T) {
	for in, want := range map[string]int{"matrix": 1, "elite": 1, "2001 a space odyssey": 0, "": 0, "ガ": 0, "#hashtag": 0} {
		if got := Group(in); got != want {
			t.Errorf("Group(%q) = %d, want %d", in, got, want)
		}
	}
}
```

- [ ] **Step 2:** Run `go -C server test ./internal/textfold/`. Expected: FAIL to compile (`Title`, `Group` undefined).
- [ ] **Step 3:** Move the code, so `metadata.Normalize(s)` is `return textfold.Title(s)`. In `openapi.json`'s `listItems` description, replace "other punctuation becomes a space and spaces collapse" with "apostrophes (', ’, ʼ) are dropped; other punctuation becomes a space and spaces collapse". Regenerate `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`).
- [ ] **Step 4:** Run `go -C server test ./internal/textfold/ ./internal/metadata/` and `npm --prefix web run test:contracts`. Expected: PASS.
- [ ] **Step 5:** Commit `refactor(textfold): one title fold for matching and catalogue sort (LIB-2c)`.

### Task 2: Probe results to contract streams and file facts (pure)

**Files:**
- Create: `server/internal/library/streams.go`, `server/internal/library/streams_test.go`

**Interfaces:**
- Consumes: `probe.Result`; CTR-2's `VideoStream`, `AudioStream`, `SubtitleStream`.
- Produces: `type fileFacts struct{ Resolution string; HDR bool; DurationMs int64; AudioLangs []string }`; `func deriveFile(r probe.Result) fileFacts`; `func streams(r probe.Result) (video []contracts.VideoStream, audio []contracts.AudioStream, subs []contracts.SubtitleStream)`; `func langT(tag string) (string, bool)` (lower-case, /B→/T per Decision 6, false for "" and "und" and anything not `^[a-z]{3}$`).

- [ ] **Step 1: Tests** (table-driven, with Decision 6 and Decision 10's exact values):

```go
func TestResolutionBuckets(t *testing.T) // 3840x2160→4k, 3840x1600→4k, 1920x1080→1080p, 1920x800→1080p, 1440x1080→1080p, 1280x720→720p, 1280x536→720p, 720x576→sd, 640x480→sd, no video→""
func TestDefaultVideoStreamDecides(t *testing.T) // two video streams, the second Default → its size and HDR win; none Default → the first
func TestLangT(t *testing.T)            // ger→deu, fre→fra, chi→zho, ENG→eng, und→false, ""→false, "english"→false
func TestStreamsMap(t *testing.T)       // HDR10+→hdr10plus, DV→dolbyVision, ""→sdr; audio Language "und"→nil; pgs→image, ass→ass, subrip→text; Profile ""→nil
```

- [ ] **Step 2:** Run them. Expected: FAIL (undefined).
- [ ] **Step 3:** Implement. `AudioLangs` is sorted and has no duplicates.
- [ ] **Step 4:** Run them. Expected: PASS.
- [ ] **Step 5:** Commit `feat(library): map probe results to contract streams and file facts (LIB-2c)`.

### Task 3: Derived columns, display values and backfill

**Files:**
- Create: `server/internal/library/migrations/20261029120000_library_browse.sql`, `server/internal/library/derive.go`, `server/internal/library/derive_test.go`
- Modify: `server/internal/library/scan.go` (`item` writes `sort_group`/`sort_key`), `server/internal/library/probes.go` (`record` writes the file's derived columns in the same `UPDATE`), `server/internal/library/store.go`

**Interfaces:**
- Consumes: Task 1's `textfold.Title`, `Group`. Task 2's `deriveFile(probe.Result) fileFacts`.
- Produces:
  - **Migration:**
    - `library_items` adds `sort_group INTEGER`, `sort_key TEXT`, `display_title TEXT`, `display_sort_title TEXT` and `display_year INTEGER`.
    - `library_files` adds `resolution TEXT CHECK (resolution IN ('sd','720p','1080p','4k'))`, `hdr INTEGER CHECK (hdr IN (0,1))` and `duration_ms INTEGER`.
    - New table `library_item_genres(item_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE, genre TEXT NOT NULL, PRIMARY KEY (item_id, genre)) STRICT`.
    - New table `library_file_audio(file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE CASCADE, lang TEXT NOT NULL, PRIMARY KEY (file_id, lang)) STRICT`.
    - Partial index `library_items_browse ON library_items(library_id, sort_group, sort_key, id) WHERE parent_id IS NULL`.
    - Indexes `library_items_added ON library_items(library_id, created_at DESC, id) WHERE parent_id IS NULL` and `library_items_children ON library_items(parent_id, idx, id)`.
  - `type Display struct{ Title, SortTitle *string; Year *int; Genres []string }` (nil leaves a value as it is; an empty non-nil `Genres` clears them). `func (s *Store) SetDisplay(ctx context.Context, itemID string, d Display) error` (`// Planned: META-1c (applies matched titles, years and genres).`). It returns `ErrNotFound` for an unknown item.
  - `func (s *Store) BackfillDerived(ctx context.Context) (items, files int, err error)`: fills NULL `sort_key` and probed files with NULL `resolution`, 500 rows per transaction.

- [ ] **Step 1: Tests**

```go
func TestPlacedItemsGetSortKeys(t *testing.T)        // scan "The Matrix (1999)", "Élite (2018)", "2001 A Space Odyssey (1968)" → sort_key matrix/elite/2001 a space odyssey; sort_group 1/1/0
func TestStoredProbeFillsFileFacts(t *testing.T)      // ProbeDue with a scripted 1920x800 HDR10 result, audio eng+ger+und → resolution 1080p, hdr 1, duration_ms set, audio {deu, eng}
func TestSetDisplayRefreshesSortKey(t *testing.T)     // SetDisplay(SortTitle "Zzz") moves the item to sort_key "zzz"; Genres ["Drama","Crime"] stored; Genres [] clears; unknown item → ErrNotFound
func TestBackfillFillsOldRows(t *testing.T)           // NULL the derived columns by SQL, BackfillDerived → same values as a fresh scan and probe; counts returned
```

- [ ] **Step 2:** Run `go -C server test -run 'SortKeys|FileFacts|SetDisplay|Backfill' ./internal/library/`. Expected: FAIL.
- [ ] **Step 3:** Implement. `sort_key` is `textfold.Title(COALESCE(display_sort_title, sort_title))`, computed in Go. The parsed `sort_title` stays as LIB-2a wrote it.
- [ ] **Step 4:** Run `go -C server test -race ./internal/library/...`. Expected: PASS, including LIB-2a/2b's tests.
- [ ] **Step 5:** Commit `feat(library): sort keys, display values and file facts for browsing (LIB-2c)`.

### Task 4: Access, libraries and facets

**Files:**
- Create: `server/internal/library/access.go`, `server/internal/library/browse.go`, `server/internal/library/browse_test.go`, `server/internal/library/cursor.go`, `server/internal/library/cursor_test.go`

**Interfaces:**
- Consumes: Task 3's columns; LIB-2a's `Store`; `m1config.Viewer`.
- Produces:
  - `type Grant struct{ All bool; LibraryIDs []string; Admin bool }`; `type Access func(contracts.Principal) Grant`; `func M1Access(v m1config.Viewer) Access` (Decision 2; `// shortcut: one M1 viewer until ACC-2 replaces Access`).
  - `func (s *Store) Libraries(ctx context.Context, g Grant, cursor string, limit int) (contracts.LibraryList, error)`: ordered by name then ID.
  - `func (s *Store) Facets(ctx context.Context, g Grant, libraryID string) (contracts.LibraryFacets, error)`: `ErrNotFound` for an unknown or ungranted library.
  - `var ErrBadRequest = errors.New("library: bad request")`, wrapped with a message the API sends as `invalid_request`.
  - `func encodeCursor(q string, keys ...any) string`, `func decodeCursor(c, q string) ([]any, error)` (Decision 8; Task 5 reuses them).
  - Test helpers in `browse_test.go`: `newBrowse(t) *benv` (store, media, scanner with a scripted prober, `scan`, `probe`, `grantAll`, `grantNone`).

- [ ] **Step 1: Tests**

```go
func TestLibrariesListsGrantedOnly(t *testing.T)            // two libraries; Grant{LibraryIDs: [A]} lists only A; Grant{} lists none; Grant{All} both, by name
func TestLibraryAvailabilityPartialAndOffline(t *testing.T) // two roots: both up → available; one root's state offline → partial; both → offline; degraded → available
func TestLibraryScanProgress(t *testing.T)                  // first scan running (no last_ok_at, run running with seen 120) → {scanning, 120, null}; after a completed scan → {idle, 0, null}
func TestFacetsFromPresentFiles(t *testing.T)               // 4k HDR deu + 1080p SDR eng + a missing 720p file → resolutions [4k 1080p], hdr true, sdr true, audioLanguages [deu eng], years {min,max} of items; genres [] until SetDisplay
func TestHiddenLibraryIs404LikeUnknown(t *testing.T)        // Facets on an ungranted and on an unknown library → both ErrNotFound
```

- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3:** Implement. `cursor_test.go` pins the encoding: a round trip returns the keys; a different query hash, version or a cursor over 512 bytes is `ErrBadRequest`.
- [ ] **Step 4:** Run them. Expected: PASS.
- [ ] **Step 5:** Commit `feat(library): libraries and facets for granted viewers (LIB-2c)`.

### Task 5: `listItems`

**Files:**
- Modify: `server/internal/library/browse.go`, `server/internal/library/browse_test.go`

**Interfaces:**
- Consumes: Tasks 3 and 4.
- Produces: `type ItemQuery struct{ LibraryID, ParentID, Sort, From, Genre, Resolution, AudioLanguage string; YearFrom, YearTo *int; HDR, Watched *bool; Cursor string; Limit int; SortGiven bool }`; `func (s *Store) Items(ctx context.Context, g Grant, q ItemQuery) (contracts.ItemPage, error)`.

- [ ] **Step 1: Tests** (each 400 case asserts `errors.Is(err, ErrBadRequest)`):

```go
func TestItemsNeedsExactlyOneScope(t *testing.T)          // neither or both of libraryId/parentId → 400
func TestItemsRejectsInapplicable(t *testing.T)           // from with sort=added → 400; genre with parentId → 400; sort given with parentId → 400; yearFrom 2001 yearTo 2000 → 400; watched=true → 400
func TestTitleSortGroupsAndJump(t *testing.T)             // "2001…", "ガンダム", "Élite", "The Matrix", "Alien", "Zodiac": sort=title → 2001…, ガンダム (# group), Alien, Élite, The Matrix, Zodiac; from=# starts at 2001…; from=E starts at Élite; from=M at The Matrix
func TestJumpPastTheEnd(t *testing.T)                     // from=Z with nothing ≥ z → items [], nextCursor null, total 0
func TestSortAddedAndYear(t *testing.T)                   // added: newest created_at first, ties by ID; year: newest first, NULL last
func TestFiltersMatchAnyPresentFile(t *testing.T)         // resolution=4k, hdr=true/false, audioLanguage=deu, yearFrom/yearTo, genre (after SetDisplay) each select the right movies; a show matches through any episode
func TestChildrenInChosenOrder(t *testing.T)              // parentId=show → seasons 1,2,0 (specials last); parentId=season → episodes by idx, NULL idx last; seasons/episodes carry showTitle, seasonIndex, childCount
func TestPagingAndTotal(t *testing.T)                     // 120 movies, limit 50 → 50/50/20, total 120 on the first page only, nextCursor null on the last
func TestCursorRejectedWhenTamperedOrReused(t *testing.T) // garbage, truncated, other version, and a cursor reused with a different genre filter → 400
func TestCursorStableWhileScanAdds(t *testing.T)          // read page 1 of sort=title, scan in new titles before and after the cursor, read the rest → no item twice, every original item once
func TestCountsOnlyCountGrantedItems(t *testing.T)        // parentId of a show in an ungranted library → ErrNotFound; total and childCount never include another library's items
func TestOfflineRootItemsStayListedUnavailable(t *testing.T) // root offline → movies still listed, available false; episodes too; shows' available null
```

- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3:** Implement as one SQL query per page built from `ItemQuery`. The `WHERE` clause uses only bound parameters, and filters are `EXISTS` subqueries over versions → files (→ `library_file_audio`, `library_item_genres`). Shows and seasons reach files through descendant episodes, via the `parent_id` chain (two levels at most). `total` is a `COUNT(*)` of the same query, run only when `Cursor == ""`. The canonical query string for the cursor hash covers every field except `Cursor` and `Limit`.
- [ ] **Step 4:** Run `go -C server test -race ./internal/library/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(library): listItems with collation, A–Z jump, filters and keyset paging (LIB-2c)`.

### Task 6: `getItem`, and the HTTP API

**Files:**
- Create: `server/internal/library/detail.go`, `server/internal/library/detail_test.go`, `server/internal/library/api.go`, `server/internal/library/api_test.go`

**Interfaces:**
- Consumes: Tasks 2–5; `contracts.WriteError`, `PrincipalFrom`, `Code*`. Scan progress comes from the run table, not the scheduler (status is 2d).
- Produces:
  - `func (s *Store) Item(ctx context.Context, g Grant, itemID string) (contracts.ItemDetail, error)`.
  - `type API struct` with an unexported store and access; `func NewAPI(st *Store, access Access) *API` (`// Planned: LIB-2c (serve mounts it)` until Task 7); `func (a *API) Handler() http.Handler`. Routes: `GET /api/v1/libraries`, `GET /api/v1/libraries/{libraryId}/facets`, `GET /api/v1/items`, `GET /api/v1/items/{itemId}`.
  - Errors map as follows:
    - `ErrBadRequest` → `invalid_request` (400);
    - `ErrNotFound` → `not_found` (404);
    - a missing principal → `unauthenticated` (401);
    - anything else → `internal_error` (500), logged with the request ID.
  - Query parsing follows the contract's parameter schemas; a malformed value (`limit=0`, `yearFrom=abc`, `from=AB`, `resolution=8k`) is 400.

- [ ] **Step 1: Tests**

```go
func TestItemDetailVersionsAndStreams(t *testing.T)    // a movie with two files (4k labelled "Director's Cut", 1080p unlabelled) → versions [1080p, 4k cut] per Decision 10; streams mapped; sourceRevision = contracts.SourceRevision(fileId, size, mtime); summary.durationMs from the default
func TestFileNameOnlyForAdmins(t *testing.T)           // Grant{All, Admin} → fileName = base name, never containing "/"; Grant{All} → null
func TestShowDetailHasNoVersions(t *testing.T)         // show and season → versions [], available null
func TestAPIShapesMatchContract(t *testing.T)          // httptest GET on each route with a scanned fixture; each body decodes with DisallowUnknownFields into its CTR-2 Go type and re-encodes byte-identically (the contracts package's round-trip rule); Content-Type application/json
func TestAPIErrors(t *testing.T)                       // no principal → 401; bad params → 400 invalid_request; unknown and hidden item → identical 404 bodies apart from requestId
func TestAPIFirstPageIsFast(t *testing.T)              // 5,000 movies (2,000 with two files) and 200 shows × 3 seasons × 10 episodes: each route's p95 over 50 calls < 50 ms on the test machine (module bound for PERF-05; the real target is Task 8)
```

- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3:** Implement. Each request reads in one read transaction.
- [ ] **Step 4:** Run `go -C server test -race ./internal/library/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(library): getItem and the catalogue HTTP API (LIB-2c)`.

### Task 7: Catalogue playback and serve wiring

**Files:**
- Create: `server/internal/library/resolver.go`, `server/internal/library/resolver_test.go`
- Modify: `server/internal/playback/source.go` (+ `source_test.go`: `Chain`), `server/internal/app/app.go` (+ `app_test.go`), `server/cmd/lisimflix/serve.go` (+ `integration_test.go`), `docs/contracts/openapi.json` (four statuses → `implemented`), `README.md`, and the `Planned:` tags of Decision 1

**Interfaces:**
- Consumes: PR B's `playback.Resolver`, `Source`, `ErrNotFound`, `Single`; Tasks 4–6.
- Produces:
  - `type Resolver struct` and `func NewResolver(st *Store, access Access) *Resolver`, implementing `playback.Resolver` (Decision 11).
  - `func Chain(rs ...Resolver) Resolver` in `playback` (first non-`ErrNotFound` answer wins; any other error is returned at once).
  - `app.New(cfg, am, playback, catalogue http.Handler, web, ready, log)`.

- [ ] **Step 1: Tests**

```go
func TestResolverResolvesCatalogueFile(t *testing.T)   // scanned movie → Source{ItemID, VersionID, FileID, RelativePath "Movies/A (2001)/A (2001).mkv", SourceRevision matching the file}
func TestResolverRefusesOfflineRoot(t *testing.T)      // root offline, file missing, wrong item for the version, ungranted library → ErrNotFound each
func TestChainFallsThrough(t *testing.T)               // Chain(a returns ErrNotFound, b resolves) → b's Source; a returns another error → that error, b not called
func TestCatalogueRoutesMounted(t *testing.T)          // app: /api/v1/libraries and /api/v1/items need a session (401 without); with nil catalogue handler → 404 not_found (UI-2's M1 fallback)
func TestServePlaysACatalogueFile(t *testing.T)        // integration (tagged like the M1 test): init, library add, wait for the scan, listItems → movie, getItem → version, createPlaybackSession {itemId, versionId}, GET content with Range → 206 with the file's bytes; the M1 movie still plays via /movies
```

- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3:** Implement and wire, following Decision 12:
  - **Serve:** run `BackfillDerived` before the scheduler starts, and construct the playback server with `Resolver: playback.Chain(library.NewResolver(st, access), single)`.
  - **Contract:** flip the four operations' `x-lisimflix.status` to `implemented`.
  - **`Planned:` tags:** retag the admin ones to `LIB-2d` per Decision 1, and remove the `Planned: LIB-2c (a catalogue Resolver replaces Single)` tag from `Single`, replacing it with `// Planned: removal after UI-2 PR C (#113): the catalogue serves every item.`
  - **README:** the Status line becomes "Libraries are scanned and served through the catalogue API; the browse app (UI-2) follows."
- [ ] **Step 4:** Run `make check`, plus the tagged integration test (`go -C server test -tags integration ./cmd/lisimflix/`). Expected: PASS.
- [ ] **Step 5:** Commit `feat(library): play catalogue files; serve the catalogue API (LIB-2c)`, then open the PR (Global Constraints workflow). The body names the M1 retirement follow-up (Decision 11) and Task 8.

### Task 8: Actual-scale qualification (after the PR merges)

No new code in this task, apart from fixes the run finds, which go in their own PRs.

- **Libraries:** zraid's library (Zac) and Pete's (about 5,000 movies and 1,000 shows), on the merged `main` image, each with its media mounted read-only.
- **SCALE-01:**
  - record the first full scan's elapsed time, files, probes, failures and peak queue;
  - repeat a scan and confirm unchanged files aren't re-probed (LIB-2b's counters).
- **SCALE-02 / PERF-05:** time each catalogue route at idle, during a full rescan and during a probe turn: first page by title, `from=M`, deep paging to the last page, each filter, `getItem` on a 3-version movie, and a 30-season show's seasons. Report server time (`http.request` logs) apart from browser time once UI-2 PR C lands. The target is p95 ≤ 1 s end to end; record query plans (`EXPLAIN QUERY PLAN`) for any route over 100 ms server-side.
- **#131 note 1:** measure `Resolve` per range request during a 4k direct play with seeking. If its share of request time is above 5%, cache per session for its idle timeout.
- **Write-up:** record the results on #77 and in `docs/testing/` as an evidence file, and mark #77 "integrated".

## Next slice: LIB-2d (outline, its own plan)

- **Admin contract additions, with UI-6:**
  - scan status per root, from `Scheduler.Status`: queued, scanning, probing, paused, offline, degraded, with reasons and the last run's counters;
  - duplicates, with each side's folders and root (#104), plus `Decide`;
  - the retirement preview and confirm;
  - probe errors with retry;
  - scan now, cancel, pause and resume.
- **Admin access:** these routes need `Grant.Admin`.
- **Remove the `Planned: LIB-2d` tags** as the routes land.

## Self-review

**Spec coverage:**

| Requirement | Where |
|---|---|
| CTR-2 `listLibraries`, `getLibraryFacets`, `listItems`, `getItem` with their contract rules | Tasks 4–6 |
| Title collation, `#` group, A–Z jump (contract, UI-2 decision 3) | Tasks 1, 3, 5 |
| Filters, `total` on the first page, 400 rules | Task 5 |
| §6 no leaks: 404 like unknown, counts only granted items, no paths, `fileName` admin-only | Tasks 4–6 |
| §3.6 offline roots: listed, unavailable, library `partial`/`offline` | Tasks 4, 5, 7 |
| §3.1 versions per item, shared-file episodes keep one `fileId` | Task 6 (segment null until LIB-3) |
| Catalogue playback (UI-2 PR B obligation) | Task 7 |
| M1 keeps working until UI-2 PR C | Decision 11, Task 7 |
| SCALE-01/02, PERF-05 | Task 6 (module bound), Task 8 (actual scale) |
| #131 note 1 (Resolve per range request) | Decision 11, Task 8 |
| #104 (duplicate sides) | LIB-2d (Decision 1) |

**Not verified while writing this plan:** unlike LIB-2a, LIB-2b and META-1a, there's no prototype behind this plan yet. The interfaces were checked against `main`, #134 (LIB-2b), #135 (META-1a) and #130 (PR B) on 2026-10-29, but the SQL and the module timing bound in Task 6 haven't been run. Review the Decisions; the first implementer should expect the timing bound to be tuned once measured.
