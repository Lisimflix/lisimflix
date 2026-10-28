# LIB-2a Catalogue Store and Offline-Safe Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn LIB-1's discovered and probed files into a persistent catalogue of movies, shows, seasons, episodes and versions. Keep it in step with each library folder, never mass-retiring entries because a disk was unmounted, emptied or only partly read.

**Architecture:** LIB-1's `library` package gains the catalogue:
- **A schema** (one migration on OPS-2's storage).
- **`Store`:** libraries, roots and runs.
- **`Scanner.ScanRoot`:** walks a root with LIB-1's `Discover`, writes files, items and versions in short batches, and marks files missing only after a complete walk of a reachable, non-empty root.
- **`Scanner.ProbeDue`:** probes stale revisions through LIB-1's `Queue`, with compare-and-set.
- **Retirement:** a preview plus an explicit `Retire` call.

A pure `library/naming` package turns paths into items from Plex/Radarr/Sonarr and anime names. Nothing is wired into `serve` yet, and there's no HTTP. LIB-2b adds the watcher, schedule and wiring, and LIB-2c the API and actual-scale checks.

**Tech Stack:** Go 1.27.3 standard library, `modernc.org/sqlite` through OPS-2's `storage`, and LIB-1's `library` and `probe` packages. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §3.1: naming and ID hints.
- §3.6: scan lifecycle, offline roots, retirement, stable files.
- §10.1–10.2: identities, short transactions.
- §13: SCAN-01, SCALE-01.
- §9.4: the LIB-2 row.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-2** (resilient catalogue, P0 / G1), slice **2a** of 3. LIB-2 is split into 2a (store, naming, scan engine), 2b (watcher, periodic/manual scans, status, `serve` wiring) and 2c (read API on CTR-2's shapes, admin actions, actual-scale SCALE-02/PERF-05). Lammy decided the split on 2026-10-27 so that each review stays reviewable (spec §8.4: small plans) |
| Issue | #77 (also closes #62) |
| Owner / reviewers | Lammy (claimant) / Pete (LIB-1 owner) and Ondo |
| Acceptance | **SCAN-01** module fixtures: a disconnected root, an empty root, a partial walk, a cancelled scan, the root returning, and interrupted-scan recovery. **SCALE-01** (module part): unchanged revisions are never re-probed, and each run records counters. §3.6 retirement (two successful scans at least 24 h apart; outages suspend it) and stable-file deferral. §3.1 names. Actual-scale qualification is LIB-2c |
| File scope | **New only:** `server/internal/library/naming/{names,classify}.go` + test, `server/internal/library/migrations/20261027120000_library_catalogue.sql`, `server/internal/library/{store,scan,probes,retire}.go` + tests. (The plans map's LIB-2 row is updated in this plan's PR.) No changes to existing code, `go.mod`, contracts or CI |
| Consumes | LIB-1 (#45, plan merged in #46): `library.Discover`, `File`, `Revision`, `Queue`, `NewQueue`, `Prober`, `Outcome`, `ErrNotStable`/`ErrChanged`/`ErrGone`, and `probe.Result`. OPS-2: `storage.DB`, `storage.Source`, `storage.Core`, `Migrate` |
| Produces | `naming.Classify`, `naming.Entry`, `naming.SortTitle`. `library.Migrations()`, `Store`, `NewStore`, `CreateLibrary`, `Library`, `MarkInterrupted`, `Scanner{Store, Media, Discover}`, `ScanRoot` → `Run`, `ProbeDue` → `ProbeRun`, `RetryProbe`, `RetirementCandidates`, `Retire`, `RetireAfter`, and the run/root state constants |
| Start dependency | **LIB-1's code merged.** Its plan is approved, but the code isn't on `main` yet, and every task here uses its package. The prototype applied LIB-1's plan code as its base |
| Standalone test | `go -C server test -race -count=1 ./internal/library/...`, then `make check` |
| Module proof | Temporary SQLite and media trees, plus a scripted prober. Mutation-checked (see Self-review) |
| Integration proof (not in this slice) | LIB-2b runs `ScanRoot`/`ProbeDue` from the watcher and schedule in `serve` with the real ffprobe; LIB-2c serves the catalogue. Record "module accepted" on #77 when 2a merges, and "integrated" after 2b |

## Global Constraints

- Go 1.27.3. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`. Run `make check` before pushing.
- **Read-only media (SAFE-01):** nothing here writes under the media root. Tests write only in `t.TempDir()`.
- Every file access goes through `os.Root` (the media root, then `OpenRoot` for a library folder), so paths can't escape.
- Migrations: one file, `YYYYMMDDHHMMSS_library_<what>.sql`, module `library`, `STRICT` tables with explicit `ON DELETE`. Never edit a merged migration.
- Write transactions stay short: at most 500 files per transaction (spec §10.2). Two writes are exceptions, because each is one indexed statement or a rename's worth of rows (#110): marking a complete scan's unseen files missing (one `UPDATE`), and `settle` folding the root's pending pairs. Measure both in LIB-2c's actual-scale run (SCALE-01, SCALE-04: transaction duration) and batch them if they show up.
- Code without a production caller yet carries `// Planned: <Feature ID> (<caller>).` (#60).
- Workflow:
  - Branch `feat/lib-2a-catalogue`. PR title `feat(library): catalogue store and offline-safe scans (LIB-2a)`.
  - Reviewers lisim and ondona. The author squash-merges after approval. Agents never merge.

## Decisions taken in this plan (please review)

1. **Package layout.** The catalogue lives in LIB-1's `library` package; spec §10.2 says `library` owns discovery and probe updates. Name parsing is a separate pure package, `library/naming`, because it has no I/O and lots of table tests.
2. **Item identity: names, plus a folder rule (Pete, #90).**
   - A movie's key is its normalised title plus year (`movie:heat|1995`); a show's is the same for the show. Seasons and episodes extend it (`…|s1|e2`, or `…|d2024-03-01` for daily shows). Each file also records the **folder** its name came from (`naming.Entry.Folder`), and `library_folders` maps each folder to its item.
   - **Two folders that exist at the same time are never merged by the scanner**, even when their names or ID hints match. Examples: a `Doctor Who` folder with no year (1963 or 2005?), or a remake. The new folder gets its own provisional item and a pending pair.
   - **After a completed scan, each pending pair is settled:**
     - **Rename or move:** if the existing item has no present file left, the new folder replaced the old one. It's folded in, keeping every item ID, so this case needs no admin.
     - **Two real folders:** otherwise both folders exist, and the pair is **flagged**.
   - **The admin decides each flag**, with `Decide(id, merge)`:
     - **Merge:** one item, with both folders' versions; matching episodes get both, and the rest move across.
     - **Keep separate.**
     - Either way the folder mapping is kept, so the flag doesn't return, and new files in a merged folder join the merged item. The admin screen is LIB-2c.
   - **Limit:** a move between two roots of one library is flagged rather than folded, because the old root isn't rescanned in the same run. The admin merges it.
3. **SCAN-01 rules:**

   | Situation | Run | Root | Files |
   |---|---|---|---|
   | Folder can't be opened | `failed` / `root_unavailable` | `offline` | unchanged |
   | Folder empty while the catalogue has files there | `failed` / `root_empty` | `offline` | unchanged |
   | Walk stops part-way (LIB-1's `Discover` errors) | `failed` / `enumeration_failed` | `degraded` | files seen are recorded; none marked missing |
   | Cancelled | `cancelled` | as before | files seen are recorded; none marked missing |
   | Complete walk | `completed` | `available` | unseen present files become `missing` (with `missing_since`); seen missing or retired files become `present` again |
   | Process stopped mid-scan | `interrupted` (at startup, `MarkInterrupted`) | `degraded` / `interrupted` | the next scan starts over. Scans are idempotent |

4. **Retirement is never automatic.**
   - A missing file becomes a candidate once a *successful* scan that started at least 24 h after `missing_since` still doesn't find it. Failed scans don't count, so an outage suspends retirement.
   - `Retire` re-checks every file it's given. Retired files keep their items and versions, and come back as `present` if they reappear. The admin preview and confirmation screen is LIB-2c.
5. **Probes.**
   - `ProbeDue` probes present files whose stored probe revision differs from their current size and mtime.
   - **Results:** saved only if the catalogue has that revision, or an older one (compare-and-set). An older one means the file changed since the last scan and is quiet now; the result records the newer revision, so the file isn't probed on every turn until the next scan (#109). A result for an older revision than the catalogue's is counted `Stale` and dropped.
   - **Only forward:** a "still changing" outcome may only move a file's recorded revision forward (`mtime_ns <= new`), so it can't overwrite a newer one a concurrent scan recorded (Pete's note on #90).
   - **Still changing:** `ErrNotStable` or `ErrChanged` records the newer revision and a not-before time of mtime + quiet period, so nothing spins. That fixes #62 without changing LIB-1.
   - **Errors:** kept, up to 500 bytes, for the admin list. They aren't retried until the file changes or `RetryProbe` clears them.
   - `probe_json` holds `probe.Result` as Go encodes it. It's internal; LIB-2c maps it to CTR-2's stream shapes.
6. **Multi-episode files.** Each episode of `S01E01E02` / `S01E02-E04` gets a version on the same file, matching CTR-2: same `fileId`, unknown segment. Ranges longer than 12 episodes are treated as a mis-tag, and only the first episode is used. The title in such a name labels the file, so the episodes keep placeholder titles until META-1 fetches real ones.
7. **Scope cuts, with owners:**
   - **NFO reading → META-1.** "NFO/provider hints" is in its feature row. ID hints in names (`{tmdb-949}`, `[anidb-17617]`) are read here.
   - **Extras → LIB-3 (G2).** Files in `Featurettes/`, `Trailers/` and similar, and `-trailer` names, are recorded as unplaced `extra`.
   - **Home videos:** not a G1 library type.
   - **Samples:** a name with a "sample" token, under 300 MiB, is unplaced `sample`.
   - **Anime:** keeps season numbering. Absolute-only files go to the folder's season (or 1), with the absolute number stored for META-2. Absolute-order libraries are META-2.
8. **Unplaced files** stay in the catalogue with a reason (`unrecognised`, `extra`, `sample`), for the admin list. They're tried again on every scan, so a newer version's naming rules pick them up.
9. **Times** are Unix milliseconds UTC. File mtimes are nanoseconds, as in LIB-1's `Revision`.

## File Structure

```
server/internal/library/
  naming/names.go           title/year/ID-hint parsing, version labels, keys, SortTitle
  naming/classify.go        Classify: movies, TV, anime; skip extras and samples
  naming/classify_test.go
  migrations/20261027120000_library_catalogue.sql
  store.go, store_test.go   Migrations, Store, CreateLibrary, Library, MarkInterrupted
  scan.go, scan_test.go     Scanner, ScanRoot, Run; placing files on items and versions
  duplicates.go, duplicates_test.go  folder → item mapping, possible duplicates, rename folding, Duplicates, Decide
  probes.go, probes_test.go ProbeDue, ProbeRun, RetryProbe
  retire.go, retire_test.go RetireAfter, RetirementCandidates, Retire
```

---

### Task 1: Place files by name (`library/naming`)

**Files:**
- Create: `server/internal/library/naming/classify.go`
- Create: `server/internal/library/naming/names.go`
- Test: `server/internal/library/naming/classify_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `naming.Classify(libType, rel string, size int64) (Entry, error)`; `Entry{Kind, Key, Title, Year, IDs, Version, Season, Episodes, AirDate, Absolute, EpisodeTitle}`; `Movies`/`TV`/`Anime`; `Movie`/`Episode`; `ErrUnrecognised`/`ErrExtra`/`ErrSample`; `SortTitle(string) string`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/naming/classify_test.go`:**

```go
package naming

import (
	"errors"
	"fmt"
	"testing"
)

func abs(n int) *int { return &n }

// got summarises an Entry for comparison; nil maps and slices compare as empty.
func summary(e Entry) string {
	ids := e.IDs
	if len(ids) == 0 {
		ids = nil
	}
	a := "-"
	if e.Absolute != nil {
		a = fmt.Sprint(*e.Absolute)
	}
	return fmt.Sprintf("%s %s %q %d %v v=%q s=%d e=%v d=%q abs=%s t=%q",
		e.Kind, e.Key, e.Title, e.Year, ids, e.Version, e.Season, e.Episodes, e.AirDate, a, e.EpisodeTitle)
}

func check(t *testing.T, libType, rel string, want Entry) {
	t.Helper()
	got, err := Classify(libType, rel, 2<<30)
	if err != nil {
		t.Fatalf("Classify(%q): %v", rel, err)
	}
	if summary(got) != summary(want) {
		t.Fatalf("Classify(%q)\n got %s\nwant %s", rel, summary(got), summary(want))
	}
}

func TestClassifyMovies(t *testing.T) {
	for _, c := range []struct {
		rel  string
		want Entry
	}{
		{"Movie Title (2024)/Movie Title (2024) - 2160p.mkv",
			Entry{Kind: Movie, Key: "movie:movie title|2024", Title: "Movie Title", Year: 2024, Version: "2160p"}},
		{"Movie (2010)/Movie (2010) - Director's Cut.mkv",
			Entry{Kind: Movie, Key: "movie:movie|2010", Title: "Movie", Year: 2010, Version: "Director's Cut"}},
		{"Movie (2010)/Movie (2010) {edition-Extended Edition}.mkv",
			Entry{Kind: Movie, Key: "movie:movie|2010", Title: "Movie", Year: 2010, Version: "Extended Edition"}},
		{"The Matrix (1999) {tmdb-603}/The Matrix (1999) {imdb-tt0133093} - [Bluray-1080p][DTS 5.1][x264]-GRP.mkv",
			Entry{Kind: Movie, Key: "movie:the matrix|1999", Title: "The Matrix", Year: 1999,
				IDs: map[string]string{"tmdb": "603", "imdb": "tt0133093"}, Version: "1080p"}},
		// A Radarr rename keeps the key: same title and year, new tags.
		{"Heat.1995.2160p.UHD.BluRay.x265-GRP/Heat.1995.2160p.UHD.BluRay.x265-GRP.mkv",
			Entry{Kind: Movie, Key: "movie:heat|1995", Title: "Heat", Year: 1995, Version: "2160p"}},
		{"Heat (1995) [tmdbid-949]/Heat (1995).mkv",
			Entry{Kind: Movie, Key: "movie:heat|1995", Title: "Heat", Year: 1995, IDs: map[string]string{"tmdb": "949"}}},
		{"Movies A-Z/Alien (1979)/Alien.mkv", Entry{Kind: Movie, Key: "movie:alien|1979", Title: "Alien", Year: 1979}},
		{"Random Folder/Arrival.2016.1080p.WEB-DL.mkv",
			Entry{Kind: Movie, Key: "movie:arrival|2016", Title: "Arrival", Year: 2016, Version: "1080p"}},
		{"Loose Movie (2001) - 720p.mkv", Entry{Kind: Movie, Key: "movie:loose movie|2001", Title: "Loose Movie", Year: 2001, Version: "720p"}},
		{"1917 (2019)/1917.mkv", Entry{Kind: Movie, Key: "movie:1917|2019", Title: "1917", Year: 2019}},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-GRP.mkv",
			Entry{Kind: Movie, Key: "movie:blade runner 2049|2017", Title: "Blade Runner 2049", Year: 2017, Version: "1080p"}},
		{"Amélie (2001)/Amélie (2001).mkv", Entry{Kind: Movie, Key: "movie:amélie|2001", Title: "Amélie", Year: 2001}},
		{"No Year Movie.mkv", Entry{Kind: Movie, Key: "movie:no year movie|0", Title: "No Year Movie"}},
		{"Bad ID {imdb-123}.mkv", Entry{Kind: Movie, Key: "movie:bad id|0", Title: "Bad ID"}},
	} {
		t.Run(c.rel, func(t *testing.T) { check(t, Movies, c.rel, c.want) })
	}
}

func TestClassifyTV(t *testing.T) {
	ids := map[string]string{"tvdb": "123"}
	show := func(e Entry) Entry {
		e.Kind, e.Key, e.Title, e.Year, e.IDs = Episode, "show:show|2019", "Show", 2019, ids
		return e
	}
	const dir = "Show (2019) {tvdb-123}/"
	for _, c := range []struct {
		rel  string
		want Entry
	}{
		{dir + "Season 01/Show - S01E02 - Title.mkv", show(Entry{Season: 1, Episodes: []int{2}, EpisodeTitle: "Title"})},
		{dir + "Season 01/Show (2019) - S01E02E03 - Two Parter [WEBDL-1080p][EAC3 5.1][h264]-GRP.mkv",
			show(Entry{Season: 1, Episodes: []int{2, 3}, EpisodeTitle: "Two Parter", Version: "1080p"})},
		{dir + "Season 01/Show - S01E04-E06.mkv", show(Entry{Season: 1, Episodes: []int{4, 5, 6}})},
		{dir + "Season 01/Show - S01E01-E99.mkv", show(Entry{Season: 1, Episodes: []int{1}})}, // implausible range: first only
		{dir + "Specials/Show - S00E01 - Christmas Special.mkv", show(Entry{Season: 0, Episodes: []int{1}, EpisodeTitle: "Christmas Special"})},
		{dir + "Season 2/Show.S02E10.1080p.WEB.h264-GRP.mkv", show(Entry{Season: 2, Episodes: []int{10}, Version: "1080p"})},
		{dir + "Season 3/3x07 - Old Style.avi", show(Entry{Season: 3, Episodes: []int{7}, EpisodeTitle: "Old Style"})},
		{"The Daily Show/Season 2024/The Daily Show - 2024-03-01 - Guest Name.mkv",
			Entry{Kind: Episode, Key: "show:the daily show|0", Title: "The Daily Show", Season: 2024, AirDate: "2024-03-01", EpisodeTitle: "Guest Name"}},
		{"Loose.Show.S01E01.720p.HDTV.x264-GRP.mkv",
			Entry{Kind: Episode, Key: "show:loose show|0", Title: "Loose Show", Season: 1, Episodes: []int{1}, Version: "720p"}},
	} {
		t.Run(c.rel, func(t *testing.T) { check(t, TV, c.rel, c.want) })
	}
}

func TestClassifyAnime(t *testing.T) {
	ids := map[string]string{"anidb": "17617"}
	fr := func(e Entry) Entry {
		e.Kind, e.Key, e.Title, e.Year, e.IDs = Episode, "show:frieren|2023", "Frieren", 2023, ids
		return e
	}
	const dir = "Frieren (2023) [anidb-17617]/"
	for _, c := range []struct {
		rel  string
		want Entry
	}{
		{dir + "Season 01/Frieren - S01E05 - 005 - Phantoms [Bluray-1080p][10bit]-Grp.mkv",
			fr(Entry{Season: 1, Episodes: []int{5}, Absolute: abs(5), EpisodeTitle: "Phantoms", Version: "1080p"})},
		{dir + "[SubsPlease] Frieren - 12 (1080p) [ABCD1234].mkv", fr(Entry{Season: 1, Episodes: []int{12}, Absolute: abs(12), Version: "1080p"})},
		{dir + "Season 02/Frieren - 30v2.mkv", fr(Entry{Season: 2, Episodes: []int{30}, Absolute: abs(30)})},
		{dir + "Specials/Frieren - OVA 2.mkv", fr(Entry{Season: 0, Episodes: []int{2}})},
		{dir + "Frieren - 13 (Part 2) - The Hero (1080p).mkv", fr(Entry{Season: 1, Episodes: []int{13}, Absolute: abs(13), EpisodeTitle: "The Hero", Version: "1080p"})},
		{dir + "Frieren - Episode 7 - Title.mkv", fr(Entry{Season: 1, Episodes: []int{7}, Absolute: abs(7), EpisodeTitle: "Title"})},
		// No episode number and no season folder: an anime film.
		{"Your Name (2016)/Your Name (2016) - 1080p.mkv",
			Entry{Kind: Movie, Key: "movie:your name|2016", Title: "Your Name", Year: 2016, Version: "1080p"}},
	} {
		t.Run(c.rel, func(t *testing.T) { check(t, Anime, c.rel, c.want) })
	}
}

func TestClassifyRefusesWhatItCannotPlace(t *testing.T) {
	for _, c := range []struct {
		lib, rel string
		size     int64
		want     error
	}{
		{Movies, "Movie (2010)/Featurettes/Making Of.mkv", 2 << 30, ErrExtra},
		{Movies, "Movie (2010)/Movie (2010)-trailer.mkv", 2 << 30, ErrExtra},
		{Movies, "Movie (2010)/trailer.mp4", 2 << 30, ErrExtra},
		{TV, "Show/Extras/Gag Reel.mkv", 2 << 30, ErrExtra},
		{Movies, "Movie (2010)/movie-sample.mkv", 50 << 20, ErrSample},
		{TV, "Show/Season 01/Some Random Clip.mkv", 2 << 30, ErrUnrecognised},
		{Anime, "Show/Season 01/Not An Episode.mkv", 2 << 30, ErrUnrecognised},
	} {
		if _, err := Classify(c.lib, c.rel, c.size); !errors.Is(err, c.want) {
			t.Errorf("Classify(%s, %q) = %v, want %v", c.lib, c.rel, err, c.want)
		}
	}
	// A real film whose title contains "Sample" is not a sample file.
	if e, err := Classify(Movies, "The Sample (2020)/The Sample (2020).mkv", 2<<30); err != nil || e.Title != "The Sample" {
		t.Fatalf("The Sample: %+v %v", e, err)
	}
	if _, err := Classify("music", "a.mkv", 1); err == nil {
		t.Fatal("unknown library type accepted")
	}
}

func TestSortTitle(t *testing.T) {
	for in, want := range map[string]string{"The Matrix": "Matrix", "A Quiet Place": "Quiet Place", "An Education": "Education",
		"Theory": "Theory", "The": "The", "Alien": "Alien"} {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyFolder(t *testing.T) {
	for _, c := range []struct{ lib, rel, want string }{
		{Movies, "Heat (1995)/Heat (1995).mkv", "Heat (1995)"},
		{Movies, "Movies A-Z/Alien (1979)/Alien.mkv", "Movies A-Z/Alien (1979)"},
		{Movies, "Loose Movie (2001).mkv", ""},
		{TV, "Show (2019)/Season 01/Show - S01E01.mkv", "Show (2019)"},
		{TV, "Loose.Show.S01E01.mkv", ""},
	} {
		if e, err := Classify(c.lib, c.rel, 2<<30); err != nil || e.Folder != c.want {
			t.Errorf("Classify(%q).Folder = %q, %v; want %q", c.rel, e.Folder, err, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `undefined: Entry`, `undefined: Classify`, `undefined: Movie` in `classify_test.go`. LIB-1's `library` tests still pass.

- [ ] **Step 3: Implement**

**Create `server/internal/library/naming/classify.go`:**

```go
package naming

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Library types, spelled as contracts' Library.type values.
const (
	Movies = "movies"
	TV     = "tv"
	Anime  = "anime"
)

type Kind string

const (
	Movie   Kind = "movie"
	Episode Kind = "episode"
)

var (
	ErrUnrecognised = errors.New("naming: no movie or episode in the file name")
	ErrExtra        = errors.New("naming: extra or trailer (LIB-3)")
	ErrSample       = errors.New("naming: sample file")
)

// Entry is where one video file belongs. Key names the movie or show:
// normalised title plus year, so cosmetic renames ("Heat.1995" → "Heat (1995)
// {tmdb-949}") give the same key. Folder is the folder the name came from;
// the store uses it to tell a rename from two folders that clash (spec §3.6).
type Entry struct {
	Kind    Kind
	Key     string            // "movie:heat|1995" or "show:show|2019"
	Folder  string            // the movie's folder or the show's top folder, inside the root; "" for loose files
	Title   string            // movie or show title
	Year    int               // 0 when unknown
	IDs     map[string]string // provider ID hints from the names, e.g. tmdb → 949
	Version string            // label among several files of one item: "2160p", "Director's Cut"

	// Episodes only.
	Season       int
	Episodes     []int  // one, or each episode of a multi-episode file
	AirDate      string // daily shows (YYYY-MM-DD), with no episode numbers
	Absolute     *int   // anime absolute number, kept for META-2's mapping
	EpisodeTitle string
}

// sampleMaxBytes: a file named "sample" is skipped only below this size, so a
// real film called "The Sample" is still catalogued.
const sampleMaxBytes = 300 << 20

// Classify places rel (a slash-separated path inside a root of a libType
// library) of the given size. Errors wrap ErrUnrecognised, ErrExtra or
// ErrSample; the scan lists such files instead of guessing (spec §3.1).
func Classify(libType, rel string, size int64) (Entry, error) {
	parts := strings.Split(rel, "/")
	dirs, file := parts[:len(parts)-1], parts[len(parts)-1]
	stem := strings.TrimSuffix(file, path.Ext(file))
	if size < sampleMaxBytes && sampleRe.MatchString(stem) {
		return Entry{}, ErrSample
	}
	for _, d := range dirs {
		if extraFolders[strings.ToLower(d)] {
			return Entry{}, ErrExtra
		}
	}
	if isExtraName(stem) && len(dirs) > 0 {
		return Entry{}, ErrExtra
	}
	switch libType {
	case Movies:
		return classifyMovie(dirs, stem), nil
	case TV, Anime:
		e, err := classifyEpisode(libType == Anime, dirs, stem)
		// Anime libraries also hold films: a file outside season folders with
		// no episode number is a movie.
		if errors.Is(err, ErrUnrecognised) && libType == Anime && !hasSeasonDir(dirs) {
			return classifyMovie(dirs, stem), nil
		}
		return e, err
	}
	return Entry{}, fmt.Errorf("naming: unknown library type %q", libType)
}

// extraFolders are Plex/Jellyfin extras folders.
var extraFolders = map[string]bool{
	"behind the scenes": true, "deleted scenes": true, "featurettes": true, "interviews": true,
	"scenes": true, "shorts": true, "trailers": true, "extras": true, "other": true, "others": true,
}

func isExtraName(stem string) bool {
	lower := strings.ToLower(stem)
	if lower == "trailer" {
		return true
	}
	for _, s := range []string{"-behindthescenes", "-deleted", "-featurette", "-interview", "-scene", "-short", "-trailer", "-other"} {
		if strings.HasSuffix(lower, s) && len(lower) > len(s) {
			return true
		}
	}
	return false
}

func classifyMovie(dirs []string, stem string) Entry {
	file := parseName(stem)
	ni := file
	if len(dirs) > 0 {
		// The innermost folder names the movie unless it has no year and the
		// file does ("Random Folder/Arrival.2016.1080p.mkv").
		if folder := parseName(dirs[len(dirs)-1]); folder.Year != 0 || file.Year == 0 {
			ni = folder
		}
		for k, v := range file.IDs {
			if _, ok := ni.IDs[k]; !ok {
				ni.IDs[k] = v
			}
		}
	}
	return Entry{Kind: Movie, Key: "movie:" + key(ni.Title) + "|" + strconv.Itoa(ni.Year), Folder: strings.Join(dirs, "/"),
		Title: ni.Title, Year: ni.Year, IDs: ni.IDs, Version: versionLabel(stem)}
}

var (
	seasonDirRe    = regexp.MustCompile(`(?i)^(?:season|series|staffel|saison|s)[\s._-]*(\d{1,4})$`)
	specialsRe     = regexp.MustCompile(`(?i)^specials?$`)
	sxeRe          = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,4})[\s._-]?e(\d{1,4})`)
	moreEpRe       = regexp.MustCompile(`(?i)^(?:-?e|-)(\d{1,4})(?:[^0-9]|$)`)
	nxnRe          = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^0-9]|$)`)
	dateRe         = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})[-. ](\d{2})[-. ](\d{2})(?:[^0-9]|$)`)
	absAfterRe     = regexp.MustCompile(`^\s*-\s*(\d{1,4})(?:v\d+)?(?:\s|$)`)
	absOnlyRe      = regexp.MustCompile(`(?i)^(.*?)\s+-\s+(?:(?:e|ep|episode)\s?)?(\d{1,4})(?:v\d+)?(?:\s+\([^)]*\))?(?:\s+-\s+(.*))?$`)
	epWordRe       = regexp.MustCompile(`(?i)^(.*?)\s+(?:e|ep|episode)\s?(\d{1,4})(?:v\d+)?(?:\s+-\s+(.*))?$`)
	specialRe      = regexp.MustCompile(`(?i)^(.*?)\s+(?:-\s+)?(?:ova|oad|sp|special)\s?-?\s?(\d{1,3})(?:v\d+)?(?:\s+-\s+(.*))?$`)
	parenQualityRe = regexp.MustCompile(`(?i)\([^)]*\b(?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|x264|x265|hevc|10bit|bluray|web-?dl|webrip)\b[^)]*\)`)
	junkRe         = regexp.MustCompile(`(?i)\s+-?(?:proper|repack)\b.*$`)
)

func seasonFromDir(name string) (int, bool) {
	if specialsRe.MatchString(name) {
		return 0, true
	}
	if m := seasonDirRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

func hasSeasonDir(dirs []string) bool {
	for _, d := range dirs {
		if _, ok := seasonFromDir(d); ok {
			return true
		}
	}
	return false
}

// maxEpisodesPerFile bounds a range like S01E01-E99 from a mis-tagged name.
const maxEpisodesPerFile = 12

// classifyEpisode handles TV and anime names:
//
//	Show (2019) {tvdb-123}/Season 01/Show - S01E02 - Title.mkv   (also S01E02E03, S01E02-E04, 1x02)
//	Show/Show - 2024-03-01 - Title.mkv                            (daily: season = year)
//	Anime/[Group] Show - 1094 [1080p].mkv, Show - OVA 2           (absolute numbers, specials)
func classifyEpisode(anime bool, dirs []string, stem string) (Entry, error) {
	e := Entry{Kind: Episode, Version: resolution(stem)}
	folderSeason, haveFolderSeason := 0, false
	for _, d := range dirs[min(1, len(dirs)):] {
		if n, ok := seasonFromDir(d); ok {
			folderSeason, haveFolderSeason = n, true
		}
	}
	haveSeason := false
	s := dotsToSpaces(bracketRe.ReplaceAllString(stem, " "))
	s = parenQualityRe.ReplaceAllString(parenYearRe.ReplaceAllString(s, " "), " ") // "(1080p)" as in SubsPlease names
	s = spacesRe.ReplaceAllString(s, " ")
	showPart, rest := "", ""

	if m := sxeRe.FindStringSubmatchIndex(s); m != nil {
		e.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		first, _ := strconv.Atoi(s[m[4]:m[5]])
		haveSeason = true
		last := first
		showPart, rest = s[:m[0]], s[m[1]:]
		for mm := moreEpRe.FindStringSubmatchIndex(rest); mm != nil; mm = moreEpRe.FindStringSubmatchIndex(rest) {
			if n, _ := strconv.Atoi(rest[mm[2]:mm[3]]); n > last {
				last = n
			}
			rest = rest[mm[3]:]
		}
		if last-first >= maxEpisodesPerFile {
			last = first
		}
		for n := first; n <= last; n++ {
			e.Episodes = append(e.Episodes, n)
		}
		if anime {
			if mm := absAfterRe.FindStringSubmatchIndex(rest); mm != nil {
				n, _ := strconv.Atoi(rest[mm[2]:mm[3]])
				e.Absolute, rest = &n, rest[mm[3]:]
			}
		}
	} else if m := nxnRe.FindStringSubmatchIndex(s); m != nil {
		e.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		ep, _ := strconv.Atoi(s[m[4]:m[5]])
		haveSeason, e.Episodes = true, []int{ep}
		showPart, rest = s[:m[2]], s[m[5]:]
	} else if m := dateRe.FindStringSubmatchIndex(s); m != nil {
		e.AirDate = s[m[2]:m[3]] + "-" + s[m[4]:m[5]] + "-" + s[m[6]:m[7]]
		e.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		haveSeason = true
		showPart, rest = s[:m[2]], s[m[7]:]
	} else if m := specialRe.FindStringSubmatch(strings.TrimSpace(s)); anime && m != nil {
		n, _ := strconv.Atoi(m[2])
		e.Season, e.Episodes, haveSeason = 0, []int{n}, true
		showPart, rest = m[1], m[3]
	} else if m := absOnlyRe.FindStringSubmatch(strings.TrimSpace(s)); anime && m != nil {
		n, _ := strconv.Atoi(m[2])
		e.Absolute, showPart, rest = &n, m[1], m[3]
	} else if m := epWordRe.FindStringSubmatch(strings.TrimSpace(s)); anime && m != nil {
		n, _ := strconv.Atoi(m[2])
		e.Absolute, showPart, rest = &n, m[1], m[3]
	} else {
		return Entry{}, ErrUnrecognised
	}

	if !haveSeason && e.Episodes == nil && e.Absolute != nil {
		// Absolute-only anime: the folder's season (specials stay 0), else 1,
		// numbered by the absolute number until META-2 maps it.
		e.Season = 1
		if haveFolderSeason {
			e.Season = folderSeason
		}
		e.Episodes = []int{*e.Absolute}
	} else if !haveSeason && haveFolderSeason {
		e.Season = folderSeason
	}
	if haveFolderSeason && folderSeason == 0 {
		e.Season = 0 // anything in a Specials folder is a special
	}
	e.EpisodeTitle = episodeTitle(rest)

	var ni nameInfo
	if len(dirs) > 0 {
		ni = parseName(dirs[0])
		e.Folder = dirs[0]
	} else {
		ni = parseName(showPart)
	}
	for k, v := range idHints(stem) {
		if _, ok := ni.IDs[k]; !ok {
			ni.IDs[k] = v
		}
	}
	if ni.Title == "" {
		return Entry{}, ErrUnrecognised
	}
	e.Key, e.Title, e.Year, e.IDs = "show:"+key(ni.Title)+"|"+strconv.Itoa(ni.Year), ni.Title, ni.Year, ni.IDs
	return e, nil
}

// episodeTitle extracts "Title" from " - Title -GROUP" (brackets already removed).
func episodeTitle(rest string) string {
	t := strings.TrimLeft(strings.TrimSpace(rest), "-. ")
	if loc := qualityRe.FindStringSubmatchIndex(" " + t); loc != nil {
		t = t[:max(0, loc[2]-1)]
	}
	t = strings.TrimSpace(releaseGroupRe.ReplaceAllString(strings.TrimSpace(t), ""))
	return cleanTitle(junkRe.ReplaceAllString(t, ""))
}
```

