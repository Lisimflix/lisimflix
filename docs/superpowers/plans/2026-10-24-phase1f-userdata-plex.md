# Phase 1F: User Data & Plex Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-profile watch state (progress, watched, play count, history, watchlist, ratings,
per-show track choices, home-row layout), the personal home rows API (Continue Watching,
Next Up, Recently Added per library, Watchlist), and a one-time admin Plex import that
copies every household member's watch state from the owner's Plex Media Server, with a
dry-run report before it writes anything.

**Architecture:** `server/internal/userdata` owns six tables and a `Store` (the 1F contract
plus extra methods). The pure `Rules` type decides when an item counts as watched or can be
resumed. A `Home` builder turns states and catalog data into rows, filtered through an
`Access` helper that applies `auth.VisibleLibraryIDs` and `auth.MaxContentRating`.
`server/internal/importer/plex` is a small PMS JSON client, a matcher (external IDs, then
file path with prefix mapping, then title and year), an import engine that merges into
userdata without ever downgrading, and an in-memory background job manager behind admin
routes. Both packages are `app.Module`s added to `main.go`.

**Tech Stack:** Go 1.27, chi v5, modernc.org/sqlite, log/slog, `encoding/json` +
`encoding/xml` (stdlib). No new third-party dependencies.

**Spec:** `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (sections 3.3, 3.4, 3.5, 5.2, 5.3)
**Index / shared contracts:** `docs/superpowers/plans/2026-10-24-phase1-index.md`
**Conventions:** `docs/superpowers/plans/2026-10-24-phase1a-core.md`

## Global Constraints

- Go **1.27**. Module `github.com/Lisimflix/lisimflix/server` in `server/`. `CGO_ENABLED=0`.
- SQLite via `modernc.org/sqlite`; tables are `STRICT` with explicit `ON DELETE` behaviour.
- Router `github.com/go-chi/chi/v5`. Logging `log/slog` only.
- All routes under `/api/v1`. JSON only. Errors use `httpx.WriteError(w, status, "snake_code", "message")`.
- Migrations live in `server/internal/db/migrations/`, named `YYYYMMDDHHMMSS_<module>_<what>.sql`.
  This plan uses `20261025140000`–`20261025140300` (module `userdata`). Never edit a merged migration.
- TDD for every task. `make test` passes before every commit. **No network in unit tests**:
  Plex and plex.tv are faked with `httptest.Server` and fixtures under `testdata/`.
- `/media` is read-only. Nothing here touches the filesystem under the media root.
- Contract names in the index are fixed. 1F implements the `userdata` contract exactly and
  only adds to it (see "Contract additions requested").
- Each module owns its tables. userdata never writes to `items`, `profiles` or any 1B/1C/1D
  table. (It reads `profiles` once, in `ProfileExists`, to validate import targets. The
  test-only `udtest` package seeds 1B/1C tables for tests.)
- Secrets: the Plex token is never stored, logged or returned. It lives only in memory for
  the duration of one import job (decision recorded in Task 15).
- Workflow (CONTRIBUTING.md): branch off an up-to-date `main` as `feat/1f-userdata-plex`
  (one PR for the whole plan, or split at task boundaries into `feat/1f-userdata` (Tasks 1-11)
  and `feat/1f-plex-import` (Tasks 12-16)). Never commit to or push `main`, never `--no-verify`. Small per-task
  commits on the branch are fine. PRs are **squash-merged** after another member's review, and
  PR titles use Conventional Commits with the area as scope: `feat(userdata): ...`,
  `feat(importer): ...`. Commits end with a `Co-Authored-By:` line when an AI agent wrote them.
- This plan never overwrites `CONTRIBUTING.md`, `CLAUDE.md` or `.gitignore` (nothing to append either).
- Out of scope: AniList/MAL watch sync (spec 3.3) is a later phase. It will hook into
  `Store.RecordProgress`/`SetWatched`, but nothing here calls an external tracker.

## Contract additions requested

1F starts after 1B, 1C and 1D are merged. It uses the index contracts plus these names, which
the finished 1B/1C/1D plans already provide:

- **1B (`auth`):** `func RatingAge(label string) (age int, known bool)` (now in the index). An item is
  allowed when `RatingAge(itemRating) <= RatingAge(MaxContentRating)`. Unknown/empty labels count
  as 18, so unrated items are hidden under any limit below 18. `VisibleLibraryIDs` returns every
  library for admins and the explicit allow-list (empty = none) for others. FK target is
  `profiles(id)`. Test seeding mirrors 1B's own harness: `users(display_name, is_admin,
  created_at, updated_at)` and `profiles(user_id, name, is_primary, created_at, updated_at)`
  (unix-second timestamps).
- **1C (`library`, `media`):** `library.Store.Create`, `media.ErrNotFound` (from `GetItem`),
  `media.Kind*` constants, `Item.ExtraType` (extras are `video` children of a movie/show and are
  skipped by 1F), `Store.UpsertItem`, `Store.SetExternalIDs`, `Store.PutFile`. FK target is `items(id)`.
- **1D (`metadata`):** `(*metadata.Store).Load(ctx, itemID) (View, bool, error)` with
  `View.ContentRating`. 1D stores ratings on top-level items. 1F's `TopLevelRatings` adapter
  (Task 7) walks an episode/season up to its show before looking the rating up.

**Requested addition to 1C's `media` package (written by this plan in Task 0, as one additive PR
commit touching `server/internal/media/lookup.go` + test; ask the 1C owner to review that file):**
```go
// FindByExternalID returns items whose item_external_ids has provider=key, value=value.
func (s *Store) FindByExternalID(ctx context.Context, key, value string) ([]Item, error)
// FindByFilePath returns the item owning the file at the exact path, or ErrNotFound.
func (s *Store) FindByFilePath(ctx context.Context, path string) (Item, error)
// FindByTitleYear matches Title or OriginalTitle case-insensitively (ASCII); year 0 = any year.
func (s *Store) FindByTitleYear(ctx context.Context, kind Kind, title string, year int) ([]Item, error)
// RecentlyAdded returns top-level, non-extra items of a library, newest first; a show counts
// as new when any of its seasons/episodes is new.
func (s *Store) RecentlyAdded(ctx context.Context, libraryID int64, limit int) ([]Item, error)
```

**New test-only package owned by 1F:** `server/internal/userdata/udtest` (Task 0), so tests can
seed profiles, libraries, items and files without new test helpers in 1B/1C. It writes to
1B/1C tables **in tests only**, through 1C's exported Store methods where they exist.

**For phase 2 (trickplay), to implement later:** `userdata.CreditsSource`
(`CreditsStartMs(ctx, itemID) (int64, bool, error)`). Until then `userdata.NoCredits{}` is used.

**Index update (made by this plan's PR, in the index file):**
- `userdata.State` gains `DurationMs int64` (additive field).
- `Store.Get` returns a zero `State` (with `ProfileID`/`ItemID` set) and `nil` error when no row exists.
- 1F also owns `home_row_prefs`.
- 1F registers these `/api/v1` paths. Other modules must not use them, or `r.Mount`/`r.Route` `/me`:
  `/me/progress/*`, `/me/items/*`, `/me/continue/*`, `/me/home`, `/me/home/rows`,
  `/me/watchlist*`, `/me/ratings/*`, `/me/history`, `/me/track-prefs/*`, `/import/plex/*`.

## Research notes: Plex Media Server API (what the importer relies on)

- PMS answers XML by default and JSON with `Accept: application/json`. The token goes in the
  `X-Plex-Token` header (never the query string, so it can't leak into logs).
- `GET /identity` returns `machineIdentifier` and `version`.
- `GET /accounts` lists accounts known to this server: `id` 0 is a system account and is
  skipped, `id` 1 is the server owner, and other ids are the plex.tv user IDs of shared and
  managed users who have used the server.
- `GET /library/sections` lists libraries (`Directory[]` with `key`, `type` = `movie|show|artist|photo`,
  `Location[].path`).
- `GET /library/sections/{key}/all?type=1|2|4&includeGuids=1` lists movies, shows or episodes.
  Paging is done with the `X-Plex-Container-Start` and `X-Plex-Container-Size` query params,
  and the response has `totalSize`. Items have `ratingKey`, `guid` (the new `plex://...` or
  legacy `com.plexapp.agents.imdb://tt..`), `Guid[]` (`imdb://`, `tmdb://`, `tvdb://`; episode
  entries carry *episode* IDs), `viewCount`, `viewOffset` (ms), `lastViewedAt` (unix s),
  `userRating` (0–10), `duration` (ms), `Media[].Part[].file`. Episodes also have
  `grandparentRatingKey`/`grandparentTitle`, `parentIndex` (season) and `index` (episode).
- **The watch fields on a listing belong to whoever owns the token.** The owner's token
  returns the owner's state only.
- `GET /status/sessions/history/all?accountID={id}&sort=viewedAt:desc` (owner token) returns
  the play history of any account: one row per *finished* play, with `ratingKey`,
  `viewedAt`, `accountID`, titles, `parentIndex` and `index`. It has no partial progress and no
  ratings. Rows can point at items that no longer exist, and PMS may have pruned old history.
- Per-user server tokens: `GET https://plex.tv/api/servers/{machineIdentifier}/shared_servers`
  with the owner's plex.tv token (in a normal setup the server admin token is the owner's
  account token) returns XML `<SharedServer userID=".." accessToken="..">` for every user the
  server is shared with, managed home users included. With that token the same library
  listings return **that user's** `viewOffset`, `viewCount`, `lastViewedAt` and `userRating`.
  python-plexapi's `MyPlexUser.get_token()` uses the same approach.

**Chosen approach (most complete, honest about gaps):**
1. Owner (account 1) gets full state from listings made with the admin token.
2. Every other mapped account gets full state from listings made with its own server token,
   either pasted by the admin or discovered through plex.tv `shared_servers` when
   `discoverUserTokens` is true (the only call to plex.tv, and it is opt-in).
3. Accounts with no usable token fall back to **history only**: watched, play count and last
   played come from `/status/sessions/history/all?accountID=`. Their in-progress positions
   and ratings cannot be imported. The report shows `source: "history_only"`.
4. Play history rows are always imported from history, for every account.

**Not importable (listed in every report):** Plex watchlists (kept on plex.tv Discover, not
the server); in-progress positions and ratings for accounts without a token; plays of items
no longer in Plex; history PMS already pruned; per-item audio/subtitle choices, playlists,
collections and "remove from Continue Watching"; music and photo libraries.

## File Structure

```
server/internal/db/migrations/
  20261025140000_userdata_state.sql        user_item_state, watch_history
  20261025140100_userdata_lists.sql        watchlist, ratings
  20261025140200_userdata_track_prefs.sql  track_prefs
  20261025140300_userdata_home_rows.sql    home_row_prefs

server/internal/media/lookup.go(+_test)   (1C package, additive) FindByExternalID/FilePath/TitleYear, RecentlyAdded
server/internal/userdata/udtest/udtest.go(+_test)  test-only seeding (profiles, libraries, items, files)

server/internal/userdata/
  rules.go            Rules, Progress, DefaultRules, LoadRules (pure, settings-driven)
  kinds.go            media kind constants, IsPlayable
  store.go            State, Store, NewStore, Get, Put, GetMany (+ raw helpers)
  progress.go         RecordProgress, SetWatched, HideFromContinue, InProgress, RecentlyWatched, CreditsSource
  lists.go            watchlist, ratings, history (cursor paging)
  trackprefs.go       TrackPrefs get/set + validation
  homerows.go         row keys, RowPref store, ArrangeRows
  access.go           Catalog/Libraries/Policy/ContentRatings interfaces, Access, Viewer, Leaves
  home.go             Card, Home: ContinueWatching, NextUp
  home_rows.go        Home: RecentlyAdded, WatchlistRow, Layout, Build
  merge.go            Imported, MergeResult, Merge (never-downgrade), ProfileExists
  http.go             Module, Deps, progress/watched/continue/home/rows handlers
  http_lists.go       watchlist, ratings, history, track-prefs handlers
  *_test.go           fixture_test.go, fakes_test.go and one test file per source file

server/internal/importer/plex/
  types.go            PMS JSON types, ExternalIDs, Files
  client.go           Client: Identity, Accounts, Sections, Items, History (paged)
  plextv.go           SharedUserTokens (plex.tv XML)
  matcher.go          PathMapping, MapPath, Matcher (ids → path → title/year)
  engine.go           Request, Report, Run
  jobs.go             Manager (background jobs, in memory)
  http.go             Module: /import/plex/accounts, /jobs, /jobs/{id}
  fakepms_test.go     httptest fake PMS + plex.tv serving testdata
  fixture_test.go     Lisimflix DB fixture for matcher/engine tests
  testdata/*.json, testdata/shared_servers.xml

server/cmd/lisimflix/main.go       (modify) construct + register both modules
server/cmd/lisimflix/main_test.go  (modify) routes-mounted test
docs/superpowers/plans/2026-10-24-phase1-index.md  (modify) index updates above
```

---

### Task 0: Branch, media lookups (addition to 1C's package), test seeding package

**Files:**
- Create: `server/internal/media/lookup.go`, `server/internal/media/lookup_test.go` (1C's package; additive; ask the 1C owner to review)
- Create: `server/internal/userdata/udtest/udtest.go`, `server/internal/userdata/udtest/udtest_test.go`

**Interfaces:**
- Consumes: 1C `media.Store` internals in the same package (`queryItems`, `UpsertItem`, `SetExternalIDs`, `PutFile`, test helper `setup(t)` and `ip`), `library.Store.Create`; 1B tables `users`, `profiles`
- Produces:
  - `(*media.Store) FindByExternalID(ctx, key, value string) ([]Item, error)`
  - `(*media.Store) FindByFilePath(ctx, path string) (Item, error)` (`ErrNotFound` when no file has that path)
  - `(*media.Store) FindByTitleYear(ctx, kind Kind, title string, year int) ([]Item, error)`
  - `(*media.Store) RecentlyAdded(ctx, libraryID int64, limit int) ([]Item, error)`
  - `udtest.NewProfile(t, d, name) (userID, profileID int64)`, `udtest.NewLibrary(t, d, name, library.Type) int64`,
    `udtest.NewItem(t, d, media.Item) media.Item`, `udtest.NewFile(t, d, itemID int64, path string) media.File`

- [ ] **Step 1: Create the branch**

```bash
git switch main && git pull && git switch -c feat/1f-userdata-plex
git config core.hooksPath   # must print .githooks; if not, stop and ask the user before setting it
```

- [ ] **Step 2: Write the failing tests**

`server/internal/media/lookup_test.go`:
```go
package media

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLookups(t *testing.T) {
	ctx := context.Background()
	s, lib := setup(t)
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	show, _ := s.UpsertItem(ctx, lib, "show:/media/tv/A", Item{Kind: KindShow, Title: "Firefly", Year: 2002})
	season, _ := s.UpsertItem(ctx, lib, "season:/media/tv/A|1", Item{Kind: KindSeason, ParentID: show, SeasonNumber: ip(1)})
	clock = clock.Add(time.Hour)
	movie, _ := s.UpsertItem(ctx, lib, "movie:/media/tv/M", Item{Kind: KindMovie, Title: "Solaris", Year: 2002})
	s.UpsertItem(ctx, lib, "extra:/media/tv/M/t.mkv", Item{Kind: KindVideo, ParentID: movie, ExtraType: ExtraTrailer, Title: "Trailer"})
	clock = clock.Add(time.Hour)
	ep, _ := s.UpsertItem(ctx, lib, "episode:/media/tv/A|s1|e1", Item{Kind: KindEpisode, ParentID: season,
		SeasonNumber: ip(1), EpisodeNumber: ip(1), Title: "Serenity"})
	if err := s.SetExternalIDs(ctx, show, map[string]string{"tvdb": "78874"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutFile(ctx, lib, File{ItemID: ep, Path: "/media/tv/A/S01E01.mkv", Size: 1, ModTime: clock}); err != nil {
		t.Fatal(err)
	}

	if got, err := s.FindByExternalID(ctx, "tvdb", "78874"); err != nil || len(got) != 1 || got[0].ID != show {
		t.Fatalf("by external id: %+v %v", got, err)
	}
	if got, _ := s.FindByExternalID(ctx, "tmdb", "78874"); len(got) != 0 {
		t.Fatalf("provider must match: %+v", got)
	}
	if got, err := s.FindByFilePath(ctx, "/media/tv/A/S01E01.mkv"); err != nil || got.ID != ep {
		t.Fatalf("by path: %+v %v", got, err)
	}
	if _, err := s.FindByFilePath(ctx, "/media/tv/A/nope.mkv"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing path: %v", err)
	}
	for _, c := range []struct {
		kind  Kind
		title string
		year  int
		want  int
	}{
		{KindMovie, "SOLARIS", 2002, 1}, {KindMovie, " solaris ", 0, 1}, {KindMovie, "Solaris", 1972, 0}, {KindShow, "Solaris", 0, 0},
	} {
		if got, err := s.FindByTitleYear(ctx, c.kind, c.title, c.year); err != nil || len(got) != c.want {
			t.Fatalf("%s %q %d: %d results (%v), want %d", c.kind, c.title, c.year, len(got), err, c.want)
		}
	}
	got, err := s.RecentlyAdded(ctx, lib, 10)
	if err != nil || len(got) != 2 || got[0].ID != show || got[1].ID != movie {
		t.Fatalf("recently added (show has the newest episode; no extras): %+v %v", got, err)
	}
	if one, _ := s.RecentlyAdded(ctx, lib, 1); len(one) != 1 {
		t.Fatalf("limit: %+v", one)
	}
}
```

`server/internal/userdata/udtest/udtest_test.go`:
```go
package udtest

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func TestSeeding(t *testing.T) {
	d := dbtest.Open(t)
	uid, pid := NewProfile(t, d, "zac")
	if uid == 0 || pid == 0 {
		t.Fatalf("ids %d %d", uid, pid)
	}
	lib := NewLibrary(t, d, "Movies", library.TypeMovies)
	it := NewItem(t, d, media.Item{LibraryID: lib, Kind: media.KindMovie, Title: "Heat", Year: 1995,
		ExternalIDs: map[string]string{"tmdb": "949"}})
	if it.ID == 0 || it.Title != "Heat" || it.ExternalIDs["tmdb"] != "949" {
		t.Fatalf("item %+v", it)
	}
	f := NewFile(t, d, it.ID, "/media/movies/Heat (1995)/Heat.mkv")
	got, err := media.NewStore(d).FindByFilePath(context.Background(), f.Path)
	if err != nil || got.ID != it.ID {
		t.Fatalf("file lookup %+v %v", got, err)
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `s.FindByExternalID undefined` and missing package `udtest`

- [ ] **Step 4: Implement**

`server/internal/media/lookup.go`:
```go
package media

import (
	"context"
	"strings"
)

// Lookups used by plan 1F (home rows and Plex import matching).

// FindByExternalID returns items with provider=key and value=value, any kind.
func (s *Store) FindByExternalID(ctx context.Context, key, value string) ([]Item, error) {
	return s.queryItems(ctx, `id IN (SELECT item_id FROM item_external_ids WHERE provider = ? AND value = ?)
		ORDER BY id`, key, value)
}

// FindByFilePath returns the item owning the file at path, or ErrNotFound.
func (s *Store) FindByFilePath(ctx context.Context, path string) (Item, error) {
	items, err := s.queryItems(ctx, `id IN (SELECT item_id FROM files WHERE path = ?)`, path)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, ErrNotFound
	}
	return items[0], nil
}

// FindByTitleYear matches Title or OriginalTitle case-insensitively (ASCII).
// year 0 matches any year.
func (s *Store) FindByTitleYear(ctx context.Context, kind Kind, title string, year int) ([]Item, error) {
	t := strings.TrimSpace(title)
	return s.queryItems(ctx, `kind = ? AND (title = ? COLLATE NOCASE OR original_title = ? COLLATE NOCASE)
		AND (? = 0 OR year = ?) ORDER BY id`, string(kind), t, t, year, year)
}

// RecentlyAdded returns a library's top-level, non-extra items, newest first.
// An item's age is the newest added_at among it, its children and grandchildren,
// so a show with a new episode counts as new. (added_at is RFC3339Nano text;
// ties within one second may order arbitrarily.)
func (s *Store) RecentlyAdded(ctx context.Context, libraryID int64, limit int) ([]Item, error) {
	return s.queryItems(ctx, `library_id = ? AND parent_id IS NULL AND extra_type = ''
		ORDER BY (SELECT max(d.added_at) FROM items d
		          WHERE d.id = items.id OR d.parent_id = items.id
		             OR d.parent_id IN (SELECT c.id FROM items c WHERE c.parent_id = items.id)) DESC, id DESC
		LIMIT ?`, libraryID, limit)
}
```

`server/internal/userdata/udtest/udtest.go`:
```go
// Package udtest seeds profiles, libraries, items and files for userdata and
// importer tests. Test-only: never import it from non-test code.
package udtest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

var seq atomic.Int64

// NewProfile inserts a non-admin user with a primary profile, using the same
// columns as plan 1B's own test harness.
func NewProfile(t testing.TB, d *db.DB, name string) (userID, profileID int64) {
	t.Helper()
	now := time.Now().Unix()
	res, err := d.Exec(`INSERT INTO users(display_name, is_admin, created_at, updated_at) VALUES(?, 0, ?, ?)`, name, now, now)
	if err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	userID, _ = res.LastInsertId()
	res, err = d.Exec(`INSERT INTO profiles(user_id, name, is_primary, created_at, updated_at) VALUES(?, ?, 1, ?, ?)`,
		userID, name, now, now)
	if err != nil {
		t.Fatalf("seed profile %s: %v", name, err)
	}
	profileID, _ = res.LastInsertId()
	return userID, profileID
}

// NewLibrary creates a library and returns its ID.
func NewLibrary(t testing.TB, d *db.DB, name string, typ library.Type) int64 {
	t.Helper()
	lib, err := library.NewStore(d).Create(context.Background(), library.Library{
		Name: name, Type: typ, Paths: []string{fmt.Sprintf("/media/udtest%d", seq.Add(1))}})
	if err != nil {
		t.Fatalf("seed library %s: %v", name, err)
	}
	return lib.ID
}

// NewItem inserts it (with ExternalIDs) and returns it as stored.
func NewItem(t testing.TB, d *db.DB, it media.Item) media.Item {
	t.Helper()
	ctx := context.Background()
	s := media.NewStore(d)
	id, err := s.UpsertItem(ctx, it.LibraryID, fmt.Sprintf("udtest:%d", seq.Add(1)), it)
	if err != nil {
		t.Fatalf("seed item %s: %v", it.Title, err)
	}
	if len(it.ExternalIDs) > 0 {
		if err := s.SetExternalIDs(ctx, id, it.ExternalIDs, true); err != nil {
			t.Fatalf("seed external ids: %v", err)
		}
	}
	got, err := s.GetItem(ctx, id)
	if err != nil {
		t.Fatalf("read seeded item: %v", err)
	}
	return got
}

// NewFile attaches a file row (no file on disk) to an item.
func NewFile(t testing.TB, d *db.DB, itemID int64, path string) media.File {
	t.Helper()
	ctx := context.Background()
	s := media.NewStore(d)
	it, err := s.GetItem(ctx, itemID)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	f := media.File{ItemID: itemID, Path: path, Size: 1, ModTime: time.Unix(1_700_000_000, 0)}
	if f.ID, err = s.PutFile(ctx, it.LibraryID, f); err != nil {
		t.Fatalf("seed file %s: %v", path, err)
	}
	return f
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/media/lookup.go server/internal/media/lookup_test.go server/internal/userdata/udtest
git commit -m "feat(media): lookups by external ID, file path, title and recency; userdata test seeding"
```

---

### Task 1: Progress rules (pure, settings-driven)

**Files:**
- Create: `server/internal/userdata/rules.go`
- Test: `server/internal/userdata/rules_test.go`

**Interfaces:**
- Consumes: nothing (a `SettingsReader` interface that `*db.DB` satisfies via `GetSetting`)
- Produces: `userdata.Rules{WatchedPercent, CreditsLeadMs, ResumeMinMs, ResumeMaxPercent, NextUpWindow}`,
  `DefaultRules() Rules`, `Progress{PositionMs, DurationMs, CreditsStartMs}`,
  `(Rules) Complete(Progress) bool`, `(Rules) ResumePositionMs(Progress) int64`, `(Rules) Validate() error`,
  `LoadRules(ctx, SettingsReader) (Rules, error)`, setting keys `userdata.watched_percent`,
  `userdata.credits_lead_seconds`, `userdata.resume_min_seconds`, `userdata.resume_max_percent`, `userdata.next_up_days`.

- [ ] **Step 1: Write the failing tests**

`server/internal/userdata/rules_test.go`:
```go
package userdata

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCompleteAndResume(t *testing.T) {
	r := DefaultRules()
	const min = 60_000
	cases := []struct {
		name     string
		p        Progress
		complete bool
		resume   int64
	}{
		{"start, under 2 min", Progress{PositionMs: 90_000, DurationMs: 100 * min}, false, 0},
		{"exactly 2 min", Progress{PositionMs: 2 * min, DurationMs: 100 * min}, false, 2 * min},
		{"middle", Progress{PositionMs: 50 * min, DurationMs: 100 * min}, false, 50 * min},
		{"89 percent", Progress{PositionMs: 89 * min, DurationMs: 100 * min}, false, 89 * min},
		{"90 percent", Progress{PositionMs: 90 * min, DurationMs: 100 * min}, true, 0},
		{"credits known, reached", Progress{PositionMs: 81 * min, DurationMs: 100 * min, CreditsStartMs: 80 * min}, true, 0},
		{"credits known, before", Progress{PositionMs: 79 * min, DurationMs: 100 * min, CreditsStartMs: 80 * min}, false, 79 * min},
		{"credits marker past end ignored", Progress{PositionMs: 50 * min, DurationMs: 100 * min, CreditsStartMs: 120 * min}, false, 50 * min},
		{"no duration", Progress{PositionMs: 50 * min}, false, 50 * min},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.Complete(c.p); got != c.complete {
				t.Fatalf("Complete = %v, want %v", got, c.complete)
			}
			if got := r.ResumePositionMs(c.p); got != c.resume {
				t.Fatalf("ResumePositionMs = %d, want %d", got, c.resume)
			}
		})
	}
}

func TestCreditsLead(t *testing.T) {
	r := DefaultRules()
	r.CreditsLeadMs = 2 * 60_000
	p := Progress{PositionMs: 78*60_000 + 1, DurationMs: 100 * 60_000, CreditsStartMs: 80 * 60_000}
	if !r.Complete(p) {
		t.Fatal("position within lead of credits should complete")
	}
}

func TestResumeMaxPercentBelowWatched(t *testing.T) {
	r := DefaultRules()
	r.WatchedPercent = 0.95
	r.ResumeMaxPercent = 0.90
	p := Progress{PositionMs: 92 * 60_000, DurationMs: 100 * 60_000}
	if r.Complete(p) || r.ResumePositionMs(p) != 0 {
		t.Fatal("92% should be neither complete nor resumable")
	}
}

type mapSettings map[string]string

func (m mapSettings) GetSetting(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}

func TestLoadRules(t *testing.T) {
	r, err := LoadRules(context.Background(), mapSettings{})
	if err != nil || r != DefaultRules() {
		t.Fatalf("defaults: %+v %v", r, err)
	}
	r, err = LoadRules(context.Background(), mapSettings{
		SettingWatchedPercent:     "0.95",
		SettingCreditsLeadSeconds: "30",
		SettingResumeMinSeconds:   "60",
		SettingResumeMaxPercent:   "0.9",
		SettingNextUpDays:         "30",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Rules{WatchedPercent: 0.95, CreditsLeadMs: 30_000, ResumeMinMs: 60_000, ResumeMaxPercent: 0.9, NextUpWindow: 30 * 24 * time.Hour}
	if r != want {
		t.Fatalf("got %+v want %+v", r, want)
	}
	for key, bad := range map[string]string{
		SettingWatchedPercent:   "1.5",
		SettingResumeMinSeconds: "-1",
		SettingNextUpDays:       "abc",
	} {
		_, err := LoadRules(context.Background(), mapSettings{key: bad})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s=%s: want error naming key, got %v", key, bad, err)
		}
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: DefaultRules`

- [ ] **Step 3: Implement**

`server/internal/userdata/rules.go`:
```go
// Package userdata stores per-profile watch state, history, watchlist, ratings,
// per-show track choices and home-row layout, and builds the personal home rows.
package userdata

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Rules decide when an item counts as watched and which positions are resumable.
type Rules struct {
	WatchedPercent   float64       // complete at >= this fraction of duration (default 0.90)
	CreditsLeadMs    int64         // complete this long before a known credits marker (default 0)
	ResumeMinMs      int64         // positions below this are not resumable (default 2 min)
	ResumeMaxPercent float64       // positions above this fraction are not resumable (default 0.90)
	NextUpWindow     time.Duration // shows watched within this window appear in Next Up (default 90 days)
}

// DefaultRules returns the built-in rules.
func DefaultRules() Rules {
	return Rules{
		WatchedPercent:   0.90,
		CreditsLeadMs:    0,
		ResumeMinMs:      2 * 60 * 1000,
		ResumeMaxPercent: 0.90,
		NextUpWindow:     90 * 24 * time.Hour,
	}
}

// Progress is one playback position report.
type Progress struct {
	PositionMs     int64
	DurationMs     int64
	CreditsStartMs int64 // 0 = unknown (credits detection arrives in phase 2)
}

// Complete reports whether the position counts as having watched the item.
func (r Rules) Complete(p Progress) bool {
	if p.DurationMs <= 0 {
		return false
	}
	if p.CreditsStartMs > 0 && p.CreditsStartMs < p.DurationMs &&
		p.PositionMs >= p.CreditsStartMs-r.CreditsLeadMs {
		return true
	}
	return float64(p.PositionMs) >= r.WatchedPercent*float64(p.DurationMs)
}

// ResumePositionMs is the position to offer "Resume" from, or 0 when the item
// should start from the beginning (too early, or effectively finished).
func (r Rules) ResumePositionMs(p Progress) int64 {
	if p.PositionMs < r.ResumeMinMs || r.Complete(p) {
		return 0
	}
	if p.DurationMs > 0 && float64(p.PositionMs) > r.ResumeMaxPercent*float64(p.DurationMs) {
		return 0
	}
	return p.PositionMs
}

// Validate checks the rules are usable.
func (r Rules) Validate() error {
	switch {
	case !(r.WatchedPercent > 0 && r.WatchedPercent <= 1):
		return fmt.Errorf("%s must be in (0,1], got %v", SettingWatchedPercent, r.WatchedPercent)
	case !(r.ResumeMaxPercent > 0 && r.ResumeMaxPercent <= 1):
		return fmt.Errorf("%s must be in (0,1], got %v", SettingResumeMaxPercent, r.ResumeMaxPercent)
	case r.CreditsLeadMs < 0:
		return fmt.Errorf("%s must be >= 0", SettingCreditsLeadSeconds)
	case r.ResumeMinMs < 0:
		return fmt.Errorf("%s must be >= 0", SettingResumeMinSeconds)
	case r.NextUpWindow <= 0:
		return fmt.Errorf("%s must be > 0", SettingNextUpDays)
	}
	return nil
}

// Server setting keys (server_settings table) that override DefaultRules.
const (
	SettingWatchedPercent     = "userdata.watched_percent"      // e.g. "0.9"
	SettingCreditsLeadSeconds = "userdata.credits_lead_seconds" // e.g. "60"
	SettingResumeMinSeconds   = "userdata.resume_min_seconds"   // e.g. "120"
	SettingResumeMaxPercent   = "userdata.resume_max_percent"   // e.g. "0.9"
	SettingNextUpDays         = "userdata.next_up_days"         // e.g. "90"
)

// SettingsReader is satisfied by *db.DB.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, bool, error)
}

// LoadRules returns DefaultRules with any server-setting overrides applied.
func LoadRules(ctx context.Context, s SettingsReader) (Rules, error) {
	r := DefaultRules()
	float := func(key string, dst *float64) error {
		v, ok, err := s.GetSetting(ctx, key)
		if err != nil || !ok {
			return err
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = f
		return nil
	}
	integer := func(key string, apply func(int64)) error {
		v, ok, err := s.GetSetting(ctx, key)
		if err != nil || !ok {
			return err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if n < 0 {
			return fmt.Errorf("%s must be >= 0, got %d", key, n)
		}
		apply(n)
		return nil
	}
	steps := []func() error{
		func() error { return float(SettingWatchedPercent, &r.WatchedPercent) },
		func() error { return float(SettingResumeMaxPercent, &r.ResumeMaxPercent) },
		func() error { return integer(SettingCreditsLeadSeconds, func(n int64) { r.CreditsLeadMs = n * 1000 }) },
		func() error { return integer(SettingResumeMinSeconds, func(n int64) { r.ResumeMinMs = n * 1000 }) },
		func() error {
			return integer(SettingNextUpDays, func(n int64) { r.NextUpWindow = time.Duration(n) * 24 * time.Hour })
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return Rules{}, err
		}
	}
	if err := r.Validate(); err != nil {
		return Rules{}, err
	}
	return r, nil
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`
Expected: `ok  .../internal/userdata`

- [ ] **Step 5: Commit**

```bash
git add server/internal/userdata/rules.go server/internal/userdata/rules_test.go
git commit -m "feat(userdata): watched and resume rules with server-setting overrides"
```

---

### Task 2: State tables and the contract Store (Get, Put, GetMany)

**Files:**
- Create: `server/internal/db/migrations/20261025140000_userdata_state.sql`
- Create: `server/internal/userdata/kinds.go`, `server/internal/userdata/store.go`
- Test: `server/internal/userdata/fixture_test.go`, `server/internal/userdata/store_test.go`

**Interfaces:**
- Consumes: `db.DB`, `dbtest.Open`, `udtest.NewProfile/NewLibrary/NewItem` (Task 0),
  `media.NewStore`, `media.Item`, `library.Type`
- Produces: `userdata.State` (contract + `DurationMs`), `ErrInvalid`, `NewStore(d) *Store`,
  `(*Store) Get/Put` (contract), `(*Store) GetMany(ctx, profileID, []int64) (map[int64]State, error)`,
  `SetClock(func() time.Time)`, `SetRules(Rules)`, `Rules() Rules`, `Now() time.Time`.
  Unexported helpers used by later tasks: `querier`, `getRaw`, `putRaw`, `scanState`,
  `listStates`, `placeholders`, `boolInt`, `msOrNull`. Kind constants `kindMovie`,
  `kindShow`, `kindSeason`, `kindEpisode`, `kindVideo`, `IsPlayable(media.Kind) bool`.
  Test fixture `newFixture(t) *fixture` (fields below) used by every later userdata test.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/20261025140000_userdata_state.sql`:
```sql
-- Per-profile watch state. position_ms is the resume point (0 = start over);
-- last_position_ms is the raw last reported position, used to count a play once.
CREATE TABLE user_item_state (
    profile_id           INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    item_id              INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position_ms          INTEGER NOT NULL DEFAULT 0 CHECK (position_ms >= 0),
    last_position_ms     INTEGER NOT NULL DEFAULT 0 CHECK (last_position_ms >= 0),
    duration_ms          INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    watched              INTEGER NOT NULL DEFAULT 0 CHECK (watched IN (0, 1)),
    play_count           INTEGER NOT NULL DEFAULT 0 CHECK (play_count >= 0),
    last_played_ms       INTEGER,
    hidden_from_continue INTEGER NOT NULL DEFAULT 0 CHECK (hidden_from_continue IN (0, 1)),
    updated_ms           INTEGER NOT NULL,
    PRIMARY KEY (profile_id, item_id)
) STRICT;

CREATE INDEX user_item_state_last_played ON user_item_state (profile_id, last_played_ms DESC);
CREATE INDEX user_item_state_item ON user_item_state (item_id);

-- One row per finished play (or imported Plex play).
CREATE TABLE watch_history (
    id         INTEGER PRIMARY KEY,
    profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    watched_ms INTEGER NOT NULL,
    source     TEXT NOT NULL CHECK (source IN ('lisimflix', 'plex')),
    UNIQUE (profile_id, item_id, watched_ms, source)
) STRICT;

CREATE INDEX watch_history_profile ON watch_history (profile_id, watched_ms DESC, id DESC);
```

- [ ] **Step 2: Write the test fixture and failing tests**

`server/internal/userdata/fixture_test.go`:
```go
package userdata

import (
	"fmt"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/userdata/udtest"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// fixture: one profile, a Movies library with The Matrix, and a TV library with
// Firefly: Specials (S00E01), Season 1 (E01-E03), Season 2 (E01-E02).
type fixture struct {
	d                 *db.DB
	store             *Store
	media             *media.Store
	clock             *fakeClock
	userID, profileID int64
	libMovies, libTV  int64
	movie, show       media.Item
	seasons           map[int]media.Item
	eps               map[string]media.Item // "S01E02"
}

func intp(i int) *int { return &i }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.Open(t)
	f := &fixture{
		d:       d,
		media:   media.NewStore(d),
		clock:   &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)},
		seasons: map[int]media.Item{},
		eps:     map[string]media.Item{},
	}
	f.store = NewStore(d)
	f.store.SetClock(f.clock.Now)
	f.userID, f.profileID = udtest.NewProfile(t, d, "zac")
	f.libMovies = udtest.NewLibrary(t, d, "Movies", library.Type("movies"))
	f.libTV = udtest.NewLibrary(t, d, "TV Shows", library.Type("tv"))
	f.movie = udtest.NewItem(t, d, media.Item{LibraryID: f.libMovies, Kind: kindMovie,
		Title: "The Matrix", Year: 1999, ExternalIDs: map[string]string{"tmdb": "603"}})
	f.show = udtest.NewItem(t, d, media.Item{LibraryID: f.libTV, Kind: kindShow, Title: "Firefly", Year: 2002})
	for _, sn := range []int{0, 1, 2} {
		f.seasons[sn] = udtest.NewItem(t, d, media.Item{LibraryID: f.libTV, ParentID: f.show.ID,
			Kind: kindSeason, Title: fmt.Sprintf("Season %d", sn), SeasonNumber: intp(sn)})
	}
	for _, se := range [][2]int{{0, 1}, {1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}} {
		key := fmt.Sprintf("S%02dE%02d", se[0], se[1])
		f.eps[key] = udtest.NewItem(t, d, media.Item{LibraryID: f.libTV, ParentID: f.seasons[se[0]].ID,
			Kind: kindEpisode, Title: "Episode " + key, SeasonNumber: intp(se[0]), EpisodeNumber: intp(se[1])})
	}
	return f
}
```

`server/internal/userdata/store_test.go`:
```go
package userdata

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGetMissingReturnsZeroState(t *testing.T) {
	f := newFixture(t)
	st, err := f.store.Get(context.Background(), f.profileID, f.movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st != (State{ProfileID: f.profileID, ItemID: f.movie.ID}) {
		t.Fatalf("got %+v", st)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	want := State{
		ProfileID: f.profileID, ItemID: f.movie.ID, PositionMs: 120_500, DurationMs: 8_160_000,
		Watched: true, PlayCount: 3, LastPlayed: time.Date(2026, 10, 1, 20, 30, 0, 123_000_000, time.UTC),
		HiddenFromContinue: true,
	}
	if err := f.store.Put(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.Get(ctx, f.profileID, f.movie.ID)
	if err != nil || got != want {
		t.Fatalf("got %+v err=%v, want %+v", got, err, want)
	}
	want.PlayCount = 4
	if err := f.store.Put(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(ctx, f.profileID, f.movie.ID); got.PlayCount != 4 {
		t.Fatalf("upsert failed: %+v", got)
	}
}

func TestPutRejectsInvalid(t *testing.T) {
	f := newFixture(t)
	bad := []State{
		{ProfileID: 0, ItemID: f.movie.ID},
		{ProfileID: f.profileID, ItemID: f.movie.ID, PositionMs: -1},
		{ProfileID: f.profileID, ItemID: f.movie.ID, PositionMs: 10, DurationMs: 5},
		{ProfileID: f.profileID, ItemID: f.movie.ID, PlayCount: -1},
	}
	for _, st := range bad {
		if err := f.store.Put(context.Background(), st); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: err = %v, want ErrInvalid", st, err)
		}
	}
}

func TestGetMany(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, b := f.eps["S01E01"].ID, f.eps["S01E02"].ID
	f.store.Put(ctx, State{ProfileID: f.profileID, ItemID: a, Watched: true, PlayCount: 1})
	got, err := f.store.GetMany(ctx, f.profileID, []int64{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[a].Watched {
		t.Fatalf("got %+v", got)
	}
	if empty, err := f.store.GetMany(ctx, f.profileID, nil); err != nil || len(empty) != 0 {
		t.Fatalf("nil ids: %v %v", empty, err)
	}
}

func TestStateCascadesOnItemAndProfileDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.store.Put(ctx, State{ProfileID: f.profileID, ItemID: f.movie.ID, PlayCount: 1})
	f.store.Put(ctx, State{ProfileID: f.profileID, ItemID: f.eps["S01E01"].ID, PlayCount: 1})
	count := func() int {
		var n int
		f.d.QueryRow(`SELECT count(*) FROM user_item_state`).Scan(&n)
		return n
	}
	if _, err := f.d.Exec(`DELETE FROM items WHERE id = ?`, f.movie.ID); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("item delete did not cascade: %d rows", count())
	}
	if _, err := f.d.Exec(`DELETE FROM profiles WHERE id = ?`, f.profileID); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("profile delete did not cascade: %d rows", count())
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewStore` (and `undefined: kindMovie`)

- [ ] **Step 4: Implement**

`server/internal/userdata/kinds.go`:
```go
package userdata

import "github.com/Lisimflix/lisimflix/server/internal/media"

const (
	kindMovie   = media.KindMovie
	kindShow    = media.KindShow
	kindSeason  = media.KindSeason
	kindEpisode = media.KindEpisode
	kindVideo   = media.KindVideo
)

// IsPlayable reports whether items of kind k have files and progress
// (movies, episodes, home videos), as opposed to containers (shows, seasons).
func IsPlayable(k media.Kind) bool {
	return k == kindMovie || k == kindEpisode || k == kindVideo
}
```

`server/internal/userdata/store.go`:
```go
package userdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db"
)

// ErrInvalid marks bad input (bad IDs, out-of-range positions or ratings, bad keys).
var ErrInvalid = errors.New("invalid input")

// State is one profile's watch state for one item (index contract, plus DurationMs).
type State struct {
	ProfileID          int64
	ItemID             int64
	PositionMs         int64 // resume point; 0 = start from the beginning
	DurationMs         int64 // last reported duration; 0 = unknown
	Watched            bool
	PlayCount          int
	LastPlayed         time.Time // zero = never played
	HiddenFromContinue bool
}

// Store reads and writes the userdata tables.
type Store struct {
	d     *db.DB
	now   func() time.Time
	rules Rules
}

// NewStore returns a Store using DefaultRules and the wall clock.
func NewStore(d *db.DB) *Store {
	return &Store{d: d, now: time.Now, rules: DefaultRules()}
}

// SetClock replaces the clock (tests).
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// SetRules replaces the progress rules (from LoadRules at startup).
func (s *Store) SetRules(r Rules) { s.rules = r }

// Rules returns the active progress rules.
func (s *Store) Rules() Rules { return s.rules }

// Now returns the store's current time.
func (s *Store) Now() time.Time { return s.now() }

// querier is satisfied by *db.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type rowScanner interface{ Scan(dest ...any) error }

const stateCols = `profile_id, item_id, position_ms, duration_ms, watched, play_count, last_played_ms, hidden_from_continue`

func scanState(sc rowScanner, extra ...any) (State, error) {
	var st State
	var watched, hidden int64
	var last sql.NullInt64
	dest := append([]any{&st.ProfileID, &st.ItemID, &st.PositionMs, &st.DurationMs,
		&watched, &st.PlayCount, &last, &hidden}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return State{}, err
	}
	st.Watched = watched == 1
	st.HiddenFromContinue = hidden == 1
	if last.Valid {
		st.LastPlayed = time.UnixMilli(last.Int64).UTC()
	}
	return st, nil
}

// Get returns the state, or a zero State (IDs set) when the profile never touched the item.
func (s *Store) Get(ctx context.Context, profileID, itemID int64) (State, error) {
	st, _, err := getRaw(ctx, s.d, profileID, itemID)
	return st, err
}

// getRaw also returns last_position_ms.
func getRaw(ctx context.Context, q querier, profileID, itemID int64) (State, int64, error) {
	var lastPos int64
	row := q.QueryRowContext(ctx, `SELECT `+stateCols+`, last_position_ms
		FROM user_item_state WHERE profile_id = ? AND item_id = ?`, profileID, itemID)
	st, err := scanState(row, &lastPos)
	if errors.Is(err, sql.ErrNoRows) {
		return State{ProfileID: profileID, ItemID: itemID}, 0, nil
	}
	if err != nil {
		return State{}, 0, fmt.Errorf("get state %d/%d: %w", profileID, itemID, err)
	}
	return st, lastPos, nil
}

// Put stores st as-is (after validation). Progress reports should use RecordProgress.
func (s *Store) Put(ctx context.Context, st State) error {
	if err := validateState(st); err != nil {
		return err
	}
	return putRaw(ctx, s.d, st, st.PositionMs, s.now())
}

func validateState(st State) error {
	switch {
	case st.ProfileID <= 0 || st.ItemID <= 0:
		return fmt.Errorf("%w: profile and item IDs are required", ErrInvalid)
	case st.PositionMs < 0 || st.DurationMs < 0 || st.PlayCount < 0:
		return fmt.Errorf("%w: negative position, duration or play count", ErrInvalid)
	case st.DurationMs > 0 && st.PositionMs > st.DurationMs:
		return fmt.Errorf("%w: position beyond duration", ErrInvalid)
	}
	return nil
}

func putRaw(ctx context.Context, q querier, st State, lastPositionMs int64, now time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO user_item_state
		(`+stateCols+`, last_position_ms, updated_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (profile_id, item_id) DO UPDATE SET
			position_ms = excluded.position_ms,
			duration_ms = excluded.duration_ms,
			watched = excluded.watched,
			play_count = excluded.play_count,
			last_played_ms = excluded.last_played_ms,
			hidden_from_continue = excluded.hidden_from_continue,
			last_position_ms = excluded.last_position_ms,
			updated_ms = excluded.updated_ms`,
		st.ProfileID, st.ItemID, st.PositionMs, st.DurationMs, boolInt(st.Watched), st.PlayCount,
		msOrNull(st.LastPlayed), boolInt(st.HiddenFromContinue), lastPositionMs, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("put state %d/%d: %w", st.ProfileID, st.ItemID, err)
	}
	return nil
}

// GetMany returns stored states keyed by item ID. Items without a row are absent.
func (s *Store) GetMany(ctx context.Context, profileID int64, itemIDs []int64) (map[int64]State, error) {
	out := make(map[int64]State, len(itemIDs))
	for start := 0; start < len(itemIDs); start += 500 {
		chunk := itemIDs[start:min(start+500, len(itemIDs))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, profileID)
		for _, id := range chunk {
			args = append(args, id)
		}
		states, err := s.listStates(ctx, `WHERE profile_id = ? AND item_id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for _, st := range states {
			out[st.ItemID] = st
		}
	}
	return out, nil
}

// listStates runs SELECT stateCols FROM user_item_state <where> with args.
func (s *Store) listStates(ctx context.Context, where string, args ...any) ([]State, error) {
	rows, err := s.d.QueryContext(ctx, `SELECT `+stateCols+` FROM user_item_state `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list states: %w", err)
	}
	defer rows.Close()
	var out []State
	for rows.Next() {
		st, err := scanState(rows)
		if err != nil {
			return nil, fmt.Errorf("scan state: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func msOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`
Expected: `ok  .../internal/userdata`. If `TestStateCascadesOnItemAndProfileDelete` fails with
a foreign-key error from *another* table (e.g. `files`, `sessions`), that table lacks
`ON DELETE CASCADE`. Raise it with the owning plan rather than changing their migration.

- [ ] **Step 6: Commit**

```bash
git add server/internal/db/migrations/20261025140000_userdata_state.sql server/internal/userdata
git commit -m "feat(userdata): user_item_state and watch_history tables with contract Store"
```

---

### Task 3: Recording progress, marking watched, Continue Watching

**Files:**
- Create: `server/internal/userdata/progress.go`
- Test: `server/internal/userdata/progress_test.go`

**Interfaces:**
- Consumes: `Store`, `getRaw`, `putRaw`, `listStates`, `Rules` (Tasks 1–2)
- Produces:
  - `CreditsSource` interface `CreditsStartMs(ctx, itemID int64) (int64, bool, error)`, `NoCredits{}`
  - `(*Store) RecordProgress(ctx, profileID, itemID, positionMs, durationMs, creditsStartMs int64) (State, error)`.
    It counts a play (PlayCount+1, Watched, history row) only when a report crosses the
    completion threshold. It clears `HiddenFromContinue` and sets `LastPlayed`.
  - `(*Store) SetWatched(ctx, profileID int64, itemIDs []int64, watched bool) error` (no history rows)
  - `(*Store) HideFromContinue(ctx, profileID, itemID int64) error`
  - `(*Store) InProgress(ctx, profileID int64, limit int) ([]State, error)`, newest first
  - `(*Store) RecentlyWatched(ctx, profileID int64, since time.Time, limit int) ([]State, error)`, newest first
  - `addHistory(ctx, q querier, profileID, itemID int64, at time.Time, source string) (bool, error)`
  - Test helper `(*fixture) watch(t, itemID)` (marks watched via a 95% progress report, then advances the clock 1 min)

- [ ] **Step 1: Write the failing tests**

`server/internal/userdata/progress_test.go`:
```go
package userdata

import (
	"context"
	"errors"
	"testing"
	"time"
)

const testDurationMs = 6_000_000 // 100 minutes

// watch marks itemID watched through a 95% progress report and advances the clock.
func (f *fixture) watch(t *testing.T, itemID int64) {
	t.Helper()
	if _, err := f.store.RecordProgress(context.Background(), f.profileID, itemID, 5_700_000, testDurationMs, 0); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
}

func historyCount(t *testing.T, f *fixture, itemID int64) int {
	t.Helper()
	var n int
	if err := f.d.QueryRow(`SELECT count(*) FROM watch_history WHERE profile_id = ? AND item_id = ?`,
		f.profileID, itemID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordProgressEarlyIsNotResumable(t *testing.T) {
	f := newFixture(t)
	st, err := f.store.RecordProgress(context.Background(), f.profileID, f.movie.ID, 60_000, testDurationMs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.PositionMs != 0 || st.Watched || st.LastPlayed.IsZero() || st.DurationMs != testDurationMs {
		t.Fatalf("got %+v", st)
	}
	if ip, _ := f.store.InProgress(context.Background(), f.profileID, 10); len(ip) != 0 {
		t.Fatalf("in progress: %+v", ip)
	}
}

func TestRecordProgressMiddleIsInProgress(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	st, err := f.store.RecordProgress(ctx, f.profileID, f.movie.ID, 1_800_000, testDurationMs, 0)
	if err != nil || st.PositionMs != 1_800_000 || st.Watched {
		t.Fatalf("got %+v %v", st, err)
	}
	got, _ := f.store.Get(ctx, f.profileID, f.movie.ID)
	if got != st {
		t.Fatalf("stored %+v, returned %+v", got, st)
	}
	ip, _ := f.store.InProgress(ctx, f.profileID, 10)
	if len(ip) != 1 || ip[0].ItemID != f.movie.ID {
		t.Fatalf("in progress: %+v", ip)
	}
}

func TestCompletionCountsOncePerPlay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	report := func(pos int64) State {
		st, err := f.store.RecordProgress(ctx, f.profileID, f.movie.ID, pos, testDurationMs, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(10 * time.Second)
		return st
	}
	report(3_000_000)
	st := report(5_500_000) // 91.6%: crosses
	if !st.Watched || st.PlayCount != 1 || st.PositionMs != 0 {
		t.Fatalf("after crossing: %+v", st)
	}
	st = report(5_900_000) // still past threshold: no double count
	if st.PlayCount != 1 || historyCount(t, f, f.movie.ID) != 1 {
		t.Fatalf("double counted: %+v", st)
	}
	report(600_000) // rewatch from the start: stays watched
	st = report(5_800_000)
	if !st.Watched || st.PlayCount != 2 || historyCount(t, f, f.movie.ID) != 2 {
		t.Fatalf("rewatch: %+v history=%d", st, historyCount(t, f, f.movie.ID))
	}
}

func TestCreditsMarkerCompletesEarly(t *testing.T) {
	f := newFixture(t)
	st, err := f.store.RecordProgress(context.Background(), f.profileID, f.movie.ID, 4_900_000, testDurationMs, 4_800_000)
	if err != nil || !st.Watched || st.PlayCount != 1 {
		t.Fatalf("got %+v %v", st, err)
	}
}

func TestRecordProgressRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, c := range [][2]int64{{-1, 100}, {200, 100}, {10, 0}} {
		if _, err := f.store.RecordProgress(ctx, f.profileID, f.movie.ID, c[0], c[1], 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("pos=%d dur=%d: err=%v", c[0], c[1], err)
		}
	}
}

func TestHideFromContinueClearedByNextPlay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.store.RecordProgress(ctx, f.profileID, f.movie.ID, 1_800_000, testDurationMs, 0)
	if err := f.store.HideFromContinue(ctx, f.profileID, f.movie.ID); err != nil {
		t.Fatal(err)
	}
	if ip, _ := f.store.InProgress(ctx, f.profileID, 10); len(ip) != 0 {
		t.Fatalf("hidden item still listed: %+v", ip)
	}
	st, _ := f.store.Get(ctx, f.profileID, f.movie.ID)
	if st.PositionMs != 1_800_000 || !st.HiddenFromContinue {
		t.Fatalf("hide must keep the position: %+v", st)
	}
	f.store.RecordProgress(ctx, f.profileID, f.movie.ID, 1_900_000, testDurationMs, 0)
	if ip, _ := f.store.InProgress(ctx, f.profileID, 10); len(ip) != 1 {
		t.Fatal("playing again should un-hide")
	}
	// Hiding an item with no state creates a hidden row (used for Next Up dismissals).
	ep := f.eps["S01E02"].ID
	if err := f.store.HideFromContinue(ctx, f.profileID, ep); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.store.Get(ctx, f.profileID, ep); !st.HiddenFromContinue {
		t.Fatalf("got %+v", st)
	}
}

func TestInProgressNewestFirstAndLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, id := range []int64{f.movie.ID, f.eps["S01E01"].ID, f.eps["S01E02"].ID} {
		f.store.RecordProgress(ctx, f.profileID, id, 1_000_000, testDurationMs, 0)
		f.clock.Advance(time.Minute)
	}
	ip, err := f.store.InProgress(ctx, f.profileID, 2)
	if err != nil || len(ip) != 2 || ip[0].ItemID != f.eps["S01E02"].ID || ip[1].ItemID != f.eps["S01E01"].ID {
		t.Fatalf("got %+v %v", ip, err)
	}
}

func TestSetWatchedAndUnwatched(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ids := []int64{f.eps["S01E01"].ID, f.eps["S01E02"].ID}
	f.store.RecordProgress(ctx, f.profileID, ids[1], 1_000_000, testDurationMs, 0)
	if err := f.store.SetWatched(ctx, f.profileID, ids, true); err != nil {
		t.Fatal(err)
	}
	states, _ := f.store.GetMany(ctx, f.profileID, ids)
	for _, id := range ids {
		st := states[id]
		if !st.Watched || st.PlayCount != 1 || st.PositionMs != 0 || st.LastPlayed.IsZero() {
			t.Fatalf("watched: %+v", st)
		}
	}
	// Marking an already watched item again does not bump the count.
	f.store.SetWatched(ctx, f.profileID, ids[:1], true)
	if st, _ := f.store.Get(ctx, f.profileID, ids[0]); st.PlayCount != 1 {
		t.Fatalf("re-mark bumped count: %+v", st)
	}
	if historyCount(t, f, ids[0]) != 0 {
		t.Fatal("manual mark must not add history")
	}
	if err := f.store.SetWatched(ctx, f.profileID, ids, false); err != nil {
		t.Fatal(err)
	}
	states, _ = f.store.GetMany(ctx, f.profileID, ids)
	for _, id := range ids {
		if st := states[id]; st.Watched || st.PlayCount != 0 || st.PositionMs != 0 {
			t.Fatalf("unwatched: %+v", st)
		}
	}
}

func TestRecentlyWatchedWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.watch(t, f.eps["S01E01"].ID)
	f.clock.Advance(48 * time.Hour)
	f.watch(t, f.eps["S01E02"].ID)
	got, err := f.store.RecentlyWatched(ctx, f.profileID, f.clock.Now().Add(-24*time.Hour), 10)
	if err != nil || len(got) != 1 || got[0].ItemID != f.eps["S01E02"].ID {
		t.Fatalf("got %+v %v", got, err)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `f.store.RecordProgress undefined`

- [ ] **Step 3: Implement**

`server/internal/userdata/progress.go`:
```go
package userdata

import (
	"context"
	"fmt"
	"time"
)

// CreditsSource reports where end credits start for an item. The phase 2
// trickplay/intro-detection module implements it; NoCredits is used until then.
type CreditsSource interface {
	CreditsStartMs(ctx context.Context, itemID int64) (startMs int64, ok bool, err error)
}

// NoCredits knows no credits markers.
type NoCredits struct{}

func (NoCredits) CreditsStartMs(context.Context, int64) (int64, bool, error) { return 0, false, nil }

// RecordProgress applies one position report from a player. creditsStartMs is 0
// when unknown. A play is counted (PlayCount+1, Watched, one history row) only
// when this report crosses the completion threshold and the previous one had
// not, so repeated reports near the end count once.
func (s *Store) RecordProgress(ctx context.Context, profileID, itemID, positionMs, durationMs, creditsStartMs int64) (State, error) {
	if profileID <= 0 || itemID <= 0 || durationMs <= 0 || positionMs < 0 || positionMs > durationMs {
		return State{}, fmt.Errorf("%w: need 0 <= positionMs <= durationMs and durationMs > 0", ErrInvalid)
	}
	tx, err := s.d.BeginTx(ctx, nil)
	if err != nil {
		return State{}, fmt.Errorf("record progress: begin: %w", err)
	}
	defer tx.Rollback()

	prev, prevLastPos, err := getRaw(ctx, tx, profileID, itemID)
	if err != nil {
		return State{}, err
	}
	now := s.now()
	cur := Progress{PositionMs: positionMs, DurationMs: durationMs, CreditsStartMs: creditsStartMs}
	before := Progress{PositionMs: prevLastPos, DurationMs: prev.DurationMs, CreditsStartMs: creditsStartMs}

	st := prev
	st.DurationMs = durationMs
	st.PositionMs = s.rules.ResumePositionMs(cur)
	st.LastPlayed = now
	st.HiddenFromContinue = false
	if s.rules.Complete(cur) && !s.rules.Complete(before) {
		st.Watched = true
		st.PlayCount++
		if _, err := addHistory(ctx, tx, profileID, itemID, now, "lisimflix"); err != nil {
			return State{}, err
		}
	}
	if err := putRaw(ctx, tx, st, positionMs, now); err != nil {
		return State{}, err
	}
	if err := tx.Commit(); err != nil {
		return State{}, fmt.Errorf("record progress: commit: %w", err)
	}
	return st, nil
}

// addHistory inserts one history row; duplicates (same instant and source) are ignored.
func addHistory(ctx context.Context, q querier, profileID, itemID int64, at time.Time, source string) (bool, error) {
	res, err := q.ExecContext(ctx, `INSERT INTO watch_history (profile_id, item_id, watched_ms, source)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, profileID, itemID, at.UnixMilli(), source)
	if err != nil {
		return false, fmt.Errorf("add history %d/%d: %w", profileID, itemID, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetWatched marks playable items watched or unwatched (callers expand shows and
// seasons with Leaves). Watched: play count +1 if it was not watched, position
// reset. Unwatched: play count 0, position reset. No history rows are written.
func (s *Store) SetWatched(ctx context.Context, profileID int64, itemIDs []int64, watched bool) error {
	if profileID <= 0 {
		return fmt.Errorf("%w: profile ID required", ErrInvalid)
	}
	tx, err := s.d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set watched: begin: %w", err)
	}
	defer tx.Rollback()
	now := s.now()
	for _, id := range itemIDs {
		st, _, err := getRaw(ctx, tx, profileID, id)
		if err != nil {
			return err
		}
		if watched {
			if !st.Watched {
				st.PlayCount++
				st.LastPlayed = now
			}
			st.Watched = true
		} else {
			st.Watched = false
			st.PlayCount = 0
		}
		st.PositionMs = 0
		st.HiddenFromContinue = false
		if err := putRaw(ctx, tx, st, 0, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set watched: commit: %w", err)
	}
	return nil
}

// HideFromContinue removes an item from Continue Watching / Next Up until it is played again.
func (s *Store) HideFromContinue(ctx context.Context, profileID, itemID int64) error {
	_, err := s.d.ExecContext(ctx, `INSERT INTO user_item_state (profile_id, item_id, hidden_from_continue, updated_ms)
		VALUES (?, ?, 1, ?)
		ON CONFLICT (profile_id, item_id) DO UPDATE SET hidden_from_continue = 1, updated_ms = excluded.updated_ms`,
		profileID, itemID, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("hide from continue %d/%d: %w", profileID, itemID, err)
	}
	return nil
}

// InProgress returns resumable, non-hidden states, most recently played first.
func (s *Store) InProgress(ctx context.Context, profileID int64, limit int) ([]State, error) {
	return s.listStates(ctx, `WHERE profile_id = ? AND position_ms > 0 AND hidden_from_continue = 0
		ORDER BY last_played_ms DESC, item_id DESC LIMIT ?`, profileID, limit)
}

// RecentlyWatched returns watched states played at or after since, newest first.
func (s *Store) RecentlyWatched(ctx context.Context, profileID int64, since time.Time, limit int) ([]State, error) {
	return s.listStates(ctx, `WHERE profile_id = ? AND watched = 1 AND last_played_ms >= ?
		ORDER BY last_played_ms DESC, item_id DESC LIMIT ?`, profileID, since.UnixMilli(), limit)
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/userdata/progress.go server/internal/userdata/progress_test.go
git commit -m "feat(userdata): progress reports, play counting, mark watched, continue-watching queries"
```

---

### Task 4: Watchlist, personal ratings, history paging

**Files:**
- Create: `server/internal/db/migrations/20261025140100_userdata_lists.sql`
- Create: `server/internal/userdata/lists.go`
- Test: `server/internal/userdata/lists_test.go`

**Interfaces:**
- Consumes: `Store`, `addHistory`, `ErrInvalid` (Tasks 2–3)
- Produces:
  - `WatchlistEntry{ItemID int64; AddedAt time.Time}`; `(*Store) AddToWatchlist(ctx, profileID, itemID) error` (idempotent, keeps first added time),
    `RemoveFromWatchlist(ctx, profileID, itemID) error`, `Watchlist(ctx, profileID int64, limit, offset int) ([]WatchlistEntry, error)` (newest first)
  - `(*Store) SetRating(ctx, profileID, itemID int64, rating float64) error` (0–10), `Rating(ctx, profileID, itemID) (float64, bool, error)`, `DeleteRating(ctx, profileID, itemID) error`
  - `HistoryEntry{ID, ItemID int64; WatchedAt time.Time; Source string}`, `(HistoryEntry) Cursor() HistoryCursor`,
    `HistoryCursor{WatchedMs, ID int64}` with `String()` (`"<watchedMs>_<id>"`) and `ParseHistoryCursor(string) (HistoryCursor, error)`,
    `(*Store) History(ctx, profileID int64, before HistoryCursor, limit int) ([]HistoryEntry, error)` (newest first; zero cursor = from newest)

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/20261025140100_userdata_lists.sql`:
```sql
CREATE TABLE watchlist (
    profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    added_ms   INTEGER NOT NULL,
    PRIMARY KEY (profile_id, item_id)
) STRICT;

CREATE INDEX watchlist_recent ON watchlist (profile_id, added_ms DESC);

CREATE TABLE ratings (
    profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    rating     REAL NOT NULL CHECK (rating >= 0 AND rating <= 10),
    updated_ms INTEGER NOT NULL,
    PRIMARY KEY (profile_id, item_id)
) STRICT;
```

- [ ] **Step 2: Write the failing tests**

`server/internal/userdata/lists_test.go`:
```go
package userdata

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestWatchlist(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.AddToWatchlist(ctx, f.profileID, f.movie.ID); err != nil {
		t.Fatal(err)
	}
	first := f.clock.Now()
	f.clock.Advance(time.Hour)
	f.store.AddToWatchlist(ctx, f.profileID, f.show.ID)
	f.store.AddToWatchlist(ctx, f.profileID, f.movie.ID) // again: keeps original time
	got, err := f.store.Watchlist(ctx, f.profileID, 10, 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v %v", got, err)
	}
	if got[0].ItemID != f.show.ID || got[1].ItemID != f.movie.ID || !got[1].AddedAt.Equal(first) {
		t.Fatalf("order/time wrong: %+v", got)
	}
	page, _ := f.store.Watchlist(ctx, f.profileID, 1, 1)
	if len(page) != 1 || page[0].ItemID != f.movie.ID {
		t.Fatalf("offset page: %+v", page)
	}
	if err := f.store.RemoveFromWatchlist(ctx, f.profileID, f.movie.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Watchlist(ctx, f.profileID, 10, 0); len(got) != 1 {
		t.Fatalf("after remove: %+v", got)
	}
}

func TestRatings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, ok, err := f.store.Rating(ctx, f.profileID, f.movie.ID); ok || err != nil {
		t.Fatalf("unrated: ok=%v err=%v", ok, err)
	}
	if err := f.store.SetRating(ctx, f.profileID, f.movie.ID, 7.5); err != nil {
		t.Fatal(err)
	}
	f.store.SetRating(ctx, f.profileID, f.movie.ID, 8)
	if r, ok, _ := f.store.Rating(ctx, f.profileID, f.movie.ID); !ok || r != 8 {
		t.Fatalf("rating = %v ok=%v", r, ok)
	}
	for _, bad := range []float64{-0.5, 10.5, math.NaN()} {
		if err := f.store.SetRating(ctx, f.profileID, f.movie.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%v: err=%v", bad, err)
		}
	}
	f.store.DeleteRating(ctx, f.profileID, f.movie.ID)
	if _, ok, _ := f.store.Rating(ctx, f.profileID, f.movie.ID); ok {
		t.Fatal("rating not deleted")
	}
}

func TestHistoryPaging(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Five plays; two share a timestamp to exercise the id tie-break.
	times := []time.Time{base, base.Add(time.Hour), base.Add(time.Hour), base.Add(2 * time.Hour), base.Add(3 * time.Hour)}
	items := []int64{f.movie.ID, f.eps["S01E01"].ID, f.eps["S01E02"].ID, f.eps["S01E03"].ID, f.movie.ID}
	for i := range times {
		if _, err := addHistory(ctx, f.d, f.profileID, items[i], times[i], "lisimflix"); err != nil {
			t.Fatal(err)
		}
	}
	var all []HistoryEntry
	cur := HistoryCursor{}
	for page := 0; page < 5; page++ {
		got, err := f.store.History(ctx, f.profileID, cur, 2)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
		if len(got) < 2 {
			break
		}
		cur = got[len(got)-1].Cursor()
	}
	if len(all) != 5 {
		t.Fatalf("got %d entries: %+v", len(all), all)
	}
	for i := 1; i < len(all); i++ {
		if all[i].WatchedAt.After(all[i-1].WatchedAt) {
			t.Fatalf("not newest first: %+v", all)
		}
	}
	if all[0].ItemID != f.movie.ID || all[0].Source != "lisimflix" {
		t.Fatalf("first = %+v", all[0])
	}
	c, err := ParseHistoryCursor(all[2].Cursor().String())
	if err != nil || c != all[2].Cursor() {
		t.Fatalf("cursor round trip: %+v %v", c, err)
	}
	for _, bad := range []string{"x", "1_", "_2", "1_2_3", "-1_5"} {
		if _, err := ParseHistoryCursor(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q: err=%v", bad, err)
		}
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `f.store.AddToWatchlist undefined`

- [ ] **Step 4: Implement**

`server/internal/userdata/lists.go`:
```go
package userdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// WatchlistEntry is one item on a profile's watchlist.
type WatchlistEntry struct {
	ItemID  int64
	AddedAt time.Time
}

// AddToWatchlist adds an item; adding it again keeps the original time.
func (s *Store) AddToWatchlist(ctx context.Context, profileID, itemID int64) error {
	_, err := s.d.ExecContext(ctx, `INSERT INTO watchlist (profile_id, item_id, added_ms) VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING`, profileID, itemID, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("add to watchlist %d/%d: %w", profileID, itemID, err)
	}
	return nil
}

// RemoveFromWatchlist removes an item (no error if absent).
func (s *Store) RemoveFromWatchlist(ctx context.Context, profileID, itemID int64) error {
	if _, err := s.d.ExecContext(ctx, `DELETE FROM watchlist WHERE profile_id = ? AND item_id = ?`,
		profileID, itemID); err != nil {
		return fmt.Errorf("remove from watchlist %d/%d: %w", profileID, itemID, err)
	}
	return nil
}

// Watchlist returns entries newest first.
func (s *Store) Watchlist(ctx context.Context, profileID int64, limit, offset int) ([]WatchlistEntry, error) {
	rows, err := s.d.QueryContext(ctx, `SELECT item_id, added_ms FROM watchlist WHERE profile_id = ?
		ORDER BY added_ms DESC, item_id DESC LIMIT ? OFFSET ?`, profileID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("watchlist: %w", err)
	}
	defer rows.Close()
	var out []WatchlistEntry
	for rows.Next() {
		var e WatchlistEntry
		var ms int64
		if err := rows.Scan(&e.ItemID, &ms); err != nil {
			return nil, err
		}
		e.AddedAt = time.UnixMilli(ms).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetRating stores a personal rating from 0 to 10.
func (s *Store) SetRating(ctx context.Context, profileID, itemID int64, rating float64) error {
	if math.IsNaN(rating) || rating < 0 || rating > 10 {
		return fmt.Errorf("%w: rating must be between 0 and 10", ErrInvalid)
	}
	_, err := s.d.ExecContext(ctx, `INSERT INTO ratings (profile_id, item_id, rating, updated_ms) VALUES (?, ?, ?, ?)
		ON CONFLICT (profile_id, item_id) DO UPDATE SET rating = excluded.rating, updated_ms = excluded.updated_ms`,
		profileID, itemID, rating, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("set rating %d/%d: %w", profileID, itemID, err)
	}
	return nil
}

// Rating returns the personal rating; ok is false when unrated.
func (s *Store) Rating(ctx context.Context, profileID, itemID int64) (float64, bool, error) {
	var r float64
	err := s.d.QueryRowContext(ctx, `SELECT rating FROM ratings WHERE profile_id = ? AND item_id = ?`,
		profileID, itemID).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("rating %d/%d: %w", profileID, itemID, err)
	}
	return r, true, nil
}

// DeleteRating clears the personal rating.
func (s *Store) DeleteRating(ctx context.Context, profileID, itemID int64) error {
	if _, err := s.d.ExecContext(ctx, `DELETE FROM ratings WHERE profile_id = ? AND item_id = ?`,
		profileID, itemID); err != nil {
		return fmt.Errorf("delete rating %d/%d: %w", profileID, itemID, err)
	}
	return nil
}

// HistoryEntry is one finished play.
type HistoryEntry struct {
	ID        int64
	ItemID    int64
	WatchedAt time.Time
	Source    string // "lisimflix" | "plex"
}

// HistoryCursor is the position after which the next page starts.
type HistoryCursor struct{ WatchedMs, ID int64 }

// Cursor returns the cursor that continues after e.
func (e HistoryEntry) Cursor() HistoryCursor {
	return HistoryCursor{WatchedMs: e.WatchedAt.UnixMilli(), ID: e.ID}
}

// String encodes the cursor for URLs.
func (c HistoryCursor) String() string { return fmt.Sprintf("%d_%d", c.WatchedMs, c.ID) }

// ParseHistoryCursor decodes String's output.
func ParseHistoryCursor(s string) (HistoryCursor, error) {
	a, b, ok := strings.Cut(s, "_")
	ms, err1 := strconv.ParseInt(a, 10, 64)
	id, err2 := strconv.ParseInt(b, 10, 64)
	if !ok || err1 != nil || err2 != nil || ms < 0 || id <= 0 {
		return HistoryCursor{}, fmt.Errorf("%w: bad history cursor %q", ErrInvalid, s)
	}
	return HistoryCursor{WatchedMs: ms, ID: id}, nil
}

// History returns plays newest first, starting after before (zero = newest).
func (s *Store) History(ctx context.Context, profileID int64, before HistoryCursor, limit int) ([]HistoryEntry, error) {
	q := `SELECT id, item_id, watched_ms, source FROM watch_history WHERE profile_id = ?`
	args := []any{profileID}
	if before != (HistoryCursor{}) {
		q += ` AND (watched_ms < ? OR (watched_ms = ? AND id < ?))`
		args = append(args, before.WatchedMs, before.WatchedMs, before.ID)
	}
	q += ` ORDER BY watched_ms DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var ms int64
		if err := rows.Scan(&e.ID, &e.ItemID, &ms, &e.Source); err != nil {
			return nil, err
		}
		e.WatchedAt = time.UnixMilli(ms).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/db/migrations/20261025140100_userdata_lists.sql server/internal/userdata/lists.go server/internal/userdata/lists_test.go
git commit -m "feat(userdata): watchlist, personal ratings and paged watch history"
```

---

### Task 5: Per-show audio/subtitle preferences

**Files:**
- Create: `server/internal/db/migrations/20261025140200_userdata_track_prefs.sql`
- Create: `server/internal/userdata/trackprefs.go`
- Test: `server/internal/userdata/trackprefs_test.go`

**Interfaces:**
- Consumes: `Store`, `ErrInvalid`, `boolInt`
- Produces: `TrackPrefs{ProfileID, ShowID int64; AudioLanguage, AudioTitle, SubtitleLanguage, SubtitleKind, SubtitleTitle string; SubtitlesOff bool; UpdatedAt time.Time}`,
  subtitle kinds `""`, `"full"`, `"styled"`, `"signs_songs"`, `"forced"`, `"sdh"`;
  `(*Store) TrackPrefs(ctx, profileID, showID) (TrackPrefs, bool, error)`, `(*Store) SetTrackPrefs(ctx, TrackPrefs) error`, `(TrackPrefs) Validate() error`.
  Phase 2 playback reads these to pick default tracks for a show's episodes.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/20261025140200_userdata_track_prefs.sql`:
```sql
-- Per profile + show: preferred audio/subtitle choice, e.g. Japanese audio with
-- "styled" (full ASS) English subtitles rather than "signs_songs".
CREATE TABLE track_prefs (
    profile_id        INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    show_id           INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    audio_language    TEXT NOT NULL DEFAULT '',
    audio_title       TEXT NOT NULL DEFAULT '',
    subtitle_language TEXT NOT NULL DEFAULT '',
    subtitle_kind     TEXT NOT NULL DEFAULT ''
                      CHECK (subtitle_kind IN ('', 'full', 'styled', 'signs_songs', 'forced', 'sdh')),
    subtitle_title    TEXT NOT NULL DEFAULT '',
    subtitles_off     INTEGER NOT NULL DEFAULT 0 CHECK (subtitles_off IN (0, 1)),
    updated_ms        INTEGER NOT NULL,
    PRIMARY KEY (profile_id, show_id)
) STRICT;
```

- [ ] **Step 2: Write the failing tests**

`server/internal/userdata/trackprefs_test.go`:
```go
package userdata

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTrackPrefsRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, ok, err := f.store.TrackPrefs(ctx, f.profileID, f.show.ID); ok || err != nil {
		t.Fatalf("unset: ok=%v err=%v", ok, err)
	}
	want := TrackPrefs{ProfileID: f.profileID, ShowID: f.show.ID, AudioLanguage: "jpn",
		SubtitleLanguage: "eng", SubtitleKind: "styled", SubtitleTitle: "Full Subs [GJM]"}
	if err := f.store.SetTrackPrefs(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := f.store.TrackPrefs(ctx, f.profileID, f.show.ID)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want.UpdatedAt = f.clock.Now()
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	off := TrackPrefs{ProfileID: f.profileID, ShowID: f.show.ID, AudioLanguage: "eng", SubtitlesOff: true}
	if err := f.store.SetTrackPrefs(ctx, off); err != nil {
		t.Fatal(err)
	}
	got, _, _ = f.store.TrackPrefs(ctx, f.profileID, f.show.ID)
	if !got.SubtitlesOff || got.SubtitleKind != "" || got.AudioLanguage != "eng" {
		t.Fatalf("overwrite: %+v", got)
	}
}

func TestTrackPrefsValidation(t *testing.T) {
	f := newFixture(t)
	base := TrackPrefs{ProfileID: f.profileID, ShowID: f.show.ID}
	bad := []func(*TrackPrefs){
		func(p *TrackPrefs) { p.AudioLanguage = "Japanese" },
		func(p *TrackPrefs) { p.SubtitleLanguage = "e" },
		func(p *TrackPrefs) { p.SubtitleKind = "karaoke" },
		func(p *TrackPrefs) { p.AudioTitle = strings.Repeat("x", 201) },
		func(p *TrackPrefs) { p.SubtitlesOff = true; p.SubtitleLanguage = "eng" },
		func(p *TrackPrefs) { p.ShowID = 0 },
	}
	for i, mut := range bad {
		p := base
		mut(&p)
		if err := f.store.SetTrackPrefs(context.Background(), p); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: err=%v", i, err)
		}
	}
	ok := base
	ok.AudioLanguage, ok.SubtitleLanguage = "pt-BR", "en"
	if err := ok.Validate(); err != nil {
		t.Fatalf("pt-BR/en should be valid: %v", err)
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: TrackPrefs`

- [ ] **Step 4: Implement**

`server/internal/userdata/trackprefs.go`:
```go
package userdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
)

// TrackPrefs is a profile's audio/subtitle choice for one show. Empty strings
// mean "no preference" (fall back to the profile/library defaults from 1B).
type TrackPrefs struct {
	ProfileID        int64
	ShowID           int64
	AudioLanguage    string // ISO 639 code, e.g. "jpn", "en", "pt-BR"
	AudioTitle       string // stream title to prefer, e.g. "Commentary"
	SubtitleLanguage string
	SubtitleKind     string // "", "full", "styled", "signs_songs", "forced", "sdh"
	SubtitleTitle    string // stream title to prefer, e.g. "Full Subs [GJM]"
	SubtitlesOff     bool
	UpdatedAt        time.Time
}

var (
	langCode      = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	subtitleKinds = map[string]bool{"": true, "full": true, "styled": true, "signs_songs": true, "forced": true, "sdh": true}
)

const maxTrackTitle = 200

// Validate checks codes, kinds and lengths.
func (p TrackPrefs) Validate() error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }
	switch {
	case p.ProfileID <= 0 || p.ShowID <= 0:
		return bad("profile and show IDs are required")
	case p.AudioLanguage != "" && !langCode.MatchString(p.AudioLanguage):
		return bad("audioLanguage must be a language code like \"jpn\" or \"en\"")
	case p.SubtitleLanguage != "" && !langCode.MatchString(p.SubtitleLanguage):
		return bad("subtitleLanguage must be a language code like \"eng\" or \"en\"")
	case !subtitleKinds[p.SubtitleKind]:
		return bad("subtitleKind must be one of full, styled, signs_songs, forced, sdh")
	case utf8.RuneCountInString(p.AudioTitle) > maxTrackTitle || utf8.RuneCountInString(p.SubtitleTitle) > maxTrackTitle:
		return bad("track titles are limited to 200 characters")
	case p.SubtitlesOff && (p.SubtitleLanguage != "" || p.SubtitleKind != "" || p.SubtitleTitle != ""):
		return bad("subtitlesOff cannot be combined with a subtitle choice")
	}
	return nil
}

// TrackPrefs returns the saved choice; ok is false when none is saved.
func (s *Store) TrackPrefs(ctx context.Context, profileID, showID int64) (TrackPrefs, bool, error) {
	p := TrackPrefs{ProfileID: profileID, ShowID: showID}
	var off, ms int64
	err := s.d.QueryRowContext(ctx, `SELECT audio_language, audio_title, subtitle_language, subtitle_kind,
		subtitle_title, subtitles_off, updated_ms FROM track_prefs WHERE profile_id = ? AND show_id = ?`,
		profileID, showID).Scan(&p.AudioLanguage, &p.AudioTitle, &p.SubtitleLanguage, &p.SubtitleKind,
		&p.SubtitleTitle, &off, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackPrefs{ProfileID: profileID, ShowID: showID}, false, nil
	}
	if err != nil {
		return TrackPrefs{}, false, fmt.Errorf("track prefs %d/%d: %w", profileID, showID, err)
	}
	p.SubtitlesOff = off == 1
	p.UpdatedAt = time.UnixMilli(ms).UTC()
	return p, true, nil
}

// SetTrackPrefs replaces the saved choice for one show.
func (s *Store) SetTrackPrefs(ctx context.Context, p TrackPrefs) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := s.d.ExecContext(ctx, `INSERT INTO track_prefs (profile_id, show_id, audio_language, audio_title,
		subtitle_language, subtitle_kind, subtitle_title, subtitles_off, updated_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (profile_id, show_id) DO UPDATE SET
			audio_language = excluded.audio_language, audio_title = excluded.audio_title,
			subtitle_language = excluded.subtitle_language, subtitle_kind = excluded.subtitle_kind,
			subtitle_title = excluded.subtitle_title, subtitles_off = excluded.subtitles_off,
			updated_ms = excluded.updated_ms`,
		p.ProfileID, p.ShowID, p.AudioLanguage, p.AudioTitle, p.SubtitleLanguage, p.SubtitleKind,
		p.SubtitleTitle, boolInt(p.SubtitlesOff), s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("set track prefs %d/%d: %w", p.ProfileID, p.ShowID, err)
	}
	return nil
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/db/migrations/20261025140200_userdata_track_prefs.sql server/internal/userdata/trackprefs.go server/internal/userdata/trackprefs_test.go
git commit -m "feat(userdata): per-show audio and subtitle preferences"
```

---

### Task 6: Home row keys, saved layout and arrangement

**Files:**
- Create: `server/internal/db/migrations/20261025140300_userdata_home_rows.sql`
- Create: `server/internal/userdata/homerows.go`
- Test: `server/internal/userdata/homerows_test.go`

**Interfaces:**
- Consumes: `Store`, `ErrInvalid`, `boolInt`
- Produces: constants `RowContinueWatching = "continue_watching"`, `RowNextUp = "next_up"`, `RowWatchlist = "watchlist"`;
  `RecentlyAddedKey(libraryID int64) string` (`"recently_added:<id>"`), `ParseRecentlyAddedKey(string) (int64, bool)`,
  `ValidRowKey(string) bool`; `RowPref{Key string \`json:"key"\`; Hidden bool \`json:"hidden"\`}`;
  `(*Store) HomeRowPrefs(ctx, profileID) ([]RowPref, error)` (saved order),
  `(*Store) SetHomeRowPrefs(ctx, profileID int64, prefs []RowPref) error` (replace all; max 100; no duplicates),
  `ArrangeRows(available []string, prefs []RowPref) []RowPref`. ArrangeRows returns every available
  key exactly once: saved ones in saved order with their hidden flag, then unsaved ones in default order, visible.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/20261025140300_userdata_home_rows.sql`:
```sql
CREATE TABLE home_row_prefs (
    profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    row_key    TEXT NOT NULL,
    position   INTEGER NOT NULL,
    hidden     INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
    PRIMARY KEY (profile_id, row_key)
) STRICT;
```

- [ ] **Step 2: Write the failing tests**

`server/internal/userdata/homerows_test.go`:
```go
package userdata

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRowKeys(t *testing.T) {
	if RecentlyAddedKey(7) != "recently_added:7" {
		t.Fatal(RecentlyAddedKey(7))
	}
	if id, ok := ParseRecentlyAddedKey("recently_added:7"); !ok || id != 7 {
		t.Fatal("parse")
	}
	for _, k := range []string{"continue_watching", "next_up", "watchlist", "recently_added:12"} {
		if !ValidRowKey(k) {
			t.Fatalf("%s should be valid", k)
		}
	}
	for _, k := range []string{"", "recently_added:", "recently_added:0", "recently_added:07", "recently_added:+7", "trending"} {
		if ValidRowKey(k) {
			t.Fatalf("%q should be invalid", k)
		}
	}
}

func TestArrangeRows(t *testing.T) {
	avail := []string{"continue_watching", "next_up", "recently_added:1", "recently_added:2", "watchlist"}
	prefs := []RowPref{
		{Key: "watchlist"},
		{Key: "recently_added:9", Hidden: true}, // library no longer visible: dropped
		{Key: "next_up", Hidden: true},
		{Key: "continue_watching"},
	}
	got := ArrangeRows(avail, prefs)
	want := []RowPref{
		{Key: "watchlist"},
		{Key: "next_up", Hidden: true},
		{Key: "continue_watching"},
		{Key: "recently_added:1"},
		{Key: "recently_added:2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := ArrangeRows(avail, nil); len(got) != 5 || got[0].Key != "continue_watching" {
		t.Fatalf("defaults: %+v", got)
	}
}

func TestHomeRowPrefsStore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if got, err := f.store.HomeRowPrefs(ctx, f.profileID); err != nil || len(got) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	prefs := []RowPref{{Key: "watchlist"}, {Key: "next_up", Hidden: true}}
	if err := f.store.SetHomeRowPrefs(ctx, f.profileID, prefs); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.HomeRowPrefs(ctx, f.profileID); !reflect.DeepEqual(got, prefs) {
		t.Fatalf("got %+v", got)
	}
	// Replace, not merge.
	f.store.SetHomeRowPrefs(ctx, f.profileID, []RowPref{{Key: "continue_watching"}})
	if got, _ := f.store.HomeRowPrefs(ctx, f.profileID); len(got) != 1 {
		t.Fatalf("not replaced: %+v", got)
	}
	for _, bad := range [][]RowPref{
		{{Key: "trending"}},
		{{Key: "watchlist"}, {Key: "watchlist"}},
	} {
		if err := f.store.SetHomeRowPrefs(ctx, f.profileID, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: err=%v", bad, err)
		}
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: RecentlyAddedKey`

- [ ] **Step 4: Implement**

`server/internal/userdata/homerows.go`:
```go
package userdata

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Home row keys.
const (
	RowContinueWatching = "continue_watching"
	RowNextUp           = "next_up"
	RowWatchlist        = "watchlist"

	rowRecentlyAddedPrefix = "recently_added:"
	maxRowPrefs            = 100
)

// RecentlyAddedKey is the row key for a library's Recently Added row.
func RecentlyAddedKey(libraryID int64) string {
	return rowRecentlyAddedPrefix + strconv.FormatInt(libraryID, 10)
}

// ParseRecentlyAddedKey returns the library ID of a canonical recently_added key.
func ParseRecentlyAddedKey(key string) (int64, bool) {
	rest, ok := strings.CutPrefix(key, rowRecentlyAddedPrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 || RecentlyAddedKey(id) != key {
		return 0, false
	}
	return id, true
}

// ValidRowKey reports whether key names a known kind of row.
func ValidRowKey(key string) bool {
	switch key {
	case RowContinueWatching, RowNextUp, RowWatchlist:
		return true
	}
	_, ok := ParseRecentlyAddedKey(key)
	return ok
}

// RowPref is one saved row setting; the slice order is the display order.
type RowPref struct {
	Key    string `json:"key"`
	Hidden bool   `json:"hidden"`
}

// HomeRowPrefs returns the saved layout in display order.
func (s *Store) HomeRowPrefs(ctx context.Context, profileID int64) ([]RowPref, error) {
	rows, err := s.d.QueryContext(ctx, `SELECT row_key, hidden FROM home_row_prefs
		WHERE profile_id = ? ORDER BY position`, profileID)
	if err != nil {
		return nil, fmt.Errorf("home row prefs: %w", err)
	}
	defer rows.Close()
	var out []RowPref
	for rows.Next() {
		var p RowPref
		var hidden int64
		if err := rows.Scan(&p.Key, &hidden); err != nil {
			return nil, err
		}
		p.Hidden = hidden == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetHomeRowPrefs replaces the saved layout.
func (s *Store) SetHomeRowPrefs(ctx context.Context, profileID int64, prefs []RowPref) error {
	if len(prefs) > maxRowPrefs {
		return fmt.Errorf("%w: at most %d rows", ErrInvalid, maxRowPrefs)
	}
	seen := map[string]bool{}
	for _, p := range prefs {
		if !ValidRowKey(p.Key) {
			return fmt.Errorf("%w: unknown row %q", ErrInvalid, p.Key)
		}
		if seen[p.Key] {
			return fmt.Errorf("%w: row %q listed twice", ErrInvalid, p.Key)
		}
		seen[p.Key] = true
	}
	tx, err := s.d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set home rows: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM home_row_prefs WHERE profile_id = ?`, profileID); err != nil {
		return fmt.Errorf("set home rows: clear: %w", err)
	}
	for i, p := range prefs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO home_row_prefs (profile_id, row_key, position, hidden)
			VALUES (?, ?, ?, ?)`, profileID, p.Key, i, boolInt(p.Hidden)); err != nil {
			return fmt.Errorf("set home rows: insert %s: %w", p.Key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set home rows: commit: %w", err)
	}
	return nil
}

// ArrangeRows applies saved prefs to the rows available right now. Saved rows
// that are not available are dropped; available rows without a pref are
// appended, visible, in their default order.
func ArrangeRows(available []string, prefs []RowPref) []RowPref {
	avail := make(map[string]bool, len(available))
	for _, k := range available {
		avail[k] = true
	}
	out := make([]RowPref, 0, len(available))
	placed := map[string]bool{}
	for _, p := range prefs {
		if avail[p.Key] && !placed[p.Key] {
			out = append(out, p)
			placed[p.Key] = true
		}
	}
	for _, k := range available {
		if !placed[k] {
			out = append(out, RowPref{Key: k})
			placed[k] = true
		}
	}
	return out
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/db/migrations/20261025140300_userdata_home_rows.sql server/internal/userdata/homerows.go server/internal/userdata/homerows_test.go
git commit -m "feat(userdata): per-profile home row order and visibility"
```

---

### Task 7: Access policy (visible libraries, content rating) and leaf expansion

**Files:**
- Create: `server/internal/userdata/access.go`
- Test: `server/internal/userdata/fakes_test.go`, `server/internal/userdata/access_test.go`

**Interfaces:**
- Consumes: `authctx.Principal`, `media.Item`, `media.ErrNotFound`, `library.Library`; at runtime
  `*media.Store` (Catalog), `*library.Store` (Libraries), `*auth.Service` (Policy),
  `TopLevelRatings` over `(*metadata.Store).Load` (ContentRatings), `AllowedByAge(auth.RatingAge)` (Allowed)
- Produces:
  ```go
  type Catalog interface {
      GetItem(ctx context.Context, id int64) (media.Item, error)
      Children(ctx context.Context, parentID int64) ([]media.Item, error)
      RecentlyAdded(ctx context.Context, libraryID int64, limit int) ([]media.Item, error)
  }
  type Libraries interface { Get(ctx context.Context, id int64) (library.Library, error) }
  type Policy interface {
      VisibleLibraryIDs(ctx context.Context, p authctx.Principal) ([]int64, error)
      MaxContentRating(ctx context.Context, p authctx.Principal) (string, error)
  }
  type ContentRatings interface { ContentRating(ctx context.Context, itemID int64) (string, error) }
  type Access struct { Policy Policy; Ratings ContentRatings; Allowed func(maxRating, rating string) bool }
  func AllowedByAge(age func(label string) (int, bool)) func(maxRating, rating string) bool // wire with auth.RatingAge
  type TopLevelRatings struct { Catalog Catalog; Lookup func(ctx context.Context, itemID int64) (string, error) }
  func (r TopLevelRatings) ContentRating(ctx, itemID int64) (string, error) // episode/season → show's rating
  type Viewer struct { Principal authctx.Principal; LibraryIDs []int64; MaxRating string /* + unexported set */ }
  func (a Access) Viewer(ctx, p authctx.Principal) (Viewer, error)
  func (a Access) CanSee(ctx, v Viewer, it media.Item) (bool, error)
  func (v Viewer) SeesLibrary(id int64) bool
  func Leaves(ctx context.Context, c Catalog, it media.Item) ([]media.Item, error) // playable descendants (or it), extras skipped
  ```
  Test fakes: `fakePolicy{libs []int64; max string}`, `fakeRatings map[int64]string`, `ladderAllowed`.

- [ ] **Step 1: Write the test fakes and failing tests**

`server/internal/userdata/fakes_test.go`:
```go
package userdata

