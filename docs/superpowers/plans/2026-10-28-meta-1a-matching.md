# META-1a Metadata Store and Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Match catalogue movies and shows to a provider's records with spec §3.7's precedence, and keep their metadata with provenance and locks per field. A user's edits and fix-match choices always win, low-confidence results go to a review queue instead of being guessed, and provider outages never touch existing metadata or playback.

**Architecture:** A new `metadata` package (spec §2.2) on OPS-2's storage, reading LIB-2a's catalogue and never writing it:
- **Store:** CTR-2's `ItemMetadata` per item, a provenance and lock per field, provider IDs, a review queue, and a pause flag per provider. Admin edits and fix-match are compare-and-set on the item's revision.
- **Matcher:** for one item, a locked ID (the admin's choice) → validated ID hints from file names (LIB-2a) and Kodi NFO files → a confident search result → the review queue. Provider calls happen outside transactions, and each result is applied in one short compare-and-set transaction.
- **Bounded jobs:** `Enqueue` and `RunDue` take a limit and use one transaction per item. Failures back off, and an auth error pauses the provider.

The provider here is an interface with a fake. **TMDB is META-1b**, and the HTTP API and serve wiring are **META-1c** (Decision 1).

**Tech Stack:** Go 1.27.3 standard library (`encoding/xml`, `database/sql`), `modernc.org/sqlite` through OPS-2's `storage`, LIB-2a's `library` package in tests. No new dependencies (Decision 6).

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §3.2: providers, NFO, fix match, "manual edits and locked fields survive rescans".
- §3.7: precedence, conflicts, provenance, outages, rate limits, pausing.
- §0.3: local playback works without metadata services; work is bounded.
- §9.4: the META-1 row.
- §13: SCALE-03, PERF-05.
- §14: D15 (TMDB, each install's own key) and D06 (AniDB, META-2).

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **META-1** (basic matching, P0 / G1), slice **1a** of 3. 1a: store, precedence, NFO and name hints, scoring, review queue, edits, fix-match, bounded jobs, all against a fake provider. **1b:** the TMDB provider (D15) and the NFO field provider. **1c:** the HTTP API, serve wiring, `ItemDetail`/`ItemSummary` fields, artwork and actual-scale SCALE-03 (Decision 1) |
| Issue | #73 |
| Owner / reviewers | Lammy (claimant) / Pete and Ondo |
| Acceptance | **§3.7 precedence** (locks → validated IDs/NFO → deterministic match → review queue), **conflicts instead of overwriting locks**, **provenance per field** (provider, provider ID, fetch time, lock), **locked fields survive refresh**, **outages keep metadata**, **bounded retries and pause-on-auth-error**, **SCALE-03 (module part):** bounded jobs, per-item failures reported, edits compare-and-set. **"Offline playback unaffected":** matching never writes the catalogue. Actual-scale SCALE-03/PERF-05 is 1c |
| File scope | **New only:** `server/internal/metadata/{score,nfo,store,provider,apply,matcher}.go`, their tests and `env_test.go`, `server/internal/metadata/migrations/20261028140000_metadata_store.sql`. No changes to existing code, `go.mod`, contracts or CI |
| Consumes | LIB-2a (#111): the tables `library_items`, `library_id_hints`, `library_versions`, `library_files`, `library_folders`, `library_roots`; in tests `library.NewStore`, `CreateLibrary`, `Scanner.ScanRoot`, `Migrations`. OPS-2: `storage.DB`, `Source`, `Core`, `Migrate`. CTR-2 (#87): `contracts.ItemMetadata`, `MetadataFields`, `Field[T]`, `Provenance`, `MetadataExternalID`, `ContentRating`, `Match*` |
| Produces | `metadata.Migrations`, `Store` (`NewStore`, `Enqueue`, `Get`, `Edit`, `FixMatch`, `Pause`, `Resume`, `Paused`), `Edit`, the `Field*` constants, `SourceUser`, `ErrItemNotFound`, `ErrRevisionConflict`, `ErrInvalid`, `ErrPaused`. `Provider`, `Query`, `Candidate`, `Record`, `Rating`, `ErrNotFound`, `ErrUnavailable`, `ErrAuth`. `Normalize`, `Score`, `Rank`, `Scored`, `AutoScore`, `AutoMargin`. `Matcher` (`MatchItem`, `RunDue`), `Batch`, `ItemError`, the `Outcome*` constants |
| Start dependency | **LIB-2a's code merged (#111):** every table read here is created there. Tasks 1–2 are pure and can start any time |
| Standalone test | `go -C server test -race -count=1 ./internal/metadata/`, then `make check` |
| Module proof | A real catalogue (LIB-2a scans a temporary media tree), a real database, and a fake provider. Mutation-checked (see Self-review) |
| Integration proof (not in this slice) | 1b proves the TMDB provider against recorded-shape fixtures; 1c runs matching from serve after LIB-2b scans and serves the results, then the actual-scale SCALE-03 run. Record "module accepted" on #73 when 1a merges |

## Global Constraints

- Go 1.27.3. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`. Run `make check` before pushing.
- **The catalogue is read-only here.** `metadata` reads LIB-2a's tables but never writes them; library browsing and playback don't depend on it (§0.3).
- **Source media is never written.** NFO files are read through `os.Root` (the media root, then the library root), at most 1 MiB each.
- **Untrusted input (§6.6):** IDs from names and NFO files must match a pattern per namespace; NFO XML is parsed without entity expansion.
- **Short transactions (§10.2):** no transaction spans a provider call; each item is applied in its own transaction.
- **Bounded work (§0.3):** `Enqueue` and `RunDue` take a limit; retries back off from 1 minute, doubling, capped at 24 hours.
- Migrations: one file, module `metadata`, `STRICT` tables with explicit `ON DELETE`, timestamp after LIB-2a's and LIB-2b's.
- Code without a production caller carries `// Planned: <Feature ID> (<caller>).` (#60).
- Workflow: branch `feat/meta-1a-matching`, PR title `feat(metadata): metadata store and matching with locks and provenance (META-1a)`, labels `area:metadata`, reviewers lisim and ondona.

## Decisions taken in this plan (please review)

1. **META-1 is split into three slices**, like LIB-2, so each review stays small. The archived draft is 9,000 lines.
   - **1a (this plan):** the store, precedence, hints, scoring, review queue, edits, fix-match and bounded jobs, all against a fake provider.
   - **1b:** the providers.
     - The TMDB client (D15): each install's own credential, rate limiting, timeouts, bounded retries, a cache of at most 180 days that's purged when the key is removed, and the attribution flag.
     - The NFO **field** provider, so a library without a TMDB key still gets local titles and plots.
     - Seasons and episodes from a show's record.
     - `communityRating`, `directors` and `studios` (Pete's UI-2 plan, #114).
   - **1c:** the HTTP API (`getItemMetadata`, edit, fix-match and review-queue operations, added to the contract through CTR-2's process), the job loop in serve (queue items after each LIB-2b scan, run due items in bounded batches), and serving the results through LIB-2c's item shapes. Also artwork, and the actual-scale SCALE-03/PERF-05 run.
   - Each later slice gets its own plan PR.
2. **The `metadata` package reads LIB-2a's tables directly**: items, ID hints, versions/files for NFO paths, folders and roots. It never writes them, and a test pins that. Its own tables reference `library_items` with `ON DELETE CASCADE`, so retiring or deleting an item removes its metadata.
   - **Folding duplicates:** when LIB-2a folds a rename's provisional item into the existing item, the provisional item's metadata goes with it, and the kept item's metadata (and locks) stay.
   - **An admin merge loses the removed item's metadata (#121).** LIB-2a's `Decide(merge)` deletes the merged-away item (`moveInto`), and the cascade takes its edits, locks and fix-match with it. Nothing in 1a can run before that delete, so **1c carries them across**: its merge endpoint copies the removed item's locked fields and locked IDs onto the kept one (where the kept one has none), inside the same transaction as LIB-2a's fold, before the delete.
3. **One row per item META-1 has queued**, and a field row only when a field has a value or a lock.
   - An item without rows reads as revision 1, `unmatched`, with LIB-2a's parsed title, sort title and year and no provenance (`source: null`).
   - Displayed values are the metadata value when present, otherwise the parsed one. LIB-2c applies that when it serves items (1c).
   - Seasons and episodes aren't queued in 1a. Their titles come with the show's record in 1b.
4. **§3.7's precedence, exactly:**
   1. **A locked ID** in one of the provider's namespaces (the admin's fix-match) is always looked up and applied. Hints are ignored.
   2. **ID hints** come from the folder or file name (LIB-2a's `{tmdb-…}`, `[imdbid-…]`) and from NFO files.
      - Hints aren't stored as provenance; the record they lead to is, under its provider's namespace. CTR-2's `Provenance` lists provider namespaces, `nfo` and `user`; 1a only writes `user` and the provider's name, and 1b's NFO field provider adds `nfo`.
      - Two different values for a namespace the provider uses make the item a **conflict** for the admin; nothing is looked up.
      - A single hint is validated by looking it up. If the provider doesn't know it, or it's another kind (a show's ID on a movie), matching **falls back to searching**.
      - **Hints in different namespaces must agree (#121).** The looked-up record's IDs are compared with the other hints: a `{tmdb-…}` folder for one film and an NFO IMDb ID for another make a conflict, and nothing is applied.
   3. **A confident search result** is applied (Decision 5).
   4. Otherwise the **review queue**: the five best candidates are stored for the admin.
   - **Conflicts instead of overwriting a lock:** applying a record that reports an ID different from a locked one (e.g. the admin locked IMDb `tt…1`, TMDB says `tt…2`) changes nothing except the status, which becomes `conflict`.
5. **Scoring and auto-match thresholds:**
   - **Title:** compared after `Normalize`: lower case, Latin accents folded, `&` → "and", punctuation dropped, a leading "the/a/an" dropped. An exact match scores 1; otherwise word overlap × 0.8, so a partial title never auto-matches. The best of the candidate's titles counts (original and alternative titles).
   - **Year:** equal +0; ±1 −0.1 (release dates differ by country); further apart −0.5; unknown on either side −0.05. A different kind (movie vs show) scores 0.
   - **Auto-match** needs a best score ≥ **0.85** and a lead of ≥ **0.10** over the next candidate. A remake with no year ("Dune": 1984 and 2021) goes to review, but with the year it matches.
6. **Only Latin is folded, with no Unicode dependency.**
   - **Precomposed letters:** a built-in table for Latin-1 and Latin Extended-A (`é` → `e`, `ß` → `ss`, `æ` → `ae` …).
   - **Decomposed marks:** a combining mark is dropped only right after a Latin letter (a decomposed `é` in a macOS file name).
   - **Other scripts:** after any other letter, marks are part of the word and are kept: the Devanagari virama, Thai vowel and tone marks, Hebrew points, the Japanese voiced mark. So Hindi, Thai, Hebrew and Japanese titles compare as written (#121).
   - **No dependency:** `golang.org/x/text` would cover more, but it's a new dependency for a small gain; non-Latin titles match through ID hints and alternative titles.
   - **UI-2's sort is a different rule on purpose.** UI-2's contract (#114) sorts titles by NFKD with every combining mark dropped. That's fine for ordering, where a dropped mark only nudges a title's position; matching deliberately folds Latin only, because dropping marks in other scripts would make different titles compare equal.
7. **Revisions and compare-and-set.**
   - Every change bumps the item's revision: an edit, a fix-match, and every matching result (matched, review, conflict).
   - Admin edits and fix-match take `expectedRevision`. A mismatch is `ErrRevisionConflict`; 1c maps it to CTR-2's `revision_conflict` (409).
   - A matching attempt reads the revision **before** calling the provider, and applies only if it hasn't changed. An edit made while the provider was being asked therefore wins: the result is dropped and the item is retried a minute later.
8. **Edits.**
   - **Locking:** a manual value has provenance `user` and is **locked by default**. Lock-only edits lock or unlock the current value, including a parsed one. An unlocked value can be replaced by the next match.
   - **Limits:**
     - title, sort title and original title: 1–512 bytes. Text is checked for emptiness after trimming spaces (a title of "  " is refused) and stored as given. Title can't be cleared;
     - overview: up to 10,000 bytes;
     - year: 1870–2200;
     - content rating: an upper-case ISO 3166-1 region and a value of 1–16 bytes;
     - genres: up to 20, each 1–64 bytes.

     Other fields can be cleared with `null`.
9. **Fix-match** stores the chosen ID as a **locked `user` ID**, clears the review candidates, sets the status to `unmatched` and queues the item **now**.
   - **It replaces the previous match (#121):** it deletes the item's **other** provider IDs, locked or not, and its **unlocked** fields. So a second fix-match in another namespace (IMDb first, then TMDB) applies cleanly instead of hitting a permanent conflict with the first one's locked IMDb ID, and the wrong film's overview and genres don't linger. The admin's locked fields stay. The matcher then applies it under the normal rules, outside the request transaction; 1c's endpoint can run `MatchItem` straight after. A locked ID keeps its `user` provenance after the match.
10. **Bounded jobs and failure handling (§3.7, SCALE-03).**
    - **Queueing:** `Enqueue(limit)` queues movies and shows without metadata, oldest first. `RunDue(limit)` takes due items oldest first, each in its own transaction. A database error on one item is reported in `Batch.Failed`, and the batch goes on.
    - **Edited items stay queued (#121).** An edit or fix-match on an item that has no metadata row yet creates the row **queued** (`next_attempt_at = now`). `Enqueue` skips items that have a row, so without this an item the admin touched before its batch came round would never be matched.
    - **Provider failures:** a timeout, 5xx or rate limit (`ErrUnavailable`) keeps all existing metadata and retries after 1, 2, 4 … minutes, capped at 24 hours, with the reason stored (bounded to 500 bytes).
      - **Overflow (#121):** the shift is clamped before shifting (`retryBase << min(attempt−1, 11)`; 2¹¹ minutes is already past the cap), so a long outage can't overflow to a negative or zero delay and retry on every batch.
      - **Jitter:** each delay gets up to **10%** random jitter, so items that failed together don't retry in the same instant (§3.7, "bounded retries/jitter"). 1b's TMDB client adds its own per-request retries with jitter on top.
    - **Auth errors:** `ErrAuth` (a revoked or wrong key) **pauses the provider**. The batch stops with `ErrPaused`, the item stays queued unchanged, and no further calls are made until `Resume`. 1b's settings screen resumes it when the key changes.
11. **NFO files.**
    - **Format:** Kodi's `<uniqueid type="…">` (v18 and later), the older `<tmdbid>`, `<imdbid>`, `<tvdbid>` and `<id>` (IMDb), and an NFO that's just a provider URL. Sources: <https://kodi.wiki/view/NFO_files/Movies>, <https://kodi.wiki/view/NFO_files/TV_shows>.
    - **Where they're looked for:** beside each video file (`<name>.nfo`) and `movie.nfo` in its folder for a movie; `tvshow.nfo` in each show folder.
    - **Limits:** at most 1 MiB each. Only IDs matching `tmdb`/`tvdb`/`anidb` (digits) or `imdb` (`tt` + 7–10 digits) are kept. Two NFOs disagreeing is a conflict like Decision 4.2. An unreadable NFO is skipped; names and search still work.
    - 1a reads IDs only; NFO titles and plots are 1b's field provider.
12. **`matched` for UI-2.** Pete's UI-2 plan (#114) has `ItemSummary.matched` produced by META-1, and I asked there for it to be nullable. 1c serves:
    - `null` when the item has no metadata row (matching hasn't run);
    - `true` for `matched`;
    - `false` for `unmatched`, `review` and `conflict` (the Unmatched badge).

    `communityRating`, `directors` and `studios` come from TMDB records (1b) and are served in 1c.
13. **Not in this slice, with owners:** the TMDB provider, the NFO field provider, episodes and the #114 fields (1b); the HTTP API, serve wiring, artwork, the About/Credits attribution data and actual scale (1c); anime ordering and mapping, AniList and AniDB (META-2, D06); TVDB, Fanart.tv and ratings providers (META-3); bulk edits (1c, as a job over `Edit`).

## Review Focus

1. **The admin edits a title while a match is in flight:** the edit must win, not the provider's slower answer → `TestEditDuringMatchWins`.
2. **Two films with the same title and no year (remakes):** never guessed → `TestRankAutoMatchesOnlyWhenConfident`, `TestSearchMatchesOnlyWhenConfident`.
3. **A wrong or stale ID in a folder name** (a typo, or a show's ID on a movie): falls back to search instead of applying the wrong record → `TestInvalidHintFallsBackToSearch`, `TestHintOfAnotherKindFallsBack`.
4. **A revoked TMDB key:** one failed call pauses the provider, rather than one failed call per item for the whole library → `TestAuthErrorPausesProvider`.
5. **Hostile or broken NFO files** (huge, entity tricks, garbage, IDs in the wrong format): bounded and ignored → `TestParseNFO`, `TestReadNFOs`.

Also pinned after Pete's review (#121):
- an item failing for months stays at the 24 h cap → `TestBackoffStaysCappedAfterManyFailures`;
- an item edited before its first match still gets matched → `TestEditBeforeFirstMatchStillMatches`;
- a second fix-match in another namespace replaces the first → `TestSecondFixMatchReplacesTheFirst`;
- hints that disagree across namespaces are a conflict → `TestHintsDisagreeingAcrossNamespaces`;
- non-Latin titles keep their marks → `TestNormalize`.

---

### Task 1: Title normalisation and scoring

**Files:**
- Create: `server/internal/metadata/score.go`
- Test: `server/internal/metadata/score_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func Normalize(title string) string`; `type Query struct{ Kind, Title string; Year int }`; `type Candidate struct{ Provider, ID, Kind, Title string; AltTitles []string; Year int }`; `func Score(q Query, c Candidate) float64`; `type Scored struct{ Candidate; Score float64 }`; `func Rank(q Query, cands []Candidate) ([]Scored, bool)`; `const AutoScore = 0.85`, `AutoMargin = 0.10`, `reviewKeep = 5`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/metadata/score_test.go`:**

```go
package metadata

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"The Lord of the Rings: The Return of the King": "lord of the rings the return of the king",
		"Lord of the Rings - The Return Of The King":    "lord of the rings the return of the king",
		"Amélie":                 "amelie",
		"Léon: The Professional": "leon the professional",
		"Fast & Furious":         "fast and furious",
		"A Quiet Place":          "quiet place",
		"The":                    "the",
		"WALL·E":                 "wall e",
		"Straße":                 "strasse",
		"進撃の巨人":                  "進撃の巨人",  // other scripts compare as written
		"Amélie":                "amelie", // a decomposed Latin accent (macOS NFD names)
		// #121 review: marks in other scripts are part of the word, not
		// accents to drop: Devanagari virama U+094D and vowel sign U+093F,
		// Thai U+0E34 and U+0E4C, Hebrew U+05B8, Japanese voiced mark U+3099.
		"हिन्दी":     "हिन्दी",
		"ภาพยนตร์":   "ภาพยนตร์",
		"שָׁלוֹם":    "שָׁלוֹם",
		"ガ":         "ガ", // ガ as written by macOS (NFD)
		"かぐや様は告らせたい": "かぐや様は告らせたい",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScore(t *testing.T) {
	q := Query{Kind: "movie", Title: "Heat", Year: 1995}
	for _, c := range []struct {
		name string
		c    Candidate
		want float64
	}{
		{"exact", Candidate{Kind: "movie", Title: "Heat", Year: 1995}, 1},
		{"one year off", Candidate{Kind: "movie", Title: "Heat", Year: 1996}, 0.9},
		{"remake", Candidate{Kind: "movie", Title: "Heat", Year: 1986}, 0.5},
		{"unknown year", Candidate{Kind: "movie", Title: "Heat"}, 0.95},
		{"alternate title", Candidate{Kind: "movie", Title: "Hitze", AltTitles: []string{"Heat"}, Year: 1995}, 1},
		{"other kind", Candidate{Kind: "show", Title: "Heat", Year: 1995}, 0},
		{"partial", Candidate{Kind: "movie", Title: "Heat Wave", Year: 1995}, 0.4},
	} {
		if got := Score(q, c.c); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s: Score = %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

// Low-confidence results never auto-match: a weak best, or two candidates
// too close to call (a remake with no year), go to review.
func TestRankAutoMatchesOnlyWhenConfident(t *testing.T) {
	heat := Candidate{ID: "949", Kind: "movie", Title: "Heat", Year: 1995}
	for _, c := range []struct {
		name  string
		q     Query
		cands []Candidate
		auto  bool
		best  string
	}{
		{"clear winner", Query{"movie", "Heat", 1995}, []Candidate{
			{ID: "1", Kind: "movie", Title: "Heat Wave", Year: 1995}, heat}, true, "949"},
		{"remakes without a year", Query{"movie", "Dune", 0}, []Candidate{
			{ID: "438631", Kind: "movie", Title: "Dune", Year: 2021}, {ID: "841", Kind: "movie", Title: "Dune", Year: 1984}}, false, "438631"},
		{"remakes, year decides", Query{"movie", "Dune", 1984}, []Candidate{
			{ID: "438631", Kind: "movie", Title: "Dune", Year: 2021}, {ID: "841", Kind: "movie", Title: "Dune", Year: 1984}}, true, "841"},
		{"only a partial match", Query{"movie", "Heat", 1995}, []Candidate{{ID: "1", Kind: "movie", Title: "Heat Wave", Year: 1995}}, false, "1"},
		{"nothing found", Query{"movie", "Heat", 1995}, nil, false, ""},
	} {
		ranked, auto := Rank(c.q, c.cands)
		best := ""
		if len(ranked) > 0 {
			best = ranked[0].ID
		}
		if auto != c.auto || best != c.best {
			t.Errorf("%s: auto=%v best=%q, want auto=%v best=%q", c.name, auto, best, c.auto, c.best)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/metadata/
```

Expected: FAIL to build: `undefined: Normalize`, `undefined: Query`, `undefined: Candidate`.

- [ ] **Step 3: Implement `score.go`**

- **The package comment** goes on `store.go` (Task 3); this file has none.
- **`Normalize`:**
  1. Lower-case the title.
  2. Map each rune through the fold table (Decision 6).
  3. A combining mark (`unicode.M`) is dropped only if the last letter written was Latin; otherwise it's kept as part of the word. Write `&` as " and ", keep letters and digits, and turn everything else into a space.
  4. Collapse the spaces, and drop a leading "the", "a" or "an" when more words follow.
- **`Score`:** Decision 5. The title part is the maximum over `c.Title` and `c.AltTitles` of `similarity(Normalize(q.Title), Normalize(t))`: 1 when equal, otherwise 0.8 × Jaccard overlap of their words. The result is floored at 0.
- **`Rank`:** score every candidate and sort best first, stably (ties keep the provider's order). Report `auto` only per Decision 5.

- [ ] **Step 4: Run it to verify it passes, then commit**

```bash
go -C server test -race -count=1 ./internal/metadata/
git add server/internal/metadata/score.go server/internal/metadata/score_test.go
git commit -m "feat(metadata): title normalisation and candidate scoring (META-1a)"
```

---

### Task 2: NFO ID hints

**Files:**
- Create: `server/internal/metadata/nfo.go`
- Test: `server/internal/metadata/nfo_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `const maxNFOBytes = 1 << 20`; `var idPatterns map[string]*regexp.Regexp` (`tmdb`, `tvdb`, `anidb`: `^[1-9][0-9]{0,9}$`; `imdb`: `^tt[0-9]{7,10}$`); `func parseNFO(b []byte) map[string]string`; `func readNFOs(root *os.Root, paths []string) (ids map[string]string, conflicts []string, err error)`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/metadata/nfo_test.go`:**

```go
package metadata

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNFO(t *testing.T) {
	for _, c := range []struct {
		name, doc string
		want      map[string]string
	}{
		{"uniqueid (Kodi v18+)", `<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>
<movie><title>The Matrix</title><uniqueid type="imdb" default="true">tt0133093</uniqueid><uniqueid type="tmdb">603</uniqueid></movie>`,
			map[string]string{"imdb": "tt0133093", "tmdb": "603"}},
		{"older elements", `<movie><id>tt0113277</id><tmdbid>949</tmdbid></movie>`,
			map[string]string{"imdb": "tt0113277", "tmdb": "949"}},
		{"tv show", `<tvshow><title>Show</title><uniqueid type="tvdb" default="true">81189</uniqueid></tvshow>`,
			map[string]string{"tvdb": "81189"}},
		{"just a URL", "https://www.imdb.com/title/tt0133093/\n", map[string]string{"imdb": "tt0133093"}},
		{"TMDB URL", "https://www.themoviedb.org/movie/603-the-matrix", map[string]string{"tmdb": "603"}},
		{"invalid IDs ignored", `<movie><uniqueid type="tmdb">abc</uniqueid><uniqueid type="imdb">0133093</uniqueid><uniqueid type="evil">1</uniqueid></movie>`,
			map[string]string{}},
		{"not an NFO", "garbage \x00\x01", map[string]string{}},
		{"entities aren't expanded", `<!DOCTYPE m [<!ENTITY x "603">]><movie><tmdbid>&x;</tmdbid></movie>`, map[string]string{}},
	} {
		if got := parseNFO([]byte(c.doc)); !maps.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadNFOs(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("Heat (1995)/Heat (1995).nfo", `<movie><uniqueid type="tmdb">949</uniqueid></movie>`)
	write("Heat (1995)/movie.nfo", `<movie><uniqueid type="tmdb">950</uniqueid><uniqueid type="imdb">tt0113277</uniqueid></movie>`)
	write("Big/movie.nfo", `<movie><tmdbid>1</tmdbid>`+strings.Repeat(" ", maxNFOBytes)+`<imdbid>tt0000001</imdbid></movie>`)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ids, conflicts, err := readNFOs(root, []string{"Heat (1995)/Heat (1995).nfo", "Heat (1995)/movie.nfo", "Heat (1995)/missing.nfo"})
	if err != nil || ids["tmdb"] != "949" || ids["imdb"] != "tt0113277" || len(conflicts) != 1 || conflicts[0] != "tmdb" {
		t.Fatalf("ids %v conflicts %v err %v", ids, conflicts, err)
	}
	// Only the first MiB is read: a truncated file gives no XML IDs.
	if ids, _, err := readNFOs(root, []string{"Big/movie.nfo"}); err != nil || len(ids) != 0 {
		t.Fatalf("oversized NFO: %v %v", ids, err)
	}
	// Paths can't leave the root.
	if _, _, err := readNFOs(root, []string{"../outside.nfo"}); err == nil {
		t.Fatal("read outside the root")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/metadata/
```

Expected: FAIL to build: `undefined: parseNFO`, `undefined: maxNFOBytes`, `undefined: readNFOs`.

- [ ] **Step 3: Implement `nfo.go`**

- **`parseNFO`:**
  - If the trimmed bytes start with `<`, decode `nfoDoc` with `xml.Decoder` (`Strict = false`; Go's decoder never expands custom entities). Add each `uniqueid` by its lower-cased `type`, then `tmdbid`, `imdbid`, `tvdbid` and `id` (as imdb).
  - If that gave nothing, look for an IMDb, TMDB (`/movie/` or `/tv/`) or TVDB URL.
  - Keep a value only if it matches its namespace's pattern; the first value for a namespace wins.
- **`readNFOs`:** open each path through `root` (a missing file is skipped; any other error is returned), read through `io.LimitReader(maxNFOBytes)`, and merge. A namespace given different values by two files is listed in `conflicts`, keeping the first value.

- [ ] **Step 4: Run it to verify it passes, then commit**

```bash
go -C server test -race -count=1 ./internal/metadata/
git add server/internal/metadata/nfo.go server/internal/metadata/nfo_test.go
git commit -m "feat(metadata): read provider IDs from Kodi NFO files (META-1a)"
```

---

### Task 3: Metadata store, edits and fix-match

**Files:**
- Create: `server/internal/metadata/migrations/20261028140000_metadata_store.sql`, `server/internal/metadata/store.go`
- Test: `server/internal/metadata/env_test.go`, `server/internal/metadata/store_test.go`

**Interfaces:**
- Consumes: LIB-2a's tables and, in tests, `library.NewStore`, `CreateLibrary`, `Scanner{Store, Media}.ScanRoot`, `library.Migrations`; CTR-2's `contracts.ItemMetadata` and friends.
- Produces:
  - `func Migrations() storage.Source` (module `metadata`); `type Store`; `func NewStore(db *storage.DB) *Store` (unexported `now func() time.Time` and `jitter func(time.Duration) time.Duration`, both replaceable in tests); `func defaultJitter(d time.Duration) time.Duration` (adds 0–10%).
  - `func (s *Store) Enqueue(ctx, limit int) (int, error)`; `Get(ctx, itemID string) (contracts.ItemMetadata, error)`; `Edit(ctx, itemID string, expectedRevision int64, edits []Edit) (contracts.ItemMetadata, error)`; `FixMatch(ctx, itemID string, expectedRevision int64, namespace, id string) (contracts.ItemMetadata, error)`; `Pause(ctx, provider, reason string) error`; `Resume(ctx, provider string) error`; `Paused(ctx, provider string) (bool, string, error)`.
  - `type Edit struct{ Field string; Value json.RawMessage; Lock *bool }`.
  - `FieldTitle`, `FieldSortTitle`, `FieldOriginalTitle`, `FieldOverview`, `FieldYear`, `FieldContentRating`, `FieldGenres` (CTR-2's JSON keys). `SourceUser = "user"` (Decision 4.2: no `filename`/`nfo` constants in 1a).
  - `ErrItemNotFound`, `ErrRevisionConflict`, `ErrInvalid`, `ErrPaused`.
  - Unexported, used by Task 4: `bump(ctx, tx, itemID string, expected, now int64) error`, `get(ctx, querier, itemID)`, `ms(time.Time) int64`, `bounded(string) string` (≤ 500 bytes), `nsRe`.
  - Test helpers: `newEnv(t) *env` with `write`, `library`, `item`, `get`, plus `raw(v) json.RawMessage` and `ptr[T](v) *T`.

- [ ] **Step 1: Write the failing tests**

**Create `server/internal/metadata/env_test.go`:**

```go
package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// env is a real catalogue (LIB-2a) over a temporary media tree, with the
// metadata store and a settable clock.
type env struct {
	t     *testing.T
	dir   string // the media root
	media *os.Root
	db    *storage.DB
	lib   *library.Store
	scan  *library.Scanner
	st    *Store
	clock time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx, []storage.Source{storage.Core(), library.Migrations(), Migrations()}, time.Now); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dir: t.TempDir(), db: db, clock: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	if e.media, err = os.OpenRoot(e.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.media.Close() })
	e.lib = library.NewStore(db)
	e.scan = &library.Scanner{Store: e.lib, Media: e.media}
	e.st = NewStore(db)
	e.st.now = func() time.Time { return e.clock }
	e.st.jitter = func(d time.Duration) time.Duration { return d } // exact delays in tests
	return e
}

// write creates a media file (or an NFO) under the media root.
func (e *env) write(rel, body string) {
	e.t.Helper()
	p := filepath.Join(e.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// library creates a library over folder and scans it.
func (e *env) library(typ, folder string) {
	e.t.Helper()
	ctx := context.Background()
	lib, err := e.lib.CreateLibrary(ctx, folder, typ, []string{folder})
	if err != nil {
		e.t.Fatal(err)
	}
	if run, err := e.scan.ScanRoot(ctx, lib.Roots[0].ID); err != nil || run.Status != library.RunCompleted {
		e.t.Fatalf("scan %+v %v", run, err)
	}
}

// item returns the ID of the movie or show with the given parsed title.
func (e *env) item(title string) string {
	e.t.Helper()
	var id string
	if err := e.db.QueryRow(`SELECT id FROM library_items WHERE title = ? AND kind IN ('movie', 'show')`, title).Scan(&id); err != nil {
		e.t.Fatalf("item %q: %v", title, err)
	}
	return id
}

func (e *env) get(id string) contracts.ItemMetadata {
	e.t.Helper()
	md, err := e.st.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return md
}
```

**Create `server/internal/metadata/store_test.go`:**

```go
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Before matching, an item's metadata is its parsed name: revision 1,
// unmatched, no provenance.
func TestGetBeforeMatching(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	md, err := e.st.Get(context.Background(), e.item("Heat"))
	if err != nil {
		t.Fatal(err)
	}
	f := md.Fields
	if md.Revision != 1 || md.MatchStatus != contracts.MatchUnmatched || *f.Title.Value != "Heat" || f.Title.Source != nil ||
		*f.Year.Value != 1995 || f.Overview.Value != nil || len(f.Genres.Value) != 0 || len(md.ExternalIDs) != 0 {
		t.Fatalf("metadata %+v", md)
	}
	if _, err := e.st.Get(context.Background(), "mov-nope"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func ptr[T any](v T) *T { return &v }

// A manual edit is "user" provenance and locked; edits are compare-and-set
// on the revision, so a stale form can't overwrite a newer change.
func TestEditLocksAndChecksRevision(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	id := e.item("Heat")

	md, err := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldTitle, Value: raw("Heat (Director's Edition)")}, {Field: FieldYear, Lock: ptr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	f := md.Fields
	if md.Revision != 2 || *f.Title.Value != "Heat (Director's Edition)" || !f.Title.Locked || f.Title.Source.Provider != SourceUser ||
		!f.Year.Locked || *f.Year.Value != 1995 || f.Year.Source != nil {
		t.Fatalf("after edit %+v", md)
	}
	if _, err := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldOverview, Value: raw("stale")}}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	// Unlocking keeps the value but lets the next match replace it.
	md, err = e.st.Edit(ctx, id, 2, []Edit{{Field: FieldTitle, Lock: ptr(false)}})
	if err != nil || md.Fields.Title.Locked || *md.Fields.Title.Value != "Heat (Director's Edition)" {
		t.Fatalf("unlock: %+v %v", md, err)
	}
	if _, err := e.st.Edit(ctx, "mov-nope", 1, []Edit{{Field: FieldTitle, Value: raw("x")}}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestEditValidates(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	for name, ed := range map[string]Edit{
		"empty title":     {Field: FieldTitle, Value: raw("  ")},
		"null title":      {Field: FieldTitle, Value: raw(nil)},
		"long title":      {Field: FieldTitle, Value: raw(string(make([]byte, 513)))},
		"year as text":    {Field: FieldYear, Value: raw("1995")},
		"year too early":  {Field: FieldYear, Value: raw(1066)},
		"bad region":      {Field: FieldContentRating, Value: raw(map[string]string{"region": "aus", "value": "M"})},
		"too many genres": {Field: FieldGenres, Value: raw(strings.Split("a b c d e f g h i j k l m n o p q r s t u", " "))},
		"empty genre":     {Field: FieldGenres, Value: raw([]string{"Drama", " "})},
		"unknown field":   {Field: "director", Value: raw("x")},
	} {
		if _, err := e.st.Edit(context.Background(), id, 1, []Edit{ed}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	// Clearing an optional field is fine.
	if _, err := e.st.Edit(context.Background(), id, 1, []Edit{{Field: FieldOverview, Value: raw(nil)}}); err != nil {
		t.Fatal(err)
	}
}

// Fix-match records the admin's choice as a locked "user" ID, clears the
// review candidates and queues the item now.
func TestFixMatchLocksTheChosenID(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Dune/Dune.mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Dune")
	// An item in review, with a candidate waiting.
	e.db.Exec(`INSERT INTO metadata_items(item_id, revision, match_status, updated_at) VALUES(?, 3, 'review', 0)`, id)
	e.db.Exec(`INSERT INTO metadata_candidates(item_id, provider, provider_id, title, year, score) VALUES(?, 'tmdb', '438631', 'Dune', 2021, 0.95)`, id)
	md, err := e.st.FixMatch(context.Background(), id, 3, "tmdb", "841")
	if err != nil {
		t.Fatal(err)
	}
	if md.Revision != 4 || md.MatchStatus != contracts.MatchUnmatched || len(md.ExternalIDs) != 1 || md.ExternalIDs[0].ID != "841" ||
		!md.ExternalIDs[0].Locked || md.ExternalIDs[0].Source.Provider != SourceUser {
		t.Fatalf("fix-match %+v", md)
	}
	var cands int
	var due *int64
	e.db.QueryRow(`SELECT COUNT(*) FROM metadata_candidates WHERE item_id = ?`, id).Scan(&cands)
	e.db.QueryRow(`SELECT next_attempt_at FROM metadata_items WHERE item_id = ?`, id).Scan(&due)
	if cands != 0 || due == nil || *due != e.clock.UnixMilli() {
		t.Fatalf("candidates %d, due %v: want none, queued now", cands, due)
	}
	if _, err := e.st.FixMatch(context.Background(), id, 1, "tmdb", "841"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale fix-match: %v", err)
	}
	if _, err := e.st.FixMatch(context.Background(), id, md.Revision, "TMDB!", "841"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad namespace: %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -race -count=1 ./internal/metadata/
```

Expected: FAIL to build: `undefined: Store`, `undefined: Migrations`, `undefined: NewStore`.

- [ ] **Step 3: Add the migration**

**Create `server/internal/metadata/migrations/20261028140000_metadata_store.sql`:**

```sql
-- Metadata (META-1): matched metadata per catalogue item, with provenance
-- and locks per field (CTR-2's ItemMetadata), provider IDs, the review
-- queue's candidates and each provider's pause state. Times are Unix
-- milliseconds (UTC). Rows exist only for items META-1 has queued; an item
-- without one shows its parsed name (LIB-2a) and is "not matched yet".

CREATE TABLE metadata_items (
    item_id         TEXT PRIMARY KEY REFERENCES library_items(id) ON DELETE CASCADE,
    revision        INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    match_status    TEXT NOT NULL DEFAULT 'unmatched'
                    CHECK (match_status IN ('unmatched', 'matched', 'review', 'conflict')),
    next_attempt_at INTEGER,          -- due for matching; NULL when not queued
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,             -- why the last attempt failed (bounded), for the admin
    updated_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX metadata_items_due ON metadata_items(next_attempt_at) WHERE next_attempt_at IS NOT NULL;

-- One row per field that has a value or a lock. value is JSON: a string,
-- an integer, {"region","value"} or a list of strings, as CTR-2's fields.
CREATE TABLE metadata_fields (
    item_id     TEXT NOT NULL REFERENCES metadata_items(item_id) ON DELETE CASCADE,
    field       TEXT NOT NULL CHECK (field IN ('title', 'sortTitle', 'originalTitle', 'overview',
                                               'year', 'contentRating', 'genres')),
    value       TEXT,                 -- JSON; NULL = no value
    provider    TEXT,                 -- provenance: tmdb, nfo, filename, user ...; NULL = unknown
    provider_id TEXT,
    fetched_at  INTEGER,
    locked      INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    PRIMARY KEY (item_id, field)
) STRICT;

-- The item's identity at each provider. A locked ID (a user's fix-match)
-- is never replaced by a match; a different hint raises a conflict instead
-- (spec §3.7).
CREATE TABLE metadata_external_ids (
    item_id     TEXT NOT NULL REFERENCES metadata_items(item_id) ON DELETE CASCADE,
    namespace   TEXT NOT NULL,
    value       TEXT NOT NULL,
    provider    TEXT,
    provider_id TEXT,
    fetched_at  INTEGER,
    locked      INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    PRIMARY KEY (item_id, namespace)
) STRICT;

-- The review queue: candidates for items no candidate matched confidently.
CREATE TABLE metadata_candidates (
    item_id     TEXT NOT NULL REFERENCES metadata_items(item_id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    title       TEXT NOT NULL,
    year        INTEGER,
    score       REAL NOT NULL,
    PRIMARY KEY (item_id, provider, provider_id)
) STRICT;

-- A provider paused by a configuration or authentication error (a bad or
-- revoked key) until the admin resumes it (spec §3.7).
CREATE TABLE metadata_providers (
    name          TEXT PRIMARY KEY,
    paused_reason TEXT,
    paused_at     INTEGER
) STRICT;
```

- [ ] **Step 4: Implement `store.go`**

- **Package comment:** "Package metadata matches catalogue items to provider records and keeps their descriptive metadata with provenance and locks per field (META-1, spec §3.2 and §3.7). Matching never blocks browsing or playback: an item without metadata shows its parsed name (LIB-2a)."
- **`Get`:**
  - Read the item's `title`, `sort_title` and `year` from `library_items` (`ErrItemNotFound` if absent), then the `metadata_items` row if any (default revision 1, `unmatched`).
  - Start from the parsed values with `source: nil`, and an empty genres list.
  - Overlay each `metadata_fields` row: JSON-decode `value` into the field's type when non-NULL, and set `Source` from `provider`/`provider_id`/`fetched_at` (nil when `provider` is NULL) and `Locked`.
  - Then `metadata_external_ids`, by namespace (an empty list, never null).
- **`Edit`:**
  1. Validate every edit first (Decision 8), with errors wrapping `ErrInvalid`.
  2. In one transaction, `bump` the revision: ensure the item exists (`ErrItemNotFound`), insert its row if missing **queued** (`next_attempt_at = now`, Decision 10), then `UPDATE … SET revision = revision + 1 WHERE item_id = ? AND revision = ?`. No row means `ErrRevisionConflict`.
  3. Upsert each field: a value with provenance `user` and `fetched_at = now`, locked unless `Lock` is false; or only `locked` for lock-only edits.
  4. Return `get` inside the transaction.
- **`FixMatch`:** check `namespace` against `^[a-z][a-z0-9]{1,31}$` and the ID length (1–64), then `bump`, then Decision 9's writes: upsert the locked `user` ID; delete the item's other external IDs and its unlocked fields; delete the candidates; set the status to `unmatched` and queue it now. Then `get`.
- **`Enqueue`:** a single `INSERT … SELECT` of movies and shows without a metadata row, `ORDER BY created_at, id LIMIT ?`, with `next_attempt_at = now`.
- **`Pause`/`Resume`/`Paused`:** upsert, delete and read a `metadata_providers` row.
- **`Planned` tags:** `Migrations`, `NewStore`, `Enqueue`, `Get`, `Edit` and `FixMatch` get `// Planned: META-1c (…)` naming their caller (serve, the scan listener and the API operations); `Resume` gets `// Planned: META-1b (the settings screen resumes TMDB when the key changes)`.

- [ ] **Step 5: Run the tests to verify they pass, then commit**

```bash
go -C server test -race -count=1 ./internal/metadata/
git add server/internal/metadata
git commit -m "feat(metadata): metadata store with provenance, locks, edits and fix-match (META-1a)"
```

---

### Task 4: Providers and the matcher

**Files:**
- Create: `server/internal/metadata/provider.go`, `server/internal/metadata/apply.go`, `server/internal/metadata/matcher.go`
- Test: `server/internal/metadata/matcher_test.go`

**Interfaces:**
- Consumes: Task 1's `Query`, `Candidate`, `Rank`, `reviewKeep`; Task 2's `readNFOs`; Task 3's `Store`, `bump`, `get`, `Pause`, `Paused`, `bounded`, `ms`, `newEnv`, `raw`, `ptr`.
- Produces:
  - `type Provider interface { Name() string; Search(ctx, Query) ([]Candidate, error); Lookup(ctx, kind, namespace, id string) (Record, error); Namespaces() []string }`.
  - `type Record struct{ ID, Kind string; Title, OriginalTitle, SortTitle, Overview *string; Year *int; ContentRating *Rating; Genres []string; ExternalIDs map[string]string }` (nil/zero = not provided, never a clear); `type Rating struct{ Region, Value string }`.
  - `ErrNotFound`, `ErrUnavailable`, `ErrAuth`.
  - `type Matcher struct{ Store *Store; Provider Provider; Media *os.Root }`; `func (m *Matcher) MatchItem(ctx, itemID string) (string, error)`; `func (m *Matcher) RunDue(ctx, limit int) (Batch, error)`.
  - `type Batch struct{ Matched, Review, Conflict, Retry int; Failed []ItemError }`; `type ItemError struct{ ItemID, Err string }`.
  - `OutcomeMatched`, `OutcomeReview`, `OutcomeConflict`, `OutcomeRetry`, `OutcomeSkipped`; `retryBase = time.Minute`, `retryCap = 24 * time.Hour`.
  - Test helpers: `newMatchEnv(t) *menv` (the env plus `fake *fakeProvider`, `m *Matcher`, `match`).

- [ ] **Step 1: Write the failing test**

**Create `server/internal/metadata/matcher_test.go`:**

```go
package metadata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// menv is env plus a matcher over a fake provider.
type menv struct {
	*env
	fake *fakeProvider
	m    *Matcher
}

func newMatchEnv(t *testing.T) *menv {
	e := newEnv(t)
	f := newFake()
	return &menv{env: e, fake: f, m: &Matcher{Store: e.st, Provider: f, Media: e.media}}
}

func (e *menv) match(itemID string) string {
	e.t.Helper()
	out, err := e.m.MatchItem(context.Background(), itemID)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// fakeProvider is an in-memory provider in the "tmdb" namespace that also
// resolves imdb IDs. during runs inside each call when set.
type fakeProvider struct {
	mu      sync.Mutex
	records map[string]Record // by "ns:id"
	results map[string][]Candidate
	err     error
	calls   []string
	during  func()
}

func newFake() *fakeProvider {
	str := func(s string) *string { return &s }
	yr := func(y int) *int { return &y }
	f := &fakeProvider{records: map[string]Record{}, results: map[string][]Candidate{}}
	f.add(Record{ID: "949", Kind: "movie", Title: str("Heat"), OriginalTitle: str("Heat"), Overview: str("A group of high-end professional thieves."),
		Year: yr(1995), ContentRating: &Rating{"AU", "MA15+"}, Genres: []string{"Crime", "Drama"}, ExternalIDs: map[string]string{"imdb": "tt0113277"}})
	f.add(Record{ID: "603", Kind: "movie", Title: str("The Matrix"), Year: yr(1999), ExternalIDs: map[string]string{"imdb": "tt0133093"}})
	f.add(Record{ID: "1396", Kind: "show", Title: str("Breaking Bad"), Year: yr(2008), Genres: []string{"Drama"}})
	f.results["heat"] = []Candidate{{Provider: "tmdb", ID: "949", Kind: "movie", Title: "Heat", Year: 1995},
		{Provider: "tmdb", ID: "1", Kind: "movie", Title: "Heat", Year: 1986}}
	f.results["dune"] = []Candidate{{Provider: "tmdb", ID: "438631", Kind: "movie", Title: "Dune", Year: 2021},
		{Provider: "tmdb", ID: "841", Kind: "movie", Title: "Dune", Year: 1984}}
	f.results["breaking bad"] = []Candidate{{Provider: "tmdb", ID: "1396", Kind: "show", Title: "Breaking Bad", Year: 2008}}
	return f
}

func (f *fakeProvider) add(r Record) {
	f.records["tmdb:"+r.ID] = r
	for ns, id := range r.ExternalIDs {
		f.records[ns+":"+id] = r
	}
}

func (f *fakeProvider) Name() string         { return "tmdb" }
func (f *fakeProvider) Namespaces() []string { return []string{"tmdb", "imdb"} }

func (f *fakeProvider) call(what string) error {
	f.mu.Lock()
	f.calls = append(f.calls, what)
	during, err := f.during, f.err
	f.mu.Unlock()
	if during != nil {
		during()
	}
	return err
}

func (f *fakeProvider) Search(_ context.Context, q Query) ([]Candidate, error) {
	if err := f.call("search " + Normalize(q.Title)); err != nil {
		return nil, err
	}
	return f.results[Normalize(q.Title)], nil
}

func (f *fakeProvider) Lookup(_ context.Context, kind, ns, id string) (Record, error) {
	if err := f.call("lookup " + ns + ":" + id); err != nil {
		return Record{}, err
	}
	r, ok := f.records[ns+":"+id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

// Fix-match after review: the chosen ID is used and the candidates go.
func TestFixMatchAfterReview(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Dune/Dune.mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Dune")
	if out := e.match(id); out != OutcomeReview {
		t.Fatalf("ambiguous remake: %s", out)
	}
	var cands int
	e.db.QueryRow(`SELECT COUNT(*) FROM metadata_candidates WHERE item_id = ?`, id).Scan(&cands)
	if cands != 2 {
		t.Fatalf("%d candidates", cands)
	}
	if _, err := e.st.FixMatch(context.Background(), id, e.get(id).Revision, "tmdb", "841"); err != nil {
		t.Fatal(err)
	}
	e.db.QueryRow(`SELECT COUNT(*) FROM metadata_candidates WHERE item_id = ?`, id).Scan(&cands)
	if cands != 0 {
		t.Fatal("candidates kept")
	}
}

func extID(md contracts.ItemMetadata, ns string) (contracts.MetadataExternalID, bool) {
	for _, x := range md.ExternalIDs {
		if x.Namespace == ns {
			return x, true
		}
	}
	return contracts.MetadataExternalID{}, false
}

// An ID hint in the folder name (LIB-2a) is validated by a lookup and
// applied: fields with the provider's provenance, and the IDs it reports.
func TestNameHintMatches(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("outcome %s", out)
	}
	md := e.get(id)
	f := md.Fields
	if md.MatchStatus != contracts.MatchMatched || *f.Overview.Value != "A group of high-end professional thieves." ||
		f.Overview.Source.Provider != "tmdb" || *f.Overview.Source.ProviderID != "949" || !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) ||
		f.ContentRating.Value.Region != "AU" {
		t.Fatalf("metadata %+v", md)
	}
	if x, ok := extID(md, "imdb"); !ok || x.ID != "tt0113277" || x.Locked {
		t.Fatalf("imdb %+v", x)
	}
	if !slices.Equal(e.fake.calls, []string{"lookup tmdb:949"}) {
		t.Fatalf("calls %v: a valid hint needs no search", e.fake.calls)
	}
}

// A Kodi NFO beside the file supplies the ID when the name has none.
func TestNFOHintMatches(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/The Matrix (1999)/The Matrix (1999).mkv", "x")
	e.write("Movies/The Matrix (1999)/movie.nfo", `<movie><uniqueid type="imdb">tt0133093</uniqueid></movie>`)
	e.library("movies", "Movies")
	if out := e.match(e.item("The Matrix")); out != OutcomeMatched || !slices.Equal(e.fake.calls, []string{"lookup imdb:tt0133093"}) {
		t.Fatalf("outcome %s, calls %v", out, e.fake.calls)
	}
}

// Without hints, a confident search result is applied; a low-confidence
// one never is: it goes to the review queue with its candidates.
func TestSearchMatchesOnlyWhenConfident(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.write("Movies/Dune/Dune.mkv", "x")
	e.write("Movies/Unknown Film (2001)/Unknown Film (2001).mkv", "x")
	e.library("movies", "Movies")
	if out := e.match(e.item("Heat")); out != OutcomeMatched {
		t.Fatalf("Heat: %s", out)
	}
	if out := e.match(e.item("Dune")); out != OutcomeReview || e.get(e.item("Dune")).MatchStatus != contracts.MatchReview {
		t.Fatalf("Dune without a year: %s", out)
	}
	if out := e.match(e.item("Unknown Film")); out != OutcomeReview {
		t.Fatalf("no results: %s", out)
	}
	if md := e.get(e.item("Dune")); md.Fields.Overview.Value != nil {
		t.Fatal("a review item got provider data")
	}
}

// §3.7: refreshing never overwrites a locked field; unlocked ones update.
func TestLockedFieldsSurviveRefresh(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.match(id)
	md := e.get(id)
	md, err := e.st.Edit(context.Background(), id, md.Revision, []Edit{{Field: FieldTitle, Value: raw("Heat (1995 cut)")}, {Field: FieldGenres, Lock: ptr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	r := e.fake.records["tmdb:949"]
	r.Overview, r.Genres = str("Updated overview."), []string{"Thriller"}
	e.fake.add(r)
	e.clock = e.clock.Add(time.Hour)
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("refresh: %s", out)
	}
	f := e.get(id).Fields
	if *f.Title.Value != "Heat (1995 cut)" || f.Title.Source.Provider != SourceUser || !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) ||
		*f.Overview.Value != "Updated overview." {
		t.Fatalf("after refresh %+v", f)
	}
}

// The admin's fix-match ID wins over every hint, and a locked ID is never
// replaced: a record reporting a different ID makes a conflict instead.
func TestLockedIDWinsAndConflictsAreRaised(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-1}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if _, err := e.st.FixMatch(context.Background(), id, 1, "tmdb", "949"); err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched || !slices.Equal(e.fake.calls, []string{"lookup tmdb:949"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
	// The locked ID keeps the admin's provenance after the match.
	if x, _ := extID(e.get(id), "tmdb"); x.ID != "949" || !x.Locked || x.Source.Provider != SourceUser {
		t.Fatalf("locked tmdb ID after matching: %+v", x)
	}
	// The admin also locks imdb to another title; the record says otherwise.
	tx, _ := e.db.Begin()
	tx.Exec(`UPDATE metadata_external_ids SET value = 'tt9999999', locked = 1 WHERE item_id = ? AND namespace = 'imdb'`, id)
	tx.Commit()
	if out := e.match(id); out != OutcomeConflict {
		t.Fatalf("outcome %s, want conflict", out)
	}
	after := e.get(id)
	if x, _ := extID(after, "imdb"); after.MatchStatus != contracts.MatchConflict || x.ID != "tt9999999" {
		t.Fatalf("conflict didn't keep the lock: %+v", after)
	}
}

// Two hints that disagree (the folder says one TMDB ID, the NFO another)
// are a conflict for the admin, not a guess.
func TestConflictingHints(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.write("Movies/Heat (1995) {tmdb-949}/movie.nfo", `<movie><tmdbid>1</tmdbid></movie>`)
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeConflict || len(e.fake.calls) != 0 {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
}

// A hint the provider doesn't know falls back to searching.
func TestInvalidHintFallsBackToSearch(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-404404}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	if out := e.match(e.item("Heat")); out != OutcomeMatched ||
		!slices.Equal(e.fake.calls, []string{"lookup tmdb:404404", "search heat", "lookup tmdb:949"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
}

// A hint that names a record of another kind (a show's ID on a movie
// folder) isn't applied: matching falls back to searching.
func TestHintOfAnotherKindFallsBack(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-1396}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeMatched ||
		!slices.Equal(e.fake.calls, []string{"lookup tmdb:1396", "search heat", "lookup tmdb:949"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
	if x, _ := extID(e.get(id), "tmdb"); x.ID != "949" {
		t.Fatalf("matched %+v", x)
	}
}

// Compare-and-set: an admin edit made while the provider was being asked
// wins; the provider's result is dropped and the item retried.
func TestEditDuringMatchWins(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.fake.during = func() {
		e.fake.during = nil
		if _, err := e.st.Edit(context.Background(), id, 1, []Edit{{Field: FieldOverview, Value: raw("Mine.")}}); err != nil {
			t.Error(err)
		}
	}
	if out := e.match(id); out != OutcomeRetry {
		t.Fatalf("outcome %s, want retry", out)
	}
	md := e.get(id)
	if *md.Fields.Overview.Value != "Mine." || md.MatchStatus != contracts.MatchUnmatched || md.Fields.Genres.Source != nil {
		t.Fatalf("the provider result overwrote the edit: %+v", md)
	}
}

// An outage keeps existing metadata, and retries back off (doubling,
// capped at a day) instead of hammering the provider.
func TestOutageKeepsMetadataAndBacksOff(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.match(id)
	before := e.get(id)
	e.fake.err = fmt.Errorf("%w: 503", ErrUnavailable)
	var next []int64
	for range 13 {
		if out := e.match(id); out != OutcomeRetry {
			t.Fatalf("outcome %s", out)
		}
		var at int64
		e.db.QueryRow(`SELECT next_attempt_at FROM metadata_items WHERE item_id = ?`, id).Scan(&at)
		next = append(next, at-e.clock.UnixMilli())
	}
	if next[0] != time.Minute.Milliseconds() || next[1] != 2*time.Minute.Milliseconds() || next[12] != retryCap.Milliseconds() {
		t.Fatalf("backoff %v", next)
	}
	after := e.get(id)
	if *after.Fields.Overview.Value != *before.Fields.Overview.Value || after.MatchStatus != contracts.MatchMatched {
		t.Fatalf("outage changed metadata: %+v", after)
	}
}

// An auth error (a revoked key) pauses the provider: the batch stops, the
// item stays queued, and nothing runs until Resume.
func TestAuthErrorPausesProvider(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.write("Movies/The Matrix (1999)/The Matrix (1999).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	if n, err := e.st.Enqueue(ctx, 10); n != 2 || err != nil {
		t.Fatalf("Enqueue = %d, %v", n, err)
	}
	e.fake.err = fmt.Errorf("%w: 401", ErrAuth)
	if b, err := e.m.RunDue(ctx, 10); !errors.Is(err, ErrPaused) || b.Matched != 0 {
		t.Fatalf("RunDue = %+v, %v", b, err)
	}
	if paused, _, _ := e.st.Paused(ctx, "tmdb"); !paused {
		t.Fatal("not paused")
	}
	if len(e.fake.calls) != 1 {
		t.Fatalf("%d calls: a paused provider must not be called again", len(e.fake.calls))
	}
	e.fake.err = nil
	if _, err := e.m.RunDue(ctx, 10); !errors.Is(err, ErrPaused) {
		t.Fatalf("still paused: %v", err)
	}
	e.st.Resume(ctx, "tmdb")
	if b, err := e.m.RunDue(ctx, 10); err != nil || b.Matched != 1 || b.Review != 1 {
		t.Fatalf("after resume %+v %v", b, err)
	}
}

// Work is bounded (SCALE-03): Enqueue and RunDue take a limit, each item is
// its own transaction, and a later batch picks up the rest.
func TestBatchesAreBounded(t *testing.T) {
	e := newMatchEnv(t)
	for i := range 5 {
		e.write(fmt.Sprintf("Movies/Film %d (2001)/Film %d (2001).mkv", i, i), "x")
	}
	e.library("movies", "Movies")
	ctx := context.Background()
	if n, _ := e.st.Enqueue(ctx, 3); n != 3 {
		t.Fatalf("queued %d, want 3", n)
	}
	if n, _ := e.st.Enqueue(ctx, 10); n != 2 {
		t.Fatalf("queued %d more, want 2", n)
	}
	b, err := e.m.RunDue(ctx, 2)
	if err != nil || b.Review != 2 {
		t.Fatalf("first batch %+v %v", b, err)
	}
	b, err = e.m.RunDue(ctx, 10)
	if err != nil || b.Review != 3 {
		t.Fatalf("second batch %+v %v", b, err)
	}
	if b, _ := e.m.RunDue(ctx, 10); b.Review != 0 {
		t.Fatal("review items were queued again")
	}
}

// Matching only writes metadata tables: the catalogue the player and
// browsing read is untouched (offline playback unaffected).
func TestMatchingLeavesTheCatalogueAlone(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	snap := func() string {
		var s string
		e.db.QueryRow(`SELECT group_concat(id || title || sort_title || coalesce(year, ''), ';') FROM
			(SELECT * FROM library_items ORDER BY id)`).Scan(&s)
		var f string
		e.db.QueryRow(`SELECT group_concat(path || state || size, ';') FROM (SELECT * FROM library_files ORDER BY path)`).Scan(&f)
		return s + "|" + f
	}
	before := snap()
	e.match(e.item("Heat"))
	if after := snap(); after != before {
		t.Fatalf("catalogue changed:\n%s\n%s", before, after)
	}
}

// Shows match like movies, with tvshow.nfo in the show folder.
func TestShowMatchesFromTvshowNFO(t *testing.T) {
	e := newMatchEnv(t)
	e.write("TV/Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "x")
	e.write("TV/Breaking Bad/tvshow.nfo", `<tvshow><uniqueid type="tmdb">1396</uniqueid></tvshow>`)
	e.library("tv", "TV")
	id := e.item("Breaking Bad")
	if out := e.match(id); out != OutcomeMatched || !slices.Equal(e.fake.calls, []string{"lookup tmdb:1396"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
	var season string
	e.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'season'`).Scan(&season)
	if _, err := e.m.MatchItem(context.Background(), season); !errors.Is(err, ErrInvalid) {
		t.Fatalf("season: %v (seasons come from the show's record, META-1b)", err)
	}
}

// #121 review: the backoff never overflows, however long an item has been
// failing. It stays at the cap (plus at most 10% jitter).
func TestBackoffStaysCappedAfterManyFailures(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.match(id)
	e.fake.err = fmt.Errorf("%w: 503", ErrUnavailable)
	for _, attempts := range []int{28, 29, 63, 64, 1000} {
		e.db.Exec(`UPDATE metadata_items SET attempts = ? WHERE item_id = ?`, attempts, id)
		if out := e.match(id); out != OutcomeRetry {
			t.Fatalf("outcome %s", out)
		}
		var at int64
		e.db.QueryRow(`SELECT next_attempt_at FROM metadata_items WHERE item_id = ?`, id).Scan(&at)
		if d := at - e.clock.UnixMilli(); d != retryCap.Milliseconds() {
			t.Fatalf("after %d attempts the next try is in %v, want the %v cap", attempts, time.Duration(d)*time.Millisecond, retryCap)
		}
	}
}

// Retries are jittered by up to 10% so items that failed together don't
// all retry in the same instant (§3.7: bounded retries with jitter).
func TestRetryJitterIsBounded(t *testing.T) {
	for _, d := range []time.Duration{time.Minute, retryCap} {
		lo, hi := d, d
		for range 200 {
			j := defaultJitter(d)
			lo, hi = min(lo, j), max(hi, j)
		}
		if lo < d || hi > d+d/10 || hi == lo {
			t.Fatalf("jitter of %v ranged %v..%v, want within [d, 1.1d] and not constant", d, lo, hi)
		}
	}
}

// #121 review: an item the admin edits before matching reaches it is still
// queued, so it gets matched (keeping the edit).
func TestEditBeforeFirstMatchStillMatches(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	id := e.item("Heat")
	if _, err := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldYear, Lock: ptr(true)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Enqueue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if b, err := e.m.RunDue(ctx, 10); err != nil || b.Matched != 1 {
		t.Fatalf("RunDue %+v %v: the edited item wasn't matched", b, err)
	}
	if md := e.get(id); !md.Fields.Year.Locked || md.Fields.Overview.Value == nil {
		t.Fatalf("after matching %+v", md)
	}
}

// #121 review: a second fix-match in another namespace replaces the first
// cleanly: the old film's IDs and unlocked fields go, the admin's own
// edits stay, and the item ends matched, not in a permanent conflict.
func TestSecondFixMatchReplacesTheFirst(t *testing.T) {
	e := newMatchEnv(t)
	str := func(s string) *string { return &s }
	r := e.fake.records["tmdb:603"]
	r.Overview, r.Genres, r.SortTitle = str("Neo learns the truth."), []string{"Science Fiction"}, str("Matrix, The")
	e.fake.add(r)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	id := e.item("Heat")
	md, _ := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldTitle, Value: raw("Heat (my copy)")}})
	// The wrong film, by IMDb ID.
	md, err := e.st.FixMatch(ctx, id, md.Revision, "imdb", "tt0133093")
	if err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("first fix-match: %s", out)
	}
	// The right film, by TMDB ID.
	if _, err := e.st.FixMatch(ctx, id, e.get(id).Revision, "tmdb", "949"); err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("second fix-match: %s, want matched", out)
	}
	md = e.get(id)
	f := md.Fields
	if x, _ := extID(md, "imdb"); x.ID != "tt0113277" || x.Locked {
		t.Fatalf("imdb %+v: the first fix-match's ID is still there", x)
	}
	if x, _ := extID(md, "tmdb"); x.ID != "949" || !x.Locked {
		t.Fatalf("tmdb %+v", x)
	}
	if *f.Overview.Value != "A group of high-end professional thieves." || !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) {
		t.Fatalf("the wrong film's fields stayed: %+v", f)
	}
	// Heat's record has no sort title, so the wrong film's must be gone,
	// not just overwritten: the parsed one shows again.
	if *f.SortTitle.Value != "Heat" || f.SortTitle.Source != nil {
		t.Fatalf("sort title %q from %+v: the wrong film's value stayed", *f.SortTitle.Value, f.SortTitle.Source)
	}
	if *f.Title.Value != "Heat (my copy)" || !f.Title.Locked {
		t.Fatalf("the admin's edit was lost: %+v", f.Title)
	}
}

// #121 review: hints in different namespaces that point at different films
// (a {tmdb-…} folder and an NFO's IMDb ID) are a conflict, caught by
// comparing the looked-up record's IDs with the other hints.
func TestHintsDisagreeingAcrossNamespaces(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.write("Movies/Heat (1995) {tmdb-949}/movie.nfo", `<movie><uniqueid type="imdb">tt0133093</uniqueid></movie>`)
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeConflict {
		t.Fatalf("outcome %s, want conflict", out)
	}
	if md := e.get(id); md.MatchStatus != contracts.MatchConflict || md.Fields.Overview.Value != nil || len(md.ExternalIDs) != 0 {
		t.Fatalf("a conflicting record was applied: %+v", md)
	}
	// Hints that agree across namespaces match as usual.
	e2 := newMatchEnv(t)
	e2.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e2.write("Movies/Heat (1995) {tmdb-949}/movie.nfo", `<movie><uniqueid type="imdb">tt0113277</uniqueid></movie>`)
	e2.library("movies", "Movies")
	if out := e2.match(e2.item("Heat")); out != OutcomeMatched {
		t.Fatalf("agreeing hints: %s", out)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/metadata/
```

Expected: FAIL to build: `undefined: Matcher` (then `Record`, `Rating`, `ErrNotFound`, `ErrAuth`, `ErrUnavailable`).

- [ ] **Step 3: Implement `provider.go`** per Interfaces, with the doc comments: Search returns at most one page, best first; Lookup accepts the provider's own namespace or one it can resolve (e.g. imdb for TMDB), and returns `ErrNotFound` if there's no such record.

- [ ] **Step 4: Implement `apply.go`: `func (s *Store) applyRecord(ctx, itemID string, rev int64, prov string, rec Record) (string, error)`**

In one transaction:
1. If the item's revision isn't `rev`, return `errStale`.
2. The record's IDs are `{prov: rec.ID}` plus `rec.ExternalIDs`. If any of them differs from a **locked** ID in that namespace, set the status to `conflict` (via `finishMatch`) and commit; nothing else changes.
3. Otherwise upsert each ID **only where unlocked** (`… DO UPDATE … WHERE metadata_external_ids.locked = 0`), and each non-nil record field the same way (`… WHERE metadata_fields.locked = 0`), with provenance `prov`, `rec.ID` and now.
4. `finishMatch`: delete the candidates and set the status, `revision = revision + 1`, `next_attempt_at = NULL`, `attempts = 0`, `last_error = NULL`.

- [ ] **Step 5: Implement `matcher.go`**

`MatchItem` runs in this order:
1. **Paused provider:** return `OutcomeSkipped`; no calls are made.
2. **Read the item:** `itemInfo` gives the kind, parsed title, year, a root path (from its files or folders) and the revision, inserting the metadata row if missing. Only `movie` and `show` items are accepted; others give `ErrInvalid`.
3. **Locked ID:** if there's one in a provider namespace, `lookup` it with no hints to check (the admin's choice wins).
4. **Hints:** gather `library_id_hints` and NFO IDs (`nfoPaths`, then `readNFOs` through `Media.OpenRoot(rootPath)`; errors are skipped). Two values in a provider namespace mean `setStatus(conflict)`. Otherwise the first provider namespace that has a hint is looked up, **checking the record against all the hints** (Decision 4.2); a lookup that returns "" falls through to search.
5. **Search:** `Search`, then `Rank`. If the result is auto, look up the best candidate (no hints to check); if that lookup returns "", or the result isn't auto, store the top `reviewKeep` candidates, with the status set to `review`.

The helpers:
- **`lookup(ctx, itemID, info, ns, id string, check map[string]string)`:**
  - `ErrNotFound`, or a record of another kind: return "" with no error.
  - Any other error: `failed`.
  - If the record reports an ID (its own for the provider's namespace, else `ExternalIDs`) that differs from the hint in `check` for that namespace: `setStatus(conflict)` with a reason naming both hints, and return `OutcomeConflict`.
  - Otherwise `applyRecord`. If that returns `errStale`, call `retryLater(attempt 0)` and return `OutcomeRetry`.
- **`failed`:** `ErrAuth` → `Pause` and `OutcomeSkipped`. Otherwise `retryLater(attempts + 1)` and `OutcomeRetry`.
- **`retryLater`:** delay = `retryBase << min(attempt − 1, 11)` (attempt 0: `retryBase`), capped at `retryCap`, then `s.jitter(delay)`. The clamp comes before the shift (Decision 10). It sets `next_attempt_at`, `attempts` and a bounded `last_error`.
- **`setStatus` and `review`:** compare-and-set on the revision; a stale write turns into `retryLater(0)`.
- **`RunDue`:** select due IDs (`next_attempt_at <= now`, oldest first, `LIMIT ?`), then run `MatchItem` on each. Count the outcomes, collect database errors in `Failed`, stop with `ErrPaused` on `OutcomeSkipped`, and stop if `ctx` ends.
- **`Planned` tags:** `RunDue` and `MatchItem` get `// Planned: META-1c (the metadata job loop and the fix-match endpoint run them)`.

- [ ] **Step 6: Run the tests to verify they pass, several times**

```bash
go -C server test -race -count=5 ./internal/metadata/
```

Expected: PASS on every run.

- [ ] **Step 7: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): matching with §3.7 precedence, review queue and bounded jobs (META-1a)"
```

---

### Task 5: Full check and PR

- [ ] **Step 1:** `make check`. Expected: PASS, with `internal/metadata` `ok` under `-race`.
- [ ] **Step 2:** Push `feat/meta-1a-matching` and open the PR:
  - Body: `Refs #73` (META-1 stays open for 1b and 1c), and the local and Actions results.
  - Request lisim and ondona as reviewers.
  - Label `area:metadata`.

## Next slices (outlines, each its own plan)

**META-1b: providers.**
- **TMDB client** (`metadata/tmdb`), implementing `Provider`. Endpoints, from TMDB's API reference (checked 2026-10-28):
  - `GET /3/search/movie` and `/3/search/tv` (`query`, `year` / `first_air_date_year`, `language`, `page`);
  - `GET /3/movie/{id}` and `/3/tv/{id}` with `append_to_response=external_ids,release_dates|content_ratings,credits`;
  - `GET /3/find/{external_id}?external_source=imdb_id|tvdb_id` (results in `movie_results` and `tv_results`).
- **Credential:** the install's TMDB **API Read Access Token**, sent as `Authorization: Bearer …` (TMDB's documented alternative to the `api_key` query parameter), so it never appears in URLs or access logs. To decide in 1b's plan; D15 says "API key", and both come from the same TMDB settings page.
- **Limits:** about 10 requests/s with a small burst, well under TMDB's documented "somewhere in the 40 requests per second range". Respect 429 with backoff, honouring `Retry-After` when present: TMDB's rate-limit page doesn't document that header. A 10 s timeout, 2 retries with jitter, and 401/403 → `ErrAuth`.
- **Cache:** a response cache with `fetched_at`, refreshed after at most 180 days (D15: "within 6 months") and purged when the key is removed.
- **Fixtures:** recorded-shape fixtures written from TMDB's documented responses and served by `httptest`. No real key is used in tests.
- **NFO field provider:** titles and plots from Kodi NFOs.
- **Show records:** seasons and episodes, and `communityRating`, `directors` and `studios` for #114.

**META-1c: API and wiring.**
- **Contract additions** (through CTR-2's process): edit metadata (`PATCH`, with `expectedRevision`), fix-match (`POST`, with a candidate search), and the review queue.
- **Serve wiring:** the metadata job loop (queue after each LIB-2b scan, `RunDue` in bounded batches, paused state in admin status).
- **Serving results** through LIB-2c's `ItemSummary`/`ItemDetail` (titles, `matched`, the #114 fields), plus the artwork cache and route and the TMDB attribution flag for About/Credits.
- **Actual scale:** the SCALE-03 run on Pete's library: correct a match, lock a field, and run a bulk refresh while PERF-05 browsing is measured.

## Self-review

**Spec coverage:**

| Requirement | Where |
|---|---|
| §3.7 precedence: locks → validated IDs/NFO → deterministic match → review queue | Task 4 (`TestLockedIDWinsAndConflictsAreRaised`, `TestNameHintMatches`, `TestNFOHintMatches`, `TestSearchMatchesOnlyWhenConfident`) |
| §3.7 conflicting IDs raise a conflict instead of overwriting a lock | Task 4 (`TestLockedIDWinsAndConflictsAreRaised`, `TestConflictingHints`) |
| §3.7 imported fields keep provider, provider ID, fetch time and lock | Tasks 3–4 (`TestNameHintMatches`, `TestEditLocksAndChecksRevision`) |
| §3.2/§3.7 manual edits and locked fields survive refresh and rescan | Task 4 (`TestLockedFieldsSurviveRefresh`); rescans never write metadata tables |
| §3.7 outages keep metadata; bounded retries; configuration/auth errors pause a provider | Task 4 (`TestOutageKeepsMetadataAndBacksOff`, `TestAuthErrorPausesProvider`) |
| §3.2 local NFO | Task 2 (IDs); field values are 1b |
| §3.2 fix match | Tasks 3–4 (`TestFixMatchLocksTheChosenID`, `TestFixMatchAfterReview`); the endpoint is 1c |
| §0.3 local playback without metadata services | `TestMatchingLeavesTheCatalogueAlone`; nothing in browsing or playback reads `metadata` yet |
| SCALE-03 bounded jobs, per-item failures, intent preserved | Task 4 (`TestBatchesAreBounded`, `TestEditDuringMatchWins`); actual scale is 1c |
| §3.7 timeouts, rate limits, caching, keys never sent to clients | 1b (TMDB client) |

**Verified while writing this plan:**
- **Prototype:** all of this ran on LIB-2a's code (branch `feat/lib-2a-catalogue`, PR #111). `make check` passed, and the metadata tests passed 5 times in a row under `-race`.
- **Rebuild:** each task was rebuilt in order on an export of that base. Each failed with its Step 2 messages before its implementation and passed after, and the result is identical to the prototype.
- **Mutation check:** each of these 34 made a test fail:
  - locked fields overwritten on refresh
  - no compare-and-set when applying
  - a low score auto-matched
  - no margin between candidates
  - a locked ID's provenance replaced
  - no conflict on a locked ID
  - conflicting hints accepted
  - the locked ID not used first
  - no fallback from an invalid hint
  - no backoff doubling
  - no backoff cap
  - an auth error not pausing
  - a paused provider still called
  - `RunDue` unbounded
  - `Enqueue` unbounded
  - edits not compare-and-set
  - manual edits not locked
  - NFO reads unbounded
  - NFO IDs not validated
  - no accent folding
  - no year penalty
  - the record kind not checked
  - year edits unbounded
  - fix-match keeping candidates
  - added after Pete's review (#121):
    - the backoff shift not clamped
    - no retry jitter
    - jitter of up to 50% instead of 10%
    - an edited item's new row not queued
    - fix-match keeping the other namespaces' IDs
    - fix-match keeping unlocked fields
    - fix-match deleting locked fields too
    - hints not checked across namespaces
    - marks dropped in every script
    - Latin marks kept
- **Pete's review on #121** found three blocking problems, each now fixed with a test that failed first:
  - **Backoff overflow:** the plan described the backoff as `retryBase << (attempt − 1)`, which overflows after 29 attempts. The prototype already clamped the shift (at 20), so `TestBackoffStaysCappedAfterManyFailures` passed at once; its mutant (no clamp) is caught.
  - **An edit before matching** left the item unqueued forever.
  - **A second fix-match in another namespace** left a permanent conflict.

  Four non-blocking notes are also handled here: Latin-only folding, the admin-merge note (Decision 2), hints checked across namespaces, and nits (provenance constants, trimming, the genres test, jitter).
- **Found while prototyping:**
  - The first conflict check hid that a match could replace a locked ID's `user` provenance with the provider's; the test now pins it.
  - No test covered a hint pointing at the wrong kind of record; added.
- **Placeholders:** none.
- **Names and types** match between tasks: Task 4 uses Task 3's `newEnv`, `raw` and `ptr`, Task 2's `readNFOs` and Task 1's `Rank`.