**Create `server/internal/library/naming/names.go`:**

```go
// Package naming turns a video file's path inside a library root into the
// catalogue item it belongs to, from Plex/Radarr/Sonarr and anime naming
// conventions (LIB-2, spec §3.1). It only reads names, never files.
package naming

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	idHintRe    = regexp.MustCompile(`(?i)[\[{]\s*(tmdb|tvdb|imdb|anidb|anilist|mal)(?:id)?\s*[-=:]\s*([a-z0-9]+)\s*[\]}]`)
	editionRe   = regexp.MustCompile(`(?i)\{edition-([^}]+)\}`)
	parenYearRe = regexp.MustCompile(`\((\d{4})\)`)
	bracketRe   = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
	qualityRe   = regexp.MustCompile(`(?i)(?:^|[\s(\[_-])(2160p|1080p|1080i|720p|576p|480p|4k|uhd|bluray|blu-ray|bdrip|brrip|bdremux|web-?dl|webrip|hdtv|dvdrip|remux|hdr10|x264|x265|h\.?264|h\.?265|hevc|10bit|8bit)(?:$|[\s)\]_-])`)
	resRe       = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(2160p|1080p|1080i|720p|576p|480p|4k|uhd)(?:$|[^a-z0-9])`)
	spacesRe    = regexp.MustCompile(`\s+`)
	imdbRe      = regexp.MustCompile(`^tt\d+$`)
	// releaseGroupRe matches a trailing "-GROUP" left after brackets are removed.
	releaseGroupRe = regexp.MustCompile(`(?:^|\s)-[^\s-][^\s]*$`)
	sampleRe       = regexp.MustCompile(`(?i)(?:^|[-._ \[(])sample(?:$|[-._ \])])`)
)

