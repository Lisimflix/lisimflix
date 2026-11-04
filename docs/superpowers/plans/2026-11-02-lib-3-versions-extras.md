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
| File scope | `server/internal/library/{scan.go, duplicates.go}` and the changed expectations in `scan_test.go`, `duplicates_test.go`; new `library/{extras.go, versions.go, segments.go, backfill.go}` with `extras_test.go`, `versions_test.go`, `segments_test.go`, `backfill_test.go`, `reconcile_test.go`; `library/naming/{classify.go, names.go, extras.go}` with `classify_test.go`, `extras_test.go`; one new `library/migrations/*.sql`; `server/cmd/lisimflix/{serve.go, main_test.go}` (one call and its failure test); **Tasks 4 and 7:** `library/detail.go` (LIB-2c's `itemVersions`, extended in place), `library/browse.go` (`versionOrderSQL`: `v.ordinal` before `v.id` only) and `library/resolver.go` with their tests (`browse_test.go` too); `server/internal/contracts/{catalogue.go, catalogue_test.go, fixtures_test.go}`, `docs/contracts/openapi.json`, `testdata/contracts/catalogue/` (fixtures and `README.md`), `web/src/api/schema.d.ts` (generated), `web/tests/fake-api.ts` (one literal), `web/contract-tests/fixtures.test.mjs`; `docs/superpowers/plans/2026-10-24-catalogue-plans-map.md`; `README.md` only if it misdescribes the branch |
| Consumes | `library_files`, `library_versions`, `library_folders`, `naming.Entry.Version`, `probe.Result.Chapters`, `contracts.MediaVersion`, `contracts.Segment` |
| Produces | LIB-2c's `itemVersions(tx, itemID)` extended in place, an unexported `itemExtras(tx, itemID)`, `Store.BackfillVersions`, `Store.VersionFlags` / `KeepVersions` / `RelabelVersion`, `naming.Extra` / `ExtraEntry` / `ExtraType` / `IsResolutionLabel`, `library.SegmentOf`, `VersionFlag`, `FileInfo`, `VersionRow`, `ExtraRow`, `contracts.Extra`, the `contracts.Extra*` constants, `ItemDetail.extras`, the reconciliation fixtures |
| Start dependency | Tasks 1–3: none open (LIB-2a/2b and CTR-2 merged). Tasks 4, 6 and 7 need LIB-2c's code merged first (Decision 5; Task 6's segment scenario needs Task 4). Tasks 1–3 don't reach main without Task 7 |
| Module dependency | LIB-2c builds the catalogue handler, `itemVersions` and the `Resolver`; LIB-2d builds the admin API. Task 7 is the production caller of LIB-3's reads, so only the admin methods carry `// Planned: LIB-2d (…)` |
| OpenAPI integration owner | Pete, for LIB-3's one additive `extras` change; agree the `openapi.json` edit order with LIB-2c's owner first (CONTRIBUTING, Working together: one named owner per shared edit) |
| Standalone test | `make check` plus the Task 6 fixtures |
| Integration proof | LIB-3's own (Task 7, once LIB-2c has merged): `getItem` tests show `extras`, a shared-file episode with a `segment`, and a numbered same-label version. UI-2's picker shows them. Version flags join the #104 duplicates view in LIB-2d |