import (
	"context"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

type fakePolicy struct {
	libs []int64
	max  string
}

func (p fakePolicy) VisibleLibraryIDs(context.Context, authctx.Principal) ([]int64, error) {
	return p.libs, nil
}
func (p fakePolicy) MaxContentRating(context.Context, authctx.Principal) (string, error) {
	return p.max, nil
}

type fakeRatings map[int64]string

func (r fakeRatings) ContentRating(_ context.Context, id int64) (string, error) { return r[id], nil }

// ladderAllowed is a tiny stand-in for AllowedByAge(auth.RatingAge): G < PG < M.
func ladderAllowed(max, rating string) bool {
	if max == "" {
		return true
	}
	order := map[string]int{"G": 1, "PG": 2, "M": 3}
	r, ok := order[rating]
	return ok && r <= order[max]
}

func (f *fixture) access(libs []int64, max string, ratings fakeRatings) Access {
	if ratings == nil {
		ratings = fakeRatings{}
	}
	return Access{Policy: fakePolicy{libs: libs, max: max}, Ratings: ratings, Allowed: ladderAllowed}
}

func (f *fixture) principal() authctx.Principal {
	return authctx.Principal{UserID: f.userID, ProfileID: f.profileID}
}
```

`server/internal/userdata/access_test.go`:
```go
package userdata

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/userdata/udtest"
)