// nameInfo is what a folder or file name says about a movie or show.
type nameInfo struct {
	Title string
	Year  int
	IDs   map[string]string
}

// parseName reads "Title (Year) {tmdb-1}", Radarr/Sonarr names and scene
// names like "Title.Year.1080p.BluRay.x264-GRP".
func parseName(name string) nameInfo {
	ni := nameInfo{IDs: idHints(name)}
	s := bracketRe.ReplaceAllString(name, " ")
	if ms := parenYearRe.FindAllStringSubmatchIndex(s, -1); len(ms) > 0 {
		m := ms[len(ms)-1]
		if y, _ := strconv.Atoi(s[m[2]:m[3]]); validYear(y) && strings.TrimSpace(s[:m[0]]) != "" {
			ni.Year = y
			ni.Title = cleanTitle(dotsToSpaces(s[:m[0]]))
			return ni
		}
	}
	s = dotsToSpaces(s)
	if loc := qualityRe.FindStringSubmatchIndex(s); loc != nil && strings.TrimSpace(s[:loc[2]]) != "" {
		s = s[:loc[2]]
	}
	// The last year-like word that isn't the first word is the year:
	// "Blade Runner 2049 2017" → 2017, but "1917" alone stays a title.
	words := strings.Fields(s)
	for i := len(words) - 1; i > 0; i-- {
		w := strings.Trim(words[i], "()")
		if y, err := strconv.Atoi(w); err == nil && len(w) == 4 && validYear(y) {
			ni.Year = y
			s = strings.Join(words[:i], " ")
			break
		}
	}
	ni.Title = cleanTitle(s)
	return ni
}

// idHints extracts {tmdb-123}, [tmdbid-123], {imdb-tt123}, [anidb-123] ... hints.
func idHints(name string) map[string]string {
	ids := map[string]string{}
	for _, m := range idHintRe.FindAllStringSubmatch(name, -1) {
		if k, v, ok := normalizeID(m[1], m[2]); ok {
			ids[k] = v
		}
	}
	return ids
}

// normalizeID checks a hint's value; the namespace is already one of the
// regexp's names, lower-cased.
func normalizeID(provider, value string) (string, string, bool) {
	provider, value = strings.ToLower(provider), strings.ToLower(value)
	if provider == "imdb" {
		return provider, value, imdbRe.MatchString(value)
	}
	if n, err := strconv.ParseInt(value, 10, 64); err != nil || n <= 0 {
		return "", "", false
	}
	return provider, value, true
}

func validYear(y int) bool { return y >= 1880 && y <= 2100 }

// dotsToSpaces turns scene-style "The.Movie_Name" into "The Movie Name" when
// the name has no real spaces.
func dotsToSpaces(s string) string {
	if strings.Count(s, " ") >= strings.Count(s, ".")+strings.Count(s, "_") {
		return s
	}
	return strings.NewReplacer(".", " ", "_", " ").Replace(s)
}

func cleanTitle(s string) string {
	s = spacesRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Trim(s, " -_.(["))
}

// versionLabel names one of several files of a movie: an {edition-...} tag,
// else the text after " - " following the year ("Director's Cut", "1080p"),
// else the resolution anywhere in the name, else "".
func versionLabel(stem string) string {
	if m := editionRe.FindStringSubmatch(stem); m != nil {
		return strings.TrimSpace(m[1])
	}
	rest := ""
	if ms := parenYearRe.FindAllStringIndex(stem, -1); len(ms) > 0 {
		rest = stem[ms[len(ms)-1][1]:]
	} else if i := strings.Index(stem, " - "); i >= 0 {
		rest = stem[i:]
	}
	rest = strings.TrimSpace(idHintRe.ReplaceAllString(rest, ""))
	if strings.HasPrefix(rest, "- ") || rest == "-" {
		label := strings.TrimSpace(bracketRe.ReplaceAllString(strings.TrimSpace(rest[1:]), " "))
		if label = cleanTitle(releaseGroupRe.ReplaceAllString(label, "")); label != "" {
			return label
		}
	}
	return resolution(stem)
}

func resolution(stem string) string {
	m := resRe.FindStringSubmatch(stem)
	if m == nil {
		return ""
	}
	if r := strings.ToLower(m[1]); r != "4k" && r != "uhd" {
		return r
	}
	return "2160p"
}