**Overlap with open work (checked 2026-11-02):**
- **`scan.go`:** #153 (Lammy, LIB-2b follow-ups) and LIB-2c (#77) also edit it. LIB-3 touches `place`, `version`, `applyFiles`' skip rule, and adds one `adopt` call beside `settle` inside `Scan` (`:232`); the run loop's flow is unchanged. Land after #153 and rebase.
- **`scheduler.go`, `serve.go`:** #153 and LIB-4 (#159) edit the scheduler. LIB-3 doesn't touch `scheduler.go`; in `server/cmd/lisimflix/serve.go` it adds one `BackfillVersions` call before the scheduler starts (Task 3), beside LIB-2c's `BackfillDerived` once that exists. Rebase after them.
- **`store.go`, `openapi.json`:** LIB-2c edits both; LIB-3 touches only `openapi.json` (Task 5). See the integration owner row.
- **META-1 (#73):** reads `library_versions`, writes none. Task 2 adds columns (`ordinal`, `seq`, `fresh`) it must ignore.
- **`library/detail.go`, `browse.go`, `resolver.go` (LIB-2c):** one read path, agreed on #77 (Decision 5): LIB-2c builds `itemVersions(tx, itemID)`; Task 4 extends it in place and adds the `v.ordinal` term to `versionOrderSQL` (`browse.go`); Task 7 wires the extras read and the `Resolver` extras branch.

## Decisions

Decided by Pete on 2026-11-02 (issue #160), "all recommended" (decision 2 revised after Lammy's #161 review, below):

1. **Segments (Task 4): DECIDED.** A range only when the chapter count equals the episode count, the chapters tile the file from 0 to its duration, and every chapter lasts at least 10 minutes; otherwise `segment` stays null and §3.8's explicit watched actions apply. Chapter-count equality plus tiling is weak evidence on its own, so revisit with real files.
2. **Rename adoption (Task 3): REVISED after Lammy's #161 review (2 Nov); replaces "label match only". Pete signed off on 4 Nov.** A newly placed version on a present file takes over an existing version whose file is missing or retired when the item matches, the pairing is one-to-one, the placement-order rule (`seq`) holds, and the two labels are in the same **adoption class**: *resolution-only* labels (null, empty, or any value `naming.resolution` returns: `480p`, `576p`, `720p`, `1080i`, `1080p`, `2160p`; exposed as `naming.IsResolutionLabel`) are one class; any other label (a cut or edition) must match exactly, case-insensitively. **Why:** an episode's version label is only its resolution (`classify.go:172`) and most Radarr upgrades change resolution, so exact matching would give every 720p → 1080p upgrade a new version ID, orphan the old entry (retirement keeps versions) and lose resume (DATA-1a stores resume per version, #154 Decision 8); §3.8 asks for confirmation only between different timelines, and a resolution change keeps the timeline. **Cut changes** (`Theatrical` → `Director's Cut`) still make a new version, and `itemVersions` leaves out versions whose file is missing or retired while the item has a present version on a root that isn't offline (the rule extras already use, Task 4), so no dead entry lingers (**this amends LIB-2c decision 10's "lists every version, available or not"**: Lammy to note it on #138/#77 with the ordinal tie-break); an offline root keeps everything listed. **Residual risk:** a `fresh` version stays eligible until the next whole-root scan (every 6 h by default; with `FullScanEvery` off, the next startup, manual, recheck, overflow or coalesced scan). If the **older** of two same-class copies is deleted inside that window, its version ID moves onto the newer file and the newer version's resume is lost. The `seq` rule prevents the reverse (an older live copy absorbed into a missing newer one), and the one-to-one rule leaves ambiguous cases for review. **Backfill note:** for a rename made before LIB-3 the backfill keeps the older (dead) version's ID; if DATA-1a stores resume by version ID before LIB-3 deploys, resume made since that rename is dropped (nothing references version IDs on `main` today). **Manual swaps** (add the new file, wait a full scan, delete the old) are never adopted: the old file is still present while `fresh` is cleared.
3. **Season-folder extras: DECIDED.** `Show/Season 01/Featurettes/x.mkv` attaches to the show, not the season.
4. **Playing an extra: DECIDED, option (a).** The extra's own ID is the playback `itemId` (`Extra.id == Extra.version.id`), LIB-2c's `Resolver` resolves extra IDs, and DATA-1 (#154) ignores progress for IDs that are not catalogue items. This avoids the parent being credited with an extra's progress (a finished trailer marking the movie watched or overwriting its resume) and needs no playback contract change. DATA-1's plan needs a line for it.

5. **Read path and wiring: DECIDED on #77 (Lammy, 2 Nov).** LIB-2c goes first (P0/G1) and builds an item's versions in one unexported function taking its read transaction, `itemVersions(tx, itemID)`. LIB-3 **extends it in place** (numbered labels, segments, the hide rule of decision 2) and adds the extras read beside it, with no exported `VersionRows`/`ExtraRows`, so there is one read path and LIB-3 deletes nothing of LIB-2c's. **LIB-3 owns** the `Resolver` extras branch and the `openapi.json` `extras` field. The wiring is **Task 7**, after LIB-2c merges. Tasks 1–3 may start now; they don't reach main without Task 7. **LIB-3 also owns the ordinal tie-break** (Lammy's correction on #77): `ordinal` is LIB-3's column, so LIB-2c can't use it. Task 4 puts `v.ordinal` before `v.id` in `versionOrderSQL` (`browse.go`, from #169); without it, "1080p (2)" can become the default version, and so set `listItems`' `durationMs`, by random `v.id`.

## Global Constraints

- Go **1.27.3**, Node **24.22.1**; no new dependency (plans map delta 6).
- IDs are opaque strings in public JSON; **a path never appears in a public shape**; `fileName` is a bare name, admins only (spec §10.1, §12).
- JSON is camelCase; a Go nil slice encodes as `[]` (CTR-2 `nonNil`).
- A scan transaction holds at most **500 files** (spec §10.2); broken files are listed, **never abort a scan** (§3.1).
- Migrations: `server/internal/library/migrations/YYYYMMDDHHMMSS_library_<what>.sql`, registered through `Migrations()`. Pick a timestamp after the newest migration on `main` when you create the file (LIB-2c adds `20261029140000`).
- Times are UTC milliseconds; probe results are read from `library_files.probe_json` and must match the file's revision.
- `MediaVersion.label` is at most **100** characters including any " (n)" suffix.
- Version ordering and the default version are LIB-2c's decision 10. **Do not change them, with two exceptions:** LIB-3 adds `v.ordinal` before `v.id` as the last tie-break in `versionOrderSQL` (decision 5, Task 4), and decision 2's hide rule for unavailable versions amends decision 10's listing (needs Lammy's note on #138/#77).
- Never assume equal-length episodes (§3.8).

## Review Focus

1. **A Sonarr/Radarr upgrade replaces the file under a different name.** The version ID stays, so resume and history survive, whichever order the scan or a watcher scope visits the files. (Task 3 `TestUpgradedFileKeepsVersionID`, `TestResolutionUpgradeKeepsVersionID`, `TestScopedScanKeepsFreshUntilFullScan`)
2. **Two files of one movie get the same label.** Both stay playable and numbered, the admin is flagged, and a later rename or folder merge still reconciles. (Task 3 `TestSameLabelKeepsBothAndFlags`, `TestRenamedSecondCopyReconciles`, `TestDecideMergeRenumbersSameLabel`)
3. **A shared-file episode must never get an equal split.** No chapters, a count mismatch, a gap, a short file or tiny chapters all give `null`. (Task 4 `TestSegmentOf`)
4. **An extras folder is scanned before (or without) its movie, or an original file returns after its replacement was adopted.** Nothing is lost or invisible, and no phantom movie appears. (Task 2 `TestExtraBeforeParentIsAdopted`, `TestExtraWithoutParentStaysUnplaced`, `TestExtraNeverClimbsIntoAnotherMovieFolder`; Task 3 `TestReturnedOriginalIsPlacedAgain`)
5. **An admin merges two folders or a disk goes offline.** Extras and flags move with the merge; offline extras report `available: false`. (Task 2 `TestFoldCarriesExtras`; Task 4 `TestItemExtrasOfflineRoot`; Task 7 `TestItemDetailListsExtras`)

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
      Folder string    // the folder the extra belongs to, inside the root: everything before the extras folder, or the file's own folder for suffix names (`-trailer`, `-featurette`, …); "" when there is none
      Folders []string // folders to try, in order: Folder, then its parent while the last path element is a season or Specials folder (repeat); never past a movie or show folder, never "". A movies library's season-named collection folder can still climb; placeExtra requires an exact one-item match, so that is accepted
      Key     string   // for a suffix name whose stem before the suffix parses WITH a year, the folder key it must match (`key(parseName(stem).Title) + "|" + year`, "heat|1995"); "" otherwise (no year, or not a suffix name). It does not depend on the library type: `Extra` and `placeExtra` don't know it, so a TV suffix name with a year matches the `show:` row
      Type   ExtraType
      Title  string
  }
  func Extra(rel string) (ExtraEntry, bool) // false for ordinary media
  func IsResolutionLabel(label string) bool  // case-insensitive; true for "" and any value resolution() returns: decision 2's adoption class, used by adoptVersions and BackfillVersions
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
  - **`Key`:** `Heat (1995)/Heat (1995)-trailer.mkv` → `heat|1995`; `Marvel/Thor (2011)-trailer.mkv` → `thor|2011`; `Heat (1995)/Teaser-trailer.mkv` → `""` (no year); `Heat (1995)/Featurettes/x.mkv` → `""`; `Show (2019)/Season 01/Making Of-featurette.mkv` → `""`; `Show (2019)/Show (2019)-trailer.mkv` → `show|2019`.
  - **`Folders`:** `Heat (1995)/Featurettes/x.mkv` → `["Heat (1995)"]` (no climbing); `Show (2019)/Season 01/Deleted Scenes/x.mkv` → `["Show (2019)/Season 01", "Show (2019)"]`; `Show (2019)/Specials/Extras/x.mkv` → `["Show (2019)/Specials", "Show (2019)"]`; `Marvel/The Avengers (2012)/Behind The Scenes/x.mkv` → `["Marvel/The Avengers (2012)"]` only, never `Marvel`

  `TestIsResolutionLabel` (`""`, `480p`, `576p`, `720p`, `1080i`, `1080p`, `1080P`, `2160p` true; `Director's Cut`, `1080p HDR`, `Theatrical` false) and `TestVersionLabelsAreCanonical` table (stem → label): `Heat (1995) - 4K` → `2160p`; `Heat (1995) - UHD` → `2160p`; `Heat (1995) - 1080P` → `1080p`; `Heat (1995) {edition-Director's Cut}` → `Director's Cut`; `Heat (1995) - Theatrical` → `Theatrical`; `Heat.1995.2160p.UHD.BluRay.x265-GRP` → `2160p`; **`Heat (1995) - 1080p REPACK` → `1080p`**; **`Heat (1995) - Bluray-1080p Proper` → `1080p`**; **`Heat (1995) - WEBDL-1080p` → `1080p`**; **`Heat (1995) - Remux-2160p` → `2160p`**; `Heat (1995) - HDTV-720p` → `720p`; `Heat (1995) - DVD` → `""`; `Heat (1995) - DVD-R` → `""`; `Heat (1995) - 4K Remux` → `2160p`; `Heat (1995) - 2160p UHD BluRay` → `2160p`; **`Heat (1995) {edition-Remux}` → `Remux`**; `Heat (1995) {edition-4K}` → `4K`; `Heat (1995) - SDTV` → `""`; `Heat (1995) - x265` → `""`; `Heat (1995) - 1080p x265` → `1080p`; `Heat (1995) - 1080p REAL` → `1080p`; **`Heat (1995) - 1080p HDR` stays `1080p HDR`** (HDR and DV are deliberately not noise: an SDR and an HDR copy of one resolution must stay distinguishable in the picker); **`Heat (1995) - Director's Cut REPACK` → `Director's Cut`**.
  Run: `go -C server test ./internal/library/naming -run 'TestExtra|TestIsResolutionLabel|TestVersionLabels' -v`. Expected: FAIL.
- [ ] **Step 2: Implement `Extra` in `naming/extras.go`.** Fold `extraFolders` and `isExtraName` into it (a map folder → `ExtraType`, a suffix list → `ExtraType`), and compute `Folders` with `seasonFromDir`/`specialsRe`; `Classify` returns `ErrExtra` when `Extra` reports ok, as today, including the existing `len(dirs) > 0` rule for suffix names. Change the `ErrExtra` text to drop "(LIB-3)".
- [ ] **Step 3: Canonicalise in `versionLabel`.** Strip `junkRe` (proper/repack, defined in `classify.go`) from the chosen label first, **before** the `label != ""` fallback in `versionLabel` (`names.go:122`) so a label that strips to nothing falls through to `resolution(stem)`; also strip a bare `proper`/`repack` label (`junkRe` needs leading whitespace) and a `REAL` token (a case-sensitive whole word; label-level only; `junkRe` itself is unchanged: `REAL` is Servarr's `{Quality Real}`). **`{edition-…}` labels are not canonicalised** (trim and the 90-rune cut only), so a cut literally named `Remux`, `Web` or `4K` survives. For other labels: a label that equals, case-insensitively, one of Servarr's quality titles with no resolution (`Unknown`, `WORKPRINT`, `CAM`, `TELESYNC`, `TELECINE`, `DVDSCR`, `REGIONAL`, `SDTV`, `DVD`, `DVD-R`, `BR-DISK`, `Raw-HD`) reduces to `""`. Otherwise split it on space, `-`, `.` and `_` (so `Bluray-1080p` splits); a label whose parts are only source words (bluray, **webdl**, web, dl, webrip, hdtv, remux, bdrip, brrip, dvdrip, dvd, sdtv) and **noise words** (codec: x264, x265, h264, h265, hevc, avc, xvid), with **zero or more resolution words** that all resolve to one value (a *resolution word* is a token `resRe` matches, `4k` and `uhd` included), reduces to that value, or to `""` when there is none, and then falls through to `resolution(stem)` like the proper/repack strip. So `DVD`, `SDTV` and `x265` reduce to `""` (the resolution class, and a DVD → `Bluray-1080p` upgrade is adopted), `4K Remux` and `2160p UHD BluRay` reduce to `2160p`. **Trade-off:** a dash label such as `- Remux` or `- Web` reduces to `""` and the picker shows no label (or "Version 2") where it used to show "Remux". The nine existing rows keep passing. Other labels keep their text, cut to 90 characters on a rune boundary. **Source of the word list:** Servarr's Radarr and Sonarr wikis (Settings, Quality) name qualities `WEBDL-1080p`, `WEBRip-1080p`, `Bluray-1080p`, `Remux-2160p`, `HDTV-1080p`, `DVD`, `SDTV` (Sonarr: `Bluray-1080p Remux`); `{Quality Full}` can append `Proper`; `{Edition Tags}` follows Plex's `{edition-…}`; codec, HDR and audio tokens are bracketed (`[x264]`, `[DV HDR10]`, `[DTS 5.1]`) and `bracketRe` already strips them, (Radarr/Sonarr output); unbracketed codec words, as in scene-style names (`1080p x265`), are noise, and HDR/DV are kept on purpose. Case differences are resolved later, by `COLLATE NOCASE` in SQL.
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
  ALTER TABLE library_versions ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 1; -- order within a label group: flag target and the versionOrderSQL tie-break; never shown (Task 4 ranks at read time) and never used to decide which file is newer
  ALTER TABLE library_versions ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;     -- placement order within the item, never reused; 0 = not yet numbered by Task 3's BackfillVersions
  ALTER TABLE library_versions ADD COLUMN fresh INTEGER NOT NULL DEFAULT 0; -- placed since the last whole-root scan; see Task 3
  CREATE INDEX library_versions_fresh ON library_versions(file_id) WHERE fresh = 1;
  CREATE TABLE library_version_flags (
      version_id TEXT PRIMARY KEY REFERENCES library_versions(id) ON DELETE CASCADE,
      of_id TEXT NOT NULL REFERENCES library_versions(id) ON DELETE CASCADE,
      state TEXT NOT NULL CHECK (state IN ('flagged','kept'))
  ) STRICT;
  ```
  Existing rows keep `seq = 0` until Task 3's `BackfillVersions` numbers them.

- [ ] **Step 1: Write failing tests** (use the temp-media helper `TestRenamedFolderKeepsItemIDs` uses):
  - `TestExtraPlacedUnderMovie`: `Heat (1995)/Heat (1995).mkv`, `Heat (1995)/Featurettes/Making Of.mkv`, `Heat (1995)/Heat (1995)-trailer.mkv` → the movie has exactly **1 version** and **2 extras** (`featurette`, `trailer`), and **no file's `unplaced` column is set** after the scan. Discovery walks in lexical order, so `Featurettes/…` and `Heat (1995)-trailer.mkv` (`-` sorts before `.`) are placed before the movie file and are only placed by `adopt`; `Run.Unplaced` is a first-sight count and is not asserted.
  - `TestExtraTitleIsCapped`: a 300-character extra file name gives a title of 200 runes.
  - `TestSuffixExtrasAttachByFolderOrKey`: `Heat (1995)/Teaser-trailer.mkv` attaches to Heat as an extra titled `Teaser`; `Show (2019)/Season 01/Making Of-featurette.mkv` and `Show (2019)/Show (2019)-trailer.mkv` attach to the show.
  - `TestShowSeasonExtraAttachesToShow`: `Show (2019)/Season 01/S01E01.mkv` plus `Show (2019)/Season 01/Featurettes/x.mkv` → the extra's item is the show; the episode has 1 version.
  - `TestExtraBeforeParentIsAdopted`: scan only `Heat (1995)/Featurettes/a.mkv` → unplaced `extra`; add `Heat (1995)/Heat (1995).mkv`, scan → placed, `unplaced` NULL.
  - `TestExtraWithoutParentStaysUnplaced`: with a loose `Heat (1995).mkv` in the root (its folder is `''`), `Extras/x.mkv` stays unplaced `extra` and attaches to nothing; an extra in a folder mapped to two items (ambiguous) also stays unplaced; **no new item is created**.
  - `TestExtraNeverClimbsIntoAnotherMovieFolder`: `Marvel/Iron Man (2008).mkv` next to `Marvel/The Avengers (2012)/Behind The Scenes/x.mkv`, scanned so the extra is walked before `The Avengers (2012).mkv` exists → the extra stays unplaced `extra` (it is **not** attached to Iron Man) and the first scan after the Avengers file arrives adopts it under The Avengers; also with the Avengers file missing, it stays unplaced. Same check for a movie whose own file is absent, and for `Marvel/Thor (2011)-trailer.mkv` beside only `Marvel/Iron Man (2008).mkv` (stays unplaced; once `Marvel/Thor (2011).mkv` exists it attaches to Thor).
  - `TestFoldCarriesExtras`: a folder-rename fold and an admin merge (`Decide(..., true)`) leave the surviving item with both folders' extras.

  Run: `go -C server test ./internal/library -run 'TestExtra|TestSuffixExtras|TestShowSeasonExtra|TestFoldCarries' -v`. Expected: FAIL.
- [ ] **Step 2: Add the migration** above.
- [ ] **Step 3: `placeExtra`:** call `naming.Extra`; try its `Folders` in order, each an **exact** `library_folders` match for the root (never `''`, never an arbitrary parent: a movie's folder is its full folder path, so climbing would reach other movies). **For a suffix name with a year (`Key != ""`) only the row whose `scan_key` equals `movie:<Key>` or `show:<Key>` counts**, so `Marvel/Thor (2011)-trailer.mkv` never attaches to Iron Man, and in a shared folder it picks Thor out of several rows. A suffix name without a year (`Teaser-trailer.mkv`) has `Key == ""` and uses the folder's single row like any other extra. **The first folder with matching rows decides**, so a season folder without a match never stops the climb. **Residual risk:** a trailer whose title differs from its folder's (`Star Wars (1977)-trailer.mkv` in `Star Wars Episode IV - A New Hope (1977)/`) stays unplaced even when it is the only movie there; accepted for the shared-folder fix, and the admin sees it as unplaced. Exactly one item → insert the extra (`ON CONFLICT(file_id) DO UPDATE` type, title cut to 200 runes, item), **set `library_files.unplaced = NULL`**, return true; several → false; none in any → false.
- [ ] **Step 4: `adopt`, extras part:** select the root's present files with `unplaced = 'extra'`, call `placeExtra` for each, in transactions of 500. Call `adopt` right after `settle` at `scan.go:232` so an extras folder walked before its movie is adopted in the same scan. Shortcut comment: `Run.Unplaced counts a file when first seen unplaced and isn't decremented by adoption, fine because the file's own unplaced column is the truth; if the admin view ever shows the run counter, have adopt return a count for Scan to subtract.`
- [ ] **Step 5: `moveInto`:** add `UPDATE OR IGNORE library_extras SET item_id = ? WHERE item_id = ?` to its statement list (versions already move there; flags follow their versions by key). Task 3 adds the renumbering call to this function.
- [ ] **Step 6: Run** the same command plus `go -C server test ./internal/library`. Expected: PASS.
- [ ] **Step 7: Commit** `feat(library): place extras under their movie or show (LIB-3)`.

---

### Task 3: Version identity, same-label numbering and admin flags

**Files:**
- Create: `server/internal/library/versions.go`, `server/internal/library/backfill.go`
- Modify: `server/internal/library/scan.go` (`version` moves to `versions.go`; `applyFiles`' skip rule at `:400-402`), `server/internal/library/duplicates.go` (`moveInto`), `server/cmd/lisimflix/serve.go` (one `BackfillVersions` call before the scheduler starts; a failure keeps the scheduler from starting and makes `/health/ready` report not ready (as storage failures do, `serve.go:127-131`), so scans never run against `seq = 0` rows while the rest of serve keeps working), and the expectations that flip from `v2` to `v1` (a rename now ends with the old version; a null label counts as the same label): `scan_test.go:205-208` (`TestMovedFileKeepsItsItem`), `duplicates_test.go:121` (`TestRenamedFolderKeepsItemIDs`) and `duplicates_test.go:281` (`TestMergedFolderCanBeRenamed`)
- Test: `server/internal/library/versions_test.go`, `backfill_test.go`, `server/cmd/lisimflix/main_test.go`

**Interfaces:**
- Consumes: the Task 2 migration (`ordinal`, `seq`, `fresh`, `library_version_flags`), `adopt`.
- Produces:
  ```go
  func (s *Store) version(ctx context.Context, tx *sql.Tx, itemID, fileID, label string) error // replaces the scan.go one; same call sites
  func (s *Store) adoptVersions(ctx context.Context, rootID string, full bool) error            // called from adopt, after settle
  func (s *Store) BackfillVersions(ctx context.Context) (versions int, err error)                // one-time, idempotent (the Backfill rule below)
  func renumber(ctx context.Context, tx *sql.Tx, into string, moved []string) error            // called from moveInto; renumbers ONLY the moved-in versions
  type VersionFlag struct{ VersionID, OfID, ItemID, Title string; Label *string } // Label is the raw stored label, not displayLabel (the picker's read-time rank differs from the stored ordinal)
  func (s *Store) VersionFlags(ctx context.Context, libID string) ([]VersionFlag, error)  // Planned: LIB-2d (the #104 duplicates view)
  func (s *Store) KeepVersions(ctx context.Context, versionID string) error              // Planned: LIB-2d
  func (s *Store) RelabelVersion(ctx context.Context, versionID, label string) error     // Planned: LIB-2d; 1–90 characters after trimming, else an error
  func displayLabel(label *string, n int) *string // "1080p (2)", cut so the whole stays ≤ 100 characters; a null label with n = 2 shows "Version 2"; n = 1 shows the label as is. n is the READ-TIME rank among the versions actually listed (Task 4), never the stored ordinal: ordinals are never closed up, so a hidden sibling would leave a lone "1080p (3)"
  ```

**The rules**
- **At placement** (`version`, runs inside the walk): (1) a row for `(item, file)` exists → nothing, so a file replaced in place keeps its version. (2) Otherwise insert a version with `fresh = 1`, `seq = 1 + max(seq)` over **all the item's versions** (never reused: placement order, so the newer file always has the higher `seq`) and `ordinal = 1 + max(ordinal)` over the item's same-label versions (case-insensitive, null equals null; stored for ordering and flags, never shown), plus a `flagged` row pointing at the group's **flag target** when the ordinal is above 1. **Flag target** of a version V (used here, in `renumber`, the backfill and the leave-group rule): the group's lowest-ordinal version **below V's ordinal** whose file is present, else the lowest-ordinal version below V's ordinal. "Below" keeps flags acyclic (no self or mutual flags) and changes nothing at placement or in `renumber`, where V always has the highest ordinal. A flag against a dead version would be hidden by `VersionFlags`, so two live duplicates would never reach the admin (the item left with one dead and two present same-label copies). Adoption cannot happen here: during the walk the replaced file is still `present`.
- **After the walk** (`adoptVersions`, after files are marked missing and `settle` has folded renames): a pair is **F**, a `fresh` version whose file is `present`, and **N**, another version of the same item (fresh or not) whose file is **not present** (missing or retired) and whose **`seq` is lower than F's** (F is the newer file; without this an older live copy would be adopted into a missing newer sibling and lose its own resume), in F's **adoption class** (decision 2). Pairing runs in **two passes**, each adopting only when one-to-one (F has exactly one candidate N and N exactly one candidate F; anything else stays for review): **pass 1 pairs exact labels** (case-insensitive, null equals null); **pass 2 pairs by adoption class among the versions still unpaired**, so a renamed folder holding a 1080p and a 2160p copy pairs each exactly before any resolution upgrade is considered. **Adoption is ordered** (deleting F before pointing N at F's file avoids `UNIQUE (item_id, file_id)`). **Same label** (case-insensitive): flags with `of_id` = F are repointed to N, F is deleted (its own flag cascades away), then N's `file_id` becomes F's file; N keeps its label and flag and takes the **lower** of its and F's ordinals. **Labels differ:** **first**, the leave-group rule for N's old group (below); then N's own flag is deleted, flags with `version_id` = F or `of_id` = F are repointed to N, F is deleted, then N's `file_id` becomes F's file and **N takes F's label and ordinal**. In both cases **N takes F's `seq` and `fresh`** (free once F is gone), so the `seq` rule still holds for the newer file and N stays eligible until the next whole-root scan, and N keeps its ID. Afterwards any flag whose `of_id` equals its own `version_id` is deleted. **Leave-group rule** (N changes label in adoption, or `RelabelVersion`): let H be the lowest-ordinal version still in N's old label group, N excluded. If there is none, nothing to do. Otherwise delete H's own flag, set H's ordinal to 1, and repoint every other flag in that group whose `of_id` = N to **that version's own flag target** (N excluded, so never a dead H while a present lower version exists), so no version is flagged against itself and no two versions are flagged against each other. `RelabelVersion` applies this rule and the self-flag cleanup, and does nothing when the new label equals the old one case-insensitively. Ordinals are stored order numbers, never shown (Task 4 ranks the listed versions at read time): a gap left by a leaving version (C keeps ordinal 3 beside a head at 1) is accepted, shows nothing to users, and `TestAdoptionOutOfGroupRepointsFlags` asserts the stored ordinals only. The query **starts from the root's `fresh` versions** (the partial index `library_versions_fresh` serves it), not from its non-present files: retired file rows are never deleted, so that set only grows. It works in batches of 500. **A whole-root scan (`full`) then clears `fresh`** for the root's versions; a scoped scan keeps it, so adoption doesn't depend on which scope a watcher visits first, and a version placed by one scoped scan can be replaced by a later one (`fresh` marks the new file, never the old one). With `FullScanEvery` off, whole-root scans still run at startup or reload, on a manual `ScanRoot`, on a lost-watch recheck, on a watcher overflow and when scoped scans coalesce, and each clears `fresh`.
- **A merge or rename fold** (`moveInto`): version IDs are random, so the order of two colliding versions can't come from them. `moveInto(into, from)` reads `from`'s version IDs before the move, runs `UPDATE OR IGNORE`, then takes the moved set as those IDs **now on `into`** (an ignored version stays on `from` and is deleted with it). `renumber(into, moved)` processes **only that set**, in their `seq` order: each gets `seq = max(seq on into) + 1`, and each whose ordinal is already taken in its label group in `into` gets `max + 1` and a `flagged` row pointing at the group's flag target (Placement). The flag insert is an **upsert** (`ON CONFLICT(version_id) DO UPDATE` sets `of_id` and returns the state to `flagged`): `version_id` is the key, and a moved version that already has a `kept` flag would otherwise fail `settle` on every scan. `into`'s existing versions never change. The rename case then finishes in `adoptVersions` (the folded-in versions are `fresh` with the higher `seq`), leaving each old version with its old ordinal and no `(2)`.
- **A returning file:** adoption points the old version at the replacement file, so an original file that comes back (missing → present, `unplaced` NULL) has no version row. In `applyFiles`, a file that **returns** (`state != 'present'`) and has no `library_versions` or `library_extras` row is placed again instead of skipped; files that never left pay no extra query.
- **Backfill** (`BackfillVersions`, Go, not SQL). Placed files are never placed again (`scan.go:400-402`), so stored labels predate Task 1's canonical labels, and every rename before LIB-3 left a dead duplicate pair (`scan_test.go:205`). It runs once at serve start, in batches of 500 items per transaction, and picks up only versions with `seq = 0`, so it is idempotent and a crash resumes. Per item: (1) recompute each version's label from its file's stored `path` with `naming.Classify(...)` (the call `place` makes); keep the stored label if the path no longer classifies. (2) Assign `seq` in order **missing or retired files first, then `id`**, and number `ordinal` per label group (case-insensitive, null equals null, as `version` does) in that same order, so a dead predecessor gets the lower values. (3) Insert a `flagged` row for every version with `ordinal > 1`, pointing at its group's flag target (Placement; a present copy is flagged against a present one when there is one). (4) Set `fresh = 1` on each present version that has a lower-`seq` version in its adoption class (decision 2) whose file isn't present, so the **first whole-root scan adopts** the dead pair and keeps the old version ID. `VersionFlags` shows a flag only while both files are present, so a dead pair never reaches the admin.
- `VersionFlags` lists a flag only while both versions' files are present. **Accepted limit** (`shortcut:` the target is chosen when the flag is written): if a flag's target later goes missing and isn't adopted, its live duplicates stay unflagged until the target returns or a rename/merge reflags them. Revisit in LIB-2d (compute `OfID` at read time) if admins report missed duplicates.

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
  - `TestFlagTargetPrefersPresentVersion`: a label group of V1 (ordinal 1, file missing) and V2 (ordinal 2, present, flagged against V1), then a third same-label present file V3 arrives → V3 (ordinal 3) is flagged against **V2**, and `VersionFlags` lists it with the item's title; with V2 also missing, V3 is flagged against V1 (fallback). The same through `renumber` (a merge). `BackfillVersions` on D (dead, ordinal 1), P1 and P2 (present) flags P1→D and P2→P1 and **never two versions against each other**. The leave-group case: D (1, dead), P1 (2→D), P2 (3→P1), P3 (4→P1), then `RelabelVersion(P1)` → D becomes ordinal 1 (head); P2 → D (no present version below it, fallback; hidden, which is fine because P2 is then the only live copy besides P3) and P3 → P2 (listed), with no mutual flags and none against P1.
  - `TestAmbiguousAdoptionMakesNewVersion`: both copies missing, one new file arrives → a third version, ordinal 3, old versions untouched.
  - `TestResolutionUpgradeKeepsVersionID` (**flips the old `TestLabelChangeMakesNewVersion`**): a movie `Heat (1995) - 720p.mkv` replaced by `… - 1080p.mkv`, a `Heat (1995) - DVD.mkv` replaced by `… - Bluray-1080p.mkv`, an episode `Show - S01E01 - 720p.mkv` replaced by `… - 1080p.mkv`, an unlabelled episode replaced by a `1080p` one, and a `1080i` episode replaced by `1080p` → in each case one version, **same ID**, on the new file, **label now the new one**, ordinal 1, no flag.
  - `TestAdoptionOutOfGroupRepointsFlags`, `TestRelabelOutOfGroupRepointsFlags`: a label group with versions A (ordinal 1), B (2, flagged against A); A leaves the group (adopted onto a different label, or relabelled) → with B (2) and C (3) both flagged against A, A leaving makes B the head (ordinal 1, no flag) and C flagged against B, with no mutual flags; when B is alone it becomes ordinal 1 with no flag (no `720p (2)` next to nothing); a later same-label placement still flags against a present target, never `of_id` NULL.
  - `TestAdoptionNeverFlagsAVersionAgainstItself`: after same-label and label-changing adoption, no `library_version_flags` row has `version_id = of_id`, and no primary-key conflict occurs when N already had its own flag.
  - `TestResolutionUpgradeIntoNumberedSibling`: a movie holds a present `1080p` V3 and a missing `720p` N; a second `1080p` file F arrives (numbered `1080p (2)`, flagged against V3) → N is adopted onto F's file, takes label `1080p` and ordinal 2, and its flag points at V3; F is gone, V3 is untouched.
  - `TestRenamedFolderWithTwoResolutionsKeepsBothIDs`: a folder holding a 1080p and a 2160p file is renamed → both versions keep their IDs (pass 1 pairs exact labels), no `(2)`, no flag.
  - `TestCutChangeMakesNewVersion`: `{edition-Theatrical}` replaced by `{edition-Director's Cut}` → two versions (the old file missing), nothing adopted. The listing rule is tested in Task 4.
  - `TestBackfillVersionsRecomputesLabels`: versions on files `Heat (1995) - 4K.mkv`, `… - 1080p REPACK.mkv`, `… - Bluray-1080p.mkv`, their stored labels set stale by SQL, end with `2160p`, `1080p`, `1080p`, then numbered, so the last two are ordinals 1 and 2.
  - `TestBackfillVersionsNumbersDeadPairFirst`: an item with a missing old `1080p` version and a present new one (a rename before LIB-3), ids chosen so the present one sorts first, ends with the **missing one at `seq` 1 and ordinal 1**, the present one at 2 with a `flagged` row (not listed by `VersionFlags`) and `fresh = 1`; the first whole-root scan adopts it, keeping the old version ID, ordinal 1, no flag. A pre-LIB-3 720p → 1080p pair is adopted the same way.
  - `TestServeKeepsSchedulerStoppedWhenBackfillFails` (`server/cmd/lisimflix/main_test.go`, beside `TestSchemaTooNewIsNotReady`): with `BackfillVersions` forced to fail (through a seam the task adds; an unreadable database would fail storage first), the scheduler is not started, `/health/ready` is not ready with the backfill error, and the process still answers.
  - `TestBackfillVersionsIsIdempotent`: a second run changes nothing and returns 0; versions placed by `version` (`seq` 1 or more) are never touched.
  - `TestDecideMergeRenumbersSameLabel`: two coexisting folders each with one `1080p` file, admin merge → the folded-in version gets ordinal 2 (the surviving item's version keeps ordinal 1 whichever version ID sorts first, run with several random IDs), one flagged row.
  - `TestReturnedOriginalIsPlacedAgain`: after the upgrade above the original file comes back → it has a version (ordinal 2, flagged), the adopted one keeps its ID.
  - `TestKeepVersionsClearsFlag`, `TestRelabelResetsOrdinal`: relabelling `1080p (2)` to `Director's Cut` sets ordinal 1 and removes the flag; relabelling to an existing label keeps it flagged with the next ordinal; an empty label is an error; 100-character names stay valid after the suffix (`displayLabel`).
  - `TestDisplayLabel`: `displayLabel(nil, 1)` → `nil`, `displayLabel(nil, 2)` → `"Version 2"`, `displayLabel(&"1080p", 2)` → `"1080p (2)"`, a 100-character label stays ≤ 100 with the suffix (pure function; the read-time rank is tested in Task 4).
  - **Change the existing expectations** `TestMovedFileKeepsItsItem` (`scan_test.go:205-208`: `movie Heat - v2` → `v1`), `TestRenamedFolderKeepsItemIDs` (`duplicates_test.go:121`) and `TestMergedFolderCanBeRenamed` (`:281`): a rename now ends with one version, the old ID. **Assert the surviving version's stored `ordinal` is 1** (the visible label is checked through `itemVersions` in Task 4), not only the count, and repeat the rename several times so a random-ID ordering can't hide a wrong ordinal.

  Run: `go -C server test ./internal/library -run 'TestUpgraded|TestScoped|TestInPlace|TestSameLabel|TestRenamedSecond|TestRenamedFolder|TestMergedFolder|TestFlagTarget|TestAmbiguous|TestNewerCopy|TestOlderCopy|TestAdoption|TestRelabelOutOfGroup|TestResolution|TestCutChange|TestBackfillVersions|TestDecideMerge|TestReturned|TestKeep|TestRelabel|TestDisplayLabel|TestMovedFile' -v`. Expected: FAIL (the regression guard passes).
- [ ] **Step 2: Implement** `version`, `displayLabel`, `adoptVersions` (called from `adopt`), `BackfillVersions` (`backfill.go`) with its one call in `serve.go`, `renumber(into, moved)` (called from `moveInto`, after the versions move), and the returning-file rule in `applyFiles`.
- [ ] **Step 3: Implement** `VersionFlags` (flagged rows of the library, ordered by the item's `sort_title`), `KeepVersions` (state `kept`, `ErrNotFound` without a flagged row) and `RelabelVersion`. Shortcut comment: `a relabelled version isn't adopted on a later rename unless the new label is in the same adoption class as the file's parsed label, when an admin-locked label field exists upgrade.`
- [ ] **Step 4: Run** the same command plus the whole package, plus `go -C server test ./cmd/lisimflix -run TestServeKeepsSchedulerStopped`. Existing tests whose version counts change because adoption now replaces a duplicate (for example any other `v2` expectation) are updated in this task; list each in the PR.
- [ ] **Step 5: Commit** `feat(library): stable version identity and flagged same-label copies (LIB-3)`.

---

### Task 4: Episode segments and the version/extra read model (needs LIB-2c's code, Decision 5)

**Files:**
- Create: `server/internal/library/segments.go`
- Modify: `server/internal/library/detail.go` (LIB-2c's `itemVersions`, extended in place), `server/internal/library/browse.go` (`versionOrderSQL`: `v.ordinal` before `v.id`, nothing else; decision 5)
- Test: `server/internal/library/segments_test.go`, `server/internal/library/browse_test.go`

**Interfaces:**
- Consumes: `probe.Result{DurationMs, Chapters}`, `library_versions`, `library_extras`, `displayLabel`, **LIB-2c's `itemVersions(tx, itemID)`**, which this task extends in place (no second path).
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
      Label *string      // displayLabel(label, read-time rank) applied
      FileInfo
      Segment *contracts.Segment
  }
  type ExtraRow struct {
      ID, ItemID, Title string
      Type naming.ExtraType
      FileInfo
  }
  func SegmentOf(chapters []probe.Chapter, durationMs int64, index, count int) *contracts.Segment
  func itemVersions(ctx context.Context, tx *sql.Tx, itemID string) ([]VersionRow, error) // LIB-2c's function (keep its actual parameter list), extended: displayLabel applied, Segment filled, unavailable versions left out (decision 2)
  func itemExtras(ctx context.Context, tx *sql.Tx, itemID string) ([]ExtraRow, error)    // new, beside it; a movie's or show's extras, none for a season or episode
  ```
- `SegmentOf` returns a range only when **all** hold: `count >= 2`; `len(chapters) == count`; each chapter lasts **at least 10 minutes** (Decision 1); the first chapter starts at 0; each starts where the previous ended; the last ends within 1000 ms of `durationMs`. `index` is the episode's position (0-based) among the file's episodes ordered by `idx`. Anything else returns nil.
- `itemVersions` fills `Segment` only for an episode whose version shares its file with at least one other episode **of the same season** (same `parent_id`); it never orders rows (LIB-2c decision 10 plus the ordinal tie-break in Step 2 do). **The "(n)" suffix is computed here at read time, after the hide rule:** `n = ROW_NUMBER() OVER (PARTITION BY lower(v.label) ORDER BY <versionOrderSQL>)` over the versions actually listed (null labels form one group), passed to `displayLabel`; ordering by `versionOrderSQL` (which now ends `v.ordinal, v.id`) keeps the rank in step with the listing, so the first-listed copy of a label always reads plain. **The hide rule must sit in the same query's `WHERE`** (an `EXISTS` on a present sibling on a root that isn't offline) or in a subquery under the window, never as a Go filter afterwards, or hidden rows would still use up rank numbers. The stored `ordinal` is kept for ordering and flags but is never shown, so a survivor whose siblings are hidden or dead reads plain "1080p" (also the pre-LIB-3 backfill case: 1080p, then REPACK, then PROPER leaves two dead same-label versions the one-to-one rule won't adopt). It also **leaves out a version whose file is missing or retired when the item has a present version on a root that isn't offline** (the rule `itemExtras` uses; decision 2), so an unmatched cut change doesn't leave a dead entry. If LIB-2c's `VersionRow` already carries these fields, reuse it rather than redefining.
- `itemExtras` leaves out extras whose file is missing or retired, except on an `offline` root, so a folder rename doesn't list two copies with one unavailable.

- [ ] **Step 1: Write failing tests:**
  - `TestSegmentOf` table: 2 episodes, chapters `[0–1_500_000, 1_500_000–3_000_000]`, duration 3_000_000 → `{0,1_500_000}` and `{1_500_000,3_000_000}` (unequal lengths are fine when the chapters say so); duration 3_000_500 → still matches; 2 episodes with 3 chapters → nil; a 10 s gap → nil; first chapter starting at 5 s → nil; last chapter ending 30 s short → nil; a chapter shorter than 10 minutes → nil; no chapters → nil; `count == 1` → nil; `durationMs == 0` → nil. **A 3_000_000 ms file with no chapters and 2 episodes gives nil for both, never 1_500_000.**
  - `TestItemVersionsSharedFile`: episodes 2 and 1 of one season on one file (inserted in that order), a probe with two chapters → episode 1 gets the first range, episode 2 the second. Episodes in different seasons sharing a file → nil.
  - `TestItemVersionsStaleProbeHasNoSegment`: the file changed after the probe (`probe_size` differs) → `Probe` nil, `Segment` nil.
  - `TestItemVersionsMovieHasNoSegment`; `TestItemVersionsHidesUnavailableWhenAPresentOneExists` (a `{edition-Theatrical}` → `{edition-Director's Cut}` change lists only the Director's Cut while its root is available; both when the root is offline; every version when none is present); `TestItemExtrasForShow`; `TestItemExtrasOfflineRoot` (`RootState` offline, `Present` still true); `TestItemExtrasSkipMissingAfterRename`.

  - `TestItemVersionsLoneSurvivorShowsPlainLabel`: (a) versions `1080p` (ordinal 1, file deleted) and `1080p` (ordinal 2, present) → the survivor lists as "1080p", not "1080p (2)"; (b) the backfill case: three same-label versions, ordinals 1 and 2 dead, ordinal 3 present → "1080p"; (c) two present same-label versions still list "1080p" and "1080p (2)", ranked by ordinal then id; (d) a null label with a dead sibling lists with no "Version 2".
  - `TestVersionOrderBreaksTiesByOrdinal` (`browse_test.go`): two same-label versions, both present, same `f.resolution`, different file `duration_ms`, with IDs **chosen** so the ordinal-2 version has the lower `id` → the ordinal-1 version is the default, and `listItems`' `durationMs` comes from its file (so the check tells them apart); then swap the IDs and get the same result.

  Run: `go -C server test ./internal/library -run 'TestSegmentOf|TestItemVersions|TestVersionOrder|TestItemExtras' -v`. Expected: FAIL.
- [ ] **Step 2: Implement** `SegmentOf` as a pure function, add `v.ordinal` before `v.id` in `versionOrderSQL` (and update its doc comment, "then by version ID"), then the two reads (`itemVersions` extended in place, `itemExtras` new; one query each: version or extra joined to file and root; `json.Unmarshal` of `probe_json` only when `probe_size`/`probe_mtime_ns` equal the file's size/mtime). Shared-file detection is `SELECT v.item_id FROM library_versions v JOIN library_items i ON i.id = v.item_id WHERE v.file_id = ? AND i.kind = 'episode' AND i.parent_id = ? ORDER BY i.idx`.
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
  // ItemDetail gains: Extras []Extra `json:"extras"` // movies and shows; empty otherwise (populated by Task 7)
  ```
  `ItemDetail.MarshalJSON` adds `d.Extras = nonNil(d.Extras)`. OpenAPI: an `ExtraType` enum (the eight values), `Extra` (`additionalProperties: false`, all four fields required, `title` 1–200 characters), `ItemDetail.extras` required. `ItemDetail` already has one `if/then` (versions empty for shows and seasons): **turn it into an `allOf`** like `ItemSummary` has, with a second entry limiting `extras` to `maxItems: 0` for seasons and episodes.

- [ ] **Step 1: Write failing tests:** the Go round trip of the four existing fixtures with `"extras": []` added and of the new `item-episode-chaptered.json` (episode 2 of a two-episode file with `segment {startMs:1500000, endMs:3000000}`); add `checkSchemaEnum(t, "ExtraType", ExtraTrailer, ExtraFeaturette, ExtraBehindTheScenes, ExtraDeletedScene, ExtraInterview, ExtraScene, ExtraShort, ExtraOther)` to `TestCatalogueEnumsMatchOpenAPI` (`catalogue_test.go:42-49`); `Extra.type` `$ref`s the separate `ExtraType` schema, so `checkEnum` on `Extra.type` would read no enum; web invalid variants: `["extra with unknown type", "item-movie.json", d => { d.extras[0].type = "bonus"; }]`, `["path in extra fileName", "item-movie-admin.json", d => { d.extras[0].version.fileName = "/media/x.mkv"; }]`, `["extras on an episode", "item-episode-shared-file.json", d => { d.extras = catalogue("item-movie.json").extras; }]`, `["extra without version", "item-movie.json", d => { delete d.extras[0].version; }]`. Run: `go -C server test ./internal/contracts` and `npm --prefix web run test:contracts`. Expected: FAIL.
- [ ] **Step 2: Implement** the Go types, the OpenAPI schemas, then `npm --prefix web run generate:api`. Rewrite the `MediaVersion.label` description (`openapi.json:737`, "Null when the item has one unnamed version") to "null for the first unnamed version; later copies read 'Version n' or 'label (n)', n counted over the listed versions". Align the `ItemDetail.versions` description (`openapi.json:715`, "Empty … for a movie or episode with no available file") with decision 2: unavailable versions are listed only when none is available or the root is offline. Fixtures: `item-movie.json` gets two extras (a trailer, and an offline featurette with `available: false`), both with `fileName: null`; `item-movie-admin.json` the same with bare file names; `item-show.json` one featurette; `item-episode-shared-file.json` `extras: []`. Add `extras: []` to the literal at `web/tests/fake-api.ts:160`.
- [ ] **Step 3: Update** `testdata/contracts/catalogue/README.md` rows (the extras and the chaptered episode).
- [ ] **Step 4: Run** both commands again plus `npm --prefix web run lint && npm --prefix web run build`. Expected: PASS and `git diff --exit-code -- web/src/api/schema.d.ts` clean (the Makefile's check).
- [ ] **Step 5: Commit** `feat(contracts): extras on the item detail (LIB-3)`.

---

### Task 6: Reconciliation fixtures and docs

**Files:**
- Create: `server/internal/library/reconcile_test.go`
- Modify: `docs/superpowers/plans/2026-10-24-catalogue-plans-map.md` (the LIB-3 row), `README.md` only if its status/layout lines mention extras or versions

- [ ] **Step 1: Write `TestVersionCutReconciliation`** — one scenario over real temp media roots (a movies root and a TV root) and several whole-root scans, asserting the catalogue after each:
  1. `Heat (1995)/Heat (1995) {edition-Theatrical}.mkv`, `… {edition-Director's Cut}.mkv`, `Heat.1995.2160p.mkv` in one folder → three versions labelled `Theatrical`, `Director's Cut`, `2160p`, none flagged.
  2. The Theatrical file is replaced by `… {edition-theatrical} REPACK.mkv` (same label, different case) → after the scan, the same version ID, still three versions (adoption runs after the walk).
  3. A second `2160p` file appears → numbered `2160p (2)`, one flag; `KeepVersions` clears it.
  4. `Heat (1995)/Featurettes/Making Of.mkv` and `Heat (1995)-trailer.mkv` → two extras and no new version.
  5. In the TV root, `Show (2019)/Season 01/Show - S01E01-E02.mkv` with a stored two-chapter probe (25 minutes each) → both episodes have a `Segment`; without chapters, both `nil`.

  Run: `go -C server test ./internal/library -run TestVersionCutReconciliation -v`. Expected: PASS once Tasks 1–5 are in.
- [ ] **Step 2: Docs.** In the plans map, LIB-3 is a segment of the `lib-1-2-3-scanner` row, not a row of its own: in that row's text change "**LIB-3**: version/extras parts of T6 + T9" to "**LIB-3**: claimed (#160), planned in `2026-11-02-lib-3-versions-extras.md`". README check: `grep -n -i 'extra\|version\|cut' README.md`; update a line only if it now misrepresents the branch, and tick the PR template's README box accordingly.
- [ ] **Step 3: Planned tags.** Every new exported function with no production caller carries `// Planned: LIB-2d (<what will call it>)` (the admin methods). `grep -rn 'Planned: LIB-3' server web` must print nothing (a `Planned: LIB-3` tag would name LIB-3 as its own caller).
- [ ] **Step 4: Full check.** From a checkout with Go available (the repo's `mise.local.toml`): `make check`. Expected: exit 0. Record the local result in the PR body.
- [ ] **Step 5: Commit** `test(library): version and cut reconciliation fixtures (LIB-3)`.

### Task 7: Wire the catalogue and the `Resolver` (after LIB-2c's code merges)

LIB-3 doesn't reach main without this task (Decision 5): without it the picker shows identical unnumbered "1080p" labels.

**Files:**
- Modify: `server/internal/library/detail.go` (LIB-2c's `Store.Item`: fill `ItemDetail.Extras` from `itemExtras` and use the extended `itemVersions`), `server/internal/library/resolver.go` (the extras branch)
- Test: `server/internal/library/detail_test.go`, `resolver_test.go`

**Interfaces:**
- Consumes: Tasks 2–5, LIB-2c's `Store.Item`, `Resolver`, `Access`/`Grant` and its `BenchmarkAPIRoutes`.
- Produces: no new exports. The `Resolver` resolves an extra: the playback `itemId` equals the `versionId` equals the extra's own ID (Decision 4), access is checked through the **parent item's library**, the extra's file must be present and its root not offline. The extras lookup runs only after the version lookup misses, so `Resolve` stays one indexed query per range request (LIB-2c decision 11). Anything else, including an extra ID paired with a different `versionId`, is refused as LIB-2c already refuses a foreign version.

- [ ] **Step 1: Write failing tests:** `TestItemDetailListsExtras` (a movie with a trailer and an offline featurette, and a show with one featurette, in the contract shape; a season and an episode have none); `TestItemDetailShowsNumberedAndSegmentedVersions` (a numbered same-label version and a shared-file episode with a `segment`: LIB-3's own integration proof); `TestResolverPlaysExtra`; `TestResolverRefusesExtraInHiddenLibrary` (a profile without access to the parent's library); `TestResolverRefusesExtraWithOtherVersionID`; `TestResolverRefusesMissingExtraFile`. Run: `go -C server test ./internal/library -run 'TestItemDetail|TestResolver' -v`. Expected: FAIL.
- [ ] **Step 2: Implement** the wiring and the branch. Drop any `Planned: LIB-2c` tag left on Tasks 2–5 code, then check `grep -rn 'Planned: LIB-2c' server | grep -i 'extra\|version'` prints nothing for LIB-3's code (Task 7 is the caller); the integration proof in the header is now this task's tests.
- [ ] **Step 3: Rerun** LIB-2c's `BenchmarkAPIRoutes` `getItem` case and record the before/after in the PR (extras and segments add reads to the hot path).
- [ ] **Step 4: Run** the same command plus `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(library): serve versions, extras and extra playback (LIB-3)`.

## Self-review

- **Spec coverage:** §3.1 versions grouped (Tasks 1, 3), picker data (Task 4 rows; ordering is LIB-2c's plus LIB-3's `v.ordinal` tie-break), extras folders (Tasks 1–2, 5); §3.8 no equal split (Task 4); §10.1 stable identity (Task 3); fixtures required by the §9.4 row (Task 6); the "segment null until LIB-3" promise in LIB-2c decision 10 (Task 4).
- **Not in this plan:** the catalogue HTTP handler itself and the picker UI (LIB-2c, UI-2), the admin screens for flags (LIB-2d, with #104), provider artwork or titles for extras (META).
- **Type consistency:** `ExtraType` spellings are the same in `naming`, the migration `CHECK`, the Go constants and the OpenAPI enum; `displayLabel` is used by Tasks 3 and 4; `adopt` (Task 2) calls `adoptVersions` (Task 3); `Extra.id == Extra.version.id` in Task 5 and Decision 4.
- **Review Focus:** each of the five lines has a named test in its owning task.
- **Proportion:** seven tasks, test names and signatures only. The spec row is one line; the plan decides the identity, evidence and contract rules the spec leaves open.
- **Revised after Lammy's review (#161, 2 Nov):** decision 2 now adopts across resolution-only labels and hides unavailable versions after a cut change (Pete signed off 4 Nov; it replaces "label match only"); numbering and the dead-pair adoption moved from SQL to the Go `BackfillVersions`; `placeExtra` matches exact folders and climbs only through season/Specials folders (`TestExtraNeverClimbsIntoAnotherMovieFolder`); decision 5 is decided on #77 and Task 7 wires `itemVersions`, extras and the `Resolver`. A fresh-reviewer pass then fixed: a per-item `seq` (placement order) so resolution upgrades can be adopted while `ordinal` stays display-only; adoption taking the new label, ordinal and flag; two adoption passes (exact labels, then class); `1080i` in the class; backfill and its tests moved to Task 3 with the call in `serve.go`; `Key` matching for `-trailer` names. Non-blocking notes (filed by Lammy as one issue) are not addressed here.
- **Revised after Lammy's second review (#161, 4 Nov):** (1) LIB-3, not LIB-2c, adds `v.ordinal` before `v.id` in `versionOrderSQL` (decision 5, Global Constraints, Task 4 file scope and `TestVersionOrderBreaksTiesByOrdinal`). (2) The "(n)" suffix is a read-time rank over the listed versions (Task 4, `TestItemVersionsLoneSurvivorShowsPlainLabel`), so a hidden or dead sibling can't leave a lone "1080p (2)"; `displayLabel` takes that rank, and the stored ordinal stays internal.