func TestCanSee(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ratings := fakeRatings{f.movie.ID: "M", f.show.ID: "PG"}

	a := f.access([]int64{f.libMovies}, "", ratings)
	v, err := a.Viewer(ctx, f.principal())
	if err != nil {
		t.Fatal(err)
	}
	if !v.SeesLibrary(f.libMovies) || v.SeesLibrary(f.libTV) {
		t.Fatalf("viewer libs: %+v", v)
	}
	if ok, _ := a.CanSee(ctx, v, f.movie); !ok {
		t.Fatal("unrestricted viewer should see the movie")
	}
	if ok, _ := a.CanSee(ctx, v, f.show); ok {
		t.Fatal("TV library is not visible")
	}

	kid := f.access([]int64{f.libMovies, f.libTV}, "PG", ratings)
	kv, _ := kid.Viewer(ctx, f.principal())
	if ok, _ := kid.CanSee(ctx, kv, f.movie); ok {
		t.Fatal("M movie must be hidden under PG")
	}
	if ok, _ := kid.CanSee(ctx, kv, f.show); !ok {
		t.Fatal("PG show should be visible under PG")
	}
	if ok, _ := kid.CanSee(ctx, kv, f.eps["S01E01"]); ok {
		t.Fatal("unrated item must be hidden when a limit is set")
	}
}

func TestLeaves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := map[string]struct {
		id   int64
		want int
	}{
		"movie":    {f.movie.ID, 1},
		"show":     {f.show.ID, 6},
		"season 1": {f.seasons[1].ID, 3},
		"episode":  {f.eps["S02E01"].ID, 1},
	}
	for name, c := range cases {
		it, err := f.media.GetItem(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Leaves(ctx, f.media, it)
		if err != nil || len(got) != c.want {
			t.Fatalf("%s: %d leaves (%v), want %d", name, len(got), err, c.want)
		}
		for _, l := range got {
			if !IsPlayable(l.Kind) {
				t.Fatalf("%s: non-playable leaf %+v", name, l)
			}
		}
	}
}

func TestLeavesSkipExtras(t *testing.T) {
	f := newFixture(t)
	udtest.NewItem(t, f.d, media.Item{LibraryID: f.libTV, ParentID: f.show.ID, Kind: media.KindVideo,
		Title: "Trailer", ExtraType: media.ExtraTrailer})
	got, err := Leaves(context.Background(), f.media, f.show)
	if err != nil || len(got) != 6 {
		t.Fatalf("got %d leaves (%v), want 6 episodes", len(got), err)
	}
}

func TestAllowedByAge(t *testing.T) {
	age := func(l string) (int, bool) {
		m := map[string]int{"G": 0, "PG": 8, "M": 15, "MA15+": 15, "R18+": 18}
		a, ok := m[l]
		if !ok {
			return 18, false
		}
		return a, true
	}
	allowed := AllowedByAge(age)
	cases := []struct {
		max, rating string
		want        bool
	}{
		{"", "R18+", true}, {"M", "PG", true}, {"M", "MA15+", true}, {"PG", "M", false},
		{"PG", "", false}, {"R18+", "", true},
	}
	for _, c := range cases {
		if got := allowed(c.max, c.rating); got != c.want {
			t.Fatalf("allowed(%q, %q) = %v, want %v", c.max, c.rating, got, c.want)
		}
	}
}