// key normalises a title for identity: lower case, letters and digits only,
// single spaces. "The.Matrix" and "The Matrix" share a key.
func key(title string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

// SortTitle drops a leading English article: "The Matrix" sorts as "Matrix".
func SortTitle(title string) string {
	for _, a := range []string{"the ", "a ", "an "} {
		if len(title) > len(a) && strings.EqualFold(title[:len(a)], a) {
			return title[len(a):]
		}
	}
	return title
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
gofmt -l server   # prints nothing
go -C server test -race -count=1 ./internal/library/...
```

Expected: `ok` for `internal/library` and `internal/library/naming`.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/naming/classify.go server/internal/library/naming/classify_test.go server/internal/library/naming/names.go
git commit -m "feat(library): place files by Plex/Radarr/Sonarr and anime names (LIB-2a)"
```

---

### Task 2: Catalogue schema and library store

**Files:**
- Create: `server/internal/library/migrations/20261027120000_library_catalogue.sql`
- Create: `server/internal/library/store.go`
- Test: `server/internal/library/store_test.go`

**Interfaces:**
- Consumes: `naming.Movies`/`TV`/`Anime` (Task 1); `storage.Open`, `storage.Core`, `(*storage.DB).Migrate`, `storage.Source` (OPS-2).
- Produces: `Migrations() storage.Source`; `Store`, `NewStore(*storage.DB) *Store`; `CreateLibrary(ctx, name, typ string, roots []string) (Library, error)`; `(*Store).Library(ctx, id)`; `MarkInterrupted(ctx) (int, error)`; `Library{ID, Name, Type, Roots}`, `Root{ID, LibraryID, Path, State, Reason, LastOK}`; `RootAvailable`/`RootScanning`/`RootDegraded`/`RootOffline`; `ErrNotFound`, `ErrInvalid`; internal `root(ctx, id) (Root, libType, error)`, `newID`, `ms`, `queryStrings`, `scanStrings`. Test helpers `newStore(t) (*Store, *clock)` and `clock` (later tasks use them).

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/store_test.go`:**

```go
package library

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// clock is a settable test time.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	if _, err := db.Migrate(ctx, []storage.Source{storage.Core(), Migrations()}, c.now); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)
	s.now = c.now
	return s, c
}

func TestCreateLibraryChecksFolders(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies", "./More Movies/"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Library(ctx, lib.ID)
	if err != nil || got.Name != "Movies" || len(got.Roots) != 2 || got.Roots[0].Path != "More Movies" || got.Roots[0].State != RootAvailable {
		t.Fatalf("Library = %+v, %v", got, err)
	}
	for name, roots := range map[string][]string{
		"escape":           {"../etc"},
		"absolute":         {"/media/Movies"},
		"same folder":      {"Movies"},
		"inside another":   {"Movies/Action"},
		"around another":   {"."},
		"overlap in input": {"TV", "TV/Anime"},
		"none":             {},
	} {
		if _, err := s.CreateLibrary(ctx, "X", "tv", roots); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.CreateLibrary(ctx, "X", "music", []string{"Music"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("music type: %v", err)
	}
	if _, err := s.CreateLibrary(ctx, " ", "tv", []string{"TV"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
	if _, err := s.Library(ctx, "lib-nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown library: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `undefined: Store`, `Migrations`, `NewStore`, `RootAvailable`, `ErrInvalid` in `store_test.go`.

- [ ] **Step 3: Implement**

**Create `server/internal/library/migrations/20261027120000_library_catalogue.sql`:**

```sql
-- Library catalogue (LIB-2): libraries, their roots, scan runs, the files
-- found under each root, and the items and versions they make up. Times are
-- Unix milliseconds (UTC); *_ns columns are file modification times in
-- nanoseconds, as LIB-1's Revision holds them.

CREATE TABLE library_libraries (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('movies', 'tv', 'anime')),
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE library_roots (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES library_libraries(id) ON DELETE CASCADE,
    path       TEXT NOT NULL UNIQUE, -- slash-separated, relative to the media root
    state      TEXT NOT NULL DEFAULT 'available'
               CHECK (state IN ('available', 'scanning', 'degraded', 'offline')),
    reason     TEXT,                 -- why the last scan failed; NULL after a success
    last_ok_at INTEGER               -- when the last successful scan finished
) STRICT;

CREATE TABLE library_scan_runs (
    id          TEXT PRIMARY KEY,
    root_id     TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    status      TEXT NOT NULL
                CHECK (status IN ('running', 'completed', 'failed', 'cancelled', 'interrupted')),
    reason      TEXT,
    seen        INTEGER NOT NULL DEFAULT 0,
    added       INTEGER NOT NULL DEFAULT 0,
    changed     INTEGER NOT NULL DEFAULT 0,
    returned    INTEGER NOT NULL DEFAULT 0,
    missing     INTEGER NOT NULL DEFAULT 0,
    unplaced    INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX library_scan_runs_root ON library_scan_runs(root_id, status, started_at);

CREATE TABLE library_files (
    id             TEXT PRIMARY KEY,
    root_id        TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    path           TEXT NOT NULL,    -- slash-separated, relative to the root
    size           INTEGER NOT NULL,
    mtime_ns       INTEGER NOT NULL,
    state          TEXT NOT NULL DEFAULT 'present' CHECK (state IN ('present', 'missing', 'retired')),
    missing_since  INTEGER,          -- first successful scan that didn't find it
    last_seen_run  TEXT,
    unplaced       TEXT,             -- why naming couldn't place it: unrecognised, extra, sample
    probe_size     INTEGER,          -- the revision the probe columns describe
    probe_mtime_ns INTEGER,
    probe_json     TEXT,
    probe_error    TEXT,
    probe_after    INTEGER,          -- not before this time: quiet period after a change (#62)
    UNIQUE (root_id, path)
) STRICT;

CREATE TABLE library_items (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES library_libraries(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('movie', 'show', 'season', 'episode')),
    parent_id  TEXT REFERENCES library_items(id) ON DELETE CASCADE,
    scan_key   TEXT NOT NULL,        -- identity from names; see naming.Entry.Key
    title      TEXT NOT NULL,
    sort_title TEXT NOT NULL,
    year       INTEGER,
    idx        INTEGER,              -- season or episode number
    air_date   TEXT,                 -- daily episodes, YYYY-MM-DD
    absolute   INTEGER,              -- anime absolute number, for META-2
    created_at INTEGER NOT NULL,
    UNIQUE (library_id, scan_key)
) STRICT;
CREATE INDEX library_items_parent ON library_items(parent_id);

CREATE TABLE library_versions (
    id      TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE CASCADE,
    label   TEXT,
    UNIQUE (item_id, file_id)
) STRICT;
CREATE INDEX library_versions_file ON library_versions(file_id);

-- Provider ID hints from file and folder names. Two different values for one
-- namespace are kept, so META-1 can flag the conflict instead of picking one.
CREATE TABLE library_id_hints (
    item_id   TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    value     TEXT NOT NULL,
    PRIMARY KEY (item_id, namespace, value)
) STRICT;

-- Which movie or show each naming folder belongs to (Entry.Folder; '' for
-- loose files in the root). Once a folder is mapped, its files always go to
-- that item, so a decision about a possible duplicate sticks.
CREATE TABLE library_folders (
    root_id  TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    folder   TEXT NOT NULL,
    scan_key TEXT NOT NULL,          -- the naming key the folder produced
    item_id  TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    PRIMARY KEY (root_id, folder, scan_key)
) STRICT;

-- A folder whose name matches an existing item from another folder gets its
-- own provisional item (other_id). After a completed scan, a pair whose
-- existing item has no present files was a rename or move and is folded
-- together; otherwise both folders exist and the pair is flagged for the
-- admin to merge or keep separate. Nothing merges two coexisting folders
-- automatically.
CREATE TABLE library_duplicates (
    id       TEXT PRIMARY KEY,
    root_id  TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE, -- the other folder's root
    item_id  TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE, -- the existing item
    other_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE, -- the provisional item
    state    TEXT NOT NULL CHECK (state IN ('pending', 'flagged', 'separate')),
    UNIQUE (item_id, other_id)
) STRICT;
```

**Create `server/internal/library/store.go`:**

```go
package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations is the catalogue's schema, for storage.Migrate.
// Planned: LIB-2b (serve registers it with the other modules' migrations).
func Migrations() storage.Source {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // embedded at build time
	}
	return storage.Source{Module: "library", FS: sub}
}

var (
	ErrNotFound = errors.New("library: not found")
	ErrInvalid  = errors.New("library: invalid library")
)

// Root states (spec §3.6).
const (
	RootAvailable = "available"
	RootScanning  = "scanning"
	RootDegraded  = "degraded"
	RootOffline   = "offline"
)

type Library struct {
	ID, Name, Type string
	Roots          []Root
}

// Root is one folder of a library, relative to the media root. An offline or
// degraded root keeps its catalogue entries (spec §3.6).
type Root struct {
	ID, LibraryID, Path string
	State               string
	Reason              string    // why the last scan failed; "" after a success
	LastOK              time.Time // zero until a scan succeeds
}

// Store is the catalogue's persistence on the shared database.
type Store struct {
	db  *storage.DB
	now func() time.Time
}

// Planned: LIB-2b (serve builds the store and scanner).
func NewStore(db *storage.DB) *Store { return &Store{db: db, now: time.Now} }

func newID(prefix string) string { return prefix + "-" + rand.Text() }

func ms(t time.Time) int64 { return t.UnixMilli() }

// CreateLibrary adds a library over one or more folders, given relative to the
// media root. A folder can't be the media root's parent, nor overlap a folder
// of any library.
// Planned: LIB-2c (the libraries API).
func (s *Store) CreateLibrary(ctx context.Context, name, typ string, roots []string) (Library, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || len(name) > 200:
		return Library{}, fmt.Errorf("%w: name must be 1–200 characters", ErrInvalid)
	case typ != naming.Movies && typ != naming.TV && typ != naming.Anime:
		return Library{}, fmt.Errorf("%w: type must be movies, tv or anime", ErrInvalid)
	case len(roots) == 0:
		return Library{}, fmt.Errorf("%w: at least one folder is required", ErrInvalid)
	}
	clean := make([]string, len(roots))
	for i, r := range roots {
		c := path.Clean(strings.TrimPrefix(r, "./"))
		if !fs.ValidPath(c) {
			return Library{}, fmt.Errorf("%w: folder %q must be inside the media root", ErrInvalid, r)
		}
		clean[i] = c
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	existing, err := queryStrings(ctx, tx, `SELECT path FROM library_roots`)
	if err != nil {
		return Library{}, err
	}
	for i, c := range clean {
		for _, other := range append(existing, clean[:i]...) {
			if overlaps(c, other) {
				return Library{}, fmt.Errorf("%w: folder %q overlaps %q", ErrInvalid, c, other)
			}
		}
	}
	lib := Library{ID: newID("lib"), Name: name, Type: typ}
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_libraries(id, name, type, created_at) VALUES(?, ?, ?, ?)`,
		lib.ID, name, typ, ms(s.now())); err != nil {
		return Library{}, err
	}
	for _, c := range clean {
		r := Root{ID: newID("root"), LibraryID: lib.ID, Path: c, State: RootAvailable}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_roots(id, library_id, path) VALUES(?, ?, ?)`, r.ID, lib.ID, c); err != nil {
			return Library{}, err
		}
		lib.Roots = append(lib.Roots, r)
	}
	return lib, tx.Commit()
}

// overlaps reports whether one folder is the other or inside it. "." (the
// whole media root) overlaps everything.
func overlaps(a, b string) bool {
	return a == b || a == "." || b == "." || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Library returns one library and its roots.
// Planned: LIB-2c (the libraries API).
func (s *Store) Library(ctx context.Context, id string) (Library, error) {
	var lib Library
	err := s.db.QueryRowContext(ctx, `SELECT id, name, type FROM library_libraries WHERE id = ?`, id).Scan(&lib.ID, &lib.Name, &lib.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, ErrNotFound
	}
	if err != nil {
		return Library{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM library_roots WHERE library_id = ? ORDER BY path`, id)
	if err != nil {
		return Library{}, err
	}
	ids, err := scanStrings(rows)
	if err != nil {
		return Library{}, err
	}
	for _, rid := range ids {
		r, _, err := s.root(ctx, rid)
		if err != nil {
			return Library{}, err
		}
		lib.Roots = append(lib.Roots, r)
	}
	return lib, nil
}

// root returns a root and its library's type.
func (s *Store) root(ctx context.Context, id string) (Root, string, error) {
	var r Root
	var reason sql.NullString
	var lastOK sql.NullInt64
	var typ string
	err := s.db.QueryRowContext(ctx, `SELECT r.id, r.library_id, r.path, r.state, r.reason, r.last_ok_at, l.type
		FROM library_roots r JOIN library_libraries l ON l.id = r.library_id WHERE r.id = ?`, id).
		Scan(&r.ID, &r.LibraryID, &r.Path, &r.State, &reason, &lastOK, &typ)
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, "", ErrNotFound
	}
	if err != nil {
		return Root{}, "", err
	}
	r.Reason = reason.String
	if lastOK.Valid {
		r.LastOK = time.UnixMilli(lastOK.Int64)
	}
	return r, typ, nil
}

// MarkInterrupted ends runs left "running" by a crash or restart. Scans are
// idempotent, so the next scan of the root simply starts again (spec §3.6).
// Planned: LIB-2b (serve calls it at startup).
func (s *Store) MarkInterrupted(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE library_scan_runs SET status = 'interrupted', finished_at = ? WHERE status = 'running'`, ms(s.now()))
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_roots SET state = 'degraded', reason = 'interrupted' WHERE state = 'scanning'`); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
gofmt -l server   # prints nothing
go -C server test -race -count=1 ./internal/library/...
```

Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/migrations/20261027120000_library_catalogue.sql server/internal/library/store.go server/internal/library/store_test.go
git commit -m "feat(library): catalogue schema and library store (LIB-2a)"
```

---

### Task 3: Offline-safe scans into items, versions and duplicate flags

**Files:**
- Create: `server/internal/library/duplicates.go`
- Create: `server/internal/library/scan.go`
- Test: `server/internal/library/duplicates_test.go`
- Test: `server/internal/library/scan_test.go`

**Interfaces:**
- Consumes: Task 2's `Store` internals and `newStore`; Task 1's `Classify`; LIB-1's `Discover`, `File`.
- Produces: `Scanner{Store, Media *os.Root, Discover}`; `(*Scanner).ScanRoot(ctx, rootID) (Run, error)`; `Duplicate{ID, ItemID, OtherID, Title}`, `(*Store).Duplicates(ctx, libID) ([]Duplicate, error)`, `(*Store).Decide(ctx, dupID string, merge bool) error`; internal `groupItem`, `settle`, `fold`, `moveInto`, `hasPresentFile`; `Run{ID, Status, Reason, Seen, Added, Changed, Returned, Missing, Unplaced}`; `RunRunning`/`RunCompleted`/`RunFailed`/`RunCancelled`/`RunInterrupted`; `ReasonRootUnavailable`/`ReasonRootEmpty`/`ReasonEnumerationFailed`; internal `setRoot` (used by Task 2's `MarkInterrupted`, now tested). Test helpers `newMedia`, `(*media).write`, `(*media).scanner`, `scan`, `counts`, `(*Store).fileStates`, `(*Store).tree`, `rootState`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/duplicates_test.go`:**

```go
package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func itemID(t *testing.T, s *Store, key string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM library_items WHERE scan_key = ?`, key).Scan(&id); err != nil {
		t.Fatalf("item %s: %v", key, err)
	}
	return id
}

func dups(t *testing.T, s *Store, libID string) []Duplicate {
	t.Helper()
	d, err := s.Duplicates(context.Background(), libID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Two folders that exist at the same time and give the same name ("Doctor
// Who" with no year: 1963 or 2005?) are never merged by the scanner.
func TestCoexistingFoldersAreFlaggedNotMerged(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E01.mkv", 20)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)

	want := []string{"episode Episode 1 1 v1", "episode Episode 1 1 v1", "season Season 1 1 v0", "season Season 1 1 v0", "show Doctor Who - v0", "show Doctor Who - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) {
		t.Fatalf("items %v, want two separate shows", got)
	}
	d := dups(t, s, lib.ID)
	if len(d) != 1 {
		t.Fatalf("duplicates %+v", d)
	}
	if err := s.Decide(ctx, d[0].ID, false); err != nil {
		t.Fatal(err)
	}
	scan(t, sc, lib.Roots[0].ID)
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) || len(dups(t, s, lib.ID)) != 0 {
		t.Fatalf("after keeping them separate: %v, flags %v", got, dups(t, s, lib.ID))
	}
	if err := s.Decide(ctx, d[0].ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deciding twice: %v", err)
	}
	if err := s.Decide(ctx, "dup-nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown pair: %v", err)
	}
}

// Merging folds the other folder's show into the existing one: matching
// episodes get both versions, the rest move across, and the existing IDs stay.
func TestMergeDecisionFoldsItemsTogether(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E01.mkv", 20)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E02.mkv", 30)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	show, ep1 := itemID(t, s, "show:doctor who|0"), itemID(t, s, "show:doctor who|0|s1|e1")

	d := dups(t, s, lib.ID)
	if len(d) != 1 || d[0].ItemID != show {
		t.Fatalf("duplicates %+v", d)
	}
	if err := s.Decide(ctx, d[0].ID, true); err != nil {
		t.Fatal(err)
	}
	want := []string{"episode Episode 1 1 v2", "episode Episode 2 2 v1", "season Season 1 1 v0", "show Doctor Who - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) {
		t.Fatalf("after merge %v, want %v", got, want)
	}
	if itemID(t, s, "show:doctor who|0") != show || itemID(t, s, "show:doctor who|0|s1|e1") != ep1 {
		t.Fatal("the existing items' IDs changed")
	}
	// The decision sticks: the next scan neither splits nor flags them, and a
	// new episode in the merged folder joins the same show.
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E03.mkv", 40)
	scan(t, sc, lib.Roots[0].ID)
	want = append(want[:2:2], append([]string{"episode Episode 3 3 v1"}, want[2:]...)...)
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) || len(dups(t, s, lib.ID)) != 0 {
		t.Fatalf("after rescan %v, want %v; flags %v", got, want, dups(t, s, lib.ID))
	}
}

// A rename is not a duplicate: the old folder is gone by the end of the scan,
// so the new folder folds into the existing items and every ID is kept.
func TestRenamedFolderKeepsItemIDs(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Show.Name/Season 1/Show.Name.S01E01.mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	show, ep := itemID(t, s, "show:show name|0"), itemID(t, s, "show:show name|0|s1|e1")

	os.RemoveAll(filepath.Join(m.dir, "TV/Show.Name"))
	m.write("TV/Show Name/Season 01/Show Name - S01E01.mkv", 10)
	scan(t, sc, lib.Roots[0].ID)
	if itemID(t, s, "show:show name|0") != show || itemID(t, s, "show:show name|0|s1|e1") != ep {
		t.Fatal("rename changed the item IDs")
	}
	want := []string{"episode Episode 1 1 v2", "season Season 1 1 v0", "show Show Name - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) || len(dups(t, s, lib.ID)) != 0 {
		t.Fatalf("after rename %v, flags %v", got, dups(t, s, lib.ID))
	}
}
```

**Create `server/internal/library/scan_test.go`:**

```go
package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// media is a temporary media root with library folders under it.
type media struct {
	t   *testing.T
	dir string
}

func newMedia(t *testing.T) *media { return &media{t: t, dir: t.TempDir()} }

// write creates rel with size bytes and an mtime an hour old, so it is past
// the quiet period.
func (m *media) write(rel string, size int) {
	m.t.Helper()
	p := filepath.Join(m.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		m.t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		m.t.Fatal(err)
	}
}

func (m *media) scanner(s *Store) *Scanner {
	m.t.Helper()
	r, err := os.OpenRoot(m.dir)
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(func() { r.Close() })
	return &Scanner{Store: s, Media: r}
}

func scan(t *testing.T, sc *Scanner, rootID string) Run {
	t.Helper()
	run, err := sc.ScanRoot(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func counts(r Run) [6]int {
	return [6]int{r.Seen, r.Added, r.Changed, r.Returned, r.Missing, r.Unplaced}
}

func (s *Store) fileStates(t *testing.T, rootID string) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT path, state FROM library_files WHERE root_id = ?`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, st string
		if err := rows.Scan(&p, &st); err != nil {
			t.Fatal(err)
		}
		out[p] = st
	}
	return out
}

// tree lists items as "kind title [idx] ← versions" lines, sorted.
func (s *Store) tree(t *testing.T, libID string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT i.kind, i.title, COALESCE(i.idx, -1),
		(SELECT COUNT(*) FROM library_versions v WHERE v.item_id = i.id)
		FROM library_items i WHERE i.library_id = ?`, libID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, title string
		var idx, versions int
		if err := rows.Scan(&kind, &title, &idx, &versions); err != nil {
			t.Fatal(err)
		}
		out = append(out, kind+" "+title+" "+itoa(idx)+" v"+itoa(versions))
	}
	slices.Sort(out)
	return out
}

func itoa(n int) string {
	if n < 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func TestScanBuildsItemsFromNames(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv", 10)
	m.write("Movies/Heat (1995) {tmdb-949}/Heat (1995) - 2160p.mkv", 20)
	m.write("Movies/Heat.1995.Remux/Heat.1995.Remux.mkv", 30) // same name, another folder: flagged, not merged
	m.write("Movies/Heat (1995) {tmdb-949}/Featurettes/Making Of.mkv", 5)
	m.write("TV/Show (2019)/Season 01/Show - S01E01E02 - Pilot.mkv", 40)
	m.write("TV/Show (2019)/Season 01/Show - S01E03.mkv", 50)
	ctx := context.Background()
	movies, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	tv, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)

	if got := counts(scan(t, sc, movies.Roots[0].ID)); got != [6]int{4, 4, 0, 0, 0, 1} {
		t.Fatalf("movies run %v", got)
	}
	if got, want := s.tree(t, movies.ID), []string{"movie Heat - v1", "movie Heat - v2"}; !slices.Equal(got, want) {
		t.Fatalf("movies %v, want %v", got, want)
	}
	if dups, err := s.Duplicates(ctx, movies.ID); err != nil || len(dups) != 1 || dups[0].Title != "Heat" {
		t.Fatalf("duplicates %+v %v", dups, err)
	}
	var hint string
	if err := s.db.QueryRow(`SELECT namespace || '-' || value FROM library_id_hints`).Scan(&hint); err != nil || hint != "tmdb-949" {
		t.Fatalf("hint %q %v", hint, err)
	}
	if got := s.fileStates(t, movies.Roots[0].ID); len(got) != 4 {
		t.Fatalf("files %v", got)
	}

	scan(t, sc, tv.Roots[0].ID)
	// The two-episode file gives both episodes a version on the same file. Its
	// title ("Pilot") names the file, not one episode, so both keep a
	// placeholder until META-1 fetches real titles.
	want := []string{"episode Episode 1 1 v1", "episode Episode 2 2 v1", "episode Episode 3 3 v1", "season Season 1 1 v0", "show Show - v0"}
	if got := s.tree(t, tv.ID); !slices.Equal(got, want) {
		t.Fatalf("tv %v, want %v", got, want)
	}
	var shared int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library_versions v JOIN library_files f ON f.id = v.file_id
		WHERE f.path = 'Show (2019)/Season 01/Show - S01E01E02 - Pilot.mkv'`).Scan(&shared); err != nil || shared != 2 {
		t.Fatalf("versions on the two-episode file: %d %v", shared, err)
	}
}

func TestRescanAndChanges(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)

	if got := counts(scan(t, sc, root)); got != [6]int{2, 0, 0, 0, 0, 0} {
		t.Fatalf("unchanged rescan %v", got)
	}
	m.write("Movies/A (2001)/A (2001).mkv", 11)
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	if got := counts(scan(t, sc, root)); got != [6]int{1, 0, 1, 0, 1, 0} {
		t.Fatalf("change + delete %v", got)
	}
	if st := s.fileStates(t, root)["B (2002)/B (2002).mkv"]; st != "missing" {
		t.Fatalf("deleted file is %q", st)
	}
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	if got := counts(scan(t, sc, root)); got != [6]int{2, 0, 1, 1, 0, 0} {
		t.Fatalf("return %v", got) // its mtime is new, so it also counts as changed
	}
	var missingSince *int64
	s.db.QueryRow(`SELECT missing_since FROM library_files WHERE path = 'B (2002)/B (2002).mkv'`).Scan(&missingSince)
	if missingSince != nil || s.fileStates(t, root)["B (2002)/B (2002).mkv"] != "present" {
		t.Fatal("returned file isn't present again")
	}
	if got := s.tree(t, lib.ID); len(got) != 2 {
		t.Fatalf("items %v", got)
	}
}

func TestMovedFileKeepsItsItem(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat.1995.1080p/Heat.1995.1080p.mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	// Radarr renames the folder and file: same title and year, same item.
	os.RemoveAll(filepath.Join(m.dir, "Movies/Heat.1995.1080p"))
	m.write("Movies/Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv", 10)
	if got := counts(scan(t, sc, lib.Roots[0].ID)); got != [6]int{1, 1, 0, 0, 1, 0} {
		t.Fatalf("rename run %v", got)
	}
	if got := s.tree(t, lib.ID); !slices.Equal(got, []string{"movie Heat - v2"}) {
		t.Fatalf("items %v", got) // the old version's file is missing; retirement removes it later
	}
}

func rootState(t *testing.T, s *Store, id string) (string, string) {
	t.Helper()
	r, _, err := s.root(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r.State, r.Reason
}

// SCAN-01: an unreachable, emptied or partly walked root never marks files missing.
func TestOfflineRootsNeverMarkFilesMissing(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)
	allPresent := func(when string) {
		t.Helper()
		for p, st := range s.fileStates(t, root) {
			if st != "present" {
				t.Fatalf("%s: %s is %s", when, p, st)
			}
		}
	}

	// Unmounted: the folder is gone.
	os.Rename(filepath.Join(m.dir, "Movies"), filepath.Join(m.dir, "Movies.away"))
	if run := scan(t, sc, root); run.Status != RunFailed || run.Reason != ReasonRootUnavailable {
		t.Fatalf("missing folder: %+v", run)
	}
	if st, why := rootState(t, s, root); st != RootOffline || why != ReasonRootUnavailable {
		t.Fatalf("root %s %s", st, why)
	}
	allPresent("missing folder")

	// Mounted but empty: an empty mount point, not a deleted library.
	os.Mkdir(filepath.Join(m.dir, "Movies"), 0o755)
	if run := scan(t, sc, root); run.Status != RunFailed || run.Reason != ReasonRootEmpty {
		t.Fatalf("empty folder: %+v", run)
	}
	allPresent("empty folder")

	// Back: a normal scan, root available again.
	os.Remove(filepath.Join(m.dir, "Movies"))
	os.Rename(filepath.Join(m.dir, "Movies.away"), filepath.Join(m.dir, "Movies"))
	if run := scan(t, sc, root); run.Status != RunCompleted || run.Missing != 0 {
		t.Fatalf("returned root: %+v", run)
	}
	if st, why := rootState(t, s, root); st != RootAvailable || why != "" {
		t.Fatalf("root %s %q", st, why)
	}

	// A walk that stops part-way (an unreadable folder) keeps what it saw
	// and marks nothing missing.
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		if err := Discover(ctx, r, func(f File) error {
			if f.Path == "B (2002)/B (2002).mkv" {
				return errors.New("permission denied")
			}
			return found(f)
		}); err != nil {
			return err
		}
		return nil
	}
	if run := scan(t, sc, root); run.Status != RunFailed || run.Reason != ReasonEnumerationFailed || run.Seen != 1 {
		t.Fatalf("partial walk: %+v", run)
	}
	if st, _ := rootState(t, s, root); st != RootDegraded {
		t.Fatalf("root after partial walk: %s", st)
	}
	allPresent("partial walk")
}

func TestCancelledScanKeepsWorkAndMarksNothing(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	m.write("Movies/C (2003)/C (2003).mkv", 10)

	cctx, cancel := context.WithCancel(ctx)
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		return Discover(ctx, r, func(f File) error {
			err := found(f)
			if f.Path == "C (2003)/C (2003).mkv" {
				cancel() // stop after the new file
			}
			return err
		})
	}
	run, err := sc.ScanRoot(cctx, root)
	if err != nil || run.Status != RunCancelled || run.Added != 1 || run.Missing != 0 {
		t.Fatalf("cancelled run %+v %v", run, err)
	}
	if got := s.fileStates(t, root); got["C (2003)/C (2003).mkv"] != "present" || got["B (2002)/B (2002).mkv"] != "present" {
		t.Fatalf("after cancel %v", got) // C kept; B not marked missing
	}
	if st, _ := rootState(t, s, root); st != RootAvailable {
		t.Fatalf("root after cancel: %s", st)
	}
}

func TestMarkInterrupted(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	s.db.Exec(`INSERT INTO library_scan_runs(id, root_id, started_at, status) VALUES('scan-x', ?, 1, 'running')`, root)
	s.setRoot(ctx, root, RootScanning, "", false)
	if n, err := s.MarkInterrupted(ctx); n != 1 || err != nil {
		t.Fatalf("MarkInterrupted = %d, %v", n, err)
	}
	var status string
	s.db.QueryRow(`SELECT status FROM library_scan_runs WHERE id = 'scan-x'`).Scan(&status)
	if st, why := rootState(t, s, root); status != RunInterrupted || st != RootDegraded || why != "interrupted" {
		t.Fatalf("run %s, root %s %s", status, st, why)
	}
}

// A file naming couldn't place is tried again on every scan, so files that a
// later version's naming rules understand are picked up without a rescan of
// everything.
func TestUnplacedFilesArePlacedAgain(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	// As if an older version had failed to place it.
	s.db.Exec(`DELETE FROM library_items`)
	s.db.Exec(`UPDATE library_files SET unplaced = 'unrecognised'`)
	if run := scan(t, sc, lib.Roots[0].ID); run.Unplaced != 0 {
		t.Fatalf("%+v", run)
	}
	var unplaced *string
	s.db.QueryRow(`SELECT unplaced FROM library_files`).Scan(&unplaced)
	if got := s.tree(t, lib.ID); unplaced != nil || !slices.Equal(got, []string{"movie A - v1"}) {
		t.Fatalf("items %v, unplaced %v", got, unplaced)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `undefined: Duplicate`, `s.Duplicates undefined`, `s.Decide undefined` in `duplicates_test.go` (and `undefined: Scanner`, `Run`, `RunFailed` in `scan_test.go`).

- [ ] **Step 3: Implement**

**Create `server/internal/library/duplicates.go`:**

```go
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// groupItem returns the movie or show item for e's folder, creating it if
// needed. A folder already mapped to an item always uses it. A new folder
// whose key another folder's item already has gets a provisional item of its
// own and a pending duplicate pair, which settle resolves after a completed
// scan (Pete's rule on #90: two folders that exist at once are never merged
// automatically).
func (s *Store) groupItem(ctx context.Context, tx *sql.Tx, root Root, e naming.Entry, it item) (id, key string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT f.item_id, i.scan_key FROM library_folders f JOIN library_items i ON i.id = f.item_id
		WHERE f.root_id = ? AND f.folder = ? AND f.scan_key = ?`, root.ID, e.Folder, e.Key).Scan(&id, &key)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, key, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM library_items WHERE library_id = ? AND scan_key = ?`, root.LibraryID, e.Key).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		it.key = e.Key
	case err != nil:
		return "", "", err
	default:
		// Provisional until settle decides. It must not start with e.Key + "|",
		// or it would look like one of the existing item's seasons.
		it.key = "dup:" + root.ID + ":" + e.Folder + ":" + e.Key
	}
	if id, err = s.item(ctx, tx, root.LibraryID, it); err != nil {
		return "", "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_folders(root_id, folder, scan_key, item_id) VALUES(?, ?, ?, ?)`,
		root.ID, e.Folder, e.Key, id); err != nil {
		return "", "", err
	}
	if existing != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO library_duplicates(id, root_id, item_id, other_id, state) VALUES(?, ?, ?, ?, 'pending')`,
			newID("dup"), root.ID, existing, id); err != nil {
			return "", "", err
		}
	}
	return id, it.key, nil
}

// settle resolves the root's pending duplicate pairs after a completed scan,
// once files that weren't found have been marked missing. If the existing
// item has no present file left, the new folder replaced it (a rename or
// move) and is folded in, keeping the item's identity. Otherwise both folders
// exist and the pair is flagged for the admin.
func (s *Store) settle(ctx context.Context, rootID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, item_id, other_id FROM library_duplicates WHERE root_id = ? AND state = 'pending'`, rootID)
	if err != nil {
		return err
	}
	type pair struct{ id, item, other string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.item, &p.other); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	for _, p := range pairs {
		alive, err := hasPresentFile(ctx, tx, p.item)
		if err != nil {
			return err
		}
		if alive {
			_, err = tx.ExecContext(ctx, `UPDATE library_duplicates SET state = 'flagged' WHERE id = ?`, p.id)
		} else {
			err = fold(ctx, tx, p.item, p.other)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// hasPresentFile reports whether any version under the item (or, for a
// show, under its seasons and episodes) is on a present file.
func hasPresentFile(ctx context.Context, tx *sql.Tx, itemID string) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM library_versions v
		JOIN library_files f ON f.id = v.file_id JOIN library_items i ON i.id = v.item_id
		JOIN library_items top ON top.id = ?
		WHERE f.state = 'present' AND (i.id = top.id OR substr(i.scan_key, 1, length(top.scan_key) + 1) = top.scan_key || '|'))`, itemID).Scan(&ok)
	return ok, err
}

// fold moves everything under item other into item into: versions, ID hints,
// folder mappings and, for a show, its seasons and episodes (merged with ones
// into already has, by key). Then other is deleted, with its pair.
func fold(ctx context.Context, tx *sql.Tx, into, other string) error {
	var intoKey, otherKey string
	if err := tx.QueryRowContext(ctx, `SELECT scan_key FROM library_items WHERE id = ?`, into).Scan(&intoKey); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT scan_key FROM library_items WHERE id = ?`, other).Scan(&otherKey); err != nil {
		return err
	}
	// Children in key order, so a season is handled before its episodes.
	rows, err := tx.QueryContext(ctx, `SELECT id, scan_key FROM library_items
		WHERE substr(scan_key, 1, length(?) + 1) = ? || '|' ORDER BY length(scan_key)`, otherKey, otherKey)
	if err != nil {
		return err
	}
	type child struct{ id, key string }
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.id, &c.key); err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	rows.Close()
	for _, c := range children {
		target := intoKey + strings.TrimPrefix(c.key, otherKey)
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM library_items WHERE scan_key = ? AND library_id = (SELECT library_id FROM library_items WHERE id = ?)`,
			target, into).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Move it across: new key, parent = the item now at its parent key.
			parentKey := target[:strings.LastIndex(target, "|")]
			if _, err := tx.ExecContext(ctx, `UPDATE library_items SET scan_key = ?, parent_id = (SELECT id FROM library_items
				WHERE scan_key = ? AND library_id = library_items.library_id) WHERE id = ?`, target, parentKey, c.id); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if err := moveInto(ctx, tx, existing, c.id); err != nil {
				return err
			}
		}
	}
	// Move the folder mappings before moveInto deletes other: they cascade.
	if _, err := tx.ExecContext(ctx, `UPDATE library_folders SET item_id = ? WHERE item_id = ?`, into, other); err != nil {
		return err
	}
	return moveInto(ctx, tx, into, other)
}

// moveInto gives item into the versions and ID hints of item from, then
// deletes from (its children have already been moved).
func moveInto(ctx context.Context, tx *sql.Tx, into, from string) error {
	for _, q := range []string{
		`UPDATE OR IGNORE library_versions SET item_id = ? WHERE item_id = ?`,
		`INSERT OR IGNORE INTO library_id_hints(item_id, namespace, value) SELECT ?, namespace, value FROM library_id_hints WHERE item_id = ?`,
		`UPDATE library_items SET parent_id = ? WHERE parent_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, into, from); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM library_items WHERE id = ?`, from)
	return err
}

// Duplicate is a possible duplicate the admin decides: two folders of the
// library whose names give the same movie or show.
type Duplicate struct {
	ID, ItemID, OtherID, Title string
}

// Duplicates lists the library's flagged pairs.
// Planned: LIB-2c (the admin duplicates screen).
func (s *Store) Duplicates(ctx context.Context, libID string) ([]Duplicate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.item_id, d.other_id, i.title FROM library_duplicates d
		JOIN library_items i ON i.id = d.item_id WHERE i.library_id = ? AND d.state = 'flagged' ORDER BY i.sort_title, d.id`, libID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Duplicate
	for rows.Next() {
		var d Duplicate
		if err := rows.Scan(&d.ID, &d.ItemID, &d.OtherID, &d.Title); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Decide records the admin's decision on a flagged pair. Merge folds the
// other folder's item into the existing one (one item, both folders'
// versions); otherwise both stay separate. Either way the folders keep their
// mapping, so the pair isn't flagged again.
// Planned: LIB-2c (the admin duplicates screen).
func (s *Store) Decide(ctx context.Context, dupID string, merge bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var item, other string
	err = tx.QueryRowContext(ctx, `SELECT item_id, other_id FROM library_duplicates WHERE id = ? AND state = 'flagged'`, dupID).Scan(&item, &other)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no flagged duplicate %s", ErrNotFound, dupID)
	}
	if err != nil {
		return err
	}
	if merge {
		err = fold(ctx, tx, item, other)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE library_duplicates SET state = 'separate' WHERE id = ?`, dupID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
```

**Create `server/internal/library/scan.go`:**

```go
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// Scan run statuses and failure reasons.
const (
	RunRunning     = "running"
	RunCompleted   = "completed"
	RunFailed      = "failed"
	RunCancelled   = "cancelled"
	RunInterrupted = "interrupted"

	ReasonRootUnavailable   = "root_unavailable"   // the folder can't be opened
	ReasonRootEmpty         = "root_empty"         // empty, while the catalogue has files there
	ReasonEnumerationFailed = "enumeration_failed" // the walk stopped part-way
)

// Run is the outcome of one scan of one root.
type Run struct {
	ID, Status, Reason                                string
	Seen, Added, Changed, Returned, Missing, Unplaced int
}

// Scanner reconciles the catalogue with the files under each root.
type Scanner struct {
	Store *Store
	Media *os.Root // the media root; library roots are folders inside it
	// Discover walks a root; nil means the package's Discover. Tests replace it.
	Discover func(context.Context, *os.Root, func(File) error) error
}

// batchSize bounds each write transaction, so a large scan never holds the
// database's single writer for long (spec §10.2).
const batchSize = 500

// ScanRoot walks one root and brings its files, items and versions up to
// date. Files that weren't found are marked missing only after a complete
// walk of a reachable, non-empty root (spec §3.6, SCAN-01); a failed or
// cancelled scan keeps the work it did and marks nothing missing. The error
// is for database failures only; a failed scan is reported in Run.
// Planned: LIB-2b (the watcher, schedule and Scan now call it).
func (sc *Scanner) ScanRoot(ctx context.Context, rootID string) (Run, error) {
	st := sc.Store
	root, libType, err := st.root(ctx, rootID)
	if err != nil {
		return Run{}, err
	}
	run := Run{ID: newID("scan"), Status: RunRunning}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO library_scan_runs(id, root_id, started_at, status) VALUES(?, ?, ?, 'running')`,
		run.ID, rootID, ms(st.now())); err != nil {
		return Run{}, err
	}
	if err := st.setRoot(ctx, rootID, RootScanning, root.Reason, false); err != nil {
		return Run{}, err
	}
	// The run's bookkeeping must be written even if ctx was cancelled.
	bg := context.WithoutCancel(ctx)

	dir, err := sc.Media.OpenRoot(root.Path)
	if err == nil {
		var info os.FileInfo
		if info, err = dir.Stat("."); err == nil && !info.IsDir() {
			err = fmt.Errorf("library: %s is not a folder", root.Path)
		}
		if err != nil {
			dir.Close()
		}
	}
	if err != nil {
		return sc.finish(bg, root, run, RunFailed, ReasonRootUnavailable, RootOffline)
	}
	defer dir.Close()

	discover := sc.Discover
	if discover == nil {
		discover = Discover
	}
	var batch []File
	flush := func() error {
		err := st.applyBatch(bg, root, libType, run.ID, batch, &run)
		batch = batch[:0]
		return err
	}
	walkErr := discover(ctx, dir, func(f File) error {
		if batch = append(batch, f); len(batch) == batchSize {
			return flush()
		}
		return nil
	})
	if err := flush(); err != nil {
		return run, err
	}
	switch {
	case ctx.Err() != nil:
		return sc.finish(bg, root, run, RunCancelled, "", root.State)
	case walkErr != nil:
		return sc.finish(bg, root, run, RunFailed, ReasonEnumerationFailed, RootDegraded)
	}
	if run.Seen == 0 {
		var present int
		if err := st.db.QueryRowContext(bg, `SELECT COUNT(*) FROM library_files WHERE root_id = ? AND state = 'present'`, rootID).Scan(&present); err != nil {
			return run, err
		}
		if present > 0 {
			// An empty folder where files were is far more likely an unmounted
			// disk than a deleted library.
			return sc.finish(bg, root, run, RunFailed, ReasonRootEmpty, RootOffline)
		}
	}
	res, err := st.db.ExecContext(bg, `UPDATE library_files SET state = 'missing', missing_since = COALESCE(missing_since, ?)
		WHERE root_id = ? AND state = 'present' AND last_seen_run IS NOT ?`, ms(st.now()), rootID, run.ID)
	if err != nil {
		return run, err
	}
	n, _ := res.RowsAffected()
	run.Missing = int(n)
	if err := st.settle(bg, rootID); err != nil {
		return run, err
	}
	return sc.finish(bg, root, run, RunCompleted, "", RootAvailable)
}

