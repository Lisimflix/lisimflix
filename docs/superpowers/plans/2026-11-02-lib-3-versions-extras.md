# LIB-3 Versions and Extras Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a movie's cuts and qualities stable, choosable versions under one item; attach Plex-style extras to their movie or show; and give each episode of a shared file its own timeline range when the file itself says where it is.

**Architecture:** Everything stays in `server/internal/library` (naming, store, scan placement) plus one additive contract change.
- **Versions:** `library_versions` already links an item to a file. LIB-3 makes a version's identity outlive a file replacement (a Sonarr/Radarr upgrade or rename) through a **post-walk adoption pass**, numbers same-label copies instead of hiding them, and flags them for the admin.
- **Extras:** a new `library_extras` table hangs each extra file off its parent movie or show. Extras are not catalogue items.
- **Segments:** computed on read from the stored probe's chapter markers, never stored, never guessed.

**Tech Stack:** Go 1.27.3 standard library, OPS-2 `storage`, LIB-1 `probe.Result`, the CTR-2 OpenAPI contract. No new dependency.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §3.1: versions/cuts grouped under one item with a "Play version" picker; extras from Plex-style folders.
- §3.8: no per-episode watched state from assumed equal-length episodes.
- §3.6 (via the LIB-2a table): moves keep identity where evidence exists, ambiguous cases go to review, no guessing from size.
- §9.4: the LIB-3 row (P1 / G2, `area:media`).
- §10.1: "version/cut maps to logical items".

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-3** (versions and extras, P1 / G2) |
| Issue / labels | #160 / `area:media`, `area:library`. Record **"module accepted"** and **"integrated"** separately on #160 |
| Branch | `feat/lib-3-versions-extras` (this plan's own docs PR uses `docs/lib-3-plan`) |
| Owner / reviewers | Pete (claimant) / reviewers per `.github/CODEOWNERS` |
| Acceptance | Spec row: stable logical identities, version choice, supplemental media, **version/cut reconciliation fixtures** (Task 6) |
| File scope | `server/internal/library/{scan.go, duplicates.go}`, new `library/{extras.go, versions.go, segments.go}`, `library/naming/{classify.go, names.go, extras.go}`, one new `library/migrations/*.sql`, `server/internal/contracts/catalogue.go`, `docs/contracts/openapi.json`, `testdata/contracts/catalogue/`, `web/src/api/schema.d.ts` (generated), `web/tests/fake-api.ts` (one literal), `web/contract-tests/fixtures.test.mjs` |
| Consumes | `library_files`, `library_versions`, `library_folders`, `naming.Entry.Version`, `probe.Result.Chapters`, `contracts.MediaVersion`, `contracts.Segment` |
| Produces | `Store.VersionRows`, `Store.ExtraRows`, `Store.VersionFlags` / `KeepVersions` / `RelabelVersion`, `contracts.Extra`, `ItemDetail.extras`, the reconciliation fixtures |
| Start dependency | None open: LIB-2a/2b merged, LIB-2c plan merged, CTR-2 merged |
| Module dependency | LIB-2c builds the catalogue handler and `Resolver`; LIB-2d builds the admin API. LIB-3's reads and admin methods carry `// Planned: LIB-2c (getItem handler)` and `// Planned: LIB-2d (…)` |
| OpenAPI integration owner | Pete, for LIB-3's one additive `extras` change; agree the `openapi.json` edit order with LIB-2c's owner first (CONTRIBUTING, Working together: one named owner per shared edit) |
| Standalone test | `make check` plus the Task 6 fixtures |
| Integration proof | When LIB-2c's `getItem` serves it: tests show `extras`, a shared-file episode with a `segment`, and a numbered same-label version. UI-2's picker shows them. Version flags join the #104 duplicates view in LIB-2d |

**Overlap with open work (checked 2026-11-02):**
- **`scan.go`:** #153 (Lammy, LIB-2b follow-ups) and LIB-2c (#77) also edit it. LIB-3 touches `place`, `version`, `applyFiles`' skip rule, and adds one `adopt` call beside `settle` inside `Scan` (`:232`); the run loop's flow is unchanged. Land after #153 and rebase.
- **`scheduler.go`, `cmd/lisimflix/library.go`:** #153 and LIB-4 (#159) edit them. LIB-3 touches neither.
- **`store.go`, `openapi.json`:** LIB-2c edits both; LIB-3 touches only `openapi.json` (Task 5). See the integration owner row.
- **META-1 (#73):** reads `library_versions`, writes none. Task 2 adds columns (`ordinal`, `fresh`) it must ignore.
- **#146** edits `web/tests/fake-api.ts` on other lines than Task 5's one literal.
- **`library/detail.go` (LIB-2c):** LIB-2c's plan has its own `Store.Item` build versions (decision 10) and never mentions `VersionRows`/`ExtraRows`. Left alone, that is a second read path. See Decision 5.

## Decisions

Decided by Pete on 2026-11-02 (issue #160), "all recommended":

1. **Segments (Task 4): DECIDED.** A range only when the chapter count equals the episode count, the chapters tile the file from 0 to its duration, and every chapter lasts at least 10 minutes; otherwise `segment` stays null and §3.8's explicit watched actions apply. Chapter-count equality plus tiling is weak evidence on its own, so revisit with real files.
2. **Rename adoption (Task 3): DECIDED, label match only.** A newly placed version on a present file takes over an existing version whose file is missing or retired when the item and label match (case-insensitive) and the pairing is one-to-one (exactly one candidate each way; anything else is left for review). This keeps the version ID and resume across a Radarr upgrade. **Accepted consequence:** a label-changing upgrade (720p → 1080p) creates a new version and leaves the old one as a dead entry in the picker indefinitely (retirement keeps versions, and LIB-2c lists unavailable ones); the admin can relabel. **Residual risk:** a `fresh` version stays eligible until the next whole-root scan (every 6 h by default; with `FullScanEvery` off, the next startup, manual, recheck, overflow or coalesced scan). If the **older** of two same-label copies is deleted inside that window, its version ID moves onto the newer file and the newer version's resume is lost. The ordinal rule prevents the reverse (an older live copy absorbed into a missing newer one), and the one-to-one rule leaves ambiguous cases for review.
3. **Season-folder extras: DECIDED.** `Show/Season 01/Featurettes/x.mkv` attaches to the show, not the season.
4. **Playing an extra: DECIDED, option (a).** The extra's own ID is the playback `itemId` (`Extra.id == Extra.version.id`), LIB-2c's `Resolver` resolves extra IDs, and DATA-1 (#154) ignores progress for IDs that are not catalogue items. This avoids the parent being credited with an extra's progress (a finished trailer marking the movie watched or overwriting its resume) and needs no playback contract change. DATA-1's plan needs a line for it.

Still open:

5. **Who wires LIB-2c's `Store.Item` to `VersionRows`/`ExtraRows`, and who adds the extras branch to LIB-2c's `Resolver`** (its plan refuses a version that isn't the item's and has no extras lookup; decision 4 needs one). Or whether LIB-2c's decision 10 builds versions itself and LIB-3 only supplies segments, labels and extras. Owner **Pete and Lammy, agreed on #77 before any LIB-3 code**.

## Global Constraints

- Go **1.27.3**, Node **24.22.1**; no new dependency (plans map delta 6).
- IDs are opaque strings in public JSON; **a path never appears in a public shape**; `fileName` is a bare name, admins only (spec §10.1, §12).
- JSON is camelCase; a Go nil slice encodes as `[]` (CTR-2 `nonNil`).
- A scan transaction holds at most **500 files** (spec §10.2); broken files are listed, **never abort a scan** (§3.1).
- Migrations: `server/internal/library/migrations/YYYYMMDDHHMMSS_library_<what>.sql`, registered through `Migrations()`. Pick a timestamp after the newest migration on `main` when you create the file (LIB-2c adds `20261029140000`).
- Times are UTC milliseconds; probe results are read from `library_files.probe_json` and must match the file's revision.
- `MediaVersion.label` is at most **100** characters including any " (n)" suffix.
- Version ordering and the default version are LIB-2c's decision 10. **Do not change them.**
- Never assume equal-length episodes (§3.8).

## Review Focus

1. **A Sonarr/Radarr upgrade replaces the file under a different name.** The version ID stays, so resume and history survive, whichever order the scan or a watcher scope visits the files. (Task 3 `TestUpgradedFileKeepsVersionID`, `TestScopedScanKeepsFreshUntilFullScan`)
2. **Two files of one movie get the same label.** Both stay playable and numbered, the admin is flagged, and a later rename or folder merge still reconciles. (Task 3 `TestSameLabelKeepsBothAndFlags`, `TestRenamedSecondCopyReconciles`, `TestDecideMergeRenumbersSameLabel`)
3. **A shared-file episode must never get an equal split.** No chapters, a count mismatch, a gap, a short file or tiny chapters all give `null`. (Task 4 `TestSegmentOf`)
4. **An extras folder is scanned before (or without) its movie, or an original file returns after its replacement was adopted.** Nothing is lost or invisible, and no phantom movie appears. (Task 2 `TestExtraBeforeParentIsAdopted`, `TestExtraWithoutParentStaysUnplaced`; Task 3 `TestReturnedOriginalIsPlacedAgain`)
5. **An admin merges two folders or a disk goes offline.** Extras and flags move with the merge; offline extras report `available: false`. (Task 2 `TestFoldCarriesExtras`; Task 5 fixture)

---

### Task 1: Naming — extra types and canonical cut labels

**Files:**
- Create: `server/internal/library/naming/extras.go`
- Modify: `server/internal/library/naming/classify.go:28,60-108` (`ErrExtra` text, `Classify`, `extraFolders`, `isExtraName`), `naming/names.go:106-125` (`versionLabel`)
- Test: `server/internal/library/naming/classify_test.go`, new `extras_test.go`

**Interfaces:**
- Produces:
  ```go
  type ExtraType string // "trailer" "featurette" "behindTheScenes" "deletedScene" "interview" "scene" "short" "other"
  type ExtraEntry struct {
      Folder string    // the folder the extra belongs to, inside the root: everything before the extras folder, or the file's own folder for "-trailer" names; "" when there is none
      Type   ExtraType
      Title  string
  }
  func Extra(rel string) (ExtraEntry, bool) // false for ordinary media
  ```
- `Classify` keeps returning `ErrExtra` for these files (callers branch on it). `Extra` is the only new entry point.

- [ ] **Step 1: Write failing tests.** `TestExtra` table (rel → `ExtraEntry`, ok):
  - `Heat (1995)/Featurettes/Making Of.mkv` → `{Heat (1995), featurette, "Making Of"}`
  - `Heat (1995)/behind the scenes/Stunts.mkv` → `behindTheScenes` (case-insensitive folder)
  - `Show (2019)/Season 01/Deleted Scenes/Cut.mkv` → `{Show (2019)/Season 01, deletedScene, "Cut"}`
  - `Heat (1995)/Heat (1995)-trailer.mkv` → `{Heat (1995), trailer, "Trailer"}` (the stem before the suffix equals the folder name, so the title is the type's name)
  - `Heat (1995)/Teaser-trailer.mkv` → title `"Teaser"`
  - `Heat (1995)/Extras/x.mkv` and `Others/x.mkv` → `other`
  - `Extras/x.mkv` → `{Folder: "", other}`, ok true
  - `Heat (1995)/Featurettes/Extras/x.mkv` → the first extras folder wins: `Folder "Heat (1995)"`, `featurette`
  - `Trailer Park (2006)/Trailer Park (2006).mkv` and `Heat (1995)/Heat (1995).mkv` → ok false

  `TestVersionLabelsAreCanonical` table (stem → label): `Heat (1995) - 4K` → `2160p`; `Heat (1995) - UHD` → `2160p`; `Heat (1995) - 1080P` → `1080p`; `Heat (1995) {edition-Director's Cut}` → `Director's Cut`; `Heat (1995) - Theatrical` → `Theatrical`; `Heat.1995.2160p.UHD.BluRay.x265-GRP` → `2160p`; **`Heat (1995) - 1080p REPACK` → `1080p`**; **`Heat (1995) - Bluray-1080p Proper` → `1080p`**; **`Heat (1995) - Director's Cut REPACK` → `Director's Cut`**.
  Run: `go -C server test ./internal/library/naming -run 'TestExtra|TestVersionLabels' -v`. Expected: FAIL.
- [ ] **Step 2: Implement `Extra` in `naming/extras.go`.** Fold `extraFolders` and `isExtraName` into it (a map folder → `ExtraType`, a suffix list → `ExtraType`); `Classify` returns `ErrExtra` when `Extra` reports ok, as today, including the existing `len(dirs) > 0` rule for suffix names. Change the `ErrExtra` text to drop "(LIB-3)".
- [ ] **Step 3: Canonicalise in `versionLabel`.** Strip `junkRe` (proper/repack, defined in `classify.go`) from the chosen label first. Then split the label on space, `-`, `.` and `_` (so `Bluray-1080p` splits); a label whose parts are only source words (bluray, web, dl, webrip, hdtv, remux, bdrip, brrip, dvdrip) plus one resolution word, or whose lower case is `4k`/`uhd`, returns `resolution(label)`. The nine existing rows keep passing. Other labels keep their text, cut to 90 characters on a rune boundary. Case differences are resolved later, by `COLLATE NOCASE` in SQL.
- [ ] **Step 4: Run** the same command plus `go -C server test ./internal/library/naming`. Expected: PASS; existing LIB-2a naming tests stay green.
- [ ] **Step 5: Commit** `feat(library): classify extras and canonical cut labels (LIB-3)`.

---

### Task 2: Schema and extras placement

**Files:**
- Create: `server/internal/library/migrations/<timestamp>_library_versions_extras.sql`, `server/internal/library/extras.go`
- Modify: `server/internal/library/scan.go:413-430` (`place`) and the `settle` call at `:232`; `server/internal/library/duplicates.go:244-246` (`moveInto`)
- Test: `server/internal/library/extras_test.go`, `duplicates_test.go`

**Interfaces:**
- Consumes: `naming.Extra`, `library_folders` (root, folder → item), `Store.item`.
- Produces:
  ```go
  func (s *Store) placeExtra(ctx context.Context, tx *sql.Tx, root Root, fileID, rel string) (placed bool, err error)
  func (s *Store) adopt(ctx context.Context, rootID string, full bool) error // the post-walk pass; runs adoptExtras here, adoptVersions in Task 3
  ```
- Schema (one migration, covers Tasks 2–3):
  ```sql
  CREATE TABLE library_extras (
      id TEXT PRIMARY KEY,
      item_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
      file_id TEXT NOT NULL UNIQUE REFERENCES library_files(id) ON DELETE CASCADE,
      type TEXT NOT NULL CHECK (type IN ('trailer','featurette','behindTheScenes','deletedScene','interview','scene','short','other')),
      title TEXT NOT NULL
  ) STRICT;
  CREATE INDEX library_extras_item ON library_extras(item_id);
  ALTER TABLE library_versions ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 1;
  ALTER TABLE library_versions ADD COLUMN fresh INTEGER NOT NULL DEFAULT 0; -- placed since the last whole-root scan; see Task 3
  CREATE INDEX library_versions_fresh ON library_versions(file_id) WHERE fresh = 1;
  CREATE TABLE library_version_flags (
      version_id TEXT PRIMARY KEY REFERENCES library_versions(id) ON DELETE CASCADE,
      of_id TEXT NOT NULL REFERENCES library_versions(id) ON DELETE CASCADE,
      state TEXT NOT NULL CHECK (state IN ('flagged','kept'))
  ) STRICT;
  ```
  **Backfill, in the same migration**, for databases that already hold same-label versions: number each item's versions per label case-insensitively (`ROW_NUMBER() OVER (PARTITION BY item_id, lower(COALESCE(label,'')) ORDER BY id)` into `ordinal`), then insert a `flagged` row for every version with `ordinal > 1`, pointing at the same item's same-label version with `ordinal = 1`. A database that already holds a rename duplicate (old file missing, new file present) backfills as `1080p (2)` with a flag and **is not adopted** (`fresh` defaults to 0); the admin keeps or relabels it.

- [ ] **Step 1: Write failing tests** (use the temp-media helper `TestRenamedFolderKeepsItemIDs` uses):
  - `TestExtraPlacedUnderMovie`: `Heat (1995)/Heat (1995).mkv`, `Heat (1995)/Featurettes/Making Of.mkv`, `Heat (1995)/Heat (1995)-trailer.mkv` → the movie has exactly **1 version** and **2 extras** (`featurette`, `trailer`), and **no file's `unplaced` column is set** after the scan. Discovery walks in lexical order, so `Featurettes/…` and `Heat (1995)-trailer.mkv` (`-` sorts before `.`) are placed before the movie file and are only placed by `adopt`; `Run.Unplaced` is a first-sight count and is not asserted.
  - `TestExtraTitleIsCapped`: a 300-character extra file name gives a title of 200 runes.
  - `TestShowSeasonExtraAttachesToShow`: `Show (2019)/Season 01/S01E01.mkv` plus `Show (2019)/Season 01/Featurettes/x.mkv` → the extra's item is the show; the episode has 1 version.
  - `TestExtraBeforeParentIsAdopted`: scan only `Heat (1995)/Featurettes/a.mkv` → unplaced `extra`; add `Heat (1995)/Heat (1995).mkv`, scan → placed, `unplaced` NULL.
  - `TestExtraWithoutParentStaysUnplaced`: with a loose `Heat (1995).mkv` in the root (its folder is `''`), `Extras/x.mkv` stays unplaced `extra` and attaches to nothing; an extra in a folder mapped to two items (ambiguous) also stays unplaced; **no new item is created**.
  - `TestFoldCarriesExtras`: a folder-rename fold and an admin merge (`Decide(..., true)`) leave the surviving item with both folders' extras.
  - `TestMigrationBackfillsSameLabelPairs`: a database with two same-label versions on one item before the migration ends with ordinals 1 and 2 and one `flagged` row.

  Run: `go -C server test ./internal/library -run 'TestExtra|TestShowSeasonExtra|TestFoldCarries|TestMigrationBackfills' -v`. Expected: FAIL.
- [ ] **Step 2: Add the migration** above.
- [ ] **Step 3: `placeExtra`:** call `naming.Extra`; walk from `Folder` up through its parent folders, **stopping before `''`** (loose files map to `''`, never to an extras parent), to the first folder with rows in `library_folders` for the root; exactly one item → insert the extra (`ON CONFLICT(file_id) DO UPDATE` type, title cut to 200 runes, item), **set `library_files.unplaced = NULL`**, return true; none or several → false. `place` calls it when `Classify` returns `ErrExtra` and records `unplaced = 'extra'` only on false.
- [ ] **Step 4: `adopt`, extras part:** select the root's present files with `unplaced = 'extra'`, call `placeExtra` for each, in transactions of 500. Call `adopt` right after `settle` at `scan.go:232` so an extras folder walked before its movie is adopted in the same scan. Shortcut comment: `Run.Unplaced counts a file when first seen unplaced and isn't decremented by adoption, fine because the file's own unplaced column is the truth; if the admin view ever shows the run counter, have adopt return a count for Scan to subtract.`
- [ ] **Step 5: `moveInto`:** add `UPDATE OR IGNORE library_extras SET item_id = ? WHERE item_id = ?` to its statement list (versions already move there; flags follow their versions by key). Task 3 adds the renumbering call to this function.
- [ ] **Step 6: Run** the same command plus `go -C server test ./internal/library`. Expected: PASS.
- [ ] **Step 7: Commit** `feat(library): place extras under their movie or show (LIB-3)`.

---

### Task 3: Version identity, same-label numbering and admin flags

**Files:**
- Create: `server/internal/library/versions.go`
- Modify: `server/internal/library/scan.go` (`version` moves to `versions.go`; `applyFiles`' skip rule at `:400-402`), `server/internal/library/duplicates.go` (`moveInto`), and the expectations that flip from `v2` to `v1` (a rename now ends with the old version; a null label counts as the same label): `scan_test.go:205-208` (`TestMovedFileKeepsItsItem`), `duplicates_test.go:121` (`TestRenamedFolderKeepsItemIDs`) and `duplicates_test.go:281` (`TestMergedFolderCanBeRenamed`)
- Test: `server/internal/library/versions_test.go`

**Interfaces:**
- Consumes: the Task 2 migration (`ordinal`, `fresh`, `library_version_flags`), `adopt`.
- Produces:
  ```go
  func (s *Store) version(ctx context.Context, tx *sql.Tx, itemID, fileID, label string) error // replaces the scan.go one; same call sites
  func (s *Store) adoptVersions(ctx context.Context, rootID string, full bool) error            // called from adopt, after settle
  func renumber(ctx context.Context, tx *sql.Tx, into string, moved []string) error            // called from moveInto; renumbers ONLY the moved-in versions
  type VersionFlag struct{ VersionID, OfID, ItemID, Title string; Label *string }
  func (s *Store) VersionFlags(ctx context.Context, libID string) ([]VersionFlag, error)  // Planned: LIB-2d (the #104 duplicates view)
  func (s *Store) KeepVersions(ctx context.Context, versionID string) error              // Planned: LIB-2d
  func (s *Store) RelabelVersion(ctx context.Context, versionID, label string) error     // Planned: LIB-2d; 1–90 characters after trimming, else an error
  func displayLabel(label *string, ordinal int) *string // "1080p (2)", cut so the whole stays ≤ 100 characters; a null label with ordinal 2 shows "Version 2"; ordinal 1 shows the label as is
  ```

**The rules**
- **At placement** (`version`, runs inside the walk): (1) a row for `(item, file)` exists → nothing, so a file replaced in place keeps its version. (2) Otherwise insert a version with `fresh = 1` and `ordinal = 1 + max(ordinal)` over the item's same-label versions (case-insensitive, null equals null; never reused), plus a `flagged` row pointing at the **ordinal-1 version** when the ordinal is above 1. Adoption cannot happen here: during the walk the replaced file is still `present`.
- **After the walk** (`adoptVersions`, after files are marked missing and `settle` has folded renames): a pair is **F**, a `fresh` version whose file is `present`, and **N**, another version of the same item (fresh or not) with the same label (case-insensitive, null equals null) whose file is **not present** (missing or retired) and whose **ordinal is lower than F's**. Ordinals are `1 + max`, never reused, so they are placement order: F is the newer file. Without this, an older live copy would be adopted into a missing newer sibling and lose its own resume. Adopt only when the pairing is **one-to-one** (F has exactly one candidate N and N exactly one candidate F); anything else stays for review. Adopting repoints any flag whose `of_id` is F to N, drops N's own flag if its `of_id` is F, deletes F (its own flag cascades) and points N at F's file; N keeps its ID, ordinal and any other flag. The query **starts from the root's `fresh` versions** (the partial index `library_versions_fresh` serves it), not from its non-present files: retired file rows are never deleted, so that set only grows. It works in batches of 500. **A whole-root scan (`full`) then clears `fresh`** for the root's versions; a scoped scan keeps it, so adoption doesn't depend on which scope a watcher visits first, and a version placed by one scoped scan can be replaced by a later one (`fresh` marks the new file, never the old one). With `FullScanEvery` off, whole-root scans still run at startup or reload, on a manual `ScanRoot`, on a lost-watch recheck, on a watcher overflow and when scoped scans coalesce, and each clears `fresh`.
- **A merge or rename fold** (`moveInto`): version IDs are random, so the order of two colliding versions can't come from them. `moveInto(into, from)` reads `from`'s version IDs before the move, runs `UPDATE OR IGNORE`, then takes the moved set as those IDs **now on `into`** (an ignored version stays on `from` and is deleted with it). `renumber(into, moved)` renumbers **only that set**, in ordinal order: each whose ordinal is already taken in its label group in `into` gets `max + 1` and a `flagged` row pointing at the group's ordinal-1 version. The flag insert is an **upsert** (`ON CONFLICT(version_id) DO UPDATE` sets `of_id` and returns the state to `flagged`): `version_id` is the key, and a moved version that already has a `kept` flag would otherwise fail `settle` on every scan. `into`'s ordinals never change. The rename case then finishes in `adoptVersions` (the folded-in version is `fresh` with the higher ordinal), leaving the old version with its old ordinal and no `(2)`.
- **A returning file:** adoption points the old version at the replacement file, so an original file that comes back (missing → present, `unplaced` NULL) has no version row. In `applyFiles`, a file that **returns** (`state != 'present'`) and has no `library_versions` or `library_extras` row is placed again instead of skipped; files that never left pay no extra query.
- `VersionFlags` lists a flag only while both versions' files are present.

- [ ] **Step 1: Write failing tests:**
  - `TestUpgradedFileKeepsVersionID`: `Heat (1995) - 1080p.mkv`; note its version ID; delete it and add `Heat (1995) - 1080p REPACK.mkv`; whole-root scan → one version, **same ID**, on the new file, ordinal 1, no flag.
  - `TestScopedImportThenScopedUpgradeKeepsVersionID`: import `Heat (1995) - 1080p.mkv` through a **scoped** scan, no whole-root scan in between, then upgrade the file through another scoped scan hours of fake time later → same version ID, one version (the old version is still `fresh`, which must not block adoption).
  - `TestScopedScanKeepsFreshUntilFullScan`: the old file lives outside the scanned scope (a rename into another folder of the root); a scoped scan leaves two versions; a later whole-root scan adopts, same ID; a version placed by a whole-root scan is no longer `fresh` afterwards.
  - `TestNewerCopyDeletedWhileOlderFreshAdoptsNothing`: V1 (older, still `fresh`) and its flagged same-label copy V2; delete V2's file, scan → nothing adopted, V1 keeps its ID, ordinal 1 and no `(2)`; the flag is not listed (V2's file isn't present).
  - `TestOlderCopyDeletedPairsCleanly`: the same two copies, delete V1's file (the older) → V1's ID moves onto V2's file, one version, ordinal 1, no flag.
  - `TestAdoptionNeedsOneToOnePairing`: two fresh same-label files and one missing same-label version → nothing adopted; a long-standing live copy plus one missing sibling and no new file → nothing adopted.
  - `TestInPlaceReplacementKeepsVersion` (a regression guard that passes today): same path, new size and mtime → same version ID.
  - `TestSameLabelKeepsBothAndFlags`: two present `1080p` files → ordinals 1 and 2, one flagged row (`VersionFlags` returns it with the item's title); neither file is unplaced.
  - `TestRenamedSecondCopyReconciles`: from that state rename the ordinal-2 file → its version follows the file; still two versions, flag kept.
  - `TestAmbiguousAdoptionMakesNewVersion`: both copies missing, one new file arrives → a third version, ordinal 3, old versions untouched.
  - `TestLabelChangeMakesNewVersion`: `- 720p.mkv` replaced by `- 1080p.mkv` → two versions (the old one missing, the new one ordinal 1 under its own label), nothing adopted: the accepted consequence of Decision 2.
  - `TestDecideMergeRenumbersSameLabel`: two coexisting folders each with one `1080p` file, admin merge → the folded-in version gets ordinal 2 (the surviving item's version keeps ordinal 1 whichever version ID sorts first, run with several random IDs), one flagged row.
  - `TestReturnedOriginalIsPlacedAgain`: after the upgrade above the original file comes back → it has a version (ordinal 2, flagged), the adopted one keeps its ID.
  - `TestKeepVersionsClearsFlag`, `TestRelabelResetsOrdinal`: relabelling `1080p (2)` to `Director's Cut` sets ordinal 1 and removes the flag; relabelling to an existing label keeps it flagged with the next ordinal; an empty label is an error; 100-character names stay valid after the suffix (`displayLabel`).
  - `TestUnlabelledCopiesDisplayAsNumbered`: two unlabelled files → `nil` and `"Version 2"`.
  - **Change the existing expectations** `TestMovedFileKeepsItsItem` (`scan_test.go:205-208`: `movie Heat - v2` → `v1`), `TestRenamedFolderKeepsItemIDs` (`duplicates_test.go:121`) and `TestMergedFolderCanBeRenamed` (`:281`): a rename now ends with one version, the old ID. **Assert the surviving version's ordinal is 1 and its `displayLabel` has no "(2)"**, not only the count, and repeat the rename several times so a random-ID ordering can't hide a wrong ordinal.

  Run: `go -C server test ./internal/library -run 'TestUpgraded|TestScoped|TestInPlace|TestSameLabel|TestRenamedSecond|TestRenamedFolder|TestMergedFolder|TestAmbiguous|TestNewerCopy|TestOlderCopy|TestAdoption|TestLabelChange|TestDecideMerge|TestReturned|TestKeep|TestRelabel|TestUnlabelled|TestMovedFile' -v`. Expected: FAIL (the regression guard passes).
- [ ] **Step 2: Implement** `version`, `displayLabel`, `adoptVersions` (called from `adopt`), `renumber(into, moved)` (called from `moveInto`, after the versions move), and the returning-file rule in `applyFiles`.
- [ ] **Step 3: Implement** `VersionFlags` (flagged rows of the library, ordered by the item's `sort_title`), `KeepVersions` (state `kept`, `ErrNotFound` without a flagged row) and `RelabelVersion`. Shortcut comment: `a relabelled version isn't adopted on a later rename (the file's parsed label no longer matches), when an admin-locked label field exists upgrade.`
- [ ] **Step 4: Run** the same command plus the whole package. Existing tests whose version counts change because adoption now replaces a duplicate (for example any other `v2` expectation) are updated in this task; list each in the PR.
- [ ] **Step 5: Commit** `feat(library): stable version identity and flagged same-label copies (LIB-3)`.

---

### Task 4: Episode segments and the version/extra read model

**Files:**
- Create: `server/internal/library/segments.go`
- Test: `server/internal/library/segments_test.go`

**Interfaces:**
- Consumes: `probe.Result{DurationMs, Chapters}`, `library_versions`, `library_extras`, `displayLabel`.
- Produces:
  ```go
  type FileInfo struct {
      FileID, Path string
      Size, MtimeNs int64
      Present bool      // file state is 'present'
      RootState string  // the root's state: available, scanning, degraded, offline
      Probe *probe.Result // nil before the first probe or when stale for the revision
  }
  type VersionRow struct {
      ID, ItemID string
      Label *string      // displayLabel applied
      FileInfo
      Segment *contracts.Segment
  }
  type ExtraRow struct {
      ID, ItemID, Title string
      Type naming.ExtraType
      FileInfo
  }
  func SegmentOf(chapters []probe.Chapter, durationMs int64, index, count int) *contracts.Segment
  func (s *Store) VersionRows(ctx context.Context, itemID string) ([]VersionRow, error) // Planned: LIB-2c (getItem handler)
  func (s *Store) ExtraRows(ctx context.Context, itemID string) ([]ExtraRow, error)    // Planned: LIB-2c (getItem handler); a movie's or show's extras, none for a season or episode
  ```
- `SegmentOf` returns a range only when **all** hold: `count >= 2`; `len(chapters) == count`; each chapter lasts **at least 10 minutes** (Decision 1); the first chapter starts at 0; each starts where the previous ended; the last ends within 1000 ms of `durationMs`. `index` is the episode's position (0-based) among the file's episodes ordered by `idx`. Anything else returns nil.
- `VersionRows` fills `Segment` only for an episode whose version shares its file with at least one other episode **of the same season** (same `parent_id`); it never orders rows (LIB-2c decision 10 does).
- `ExtraRows` leaves out extras whose file is missing or retired, except on an `offline` root, so a folder rename doesn't list two copies with one unavailable.

- [ ] **Step 1: Write failing tests:**
  - `TestSegmentOf` table: 2 episodes, chapters `[0–1_500_000, 1_500_000–3_000_000]`, duration 3_000_000 → `{0,1_500_000}` and `{1_500_000,3_000_000}` (unequal lengths are fine when the chapters say so); duration 3_000_500 → still matches; 2 episodes with 3 chapters → nil; a 10 s gap → nil; first chapter starting at 5 s → nil; last chapter ending 30 s short → nil; a chapter shorter than 10 minutes → nil; no chapters → nil; `count == 1` → nil; `durationMs == 0` → nil. **A 3_000_000 ms file with no chapters and 2 episodes gives nil for both, never 1_500_000.**
  - `TestVersionRowsSharedFile`: episodes 2 and 1 of one season on one file (inserted in that order), a probe with two chapters → episode 1 gets the first range, episode 2 the second. Episodes in different seasons sharing a file → nil.
  - `TestVersionRowsStaleProbeHasNoSegment`: the file changed after the probe (`probe_size` differs) → `Probe` nil, `Segment` nil.
  - `TestVersionRowsMovieHasNoSegment`; `TestExtraRowsForShow`; `TestExtraRowsOfflineRoot` (`RootState` offline, `Present` still true); `TestExtraRowsSkipMissingAfterRename`.

  Run: `go -C server test ./internal/library -run 'TestSegmentOf|TestVersionRows|TestExtraRows' -v`. Expected: FAIL.
- [ ] **Step 2: Implement** `SegmentOf` as a pure function, then the two reads (one query each: version or extra joined to file and root; `json.Unmarshal` of `probe_json` only when `probe_size`/`probe_mtime_ns` equal the file's size/mtime). Shared-file detection is `SELECT v.item_id FROM library_versions v JOIN library_items i ON i.id = v.item_id WHERE v.file_id = ? AND i.kind = 'episode' AND i.parent_id = ? ORDER BY i.idx`.
- [ ] **Step 3: Run** the same command plus the package. Expected: PASS.
- [ ] **Step 4: Commit** `feat(library): episode segments from chapters and version/extra reads (LIB-3)`.

---

### Task 5: Contract — `extras` on the item detail

**Files:**
- Modify: `server/internal/contracts/catalogue.go:211-240` (`ItemDetail`, new `Extra` and `ExtraType` constants), `docs/contracts/openapi.json:701-760` (`ItemDetail`, new `Extra` and `ExtraType`), `testdata/contracts/catalogue/{item-movie,item-movie-admin,item-show,item-episode-shared-file}.json`, `testdata/contracts/catalogue/README.md`, `web/src/api/schema.d.ts` (regenerate), `web/tests/fake-api.ts:160`, `web/contract-tests/fixtures.test.mjs`
- Create: `testdata/contracts/catalogue/item-episode-chaptered.json`
- Test: `server/internal/contracts/catalogue_test.go`, `fixtures_test.go` (list the new fixture), `web/contract-tests/fixtures.test.mjs`

**Interfaces:**
- Produces:
  ```go
  const ( ExtraTrailer = "trailer"; ExtraFeaturette = "featurette"; ExtraBehindTheScenes = "behindTheScenes"; ExtraDeletedScene = "deletedScene"; ExtraInterview = "interview"; ExtraScene = "scene"; ExtraShort = "short"; ExtraOther = "other" )
  type Extra struct {
      ID      string       `json:"id"`      // also Version.ID; the extra's own playback item ID (Decision 4)
      Type    string       `json:"type"`    // one of the ExtraType constants
      Title   string       `json:"title"`
      Version MediaVersion `json:"version"`
  }
  // ItemDetail gains: Extras []Extra `json:"extras"` // movies and shows; empty otherwise. Planned: LIB-2c (getItem handler)
  ```
  `ItemDetail.MarshalJSON` adds `d.Extras = nonNil(d.Extras)`. OpenAPI: an `ExtraType` enum (the eight values), `Extra` (`additionalProperties: false`, all four fields required, `title` 1–200 characters), `ItemDetail.extras` required. `ItemDetail` already has one `if/then` (versions empty for shows and seasons): **turn it into an `allOf`** like `ItemSummary` has, with a second entry limiting `extras` to `maxItems: 0` for seasons and episodes.

- [ ] **Step 1: Write failing tests:** the Go round trip of the four existing fixtures with `"extras": []` added and of the new `item-episode-chaptered.json` (episode 2 of a two-episode file with `segment {startMs:1500000, endMs:3000000}`); add `checkSchemaEnum(t, "ExtraType", ExtraTrailer, ExtraFeaturette, ExtraBehindTheScenes, ExtraDeletedScene, ExtraInterview, ExtraScene, ExtraShort, ExtraOther)` to `TestCatalogueEnumsMatchOpenAPI` (`catalogue_test.go:42-49`); `Extra.type` `$ref`s the separate `ExtraType` schema, so `checkEnum` on `Extra.type` would read no enum; web invalid variants: `["extra with unknown type", "item-movie.json", d => { d.extras[0].type = "bonus"; }]`, `["path in extra fileName", "item-movie-admin.json", d => { d.extras[0].version.fileName = "/media/x.mkv"; }]`, `["extras on an episode", "item-episode-shared-file.json", d => { d.extras = catalogue("item-movie.json").extras; }]`, `["extra without version", "item-movie.json", d => { delete d.extras[0].version; }]`. Run: `go -C server test ./internal/contracts` and `npm --prefix web run test:contracts`. Expected: FAIL.
- [ ] **Step 2: Implement** the Go types, the OpenAPI schemas, then `npm --prefix web run generate:api`. Fixtures: `item-movie.json` gets two extras (a trailer, and an offline featurette with `available: false`), both with `fileName: null`; `item-movie-admin.json` the same with bare file names; `item-show.json` one featurette; `item-episode-shared-file.json` `extras: []`. Add `extras: []` to the literal at `web/tests/fake-api.ts:160`.
- [ ] **Step 3: Update** `testdata/contracts/catalogue/README.md` rows (the extras and the chaptered episode).
- [ ] **Step 4: Run** both commands again plus `npm --prefix web run lint && npm --prefix web run build`. Expected: PASS and `git diff --exit-code -- web/src/api/schema.d.ts` clean (the Makefile's check).
- [ ] **Step 5: Commit** `feat(contracts): extras on the item detail (LIB-3)`.

---

### Task 6: Reconciliation fixtures, docs and close-out

**Files:**
- Create: `server/internal/library/reconcile_test.go`
- Modify: `docs/superpowers/plans/2026-10-24-catalogue-plans-map.md` (the LIB-3 row), `README.md` only if its status/layout lines mention extras or versions

- [ ] **Step 1: Write `TestVersionCutReconciliation`** — one scenario over a real temp media root and several whole-root scans, asserting the catalogue after each:
  1. `Heat (1995)/Heat (1995) {edition-Theatrical}.mkv`, `… {edition-Director's Cut}.mkv`, `Heat.1995.2160p.mkv` in one folder → three versions labelled `Theatrical`, `Director's Cut`, `2160p`, none flagged.
  2. The Theatrical file is replaced by `… {edition-theatrical} REPACK.mkv` (same label, different case) → after the scan, the same version ID, still three versions (adoption runs after the walk).
  3. A second `2160p` file appears → numbered `2160p (2)`, one flag; `KeepVersions` clears it.
  4. `Heat (1995)/Featurettes/Making Of.mkv` and `Heat (1995)-trailer.mkv` → two extras and no new version.
  5. `Show (2019)/Season 01/Show - S01E01-E02.mkv` with a stored two-chapter probe (25 minutes each) → both episodes have a `Segment`; without chapters, both `nil`.

  Run: `go -C server test ./internal/library -run TestVersionCutReconciliation -v`. Expected: PASS once Tasks 1–5 are in.
- [ ] **Step 2: Docs.** In the plans map, LIB-3 is a segment of the `lib-1-2-3-scanner` row, not a row of its own: in that row's text change "**LIB-3**: version/extras parts of T6 + T9" to "**LIB-3**: claimed (#160), planned in `2026-11-02-lib-3-versions-extras.md`". README check: `grep -n -i 'extra\|version\|cut' README.md`; update a line only if it now misrepresents the branch, and tick the PR template's README box accordingly.
- [ ] **Step 3: Planned tags.** Every new exported function with no production caller carries `// Planned: LIB-2c (<what will call it>)` or `// Planned: LIB-2d (<…>)`. Check with `grep -rn 'Planned: LIB-3' server web`; it must print nothing (a `Planned: LIB-3` tag would name LIB-3 as its own caller).
- [ ] **Step 4: Full check.** From a checkout with Go available (the repo's `mise.local.toml`): `make check`. Expected: exit 0. Record the local result in the PR body.
- [ ] **Step 5: Commit** `test(library): version and cut reconciliation fixtures (LIB-3)`.

## Self-review

- **Spec coverage:** §3.1 versions grouped (Tasks 1, 3), picker data (Task 4 rows; ordering stays LIB-2c's), extras folders (Tasks 1–2, 5); §3.8 no equal split (Task 4); §10.1 stable identity (Task 3); fixtures required by the §9.4 row (Task 6); the "segment null until LIB-3" promise in LIB-2c decision 10 (Task 4).
- **Not in this plan:** the catalogue HTTP handler, the `Resolver` lookup of extras, the picker UI (LIB-2c, UI-2), the admin screens for flags (LIB-2d, with #104), provider artwork or titles for extras (META).
- **Type consistency:** `ExtraType` spellings are the same in `naming`, the migration `CHECK`, the Go constants and the OpenAPI enum; `displayLabel` is used by Tasks 3 and 4; `adopt` (Task 2) calls `adoptVersions` (Task 3); `Extra.id == Extra.version.id` in Task 5 and Decision 4.
- **Review Focus:** each of the five lines has a named test in its owning task.
- **Proportion:** six tasks, test names and signatures only. The spec row is one line; the plan decides the identity, evidence and contract rules the spec leaves open.