func TestTopLevelRatingsUsesShow(t *testing.T) {
	f := newFixture(t)
	var asked []int64
	r := TopLevelRatings{Catalog: f.media, Lookup: func(_ context.Context, id int64) (string, error) {
		asked = append(asked, id)
		return map[int64]string{f.show.ID: "TV-14", f.movie.ID: "R"}[id], nil
	}}
	if got, err := r.ContentRating(context.Background(), f.eps["S01E02"].ID); err != nil || got != "TV-14" {
		t.Fatalf("episode: %q %v", got, err)
	}
	if got, _ := r.ContentRating(context.Background(), f.movie.ID); got != "R" {
		t.Fatalf("movie: %q", got)
	}
	if asked[0] != f.show.ID {
		t.Fatalf("looked up %v, want the show first", asked)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Access`

- [ ] **Step 3: Implement**

`server/internal/userdata/access.go`:
```go
package userdata

import (
	"context"
	"fmt"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// Catalog is the read side of the media store that userdata needs (*media.Store).
type Catalog interface {
	GetItem(ctx context.Context, id int64) (media.Item, error)
	Children(ctx context.Context, parentID int64) ([]media.Item, error)
	RecentlyAdded(ctx context.Context, libraryID int64, limit int) ([]media.Item, error)
}

// Libraries looks up library names (*library.Store).
type Libraries interface {
	Get(ctx context.Context, id int64) (library.Library, error)
}

// Policy is the auth policy (*auth.Service).
type Policy interface {
	VisibleLibraryIDs(ctx context.Context, p authctx.Principal) ([]int64, error)
	MaxContentRating(ctx context.Context, p authctx.Principal) (string, error)
}

// ContentRatings returns an item's effective content rating (*metadata.Store).
type ContentRatings interface {
	ContentRating(ctx context.Context, itemID int64) (string, error)
}

// Access applies library visibility and the content-rating limit.
type Access struct {
	Policy  Policy
	Ratings ContentRatings
	Allowed func(maxRating, rating string) bool // AllowedByAge(auth.RatingAge)
}

// Viewer is a principal's resolved policy for one request.
type Viewer struct {
	Principal  authctx.Principal
	LibraryIDs []int64 // in the order auth returns them (used for default row order)
	MaxRating  string  // "" = unrestricted
	libs       map[int64]bool
}

// SeesLibrary reports whether the library is visible to the viewer.
func (v Viewer) SeesLibrary(id int64) bool { return v.libs[id] }

// Viewer resolves p's policy once.
func (a Access) Viewer(ctx context.Context, p authctx.Principal) (Viewer, error) {
	ids, err := a.Policy.VisibleLibraryIDs(ctx, p)
	if err != nil {
		return Viewer{}, fmt.Errorf("visible libraries: %w", err)
	}
	max, err := a.Policy.MaxContentRating(ctx, p)
	if err != nil {
		return Viewer{}, fmt.Errorf("max content rating: %w", err)
	}
	v := Viewer{Principal: p, LibraryIDs: ids, MaxRating: max, libs: make(map[int64]bool, len(ids))}
	for _, id := range ids {
		v.libs[id] = true
	}
	return v, nil
}

// CanSee reports whether the viewer may see the item.
func (a Access) CanSee(ctx context.Context, v Viewer, it media.Item) (bool, error) {
	if !v.libs[it.LibraryID] {
		return false, nil
	}
	if v.MaxRating == "" {
		return true, nil
	}
	rating, err := a.Ratings.ContentRating(ctx, it.ID)
	if err != nil {
		return false, fmt.Errorf("content rating for item %d: %w", it.ID, err)
	}
	return a.Allowed(v.MaxRating, rating), nil
}

// Leaves returns the playable items under it (it itself if playable), e.g. all
// episodes of a show including specials. Used to cascade mark watched/unwatched.
func Leaves(ctx context.Context, c Catalog, it media.Item) ([]media.Item, error) {
	if IsPlayable(it.Kind) {
		return []media.Item{it}, nil
	}
	kids, err := c.Children(ctx, it.ID)
	if err != nil {
		return nil, fmt.Errorf("children of %d: %w", it.ID, err)
	}
	var out []media.Item
	for _, k := range kids {
		if k.ExtraType != "" {
			continue // trailers and featurettes are not part of "watched"
		}
		sub, err := Leaves(ctx, c, k)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

// AllowedByAge builds Access.Allowed from auth.RatingAge: an item is allowed when
// its rating's minimum age is at most the limit's. Unknown labels (including "")
// count as 18 inside RatingAge.
func AllowedByAge(age func(label string) (int, bool)) func(maxRating, rating string) bool {
	return func(maxRating, rating string) bool {
		if maxRating == "" {
			return true
		}
		ra, _ := age(rating)
		ma, _ := age(maxRating)
		return ra <= ma
	}
}

// TopLevelRatings resolves an item's content rating from its top-level item
// (episodes and seasons use their show's), since 1D stores ratings there.
// Lookup is wired to (*metadata.Store).Load(...).ContentRating in main.
type TopLevelRatings struct {
	Catalog Catalog
	Lookup  func(ctx context.Context, itemID int64) (string, error)
}

// ContentRating implements ContentRatings.
func (r TopLevelRatings) ContentRating(ctx context.Context, itemID int64) (string, error) {
	it, err := r.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return "", fmt.Errorf("item %d: %w", itemID, err)
	}
	for i := 0; i < 3 && it.ParentID != 0; i++ {
		if it, err = r.Catalog.GetItem(ctx, it.ParentID); err != nil {
			return "", fmt.Errorf("parent of item %d: %w", itemID, err)
		}
	}
	return r.Lookup(ctx, it.ID)
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/userdata/access.go server/internal/userdata/access_test.go server/internal/userdata/fakes_test.go
git commit -m "feat(userdata): library visibility and content-rating access checks"
```

---

### Task 8: Home rows part 1: Continue Watching and Next Up

**Files:**
- Create: `server/internal/userdata/home.go`
- Test: `server/internal/userdata/home_test.go`

**Interfaces:**
- Consumes: `Store` (InProgress, RecentlyWatched, GetMany, Now, Rules), `Catalog`, `Libraries`, `Access`, `Viewer`, kind constants
- Produces:
  ```go
  type Card struct {
      ID int64 `json:"id"`; Kind string `json:"kind"`; Title string `json:"title"`; Year int `json:"year,omitempty"`
      LibraryID int64 `json:"libraryId"`; ShowID int64 `json:"showId,omitempty"`; ShowTitle string `json:"showTitle,omitempty"`
      SeasonNumber *int `json:"seasonNumber,omitempty"`; EpisodeNumber *int `json:"episodeNumber,omitempty"`
      State *CardState `json:"state,omitempty"`
  }
  type CardState struct { PositionMs, DurationMs int64; Watched bool; PlayCount int; LastPlayed *time.Time } // camelCase JSON
  type Home struct { Store *Store; Catalog Catalog; Libraries Libraries; Access Access }
  const DefaultRowLimit = 20
  func (h *Home) ContinueWatching(ctx, v Viewer, limit int) ([]Card, error)
  func (h *Home) NextUp(ctx, v Viewer, limit int) ([]Card, error)
  func (h *Home) Card(ctx, it media.Item, st State) (Card, error)
  func (h *Home) visible(ctx, v Viewer, itemID int64) (media.Item, bool, error) // missing item = not visible
  ```
  **Next Up rule:** take watched, non-special episodes played within `Rules.NextUpWindow`, newest
  first. The newest one per show is that show's anchor. The candidate is the first episode after
  the anchor (seasons > 0 in order, then episode number) that is not watched. Nothing is shown for the
  show if that candidate is in progress (it is in Continue Watching instead) or was dismissed, if the
  show has any in-progress episode, if the anchor is the last episode, or if the show isn't visible.

- [ ] **Step 1: Write the failing tests**

`server/internal/userdata/home_test.go`:
```go
package userdata

import (
	"context"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library"
)

func (f *fixture) home(a Access) *Home {
	return &Home{Store: f.store, Catalog: f.media, Libraries: library.NewStore(f.d), Access: a}
}

func (f *fixture) viewer(t *testing.T, a Access) Viewer {
	t.Helper()
	v, err := a.Viewer(context.Background(), f.principal())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func cardIDs(cs []Card) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestContinueWatching(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.access([]int64{f.libMovies, f.libTV}, "", nil)
	h, v := f.home(a), f.viewer(t, a)
	f.store.RecordProgress(ctx, f.profileID, f.movie.ID, 1_000_000, testDurationMs, 0)
	f.clock.Advance(time.Minute)
	f.store.RecordProgress(ctx, f.profileID, f.eps["S01E02"].ID, 2_000_000, testDurationMs, 0)

	got, err := h.ContinueWatching(ctx, v, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != f.eps["S01E02"].ID || got[1].ID != f.movie.ID {
		t.Fatalf("got %v", cardIDs(got))
	}
	ep := got[0]
	if ep.ShowID != f.show.ID || ep.ShowTitle != "Firefly" || *ep.SeasonNumber != 1 || *ep.EpisodeNumber != 2 {
		t.Fatalf("episode card: %+v", ep)
	}
	if ep.State == nil || ep.State.PositionMs != 2_000_000 || ep.State.DurationMs != testDurationMs {
		t.Fatalf("card state: %+v", ep.State)
	}
	if one, _ := h.ContinueWatching(ctx, v, 1); len(one) != 1 {
		t.Fatalf("limit: %v", cardIDs(one))
	}

	moviesOnly := f.access([]int64{f.libMovies}, "", nil)
	got, _ = f.home(moviesOnly).ContinueWatching(ctx, f.viewer(t, moviesOnly), 10)
	if len(got) != 1 || got[0].ID != f.movie.ID {
		t.Fatalf("library filter: %v", cardIDs(got))
	}
	kid := f.access([]int64{f.libMovies, f.libTV}, "PG", fakeRatings{f.movie.ID: "M", f.eps["S01E02"].ID: "PG"})
	got, _ = f.home(kid).ContinueWatching(ctx, f.viewer(t, kid), 10)
	if len(got) != 1 || got[0].ID != f.eps["S01E02"].ID {
		t.Fatalf("rating filter: %v", cardIDs(got))
	}
}

func TestNextUp(t *testing.T) {
	all := func(f *fixture) Access { return f.access([]int64{f.libMovies, f.libTV}, "", nil) }
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
		want  string // episode key, "" = no Next Up entry
		acc   func(f *fixture) Access
	}{
		{"after first episode", func(t *testing.T, f *fixture) { f.watch(t, f.eps["S01E01"].ID) }, "S01E02", all},
		{"season boundary", func(t *testing.T, f *fixture) {
			f.watch(t, f.eps["S01E01"].ID)
			f.watch(t, f.eps["S01E02"].ID)
			f.watch(t, f.eps["S01E03"].ID)
		}, "S02E01", all},
		{"skips already watched", func(t *testing.T, f *fixture) {
			f.watch(t, f.eps["S01E02"].ID)
			f.watch(t, f.eps["S01E01"].ID) // most recent anchor is E01; E02 already watched
		}, "S01E03", all},
		{"special only", func(t *testing.T, f *fixture) { f.watch(t, f.eps["S00E01"].ID) }, "", all},
		{"special does not move the anchor", func(t *testing.T, f *fixture) {
			f.watch(t, f.eps["S01E01"].ID)
			f.watch(t, f.eps["S00E01"].ID)
		}, "S01E02", all},
		{"finished show", func(t *testing.T, f *fixture) { f.watch(t, f.eps["S02E02"].ID) }, "", all},
		{"next is in progress", func(t *testing.T, f *fixture) {
			f.watch(t, f.eps["S01E01"].ID)
			f.store.RecordProgress(context.Background(), f.profileID, f.eps["S01E02"].ID, 1_000_000, testDurationMs, 0)
		}, "", all},
		{"next dismissed", func(t *testing.T, f *fixture) {
			f.watch(t, f.eps["S01E01"].ID)
			f.store.HideFromContinue(context.Background(), f.profileID, f.eps["S01E02"].ID)
		}, "", all},
		{"outside window", func(t *testing.T, f *fixture) {
			f.watch(t, f.eps["S01E01"].ID)
			f.clock.Advance(91 * 24 * time.Hour)
		}, "", all},
		{"library hidden", func(t *testing.T, f *fixture) { f.watch(t, f.eps["S01E01"].ID) }, "",
			func(f *fixture) Access { return f.access([]int64{f.libMovies}, "", nil) }},
		{"rating too high", func(t *testing.T, f *fixture) { f.watch(t, f.eps["S01E01"].ID) }, "",
			func(f *fixture) Access {
				return f.access([]int64{f.libMovies, f.libTV}, "PG", fakeRatings{f.show.ID: "M"})
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(t, f)
			a := c.acc(f)
			got, err := f.home(a).NextUp(context.Background(), f.viewer(t, a), 10)
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("want nothing, got %v", cardIDs(got))
				}
				return
			}
			if len(got) != 1 || got[0].ID != f.eps[c.want].ID || got[0].ShowID != f.show.ID {
				t.Fatalf("want %s (%d), got %+v", c.want, f.eps[c.want].ID, got)
			}
		})
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Home`

- [ ] **Step 3: Implement**

`server/internal/userdata/home.go`:
```go
package userdata

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// DefaultRowLimit is the number of cards per home row.
const DefaultRowLimit = 20

// Card is one tile in a home row or list.
type Card struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	Year          int        `json:"year,omitempty"`
	LibraryID     int64      `json:"libraryId"`
	ShowID        int64      `json:"showId,omitempty"`
	ShowTitle     string     `json:"showTitle,omitempty"`
	SeasonNumber  *int       `json:"seasonNumber,omitempty"`
	EpisodeNumber *int       `json:"episodeNumber,omitempty"`
	State         *CardState `json:"state,omitempty"`
}

// CardState is the viewer's state for a card; nil when the item was never touched.
type CardState struct {
	PositionMs int64      `json:"positionMs"`
	DurationMs int64      `json:"durationMs"`
	Watched    bool       `json:"watched"`
	PlayCount  int        `json:"playCount"`
	LastPlayed *time.Time `json:"lastPlayed,omitempty"`
}

func cardState(st State) *CardState {
	if !st.Watched && st.PositionMs == 0 && st.PlayCount == 0 && st.LastPlayed.IsZero() {
		return nil
	}
	cs := &CardState{PositionMs: st.PositionMs, DurationMs: st.DurationMs, Watched: st.Watched, PlayCount: st.PlayCount}
	if !st.LastPlayed.IsZero() {
		lp := st.LastPlayed
		cs.LastPlayed = &lp
	}
	return cs
}

// Home builds the personal home rows.
type Home struct {
	Store     *Store
	Catalog   Catalog
	Libraries Libraries
	Access    Access
}

// Card renders an item (with its show for episodes) and the viewer's state.
func (h *Home) Card(ctx context.Context, it media.Item, st State) (Card, error) {
	c := Card{
		ID: it.ID, Kind: string(it.Kind), Title: it.Title, Year: it.Year, LibraryID: it.LibraryID,
		SeasonNumber: it.SeasonNumber, EpisodeNumber: it.EpisodeNumber, State: cardState(st),
	}
	if it.Kind == kindEpisode || it.Kind == kindSeason {
		show, err := h.showOf(ctx, it)
		if err != nil {
			return Card{}, err
		}
		c.ShowID, c.ShowTitle = show.ID, show.Title
	}
	return c, nil
}

// showOf walks up from an episode or season to its show.
func (h *Home) showOf(ctx context.Context, it media.Item) (media.Item, error) {
	cur := it
	for i := 0; i < 3 && cur.Kind != kindShow; i++ {
		if cur.ParentID == 0 {
			break
		}
		next, err := h.Catalog.GetItem(ctx, cur.ParentID)
		if err != nil {
			return media.Item{}, fmt.Errorf("parent of item %d: %w", cur.ID, err)
		}
		cur = next
	}
	if cur.Kind != kindShow {
		return media.Item{}, fmt.Errorf("item %d has no show ancestor", it.ID)
	}
	return cur, nil
}

// visible loads an item and checks access. Deleted items count as not visible.
func (h *Home) visible(ctx context.Context, v Viewer, itemID int64) (media.Item, bool, error) {
	it, err := h.Catalog.GetItem(ctx, itemID)
	if errors.Is(err, media.ErrNotFound) {
		return media.Item{}, false, nil
	}
	if err != nil {
		return media.Item{}, false, fmt.Errorf("item %d: %w", itemID, err)
	}
	ok, err := h.Access.CanSee(ctx, v, it)
	return it, ok, err
}

// ContinueWatching lists in-progress items, most recently played first.
func (h *Home) ContinueWatching(ctx context.Context, v Viewer, limit int) ([]Card, error) {
	states, err := h.Store.InProgress(ctx, v.Principal.ProfileID, limit*3)
	if err != nil {
		return nil, err
	}
	out := make([]Card, 0, limit)
	for _, st := range states {
		if len(out) >= limit {
			break
		}
		it, ok, err := h.visible(ctx, v, st.ItemID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		c, err := h.Card(ctx, it, st)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func isSpecial(it media.Item) bool { return it.SeasonNumber != nil && *it.SeasonNumber == 0 }

func episodeNumber(it media.Item) int {
	if it.EpisodeNumber == nil {
		return math.MaxInt
	}
	return *it.EpisodeNumber
}

// NextUp lists, per recently watched show, the next unwatched episode.
func (h *Home) NextUp(ctx context.Context, v Viewer, limit int) ([]Card, error) {
	pid := v.Principal.ProfileID
	since := h.Store.Now().Add(-h.Store.Rules().NextUpWindow)
	recent, err := h.Store.RecentlyWatched(ctx, pid, since, 500)
	if err != nil {
		return nil, err
	}
	busy, err := h.showsInProgress(ctx, pid)
	if err != nil {
		return nil, err
	}
	done := map[int64]bool{}
	out := make([]Card, 0, limit)
	for _, st := range recent {
		if len(out) >= limit {
			break
		}
		ep, err := h.Catalog.GetItem(ctx, st.ItemID)
		if errors.Is(err, media.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", st.ItemID, err)
		}
		if ep.Kind != kindEpisode || isSpecial(ep) {
			continue
		}
		show, err := h.showOf(ctx, ep)
		if err != nil {
			return nil, err
		}
		if done[show.ID] {
			continue
		}
		done[show.ID] = true
		if busy[show.ID] {
			continue
		}
		if ok, err := h.Access.CanSee(ctx, v, show); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		next, nst, found, err := h.nextAfter(ctx, pid, show.ID, ep.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		c, err := h.Card(ctx, next, nst)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// showsInProgress returns the shows that have an in-progress (visible or not) episode.
func (h *Home) showsInProgress(ctx context.Context, profileID int64) (map[int64]bool, error) {
	states, err := h.Store.InProgress(ctx, profileID, 200)
	if err != nil {
		return nil, err
	}
	busy := map[int64]bool{}
	for _, st := range states {
		it, err := h.Catalog.GetItem(ctx, st.ItemID)
		if errors.Is(err, media.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", st.ItemID, err)
		}
		if it.Kind != kindEpisode {
			continue
		}
		show, err := h.showOf(ctx, it)
		if err != nil {
			return nil, err
		}
		busy[show.ID] = true
	}
	return busy, nil
}

// orderedEpisodes returns a show's regular episodes (season > 0) in watch order.
func (h *Home) orderedEpisodes(ctx context.Context, showID int64) ([]media.Item, error) {
	kids, err := h.Catalog.Children(ctx, showID)
	if err != nil {
		return nil, fmt.Errorf("seasons of %d: %w", showID, err)
	}
	var seasons []media.Item
	for _, s := range kids {
		if s.Kind == kindSeason && s.SeasonNumber != nil && *s.SeasonNumber > 0 {
			seasons = append(seasons, s)
		}
	}
	sort.Slice(seasons, func(i, j int) bool { return *seasons[i].SeasonNumber < *seasons[j].SeasonNumber })
	var eps []media.Item
	for _, s := range seasons {
		kids, err := h.Catalog.Children(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("episodes of %d: %w", s.ID, err)
		}
		var se []media.Item
		for _, k := range kids {
			if k.Kind == kindEpisode {
				se = append(se, k)
			}
		}
		sort.SliceStable(se, func(i, j int) bool { return episodeNumber(se[i]) < episodeNumber(se[j]) })
		eps = append(eps, se...)
	}
	return eps, nil
}

// nextAfter finds the first unwatched episode after anchorID.
func (h *Home) nextAfter(ctx context.Context, profileID, showID, anchorID int64) (media.Item, State, bool, error) {
	eps, err := h.orderedEpisodes(ctx, showID)
	if err != nil {
		return media.Item{}, State{}, false, err
	}
	ids := make([]int64, len(eps))
	anchor := -1
	for i, e := range eps {
		ids[i] = e.ID
		if e.ID == anchorID {
			anchor = i
		}
	}
	if anchor < 0 {
		return media.Item{}, State{}, false, nil
	}
	states, err := h.Store.GetMany(ctx, profileID, ids[anchor+1:])
	if err != nil {
		return media.Item{}, State{}, false, err
	}
	for _, e := range eps[anchor+1:] {
		st, ok := states[e.ID]
		if !ok {
			st = State{ProfileID: profileID, ItemID: e.ID}
		}
		if st.Watched {
			continue
		}
		if st.HiddenFromContinue || st.PositionMs > 0 {
			return media.Item{}, State{}, false, nil
		}
		return e, st, true, nil
	}
	return media.Item{}, State{}, false, nil
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/userdata/home.go server/internal/userdata/home_test.go
git commit -m "feat(userdata): continue watching and next up rows"
```

---

### Task 9: Home rows part 2: Recently Added, Watchlist, layout and full build

**Files:**
- Create: `server/internal/userdata/home_rows.go`
- Test: `server/internal/userdata/home_rows_test.go`

**Interfaces:**
- Consumes: `Home`, `Card`, `ArrangeRows`, row key helpers, `Store.Watchlist`, `Store.HomeRowPrefs`, `Catalog.RecentlyAdded`, `Libraries.Get`
- Produces:
  ```go
  type Row struct { Key string `json:"key"`; Title string `json:"title"`; Items []Card `json:"items"` }
  type RowLayout struct { Key string `json:"key"`; Title string `json:"title"`; Hidden bool `json:"hidden"` }
  func (h *Home) RecentlyAdded(ctx, v Viewer, libraryID int64, limit int) ([]Card, error)
  func (h *Home) WatchlistRow(ctx, v Viewer, limit int) ([]Card, error)
  func (h *Home) AvailableRows(v Viewer) []string // continue_watching, next_up, recently_added:<lib>..., watchlist
  func (h *Home) RowTitle(ctx, key string) (string, error)
  func (h *Home) Layout(ctx, v Viewer) ([]RowLayout, error)
  func (h *Home) Build(ctx, v Viewer, limit int) ([]Row, error) // visible, non-empty rows in layout order
  ```

- [ ] **Step 1: Write the failing tests**

`server/internal/userdata/home_rows_test.go`:
```go
package userdata

import (
	"context"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/userdata/udtest"
)

func rowKeys(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Key
	}
	return out
}

func TestRecentlyAddedFiltersAndMarksState(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	heat := udtest.NewItem(t, f.d, media.Item{LibraryID: f.libMovies, Kind: kindMovie, Title: "Heat", Year: 1995})
	f.watch(t, f.movie.ID)
	a := f.access([]int64{f.libMovies, f.libTV}, "PG", fakeRatings{f.movie.ID: "PG", heat.ID: "M"})
	h := f.home(a)
	got, err := h.RecentlyAdded(ctx, f.viewer(t, a), f.libMovies, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != f.movie.ID || got[0].State == nil || !got[0].State.Watched {
		t.Fatalf("got %+v", got)
	}
	hidden := f.access([]int64{f.libTV}, "", nil)
	if got, _ := f.home(hidden).RecentlyAdded(ctx, f.viewer(t, hidden), f.libMovies, 10); len(got) != 0 {
		t.Fatalf("invisible library leaked: %+v", got)
	}
}

func TestWatchlistRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.store.AddToWatchlist(ctx, f.profileID, f.movie.ID)
	f.clock.Advance(time.Minute)
	f.store.AddToWatchlist(ctx, f.profileID, f.show.ID)
	a := f.access([]int64{f.libMovies, f.libTV}, "", nil)
	got, err := f.home(a).WatchlistRow(ctx, f.viewer(t, a), 10)
	if err != nil || len(got) != 2 || got[0].ID != f.show.ID || got[0].Kind != "show" {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestBuildDefaultOrderAndPrefs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.watch(t, f.eps["S01E01"].ID)                                                      // next_up: S01E02
	f.store.RecordProgress(ctx, f.profileID, f.movie.ID, 1_000_000, testDurationMs, 0) // continue_watching
	f.store.AddToWatchlist(ctx, f.profileID, f.show.ID)                                 // watchlist
	a := f.access([]int64{f.libMovies, f.libTV}, "", nil)
	h, v := f.home(a), f.viewer(t, a)

	rows, err := h.Build(ctx, v, DefaultRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{RowContinueWatching, RowNextUp, RecentlyAddedKey(f.libMovies), RecentlyAddedKey(f.libTV), RowWatchlist}
	if got := rowKeys(rows); len(got) != len(want) {
		t.Fatalf("rows %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("rows %v, want %v", got, want)
			}
		}
	}
	if rows[2].Title != "Recently Added in Movies" || rows[0].Title != "Continue Watching" {
		t.Fatalf("titles: %q %q", rows[0].Title, rows[2].Title)
	}

	f.store.SetHomeRowPrefs(ctx, f.profileID, []RowPref{{Key: RowWatchlist}, {Key: RowNextUp, Hidden: true}})
	rows, _ = h.Build(ctx, v, DefaultRowLimit)
	got := rowKeys(rows)
	if got[0] != RowWatchlist || len(got) != 4 {
		t.Fatalf("prefs not applied: %v", got)
	}
	for _, k := range got {
		if k == RowNextUp {
			t.Fatal("hidden row returned")
		}
	}
	layout, err := h.Layout(ctx, v)
	if err != nil || len(layout) != 5 || layout[1].Key != RowNextUp || !layout[1].Hidden || layout[1].Title != "Next Up" {
		t.Fatalf("layout: %+v %v", layout, err)
	}
}

func TestBuildOmitsEmptyRows(t *testing.T) {
	f := newFixture(t)
	a := f.access([]int64{f.libMovies}, "", nil)
	rows, err := f.home(a).Build(context.Background(), f.viewer(t, a), DefaultRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowKeys(rows); len(got) != 1 || got[0] != RecentlyAddedKey(f.libMovies) {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `h.RecentlyAdded undefined`

- [ ] **Step 3: Implement**

`server/internal/userdata/home_rows.go`:
```go
package userdata

import (
	"context"
	"fmt"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// Row is one rendered home row.
type Row struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Items []Card `json:"items"`
}

// RowLayout is one row in the profile's layout (for the customise screen).
type RowLayout struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Hidden bool   `json:"hidden"`
}

// RecentlyAdded lists a visible library's newest items.
func (h *Home) RecentlyAdded(ctx context.Context, v Viewer, libraryID int64, limit int) ([]Card, error) {
	if !v.SeesLibrary(libraryID) {
		return nil, nil
	}
	items, err := h.Catalog.RecentlyAdded(ctx, libraryID, limit*2)
	if err != nil {
		return nil, fmt.Errorf("recently added in %d: %w", libraryID, err)
	}
	return h.cards(ctx, v, items, limit)
}

// WatchlistRow lists the profile's watchlist, newest first.
func (h *Home) WatchlistRow(ctx context.Context, v Viewer, limit int) ([]Card, error) {
	entries, err := h.Store.Watchlist(ctx, v.Principal.ProfileID, limit*2, 0)
	if err != nil {
		return nil, err
	}
	var items []media.Item
	for _, e := range entries {
		it, ok, err := h.visible(ctx, v, e.ItemID)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, it)
		}
	}
	return h.cards(ctx, v, items, limit)
}

// cards filters items by access and renders up to limit cards with state.
func (h *Home) cards(ctx context.Context, v Viewer, items []media.Item, limit int) ([]Card, error) {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	states, err := h.Store.GetMany(ctx, v.Principal.ProfileID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Card, 0, min(limit, len(items)))
	for _, it := range items {
		if len(out) >= limit {
			break
		}
		ok, err := h.Access.CanSee(ctx, v, it)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		c, err := h.Card(ctx, it, states[it.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// AvailableRows lists the row keys this viewer can have, in default order.
func (h *Home) AvailableRows(v Viewer) []string {
	keys := []string{RowContinueWatching, RowNextUp}
	for _, id := range v.LibraryIDs {
		keys = append(keys, RecentlyAddedKey(id))
	}
	return append(keys, RowWatchlist)
}

// RowTitle is the display title for a row key.
func (h *Home) RowTitle(ctx context.Context, key string) (string, error) {
	switch key {
	case RowContinueWatching:
		return "Continue Watching", nil
	case RowNextUp:
		return "Next Up", nil
	case RowWatchlist:
		return "Watchlist", nil
	}
	if id, ok := ParseRecentlyAddedKey(key); ok {
		lib, err := h.Libraries.Get(ctx, id)
		if err != nil {
			return "", fmt.Errorf("library %d: %w", id, err)
		}
		return "Recently Added in " + lib.Name, nil
	}
	return "", fmt.Errorf("%w: unknown row %q", ErrInvalid, key)
}

// Layout is the viewer's arranged rows, hidden ones included.
func (h *Home) Layout(ctx context.Context, v Viewer) ([]RowLayout, error) {
	prefs, err := h.Store.HomeRowPrefs(ctx, v.Principal.ProfileID)
	if err != nil {
		return nil, err
	}
	arranged := ArrangeRows(h.AvailableRows(v), prefs)
	out := make([]RowLayout, 0, len(arranged))
	for _, p := range arranged {
		title, err := h.RowTitle(ctx, p.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, RowLayout{Key: p.Key, Title: title, Hidden: p.Hidden})
	}
	return out, nil
}

// Build renders the visible, non-empty rows in layout order.
func (h *Home) Build(ctx context.Context, v Viewer, limit int) ([]Row, error) {
	layout, err := h.Layout(ctx, v)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(layout))
	for _, l := range layout {
		if l.Hidden {
			continue
		}
		items, err := h.rowItems(ctx, v, l.Key, limit)
		if err != nil {
			return nil, fmt.Errorf("row %s: %w", l.Key, err)
		}
		if len(items) == 0 {
			continue
		}
		rows = append(rows, Row{Key: l.Key, Title: l.Title, Items: items})
	}
	return rows, nil
}

func (h *Home) rowItems(ctx context.Context, v Viewer, key string, limit int) ([]Card, error) {
	switch key {
	case RowContinueWatching:
		return h.ContinueWatching(ctx, v, limit)
	case RowNextUp:
		return h.NextUp(ctx, v, limit)
	case RowWatchlist:
		return h.WatchlistRow(ctx, v, limit)
	}
	if id, ok := ParseRecentlyAddedKey(key); ok {
		return h.RecentlyAdded(ctx, v, id, limit)
	}
	return nil, fmt.Errorf("%w: unknown row %q", ErrInvalid, key)
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/userdata/home_rows.go server/internal/userdata/home_rows_test.go
git commit -m "feat(userdata): recently added and watchlist rows, layout and home build"
```

---

### Task 10: HTTP module part 1: progress, watched, continue, home, row layout

**Files:**
- Create: `server/internal/userdata/http.go`
- Test: `server/internal/userdata/http_test.go`

**Interfaces:**
- Consumes: `authctx.Guard/Principal/FromContext/StaticGuard`, `httpx.WriteJSON/WriteError/DecodeJSON`, `chi`, `Store`, `Home`, `Access`, `Leaves`, `CreditsSource`
- Produces:
  ```go
  type Deps struct { Store *Store; Catalog Catalog; Libraries Libraries; Access Access; Credits CreditsSource /* nil = NoCredits */; Log *slog.Logger }
  func NewModule(d Deps) *Module   // implements app.Module, Name() == "userdata"
  ```
  Routes (all `RequireUser`, then 403 `profile_required` when `Principal.ProfileID == 0`):

  | Method & path | Body | Success | Errors |
  |---|---|---|---|
  | `PUT /me/progress/{itemId}` | `{"positionMs":n,"durationMs":n}` | 200 state JSON | 400 `invalid_id`, `invalid_body`, `not_playable`, `invalid_progress`; 404 `not_found` |
  | `POST /me/items/{itemId}/watched` | none | 204 (cascades show/season → episodes) | 400 `invalid_id`; 404 |
  | `DELETE /me/items/{itemId}/watched` | none | 204 | same |
  | `DELETE /me/continue/{itemId}` | none | 204 | 400 `not_playable`; 404 |
  | `GET /me/home?limit=20` | none | 200 `{"rows":[Row]}` | 400 `invalid_query` |
  | `GET /me/home/rows` | none | 200 `{"rows":[RowLayout]}` | |
  | `PUT /me/home/rows` | `{"rows":[{"key":"..","hidden":bool}]}` | 200 `{"rows":[RowLayout]}` | 400 `invalid_body`, `invalid_rows` |

  State JSON: `{"itemId","positionMs","durationMs","watched","playCount","lastPlayed"?, "hiddenFromContinue"}`.
  Items the viewer can't see answer 404 (not 403), so hidden items can't be probed.
  Unexported helpers used by Task 11: `(*Module) viewer`, `visibleItem`, `internal`, `queryInt`, `stateJSON`.

- [ ] **Step 1: Write the failing tests**

`server/internal/userdata/http_test.go`:
```go
package userdata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/library"
)

func (f *fixture) api(t *testing.T, p authctx.Principal, a Access) http.Handler {
	t.Helper()
	m := NewModule(Deps{Store: f.store, Catalog: f.media, Libraries: library.NewStore(f.d), Access: a,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if m.Name() != "userdata" {
		t.Fatalf("Name() = %q", m.Name())
	}
	r := chi.NewRouter()
	m.Mount(r, authctx.StaticGuard{P: p})
	return r
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	return rec
}

func id(n int64) string { return strconv.FormatInt(n, 10) }

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func TestProfileRequired(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, authctx.Principal{UserID: f.userID}, f.access([]int64{f.libMovies}, "", nil))
	rec := call(h, "GET", "/me/home", "")
	if rec.Code != 403 || errCode(t, rec) != "profile_required" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestPutProgress(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies, f.libTV}, "", nil))
	rec := call(h, "PUT", "/me/progress/"+id(f.movie.ID), `{"positionMs":1800000,"durationMs":6000000}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var st struct {
		ItemID     int64 `json:"itemId"`
		PositionMs int64 `json:"positionMs"`
		Watched    bool  `json:"watched"`
	}
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.ItemID != f.movie.ID || st.PositionMs != 1_800_000 || st.Watched {
		t.Fatalf("body %s", rec.Body.String())
	}
	rec = call(h, "PUT", "/me/progress/"+id(f.movie.ID), `{"positionMs":5800000,"durationMs":6000000}`)
	json.Unmarshal(rec.Body.Bytes(), &st)
	if !st.Watched || st.PositionMs != 0 {
		t.Fatalf("not watched: %s", rec.Body.String())
	}

	for _, c := range []struct {
		path, body, code string
		status          int
	}{
		{"/me/progress/" + id(f.show.ID), `{"positionMs":1,"durationMs":2}`, "not_playable", 400},
		{"/me/progress/abc", `{"positionMs":1,"durationMs":2}`, "invalid_id", 400},
		{"/me/progress/999999", `{"positionMs":1,"durationMs":2}`, "not_found", 404},
		{"/me/progress/" + id(f.movie.ID), `{"positionMs":9,"durationMs":2}`, "invalid_progress", 400},
		{"/me/progress/" + id(f.movie.ID), `{"position":1}`, "invalid_body", 400},
	} {
		rec := call(h, "PUT", c.path, c.body)
		if rec.Code != c.status || errCode(t, rec) != c.code {
			t.Fatalf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body.String())
		}
	}

	moviesHidden := f.api(t, f.principal(), f.access([]int64{f.libTV}, "", nil))
	if rec := call(moviesHidden, "PUT", "/me/progress/"+id(f.movie.ID), `{"positionMs":1,"durationMs":2}`); rec.Code != 404 {
		t.Fatalf("invisible item: %d", rec.Code)
	}
}

func TestMarkShowWatchedCascades(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies, f.libTV}, "", nil))
	if rec := call(h, "POST", "/me/items/"+id(f.show.ID)+"/watched", ""); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for key, ep := range f.eps {
		if st, _ := f.store.Get(ctx, f.profileID, ep.ID); !st.Watched {
			t.Fatalf("%s not watched", key)
		}
	}
	if rec := call(h, "DELETE", "/me/items/"+id(f.seasons[1].ID)+"/watched", ""); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if st, _ := f.store.Get(ctx, f.profileID, f.eps["S01E02"].ID); st.Watched {
		t.Fatal("season unwatch did not cascade")
	}
	if st, _ := f.store.Get(ctx, f.profileID, f.eps["S02E01"].ID); !st.Watched {
		t.Fatal("season unwatch touched another season")
	}
}

func TestHideFromContinueAndHome(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies, f.libTV}, "", nil))
	call(h, "PUT", "/me/progress/"+id(f.movie.ID), `{"positionMs":1800000,"durationMs":6000000}`)

	var home struct{ Rows []Row }
	rec := call(h, "GET", "/me/home", "")
	json.Unmarshal(rec.Body.Bytes(), &home)
	if rec.Code != 200 || len(home.Rows) == 0 || home.Rows[0].Key != RowContinueWatching || home.Rows[0].Items[0].ID != f.movie.ID {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, "DELETE", "/me/continue/"+id(f.movie.ID), ""); rec.Code != 204 {
		t.Fatalf("hide: %d", rec.Code)
	}
	home.Rows = nil
	json.Unmarshal(call(h, "GET", "/me/home", "").Body.Bytes(), &home)
	for _, r := range home.Rows {
		if r.Key == RowContinueWatching {
			t.Fatal("hidden item still in continue watching")
		}
	}
	if rec := call(h, "GET", "/me/home?limit=0", ""); rec.Code != 400 || errCode(t, rec) != "invalid_query" {
		t.Fatalf("limit=0: %d", rec.Code)
	}
}

func TestHomeRowsEndpoints(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies, f.libTV}, "", nil))
	var layout struct{ Rows []RowLayout }
	rec := call(h, "GET", "/me/home/rows", "")
	json.Unmarshal(rec.Body.Bytes(), &layout)
	if rec.Code != 200 || len(layout.Rows) != 5 || layout.Rows[0].Key != RowContinueWatching {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = call(h, "PUT", "/me/home/rows", `{"rows":[{"key":"watchlist","hidden":false},{"key":"next_up","hidden":true}]}`)
	json.Unmarshal(rec.Body.Bytes(), &layout)
	if rec.Code != 200 || layout.Rows[0].Key != RowWatchlist || !layout.Rows[1].Hidden {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = call(h, "PUT", "/me/home/rows", `{"rows":[{"key":"trending","hidden":false}]}`)
	if rec.Code != 400 || errCode(t, rec) != "invalid_rows" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewModule`

- [ ] **Step 3: Implement**

`server/internal/userdata/http.go`:
```go
package userdata

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// Deps are the userdata module's collaborators.
type Deps struct {
	Store     *Store
	Catalog   Catalog
	Libraries Libraries
	Access    Access
	Credits   CreditsSource // nil = NoCredits
	Log       *slog.Logger
}

// Module serves /api/v1/me/... user-data routes.
type Module struct {
	store   *Store
	catalog Catalog
	access  Access
	credits CreditsSource
	home    *Home
	log     *slog.Logger
}

// NewModule builds the module.
func NewModule(d Deps) *Module {
	if d.Credits == nil {
		d.Credits = NoCredits{}
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Module{
		store: d.Store, catalog: d.Catalog, access: d.Access, credits: d.Credits, log: d.Log,
		home: &Home{Store: d.Store, Catalog: d.Catalog, Libraries: d.Libraries, Access: d.Access},
	}
}

// Name implements app.Module.
func (m *Module) Name() string { return "userdata" }

// Mount registers individual /me/... routes (no r.Route("/me"), so other modules
// can also register /me paths without a chi mount conflict).
func (m *Module) Mount(r chi.Router, g authctx.Guard) {
	r.Group(func(r chi.Router) {
		r.Use(g.RequireUser, requireProfile)
		r.Put("/me/progress/{itemId}", m.putProgress)
		r.Post("/me/items/{itemId}/watched", m.markWatched)
		r.Delete("/me/items/{itemId}/watched", m.markUnwatched)
		r.Delete("/me/continue/{itemId}", m.hideFromContinue)
		r.Get("/me/home", m.getHome)
		r.Get("/me/home/rows", m.getRows)
		r.Put("/me/home/rows", m.putRows)
	})
}

func requireProfile(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := authctx.FromContext(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if p.ProfileID == 0 {
			httpx.WriteError(w, http.StatusForbidden, "profile_required", "select a profile first")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Module) internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	m.log.Error("userdata: "+what, "err", err, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func (m *Module) viewer(w http.ResponseWriter, r *http.Request) (Viewer, bool) {
	p, _ := authctx.FromContext(r.Context())
	v, err := m.access.Viewer(r.Context(), p)
	if err != nil {
		m.internal(w, r, "resolve viewer", err)
		return Viewer{}, false
	}
	return v, true
}

// visibleItem parses the {param} item ID, loads it and checks access.
// Missing and invisible items both answer 404.
func (m *Module) visibleItem(w http.ResponseWriter, r *http.Request, param string) (media.Item, Viewer, bool) {
	v, ok := m.viewer(w, r)
	if !ok {
		return media.Item{}, Viewer{}, false
	}
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "item ID must be a positive integer")
		return media.Item{}, Viewer{}, false
	}
	it, err := m.catalog.GetItem(r.Context(), id)
	if errors.Is(err, media.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such item")
		return media.Item{}, Viewer{}, false
	}
	if err != nil {
		m.internal(w, r, "get item", err)
		return media.Item{}, Viewer{}, false
	}
	visible, err := m.access.CanSee(r.Context(), v, it)
	if err != nil {
		m.internal(w, r, "access check", err)
		return media.Item{}, Viewer{}, false
	}
	if !visible {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such item")
		return media.Item{}, Viewer{}, false
	}
	return it, v, true
}

// queryInt reads an optional integer query parameter in [1, max].
func queryInt(r *http.Request, name string, def, max int) (int, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > max {
		return 0, false
	}
	return n, true
}

type stateBody struct {
	ItemID             int64      `json:"itemId"`
	PositionMs         int64      `json:"positionMs"`
	DurationMs         int64      `json:"durationMs"`
	Watched            bool       `json:"watched"`
	PlayCount          int        `json:"playCount"`
	LastPlayed         *time.Time `json:"lastPlayed,omitempty"`
	HiddenFromContinue bool       `json:"hiddenFromContinue"`
}

func stateJSON(st State) stateBody {
	b := stateBody{ItemID: st.ItemID, PositionMs: st.PositionMs, DurationMs: st.DurationMs,
		Watched: st.Watched, PlayCount: st.PlayCount, HiddenFromContinue: st.HiddenFromContinue}
	if !st.LastPlayed.IsZero() {
		lp := st.LastPlayed
		b.LastPlayed = &lp
	}
	return b
}

func (m *Module) putProgress(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	if !IsPlayable(it.Kind) {
		httpx.WriteError(w, http.StatusBadRequest, "not_playable", "progress applies to movies, episodes and videos")
		return
	}
	var body struct {
		PositionMs int64 `json:"positionMs"`
		DurationMs int64 `json:"durationMs"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	credits, has, err := m.credits.CreditsStartMs(r.Context(), it.ID)
	if err != nil {
		m.internal(w, r, "credits marker", err)
		return
	}
	if !has {
		credits = 0
	}
	st, err := m.store.RecordProgress(r.Context(), v.Principal.ProfileID, it.ID, body.PositionMs, body.DurationMs, credits)
	if errors.Is(err, ErrInvalid) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_progress", err.Error())
		return
	}
	if err != nil {
		m.internal(w, r, "record progress", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stateJSON(st))
}

func (m *Module) markWatched(w http.ResponseWriter, r *http.Request)   { m.setWatched(w, r, true) }
func (m *Module) markUnwatched(w http.ResponseWriter, r *http.Request) { m.setWatched(w, r, false) }

func (m *Module) setWatched(w http.ResponseWriter, r *http.Request, watched bool) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	leaves, err := Leaves(r.Context(), m.catalog, it)
	if err != nil {
		m.internal(w, r, "expand item", err)
		return
	}
	ids := make([]int64, len(leaves))
	for i, l := range leaves {
		ids[i] = l.ID
	}
	if err := m.store.SetWatched(r.Context(), v.Principal.ProfileID, ids, watched); err != nil {
		m.internal(w, r, "set watched", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) hideFromContinue(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	if !IsPlayable(it.Kind) {
		httpx.WriteError(w, http.StatusBadRequest, "not_playable", "only movies, episodes and videos appear in Continue Watching")
		return
	}
	if err := m.store.HideFromContinue(r.Context(), v.Principal.ProfileID, it.ID); err != nil {
		m.internal(w, r, "hide from continue", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) getHome(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(r, "limit", DefaultRowLimit, 50)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_query", "limit must be 1-50")
		return
	}
	v, ok := m.viewer(w, r)
	if !ok {
		return
	}
	rows, err := m.home.Build(r.Context(), v, limit)
	if err != nil {
		m.internal(w, r, "build home", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

func (m *Module) getRows(w http.ResponseWriter, r *http.Request) {
	v, ok := m.viewer(w, r)
	if !ok {
		return
	}
	layout, err := m.home.Layout(r.Context(), v)
	if err != nil {
		m.internal(w, r, "home layout", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": layout})
}

func (m *Module) putRows(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rows []RowPref `json:"rows"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	v, ok := m.viewer(w, r)
	if !ok {
		return
	}
	if err := m.store.SetHomeRowPrefs(r.Context(), v.Principal.ProfileID, body.Rows); err != nil {
		if errors.Is(err, ErrInvalid) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_rows", err.Error())
			return
		}
		m.internal(w, r, "save home rows", err)
		return
	}
	m.getRows(w, r)
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/userdata/http.go server/internal/userdata/http_test.go
git commit -m "feat(userdata): progress, watched, continue-watching and home API"
```

---

### Task 11: HTTP module part 2: watchlist, ratings, history, track prefs

**Files:**
- Create: `server/internal/userdata/http_lists.go`
- Modify: `server/internal/userdata/http.go` (add 9 route lines inside `Mount`)
- Test: `server/internal/userdata/http_lists_test.go`

**Interfaces:**
- Consumes: Task 10 helpers (`visibleItem`, `viewer`, `internal`, `queryInt`), `Home.Card`, `Store` list methods, `TrackPrefs`
- Produces routes:

  | Method & path | Body | Success | Errors |
  |---|---|---|---|
  | `GET /me/watchlist?limit=50&offset=0` | | 200 `{"items":[Card],"nextOffset":n|null}` | 400 `invalid_query` |
  | `PUT /me/watchlist/{itemId}` | | 204 | 404 |
  | `DELETE /me/watchlist/{itemId}` | | 204 | 404 |
  | `GET /me/ratings/{itemId}` | | 200 `{"rating":x}` | 404 `not_rated` |
  | `PUT /me/ratings/{itemId}` | `{"rating":0..10}` | 204 | 400 `invalid_rating` |
  | `DELETE /me/ratings/{itemId}` | | 204 | |
  | `GET /me/history?limit=50&cursor=..` | | 200 `{"entries":[{"id","watchedAt","source","item":Card}],"nextCursor":".."|null}` | 400 `invalid_query` |
  | `GET /me/track-prefs/{showId}` | | 200 track prefs JSON + `"saved":bool` | 400 `not_a_show` |
  | `PUT /me/track-prefs/{showId}` | track prefs JSON (no `saved`) | 200 track prefs JSON | 400 `invalid_track_prefs` |

  Track prefs JSON: `{"audioLanguage","audioTitle","subtitleLanguage","subtitleKind","subtitleTitle","subtitlesOff"}`.
  History entries whose item was deleted or is no longer visible are skipped.

- [ ] **Step 1: Write the failing tests**

`server/internal/userdata/http_lists_test.go`:
```go
package userdata

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestWatchlistEndpoints(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies, f.libTV}, "", nil))
	if rec := call(h, "PUT", "/me/watchlist/"+id(f.movie.ID), ""); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	f.clock.Advance(time.Minute)
	call(h, "PUT", "/me/watchlist/"+id(f.show.ID), "")
	var page struct {
		Items      []Card
		NextOffset *int `json:"nextOffset"`
	}
	rec := call(h, "GET", "/me/watchlist?limit=1", "")
	json.Unmarshal(rec.Body.Bytes(), &page)
	if rec.Code != 200 || len(page.Items) != 1 || page.Items[0].ID != f.show.ID || page.NextOffset == nil || *page.NextOffset != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	page = struct {
		Items      []Card
		NextOffset *int `json:"nextOffset"`
	}{}
	json.Unmarshal(call(h, "GET", "/me/watchlist?limit=10", "").Body.Bytes(), &page)
	if len(page.Items) != 2 || page.NextOffset != nil {
		t.Fatalf("full page: %+v", page)
	}
	call(h, "DELETE", "/me/watchlist/"+id(f.show.ID), "")
	if got, _ := f.store.Watchlist(context.Background(), f.profileID, 10, 0); len(got) != 1 {
		t.Fatalf("delete: %+v", got)
	}
	if rec := call(h, "GET", "/me/watchlist?offset=-1", ""); rec.Code != 400 {
		t.Fatalf("bad offset: %d", rec.Code)
	}
}

func TestRatingEndpoints(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies}, "", nil))
	path := "/me/ratings/" + id(f.movie.ID)
	if rec := call(h, "GET", path, ""); rec.Code != 404 || errCode(t, rec) != "not_rated" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, "PUT", path, `{"rating":8.5}`); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec := call(h, "GET", path, "")
	var body struct{ Rating float64 }
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body.Rating != 8.5 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, "PUT", path, `{"rating":11}`); rec.Code != 400 || errCode(t, rec) != "invalid_rating" {
		t.Fatalf("%d", rec.Code)
	}
	call(h, "DELETE", path, "")
	if rec := call(h, "GET", path, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

func TestHistoryEndpoint(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libTV}, "", nil))
	f.watch(t, f.movie.ID) // movie library not visible: skipped in output
	f.watch(t, f.eps["S01E01"].ID)
	f.watch(t, f.eps["S01E02"].ID)
	type entry struct {
		ID        int64
		WatchedAt time.Time
		Source    string
		Item      Card
	}
	var page struct {
		Entries    []entry
		NextCursor *string `json:"nextCursor"`
	}
	rec := call(h, "GET", "/me/history?limit=2", "")
	json.Unmarshal(rec.Body.Bytes(), &page)
	if rec.Code != 200 || len(page.Entries) != 2 || page.Entries[0].Item.ID != f.eps["S01E02"].ID || page.NextCursor == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	cursor := *page.NextCursor
	page.Entries, page.NextCursor = nil, nil
	json.Unmarshal(call(h, "GET", "/me/history?limit=2&cursor="+cursor, "").Body.Bytes(), &page)
	if len(page.Entries) != 0 || page.NextCursor != nil {
		t.Fatalf("second page should only hold the invisible movie: %+v", page)
	}
	if rec := call(h, "GET", "/me/history?cursor=zzz", ""); rec.Code != 400 {
		t.Fatalf("bad cursor: %d", rec.Code)
	}
}

func TestTrackPrefsEndpoints(t *testing.T) {
	f := newFixture(t)
	h := f.api(t, f.principal(), f.access([]int64{f.libMovies, f.libTV}, "", nil))
	path := "/me/track-prefs/" + id(f.show.ID)
	var tp struct {
		AudioLanguage string
		SubtitleKind  string
		SubtitlesOff  bool
		Saved         bool
	}
	rec := call(h, "GET", path, "")
	json.Unmarshal(rec.Body.Bytes(), &tp)
	if rec.Code != 200 || tp.Saved {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	body := `{"audioLanguage":"jpn","audioTitle":"","subtitleLanguage":"eng","subtitleKind":"signs_songs","subtitleTitle":"","subtitlesOff":false}`
	rec = call(h, "PUT", path, body)
	json.Unmarshal(rec.Body.Bytes(), &tp)
	if rec.Code != 200 || !tp.Saved || tp.AudioLanguage != "jpn" || tp.SubtitleKind != "signs_songs" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, "PUT", path, `{"subtitleKind":"karaoke"}`); rec.Code != 400 || errCode(t, rec) != "invalid_track_prefs" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, "GET", "/me/track-prefs/"+id(f.movie.ID), ""); rec.Code != 400 || errCode(t, rec) != "not_a_show" {
		t.Fatalf("movie: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL. The routes are not registered, so `TestWatchlistEndpoints` gets `405`/`404` instead of `204`.

- [ ] **Step 3: Register the routes**

In `server/internal/userdata/http.go`, inside `Mount`, after the line
`r.Put("/me/home/rows", m.putRows)` add:
```go
		r.Get("/me/watchlist", m.getWatchlist)
		r.Put("/me/watchlist/{itemId}", m.addWatchlist)
		r.Delete("/me/watchlist/{itemId}", m.removeWatchlist)
		r.Get("/me/ratings/{itemId}", m.getRating)
		r.Put("/me/ratings/{itemId}", m.putRating)
		r.Delete("/me/ratings/{itemId}", m.deleteRating)
		r.Get("/me/history", m.getHistory)
		r.Get("/me/track-prefs/{showId}", m.getTrackPrefs)
		r.Put("/me/track-prefs/{showId}", m.putTrackPrefs)
```

- [ ] **Step 4: Implement the handlers**

`server/internal/userdata/http_lists.go`:
```go
package userdata

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func (m *Module) getWatchlist(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(r, "limit", 50, 200)
	offset := 0
	if s := r.URL.Query().Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			ok = false
		}
		offset = n
	}
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_query", "limit must be 1-200 and offset >= 0")
		return
	}
	v, ok := m.viewer(w, r)
	if !ok {
		return
	}
	entries, err := m.store.Watchlist(r.Context(), v.Principal.ProfileID, limit, offset)
	if err != nil {
		m.internal(w, r, "watchlist", err)
		return
	}
	cards := make([]Card, 0, len(entries))
	for _, e := range entries {
		it, visible, err := m.home.visible(r.Context(), v, e.ItemID)
		if err != nil {
			m.internal(w, r, "watchlist item", err)
			return
		}
		if !visible {
			continue
		}
		st, err := m.store.Get(r.Context(), v.Principal.ProfileID, it.ID)
		if err != nil {
			m.internal(w, r, "watchlist state", err)
			return
		}
		c, err := m.home.Card(r.Context(), it, st)
		if err != nil {
			m.internal(w, r, "watchlist card", err)
			return
		}
		cards = append(cards, c)
	}
	var next *int
	if len(entries) == limit {
		n := offset + limit
		next = &n
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": cards, "nextOffset": next})
}

func (m *Module) addWatchlist(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	if err := m.store.AddToWatchlist(r.Context(), v.Principal.ProfileID, it.ID); err != nil {
		m.internal(w, r, "add to watchlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) removeWatchlist(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	if err := m.store.RemoveFromWatchlist(r.Context(), v.Principal.ProfileID, it.ID); err != nil {
		m.internal(w, r, "remove from watchlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) getRating(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	rating, rated, err := m.store.Rating(r.Context(), v.Principal.ProfileID, it.ID)
	if err != nil {
		m.internal(w, r, "get rating", err)
		return
	}
	if !rated {
		httpx.WriteError(w, http.StatusNotFound, "not_rated", "you have not rated this item")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]float64{"rating": rating})
}

func (m *Module) putRating(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	var body struct {
		Rating *float64 `json:"rating"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || body.Rating == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", `body must be {"rating": 0-10}`)
		return
	}
	if err := m.store.SetRating(r.Context(), v.Principal.ProfileID, it.ID, *body.Rating); err != nil {
		if errors.Is(err, ErrInvalid) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_rating", err.Error())
			return
		}
		m.internal(w, r, "set rating", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) deleteRating(w http.ResponseWriter, r *http.Request) {
	it, v, ok := m.visibleItem(w, r, "itemId")
	if !ok {
		return
	}
	if err := m.store.DeleteRating(r.Context(), v.Principal.ProfileID, it.ID); err != nil {
		m.internal(w, r, "delete rating", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type historyEntryJSON struct {
	ID        int64     `json:"id"`
	WatchedAt time.Time `json:"watchedAt"`
	Source    string    `json:"source"`
	Item      Card      `json:"item"`
}

func (m *Module) getHistory(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(r, "limit", 50, 200)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_query", "limit must be 1-200")
		return
	}
	var cursor HistoryCursor
	if s := r.URL.Query().Get("cursor"); s != "" {
		c, err := ParseHistoryCursor(s)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_query", err.Error())
			return
		}
		cursor = c
	}
	v, ok := m.viewer(w, r)
	if !ok {
		return
	}
	entries, err := m.store.History(r.Context(), v.Principal.ProfileID, cursor, limit)
	if err != nil {
		m.internal(w, r, "history", err)
		return
	}
	out := make([]historyEntryJSON, 0, len(entries))
	for _, e := range entries {
		it, visible, err := m.home.visible(r.Context(), v, e.ItemID)
		if err != nil {
			m.internal(w, r, "history item", err)
			return
		}
		if !visible {
			continue
		}
		c, err := m.home.Card(r.Context(), it, State{})
		if err != nil {
			m.internal(w, r, "history card", err)
			return
		}
		out = append(out, historyEntryJSON{ID: e.ID, WatchedAt: e.WatchedAt, Source: e.Source, Item: c})
	}
	var next *string
	if len(entries) == limit {
		s := entries[len(entries)-1].Cursor().String()
		next = &s
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": out, "nextCursor": next})
}

type trackPrefsIn struct {
	AudioLanguage    string `json:"audioLanguage"`
	AudioTitle       string `json:"audioTitle"`
	SubtitleLanguage string `json:"subtitleLanguage"`
	SubtitleKind     string `json:"subtitleKind"`
	SubtitleTitle    string `json:"subtitleTitle"`
	SubtitlesOff     bool   `json:"subtitlesOff"`
}

type trackPrefsOut struct {
	trackPrefsIn
	Saved bool `json:"saved"`
}

func trackPrefsJSON(p TrackPrefs, saved bool) trackPrefsOut {
	return trackPrefsOut{trackPrefsIn: trackPrefsIn{
		AudioLanguage: p.AudioLanguage, AudioTitle: p.AudioTitle, SubtitleLanguage: p.SubtitleLanguage,
		SubtitleKind: p.SubtitleKind, SubtitleTitle: p.SubtitleTitle, SubtitlesOff: p.SubtitlesOff,
	}, Saved: saved}
}

func (m *Module) showParam(w http.ResponseWriter, r *http.Request) (media.Item, Viewer, bool) {
	it, v, ok := m.visibleItem(w, r, "showId")
	if !ok {
		return media.Item{}, Viewer{}, false
	}
	if it.Kind != kindShow {
		httpx.WriteError(w, http.StatusBadRequest, "not_a_show", "track preferences are saved per show")
		return media.Item{}, Viewer{}, false
	}
	return it, v, true
}

func (m *Module) getTrackPrefs(w http.ResponseWriter, r *http.Request) {
	show, v, ok := m.showParam(w, r)
	if !ok {
		return
	}
	p, saved, err := m.store.TrackPrefs(r.Context(), v.Principal.ProfileID, show.ID)
	if err != nil {
		m.internal(w, r, "get track prefs", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, trackPrefsJSON(p, saved))
}

func (m *Module) putTrackPrefs(w http.ResponseWriter, r *http.Request) {
	show, v, ok := m.showParam(w, r)
	if !ok {
		return
	}
	var in trackPrefsIn
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	p := TrackPrefs{ProfileID: v.Principal.ProfileID, ShowID: show.ID,
		AudioLanguage: in.AudioLanguage, AudioTitle: in.AudioTitle, SubtitleLanguage: in.SubtitleLanguage,
		SubtitleKind: in.SubtitleKind, SubtitleTitle: in.SubtitleTitle, SubtitlesOff: in.SubtitlesOff}
	if err := m.store.SetTrackPrefs(r.Context(), p); err != nil {
		if errors.Is(err, ErrInvalid) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_track_prefs", err.Error())
			return
		}
		m.internal(w, r, "set track prefs", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, trackPrefsJSON(p, true))
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/userdata/http.go server/internal/userdata/http_lists.go server/internal/userdata/http_lists_test.go
git commit -m "feat(userdata): watchlist, ratings, history and track-preference API"
```

---

### Task 12: Plex Media Server client, plex.tv token lookup, recorded fixtures

**Files:**
- Create: `server/internal/importer/plex/types.go`, `server/internal/importer/plex/client.go`, `server/internal/importer/plex/plextv.go`
- Create: `server/internal/importer/plex/testdata/` (12 JSON files + `shared_servers.xml`, listed below)
- Test: `server/internal/importer/plex/fakepms_test.go`, `server/internal/importer/plex/client_test.go`

**Interfaces:**
- Consumes: stdlib only
- Produces:
  ```go
  var ErrUnauthorized = errors.New("plex rejected the token")
  const OwnerAccountID int64 = 1
  const DefaultPlexTVURL = "https://plex.tv"
  type Identity struct { MachineIdentifier, Version string }
  type Account struct { ID int64 `json:"id"`; Name string `json:"name"` }
  type Section struct { Key, Type, Title string; Location []Location } // Type: movie|show|artist|photo
  type Metadata struct { RatingKey, Type, Title string; Year int; GUID string; GUIDs []GUID;
      ViewCount int; ViewOffset, LastViewedAt int64; UserRating float64; Duration int64;
      ParentIndex, Index *int; GrandparentRatingKey, GrandparentTitle string; Media []Media;
      ViewedAt, AccountID int64 /* history rows */ }
  func (m Metadata) ExternalIDs() map[string]string // "tmdb","tvdb","imdb" from Guid[]; legacy agent guid for movies/shows
  func (m Metadata) Files() []string
  func NewClient(baseURL, token string, hc *http.Client) (*Client, error)
  func (c *Client) WithToken(token string) *Client
  func (c *Client) Identity(ctx) (Identity, error)
  func (c *Client) Accounts(ctx) ([]Account, error)
  func (c *Client) Sections(ctx) ([]Section, error)
  func (c *Client) Items(ctx, sectionKey string, typ int) ([]Metadata, error) // type 1 movie, 2 show, 4 episode; paged
  func (c *Client) History(ctx, accountID int64) ([]Metadata, error)          // paged
  func SharedUserTokens(ctx, hc *http.Client, plexTVURL, ownerToken, machineID string) (map[int64]string, error)
  ```
  Test helper `newFakePMS(t) *fakePMS` (`.URL()`, `.Client()`, `.requests`). It serves fixtures keyed by token, path,
  `type` and `accountID`, requires `Accept: application/json`, rejects tokens in the query string, and
  answers 401 for unknown tokens. Tokens: `owner-token` (owner), `tokA` (lisim).

**Fixture scenario** (used again by Tasks 13–15). Plex has Movies (section 1, `/data/movies`),
TV Shows (section 2, `/data/tv`) and Music (section 3, ignored). Accounts: `1` zac (owner),
`2345678` lisim (has a shared-server token `tokA`), `3456789` ondo (no token: history only).

| ratingKey | Item | Matching path in Lisimflix (Task 13 fixture) | Owner (zac) state |
|---|---|---|---|
| 101 | The Matrix (1999), Guid imdb/tmdb/tvdb | external ID (tmdb 603) | viewCount 2, userRating 9 |
| 102 | Heat (1995), local item, no Guid | file path `/data/movies/...` → `/media/movies/...` | viewOffset 3600000 / 10200000 |
| 103 | Alien (1979), no Guid, file unknown to Lisimflix | title + year | viewCount 1 |
| 104 | Obscure Film (2001) | none → `no_match` | viewCount 1 |
| 105 | Solaris (2002) | two Lisimflix "Solaris (2002)" → `ambiguous` | viewCount 1 |
| 106 | Arrival (2016) | (no state, skipped) | none |
| 107 | Blade Runner (1982), legacy `com.plexapp.agents.imdb://tt0083658` | external ID (imdb via legacy guid) | viewCount 1 |
| 200 | Firefly (show), Guid tvdb 78874 | show by tvdb, then S/E numbers | n/a |
| 201 | Firefly S01E01 | show + S01E01 | viewCount 1 |
| 202 | Firefly S01E02 | show + S01E02 | viewOffset 600000 / 2580000 |
| 203 | Firefly S01E03 | | none |
| 999 | (only in history) Deleted Movie | `not_in_plex_library` | history 1 |

lisim (`tokA` listings): 101 viewOffset 1200000; 201 viewCount 1. History: 201 once.
ondo (history only): 101 twice (1758000000, 1759000000), 107 once.

- [ ] **Step 1 (optional, on zraid): refresh fixtures from the real server**

The fixtures below are hand-written in the exact PMS JSON shape. To compare them against
the household Plex server, run these commands. The token is read from an env var and never written to disk:
```bash
read -rs PLEX_TOKEN   # paste the server admin token (Plex Web > any item > Get Info > View XML > X-Plex-Token)
P=http://zraid:32400
for p in /identity /accounts /library/sections "/library/sections/1/all?type=1&includeGuids=1&X-Plex-Container-Size=2" \
         "/status/sessions/history/all?accountID=1&sort=viewedAt:desc&X-Plex-Container-Size=2"; do
  curl -s -H "Accept: application/json" -H "X-Plex-Token: $PLEX_TOKEN" "$P$p" | head -c 1500; echo; echo ---
done
unset PLEX_TOKEN
```
Expected: the same field names and types as the fixtures (`ratingKey` string, `viewOffset`/`duration` ms
numbers, `lastViewedAt`/`viewedAt` unix seconds, `Guid` array, `accountID` number). If a name differs,
fix `types.go` and the fixtures. **Never commit real responses.** They contain the household's titles
and plex.tv avatars.

- [ ] **Step 2: Write the fixtures**

`server/internal/importer/plex/testdata/identity.json`:
```json
{"MediaContainer":{"size":0,"apiVersion":"1.1.1","claimed":true,"machineIdentifier":"abc123machine","version":"1.42.2.10156-f737b826c"}}
```

`server/internal/importer/plex/testdata/accounts.json`:
```json
{"MediaContainer":{"size":4,"identifier":"com.plexapp.system.accounts","Account":[
{"id":0,"key":"/accounts/0","name":"","defaultAudioLanguage":"en","autoSelectAudio":true,"defaultSubtitleLanguage":"en","subtitleMode":0,"thumb":""},
{"id":1,"key":"/accounts/1","name":"zac","defaultAudioLanguage":"en","autoSelectAudio":true,"defaultSubtitleLanguage":"en","subtitleMode":1,"thumb":"https://plex.tv/users/1a2b3c/avatar?c=1700000000"},
{"id":2345678,"key":"/accounts/2345678","name":"lisim","defaultAudioLanguage":"en","autoSelectAudio":true,"defaultSubtitleLanguage":"en","subtitleMode":1,"thumb":"https://plex.tv/users/4d5e6f/avatar?c=1700000000"},
{"id":3456789,"key":"/accounts/3456789","name":"ondo","defaultAudioLanguage":"ja","autoSelectAudio":true,"defaultSubtitleLanguage":"en","subtitleMode":1,"thumb":""}
]}}
```

`server/internal/importer/plex/testdata/sections.json`:
```json
{"MediaContainer":{"size":3,"allowSync":false,"title1":"Plex Library","Directory":[
{"allowSync":true,"art":"/:/resources/movie-fanart.jpg","composite":"/library/sections/1/composite/1760000000","filters":true,"refreshing":false,"thumb":"/:/resources/movie.png","key":"1","type":"movie","title":"Movies","agent":"tv.plex.agents.movie","scanner":"Plex Movie","language":"en-US","uuid":"6e1f8c2a-0000-4000-8000-000000000001","updatedAt":1760000000,"createdAt":1600000000,"scannedAt":1760000000,"content":true,"directory":true,"contentChangedAt":1234567,"hidden":0,"Location":[{"id":1,"path":"/data/movies"}]},
{"allowSync":true,"art":"/:/resources/show-fanart.jpg","composite":"/library/sections/2/composite/1760000000","filters":true,"refreshing":false,"thumb":"/:/resources/show.png","key":"2","type":"show","title":"TV Shows","agent":"tv.plex.agents.series","scanner":"Plex TV Series","language":"en-US","uuid":"6e1f8c2a-0000-4000-8000-000000000002","updatedAt":1760000000,"createdAt":1600000000,"scannedAt":1760000000,"content":true,"directory":true,"contentChangedAt":1234568,"hidden":0,"Location":[{"id":2,"path":"/data/tv"}]},
{"allowSync":true,"art":"/:/resources/artist-fanart.jpg","composite":"/library/sections/3/composite/1760000000","filters":true,"refreshing":false,"thumb":"/:/resources/artist.png","key":"3","type":"artist","title":"Music","agent":"tv.plex.agents.music","scanner":"Plex Music","language":"en-US","uuid":"6e1f8c2a-0000-4000-8000-000000000003","updatedAt":1760000000,"createdAt":1600000000,"scannedAt":1760000000,"content":true,"directory":true,"contentChangedAt":1234569,"hidden":0,"Location":[{"id":3,"path":"/data/music"}]}
]}}
```

`server/internal/importer/plex/testdata/owner_movies.json`:
```json
{"MediaContainer":{"size":7,"totalSize":7,"offset":0,"allowSync":true,"librarySectionID":1,"librarySectionTitle":"Movies","librarySectionUUID":"6e1f8c2a-0000-4000-8000-000000000001","mediaTagPrefix":"/system/bundle/media/flags/","viewGroup":"movie","Metadata":[
{"ratingKey":"101","key":"/library/metadata/101","guid":"plex://movie/5d7768ba96b655001fdc0408","studio":"Warner Bros. Pictures","type":"movie","title":"The Matrix","contentRating":"R","summary":"A hacker learns the truth about reality.","rating":8.3,"audienceRating":8.5,"userRating":9.0,"viewCount":2,"lastViewedAt":1760000000,"lastRatedAt":1760000100,"year":1999,"thumb":"/library/metadata/101/thumb/1760000000","duration":8160000,"originallyAvailableAt":"1999-03-31","addedAt":1600000000,"updatedAt":1760000000,
 "Media":[{"id":1001,"duration":8160000,"bitrate":10500,"width":1920,"height":1080,"videoCodec":"h264","audioCodec":"dts","container":"mkv","videoResolution":"1080","Part":[{"id":2001,"key":"/library/parts/2001/1600000000/file.mkv","duration":8160000,"file":"/data/movies/The Matrix (1999)/The Matrix (1999) Bluray-1080p.mkv","size":10710000000,"container":"mkv"}]}],
 "Guid":[{"id":"imdb://tt0133093"},{"id":"tmdb://603"},{"id":"tvdb://169"}]},
{"ratingKey":"102","key":"/library/metadata/102","guid":"local://102","type":"movie","title":"Heat","summary":"","viewOffset":3600000,"lastViewedAt":1760100000,"year":1995,"duration":10200000,"addedAt":1600000100,"updatedAt":1760100000,
 "Media":[{"id":1002,"duration":10200000,"container":"mkv","Part":[{"id":2002,"key":"/library/parts/2002/1600000100/file.mkv","duration":10200000,"file":"/data/movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv","size":12000000000,"container":"mkv"}]}]},
{"ratingKey":"103","key":"/library/metadata/103","guid":"local://103","type":"movie","title":"Alien","viewCount":1,"lastViewedAt":1750000000,"year":1979,"duration":7020000,"addedAt":1600000200,"updatedAt":1750000000,
 "Media":[{"id":1003,"duration":7020000,"container":"mp4","Part":[{"id":2003,"key":"/library/parts/2003/1600000200/file.mp4","duration":7020000,"file":"/data/movies/Alien/alien.mp4","size":4000000000,"container":"mp4"}]}]},
{"ratingKey":"104","key":"/library/metadata/104","guid":"local://104","type":"movie","title":"Obscure Film","viewCount":1,"lastViewedAt":1740000000,"year":2001,"duration":5400000,"addedAt":1600000300,"updatedAt":1740000000,
 "Media":[{"id":1004,"duration":5400000,"container":"avi","Part":[{"id":2004,"key":"/library/parts/2004/1600000300/file.avi","duration":5400000,"file":"/data/movies/Obscure/obscure.avi","size":700000000,"container":"avi"}]}]},
{"ratingKey":"105","key":"/library/metadata/105","guid":"local://105","type":"movie","title":"Solaris","viewCount":1,"lastViewedAt":1745000000,"year":2002,"duration":5940000,"addedAt":1600000400,"updatedAt":1745000000,
 "Media":[{"id":1005,"duration":5940000,"container":"mkv","Part":[{"id":2005,"key":"/library/parts/2005/1600000400/file.mkv","duration":5940000,"file":"/data/movies/Solaris (2002)/Solaris.mkv","size":5000000000,"container":"mkv"}]}]},
{"ratingKey":"106","key":"/library/metadata/106","guid":"plex://movie/5d776d1847dd6e001f6f0e93","type":"movie","title":"Arrival","year":2016,"duration":6960000,"addedAt":1600000500,"updatedAt":1600000500,
 "Media":[{"id":1006,"duration":6960000,"container":"mkv","Part":[{"id":2006,"key":"/library/parts/2006/1600000500/file.mkv","duration":6960000,"file":"/data/movies/Arrival (2016)/Arrival (2016).mkv","size":9000000000,"container":"mkv"}]}],
 "Guid":[{"id":"imdb://tt2543164"},{"id":"tmdb://329865"}]},
{"ratingKey":"107","key":"/library/metadata/107","guid":"com.plexapp.agents.imdb://tt0083658?lang=en","type":"movie","title":"Blade Runner","viewCount":1,"lastViewedAt":1755500000,"year":1982,"duration":7020000,"addedAt":1600000600,"updatedAt":1755500000,
 "Media":[{"id":1007,"duration":7020000,"container":"mkv","Part":[{"id":2007,"key":"/library/parts/2007/1600000600/file.mkv","duration":7020000,"file":"/data/movies/Blade Runner (1982)/Blade Runner (1982).mkv","size":8000000000,"container":"mkv"}]}]}
]}}
```

`server/internal/importer/plex/testdata/owner_shows.json`:
```json
{"MediaContainer":{"size":1,"totalSize":1,"offset":0,"allowSync":true,"librarySectionID":2,"librarySectionTitle":"TV Shows","viewGroup":"show","Metadata":[
{"ratingKey":"200","key":"/library/metadata/200/children","guid":"plex://show/5d9c086c46115600200aa2fe","studio":"20th Century Fox Television","type":"show","title":"Firefly","contentRating":"TV-14","summary":"Five hundred years in the future...","index":1,"year":2002,"thumb":"/library/metadata/200/thumb/1760000000","leafCount":3,"viewedLeafCount":1,"childCount":1,"addedAt":1600001000,"updatedAt":1760300000,
 "Location":[{"path":"/data/tv/Firefly"}],
 "Guid":[{"id":"imdb://tt0303461"},{"id":"tmdb://1437"},{"id":"tvdb://78874"}]}
]}}
```

`server/internal/importer/plex/testdata/owner_episodes.json`:
```json
{"MediaContainer":{"size":3,"totalSize":3,"offset":0,"allowSync":true,"librarySectionID":2,"librarySectionTitle":"TV Shows","viewGroup":"episode","Metadata":[
{"ratingKey":"201","key":"/library/metadata/201","parentRatingKey":"210","grandparentRatingKey":"200","guid":"plex://episode/5d9c1275e9d5a1001f4ea3a1","parentGuid":"plex://season/602e67e766dfdb002c0a1b01","grandparentGuid":"plex://show/5d9c086c46115600200aa2fe","type":"episode","title":"Serenity","grandparentKey":"/library/metadata/200","parentKey":"/library/metadata/210","grandparentTitle":"Firefly","parentTitle":"Season 1","index":1,"parentIndex":1,"viewCount":1,"lastViewedAt":1760200000,"year":2002,"duration":5160000,"originallyAvailableAt":"2002-12-20","addedAt":1600001000,"updatedAt":1760200000,
 "Media":[{"id":1201,"duration":5160000,"container":"mkv","Part":[{"id":2201,"key":"/library/parts/2201/1600001000/file.mkv","duration":5160000,"file":"/data/tv/Firefly/Season 01/Firefly - S01E01 - Serenity.mkv","size":2000000000,"container":"mkv"}]}],
 "Guid":[{"id":"imdb://tt0579539"},{"id":"tmdb://63478"},{"id":"tvdb://297989"}]},
{"ratingKey":"202","key":"/library/metadata/202","parentRatingKey":"210","grandparentRatingKey":"200","guid":"plex://episode/5d9c1275e9d5a1001f4ea3a2","type":"episode","title":"The Train Job","grandparentTitle":"Firefly","parentTitle":"Season 1","index":2,"parentIndex":1,"viewOffset":600000,"lastViewedAt":1760300000,"year":2002,"duration":2580000,"addedAt":1600001000,"updatedAt":1760300000,
 "Media":[{"id":1202,"duration":2580000,"container":"mkv","Part":[{"id":2202,"key":"/library/parts/2202/1600001000/file.mkv","duration":2580000,"file":"/data/tv/Firefly/Season 01/Firefly - S01E02 - The Train Job.mkv","size":1000000000,"container":"mkv"}]}],
 "Guid":[{"id":"imdb://tt0579536"},{"id":"tmdb://63479"},{"id":"tvdb"},{"id":"tvdb://297990"}]},
{"ratingKey":"203","key":"/library/metadata/203","parentRatingKey":"210","grandparentRatingKey":"200","guid":"plex://episode/5d9c1275e9d5a1001f4ea3a3","type":"episode","title":"Bushwhacked","grandparentTitle":"Firefly","parentTitle":"Season 1","index":3,"parentIndex":1,"year":2002,"duration":2580000,"addedAt":1600001000,"updatedAt":1600001000,
 "Media":[{"id":1203,"duration":2580000,"container":"mkv","Part":[{"id":2203,"key":"/library/parts/2203/1600001000/file.mkv","duration":2580000,"file":"/data/tv/Firefly/Season 01/Firefly - S01E03 - Bushwhacked.mkv","size":1000000000,"container":"mkv"}]}],
 "Guid":[{"id":"tvdb://297991"}]}
]}}
```
(`{"id":"tvdb"}` is deliberate: an entry without `://` that `ExternalIDs` must skip, as
seen with leftovers from old agents.)

`server/internal/importer/plex/testdata/lisim_movies.json`:
```json
{"MediaContainer":{"size":2,"totalSize":2,"offset":0,"librarySectionID":1,"librarySectionTitle":"Movies","viewGroup":"movie","Metadata":[
{"ratingKey":"101","key":"/library/metadata/101","guid":"plex://movie/5d7768ba96b655001fdc0408","type":"movie","title":"The Matrix","viewOffset":1200000,"lastViewedAt":1760400000,"year":1999,"duration":8160000,
 "Media":[{"id":1001,"Part":[{"id":2001,"file":"/data/movies/The Matrix (1999)/The Matrix (1999) Bluray-1080p.mkv"}]}],
 "Guid":[{"id":"imdb://tt0133093"},{"id":"tmdb://603"}]},
{"ratingKey":"102","key":"/library/metadata/102","guid":"local://102","type":"movie","title":"Heat","year":1995,"duration":10200000,
 "Media":[{"id":1002,"Part":[{"id":2002,"file":"/data/movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv"}]}]}
]}}
```

`server/internal/importer/plex/testdata/lisim_episodes.json`:
```json
{"MediaContainer":{"size":1,"totalSize":1,"offset":0,"librarySectionID":2,"librarySectionTitle":"TV Shows","viewGroup":"episode","Metadata":[
{"ratingKey":"201","key":"/library/metadata/201","grandparentRatingKey":"200","type":"episode","title":"Serenity","grandparentTitle":"Firefly","index":1,"parentIndex":1,"viewCount":1,"lastViewedAt":1760500000,"duration":5160000,
 "Media":[{"id":1201,"Part":[{"id":2201,"file":"/data/tv/Firefly/Season 01/Firefly - S01E01 - Serenity.mkv"}]}]}
]}}
```

`server/internal/importer/plex/testdata/history_1.json`:
```json
{"MediaContainer":{"size":4,"totalSize":4,"offset":0,"Metadata":[
{"historyKey":"/status/sessions/history/9004","key":"/library/metadata/201","ratingKey":"201","librarySectionID":"2","parentKey":"/library/metadata/210","grandparentKey":"/library/metadata/200","title":"Serenity","grandparentTitle":"Firefly","type":"episode","thumb":"/library/metadata/201/thumb/1760000000","originallyAvailableAt":"2002-12-20","index":1,"parentIndex":1,"viewedAt":1760200000,"accountID":1,"deviceID":5},
{"historyKey":"/status/sessions/history/9003","key":"/library/metadata/101","ratingKey":"101","librarySectionID":"1","title":"The Matrix","type":"movie","thumb":"/library/metadata/101/thumb/1760000000","originallyAvailableAt":"1999-03-31","viewedAt":1760000000,"accountID":1,"deviceID":5},
{"historyKey":"/status/sessions/history/9002","key":"/library/metadata/999","ratingKey":"999","librarySectionID":"1","title":"Deleted Movie","type":"movie","viewedAt":1757000000,"accountID":1,"deviceID":2},
{"historyKey":"/status/sessions/history/9001","key":"/library/metadata/101","ratingKey":"101","librarySectionID":"1","title":"The Matrix","type":"movie","viewedAt":1755000000,"accountID":1,"deviceID":2}
]}}
```

`server/internal/importer/plex/testdata/history_2345678.json`:
```json
{"MediaContainer":{"size":1,"totalSize":1,"offset":0,"Metadata":[
{"historyKey":"/status/sessions/history/9010","key":"/library/metadata/201","ratingKey":"201","librarySectionID":"2","title":"Serenity","grandparentTitle":"Firefly","type":"episode","index":1,"parentIndex":1,"viewedAt":1760500000,"accountID":2345678,"deviceID":7}
]}}
```

`server/internal/importer/plex/testdata/history_3456789.json`:
```json
{"MediaContainer":{"size":3,"totalSize":3,"offset":0,"Metadata":[
{"historyKey":"/status/sessions/history/9022","key":"/library/metadata/107","ratingKey":"107","librarySectionID":"1","title":"Blade Runner","type":"movie","viewedAt":1759500000,"accountID":3456789,"deviceID":9},
{"historyKey":"/status/sessions/history/9021","key":"/library/metadata/101","ratingKey":"101","librarySectionID":"1","title":"The Matrix","type":"movie","viewedAt":1759000000,"accountID":3456789,"deviceID":9},
{"historyKey":"/status/sessions/history/9020","key":"/library/metadata/101","ratingKey":"101","librarySectionID":"1","title":"The Matrix","type":"movie","viewedAt":1758000000,"accountID":3456789,"deviceID":9}
]}}
```

`server/internal/importer/plex/testdata/shared_servers.xml`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<MediaContainer friendlyName="myPlex" identifier="com.plexapp.plugins.myplex" machineIdentifier="abc123machine" size="1">
  <SharedServer id="9876543" username="lisim" email="lisim@example.com" userID="2345678" accessToken="tokA" name="zraid" acceptedAt="1690000000" invitedAt="1689990000" allowSync="0" allowCameraUpload="0" allowChannels="0" allowTuners="0" allowSubtitleAdmin="0" owned="0" allLibraries="1" filterAll="" filterMovies="" filterMusic="" filterPhotos="" filterTelevision="">
    <Section id="11111" key="1" title="Movies" type="movie" shared="1"/>
    <Section id="11112" key="2" title="TV Shows" type="show" shared="1"/>
  </SharedServer>
</MediaContainer>
```

- [ ] **Step 3: Write the fake server and failing tests**

`server/internal/importer/plex/fakepms_test.go`:
```go
package plex

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakePMS serves recorded fixtures as a Plex Media Server and as plex.tv.
type fakePMS struct {
	t        *testing.T
	srv      *httptest.Server
	files    map[string]string
	mu       sync.Mutex
	requests []string
}

func newFakePMS(t *testing.T) *fakePMS {
	t.Helper()
	f := &fakePMS{t: t, files: map[string]string{
		"owner-token /identity":                                  "identity.json",
		"owner-token /accounts":                                  "accounts.json",
		"owner-token /library/sections":                          "sections.json",
		"owner-token /library/sections/1/all type=1":             "owner_movies.json",
		"owner-token /library/sections/2/all type=2":             "owner_shows.json",
		"owner-token /library/sections/2/all type=4":             "owner_episodes.json",
		"owner-token /status/sessions/history/all accountID=1":       "history_1.json",
		"owner-token /status/sessions/history/all accountID=2345678": "history_2345678.json",
		"owner-token /status/sessions/history/all accountID=3456789": "history_3456789.json",
		"owner-token /api/servers/abc123machine/shared_servers":      "shared_servers.xml",
		"tokA /library/sections":                                     "sections.json",
		"tokA /library/sections/1/all type=1":                        "lisim_movies.json",
		"tokA /library/sections/2/all type=4":                        "lisim_episodes.json",
	}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePMS) URL() string          { return f.srv.URL }
func (f *fakePMS) Client() *http.Client { return f.srv.Client() }

func (f *fakePMS) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("X-Plex-Token") != "" {
		f.t.Errorf("token sent in query string: %s", r.URL.String())
	}
	xml := strings.HasPrefix(r.URL.Path, "/api/servers/")
	if !xml && r.Header.Get("Accept") != "application/json" {
		http.Error(w, "want JSON", http.StatusNotAcceptable)
		return
	}
	tok := r.Header.Get("X-Plex-Token")
	key := tok + " " + r.URL.Path
	if v := q.Get("type"); v != "" {
		key += " type=" + v
	}
	if v := q.Get("accountID"); v != "" {
		key += " accountID=" + v
	}
	f.mu.Lock()
	f.requests = append(f.requests, key)
	f.mu.Unlock()
	if tok != "owner-token" && tok != "tokA" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name, ok := f.files[key]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		f.t.Errorf("fixture %s: %v", name, err)
		http.Error(w, err.Error(), 500)
		return
	}
	if xml {
		w.Header().Set("Content-Type", "application/xml")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.Write(body)
}
```

`server/internal/importer/plex/client_test.go`:
```go
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func ownerClient(t *testing.T, f *fakePMS) *Client {
	t.Helper()
	c, err := NewClient(f.URL()+"/", "owner-token", f.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewClientValidates(t *testing.T) {
	for _, u := range []string{"", "zraid:32400", "ftp://zraid", "http://"} {
		if _, err := NewClient(u, "t", nil); err == nil {
			t.Fatalf("%q accepted", u)
		}
	}
	if _, err := NewClient("http://zraid:32400", " ", nil); err == nil {
		t.Fatal("blank token accepted")
	}
}

func TestIdentityAccountsSections(t *testing.T) {
	f := newFakePMS(t)
	c := ownerClient(t, f)
	ctx := context.Background()
	id, err := c.Identity(ctx)
	if err != nil || id.MachineIdentifier != "abc123machine" {
		t.Fatalf("identity %+v %v", id, err)
	}
	accts, err := c.Accounts(ctx)
	if err != nil || len(accts) != 4 || accts[2].ID != 2345678 || accts[2].Name != "lisim" {
		t.Fatalf("accounts %+v %v", accts, err)
	}
	secs, err := c.Sections(ctx)
	if err != nil || len(secs) != 3 || secs[1].Key != "2" || secs[1].Type != "show" || secs[0].Location[0].Path != "/data/movies" {
		t.Fatalf("sections %+v %v", secs, err)
	}
}

func TestItemsAndExternalIDs(t *testing.T) {
	f := newFakePMS(t)
	c := ownerClient(t, f)
	ctx := context.Background()
	movies, err := c.Items(ctx, "1", 1)
	if err != nil || len(movies) != 7 {
		t.Fatalf("movies %d %v", len(movies), err)
	}
	byKey := map[string]Metadata{}
	for _, m := range movies {
		byKey[m.RatingKey] = m
	}
	matrix := byKey["101"]
	if matrix.ViewCount != 2 || matrix.UserRating != 9 || matrix.LastViewedAt != 1760000000 || matrix.Year != 1999 {
		t.Fatalf("matrix %+v", matrix)
	}
	if ids := matrix.ExternalIDs(); ids["tmdb"] != "603" || ids["imdb"] != "tt0133093" || ids["tvdb"] != "169" {
		t.Fatalf("matrix ids %v", ids)
	}
	if ids := byKey["107"].ExternalIDs(); ids["imdb"] != "tt0083658" || len(ids) != 1 {
		t.Fatalf("legacy guid ids %v", ids)
	}
	if ids := byKey["102"].ExternalIDs(); len(ids) != 0 {
		t.Fatalf("local item ids %v", ids)
	}
	heat := byKey["102"]
	if heat.ViewOffset != 3600000 || heat.Duration != 10200000 ||
		len(heat.Files()) != 1 || heat.Files()[0] != "/data/movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv" {
		t.Fatalf("heat %+v files %v", heat, heat.Files())
	}

	eps, err := c.Items(ctx, "2", 4)
	if err != nil || len(eps) != 3 {
		t.Fatalf("episodes %d %v", len(eps), err)
	}
	e2 := eps[1]
	if e2.GrandparentRatingKey != "200" || *e2.ParentIndex != 1 || *e2.Index != 2 || e2.ViewOffset != 600000 {
		t.Fatalf("episode %+v", e2)
	}
	if ids := e2.ExternalIDs(); ids["tvdb"] != "297990" {
		t.Fatalf("episode ids %v", ids)
	}
}

func TestHistory(t *testing.T) {
	f := newFakePMS(t)
	h, err := ownerClient(t, f).History(context.Background(), 3456789)
	if err != nil || len(h) != 3 || h[0].RatingKey != "107" || h[0].ViewedAt != 1759500000 || h[0].AccountID != 3456789 {
		t.Fatalf("history %+v %v", h, err)
	}
}

func TestUnauthorized(t *testing.T) {
	f := newFakePMS(t)
	c, _ := NewClient(f.URL(), "wrong", f.Client())
	if _, err := c.Identity(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.WithToken("tokA").Sections(context.Background()); err != nil {
		t.Fatalf("WithToken: %v", err)
	}
}

func TestPagination(t *testing.T) {
	const total = 5
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
		size, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Size"))
		var md []map[string]any
		for i := start; i < total && i < start+size; i++ {
			md = append(md, map[string]any{"ratingKey": fmt.Sprint(i), "type": "movie", "title": fmt.Sprint("M", i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{
			"size": len(md), "totalSize": total, "offset": start, "Metadata": md}})
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "t", srv.Client())
	c.PageSize = 2
	items, err := c.Items(context.Background(), "1", 1)
	if err != nil || len(items) != total || items[4].RatingKey != "4" {
		t.Fatalf("items %+v %v", items, err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestSharedUserTokens(t *testing.T) {
	f := newFakePMS(t)
	toks, err := SharedUserTokens(context.Background(), f.Client(), f.URL(), "owner-token", "abc123machine")
	if err != nil || len(toks) != 1 || toks[2345678] != "tokA" {
		t.Fatalf("tokens %v %v", toks, err)
	}
	if _, err := SharedUserTokens(context.Background(), f.Client(), f.URL(), "bad", "abc123machine"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token err = %v", err)
	}
}
```

- [ ] **Step 4: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewClient`

- [ ] **Step 5: Implement**

`server/internal/importer/plex/types.go`:
```go
// Package plex imports watch state from a Plex Media Server into Lisimflix
// profiles: a small PMS JSON client, an item matcher, the import engine and an
// admin-only background job API.
package plex

import "strings"

// OwnerAccountID is the server owner's account ID in PMS /accounts.
const OwnerAccountID int64 = 1

// mediaContainer is the envelope of every PMS JSON response.
type mediaContainer struct {
	MediaContainer struct {
		Size              int        `json:"size"`
		TotalSize         int        `json:"totalSize"`
		Offset            int        `json:"offset"`
		MachineIdentifier string     `json:"machineIdentifier"`
		Version           string     `json:"version"`
		Account           []Account  `json:"Account"`
		Directory         []Section  `json:"Directory"`
		Metadata          []Metadata `json:"Metadata"`
	} `json:"MediaContainer"`
}

// Identity is GET /identity.
type Identity struct {
	MachineIdentifier string
	Version           string
}

// Account is one entry of GET /accounts. ID 0 is a system account.
type Account struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Location is a library folder on the Plex host.
type Location struct {
	Path string `json:"path"`
}

// Section is one library from GET /library/sections.
type Section struct {
	Key      string     `json:"key"`
	Type     string     `json:"type"` // movie | show | artist | photo
	Title    string     `json:"title"`
	Location []Location `json:"Location"`
}

// GUID is one entry of the Guid array, e.g. {"id":"tmdb://603"}.
type GUID struct {
	ID string `json:"id"`
}

// Part is one file of a Media version.
type Part struct {
	File string `json:"file"`
}

// Media is one version of an item.
type Media struct {
	Part []Part `json:"Part"`
}

// Metadata is a library item (movie, show, episode) or a history row.
// Watch fields belong to the account whose token made the request.
type Metadata struct {
	RatingKey            string  `json:"ratingKey"`
	Type                 string  `json:"type"` // movie | show | episode
	Title                string  `json:"title"`
	Year                 int     `json:"year"`
	GUID                 string  `json:"guid"` // plex://... or legacy com.plexapp.agents.*://...
	GUIDs                []GUID  `json:"Guid"`
	ViewCount            int     `json:"viewCount"`
	ViewOffset           int64   `json:"viewOffset"`   // ms
	LastViewedAt         int64   `json:"lastViewedAt"` // unix seconds
	UserRating           float64 `json:"userRating"`   // 0..10
	Duration             int64   `json:"duration"`     // ms
	ParentIndex          *int    `json:"parentIndex"`  // season number (episodes)
	Index                *int    `json:"index"`        // episode number (episodes)
	GrandparentRatingKey string  `json:"grandparentRatingKey"`
	GrandparentTitle     string  `json:"grandparentTitle"`
	Media                []Media `json:"Media"`
	ViewedAt             int64   `json:"viewedAt"`  // history rows: unix seconds
	AccountID            int64   `json:"accountID"` // history rows
}

// ExternalIDs returns "tmdb", "tvdb" and "imdb" IDs from the Guid array and, for
// movies and shows, from a legacy agent guid. For episodes the IDs are episode IDs.
func (m Metadata) ExternalIDs() map[string]string {
	ids := map[string]string{}
	for _, g := range m.GUIDs {
		scheme, val, ok := strings.Cut(g.ID, "://")
		if !ok || val == "" {
			continue
		}
		switch scheme {
		case "tmdb", "tvdb", "imdb":
			ids[scheme] = val
		}
	}
	if m.Type == "movie" || m.Type == "show" {
		if k, v, ok := legacyGUID(m.GUID); ok {
			if _, exists := ids[k]; !exists {
				ids[k] = v
			}
		}
	}
	return ids
}

// legacyGUID parses old agent guids like "com.plexapp.agents.imdb://tt0083658?lang=en".
func legacyGUID(g string) (key, id string, ok bool) {
	scheme, rest, found := strings.Cut(g, "://")
	if !found {
		return "", "", false
	}
	rest, _, _ = strings.Cut(rest, "?")
	rest, _, _ = strings.Cut(rest, "/")
	if rest == "" {
		return "", "", false
	}
	switch scheme {
	case "com.plexapp.agents.imdb":
		return "imdb", rest, true
	case "com.plexapp.agents.themoviedb":
		return "tmdb", rest, true
	case "com.plexapp.agents.thetvdb":
		return "tvdb", rest, true
	}
	return "", "", false
}

// Files lists the item's file paths as Plex sees them.
func (m Metadata) Files() []string {
	var out []string
	for _, md := range m.Media {
		for _, p := range md.Part {
			if p.File != "" {
				out = append(out, p.File)
			}
		}
	}
	return out
}
```

`server/internal/importer/plex/client.go`:
```go
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrUnauthorized means Plex (or plex.tv) rejected the token.
var ErrUnauthorized = errors.New("plex rejected the token")

// DefaultPageSize is the X-Plex-Container-Size used for listings.
const DefaultPageSize = 200

const clientIdentifier = "lisimflix-plex-importer"

// Client talks to one Plex Media Server with one token. The token is sent only
// in the X-Plex-Token header, never in URLs, so it can't appear in logs or errors.
type Client struct {
	base     *url.URL
	token    string
	hc       *http.Client
	PageSize int
}

// NewClient validates the base URL (http/https with a host) and token.
func NewClient(baseURL, token string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("plex base URL must look like http://host:32400, got %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("plex token is required")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{base: u, token: token, hc: hc, PageSize: DefaultPageSize}, nil
}

// WithToken returns a copy of c that uses another account's server token.
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("plex GET %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier)
	req.Header.Set("X-Plex-Product", "Lisimflix Importer")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("plex GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("plex GET %s: %w", path, ErrUnauthorized)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("plex GET %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("plex GET %s: decode: %w", path, err)
	}
	return nil
}

// paged fetches every page of a Metadata listing.
func (c *Client) paged(ctx context.Context, path string, q url.Values) ([]Metadata, error) {
	var all []Metadata
	start := 0
	for {
		pq := url.Values{}
		for k, v := range q {
			pq[k] = v
		}
		pq.Set("X-Plex-Container-Start", strconv.Itoa(start))
		pq.Set("X-Plex-Container-Size", strconv.Itoa(c.PageSize))
		var mc mediaContainer
		if err := c.get(ctx, path, pq, &mc); err != nil {
			return nil, err
		}
		page := mc.MediaContainer.Metadata
		all = append(all, page...)
		start += len(page)
		total := mc.MediaContainer.TotalSize
		if len(page) == 0 || len(page) < c.PageSize || (total > 0 && start >= total) {
			return all, nil
		}
	}
}

// Identity returns the server's machine identifier and version.
func (c *Client) Identity(ctx context.Context) (Identity, error) {
	var mc mediaContainer
	if err := c.get(ctx, "/identity", nil, &mc); err != nil {
		return Identity{}, err
	}
	return Identity{MachineIdentifier: mc.MediaContainer.MachineIdentifier, Version: mc.MediaContainer.Version}, nil
}

// Accounts lists accounts known to the server (including system account 0).
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var mc mediaContainer
	if err := c.get(ctx, "/accounts", nil, &mc); err != nil {
		return nil, err
	}
	return mc.MediaContainer.Account, nil
}

// Sections lists the libraries visible to the token.
func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	var mc mediaContainer
	if err := c.get(ctx, "/library/sections", nil, &mc); err != nil {
		return nil, err
	}
	return mc.MediaContainer.Directory, nil
}

// Items lists a section's items of one type (1 movie, 2 show, 4 episode) with Guids.
func (c *Client) Items(ctx context.Context, sectionKey string, typ int) ([]Metadata, error) {
	q := url.Values{"type": {strconv.Itoa(typ)}, "includeGuids": {"1"}}
	return c.paged(ctx, "/library/sections/"+url.PathEscape(sectionKey)+"/all", q)
}

// History lists an account's finished plays, newest first (needs the owner token).
func (c *Client) History(ctx context.Context, accountID int64) ([]Metadata, error) {
	q := url.Values{"accountID": {strconv.FormatInt(accountID, 10)}, "sort": {"viewedAt:desc"}}
	return c.paged(ctx, "/status/sessions/history/all", q)
}
```

`server/internal/importer/plex/plextv.go`:
```go
package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultPlexTVURL is plex.tv; tests point this at a fake.
const DefaultPlexTVURL = "https://plex.tv"

type sharedServersXML struct {
	XMLName xml.Name `xml:"MediaContainer"`
	Servers []struct {
		UserID      int64  `xml:"userID,attr"`
		Username    string `xml:"username,attr"`
		AccessToken string `xml:"accessToken,attr"`
	} `xml:"SharedServer"`
}

// SharedUserTokens asks plex.tv for the server access token of every user the
// server is shared with (friends and managed users), keyed by plex.tv user ID,
// which equals the account ID in PMS /accounts. ownerToken must be the owner's
// plex.tv token (normally the same as the server admin token).
func SharedUserTokens(ctx context.Context, hc *http.Client, plexTVURL, ownerToken, machineID string) (map[int64]string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	u := strings.TrimRight(plexTVURL, "/") + "/api/servers/" + url.PathEscape(machineID) + "/shared_servers"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("plex.tv shared servers: %w", err)
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Token", ownerToken)
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier)
	req.Header.Set("X-Plex-Product", "Lisimflix Importer")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex.tv shared servers: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("plex.tv shared servers: %w", ErrUnauthorized)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("plex.tv shared servers: status %d", resp.StatusCode)
	}
	var mc sharedServersXML
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&mc); err != nil {
		return nil, fmt.Errorf("plex.tv shared servers: decode: %w", err)
	}
	out := map[int64]string{}
	for _, s := range mc.Servers {
		if s.UserID > 0 && s.AccessToken != "" {
			out[s.UserID] = s.AccessToken
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Run tests, expect PASS**

Run: `make test`
Expected: `ok  .../internal/importer/plex`.

- [ ] **Step 7: Commit**

```bash
git add server/internal/importer/plex
git commit -m "feat(importer): Plex Media Server JSON client, plex.tv token lookup and fixtures"
```

---

### Task 13: Matching Plex items to Lisimflix items

**Files:**
- Create: `server/internal/importer/plex/matcher.go`
- Test: `server/internal/importer/plex/fixture_test.go`, `server/internal/importer/plex/matcher_test.go`

**Interfaces:**
- Consumes: `Metadata.ExternalIDs/Files` (Task 12); `media.Store.FindByExternalID/FindByFilePath/FindByTitleYear/Children`, `media.ErrNotFound` (contract additions)
- Produces:
  ```go
  type PathMapping struct { Plex string `json:"plex"`; Local string `json:"local"` }
  func MapPath(mappings []PathMapping, plexPath string) (string, bool) // longest prefix on a path boundary; "\" → "/"
  type MatchCatalog interface {
      Children(ctx context.Context, parentID int64) ([]media.Item, error)
      FindByExternalID(ctx context.Context, key, value string) ([]media.Item, error)
      FindByFilePath(ctx context.Context, path string) (media.Item, error)
      FindByTitleYear(ctx context.Context, kind media.Kind, title string, year int) ([]media.Item, error)
  }
  type Method string // "external_id" | "file_path" | "title_year"
  type Reason string // "no_match" | "ambiguous" | "episode_not_found" | "unsupported_type" | "not_in_plex_library"
  type Match struct { ItemID int64; Method Method }
  type MatchError struct { Reason Reason; Candidates []int64 } // implements error
  func NewMatcher(cat MatchCatalog, mappings []PathMapping, shows []Metadata) *Matcher
  func (m *Matcher) Match(ctx context.Context, md Metadata) (Match, error) // *MatchError for "no good match"; other errors are failures
  ```
  **Order (spec 3.5):** movies try external IDs (tmdb, imdb, tvdb), then the mapped file path,
  then title + year. If the external IDs give several candidates, the path can still settle it;
  otherwise the result is `ambiguous`. Episodes try episode-level IDs, then the show (by the show's
  IDs from the type=2 listing) plus season/episode numbers, then the mapped file path, then the
  show by title + year plus season/episode numbers.
  Test fixture `newImportFixture(t) *importFixture` (Lisimflix side of the Task 12 scenario).

- [ ] **Step 1: Write the fixture and failing tests**

`server/internal/importer/plex/fixture_test.go`:
```go
package plex

import (
	"fmt"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/userdata/udtest"
	"github.com/Lisimflix/lisimflix/server/internal/userdata"
)

// importFixture is the Lisimflix side of the fixture scenario in Task 12.
type importFixture struct {
	d                                  *db.DB
	media                              *media.Store
	ud                                 *userdata.Store
	zac, lisim, ondo                   int64 // profile IDs
	libMovies, libTV                   int64
	matrix, heat, alien, bladeRunner   media.Item
	solaris1, solaris2, firefly        media.Item
	ep                                 map[int]media.Item // Firefly S01, by episode number
}

func intp(i int) *int { return &i }

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	d := dbtest.Open(t)
	f := &importFixture{d: d, media: media.NewStore(d), ud: userdata.NewStore(d), ep: map[int]media.Item{}}
	_, f.zac = udtest.NewProfile(t, d, "zac")
	_, f.lisim = udtest.NewProfile(t, d, "lisim")
	_, f.ondo = udtest.NewProfile(t, d, "ondo")
	f.libMovies = udtest.NewLibrary(t, d, "Movies", library.Type("movies"))
	f.libTV = udtest.NewLibrary(t, d, "TV Shows", library.Type("tv"))
	movie := func(title string, year int, ids map[string]string) media.Item {
		return udtest.NewItem(t, d, media.Item{LibraryID: f.libMovies, Kind: kindMovie, Title: title, Year: year, ExternalIDs: ids})
	}
	f.matrix = movie("The Matrix", 1999, map[string]string{"tmdb": "603", "imdb": "tt0133093"})
	f.heat = movie("Heat", 1995, nil)
	udtest.NewFile(t, d, f.heat.ID, "/media/movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	f.alien = movie("Alien", 1979, nil)
	f.solaris1 = movie("Solaris", 2002, nil)
	f.solaris2 = movie("Solaris", 2002, nil)
	f.bladeRunner = movie("Blade Runner", 1982, map[string]string{"imdb": "tt0083658"})
	f.firefly = udtest.NewItem(t, d, media.Item{LibraryID: f.libTV, Kind: kindShow, Title: "Firefly", Year: 2002,
		ExternalIDs: map[string]string{"tvdb": "78874"}})
	season := udtest.NewItem(t, d, media.Item{LibraryID: f.libTV, ParentID: f.firefly.ID, Kind: kindSeason,
		Title: "Season 1", SeasonNumber: intp(1)})
	titles := map[int]string{1: "Serenity", 2: "The Train Job", 3: "Bushwhacked"}
	for n := 1; n <= 3; n++ {
		var ids map[string]string
		if n == 1 {
			ids = map[string]string{"tvdb": "297989"}
		}
		f.ep[n] = udtest.NewItem(t, d, media.Item{LibraryID: f.libTV, ParentID: season.ID, Kind: kindEpisode,
			Title: titles[n], SeasonNumber: intp(1), EpisodeNumber: intp(n), ExternalIDs: ids})
		udtest.NewFile(t, d, f.ep[n].ID, fmt.Sprintf("/media/tv/Firefly/Season 01/Firefly - S01E%02d - %s.mkv", n, titles[n]))
	}
	return f
}
```

`server/internal/importer/plex/matcher_test.go`:
```go
package plex

import (
	"context"
	"errors"
	"testing"
)

func TestMapPath(t *testing.T) {
	maps := []PathMapping{{Plex: "/data", Local: "/media"}, {Plex: "/data/tv", Local: "/tv"}, {Plex: `C:\Media`, Local: "/win"}}
	cases := map[string]string{
		"/data/movies/a.mkv":       "/media/movies/a.mkv",
		"/data/tv/Show/s01e01.mkv": "/tv/Show/s01e01.mkv",
		"/data":                    "/media",
		`C:\Media\Movies\x.mkv`:    "/win/Movies/x.mkv",
	}
	for in, want := range cases {
		if got, ok := MapPath(maps, in); !ok || got != want {
			t.Fatalf("%s → %q ok=%v, want %q", in, got, ok, want)
		}
	}
	if _, ok := MapPath(maps, "/data2/x.mkv"); ok {
		t.Fatal("/data2 must not match /data")
	}
	if got, ok := MapPath([]PathMapping{{Plex: "/", Local: "/media"}}, "/movies/a.mkv"); !ok || got != "/media/movies/a.mkv" {
		t.Fatalf("root mapping: %q", got)
	}
}

var testShows = []Metadata{{RatingKey: "200", Type: "show", Title: "Firefly", Year: 2002, GUIDs: []GUID{{ID: "tvdb://78874"}}}}

func TestMatchMovies(t *testing.T) {
	f := newImportFixture(t)
	m := NewMatcher(f.media, []PathMapping{{Plex: "/data", Local: "/media"}}, testShows)
	ctx := context.Background()
	file := func(p string) []Media { return []Media{{Part: []Part{{File: p}}}} }
	cases := []struct {
		name   string
		md     Metadata
		want   int64
		method Method
	}{
		{"tmdb", Metadata{Type: "movie", Title: "Matrix, The", Year: 1999, GUIDs: []GUID{{ID: "tmdb://603"}}}, f.matrix.ID, MethodExternalID},
		{"legacy imdb guid", Metadata{Type: "movie", Title: "BR", GUID: "com.plexapp.agents.imdb://tt0083658?lang=en"}, f.bladeRunner.ID, MethodExternalID},
		{"file path", Metadata{Type: "movie", Title: "Heat (Director's)", Year: 1995,
			Media: file("/data/movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")}, f.heat.ID, MethodFilePath},
		{"title year, case-insensitive", Metadata{Type: "movie", Title: "alien", Year: 1979}, f.alien.ID, MethodTitleYear},
	}
	for _, c := range cases {
		got, err := m.Match(ctx, c.md)
		if err != nil || got.ItemID != c.want || got.Method != c.method {
			t.Fatalf("%s: got %+v err=%v, want %d via %s", c.name, got, err, c.want, c.method)
		}
	}

	_, err := m.Match(ctx, Metadata{Type: "movie", Title: "Solaris", Year: 2002})
	var me *MatchError
	if !errors.As(err, &me) || me.Reason != ReasonAmbiguous || len(me.Candidates) != 2 {
		t.Fatalf("solaris: %v", err)
	}
	_, err = m.Match(ctx, Metadata{Type: "movie", Title: "Obscure Film", Year: 2001})
	if !errors.As(err, &me) || me.Reason != ReasonNoMatch {
		t.Fatalf("obscure: %v", err)
	}
	_, err = m.Match(ctx, Metadata{Type: "show", Title: "Firefly"})
	if !errors.As(err, &me) || me.Reason != ReasonUnsupported {
		t.Fatalf("show: %v", err)
	}
}

func TestMatchEpisodes(t *testing.T) {
	f := newImportFixture(t)
	m := NewMatcher(f.media, []PathMapping{{Plex: "/data", Local: "/media"}}, testShows)
	ctx := context.Background()
	ep := func(gp, gpTitle string, s, e int, guids []GUID, files ...string) Metadata {
		md := Metadata{Type: "episode", GrandparentRatingKey: gp, GrandparentTitle: gpTitle, ParentIndex: intp(s), Index: intp(e), GUIDs: guids}
		for _, p := range files {
			md.Media = append(md.Media, Media{Part: []Part{{File: p}}})
		}
		return md
	}
	cases := []struct {
		name   string
		md     Metadata
		want   int64
		method Method
	}{
		{"episode tvdb id", ep("300", "Unknown", 9, 9, []GUID{{ID: "tvdb://297989"}}), f.ep[1].ID, MethodExternalID},
		{"show tvdb + numbers", ep("200", "Firefly", 1, 2, nil), f.ep[2].ID, MethodExternalID},
		{"file path, show unknown", ep("300", "Firefly (Old)", 1, 3, nil,
			"/data/tv/Firefly/Season 01/Firefly - S01E03 - Bushwhacked.mkv"), f.ep[3].ID, MethodFilePath},
		{"show title + numbers", ep("301", "firefly", 1, 1, nil), f.ep[1].ID, MethodTitleYear},
	}
	for _, c := range cases {
		got, err := m.Match(ctx, c.md)
		if err != nil || got.ItemID != c.want || got.Method != c.method {
			t.Fatalf("%s: got %+v err=%v, want %d via %s", c.name, got, err, c.want, c.method)
		}
	}
	var me *MatchError
	if _, err := m.Match(ctx, ep("200", "Firefly", 1, 9, nil)); !errors.As(err, &me) || me.Reason != ReasonEpisodeNotFound {
		t.Fatalf("missing episode: %v", err)
	}
	if _, err := m.Match(ctx, ep("302", "Nope", 1, 1, nil)); !errors.As(err, &me) || me.Reason != ReasonNoMatch {
		t.Fatalf("unknown show: %v", err)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewMatcher`

- [ ] **Step 3: Implement**

`server/internal/importer/plex/matcher.go`:
```go
package plex

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

const (
	kindMovie   = media.KindMovie
	kindShow    = media.KindShow
	kindSeason  = media.KindSeason
	kindEpisode = media.KindEpisode
)

// PathMapping rewrites a Plex path prefix to the Lisimflix one, e.g. "/data" → "/media".
type PathMapping struct {
	Plex  string `json:"plex"`
	Local string `json:"local"`
}

func normalizePath(p string) string { return path.Clean(strings.ReplaceAll(p, `\`, "/")) }

// MapPath applies the longest matching mapping (on a path-segment boundary).
func MapPath(mappings []PathMapping, plexPath string) (string, bool) {
	p := normalizePath(plexPath)
	best, out := -1, ""
	for _, m := range mappings {
		from := normalizePath(m.Plex)
		if p != from && !strings.HasPrefix(p, strings.TrimSuffix(from, "/")+"/") {
			continue
		}
		if len(from) <= best {
			continue
		}
		best = len(from)
		out = path.Join(normalizePath(m.Local), strings.TrimPrefix(p, from))
	}
	return out, best >= 0
}

// MatchCatalog is what the matcher needs from the media store (*media.Store).
type MatchCatalog interface {
	Children(ctx context.Context, parentID int64) ([]media.Item, error)
	FindByExternalID(ctx context.Context, key, value string) ([]media.Item, error)
	FindByFilePath(ctx context.Context, path string) (media.Item, error)
	FindByTitleYear(ctx context.Context, kind media.Kind, title string, year int) ([]media.Item, error)
}

// Method says how an item was matched.
type Method string

const (
	MethodExternalID Method = "external_id"
	MethodFilePath   Method = "file_path"
	MethodTitleYear  Method = "title_year"
)

// Reason says why an item was not matched.
type Reason string

const (
	ReasonNoMatch         Reason = "no_match"
	ReasonAmbiguous       Reason = "ambiguous"
	ReasonEpisodeNotFound Reason = "episode_not_found"
	ReasonUnsupported     Reason = "unsupported_type"
	ReasonNotInPlex       Reason = "not_in_plex_library"
)

// Match is a successful match.
type Match struct {
	ItemID int64
	Method Method
}

// MatchError is returned when no single Lisimflix item fits.
type MatchError struct {
	Reason     Reason
	Candidates []int64
}

func (e *MatchError) Error() string {
	if len(e.Candidates) > 0 {
		return fmt.Sprintf("%s (candidates %v)", e.Reason, e.Candidates)
	}
	return string(e.Reason)
}

// Matcher maps Plex items to Lisimflix items. Not safe for concurrent use.
type Matcher struct {
	cat         MatchCatalog
	mappings    []PathMapping
	shows       map[string]Metadata // show ratingKey → show listing entry
	showByIDs   map[string]int64    // cache; 0 = not found
	showByTitle map[string]int64    // cache; 0 = not found
}

// NewMatcher builds a matcher. shows is the type=2 listing (for show-level IDs).
func NewMatcher(cat MatchCatalog, mappings []PathMapping, shows []Metadata) *Matcher {
	m := &Matcher{cat: cat, mappings: mappings, shows: map[string]Metadata{},
		showByIDs: map[string]int64{}, showByTitle: map[string]int64{}}
	for _, s := range shows {
		m.shows[s.RatingKey] = s
	}
	return m
}

// Match finds the Lisimflix item for a Plex movie or episode.
func (m *Matcher) Match(ctx context.Context, md Metadata) (Match, error) {
	switch md.Type {
	case "movie":
		return m.matchMovie(ctx, md)
	case "episode":
		return m.matchEpisode(ctx, md)
	}
	return Match{}, &MatchError{Reason: ReasonUnsupported}
}

func (m *Matcher) matchMovie(ctx context.Context, md Metadata) (Match, error) {
	ids, err := m.byIDs(ctx, md.ExternalIDs(), kindMovie)
	if err != nil {
		return Match{}, err
	}
	if len(ids) == 1 {
		return Match{ItemID: ids[0], Method: MethodExternalID}, nil
	}
	if id, ok, err := m.byPath(ctx, md.Files(), kindMovie); err != nil || ok {
		return Match{ItemID: id, Method: MethodFilePath}, err
	}
	if len(ids) > 1 {
		return Match{}, &MatchError{Reason: ReasonAmbiguous, Candidates: ids}
	}
	ids, err = m.byTitle(ctx, kindMovie, md.Title, md.Year)
	if err != nil {
		return Match{}, err
	}
	switch len(ids) {
	case 1:
		return Match{ItemID: ids[0], Method: MethodTitleYear}, nil
	case 0:
		return Match{}, &MatchError{Reason: ReasonNoMatch}
	default:
		return Match{}, &MatchError{Reason: ReasonAmbiguous, Candidates: ids}
	}
}

func (m *Matcher) matchEpisode(ctx context.Context, md Metadata) (Match, error) {
	ids, err := m.byIDs(ctx, md.ExternalIDs(), kindEpisode)
	if err != nil {
		return Match{}, err
	}
	if len(ids) == 1 {
		return Match{ItemID: ids[0], Method: MethodExternalID}, nil
	}
	showFound := false
	show, err := m.showFromIDs(ctx, md.GrandparentRatingKey)
	if err != nil {
		return Match{}, err
	}
	if show > 0 {
		showFound = true
		if id, ok, err := m.episodeOf(ctx, show, md); err != nil || ok {
			return Match{ItemID: id, Method: MethodExternalID}, err
		}
	}
	if id, ok, err := m.byPath(ctx, md.Files(), kindEpisode); err != nil || ok {
		return Match{ItemID: id, Method: MethodFilePath}, err
	}
	show, err = m.showFromTitle(ctx, md)
	if err != nil {
		return Match{}, err
	}
	if show > 0 {
		showFound = true
		if id, ok, err := m.episodeOf(ctx, show, md); err != nil || ok {
			return Match{ItemID: id, Method: MethodTitleYear}, err
		}
	}
	if showFound {
		return Match{}, &MatchError{Reason: ReasonEpisodeNotFound}
	}
	return Match{}, &MatchError{Reason: ReasonNoMatch}
}

// byIDs returns the distinct items of kind matching any of the external IDs.
func (m *Matcher) byIDs(ctx context.Context, ids map[string]string, kind media.Kind) ([]int64, error) {
	set := map[int64]bool{}
	for _, key := range []string{"tmdb", "imdb", "tvdb"} {
		v, ok := ids[key]
		if !ok {
			continue
		}
		items, err := m.cat.FindByExternalID(ctx, key, v)
		if err != nil {
			return nil, fmt.Errorf("find %s %s: %w", key, v, err)
		}
		for _, it := range items {
			if it.Kind == kind {
				set[it.ID] = true
			}
		}
	}
	return sortedIDs(set), nil
}

func (m *Matcher) byPath(ctx context.Context, files []string, kind media.Kind) (int64, bool, error) {
	for _, f := range files {
		p, ok := MapPath(m.mappings, f)
		if !ok {
			p = normalizePath(f)
		}
		it, err := m.cat.FindByFilePath(ctx, p)
		if errors.Is(err, media.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, false, fmt.Errorf("find file %s: %w", p, err)
		}
		if it.Kind == kind {
			return it.ID, true, nil
		}
	}
	return 0, false, nil
}

func (m *Matcher) byTitle(ctx context.Context, kind media.Kind, title string, year int) ([]int64, error) {
	if strings.TrimSpace(title) == "" {
		return nil, nil
	}
	items, err := m.cat.FindByTitleYear(ctx, kind, title, year)
	if err != nil {
		return nil, fmt.Errorf("find %s %q (%d): %w", kind, title, year, err)
	}
	set := map[int64]bool{}
	for _, it := range items {
		if it.Kind == kind {
			set[it.ID] = true
		}
	}
	return sortedIDs(set), nil
}

func (m *Matcher) showFromIDs(ctx context.Context, showKey string) (int64, error) {
	if id, ok := m.showByIDs[showKey]; ok {
		return id, nil
	}
	var id int64
	if s, ok := m.shows[showKey]; ok {
		ids, err := m.byIDs(ctx, s.ExternalIDs(), kindShow)
		if err != nil {
			return 0, err
		}
		if len(ids) == 1 {
			id = ids[0]
		}
	}
	m.showByIDs[showKey] = id
	return id, nil
}

func (m *Matcher) showFromTitle(ctx context.Context, md Metadata) (int64, error) {
	if id, ok := m.showByTitle[md.GrandparentRatingKey]; ok {
		return id, nil
	}
	year := m.shows[md.GrandparentRatingKey].Year // 0 when the show is unknown = any year
	ids, err := m.byTitle(ctx, kindShow, md.GrandparentTitle, year)
	if err != nil {
		return 0, err
	}
	var id int64
	if len(ids) == 1 {
		id = ids[0]
	}
	m.showByTitle[md.GrandparentRatingKey] = id
	return id, nil
}

// episodeOf finds season parentIndex / episode index under a Lisimflix show.
func (m *Matcher) episodeOf(ctx context.Context, showID int64, md Metadata) (int64, bool, error) {
	if md.ParentIndex == nil || md.Index == nil {
		return 0, false, nil
	}
	seasons, err := m.cat.Children(ctx, showID)
	if err != nil {
		return 0, false, fmt.Errorf("seasons of %d: %w", showID, err)
	}
	for _, s := range seasons {
		if s.Kind != kindSeason || s.SeasonNumber == nil || *s.SeasonNumber != *md.ParentIndex {
			continue
		}
		eps, err := m.cat.Children(ctx, s.ID)
		if err != nil {
			return 0, false, fmt.Errorf("episodes of %d: %w", s.ID, err)
		}
		for _, e := range eps {
			if e.Kind == kindEpisode && e.EpisodeNumber != nil && *e.EpisodeNumber == *md.Index {
				return e.ID, true, nil
			}
		}
	}
	return 0, false, nil
}

func sortedIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/importer/plex/matcher.go server/internal/importer/plex/matcher_test.go server/internal/importer/plex/fixture_test.go
git commit -m "feat(importer): match Plex items by external ID, mapped file path, then title and year"
```

---

### Task 14: Never-downgrade merge and the import engine (dry run and apply)

**Files:**
- Create: `server/internal/userdata/merge.go`, `server/internal/userdata/merge_test.go`
- Create: `server/internal/importer/plex/engine.go`
- Test: `server/internal/importer/plex/engine_test.go`

**Interfaces:**
- Consumes: userdata `getRaw`, `putRaw`, `addHistory`, `Rules`; plex `Client`, `SharedUserTokens`, `Matcher`, `MatchError`, fixtures (Tasks 12–13)
- Produces (userdata):
  ```go
  type Imported struct { Watched bool; PlayCount int; PositionMs, DurationMs int64; LastPlayed time.Time; History []time.Time; Rating *float64 }
  type MergeResult struct { StateChanged bool; HistoryAdded int; RatingSet bool }
  func (s *Store) Merge(ctx, profileID, itemID int64, imp Imported, dryRun bool) (MergeResult, error)
  func (s *Store) ProfileExists(ctx, profileID int64) (bool, error)
  ```
  **Merge rules (never downgrade):** watched = existing OR imported. Play count, last played and
  duration take the max. The resume position takes the max (after `Rules.ResumePositionMs`), except
  when Lisimflix finished the item later than Plex last saw it. History rows are added with
  source `plex` (duplicates ignored). A rating is set only if the profile has none. A dry run
  computes everything inside a transaction and rolls it back.
- Produces (plex):
  ```go
  var ErrBadRequest = errors.New("invalid import request")
  type AccountMapping struct { PlexAccountID int64 `json:"plexAccountId"`; ProfileID int64 `json:"profileId"`; Token string `json:"token,omitempty"` }
  type Request struct { BaseURL string `json:"baseUrl"`; Token string `json:"token"`; DryRun bool `json:"dryRun"`;
      PathMappings []PathMapping `json:"pathMappings"`; Accounts []AccountMapping `json:"accounts"`; DiscoverUserTokens bool `json:"discoverUserTokens"` }
  func (r Request) Validate() error
  type Writer interface { Merge(...) (userdata.MergeResult, error); ProfileExists(ctx, int64) (bool, error) } // *userdata.Store
  type Deps struct { Catalog MatchCatalog; Writer Writer; HTTP *http.Client; PlexTVURL string; Log *slog.Logger }
  type Status struct { Phase string `json:"phase"`; Done int `json:"done"`; Total int `json:"total"` }
  type Report struct { DryRun; Matched; MatchedBy map[Method]int; Accounts []AccountReport; Unmatched []Unmatched; UnmatchedTotal;
      Conflicts []Conflict; ConflictsTotal; Warnings []string; NotImported []string } // camelCase JSON
  type AccountReport struct { PlexAccountID; PlexName; ProfileID; Source ("full"|"history_only"); Items; Watched; InProgress;
      HistoryEntries; Ratings; Unmatched; Updated; Unchanged; HistoryAdded; RatingsAdded }
  type Unmatched struct { RatingKey, Title, Type string; Reason Reason }
  type Conflict struct { RatingKey, Title string; Reason Reason; CandidateItemIDs []int64 }
  var NotImported []string
  func Run(ctx context.Context, deps Deps, req Request, progress func(Status)) (Report, error)
  ```
  Bad input (validation, unknown Plex account, unknown profile) → `ErrBadRequest`. A bad owner
  token → wraps `ErrUnauthorized`. A rejected per-user token or a plex.tv failure → warning plus a
  history-only fallback for that account. Report lists are capped at 500 entries; totals are exact.

- [ ] **Step 1: Write the failing userdata merge tests**

`server/internal/userdata/merge_test.go`:
```go
package userdata

import (
	"context"
	"errors"
	"testing"
	"time"
)

func at(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
func fp(v float64) *float64  { return &v }

func TestMergeNeverDowngrades(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	imp := Imported{Watched: true, PlayCount: 2, LastPlayed: at(1000), DurationMs: testDurationMs,
		History: []time.Time{at(900), at(1000)}, Rating: fp(9)}
	res, err := f.store.Merge(ctx, f.profileID, f.movie.ID, imp, false)
	if err != nil || !res.StateChanged || res.HistoryAdded != 2 || !res.RatingSet {
		t.Fatalf("first merge: %+v %v", res, err)
	}
	st, _ := f.store.Get(ctx, f.profileID, f.movie.ID)
	if !st.Watched || st.PlayCount != 2 || !st.LastPlayed.Equal(at(1000)) {
		t.Fatalf("state %+v", st)
	}
	if res, _ := f.store.Merge(ctx, f.profileID, f.movie.ID, imp, false); res.StateChanged || res.HistoryAdded != 0 || res.RatingSet {
		t.Fatalf("re-import changed something: %+v", res)
	}
	f.store.Merge(ctx, f.profileID, f.movie.ID, Imported{PlayCount: 1, LastPlayed: at(500), Rating: fp(3)}, false)
	st, _ = f.store.Get(ctx, f.profileID, f.movie.ID)
	r, _, _ := f.store.Rating(ctx, f.profileID, f.movie.ID)
	if !st.Watched || st.PlayCount != 2 || !st.LastPlayed.Equal(at(1000)) || r != 9 {
		t.Fatalf("downgraded: %+v rating=%v", st, r)
	}
}

func TestMergePositions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ep := f.eps["S01E01"].ID
	f.store.Merge(ctx, f.profileID, ep, Imported{PositionMs: 1_000_000, DurationMs: testDurationMs, LastPlayed: at(2000)}, false)
	f.store.Merge(ctx, f.profileID, ep, Imported{PositionMs: 500_000, DurationMs: testDurationMs, LastPlayed: at(2100)}, false)
	if st, _ := f.store.Get(ctx, f.profileID, ep); st.PositionMs != 1_000_000 || st.Watched {
		t.Fatalf("position: %+v", st)
	}
	// Under 2 minutes is not a resume point.
	ep3 := f.eps["S01E03"].ID
	f.store.Merge(ctx, f.profileID, ep3, Imported{PositionMs: 60_000, DurationMs: testDurationMs, LastPlayed: at(2000)}, false)
	if st, _ := f.store.Get(ctx, f.profileID, ep3); st.PositionMs != 0 {
		t.Fatalf("short position kept: %+v", st)
	}
	// Finished in Lisimflix after Plex's last view: no resurrected position.
	ep2 := f.eps["S01E02"].ID
	f.store.Put(ctx, State{ProfileID: f.profileID, ItemID: ep2, Watched: true, PlayCount: 1, LastPlayed: at(5000)})
	f.store.Merge(ctx, f.profileID, ep2, Imported{PositionMs: 2_000_000, DurationMs: testDurationMs, LastPlayed: at(4000)}, false)
	if st, _ := f.store.Get(ctx, f.profileID, ep2); st.PositionMs != 0 {
		t.Fatalf("resurrected position: %+v", st)
	}
	// Watched in Plex without a count still counts once.
	f.store.Merge(ctx, f.profileID, f.eps["S02E01"].ID, Imported{Watched: true}, false)
	if st, _ := f.store.Get(ctx, f.profileID, f.eps["S02E01"].ID); st.PlayCount != 1 {
		t.Fatalf("play count %+v", st)
	}
}

func TestMergeDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.store.Merge(ctx, f.profileID, f.movie.ID,
		Imported{Watched: true, PlayCount: 1, LastPlayed: at(1000), History: []time.Time{at(1000)}, Rating: fp(7)}, true)
	if err != nil || !res.StateChanged || res.HistoryAdded != 1 || !res.RatingSet {
		t.Fatalf("dry run result: %+v %v", res, err)
	}
	st, _ := f.store.Get(ctx, f.profileID, f.movie.ID)
	_, rated, _ := f.store.Rating(ctx, f.profileID, f.movie.ID)
	if st.Watched || rated || historyCount(t, f, f.movie.ID) != 0 {
		t.Fatalf("dry run wrote: %+v rated=%v", st, rated)
	}
}

func TestMergeRejectsInvalid(t *testing.T) {
	f := newFixture(t)
	for _, imp := range []Imported{{PlayCount: -1}, {PositionMs: -5}, {Rating: fp(11)}} {
		if _, err := f.store.Merge(context.Background(), f.profileID, f.movie.ID, imp, false); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", imp, err)
		}
	}
}

func TestProfileExists(t *testing.T) {
	f := newFixture(t)
	if ok, err := f.store.ProfileExists(context.Background(), f.profileID); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, _ := f.store.ProfileExists(context.Background(), 999999); ok {
		t.Fatal("missing profile reported as existing")
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Imported`

- [ ] **Step 3: Implement the merge**

`server/internal/userdata/merge.go`:
```go
package userdata

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Imported is watch data from another system (Plex) for one profile and item.
type Imported struct {
	Watched    bool
	PlayCount  int
	PositionMs int64 // raw position; resume rules are applied on merge
	DurationMs int64
	LastPlayed time.Time
	History    []time.Time // finished plays
	Rating     *float64    // nil = no rating
}

// MergeResult says what a merge changed (or would change, in a dry run).
type MergeResult struct {
	StateChanged bool
	HistoryAdded int
	RatingSet    bool
}

// Merge folds imported data into the profile's state without ever lowering
// anything Lisimflix already has. With dryRun, nothing is committed.
func (s *Store) Merge(ctx context.Context, profileID, itemID int64, imp Imported, dryRun bool) (MergeResult, error) {
	if profileID <= 0 || itemID <= 0 || imp.PlayCount < 0 || imp.PositionMs < 0 || imp.DurationMs < 0 {
		return MergeResult{}, fmt.Errorf("%w: bad imported state", ErrInvalid)
	}
	if imp.Rating != nil && (math.IsNaN(*imp.Rating) || *imp.Rating < 0 || *imp.Rating > 10) {
		return MergeResult{}, fmt.Errorf("%w: rating must be between 0 and 10", ErrInvalid)
	}
	tx, err := s.d.BeginTx(ctx, nil)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge: begin: %w", err)
	}
	defer tx.Rollback()

	prev, prevLastPos, err := getRaw(ctx, tx, profileID, itemID)
	if err != nil {
		return MergeResult{}, err
	}
	next, lastPos := prev, prevLastPos
	next.Watched = prev.Watched || imp.Watched
	if imp.PlayCount > next.PlayCount {
		next.PlayCount = imp.PlayCount
	}
	if next.Watched && next.PlayCount == 0 {
		next.PlayCount = 1
	}
	if imp.LastPlayed.After(next.LastPlayed) {
		next.LastPlayed = imp.LastPlayed
	}
	if imp.DurationMs > next.DurationMs {
		next.DurationMs = imp.DurationMs
	}
	pos := s.rules.ResumePositionMs(Progress{PositionMs: imp.PositionMs, DurationMs: imp.DurationMs})
	finishedHereLater := prev.Watched && prev.LastPlayed.After(imp.LastPlayed)
	if pos > prev.PositionMs && !finishedHereLater {
		next.PositionMs = pos
		lastPos = pos
	}

	var res MergeResult
	if next != prev {
		res.StateChanged = true
		if err := putRaw(ctx, tx, next, lastPos, s.now()); err != nil {
			return MergeResult{}, err
		}
	}
	for _, played := range imp.History {
		added, err := addHistory(ctx, tx, profileID, itemID, played, "plex")
		if err != nil {
			return MergeResult{}, err
		}
		if added {
			res.HistoryAdded++
		}
	}
	if imp.Rating != nil {
		r, err := tx.ExecContext(ctx, `INSERT INTO ratings (profile_id, item_id, rating, updated_ms) VALUES (?, ?, ?, ?)
			ON CONFLICT DO NOTHING`, profileID, itemID, *imp.Rating, s.now().UnixMilli())
		if err != nil {
			return MergeResult{}, fmt.Errorf("merge rating %d/%d: %w", profileID, itemID, err)
		}
		n, _ := r.RowsAffected()
		res.RatingSet = n == 1
	}
	if dryRun {
		return res, nil // deferred Rollback discards everything
	}
	if err := tx.Commit(); err != nil {
		return MergeResult{}, fmt.Errorf("merge: commit: %w", err)
	}
	return res, nil
}

// ProfileExists reports whether a profile row exists (read-only check on 1B's table).
func (s *Store) ProfileExists(ctx context.Context, profileID int64) (bool, error) {
	var ok bool
	if err := s.d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profiles WHERE id = ?)`, profileID).Scan(&ok); err != nil {
		return false, fmt.Errorf("profile exists %d: %w", profileID, err)
	}
	return ok, nil
}
```

- [ ] **Step 4: Run tests, expect the userdata ones to PASS**

Run: `make test`

- [ ] **Step 5: Write the failing engine tests**

`server/internal/importer/plex/engine_test.go`:
```go
package plex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/userdata"
)

func (f *importFixture) deps(p *fakePMS) Deps {
	return Deps{Catalog: f.media, Writer: f.ud, HTTP: p.Client(), PlexTVURL: p.URL()}
}

func (f *importFixture) request(p *fakePMS, dry bool) Request {
	return Request{BaseURL: p.URL(), Token: "owner-token", DryRun: dry, DiscoverUserTokens: true,
		PathMappings: []PathMapping{{Plex: "/data", Local: "/media"}},
		Accounts: []AccountMapping{
			{PlexAccountID: 1, ProfileID: f.zac},
			{PlexAccountID: 2345678, ProfileID: f.lisim},
			{PlexAccountID: 3456789, ProfileID: f.ondo},
		}}
}

func (f *importFixture) state(t *testing.T, profile, item int64) userdata.State {
	t.Helper()
	st, err := f.ud.Get(context.Background(), profile, item)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *importFixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func unix(s int64) time.Time { return time.Unix(s, 0).UTC() }

func TestRunApply(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	var phases []string
	rep, err := Run(context.Background(), f.deps(p), f.request(p, false), func(s Status) { phases = append(phases, s.Phase) })
	if err != nil {
		t.Fatal(err)
	}
	if phases[0] != "connecting" || phases[len(phases)-1] != "done" {
		t.Fatalf("phases %v", phases)
	}

	// Report
	if rep.Matched != 6 || rep.MatchedBy[MethodExternalID] != 4 || rep.MatchedBy[MethodFilePath] != 1 || rep.MatchedBy[MethodTitleYear] != 1 {
		t.Fatalf("matched %d %v", rep.Matched, rep.MatchedBy)
	}
	if rep.UnmatchedTotal != 2 || rep.ConflictsTotal != 1 || len(rep.Conflicts[0].CandidateItemIDs) != 2 || rep.Conflicts[0].RatingKey != "105" {
		t.Fatalf("unmatched %+v conflicts %+v", rep.Unmatched, rep.Conflicts)
	}
	reasons := map[string]Reason{}
	for _, u := range rep.Unmatched {
		reasons[u.RatingKey] = u.Reason
	}
	if reasons["104"] != ReasonNoMatch || reasons["999"] != ReasonNotInPlex {
		t.Fatalf("reasons %v", reasons)
	}
	if len(rep.NotImported) == 0 || len(rep.Warnings) != 0 {
		t.Fatalf("notImported %v warnings %v", rep.NotImported, rep.Warnings)
	}
	zac, lisim, ondo := rep.Accounts[0], rep.Accounts[1], rep.Accounts[2]
	if zac.Source != SourceFull || zac.PlexName != "zac" || zac.Items != 9 || zac.Watched != 6 || zac.InProgress != 2 ||
		zac.Ratings != 1 || zac.Unmatched != 3 || zac.Updated != 6 || zac.HistoryEntries != 4 || zac.HistoryAdded != 3 || zac.RatingsAdded != 1 {
		t.Fatalf("zac %+v", zac)
	}
	if lisim.Source != SourceFull || lisim.Items != 2 || lisim.Updated != 2 {
		t.Fatalf("lisim %+v", lisim)
	}
	if ondo.Source != SourceHistoryOnly || ondo.Items != 2 || ondo.Watched != 2 || ondo.Updated != 2 {
		t.Fatalf("ondo %+v", ondo)
	}

	// Owner: full state.
	if st := f.state(t, f.zac, f.matrix.ID); !st.Watched || st.PlayCount != 2 || !st.LastPlayed.Equal(unix(1760000000)) {
		t.Fatalf("zac matrix %+v", st)
	}
	if r, ok, _ := f.ud.Rating(context.Background(), f.zac, f.matrix.ID); !ok || r != 9 {
		t.Fatalf("zac matrix rating %v %v", r, ok)
	}
	if n := f.count(t, `SELECT count(*) FROM watch_history WHERE profile_id = ? AND item_id = ? AND source = 'plex'`, f.zac, f.matrix.ID); n != 2 {
		t.Fatalf("zac matrix history %d", n)
	}
	if st := f.state(t, f.zac, f.heat.ID); st.PositionMs != 3600000 || st.DurationMs != 10200000 || st.Watched {
		t.Fatalf("zac heat %+v", st)
	}
	if st := f.state(t, f.zac, f.alien.ID); !st.Watched {
		t.Fatalf("zac alien %+v", st)
	}
	if st := f.state(t, f.zac, f.bladeRunner.ID); !st.Watched {
		t.Fatalf("zac blade runner %+v", st)
	}
	if st := f.state(t, f.zac, f.ep[1].ID); !st.Watched {
		t.Fatalf("zac S01E01 %+v", st)
	}
	if st := f.state(t, f.zac, f.ep[2].ID); st.PositionMs != 600000 {
		t.Fatalf("zac S01E02 %+v", st)
	}
	if st := f.state(t, f.zac, f.solaris1.ID); st.Watched {
		t.Fatal("ambiguous item must not be written")
	}
	// Shared user with a discovered token: their own state.
	if st := f.state(t, f.lisim, f.matrix.ID); st.PositionMs != 1200000 || st.Watched {
		t.Fatalf("lisim matrix %+v", st)
	}
	if st := f.state(t, f.lisim, f.ep[1].ID); !st.Watched || !st.LastPlayed.Equal(unix(1760500000)) {
		t.Fatalf("lisim S01E01 %+v", st)
	}
	// History-only user.
	if st := f.state(t, f.ondo, f.matrix.ID); !st.Watched || st.PlayCount != 2 || st.PositionMs != 0 || !st.LastPlayed.Equal(unix(1759000000)) {
		t.Fatalf("ondo matrix %+v", st)
	}
	if st := f.state(t, f.ondo, f.bladeRunner.ID); !st.Watched || st.PlayCount != 1 {
		t.Fatalf("ondo blade runner %+v", st)
	}

	// Running again changes nothing.
	again, err := Run(context.Background(), f.deps(p), f.request(p, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range again.Accounts {
		if a.Updated != 0 || a.HistoryAdded != 0 || a.RatingsAdded != 0 {
			t.Fatalf("second run not idempotent: %+v", a)
		}
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	rep, err := Run(context.Background(), f.deps(p), f.request(p, true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || rep.Accounts[0].Updated != 6 || rep.Accounts[0].HistoryAdded != 3 {
		t.Fatalf("dry-run report %+v", rep.Accounts[0])
	}
	for _, table := range []string{"user_item_state", "watch_history", "ratings"} {
		if n := f.count(t, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("dry run wrote %d rows to %s", n, table)
		}
	}
}

func TestRunNeverDowngrades(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	ctx := context.Background()
	f.ud.Put(ctx, userdata.State{ProfileID: f.zac, ItemID: f.matrix.ID, Watched: true, PlayCount: 5, LastPlayed: unix(1770000000)})
	f.ud.SetRating(ctx, f.zac, f.matrix.ID, 4)
	f.ud.Put(ctx, userdata.State{ProfileID: f.zac, ItemID: f.heat.ID, PositionMs: 5000000, DurationMs: 10200000, LastPlayed: unix(1765000000)})
	if _, err := Run(ctx, f.deps(p), f.request(p, false), nil); err != nil {
		t.Fatal(err)
	}
	if st := f.state(t, f.zac, f.matrix.ID); st.PlayCount != 5 || !st.LastPlayed.Equal(unix(1770000000)) {
		t.Fatalf("matrix downgraded %+v", st)
	}
	if r, _, _ := f.ud.Rating(ctx, f.zac, f.matrix.ID); r != 4 {
		t.Fatalf("rating overwritten: %v", r)
	}
	if st := f.state(t, f.zac, f.heat.ID); st.PositionMs != 5000000 {
		t.Fatalf("heat position lowered %+v", st)
	}
}

func TestRunWithoutDiscoveryAndWithRejectedToken(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	req := f.request(p, false)
	req.DiscoverUserTokens = false
	req.Accounts[2].Token = "rejected"
	rep, err := Run(context.Background(), f.deps(p), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Accounts[1].Source != SourceHistoryOnly || rep.Accounts[2].Source != SourceHistoryOnly || len(rep.Warnings) != 1 {
		t.Fatalf("sources %s %s warnings %v", rep.Accounts[1].Source, rep.Accounts[2].Source, rep.Warnings)
	}
	if st := f.state(t, f.lisim, f.matrix.ID); st.PositionMs != 0 || st.Watched {
		t.Fatalf("history-only lisim must not get a position: %+v", st)
	}
	if st := f.state(t, f.lisim, f.ep[1].ID); !st.Watched {
		t.Fatalf("lisim history not applied: %+v", st)
	}
}

func TestRunErrors(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	ctx := context.Background()

	bad := f.request(p, true)
	bad.Token = "wrong"
	if _, err := Run(ctx, f.deps(p), bad, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad owner token: %v", err)
	}
	unknownAcct := f.request(p, true)
	unknownAcct.Accounts = []AccountMapping{{PlexAccountID: 42, ProfileID: f.zac}}
	if _, err := Run(ctx, f.deps(p), unknownAcct, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("unknown plex account: %v", err)
	}
	unknownProfile := f.request(p, true)
	unknownProfile.Accounts = []AccountMapping{{PlexAccountID: 1, ProfileID: 999999}}
	if _, err := Run(ctx, f.deps(p), unknownProfile, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("unknown profile: %v", err)
	}
	for name, mut := range map[string]func(*Request){
		"no accounts":       func(r *Request) { r.Accounts = nil },
		"duplicate account": func(r *Request) { r.Accounts = append(r.Accounts, r.Accounts[0]) },
		"system account":    func(r *Request) { r.Accounts[0].PlexAccountID = 0 },
		"relative mapping":  func(r *Request) { r.PathMappings = []PathMapping{{Plex: "/data", Local: "media"}} },
		"no url":            func(r *Request) { r.BaseURL = "" },
	} {
		r := f.request(p, true)
		mut(&r)
		if err := r.Validate(); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
```

Expected counts, worked through from the fixture table in Task 12 (they make the asserts above easy to check):
- zac `Items` 9 = listing items with state (101, 102, 103, 104, 105, 107, 201, 202) + history-only 999.
  `Watched` 6 (101, 103, 104, 105, 107, 201). `InProgress` 2 (102, 202). `Unmatched` 3 (104, 105, 999).
  `Updated` 6. `HistoryAdded` 3 (101 ×2, 201; 999 is unmatched).
- `Matched` 6: 101, 107, 201 (episode tvdb), 202 (show tvdb + S01E02) by external ID; 102 by path; 103 by title.

- [ ] **Step 6: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Run`

- [ ] **Step 7: Implement the engine**

`server/internal/importer/plex/engine.go`:
```go
package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/userdata"
)

// ErrBadRequest marks an invalid import request (bad fields, unknown account or profile).
var ErrBadRequest = errors.New("invalid import request")

// AccountMapping maps one Plex account to one Lisimflix profile. Token is an
// optional server token for that account (see Request.DiscoverUserTokens).
type AccountMapping struct {
	PlexAccountID int64  `json:"plexAccountId"`
	ProfileID     int64  `json:"profileId"`
	Token         string `json:"token,omitempty"`
}

// Request is one import run. Tokens are used for this run only and never stored.
type Request struct {
	BaseURL            string           `json:"baseUrl"`
	Token              string           `json:"token"`
	DryRun             bool             `json:"dryRun"`
	PathMappings       []PathMapping    `json:"pathMappings"`
	Accounts           []AccountMapping `json:"accounts"`
	DiscoverUserTokens bool             `json:"discoverUserTokens"`
}

// Validate checks the request without contacting Plex.
func (r Request) Validate() error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrBadRequest, msg) }
	if strings.TrimSpace(r.BaseURL) == "" {
		return bad("baseUrl is required")
	}
	if strings.TrimSpace(r.Token) == "" {
		return bad("token is required")
	}
	if len(r.Accounts) == 0 {
		return bad("map at least one Plex account to a profile")
	}
	seen := map[int64]bool{}
	for _, a := range r.Accounts {
		if a.PlexAccountID <= 0 || a.ProfileID <= 0 {
			return bad("plexAccountId and profileId must be positive")
		}
		if seen[a.PlexAccountID] {
			return bad(fmt.Sprintf("plex account %d is mapped twice", a.PlexAccountID))
		}
		seen[a.PlexAccountID] = true
	}
	for _, m := range r.PathMappings {
		if strings.TrimSpace(m.Plex) == "" || !strings.HasPrefix(m.Local, "/") {
			return bad("path mappings need a Plex path and an absolute Lisimflix path")
		}
	}
	return nil
}

// Writer stores imported state (*userdata.Store).
type Writer interface {
	Merge(ctx context.Context, profileID, itemID int64, imp userdata.Imported, dryRun bool) (userdata.MergeResult, error)
	ProfileExists(ctx context.Context, profileID int64) (bool, error)
}

// Deps are the engine's collaborators.
type Deps struct {
	Catalog   MatchCatalog
	Writer    Writer
	HTTP      *http.Client // nil = default client with timeouts
	PlexTVURL string       // "" = DefaultPlexTVURL
	Log       *slog.Logger
}

// Status is the job's progress.
type Status struct {
	Phase string `json:"phase"` // connecting | reading_library | importing | done
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Account sources.
const (
	SourceFull        = "full"
	SourceHistoryOnly = "history_only"
)

// Report is the result of a dry run or an apply run.
type Report struct {
	DryRun         bool            `json:"dryRun"`
	Matched        int             `json:"matched"`
	MatchedBy      map[Method]int  `json:"matchedBy"`
	Accounts       []AccountReport `json:"accounts"`
	Unmatched      []Unmatched     `json:"unmatched"`
	UnmatchedTotal int             `json:"unmatchedTotal"`
	Conflicts      []Conflict      `json:"conflicts"`
	ConflictsTotal int             `json:"conflictsTotal"`
	Warnings       []string        `json:"warnings"`
	NotImported    []string        `json:"notImported"`
}

// AccountReport summarises one Plex account → profile import.
type AccountReport struct {
	PlexAccountID  int64  `json:"plexAccountId"`
	PlexName       string `json:"plexName"`
	ProfileID      int64  `json:"profileId"`
	Source         string `json:"source"`
	Items          int    `json:"items"`
	Watched        int    `json:"watched"`
	InProgress     int    `json:"inProgress"`
	HistoryEntries int    `json:"historyEntries"`
	Ratings        int    `json:"ratings"`
	Unmatched      int    `json:"unmatched"`
	Updated        int    `json:"updated"`
	Unchanged      int    `json:"unchanged"`
	HistoryAdded   int    `json:"historyAdded"`
	RatingsAdded   int    `json:"ratingsAdded"`
}

// Unmatched is a Plex item with watch data that has no Lisimflix item.
type Unmatched struct {
	RatingKey string `json:"ratingKey"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Reason    Reason `json:"reason"`
}

// Conflict is a Plex item that fits several Lisimflix items; nothing is written for it.
type Conflict struct {
	RatingKey        string  `json:"ratingKey"`
	Title            string  `json:"title"`
	Reason           Reason  `json:"reason"`
	CandidateItemIDs []int64 `json:"candidateItemIds"`
}

// NotImported lists what Plex does not expose (shown in every report).
var NotImported = []string{
	"Plex watchlists (kept on plex.tv Discover, not on the server)",
	"In-progress positions and ratings for accounts without a server token (history only records finished plays)",
	"Plays of items that are no longer in the Plex library",
	"Watch history that Plex has already pruned",
	"Per-item audio/subtitle choices, playlists, collections and 'remove from Continue Watching'",
	"Music and photo libraries",
}

const maxListed = 500

// Run imports (or, with req.DryRun, previews) Plex watch state into profiles.
func Run(ctx context.Context, deps Deps, req Request, progress func(Status)) (Report, error) {
	if progress == nil {
		progress = func(Status) {}
	}
	if err := req.Validate(); err != nil {
		return Report{}, err
	}
	for _, a := range req.Accounts {
		ok, err := deps.Writer.ProfileExists(ctx, a.ProfileID)
		if err != nil {
			return Report{}, err
		}
		if !ok {
			return Report{}, fmt.Errorf("%w: profile %d does not exist", ErrBadRequest, a.ProfileID)
		}
	}
	owner, err := NewClient(req.BaseURL, req.Token, deps.HTTP)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	r := &run{deps: deps, req: req, owner: owner, outcomes: map[string]int64{}, rep: Report{
		DryRun: req.DryRun, MatchedBy: map[Method]int{}, Accounts: []AccountReport{},
		Unmatched: []Unmatched{}, Conflicts: []Conflict{}, Warnings: []string{}, NotImported: NotImported,
	}}
	progress(Status{Phase: "connecting"})
	if err := r.connect(ctx); err != nil {
		return Report{}, err
	}
	progress(Status{Phase: "reading_library"})
	if err := r.readLibrary(ctx); err != nil {
		return Report{}, err
	}
	for i, a := range req.Accounts {
		progress(Status{Phase: "importing", Done: i, Total: len(req.Accounts)})
		ar, err := r.importAccount(ctx, a)
		if err != nil {
			return Report{}, fmt.Errorf("import plex account %d: %w", a.PlexAccountID, err)
		}
		r.rep.Accounts = append(r.rep.Accounts, ar)
	}
	progress(Status{Phase: "done", Done: len(req.Accounts), Total: len(req.Accounts)})
	return r.rep, nil
}

type run struct {
	deps       Deps
	req        Request
	owner      *Client
	machineID  string
	names      map[int64]string
	tokens     map[int64]string
	ownerItems []Metadata          // owner listings of movies and episodes (state + match catalog)
	byKey      map[string]Metadata // ratingKey → owner listing entry
	matcher    *Matcher
	outcomes   map[string]int64 // ratingKey → Lisimflix item (0 = not matched)
	rep        Report
}

func (r *run) warn(format string, args ...any) {
	r.rep.Warnings = append(r.rep.Warnings, fmt.Sprintf(format, args...))
}

func (r *run) connect(ctx context.Context) error {
	ident, err := r.owner.Identity(ctx)
	if err != nil {
		return err
	}
	r.machineID = ident.MachineIdentifier
	accts, err := r.owner.Accounts(ctx)
	if err != nil {
		return err
	}
	r.names = map[int64]string{}
	for _, a := range accts {
		if a.ID > 0 {
			r.names[a.ID] = a.Name
		}
	}
	for _, a := range r.req.Accounts {
		if _, ok := r.names[a.PlexAccountID]; !ok {
			return fmt.Errorf("%w: plex account %d not found on this server", ErrBadRequest, a.PlexAccountID)
		}
	}
	r.tokens = map[int64]string{OwnerAccountID: r.req.Token}
	for _, a := range r.req.Accounts {
		if a.Token != "" {
			r.tokens[a.PlexAccountID] = a.Token
		}
	}
	if r.req.DiscoverUserTokens {
		plexTV := r.deps.PlexTVURL
		if plexTV == "" {
			plexTV = DefaultPlexTVURL
		}
		shared, err := SharedUserTokens(ctx, r.deps.HTTP, plexTV, r.req.Token, r.machineID)
		if err != nil {
			r.warn("could not read per-user tokens from plex.tv (%v); accounts without a token were imported from history only", err)
		} else {
			for id, tok := range shared {
				if _, ok := r.tokens[id]; !ok {
					r.tokens[id] = tok
				}
			}
		}
	}
	return nil
}

func (r *run) readLibrary(ctx context.Context) error {
	sections, err := r.owner.Sections(ctx)
	if err != nil {
		return err
	}
	var shows []Metadata
	for _, s := range sections {
		switch s.Type {
		case "movie":
			items, err := r.owner.Items(ctx, s.Key, 1)
			if err != nil {
				return err
			}
			r.ownerItems = append(r.ownerItems, items...)
		case "show":
			sh, err := r.owner.Items(ctx, s.Key, 2)
			if err != nil {
				return err
			}
			shows = append(shows, sh...)
			eps, err := r.owner.Items(ctx, s.Key, 4)
			if err != nil {
				return err
			}
			r.ownerItems = append(r.ownerItems, eps...)
		}
	}
	r.byKey = make(map[string]Metadata, len(r.ownerItems))
	for _, md := range r.ownerItems {
		r.byKey[md.RatingKey] = md
	}
	r.matcher = NewMatcher(r.deps.Catalog, r.req.PathMappings, shows)
	return nil
}

// listingFor returns movies and episodes as seen by one account's token.
func (r *run) listingFor(ctx context.Context, accountID int64, token string) ([]Metadata, error) {
	if accountID == OwnerAccountID && token == r.req.Token {
		return r.ownerItems, nil
	}
	c := r.owner.WithToken(token)
	sections, err := c.Sections(ctx)
	if err != nil {
		return nil, err
	}
	var out []Metadata
	for _, s := range sections {
		typ := 0
		switch s.Type {
		case "movie":
			typ = 1
		case "show":
			typ = 4
		default:
			continue
		}
		items, err := c.Items(ctx, s.Key, typ)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

type accState struct {
	title, typ string
	watched    bool
	playCount  int
	positionMs int64
	durationMs int64
	lastPlayed time.Time
	rating     *float64
	history    []time.Time
}

func (s *accState) imported() userdata.Imported {
	return userdata.Imported{Watched: s.watched, PlayCount: s.playCount, PositionMs: s.positionMs,
		DurationMs: s.durationMs, LastPlayed: s.lastPlayed, History: s.history, Rating: s.rating}
}

func unixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

func displayTitle(md Metadata) string {
	if md.Type == "episode" && md.ParentIndex != nil && md.Index != nil {
		return fmt.Sprintf("%s S%02dE%02d %s", md.GrandparentTitle, *md.ParentIndex, *md.Index, md.Title)
	}
	if md.Year > 0 {
		return fmt.Sprintf("%s (%d)", md.Title, md.Year)
	}
	return md.Title
}

func (r *run) importAccount(ctx context.Context, a AccountMapping) (AccountReport, error) {
	ar := AccountReport{PlexAccountID: a.PlexAccountID, PlexName: r.names[a.PlexAccountID],
		ProfileID: a.ProfileID, Source: SourceHistoryOnly}
	states := map[string]*accState{}
	get := func(md Metadata) *accState {
		st, ok := states[md.RatingKey]
		if !ok {
			st = &accState{title: displayTitle(md), typ: md.Type}
			states[md.RatingKey] = st
		}
		return st
	}

	if tok, ok := r.tokens[a.PlexAccountID]; ok {
		listing, err := r.listingFor(ctx, a.PlexAccountID, tok)
		switch {
		case errors.Is(err, ErrUnauthorized):
			r.warn("the server token for Plex account %q was rejected; imported its history only", ar.PlexName)
		case err != nil:
			return ar, err
		default:
			ar.Source = SourceFull
			for _, md := range listing {
				if md.ViewCount == 0 && md.ViewOffset == 0 && md.UserRating == 0 {
					continue
				}
				st := get(md)
				st.watched = md.ViewCount > 0
				st.playCount = md.ViewCount
				st.positionMs, st.durationMs = md.ViewOffset, md.Duration
				st.lastPlayed = unixTime(md.LastViewedAt)
				if md.UserRating > 0 {
					v := md.UserRating
					st.rating = &v
				}
			}
		}
	}

	hist, err := r.owner.History(ctx, a.PlexAccountID)
	if err != nil {
		return ar, err
	}
	ar.HistoryEntries = len(hist)
	for _, h := range hist {
		st := get(h)
		played := unixTime(h.ViewedAt)
		if played.IsZero() {
			continue
		}
		st.history = append(st.history, played)
		if ar.Source == SourceHistoryOnly {
			st.watched = true
			st.playCount++
			if played.After(st.lastPlayed) {
				st.lastPlayed = played
			}
		}
	}

	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		st := states[key]
		ar.Items++
		if st.watched {
			ar.Watched++
		}
		if st.positionMs > 0 {
			ar.InProgress++
		}
		if st.rating != nil {
			ar.Ratings++
		}
		itemID, err := r.resolve(ctx, key, st)
		if err != nil {
			return ar, err
		}
		if itemID == 0 {
			ar.Unmatched++
			continue
		}
		res, err := r.deps.Writer.Merge(ctx, a.ProfileID, itemID, st.imported(), r.req.DryRun)
		if err != nil {
			return ar, fmt.Errorf("merge %s into item %d: %w", key, itemID, err)
		}
		if res.StateChanged {
			ar.Updated++
		} else {
			ar.Unchanged++
		}
		ar.HistoryAdded += res.HistoryAdded
		if res.RatingSet {
			ar.RatingsAdded++
		}
	}
	return ar, nil
}

// resolve matches a Plex ratingKey once per run and records report entries.
func (r *run) resolve(ctx context.Context, key string, st *accState) (int64, error) {
	if id, ok := r.outcomes[key]; ok {
		return id, nil
	}
	md, ok := r.byKey[key]
	if !ok {
		r.outcomes[key] = 0
		r.addUnmatched(Unmatched{RatingKey: key, Title: st.title, Type: st.typ, Reason: ReasonNotInPlex})
		return 0, nil
	}
	m, err := r.matcher.Match(ctx, md)
	var me *MatchError
	switch {
	case err == nil:
		r.outcomes[key] = m.ItemID
		r.rep.Matched++
		r.rep.MatchedBy[m.Method]++
		return m.ItemID, nil
	case errors.As(err, &me):
		r.outcomes[key] = 0
		if me.Reason == ReasonAmbiguous {
			r.rep.ConflictsTotal++
			if len(r.rep.Conflicts) < maxListed {
				r.rep.Conflicts = append(r.rep.Conflicts, Conflict{RatingKey: key, Title: st.title,
					Reason: me.Reason, CandidateItemIDs: me.Candidates})
			}
		} else {
			r.addUnmatched(Unmatched{RatingKey: key, Title: st.title, Type: md.Type, Reason: me.Reason})
		}
		return 0, nil
	default:
		return 0, err
	}
}

func (r *run) addUnmatched(u Unmatched) {
	r.rep.UnmatchedTotal++
	if len(r.rep.Unmatched) < maxListed {
		r.rep.Unmatched = append(r.rep.Unmatched, u)
	}
}
```

- [ ] **Step 8: Run tests, expect PASS**

Run: `make test`
Expected: all `userdata` and `importer/plex` tests pass. If a count in `TestRunApply` is off,
compare it with the worked counts under Step 5 before changing code: they follow directly from
the fixtures.

- [ ] **Step 9: Commit**

```bash
git add server/internal/userdata/merge.go server/internal/userdata/merge_test.go server/internal/importer/plex/engine.go server/internal/importer/plex/engine_test.go
git commit -m "feat(importer): Plex import engine with dry run, never-downgrade merge and report"
```

---

### Task 15: Background import jobs and the admin API

**Files:**
- Create: `server/internal/importer/plex/jobs.go`, `server/internal/importer/plex/http.go`
- Test: `server/internal/importer/plex/jobs_test.go`, `server/internal/importer/plex/http_test.go`

**Interfaces:**
- Consumes: `Run`, `Request`, `Deps`, `Status`, `Report`, `NewClient`, `ErrBadRequest`, `ErrUnauthorized`; `authctx.Guard`, `httpx`
- Produces:
  ```go
  var ErrBusy = errors.New("a Plex import is already running")
  const (JobRunning = "running"; JobDone = "done"; JobFailed = "failed")
  type Job struct { ID string `json:"id"`; State string `json:"state"`; DryRun bool `json:"dryRun"`; StartedAt time.Time `json:"startedAt"`;
      FinishedAt *time.Time `json:"finishedAt,omitempty"`; Progress Status `json:"progress"`; Report *Report `json:"report,omitempty"`; Error string `json:"error,omitempty"` }
  func NewManager(deps Deps) *Manager
  func (m *Manager) Start(req Request) (Job, error) // validates; ErrBusy when one runs
  func (m *Manager) Get(id string) (Job, bool)
  func (m *Manager) Close()                        // cancels a running job and waits
  func NewModule(jobs *Manager, deps Deps) *Module   // app.Module, Name() == "plex_import"
  ```
  Routes (all `RequireAdmin`, under `/api/v1/import/plex`):

  | Method & path | Body | Success | Errors |
  |---|---|---|---|
  | `POST /accounts` | `{"baseUrl","token"}` | 200 `{"machineIdentifier","version","accounts":[{"id","name","owner"}]}` | 400 `invalid_body`, `invalid_plex_url`, `plex_unauthorized`; 502 `plex_unreachable` |
  | `POST /jobs` | `Request` JSON | 202 `Job` | 400 `invalid_request`; 409 `import_running` |
  | `GET /jobs/{jobId}` | | 200 `Job` | 404 `not_found` |

  **Token handling decision:** tokens are **never stored**. A token exists only in the request body
  and in the memory of the goroutine running that one job. It is dropped when the job ends, never
  written to the DB, logs, the `Job` struct or error messages (the client sends it only as a
  header). The admin pastes it again for the apply run after reviewing the dry run. Jobs live in
  memory (last 10 kept). A restart loses the report, and the admin can simply re-run the dry run.
  Because the admin supplies the base URL, the server will contact any URL an admin enters. This is
  acceptable because only admins can reach the route.

- [ ] **Step 1: Write the failing tests**

`server/internal/importer/plex/jobs_test.go`:
```go
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func waitJob(t *testing.T, m *Manager, id string) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, ok := m.Get(id)
		if !ok {
			t.Fatalf("job %s vanished", id)
		}
		if j.State != JobRunning {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still running", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagerRunsDryRun(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	m := NewManager(f.deps(p))
	defer m.Close()
	job, err := m.Start(f.request(p, true))
	if err != nil || job.State != JobRunning || !job.DryRun || job.ID == "" {
		t.Fatalf("start %+v %v", job, err)
	}
	done := waitJob(t, m, job.ID)
	if done.State != JobDone || done.Report == nil || done.Report.Matched != 6 || done.FinishedAt == nil || done.Progress.Phase != "done" {
		t.Fatalf("job %+v", done)
	}
	b, _ := json.Marshal(done)
	if strings.Contains(string(b), "owner-token") || strings.Contains(string(b), "tokA") {
		t.Fatal("token leaked into job JSON")
	}
}

func TestManagerBusyAndFailed(t *testing.T) {
	m := NewManager(Deps{})
	defer m.Close()
	release := make(chan struct{})
	m.runFn = func(ctx context.Context, _ Deps, _ Request, progress func(Status)) (Report, error) {
		progress(Status{Phase: "importing", Done: 1, Total: 3})
		<-release
		return Report{}, errors.New("plex GET /accounts: status 500")
	}
	req := Request{BaseURL: "http://plex:32400", Token: "t", Accounts: []AccountMapping{{PlexAccountID: 1, ProfileID: 1}}}
	job, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(req); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
	close(release)
	failed := waitJob(t, m, job.ID)
	if failed.State != JobFailed || !strings.Contains(failed.Error, "status 500") || failed.Report != nil {
		t.Fatalf("failed job %+v", failed)
	}
	if _, err := m.Start(Request{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("invalid request: %v", err)
	}
	if _, ok := m.Get("nope"); ok {
		t.Fatal("unknown job found")
	}
}

func TestManagerKeepsLastTen(t *testing.T) {
	m := NewManager(Deps{})
	defer m.Close()
	m.runFn = func(context.Context, Deps, Request, func(Status)) (Report, error) { return Report{}, nil }
	req := Request{BaseURL: "http://plex:32400", Token: "t", Accounts: []AccountMapping{{PlexAccountID: 1, ProfileID: 1}}}
	var first string
	for i := 0; i < 12; i++ {
		j, err := m.Start(req)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = j.ID
		}
		waitJob(t, m, j.ID)
	}
	if _, ok := m.Get(first); ok {
		t.Fatal("oldest job not pruned")
	}
}
```

`server/internal/importer/plex/http_test.go`:
```go
package plex

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

func importAPI(t *testing.T, f *importFixture, p *fakePMS, admin bool) http.Handler {
	t.Helper()
	deps := f.deps(p)
	jobs := NewManager(deps)
	t.Cleanup(jobs.Close)
	mod := NewModule(jobs, deps)
	if mod.Name() != "plex_import" {
		t.Fatal(mod.Name())
	}
	r := chi.NewRouter()
	mod.Mount(r, authctx.StaticGuard{P: authctx.Principal{UserID: 1, IsAdmin: admin}})
	return r
}

func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	return rec
}

func TestAccountsEndpoint(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	h := importAPI(t, f, p, true)
	rec := send(h, "POST", "/import/plex/accounts", `{"baseUrl":"`+p.URL()+`","token":"owner-token"}`)
	var body struct {
		MachineIdentifier string
		Accounts          []struct {
			ID    int64
			Name  string
			Owner bool
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body.MachineIdentifier != "abc123machine" || len(body.Accounts) != 3 || !body.Accounts[0].Owner || body.Accounts[1].Owner {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "owner-token") {
		t.Fatal("token echoed")
	}
	rec = send(h, "POST", "/import/plex/accounts", `{"baseUrl":"`+p.URL()+`","token":"wrong"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "plex_unauthorized") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = send(h, "POST", "/import/plex/accounts", `{"baseUrl":"plex","token":"x"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_plex_url") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = send(h, "POST", "/import/plex/accounts", `{"baseUrl":"http://127.0.0.1:1","token":"x"}`)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "plex_unreachable") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestJobEndpoints(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	h := importAPI(t, f, p, true)
	reqJSON, _ := json.Marshal(f.request(p, true))
	rec := send(h, "POST", "/import/plex/jobs", string(reqJSON))
	var job Job
	json.Unmarshal(rec.Body.Bytes(), &job)
	if rec.Code != 202 || job.ID == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for job.State == JobRunning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		json.Unmarshal(send(h, "GET", "/import/plex/jobs/"+job.ID, "").Body.Bytes(), &job)
	}
	if job.State != JobDone || job.Report == nil || job.Report.Matched != 6 || job.Report.Accounts[2].Source != SourceHistoryOnly {
		t.Fatalf("job %+v", job)
	}
	if rec := send(h, "GET", "/import/plex/jobs/nope", ""); rec.Code != 404 {
		t.Fatalf("unknown job: %d", rec.Code)
	}
	if rec := send(h, "POST", "/import/plex/jobs", `{"baseUrl":"x","token":"y","accounts":[]}`); rec.Code != 400 ||
		!strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("invalid: %d %s", rec.Code, rec.Body.String())
	}
}

func TestImportRequiresAdmin(t *testing.T) {
	f := newImportFixture(t)
	p := newFakePMS(t)
	h := importAPI(t, f, p, false)
	if rec := send(h, "POST", "/import/plex/jobs", `{}`); rec.Code != 403 {
		t.Fatalf("non-admin: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewManager`

- [ ] **Step 3: Implement the job manager**

`server/internal/importer/plex/jobs.go`:
```go
package plex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrBusy is returned when an import is already running.
var ErrBusy = errors.New("a Plex import is already running")

// Job states.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

const maxJobs = 10

// Job is one import run as shown to the admin. It never holds tokens.
type Job struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	DryRun     bool       `json:"dryRun"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Progress   Status     `json:"progress"`
	Report     *Report    `json:"report,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Manager runs one import at a time in the background and keeps recent jobs in memory.
type Manager struct {
	deps   Deps
	runFn  func(context.Context, Deps, Request, func(Status)) (Report, error)
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	jobs    map[string]*Job
	order   []string
	running bool
}

// NewManager returns a manager that runs Run with deps.
func NewManager(deps Deps) *Manager {
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{deps: deps, runFn: Run, now: time.Now, ctx: ctx, cancel: cancel, jobs: map[string]*Job{}}
}

// Start validates req and runs it in the background.
func (m *Manager) Start(req Request) (Job, error) {
	if err := req.Validate(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return Job{}, ErrBusy
	}
	j := &Job{ID: newJobID(), State: JobRunning, DryRun: req.DryRun, StartedAt: m.now().UTC()}
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	m.prune()
	m.running = true
	m.wg.Add(1)
	go m.execute(j.ID, req)
	return *j, nil
}

func (m *Manager) execute(id string, req Request) {
	defer m.wg.Done()
	rep, err := m.runFn(m.ctx, m.deps, req, func(s Status) {
		m.mu.Lock()
		m.jobs[id].Progress = s
		m.mu.Unlock()
	})
	req = Request{} // drop the tokens as soon as the run ends
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	finished := m.now().UTC()
	j.FinishedAt = &finished
	if err != nil {
		j.State, j.Error = JobFailed, err.Error()
		m.deps.Log.Warn("plex import failed", "job", id, "err", err)
	} else {
		j.State, j.Report = JobDone, &rep
		m.deps.Log.Info("plex import finished", "job", id, "dry_run", rep.DryRun, "matched", rep.Matched)
	}
	m.running = false
}

// prune drops the oldest finished jobs beyond maxJobs. Caller holds mu.
func (m *Manager) prune() {
	for len(m.order) > maxJobs {
		removed := false
		for i, id := range m.order {
			if m.jobs[id].State != JobRunning {
				delete(m.jobs, id)
				m.order = append(m.order[:i], m.order[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			return
		}
	}
}

// Get returns a copy of a job.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

// Close cancels a running import and waits for it to stop.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
}

func newJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: Implement the HTTP module**

`server/internal/importer/plex/http.go`:
```go
package plex

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// Module serves the admin-only Plex import API under /api/v1/import/plex.
type Module struct {
	jobs *Manager
	deps Deps
	log  *slog.Logger
}

// NewModule builds the module.
func NewModule(jobs *Manager, deps Deps) *Module {
	log := deps.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{jobs: jobs, deps: deps, log: log}
}

// Name implements app.Module.
func (m *Module) Name() string { return "plex_import" }

// Mount implements app.Module.
func (m *Module) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/import/plex", func(r chi.Router) {
		r.Use(g.RequireAdmin)
		r.Post("/accounts", m.listAccounts)
		r.Post("/jobs", m.startJob)
		r.Get("/jobs/{jobId}", m.getJob)
	})
}

func (m *Module) plexError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnauthorized) {
		httpx.WriteError(w, http.StatusBadRequest, "plex_unauthorized", "Plex rejected the token")
		return
	}
	m.log.Warn("plex import: server request failed", "err", err)
	httpx.WriteError(w, http.StatusBadGateway, "plex_unreachable", "could not reach the Plex server")
}

type accountJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Owner bool   `json:"owner"`
}

func (m *Module) listAccounts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"baseUrl"`
		Token   string `json:"token"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	c, err := NewClient(body.BaseURL, body.Token, m.deps.HTTP)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_plex_url", err.Error())
		return
	}
	ident, err := c.Identity(r.Context())
	if err != nil {
		m.plexError(w, err)
		return
	}
	accts, err := c.Accounts(r.Context())
	if err != nil {
		m.plexError(w, err)
		return
	}
	out := make([]accountJSON, 0, len(accts))
	for _, a := range accts {
		if a.ID > 0 {
			out = append(out, accountJSON{ID: a.ID, Name: a.Name, Owner: a.ID == OwnerAccountID})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"machineIdentifier": ident.MachineIdentifier, "version": ident.Version, "accounts": out,
	})
}

func (m *Module) startJob(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	job, err := m.jobs.Start(req)
	switch {
	case errors.Is(err, ErrBadRequest):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrBusy):
		httpx.WriteError(w, http.StatusConflict, "import_running", err.Error())
	case err != nil:
		m.log.Error("plex import: start job", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	default:
		httpx.WriteJSON(w, http.StatusAccepted, job)
	}
}

func (m *Module) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok := m.jobs.Get(chi.URLParam(r, "jobId"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such import job")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, job)
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`
Expected: PASS. The `-race` CI run also covers the manager (progress callbacks lock `mu`).

- [ ] **Step 6: Commit**

```bash
git add server/internal/importer/plex/jobs.go server/internal/importer/plex/jobs_test.go server/internal/importer/plex/http.go server/internal/importer/plex/http_test.go
git commit -m "feat(importer): background Plex import jobs and admin API"
```

---

### Task 16: Wire both modules into the server, update the index, open the PR

**Files:**
- Modify: `server/cmd/lisimflix/main.go` (additive: build stores and modules, append them to `app.New`)
- Modify: `server/cmd/lisimflix/main_test.go` (add one test)
- Modify: `docs/superpowers/plans/2026-10-24-phase1-index.md` (1F section only)

**Interfaces:**
- Consumes: `userdata.NewStore/LoadRules/NewModule/Deps/Access/TopLevelRatings/AllowedByAge`, `plex.NewManager/NewModule/Deps`,
  `media.NewStore`, `library.NewStore`, `metadata.NewStore` + `Load`, `auth.RatingAge`, the `*auth.Service` value 1B created in `run()`
- Produces: `/api/v1/me/...` and `/api/v1/import/plex/...` live in the binary.

- [ ] **Step 1: Write the failing test**

Append to `server/cmd/lisimflix/main_test.go`. It reuses `freeAddr` and the env/healthcheck
pattern from 1A's `TestServeHealthcheckAndShutdown`, and adds `"net/http"` to the imports if
it isn't there yet:
```go
func TestUserDataAndPlexImportRoutesMounted(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	env := map[string]string{
		"LISIMFLIX_CONFIG_DIR": dir + "/config",
		"LISIMFLIX_CACHE_DIR":  dir + "/cache",
		"LISIMFLIX_MEDIA_DIR":  dir + "/media",
		"LISIMFLIX_HTTP_ADDR":  addr,
	}
	getenv := func(k string) string { return env[k] }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var logs bytes.Buffer
	go func() { done <- run(ctx, nil, getenv, &logs, &logs) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(10 * time.Second)
	for run(context.Background(), []string{"healthcheck"}, getenv, io.Discard, io.Discard) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy; logs:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Unauthenticated: a mounted route answers 401, an unknown one 404.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/me/home"},
		{"PUT", "/api/v1/me/progress/1"},
		{"POST", "/api/v1/import/plex/jobs"},
	} {
		req, _ := http.NewRequest(c.method, "http://"+addr+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d, want 401 (route mounted, guard active)", c.method, c.path, resp.StatusCode)
		}
	}
}
```
(Add `"io"` and `"net/http"` to the test imports if they are missing.)

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL. `GET /api/v1/me/home = 404, want 401`.

- [ ] **Step 3: Wire the modules (additive edit)**

In `server/cmd/lisimflix/main.go` add the imports
```go
	"github.com/Lisimflix/lisimflix/server/internal/importer/plex"
	"github.com/Lisimflix/lisimflix/server/internal/userdata"
```
(and `auth`, `library`, `media`, `metadata` if not already imported). Then, in `run()`, after
the database is migrated and after 1B's `*auth.Service` exists (called `authSvc` below; use the
variable name 1B actually chose) and before `app.New(...)`, insert:
```go
	// Plan 1F: per-profile user data and the Plex import tool.
	mediaStore := media.NewStore(d)
	metaStore := metadata.NewStore(d)
	udStore := userdata.NewStore(d)
	if rules, err := userdata.LoadRules(ctx, d); err != nil {
		log.Warn("user data rules: using defaults", "err", err)
	} else {
		udStore.SetRules(rules)
	}
	udModule := userdata.NewModule(userdata.Deps{
		Store:     udStore,
		Catalog:   mediaStore,
		Libraries: library.NewStore(d),
		Access: userdata.Access{
			Policy: authSvc,
			Ratings: userdata.TopLevelRatings{Catalog: mediaStore, Lookup: func(ctx context.Context, id int64) (string, error) {
				v, _, err := metaStore.Load(ctx, id)
				return v.ContentRating, err
			}},
			Allowed: userdata.AllowedByAge(auth.RatingAge),
		},
		Log: log,
	})
	plexDeps := plex.Deps{Catalog: mediaStore, Writer: udStore, Log: log}
	plexJobs := plex.NewManager(plexDeps)
	defer plexJobs.Close()
	plexModule := plex.NewModule(plexJobs, plexDeps)
```
Then append `udModule, plexModule` to the end of the existing module list in the `app.New(...)`
call. Don't reorder or remove anything else.

- [ ] **Step 4: Run all tests and vet**

Run: `make fmt && make test && make vet`
Expected: all packages PASS (including `cmd/lisimflix`), vet clean.

- [ ] **Step 5: Manual check against the real Plex (zraid, optional but recommended)**

```bash
make run   # in another terminal; sign in as admin per 1B, keep the session cookie in $C
read -rs PLEX_TOKEN
curl -s -b "$C" -X POST localhost:8420/api/v1/import/plex/accounts \
  -d "{\"baseUrl\":\"http://zraid:32400\",\"token\":\"$PLEX_TOKEN\"}" | jq
# map accounts to profiles, dry run first:
curl -s -b "$C" -X POST localhost:8420/api/v1/import/plex/jobs -d "{\"baseUrl\":\"http://zraid:32400\",\"token\":\"$PLEX_TOKEN\",
  \"dryRun\":true,\"discoverUserTokens\":true,\"pathMappings\":[{\"plex\":\"/data\",\"local\":\"/media\"}],
  \"accounts\":[{\"plexAccountId\":1,\"profileId\":1}]}" | jq .id
curl -s -b "$C" localhost:8420/api/v1/import/plex/jobs/<id> | jq '.state, .report.matched, .report.unmatchedTotal, .report.conflictsTotal'
unset PLEX_TOKEN
```
Expected: the job reaches `done` with matched counts close to the number of watched items in Plex.
Check the Plex path prefix in Plex's library settings before running (the zraid Plex container's
media mount). Don't run an apply (`dryRun:false`) until the person who owns the data has looked at
the dry-run report.

- [ ] **Step 6: Update the phase index (1F section only)**

In `docs/superpowers/plans/2026-10-24-phase1-index.md`, replace the 1F block's `State` struct
and owned-tables line with:
```go
type State struct {
    ProfileID   int64
    ItemID      int64
    PositionMs  int64 // resume point; 0 = start over
    DurationMs  int64 // added by 1F (additive)
    Watched     bool
    PlayCount   int
    LastPlayed  time.Time
    HiddenFromContinue bool
}
type Store struct{ /* ... */ }
func NewStore(d *db.DB) *Store
func (s *Store) Get(ctx context.Context, profileID, itemID int64) (State, error) // zero State (IDs set), nil error when no row
func (s *Store) Put(ctx context.Context, st State) error
// Progress hook for phase 2 (credits markers):
type CreditsSource interface { CreditsStartMs(ctx context.Context, itemID int64) (int64, bool, error) }
```
```
Tables owned: `user_item_state`, `watch_history`, `watchlist`, `ratings`, `track_prefs`, `home_row_prefs`.
Routes: individual `/me/...` paths (no `r.Route("/me")`; other modules must not mount `/me`) and `/import/plex/*`.
1F also added `media.Store.FindByExternalID/FindByFilePath/FindByTitleYear/RecentlyAdded` (server/internal/media/lookup.go).
```

- [ ] **Step 7: Commit, push, open the PR**

```bash
git add server/cmd/lisimflix/main.go server/cmd/lisimflix/main_test.go docs/superpowers/plans/2026-10-24-phase1-index.md
git commit -m "feat(userdata): wire user data and Plex import modules into the server"
git push -u origin feat/1f-userdata-plex
gh pr create --base main --title "feat(userdata): per-profile watch state, home rows and Plex import" \
  --body "$(printf '## What changed\nPlan 1F: user data (progress, watched, history, watchlist, ratings, track prefs, home rows) and the admin Plex import (dry run + apply). Adds media lookups in server/internal/media/lookup.go (1C owner please review that file).\n\n## Source\nSpec §3.4, §3.5, §5.2; plan docs/superpowers/plans/2026-10-24-phase1f-userdata-plex.md\n\n## How it was tested\nmake test/vet (fake PMS + plex.tv fixtures); optional dry run against the zraid Plex (Task 16 Step 5)\n')"
gh pr checks --watch
```
Expected: CI `test` passes. Another member reviews (`/code-review` first pass, then reads it
themselves). Once approved, the **author** squash-merges. An AI agent never merges. It hands back
to its human (see `CLAUDE.md`).

---

## Done when

- `make test` and `make vet` pass. CI is green on the PR.
- Signed-in profiles can report progress, mark shows/seasons watched, hide items from Continue
  Watching, manage watchlist/ratings/track prefs, page through history, and get
  `GET /api/v1/me/home` rows that respect library visibility and content rating.
- An admin can list Plex accounts, run a dry-run import that reports matched, unmatched and
  conflicting items, then apply it without lowering any existing Lisimflix state.

## Self-review notes

- **Spec coverage:** 3.4 → Tasks 2–6 and 10–11 (progress, watched, history, watchlist,
  ratings, per-show tracks, remove from Continue Watching). 3.5 → Tasks 12–15 (admin token, per-user
  state, GUID/TMDB/TVDB plus path matching, profile mapping, dry run first). 5.2 Home → Tasks 8–9
  (Continue Watching, Next Up, Recently Added per library, Watchlist, reorder/hide). Collections
  and admin-shared rows wait for their features (no collections module yet). 5.3 Plex import tool →
  Task 15 API (UI in phase 3). AniList/MAL sync is out of scope.
- **Credits hook:** `CreditsSource` is consulted on every progress report; `NoCredits` until phase 2.
- **Open questions:** (1) Does `shared_servers` include managed home users on the household's
  plex.tv plan? If not, those users fall back to history only. Task 16 Step 5 shows it.
  (2) Should Next Up show the next episode during a rewatch (all later episodes already watched)?
  This plan shows nothing in that case, the same as before. (3) New libraries' Recently Added rows go to
  the end of a customised layout.