// finish records the run's outcome and the root's resulting state.
func (sc *Scanner) finish(ctx context.Context, root Root, run Run, status, reason, rootState string) (Run, error) {
	run.Status, run.Reason = status, reason
	_, err := sc.Store.db.ExecContext(ctx, `UPDATE library_scan_runs SET status = ?, reason = NULLIF(?, ''), finished_at = ?,
		seen = ?, added = ?, changed = ?, returned = ?, missing = ?, unplaced = ? WHERE id = ?`,
		status, reason, ms(sc.Store.now()), run.Seen, run.Added, run.Changed, run.Returned, run.Missing, run.Unplaced, run.ID)
	if err != nil {
		return run, err
	}
	if status == RunCancelled {
		reason = root.Reason
	}
	return run, sc.Store.setRoot(ctx, root.ID, rootState, reason, status == RunCompleted)
}

func (s *Store) setRoot(ctx context.Context, id, state, reason string, ok bool) error {
	q := `UPDATE library_roots SET state = ?, reason = NULLIF(?, '') WHERE id = ?`
	args := []any{state, reason, id}
	if ok {
		q = `UPDATE library_roots SET state = ?, reason = NULLIF(?, ''), last_ok_at = ? WHERE id = ?`
		args = []any{state, reason, ms(s.now()), id}
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// applyBatch records one batch of discovered files in a single transaction:
// new files are added and placed, changed revisions replace the old ones
// (making the probe stale), and missing files that are back become present.
func (s *Store) applyBatch(ctx context.Context, root Root, libType, runID string, files []File, run *Run) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range files {
		run.Seen++
		var id, state string
		var size, mtime int64
		var unplaced sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, size, mtime_ns, state, unplaced FROM library_files WHERE root_id = ? AND path = ?`,
			root.ID, f.Path).Scan(&id, &size, &mtime, &state, &unplaced)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id = newID("file")
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_files(id, root_id, path, size, mtime_ns, last_seen_run) VALUES(?, ?, ?, ?, ?, ?)`,
				id, root.ID, f.Path, f.Rev.Size, f.Rev.ModTime, runID); err != nil {
				return err
			}
			run.Added++
		case err != nil:
			return err
		default:
			if size != f.Rev.Size || mtime != f.Rev.ModTime {
				run.Changed++
			}
			if state != "present" {
				run.Returned++
			}
			if _, err := tx.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, state = 'present', missing_since = NULL,
				last_seen_run = ? WHERE id = ?`, f.Rev.Size, f.Rev.ModTime, runID, id); err != nil {
				return err
			}
			if !unplaced.Valid {
				continue // already placed
			}
		}
		if err := s.place(ctx, tx, root, libType, id, f, run); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// place attaches a file to its items (creating them as needed), or records
// why naming couldn't place it.
func (s *Store) place(ctx context.Context, tx *sql.Tx, root Root, libType, fileID string, f File, run *Run) error {
	libID := root.LibraryID
	e, err := naming.Classify(libType, f.Path, f.Rev.Size)
	if err != nil {
		run.Unplaced++
		reason := "unrecognised"
		switch {
		case errors.Is(err, naming.ErrExtra):
			reason = "extra"
		case errors.Is(err, naming.ErrSample):
			reason = "sample"
		}
		_, err := tx.ExecContext(ctx, `UPDATE library_files SET unplaced = ? WHERE id = ?`, reason, fileID)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_files SET unplaced = NULL WHERE id = ?`, fileID); err != nil {
		return err
	}
	var year any
	if e.Year != 0 {
		year = e.Year
	}
	if e.Kind == naming.Movie {
		movie, _, err := s.groupItem(ctx, tx, root, e, item{kind: "movie", title: e.Title, year: year})
		if err != nil {
			return err
		}
		if err := s.hints(ctx, tx, movie, e.IDs); err != nil {
			return err
		}
		return s.version(ctx, tx, movie, fileID, e.Version)
	}
	show, showKey, err := s.groupItem(ctx, tx, root, e, item{kind: "show", title: e.Title, year: year})
	if err != nil {
		return err
	}
	if err := s.hints(ctx, tx, show, e.IDs); err != nil {
		return err
	}
	seasonTitle := "Season " + strconv.Itoa(e.Season)
	if e.Season == 0 {
		seasonTitle = "Specials"
	}
	seasonKey := showKey + "|s" + strconv.Itoa(e.Season)
	season, err := s.item(ctx, tx, libID, item{kind: "season", key: seasonKey, title: seasonTitle, parent: show, idx: e.Season})
	if err != nil {
		return err
	}
	type ep struct {
		key, title string
		idx, abs   any
		airDate    any
	}
	var eps []ep
	if e.AirDate != "" {
		eps = []ep{{key: seasonKey + "|d" + e.AirDate, title: firstNonEmpty(e.EpisodeTitle, e.AirDate), airDate: e.AirDate}}
	}
	for _, n := range e.Episodes {
		x := ep{key: seasonKey + "|e" + strconv.Itoa(n), title: "Episode " + strconv.Itoa(n), idx: n}
		if len(e.Episodes) == 1 {
			x.title = firstNonEmpty(e.EpisodeTitle, x.title)
			if e.Absolute != nil {
				x.abs = *e.Absolute
			}
		}
		eps = append(eps, x)
	}
	for _, x := range eps {
		id, err := s.item(ctx, tx, libID, item{kind: "episode", key: x.key, title: x.title, parent: season,
			idx: x.idx, absolute: x.abs, airDate: x.airDate})
		if err != nil {
			return err
		}
		// Every episode in a multi-episode file gets a version on the same
		// file (CTR-2: same fileId, unknown segment).
		if err := s.version(ctx, tx, id, fileID, e.Version); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// idPrefix makes item IDs readable in logs; clients treat them as opaque.
var idPrefix = map[string]string{"movie": "mov", "show": "shw", "season": "sea", "episode": "epi"}

type item struct {
	kind, key, title             string
	parent                       any // item ID or nil
	year, idx, absolute, airDate any
}

// item returns the ID of the library's item with it.key, creating it if
// needed. An existing item keeps its title: later edits (META-1) win.
func (s *Store) item(ctx context.Context, tx *sql.Tx, libID string, it item) (string, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_items(id, library_id, kind, parent_id, scan_key, title, sort_title, year, idx, air_date, absolute, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(library_id, scan_key) DO NOTHING`,
		newID(idPrefix[it.kind]), libID, it.kind, it.parent, it.key, it.title, naming.SortTitle(it.title), it.year, it.idx, it.airDate, it.absolute, ms(s.now())); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM library_items WHERE library_id = ? AND scan_key = ?`, libID, it.key).Scan(&id)
	return id, err
}

func (s *Store) version(ctx context.Context, tx *sql.Tx, itemID, fileID, label string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO library_versions(id, item_id, file_id, label) VALUES(?, ?, ?, NULLIF(?, ''))
		ON CONFLICT(item_id, file_id) DO NOTHING`, newID("ver"), itemID, fileID, label)
	return err
}

func (s *Store) hints(ctx context.Context, tx *sql.Tx, itemID string, ids map[string]string) error {
	for ns, v := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_id_hints(item_id, namespace, value) VALUES(?, ?, ?) ON CONFLICT DO NOTHING`,
			itemID, ns, v); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
gofmt -l server   # prints nothing
go -C server test -race -count=1 ./internal/library/...
```

Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/duplicates.go server/internal/library/duplicates_test.go server/internal/library/scan.go server/internal/library/scan_test.go
git commit -m "feat(library): offline-safe scans into items, versions and duplicate flags (LIB-2a)"
```

---

### Task 4: Probe stale revisions (compare-and-set, quiet period)

**Files:**
- Create: `server/internal/library/probes.go`
- Test: `server/internal/library/probes_test.go`

**Interfaces:**
- Consumes: Task 3's `Scanner` and test helpers; LIB-1's `NewQueue`, `Prober`, `Outcome`, `ErrNotStable`, `ErrChanged`, `ErrGone`; `probe.Result`, `probe.ErrTimeout`.
- Produces: `(*Scanner).ProbeDue(ctx, rootID string, p Prober, workers int, quiet time.Duration) (ProbeRun, error)`; `ProbeRun{Queued, Stored, Stale, Deferred, Failed, Gone}`; `(*Store).RetryProbe(ctx, fileID) error`. Test helpers `scriptedProber`, `setupMovies(t, files...) (*Store, *clock, *media, *Scanner, rootID)`, `quiet`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/probes_test.go`:**

```go
package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// scriptedProber returns a result naming the file, or the error set for it, and
// runs during() inside the probe when set.
type scriptedProber struct {
	mu     sync.Mutex
	calls  []string
	fail   map[string]error
	during func(rel string)
}

func (f *scriptedProber) Probe(_ context.Context, _ *os.Root, rel string) (probe.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, rel)
	err := f.fail[rel]
	during := f.during
	f.mu.Unlock()
	if during != nil {
		during(rel)
	}
	if err != nil {
		return probe.Result{}, err
	}
	return probe.Result{Container: "fake:" + rel, DurationMs: 1000}, nil
}

const quiet = 30 * time.Second

func probeDue(t *testing.T, sc *Scanner, root string, p Prober) ProbeRun {
	t.Helper()
	run, err := sc.ProbeDue(context.Background(), root, p, 2, quiet)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func probeColumns(t *testing.T, s *Store, rel string) (json, probeErr *string, after *int64) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT probe_json, probe_error, probe_after FROM library_files WHERE path = ?`, rel).
		Scan(&json, &probeErr, &after); err != nil {
		t.Fatal(err)
	}
	return
}

func setupMovies(t *testing.T, files ...string) (*Store, *clock, *media, *Scanner, string) {
	t.Helper()
	s, c := newStore(t)
	m := newMedia(t)
	for _, f := range files {
		m.write("Movies/"+f, 10)
	}
	lib, err := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	return s, c, m, sc, lib.Roots[0].ID
}

// SCALE-01: a probed file isn't probed again until its revision changes.
func TestProbeDueProbesEachRevisionOnce(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	p := &scriptedProber{}
	if run := probeDue(t, sc, root, p); run.Queued != 2 || run.Stored != 2 {
		t.Fatalf("first %+v", run)
	}
	if js, _, _ := probeColumns(t, s, "A (2001)/A (2001).mkv"); js == nil || *js == "" {
		t.Fatal("no probe stored")
	}
	if run := probeDue(t, sc, root, p); run.Queued != 0 || len(p.calls) != 2 {
		t.Fatalf("unchanged files were probed again: %+v %v", run, p.calls)
	}
	m.write("Movies/A (2001)/A (2001).mkv", 11)
	scan(t, sc, root)
	if run := probeDue(t, sc, root, p); run.Queued != 1 || run.Stored != 1 {
		t.Fatalf("after a change %+v", run)
	}
}

func TestProbeFailureIsKeptUntilRetried(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{fail: map[string]error{"A (2001)/A (2001).mkv": probe.ErrTimeout}}
	if run := probeDue(t, sc, root, p); run.Failed != 1 {
		t.Fatalf("%+v", run)
	}
	if _, perr, _ := probeColumns(t, s, "A (2001)/A (2001).mkv"); perr == nil || *perr != probe.ErrTimeout.Error() {
		t.Fatalf("probe_error %v", perr)
	}
	if run := probeDue(t, sc, root, p); run.Queued != 0 {
		t.Fatalf("a failed probe was retried without a change or a retry: %+v", run)
	}
	var id string
	s.db.QueryRow(`SELECT id FROM library_files`).Scan(&id)
	if err := s.RetryProbe(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	p.fail = nil
	if run := probeDue(t, sc, root, p); run.Stored != 1 {
		t.Fatalf("after RetryProbe %+v", run)
	}
	if err := s.RetryProbe(context.Background(), "file-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown file: %v", err)
	}
}

// #62: a file still being written waits for the quiet period instead of
// being re-queued in a loop.
func TestStillChangingFileWaitsForQuietPeriod(t *testing.T) {
	s, c, m, sc, root := setupMovies(t)
	c.t = time.Now()
	p := filepath.Join(m.dir, "Movies/New (2026)/New (2026).mkv")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, make([]byte, 10), 0o644) // mtime: now
	scan(t, sc, root)
	if run := probeDue(t, sc, root, &scriptedProber{}); run.Deferred != 1 || run.Stored != 0 {
		t.Fatalf("fresh file %+v", run)
	}
	_, _, after := probeColumns(t, s, "New (2026)/New (2026).mkv")
	info, _ := os.Stat(p)
	if after == nil || *after != info.ModTime().Add(quiet).UnixMilli() {
		t.Fatalf("probe_after %v, want mtime + quiet", after)
	}
	if run := probeDue(t, sc, root, &scriptedProber{}); run.Queued != 0 {
		t.Fatalf("queued again before the quiet period ended: %+v", run)
	}
	c.t = info.ModTime().Add(quiet + time.Second)
	if run := probeDue(t, sc, root, &scriptedProber{}); run.Queued != 1 {
		t.Fatalf("not queued after the quiet period: %+v", run)
	}
}

// A result for a revision the catalogue no longer holds is dropped.
func TestStaleProbeResultIsDropped(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{during: func(string) {
		// A scan records a newer revision while the probe runs.
		s.db.Exec(`UPDATE library_files SET size = size + 1`)
	}}
	if run := probeDue(t, sc, root, p); run.Stale != 1 || run.Stored != 0 {
		t.Fatalf("%+v", run)
	}
	if js, _, _ := probeColumns(t, s, "A (2001)/A (2001).mkv"); js != nil {
		t.Fatal("stale result stored")
	}
}

// A file that changes on disk during its probe is deferred with its new revision.
func TestFileChangedDuringProbeIsDeferred(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{during: func(rel string) { m.write("Movies/"+rel, 99) }}
	if run := probeDue(t, sc, root, p); run.Deferred != 1 {
		t.Fatalf("%+v", run)
	}
	var size int64
	s.db.QueryRow(`SELECT size FROM library_files`).Scan(&size)
	if size != 99 {
		t.Fatalf("catalogue size %d, want the new revision's 99", size)
	}
}

// A deferred outcome never replaces a newer revision a scan recorded while
// the probe ran (#90 review).
func TestDeferredRevisionNeverMovesBackwards(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	const later = int64(1) << 62
	p := &scriptedProber{during: func(rel string) {
		m.write("Movies/"+rel, 99)                                // the probe sees the file change...
		s.db.Exec(`UPDATE library_files SET mtime_ns = ?`, later) // ...and a scan records a newer revision
	}}
	if run := probeDue(t, sc, root, p); run.Deferred != 1 {
		t.Fatalf("%+v", run)
	}
	var mtime int64
	s.db.QueryRow(`SELECT mtime_ns FROM library_files`).Scan(&mtime)
	if mtime != later {
		t.Fatalf("mtime_ns %d: the deferred outcome overwrote the newer revision", mtime)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `undefined: ProbeRun`, `sc.ProbeDue undefined`, `s.RetryProbe undefined` in `probes_test.go`.

- [ ] **Step 3: Implement**

**Create `server/internal/library/probes.go`:**

```go
package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ProbeRun counts what one ProbeDue call did.
type ProbeRun struct {
	Queued   int // files due for a probe
	Stored   int // results saved
	Stale    int // results dropped: a scan saw a newer revision meanwhile
	Deferred int // still changing: retried after the quiet period
	Failed   int // probe errors, kept for the admin list (spec §3.1)
	Gone     int // vanished; the next scan marks them missing
}

// maxProbeError bounds the stored error text.
const maxProbeError = 500

// ProbeDue probes the root's present files whose probe doesn't match their
// current revision, using LIB-1's queue with workers at a time. Unchanged
// files are never probed again (SCALE-01). A result is saved only if the
// file still has the revision it describes (compare-and-set on size and
// mtime). A file that is still changing gets a not-before time instead of
// being re-queued at once (#62).
// Planned: LIB-2b (the scan loop runs it after each scan and on its schedule).
func (sc *Scanner) ProbeDue(ctx context.Context, rootID string, p Prober, workers int, quiet time.Duration) (ProbeRun, error) {
	st := sc.Store
	root, _, err := st.root(ctx, rootID)
	if err != nil {
		return ProbeRun{}, err
	}
	paths, err := queryStrings(ctx, st.db, `SELECT path FROM library_files WHERE root_id = ? AND state = 'present'
		AND (probe_size IS NOT size OR probe_mtime_ns IS NOT mtime_ns)
		AND (probe_after IS NULL OR probe_after <= ?) ORDER BY path`, rootID, ms(st.now()))
	if err != nil || len(paths) == 0 {
		return ProbeRun{}, err
	}
	dir, err := sc.Media.OpenRoot(root.Path)
	if err != nil {
		return ProbeRun{}, fmt.Errorf("library: root %s unavailable: %w", rootID, err)
	}
	defer dir.Close()

	run := ProbeRun{Queued: len(paths)}
	var mu sync.Mutex
	var firstErr error
	left := len(paths)
	qctx, stop := context.WithCancel(ctx)
	defer stop()
	q := NewQueue(dir, p, quiet, func(o Outcome) {
		mu.Lock()
		defer mu.Unlock()
		if err := st.record(context.WithoutCancel(ctx), rootID, o, quiet, &run); err != nil && firstErr == nil {
			firstErr = err
		}
		if left--; left == 0 {
			stop()
		}
	})
	for _, p := range paths {
		q.Add(p)
	}
	q.Run(qctx, workers)
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return run, firstErr
}

// record saves one probe outcome.
func (s *Store) record(ctx context.Context, rootID string, o Outcome, quiet time.Duration, run *ProbeRun) error {
	switch {
	case o.Err == nil:
		js, err := json.Marshal(o.Probe)
		if err != nil {
			return err
		}
		res, err := s.db.ExecContext(ctx, `UPDATE library_files SET probe_size = ?, probe_mtime_ns = ?, probe_json = ?,
			probe_error = NULL, probe_after = NULL WHERE root_id = ? AND path = ? AND size = ? AND mtime_ns = ?`,
			o.Rev.Size, o.Rev.ModTime, string(js), rootID, o.Path, o.Rev.Size, o.Rev.ModTime)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			run.Stale++
			return nil
		}
		run.Stored++
	case errors.Is(o.Err, ErrNotStable), errors.Is(o.Err, ErrChanged):
		// Record the newer revision and wait until it has been quiet.
		after := time.Unix(0, o.Rev.ModTime).Add(quiet)
		// Only ever forward: a scan may already have recorded a newer revision.
		_, err := s.db.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, probe_after = ?
			WHERE root_id = ? AND path = ? AND mtime_ns <= ?`, o.Rev.Size, o.Rev.ModTime, ms(after), rootID, o.Path, o.Rev.ModTime)
		if err != nil {
			return err
		}
		run.Deferred++
	case errors.Is(o.Err, ErrGone):
		run.Gone++
	default:
		msg := o.Err.Error()
		if len(msg) > maxProbeError {
			msg = msg[:maxProbeError]
		}
		res, err := s.db.ExecContext(ctx, `UPDATE library_files SET probe_size = ?, probe_mtime_ns = ?, probe_json = NULL,
			probe_error = ?, probe_after = NULL WHERE root_id = ? AND path = ? AND size = ? AND mtime_ns = ?`,
			o.Rev.Size, o.Rev.ModTime, msg, rootID, o.Path, o.Rev.Size, o.Rev.ModTime)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			run.Stale++
			return nil
		}
		run.Failed++
	}
	return nil
}

// RetryProbe clears a file's probe so the next ProbeDue probes it again: the
// admin's explicit retry after a failure (spec §3.6).
// Planned: LIB-2c (the admin retry action).
func (s *Store) RetryProbe(ctx context.Context, fileID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE library_files SET probe_size = NULL, probe_mtime_ns = NULL, probe_json = NULL,
		probe_error = NULL, probe_after = NULL WHERE id = ?`, fileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
gofmt -l server   # prints nothing
go -C server test -race -count=1 ./internal/library/...
```

Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/probes.go server/internal/library/probes_test.go
git commit -m "feat(library): probe stale revisions with compare-and-set and a quiet period (LIB-2a)"
```

---

### Task 5: Retirement preview and explicit retire

**Files:**
- Create: `server/internal/library/retire.go`
- Test: `server/internal/library/retire_test.go`

**Interfaces:**
- Consumes: Task 4's `setupMovies`; Task 3's scans and `tree`; the `library_scan_runs` statuses.
- Produces: `RetireAfter = 24 * time.Hour`; `Candidate{FileID, Path, MissingSince}`; `(*Store).RetirementCandidates(ctx, rootID) ([]Candidate, error)`; `(*Store).Retire(ctx, rootID string, fileIDs []string) (int, error)`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/retire_test.go`:**

```go
package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetirementNeedsTwoSuccessfulScansADayApart(t *testing.T) {
	s, c, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	ctx := context.Background()
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	scan(t, sc, root) // B first missed now
	candidates := func() int {
		t.Helper()
		cs, err := s.RetirementCandidates(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		return len(cs)
	}

	c.t = c.t.Add(time.Hour)
	scan(t, sc, root)
	if n := candidates(); n != 0 {
		t.Fatalf("%d candidates after 1 h", n)
	}

	// An outage: a day passes, but the root is unreachable, so failed scans
	// don't confirm anything.
	os.Rename(filepath.Join(m.dir, "Movies"), filepath.Join(m.dir, "away"))
	c.t = c.t.Add(25 * time.Hour)
	if run := scan(t, sc, root); run.Status != RunFailed {
		t.Fatalf("outage scan %+v", run)
	}
	if n := candidates(); n != 0 {
		t.Fatalf("%d candidates from failed scans", n)
	}

	os.Rename(filepath.Join(m.dir, "away"), filepath.Join(m.dir, "Movies"))
	scan(t, sc, root)
	cs, _ := s.RetirementCandidates(ctx, root)
	if len(cs) != 1 || cs[0].Path != "B (2002)/B (2002).mkv" {
		t.Fatalf("candidates %+v", cs)
	}

	var presentID string
	s.db.QueryRow(`SELECT id FROM library_files WHERE state = 'present'`).Scan(&presentID)
	if n, err := s.Retire(ctx, root, []string{cs[0].FileID, presentID}); n != 1 || err != nil {
		t.Fatalf("Retire = %d, %v (the present file must be refused)", n, err)
	}
	if got := s.fileStates(t, root); got["B (2002)/B (2002).mkv"] != "retired" || got["A (2001)/A (2001).mkv"] != "present" {
		t.Fatalf("states %v", got)
	}
	// The item and its user-facing identity remain.
	if got := s.tree(t, mustLibID(t, s, root)); len(got) != 2 {
		t.Fatalf("items %v", got)
	}

	// A retired file that comes back is present again.
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	if run := scan(t, sc, root); run.Returned != 1 {
		t.Fatalf("return %+v", run)
	}
}

func mustLibID(t *testing.T, s *Store, root string) string {
	t.Helper()
	r, _, err := s.root(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return r.LibraryID
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `s.RetirementCandidates undefined`, `s.Retire undefined` in `retire_test.go`.

- [ ] **Step 3: Implement**

**Create `server/internal/library/retire.go`:**

```go
package library

import (
	"context"
	"database/sql"
	"time"
)

// RetireAfter is how long a file must stay missing before it can be retired:
// a successful scan at least this long after the one that first missed it
// must still not find it (spec §3.6). Failed scans don't count, so an outage
// suspends retirement.
const RetireAfter = 24 * time.Hour

// Candidate is a missing file that may now be retired.
type Candidate struct {
	FileID, Path string
	MissingSince time.Time
}

const candidateCond = `f.state = 'missing' AND EXISTS (SELECT 1 FROM library_scan_runs r
	WHERE r.root_id = f.root_id AND r.status = 'completed' AND r.started_at >= f.missing_since + ?)`

// RetirementCandidates lists the root's files that meet the retirement rule,
// for the admin preview. Nothing is retired automatically.
// Planned: LIB-2c (the admin retirement preview).
func (s *Store) RetirementCandidates(ctx context.Context, rootID string) ([]Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.path, f.missing_since FROM library_files f
		WHERE f.root_id = ? AND `+candidateCond+` ORDER BY f.path`, rootID, RetireAfter.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var since int64
		if err := rows.Scan(&c.FileID, &c.Path, &since); err != nil {
			return nil, err
		}
		c.MissingSince = time.UnixMilli(since)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Retire retires the given files of the root that are still candidates and
// returns how many it retired. Their items and versions stay, with any user
// data; a retired file that comes back is present again on the next scan.
// Planned: LIB-2c (the admin confirms the preview).
func (s *Store) Retire(ctx context.Context, rootID string, fileIDs []string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, id := range fileIDs {
		var res sql.Result
		if res, err = tx.ExecContext(ctx, `UPDATE library_files AS f SET state = 'retired'
			WHERE f.id = ? AND f.root_id = ? AND `+candidateCond, id, rootID, RetireAfter.Milliseconds()); err != nil {
			return 0, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, tx.Commit()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
gofmt -l server   # prints nothing
go -C server test -race -count=1 ./internal/library/...
```

Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/retire.go server/internal/library/retire_test.go
git commit -m "feat(library): retirement preview and explicit retire (LIB-2a)"
```

---

### Task 6: Full check and PR

- [ ] **Step 1: Run the full local check**

```bash
make check
```

Expected:
- PASS.
- `internal/library` and `internal/library/naming` are `ok` under `-race`, with every other package unchanged.

- [ ] **Step 2: Push and open the PR**

```bash
git push -u origin feat/lib-2a-catalogue
gh pr create --title "feat(library): catalogue store and offline-safe scans (LIB-2a)" --body-file <filled PR template>
```

- **Body:**
  - Source: LIB-2a, SCAN-01, SCALE-01 (module part), spec §3.1/§3.6.
  - `Refs #77` (LIB-2 stays open for 2b/2c) and `Closes #62`.
  - Fill in the local and Actions results separately.
- **Reviewers:** request lisim and ondona as two separate API calls.

## Self-review

**Spec coverage:**

| Requirement | Where |
|---|---|
| §3.6: an offline root must not remove its catalogue entries | Task 3: unreachable, empty and partial walks, each a failed run with nothing missing |
| §3.6: missing only after successful enumeration of an online root | Task 3 (missing-marking only on `completed`) |
| §3.6: retirement after two successful scans at least 24 h apart, with an admin preview; outages suspend it | Task 5 (candidates plus explicit `Retire`); the preview UI is 2c |
| §3.6: resumable, idempotent scans; cancel keeps completed work | Task 3 (batches committed as they go, cancelled runs, `MarkInterrupted`) |
| §3.6: a size/mtime change invalidates the probe; stale results can't overwrite newer data | Task 4 (stale-revision selection, compare-and-set) |
| §3.6: growing files retried after a quiet period | Task 4 (not-before time, #62) |
| §3.6: broken files listed, never abort a scan; admin retry | Task 4 (`probe_error`, `RetryProbe`) |
| §3.6: moves keep identity where evidence exists; ambiguous cases go to review; no guessing from size | Task 1 keys and folders; Task 3 rename folding, coexisting-folder flags and admin decisions |
| §3.1: Sonarr/Radarr naming, ID hints, versions | Task 1 |
| §10.1: separate item, version and file identities; files keep a revision and probe | Tasks 2–3 |
| §10.2: short transactions | Task 3 (batches of 500) |
| SCALE-01 (module): repeat scans skip unchanged revisions; counters recorded | Tasks 3–4 |

**Verified while writing this plan:**
- **Prototype:** all of this ran on LIB-1's plan code. `make check` passed, and the library tests passed 5 times in a row under `-race`.
- **Rebuild:** each task was rebuilt in order on that base. Each failed with the Step 2 messages before its implementation and passed after. The result is identical to the prototype.
- **Mutation check:** each of these 17 made exactly the matching test fail:
  - an empty root treated as fine
  - a partial walk treated as complete
  - a cancelled scan marking files missing
  - files seen in the current run marked missing
  - no compare-and-set on probe results
  - the quiet period ignored
  - failed probes retried every time
  - failed scans counting toward retirement
  - no 24 h gap before retirement
  - overlapping folders allowed
  - unplaced files never tried again
  - coexisting folders merged automatically
  - renames never folded
  - the folder mapping ignored
  - a merge forgetting the folder mapping
  - a provisional key that looks like the existing item's child
  - a deferred outcome moving a revision backwards
- **Placeholders:** none.
- **Names and types** match between tasks: Task 3 uses Task 2's `newStore`, and Tasks 4–5 share `setupMovies`.
- **Found while writing:** strengthening the merge test (a new episode arrives in a merged folder) exposed a real bug. `fold` deleted the merged item before moving its folder mappings, which the cascade then dropped. Now fixed and covered.
