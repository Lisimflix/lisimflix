# Phase 1D: Metadata Core Implementation Plan

> **Realigned 2026-10-26: draft reference, not executable as-is.** Feature IDs: META-1 (T1–T8, T10–T16), META-3 (T9 Fanart.tv). Gates: META-1 P0 G1; META-3 P2 G4.
> Unclaimed and unapproved. Before claiming, read [the catalogue plans map](2026-10-24-catalogue-plans-map.md):
> its **alignment deltas** replace this plan's references to the retired phase 1 index and plan 1A
> (`authctx`, `httpx`, `app.Module`, `dbtest`, `make test`) with CTR-1 contracts, the OPS-1 runtime, OPS-2
> storage and `make check`. Specific to this plan: Check match provenance, revision and confidence against spec §3.7 before claiming.

> **Do not execute.** Archived draft for reference only: it predates the revised spec and the CTR-1/OPS-1/OPS-2 contracts. Claim the feature and write a new plan through the planning gate instead.

**Goal:** Every movie and TV show the 1C scanner finds is matched to TMDB (with Fanart.tv artwork
and local Kodi NFO overrides), its metadata and artwork are stored and cached locally, and admins
can fix matches and edit/lock fields over the API.

**Architecture:** Package `server/internal/metadata` holds the fixed contract types, a `Registry`
of ordered providers with per-library-type field-priority merging, a cached + rate-limited HTTP
`Fetcher`, a `Store` for the 1D-owned tables, a `Matcher` that implements 1C's `ScanListener`
and runs a retrying background `Queue`, and the HTTP `Module`. Providers live in subpackages
(`tmdb`, `fanart`, `nfo`) that import `metadata` (never the reverse). `metadata/artwork`
downloads images to `cfg.ConfigDir/artwork/` and builds JPEG variants in pure Go.

**Tech Stack:** Go 1.27, chi v5, modernc.org/sqlite, `golang.org/x/image` (draw, webp),
`golang.org/x/text` (diacritic folding, legacy NFO charsets), `golang.org/x/time/rate`.

**Spec:** `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (section 3.2; also 2.2, 6.2, 7)
**Index / shared contracts:** `docs/superpowers/plans/2026-10-24-catalogue-plans-map.md`
**Conventions:** `docs/superpowers/plans/2026-10-24-catalogue-plans-map.md`, `CONTRIBUTING.md`, `CLAUDE.md`

## Global Constraints

- Go 1.27 (`golang:1.27-trixie`). Module `github.com/Lisimflix/lisimflix/server` in `server/`. `CGO_ENABLED=0` (all image code is pure Go).
- SQLite via `modernc.org/sqlite`. Router chi v5. Logging `log/slog` only.
- All routes under `/api/v1`, JSON only, errors via `httpx.WriteError` envelope `{"error":{"code":"snake_case_code","message":"human text"}}`.
- `/media` is **read-only**: NFO and local artwork are only ever read. Artwork is written only under `cfg.ConfigDir/artwork/`.
- Migrations in `server/internal/db/migrations/`, named `YYYYMMDDHHMMSS_<module>_<what>.sql`; this plan uses `20261025120000_metadata_tables.sql`. `STRICT` tables, explicit FKs with `ON DELETE`.
- TDD for every task. **No network in unit tests**: providers are tested against recorded JSON fixtures in each package's `testdata/` via `httptest.Server`. The only network test is behind build tag `live`.
- No secrets in the repo or logs. API keys come from env (`LISIMFLIX_TMDB_API_KEY`, `LISIMFLIX_FANART_API_KEY`) or `server_settings` (`metadata.tmdb.api_key`, `metadata.fanart.api_key`); errors never contain the request URL.
- Contract names in the index are fixed. 1C types are used only through the index contracts and the 1C plan names listed below.
- Workflow (CONTRIBUTING.md): work on branch `feat/metadata-core` (never commit to or push `main`); per-task commits are fine; the PR title is `feat(metadata): metadata core with TMDB, Fanart.tv and NFO`; the PR template's **Source** section says `Spec §3.2 Metadata (plan 1D)` (plus `Closes #N` if there is an issue); the reviewer runs `/code-review` on the branch first, then reads and approves; after approval the **author** squash-merges. Do not overwrite `CONTRIBUTING.md`, `CLAUDE.md` or `.gitignore` (append only, and this plan needs no changes to them). AI-written commits end with a `Co-Authored-By:` line.
- Run everything through Docker: `make test`, `make vet`, `make fmt`, `make tidy`.

## 1C names this plan uses (no contract additions requested)

Plan 1C (`2026-10-24-lib-1-2-3-scanner.md`) already provides everything 1D needs, so **no
contract additions are requested**. Besides the index contracts, 1D uses these 1C additions:

- `scanner.ScanListener` / `(*scanner.Scanner).AddListener` live in package `scanner`
  (`server/internal/scanner`). 1D's `*metadata.Matcher` satisfies `scanner.ScanListener`.
- `library.Store.Create` (tests), `library.ErrNotFound`, `library.Visibility`
  (`VisibleLibraryIDs(ctx, authctx.Principal) ([]int64, error)`; returns every library for
  admins), `library.CanSee`, `library.AdminVisibility{Store}` (used until 1B's auth service is wired).
- `media.ErrNotFound` (from `GetItem` / `UpdateItem`), `media.Store.UpsertItem(ctx, libraryID, key, it) (int64, error)`
  and `media.Store.PutFile(ctx, libraryID, f) (int64, error)` (tests seed items and files with these),
  `Item.AirDate` (1D fills it for episodes), `Item.ExtraType` (extras are `video` children; 1D skips them).
- `UpdateItem` writes title, original/sort title, year, numbers, air date, match state, locked
  fields and **replaces** the external IDs. `UpsertItem` stops overwriting title/year once an item
  is matched, so 1D's matches survive rescans.
- `ItemsNeedingMatch` returns unmatched top-level movies and shows.
- 1C's media module registers flat routes `GET /items/{id}` and `GET /items/{id}/children`
  (no `r.Route("/items")`), so 1D adds its own flat `/items/{id}/...` routes beside them.
- `main.go`: `libraryModules(ctx, cfg, d, log) ([]app.Module, *scanner.Scanner, func())`; 1D takes
  the returned scanner and calls `AddListener` on it (Task 16).

## Merge rules (Registry)

`Registry.Merge(libType, records, preferLocal)` combines one `Record` per provider:

1. **Provider order per field.** Each library type has a `Priorities` map `field → []providerName`;
   field `"*"` is the default for fields not listed. Providers in the list come first, in list
   order; providers that returned a record but are not listed follow in the order the records were
   passed (registry order).
2. **Local NFO.** The `nfo` provider is removed from every list and then put **first** when the
   library's `prefer_local` setting is true (default) or **last** when false.
3. **Scalars** (`title`, `original_title`, `sort_title`, `overview`, `released`,
   `content_rating`, `year`): the first provider with a non-empty / non-zero value wins.
4. **Lists** (`genres`, `studios`, `tags`, `people`): the first non-empty list wins **whole**
   (vocabularies from different providers are never mixed).
5. **Maps** (`ids`, `ratings`, `alt_titles`): union; on a key conflict the earlier provider wins.
6. **Images:** per type (`images.poster`, `images.backdrop`, ...) an ordered union, de-duplicated
   by URL. The first image of a type is the "primary" one.
7. **Seasons:** the first provider with seasons supplies them (with episodes); other providers'
   season images are appended to the season with the same number.
8. `Merged.Sources[field]` records which provider supplied each field.

Default priorities (movies, tv, anime): `"*": [tmdb, fanart]`; `images.poster`,
`images.backdrop`, `images.thumb`: `[tmdb, fanart]`; `images.logo`, `images.clearart`,
`images.banner`: `[fanart, tmdb]`. Home videos: `"*": [nfo]`. Plan 1E changes anime with
`Registry.SetPriority`.

**Locks:** fields named in `Item.LockedFields` are never overwritten by matching. Lock names are
the field names above; `images` locks every image type; `images.<type>` locks one.
**Manual matches:** `MatchState "manual"` items are never searched again; they are refreshed only
by their stored IDs, and NFO IDs never replace them.

## File Structure

```
server/internal/db/migrations/20261025120000_metadata_tables.sql   1D tables
server/internal/metadata/
  types.go              contract types, constants, errors, ID helpers
  normalize.go          NormalizeTitle, TitleSimilarity, SortTitle
  score.go              ScoreCandidate, Rank, PickBest
  registry.go           Registry, Priorities, Merge, no-op mappers
  fetcher.go            Fetcher: rate limit, retry/backoff, metadata_cache
  settings.go           SettingsStore: env + server_settings, masking, validation
  store.go              Store: item_metadata, item_genres, people, item_people, images
  queue.go              retrying background work queue
  matcher.go            Matcher: match/refresh one item, search, manual match
  shows.go              apply season/episode records to child items
  listener.go           ScanListener (ItemsChanged), Start, Sweep, Enqueue
  images.go             Images service: download-on-demand + prefetch
  module.go             HTTP module (Mount, helpers)
  api_read.go           GET /items/{id}/metadata, GET /images/{id}
  api_settings.go       GET/PUT /metadata/settings
  api_write.go          match search/apply, PATCH metadata, refresh
  *_test.go             tests (helpers_test.go, matcher_helpers_test.go hold fakes/seeds)
server/internal/metadata/artwork/artwork.go(+_test)   download, JPEG variants, paths
server/internal/metadata/tmdb/tmdb.go, movie.go, tv.go(+tmdb_test.go, live_test.go, testdata/)
server/internal/metadata/fanart/fanart.go(+_test, testdata/)
server/internal/metadata/nfo/nfo.go(+_test, testdata/)
server/cmd/lisimflix/metadata.go                 metadataModule(): builds and starts everything
server/cmd/lisimflix/metadata_test.go            wiring test
server/cmd/lisimflix/main.go                     two-line additive edit
```

**Decisions:** images use `images.item_id` (nullable) plus `images.person_id` (nullable,
exactly one set) instead of a link table: an image row belongs to one owner, and stable row IDs
let `/api/v1/images/{id}` be cached as immutable. People are de-duplicated by profile image URL
(or lower-cased name when there is no image) because `Person` carries no provider ID.

---

### Task 1: Tables, contract types and test helpers

**Files:**
- Create: `server/internal/db/migrations/20261025120000_metadata_tables.sql`
- Create: `server/internal/metadata/types.go`
- Test: `server/internal/metadata/types_test.go`, `server/internal/metadata/helpers_test.go`

**Interfaces:**
- Consumes: `dbtest.Open`, `library.NewStore(d).Create`, `media.NewStore(d)` `UpsertItem`, `UpdateItem`, `GetItem`, `media.Item`, `media.Kind`, `library.Type`.
- Produces: the contract types `Query`, `Candidate`, `Record`, `Image`, `Person`, `SeasonRecord`, `EpisodeRecord`, `Provider`, `IDMapper`, `EpisodeMapper` (exactly as in the index); `LocalEpisodeReader`; constants `Lib*`, `Kind*`, `State*`, `Image*`, `Role*`, `Field*`, `LocalProviderName = "nfo"`, `LocalPathKey = "_path"`; errors `ErrNotFound`, `ErrNoUsableID`, `ErrNotConfigured`; `ImageTypes []string`; `ImageField(t) string`; `URLHash(u) string`; `PublicIDs(ids) map[string]string`; unexported `cloneIDs`, `hasRemoteID`, `lockSet`. Test helpers (package `metadata`, `_test.go`): `fakeProvider`, `seedLibrary(t, d, lt, opts...)`, `seedItem(t, d, it)`, `seedFile(t, d, libraryID, itemID, path)`, `intp(n)`.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/20261025120000_metadata_tables.sql`:
```sql
-- Metadata core (plan 1D). Items belong to plan 1C; rows here cascade with them.

CREATE TABLE item_metadata (
    item_id        INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    overview       TEXT NOT NULL DEFAULT '',
    released       TEXT NOT NULL DEFAULT '',
    content_rating TEXT NOT NULL DEFAULT '',
    studios        TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings
    tags           TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings
    alt_titles     TEXT NOT NULL DEFAULT '{}',   -- JSON object lang -> title
    ratings        TEXT NOT NULL DEFAULT '{}',   -- JSON object source -> 0..10
    sources        TEXT NOT NULL DEFAULT '{}',   -- JSON object field -> provider
    refreshed_at   TEXT NOT NULL
) STRICT;

CREATE TABLE item_genres (
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    genre    TEXT    NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (item_id, genre)
) STRICT;
CREATE INDEX item_genres_genre ON item_genres(genre COLLATE NOCASE);

CREATE TABLE people (
    id        INTEGER PRIMARY KEY,
    key       TEXT NOT NULL UNIQUE,   -- "img:<profile url>" or "name:<lower name>"
    name      TEXT NOT NULL,
    image_url TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX people_name ON people(name COLLATE NOCASE);

CREATE TABLE item_people (
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    role       TEXT    NOT NULL,
    character  TEXT    NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL,
    PRIMARY KEY (item_id, person_id, role, character)
) STRICT;
CREATE INDEX item_people_person ON item_people(person_id);

CREATE TABLE images (
    id         INTEGER PRIMARY KEY,
    item_id    INTEGER REFERENCES items(id) ON DELETE CASCADE,
    person_id  INTEGER REFERENCES people(id) ON DELETE CASCADE,
    type       TEXT    NOT NULL,
    provider   TEXT    NOT NULL DEFAULT '',
    source_url TEXT    NOT NULL,
    sha        TEXT    NOT NULL,          -- hex sha256 of source_url; artwork folder name
    lang       TEXT    NOT NULL DEFAULT '',
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0, -- 0 = primary image of its type
    status     TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ok', 'failed')),
    CHECK ((item_id IS NULL) <> (person_id IS NULL))
) STRICT;
CREATE UNIQUE INDEX images_item_src ON images(item_id, type, source_url) WHERE item_id IS NOT NULL;
CREATE UNIQUE INDEX images_person_src ON images(person_id, source_url) WHERE person_id IS NOT NULL;
CREATE INDEX images_sha ON images(sha);

CREATE TABLE metadata_cache (
    key        TEXT    PRIMARY KEY,       -- "<provider>:<request without secrets>"
    provider   TEXT    NOT NULL,
    body       BLOB    NOT NULL,
    fetched_at INTEGER NOT NULL,          -- unix seconds
    expires_at INTEGER NOT NULL
) STRICT;
CREATE INDEX metadata_cache_expires ON metadata_cache(expires_at);
```

SQLite does not check that `items` exists when a table referencing it is created, so this works
whatever 1C's migration timestamps are; the FK is enforced on insert.

- [ ] **Step 2: Write the failing tests and helpers**

`server/internal/metadata/helpers_test.go`:
```go
package metadata

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// fakeProvider is a scriptable Provider. Fetch looks up records by ids[idKey].
type fakeProvider struct {
	mu        sync.Mutex
	name      string
	kinds     []media.Kind
	idKey     string
	cands     []Candidate
	records   map[string]Record
	searchErr error
	fetchErr  error
	searches  []Query
	fetches   []map[string]string
}

var _ Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Supports(k media.Kind, _ library.Type) bool {
	for _, x := range f.kinds {
		if x == k {
			return true
		}
	}
	return false
}

func (f *fakeProvider) Search(_ context.Context, q Query) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, q)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.cands, nil
}

func (f *fakeProvider) Fetch(_ context.Context, _ media.Kind, ids map[string]string) (Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches = append(f.fetches, cloneIDs(ids))
	if f.fetchErr != nil {
		return Record{}, f.fetchErr
	}
	id := ids[f.idKey]
	if id == "" {
		return Record{}, ErrNoUsableID
	}
	r, ok := f.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

// counts returns how many Search and Fetch calls were made.
func (f *fakeProvider) counts() (searches, fetches int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.searches), len(f.fetches)
}

func intp(n int) *int { return &n }

func seedLibrary(t *testing.T, d *db.DB, lt library.Type, opts ...library.Options) library.Library {
	t.Helper()
	l := library.Library{Name: fmt.Sprintf("Test %s %d", lt, seedSeq.Add(1)), Type: lt, Paths: []string{t.TempDir()}}
	if len(opts) > 0 {
		l.Options = opts[0]
	}
	got, err := library.NewStore(d).Create(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var seedSeq atomic.Int64

// seedItem inserts it (UpsertItem), then applies the fields UpsertItem does not
// set (external IDs, match state, locks, original title) with UpdateItem.
func seedItem(t *testing.T, d *db.DB, it media.Item) media.Item {
	t.Helper()
	ctx := context.Background()
	s := media.NewStore(d)
	id, err := s.UpsertItem(ctx, it.LibraryID, fmt.Sprintf("test-%d", seedSeq.Add(1)), it)
	if err != nil {
		t.Fatal(err)
	}
	it.ID = id
	if it.MatchState == "" {
		it.MatchState = StateUnmatched
	}
	if err := s.UpdateItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetItem(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// seedFile attaches a file row (no file on disk is needed).
func seedFile(t *testing.T, d *db.DB, libraryID, itemID int64, path string) {
	t.Helper()
	if _, err := media.NewStore(d).PutFile(context.Background(), libraryID, media.File{ItemID: itemID, Path: path}); err != nil {
		t.Fatal(err)
	}
}
```

`server/internal/metadata/types_test.go`:
```go
package metadata

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func TestMigrationCreatesTables(t *testing.T) {
	d := dbtest.Open(t)
	for _, name := range []string{"item_metadata", "item_genres", "people", "item_people", "images", "metadata_cache"} {
		var n int
		if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing", name)
		}
	}
}

func TestSeedHelpers(t *testing.T) {
	d := dbtest.Open(t)
	lib := seedLibrary(t, d, LibMovies)
	it := seedItem(t, d, media.Item{LibraryID: lib.ID, Kind: KindMovie, Title: "The Matrix", Year: 1999,
		ExternalIDs: map[string]string{"tmdb": "603"}})
	got, err := media.NewStore(d).GetItem(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Matrix" || got.ExternalIDs["tmdb"] != "603" || got.MatchState != StateUnmatched {
		t.Fatalf("got %+v", got)
	}
}

func TestPublicIDs(t *testing.T) {
	got := PublicIDs(map[string]string{"tmdb": "603", LocalPathKey: "/media/x.mkv", "imdb": ""})
	if len(got) != 1 || got["tmdb"] != "603" {
		t.Fatalf("PublicIDs = %v", got)
	}
	if hasRemoteID(map[string]string{LocalPathKey: "/x"}) {
		t.Fatal("internal key counted as remote id")
	}
}

func TestURLHash(t *testing.T) {
	a, b := URLHash("https://x/a.jpg"), URLHash("https://x/b.jpg")
	if len(a) != 64 || a == b || a != URLHash("https://x/a.jpg") {
		t.Fatalf("a=%s b=%s", a, b)
	}
	if ImageField(ImagePoster) != "images.poster" {
		t.Fatal(ImageField(ImagePoster))
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL in `internal/metadata`: `undefined: Query`, `undefined: PublicIDs` (and so on).
(1C must already be merged: `library.Store.Create`, `media.Store.UpsertItem` and `PutFile` come from it.)

- [ ] **Step 4: Implement `types.go`**

`server/internal/metadata/types.go`:
```go
// Package metadata matches library items to metadata providers (TMDB,
// Fanart.tv, local NFO files, and the anime providers of plan 1E), merges
// their answers by per-field priority, stores the result and serves it.
package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// Library types and item kinds. Plan 1C fixes these string values; the names
// are local so this package does not depend on 1C's constant names.
const (
	LibMovies     library.Type = "movies"
	LibTV         library.Type = "tv"
	LibAnime      library.Type = "anime"
	LibHomeVideos library.Type = "homevideos"

	KindMovie   media.Kind = "movie"
	KindShow    media.Kind = "show"
	KindSeason  media.Kind = "season"
	KindEpisode media.Kind = "episode"
	KindVideo   media.Kind = "video"
)

// Match states stored in media.Item.MatchState.
const (
	StateUnmatched = "unmatched"
	StateMatched   = "matched"
	StateManual    = "manual"
)

// LocalProviderName is the provider that reads files next to the media.
const LocalProviderName = "nfo"

// LocalPathKey carries an item's local path to the local provider inside an
// IDs map. Keys that start with "_" are internal and never stored.
const LocalPathKey = "_path"

// Provider errors. Providers wrap these so callers can use errors.Is.
var (
	ErrNotFound      = errors.New("metadata: not found")
	ErrNoUsableID    = errors.New("metadata: no usable id for this provider")
	ErrNotConfigured = errors.New("metadata: provider not configured")
)

// Image types.
const (
	ImagePoster   = "poster"
	ImageBackdrop = "backdrop"
	ImageLogo     = "logo"
	ImageThumb    = "thumb"
	ImageBanner   = "banner"
	ImageClearArt = "clearart"
)

// ImageTypes lists every image type this package stores.
var ImageTypes = []string{ImagePoster, ImageBackdrop, ImageLogo, ImageThumb, ImageBanner, ImageClearArt}

// Person roles.
const (
	RoleActor    = "actor"
	RoleDirector = "director"
	RoleWriter   = "writer"
	RoleProducer = "producer"
	RoleCreator  = "creator"
)

// Field names: used in Item.LockedFields, merge priorities and Sources.
const (
	FieldTitle         = "title"
	FieldOriginalTitle = "original_title"
	FieldSortTitle     = "sort_title"
	FieldYear          = "year"
	FieldOverview      = "overview"
	FieldReleased      = "released"
	FieldContentRating = "content_rating"
	FieldGenres        = "genres"
	FieldStudios       = "studios"
	FieldTags          = "tags"
	FieldAltTitles     = "alt_titles"
	FieldRatings       = "ratings"
	FieldPeople        = "people"
	FieldIDs           = "ids"
	FieldSeasons       = "seasons"
	FieldImages        = "images"
)

// ImageField is the field name of one image type, e.g. "images.poster".
func ImageField(imageType string) string { return FieldImages + "." + imageType }

// Query is what a provider searches for.
type Query struct {
	Kind    media.Kind
	LibType library.Type
	Title   string
	Year    int
	IDs     map[string]string // known external IDs
}

// Candidate is one search result.
type Candidate struct {
	Provider string
	IDs      map[string]string
	Title    string
	Year     int
	Score    float64 // 0..1, provider-side confidence
}

// Record is everything one provider knows about one title.
type Record struct {
	Provider                        string
	IDs                             map[string]string
	Title, OriginalTitle, SortTitle string
	AltTitles                       map[string]string // lang/script → title, e.g. "en","x-jat"(romaji),"ja"
	Overview                        string
	Year                            int
	Released                        string // YYYY-MM-DD
	Genres, Studios, Tags           []string
	ContentRating                   string             // e.g. "PG-13", "TV-14"
	Ratings                         map[string]float64 // "tmdb","imdb","rt","anilist","mal" → 0..10
	People                          []Person
	Images                          []Image
	Seasons                         []SeasonRecord // shows only
}

// Image is a remote (or file://) artwork reference.
type Image struct {
	Type          string // poster|backdrop|logo|thumb|banner|clearart
	URL           string
	Lang          string
	Width, Height int
}

// Person is a cast or crew credit.
type Person struct {
	Name, Role, Character string
	ImageURL              string
	Order                 int
}

// SeasonRecord is one season of a show.
type SeasonRecord struct {
	Number          int
	Title, Overview string
	Episodes        []EpisodeRecord
	Images          []Image
}

// EpisodeRecord is one episode of a season.
type EpisodeRecord struct {
	Season, Number           int
	Absolute                 *int
	Title, Overview, AirDate string
	Images                   []Image
}

// Provider is a metadata source.
type Provider interface {
	Name() string
	Supports(kind media.Kind, lt library.Type) bool
	Search(ctx context.Context, q Query) ([]Candidate, error)
	Fetch(ctx context.Context, kind media.Kind, ids map[string]string) (Record, error)
}

// IDMapper lets plan 1E plug in the anime-lists cross-reference.
type IDMapper interface {
	Expand(ctx context.Context, ids map[string]string) (map[string]string, error)
}

// EpisodeMapper lets plan 1E plug in absolute↔season mapping.
type EpisodeMapper interface {
	Map(ctx context.Context, show Record, ep media.Item) (season, episode int, absolute *int, ok bool)
}

// LocalEpisodeReader is implemented by the local provider to read per-episode
// sidecar files (e.g. "<video>.nfo"). ok is false when there is none.
type LocalEpisodeReader interface {
	ReadEpisode(videoPath string) (rec EpisodeRecord, ok bool, err error)
}

// URLHash is the hex SHA-256 of an image source URL. It names the artwork folder.
func URLHash(u string) string {
	s := sha256.Sum256([]byte(u))
	return hex.EncodeToString(s[:])
}

// PublicIDs returns a copy of ids without internal ("_"-prefixed) or empty entries.
func PublicIDs(ids map[string]string) map[string]string {
	out := make(map[string]string, len(ids))
	for k, v := range ids {
		if k == "" || strings.HasPrefix(k, "_") || v == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func cloneIDs(ids map[string]string) map[string]string {
	out := make(map[string]string, len(ids))
	for k, v := range ids {
		out[k] = v
	}
	return out
}

func hasRemoteID(ids map[string]string) bool { return len(PublicIDs(ids)) > 0 }

func lockSet(fields []string) map[string]bool {
	s := make(map[string]bool, len(fields))
	for _, f := range fields {
		s[f] = true
	}
	return s
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`
Expected: `ok  github.com/Lisimflix/lisimflix/server/internal/metadata`

- [ ] **Step 6: Commit**

```bash
git switch -c feat/metadata-core   # first task only, from an up-to-date main
git add server/internal/db/migrations/20261025120000_metadata_tables.sql server/internal/metadata
git commit -m "feat(metadata): metadata tables and provider contract types"
```

---

### Task 2: Title normalisation and candidate scoring

**Files:**
- Create: `server/internal/metadata/normalize.go`, `server/internal/metadata/score.go`
- Test: `server/internal/metadata/score_test.go`
- Modify: `server/go.mod`, `server/go.sum` (via `make tidy`: adds `golang.org/x/text`)

**Interfaces:**
- Consumes: `Query`, `Candidate`, `PublicIDs` (Task 1).
- Produces: `NormalizeTitle(s) string`, `TitleSimilarity(a, b) float64`, `SortTitle(title) string`, `ScoreCandidate(q, c) float64`, `type Scored struct{ Candidate; Match float64 }`, `Rank(q, cands) []Scored`, `PickBest(q, cands, threshold) (Scored, bool)`, `DefaultMatchThreshold = 0.80`.

Scoring: `0.60 × title similarity + 0.25 × year score + 0.15 × provider score`.
Title similarity is 1 − Levenshtein / longer length on normalised titles (lower case, diacritics
removed, `&`→`and`, apostrophes dropped, other punctuation → space, leading article or trailing
", The" removed). Year score: unknown on either side 0.5, exact 1.0, ±1 year 0.8, otherwise 0.
So an exact title with the right year scores ≥ 0.85, an exact title one year off ≥ 0.80, and an
exact title three years off at most 0.75 (below the 0.80 threshold, stays unmatched).

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/score_test.go`:
```go
package metadata

import "testing"

func TestNormalizeTitle(t *testing.T) {
	cases := map[string]string{
		"The Matrix":        "matrix",
		"Matrix, The":       "matrix",
		"Amélie":            "amelie",
		"Fast & Furious":    "fast and furious",
		"Ocean's Eleven":    "oceans eleven",
		"Spider-Man: No Way": "spider man no way",
		"  WALL·E  ":        "wall e",
		"A":                 "a",
		"Pokémon: The Movie": "pokemon the movie",
	}
	for in, want := range cases {
		if got := NormalizeTitle(in); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleSimilarity(t *testing.T) {
	if s := TitleSimilarity("The Matrix", "Matrix, The"); s != 1 {
		t.Fatalf("article forms = %v", s)
	}
	if s := TitleSimilarity("The Matrix", "The Matrix Reloaded"); s > 0.5 {
		t.Fatalf("different titles too similar: %v", s)
	}
	if s := TitleSimilarity("", "x"); s != 0 {
		t.Fatalf("empty = %v", s)
	}
}

func TestSortTitle(t *testing.T) {
	for in, want := range map[string]string{"The Matrix": "Matrix", "An American Tail": "American Tail", "A": "A", "Alien": "Alien"} {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScoreCandidate(t *testing.T) {
	q := Query{Title: "The Matrix", Year: 1999}
	cases := []struct {
		name string
		c    Candidate
		min  float64
		max  float64
	}{
		{"exact", Candidate{Title: "The Matrix", Year: 1999, Score: 1}, 1, 1},
		{"year off by one", Candidate{Title: "The Matrix", Year: 2000, Score: 1}, 0.95, 0.95},
		{"year off by three", Candidate{Title: "The Matrix", Year: 2002, Score: 1}, 0.75, 0.75},
		{"unknown year", Candidate{Title: "Matrix", Year: 0, Score: 1}, 0.875, 0.875},
		{"other title", Candidate{Title: "The Matrix Reloaded", Year: 2003, Score: 1}, 0, 0.5},
	}
	for _, c := range cases {
		got := ScoreCandidate(q, c.c)
		if got < c.min || got > c.max {
			t.Errorf("%s: score %v not in [%v, %v]", c.name, got, c.min, c.max)
		}
	}
}

func TestPickBestAndRank(t *testing.T) {
	q := Query{Title: "the matrix", Year: 1999}
	cands := []Candidate{
		{Provider: "tmdb", IDs: map[string]string{"tmdb": "604"}, Title: "The Matrix Reloaded", Year: 2003, Score: 1},
		{Provider: "tmdb", IDs: map[string]string{"tmdb": "603"}, Title: "The Matrix", Year: 1999, Score: 0.95},
	}
	best, ok := PickBest(q, cands, DefaultMatchThreshold)
	if !ok || best.IDs["tmdb"] != "603" {
		t.Fatalf("best = %+v ok=%v", best, ok)
	}
	if _, ok := PickBest(Query{Title: "Completely Different", Year: 1999}, cands, DefaultMatchThreshold); ok {
		t.Fatal("unrelated query should not match")
	}
	// Same provider + IDs collapse to the best-scoring alternate title.
	r := Rank(Query{Title: "Amelie", Year: 2001}, []Candidate{
		{Provider: "tmdb", IDs: map[string]string{"tmdb": "194"}, Title: "Le Fabuleux Destin d'Amélie Poulain", Year: 2001, Score: 1},
		{Provider: "tmdb", IDs: map[string]string{"tmdb": "194"}, Title: "Amélie", Year: 2001, Score: 1},
	})
	if len(r) != 1 || r[0].Title != "Amélie" || r[0].Match != 1 {
		t.Fatalf("rank = %+v", r)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NormalizeTitle`

- [ ] **Step 3: Implement**

`server/internal/metadata/normalize.go`:
```go
package metadata

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var leadingArticles = map[string]bool{"the": true, "a": true, "an": true}
var trailingArticles = []string{", the", ", a", ", an"}

// NormalizeTitle folds a title for comparison: lower case, no diacritics,
// "&" → "and", apostrophes dropped, other punctuation → space, a leading
// article or trailing ", The" removed, whitespace collapsed.
func NormalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(foldDiacritics(s)))
	for _, suf := range trailingArticles {
		if strings.HasSuffix(s, suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		case r == '\'' || r == '’':
			// "Ocean's" → "oceans"
		default:
			b.WriteRune(' ')
		}
	}
	words := strings.Fields(b.String())
	if len(words) > 1 && leadingArticles[words[0]] {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

func foldDiacritics(s string) string {
	t := transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}

// TitleSimilarity is 1 − edit distance / longer length of the normalised titles.
func TitleSimilarity(a, b string) float64 {
	na, nb := []rune(NormalizeTitle(a)), []rune(NormalizeTitle(b))
	if len(na) == 0 || len(nb) == 0 {
		return 0
	}
	if string(na) == string(nb) {
		return 1
	}
	return 1 - float64(levenshtein(na, nb))/float64(max(len(na), len(nb)))
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// SortTitle drops a leading English article: "The Matrix" → "Matrix".
func SortTitle(title string) string {
	t := strings.TrimSpace(title)
	lower := strings.ToLower(t)
	for _, a := range []string{"the ", "an ", "a "} {
		if strings.HasPrefix(lower, a) && len(t) > len(a) {
			return strings.TrimSpace(t[len(a):])
		}
	}
	return t
}
```

`server/internal/metadata/score.go`:
```go
package metadata

import (
	"math"
	"sort"
	"strings"
)

const (
	weightTitle    = 0.60
	weightYear     = 0.25
	weightProvider = 0.15

	// DefaultMatchThreshold is the minimum combined score for an automatic match.
	DefaultMatchThreshold = 0.80
)

// Scored is a candidate with its combined match score (0..1).
type Scored struct {
	Candidate
	Match float64
}

func yearScore(want, got int) float64 {
	if want == 0 || got == 0 {
		return 0.5
	}
	switch d := want - got; {
	case d == 0:
		return 1
	case d == 1 || d == -1:
		return 0.8
	default:
		return 0
	}
}

// ScoreCandidate combines title similarity, year closeness and the provider's
// own confidence into 0..1 (rounded to 3 decimals).
func ScoreCandidate(q Query, c Candidate) float64 {
	ps := math.Max(0, math.Min(1, c.Score))
	s := weightTitle*TitleSimilarity(q.Title, c.Title) + weightYear*yearScore(q.Year, c.Year) + weightProvider*ps
	return math.Round(s*1000) / 1000
}

// Rank scores and sorts candidates best first. Candidates with the same
// provider and IDs (one per alternate title) collapse to the best one.
func Rank(q Query, cands []Candidate) []Scored {
	index := map[string]int{}
	out := []Scored{}
	for _, c := range cands {
		s := Scored{Candidate: c, Match: ScoreCandidate(q, c)}
		key := candidateKey(c)
		if i, ok := index[key]; ok {
			if s.Match > out[i].Match {
				out[i] = s
			}
			continue
		}
		index[key] = len(out)
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Match > out[j].Match })
	return out
}

func candidateKey(c Candidate) string {
	parts := []string{}
	for k, v := range PublicIDs(c.IDs) {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return c.Provider + "|" + strings.Join(parts, ",")
}

// PickBest returns the top-ranked candidate if it reaches threshold.
func PickBest(q Query, cands []Candidate, threshold float64) (Scored, bool) {
	r := Rank(q, cands)
	if len(r) == 0 || r[0].Match < threshold {
		return Scored{}, false
	}
	return r[0], true
}
```

- [ ] **Step 4: Fetch the dependency and run tests**

Run: `make tidy && make test`
Expected: `go.mod` gains `golang.org/x/text`; all `internal/metadata` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/metadata
git commit -m "feat(metadata): title normalisation and match scoring"
```

---

### Task 3: Registry and field-priority merge

**Files:**
- Create: `server/internal/metadata/registry.go`
- Test: `server/internal/metadata/registry_test.go`

**Interfaces:**
- Consumes: Task 1 types, `fakeProvider`.
- Produces: `type Priorities map[string][]string`; `DefaultPriorities() map[library.Type]Priorities`; `NewRegistry() *Registry`; `(*Registry) Register(p)` (same name replaces), `Providers() []Provider`, `Provider(name) (Provider, bool)`, `Supporting(kind, lt) []Provider`, `SetPriority(lt, field, providers...)`, `SetIDMapper(m)`, `SetEpisodeMapper(m)`, `CurrentIDMapper() IDMapper`, `CurrentEpisodeMapper() EpisodeMapper` (no-op defaults), `Merge(lt, recs []Record, preferLocal bool) Merged`; `type Merged struct{ Record; Sources map[string]string }`. Rules: see "Merge rules" at the top.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/registry_test.go`:
```go
package metadata

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func imagesOf(imgs []Image, typ string) []string {
	var out []string
	for _, im := range imgs {
		if im.Type == typ {
			out = append(out, im.URL)
		}
	}
	return out
}

func mergeFixture() []Record {
	return []Record{
		{Provider: "tmdb", IDs: map[string]string{"tmdb": "603", "imdb": "tt0133093"},
			Title: "The Matrix", Overview: "tmdb overview", Year: 1999,
			Genres: []string{"Action", "Science Fiction"}, Ratings: map[string]float64{"tmdb": 8.2},
			Images: []Image{{Type: ImagePoster, URL: "tmdb-poster"}, {Type: ImageLogo, URL: "tmdb-logo"}}},
		{Provider: "fanart", IDs: map[string]string{"tmdb": "603"}, Title: "Matrix (fanart)",
			Images: []Image{{Type: ImagePoster, URL: "fanart-poster"}, {Type: ImageLogo, URL: "fanart-logo"}, {Type: ImagePoster, URL: "tmdb-poster"}}},
		{Provider: "nfo", IDs: map[string]string{"imdb": "tt9999999"}, Overview: "my overview",
			Genres: []string{"Cyberpunk"}, Ratings: map[string]float64{"imdb": 8.7, "tmdb": 1}},
	}
}

func TestMergePreferLocal(t *testing.T) {
	m := NewRegistry().Merge(LibMovies, mergeFixture(), true)
	if m.Title != "The Matrix" || m.Sources[FieldTitle] != "tmdb" {
		t.Fatalf("title %q from %q", m.Title, m.Sources[FieldTitle])
	}
	if m.Overview != "my overview" || m.Sources[FieldOverview] != "nfo" {
		t.Fatalf("overview %q from %q", m.Overview, m.Sources[FieldOverview])
	}
	if len(m.Genres) != 1 || m.Genres[0] != "Cyberpunk" {
		t.Fatalf("genres must come whole from one provider: %v", m.Genres)
	}
	if m.IDs["imdb"] != "tt9999999" || m.IDs["tmdb"] != "603" {
		t.Fatalf("ids %v", m.IDs)
	}
	if m.Ratings["imdb"] != 8.7 || m.Ratings["tmdb"] != 1 {
		t.Fatalf("ratings %v", m.Ratings)
	}
	if m.Year != 1999 || m.Provider != "tmdb" {
		t.Fatalf("year %d provider %q", m.Year, m.Provider)
	}
}

func TestMergeLocalLast(t *testing.T) {
	m := NewRegistry().Merge(LibMovies, mergeFixture(), false)
	if m.Overview != "tmdb overview" || m.IDs["imdb"] != "tt0133093" || m.Ratings["tmdb"] != 8.2 {
		t.Fatalf("overview %q ids %v ratings %v", m.Overview, m.IDs, m.Ratings)
	}
	if m.Genres[0] != "Action" {
		t.Fatalf("genres %v", m.Genres)
	}
}

func TestMergeImagesPerType(t *testing.T) {
	m := NewRegistry().Merge(LibMovies, mergeFixture(), false)
	if got := imagesOf(m.Images, ImagePoster); len(got) != 2 || got[0] != "tmdb-poster" || got[1] != "fanart-poster" {
		t.Fatalf("posters %v", got)
	}
	if got := imagesOf(m.Images, ImageLogo); len(got) != 2 || got[0] != "fanart-logo" {
		t.Fatalf("logos %v", got)
	}
	if m.Sources[ImageField(ImageLogo)] != "fanart" {
		t.Fatalf("logo source %q", m.Sources[ImageField(ImageLogo)])
	}
}

func TestSetPriorityOverrides(t *testing.T) {
	r := NewRegistry()
	r.SetPriority(LibMovies, FieldTitle, "fanart", "tmdb")
	if m := r.Merge(LibMovies, mergeFixture(), false); m.Title != "Matrix (fanart)" {
		t.Fatalf("title %q", m.Title)
	}
}

func TestMergeSeasons(t *testing.T) {
	recs := []Record{
		{Provider: "tmdb", Seasons: []SeasonRecord{{Number: 1, Title: "Season 1",
			Episodes: []EpisodeRecord{{Season: 1, Number: 1, Title: "Pilot"}},
			Images:   []Image{{Type: ImagePoster, URL: "tmdb-s1"}}}}},
		{Provider: "fanart", Seasons: []SeasonRecord{
			{Number: 1, Images: []Image{{Type: ImagePoster, URL: "fanart-s1"}}},
			{Number: 9, Images: []Image{{Type: ImagePoster, URL: "fanart-s9"}}}}},
	}
	m := NewRegistry().Merge(LibTV, recs, true)
	if len(m.Seasons) != 1 || len(m.Seasons[0].Episodes) != 1 {
		t.Fatalf("seasons %+v", m.Seasons)
	}
	if got := imagesOf(m.Seasons[0].Images, ImagePoster); len(got) != 2 || got[1] != "fanart-s1" {
		t.Fatalf("season images %v", got)
	}
	if len(recs[0].Seasons[0].Images) != 1 {
		t.Fatal("merge mutated its input")
	}
}

func TestRegistryProviders(t *testing.T) {
	r := NewRegistry()
	a := &fakeProvider{name: "tmdb", kinds: []media.Kind{KindMovie, KindShow}}
	b := &fakeProvider{name: "fanart", kinds: []media.Kind{KindMovie}}
	r.Register(a)
	r.Register(b)
	r.Register(&fakeProvider{name: "tmdb", kinds: []media.Kind{KindShow}}) // replaces a in place
	if ps := r.Providers(); len(ps) != 2 || ps[0].Name() != "tmdb" {
		t.Fatalf("providers %v", ps)
	}
	if ps := r.Supporting(KindMovie, LibMovies); len(ps) != 1 || ps[0].Name() != "fanart" {
		t.Fatalf("supporting %v", ps)
	}
	if _, ok := r.Provider("nope"); ok {
		t.Fatal("unknown provider found")
	}
}

func TestNoopMappers(t *testing.T) {
	r := NewRegistry()
	ids, err := r.CurrentIDMapper().Expand(context.Background(), map[string]string{"tmdb": "1"})
	if err != nil || ids["tmdb"] != "1" {
		t.Fatalf("expand %v %v", ids, err)
	}
	if _, _, _, ok := r.CurrentEpisodeMapper().Map(context.Background(), Record{}, media.Item{}); ok {
		t.Fatal("no-op episode mapper must return ok=false")
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewRegistry`

- [ ] **Step 3: Implement**

`server/internal/metadata/registry.go`:
```go
package metadata

import (
	"context"
	"sync"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// Priorities maps a field name to provider names in priority order. The "*"
// entry is the default for fields that are not listed.
type Priorities map[string][]string

const anyField = "*"

// DefaultPriorities: TMDB first for text, Fanart.tv first for logos, clear
// art and banners. Plan 1E adjusts anime with SetPriority.
func DefaultPriorities() map[library.Type]Priorities {
	video := func() Priorities {
		return Priorities{
			anyField:                  {"tmdb", "fanart"},
			ImageField(ImagePoster):   {"tmdb", "fanart"},
			ImageField(ImageBackdrop): {"tmdb", "fanart"},
			ImageField(ImageThumb):    {"tmdb", "fanart"},
			ImageField(ImageLogo):     {"fanart", "tmdb"},
			ImageField(ImageClearArt): {"fanart", "tmdb"},
			ImageField(ImageBanner):   {"fanart", "tmdb"},
		}
	}
	return map[library.Type]Priorities{
		LibMovies:     video(),
		LibTV:         video(),
		LibAnime:      video(),
		LibHomeVideos: {anyField: {LocalProviderName}},
	}
}

// Registry holds providers in registration order plus merge priorities.
type Registry struct {
	mu        sync.RWMutex
	providers []Provider
	prio      map[library.Type]Priorities
	idMapper  IDMapper
	epMapper  EpisodeMapper
}

// NewRegistry returns an empty registry with DefaultPriorities.
func NewRegistry() *Registry { return &Registry{prio: DefaultPriorities()} }

// Register adds p, or replaces the provider with the same name in place.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.providers {
		if x.Name() == p.Name() {
			r.providers[i] = p
			return
		}
	}
	r.providers = append(r.providers, p)
}

// Providers returns all providers in registration order.
func (r *Registry) Providers() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Provider(nil), r.providers...)
}

// Provider returns the provider called name.
func (r *Registry) Provider(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// Supporting returns the providers that handle kind in library type lt.
func (r *Registry) Supporting(kind media.Kind, lt library.Type) []Provider {
	var out []Provider
	for _, p := range r.Providers() {
		if p.Supports(kind, lt) {
			out = append(out, p)
		}
	}
	return out
}

// SetPriority sets the provider order for one field of one library type.
func (r *Registry) SetPriority(lt library.Type, field string, providers ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prio == nil {
		r.prio = DefaultPriorities()
	}
	p := r.prio[lt]
	if p == nil {
		p = Priorities{}
		r.prio[lt] = p
	}
	p[field] = append([]string(nil), providers...)
}

// SetIDMapper installs the ID cross-reference (plan 1E).
func (r *Registry) SetIDMapper(m IDMapper) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idMapper = m
}

// SetEpisodeMapper installs the episode-number mapper (plan 1E).
func (r *Registry) SetEpisodeMapper(m EpisodeMapper) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.epMapper = m
}

// CurrentIDMapper returns the installed IDMapper or a no-op one.
func (r *Registry) CurrentIDMapper() IDMapper {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.idMapper == nil {
		return noopIDMapper{}
	}
	return r.idMapper
}

// CurrentEpisodeMapper returns the installed EpisodeMapper or a no-op one.
func (r *Registry) CurrentEpisodeMapper() EpisodeMapper {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.epMapper == nil {
		return noopEpisodeMapper{}
	}
	return r.epMapper
}

type noopIDMapper struct{}

func (noopIDMapper) Expand(_ context.Context, ids map[string]string) (map[string]string, error) {
	return cloneIDs(ids), nil
}

type noopEpisodeMapper struct{}

func (noopEpisodeMapper) Map(context.Context, Record, media.Item) (int, int, *int, bool) {
	return 0, 0, nil, false
}

// Merged is a merged record plus the provider that supplied each field.
type Merged struct {
	Record
	Sources map[string]string
}

// order returns the providers present in recs, in priority order for field.
func (r *Registry) order(lt library.Type, field string, present []string, preferLocal bool) []string {
	r.mu.RLock()
	prio := r.prio[lt]
	if r.prio == nil {
		prio = DefaultPriorities()[lt]
	}
	list, ok := prio[field]
	if !ok {
		list = prio[anyField]
	}
	r.mu.RUnlock()

	has := map[string]bool{}
	for _, n := range present {
		has[n] = true
	}
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if has[n] && !seen[n] && n != LocalProviderName {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range list {
		add(n)
	}
	for _, n := range present {
		add(n)
	}
	if has[LocalProviderName] {
		if preferLocal {
			out = append([]string{LocalProviderName}, out...)
		} else {
			out = append(out, LocalProviderName)
		}
	}
	return out
}

// Merge combines one record per provider using the documented merge rules.
// recs should be in registry order; a second record from the same provider is ignored.
func (r *Registry) Merge(lt library.Type, recs []Record, preferLocal bool) Merged {
	by := map[string]Record{}
	var present []string
	for _, rec := range recs {
		if _, dup := by[rec.Provider]; dup {
			continue
		}
		by[rec.Provider] = rec
		present = append(present, rec.Provider)
	}
	m := Merged{Sources: map[string]string{}}
	m.IDs = map[string]string{}
	m.AltTitles = map[string]string{}
	m.Ratings = map[string]float64{}
	ord := func(field string) []string { return r.order(lt, field, present, preferLocal) }

	str := func(field string, get func(Record) string, set func(string)) {
		for _, n := range ord(field) {
			if v := get(by[n]); v != "" {
				set(v)
				m.Sources[field] = n
				return
			}
		}
	}
	str(FieldTitle, func(x Record) string { return x.Title }, func(v string) { m.Title = v })
	str(FieldOriginalTitle, func(x Record) string { return x.OriginalTitle }, func(v string) { m.OriginalTitle = v })
	str(FieldSortTitle, func(x Record) string { return x.SortTitle }, func(v string) { m.SortTitle = v })
	str(FieldOverview, func(x Record) string { return x.Overview }, func(v string) { m.Overview = v })
	str(FieldReleased, func(x Record) string { return x.Released }, func(v string) { m.Released = v })
	str(FieldContentRating, func(x Record) string { return x.ContentRating }, func(v string) { m.ContentRating = v })
	for _, n := range ord(FieldYear) {
		if y := by[n].Year; y > 0 {
			m.Year = y
			m.Sources[FieldYear] = n
			break
		}
	}

	list := func(field string, get func(Record) []string, set func([]string)) {
		for _, n := range ord(field) {
			if v := get(by[n]); len(v) > 0 {
				set(append([]string(nil), v...))
				m.Sources[field] = n
				return
			}
		}
	}
	list(FieldGenres, func(x Record) []string { return x.Genres }, func(v []string) { m.Genres = v })
	list(FieldStudios, func(x Record) []string { return x.Studios }, func(v []string) { m.Studios = v })
	list(FieldTags, func(x Record) []string { return x.Tags }, func(v []string) { m.Tags = v })
	for _, n := range ord(FieldPeople) {
		if p := by[n].People; len(p) > 0 {
			m.People = append([]Person(nil), p...)
			m.Sources[FieldPeople] = n
			break
		}
	}

	for _, n := range ord(FieldIDs) {
		for k, v := range PublicIDs(by[n].IDs) {
			if _, ok := m.IDs[k]; !ok {
				m.IDs[k] = v
				if _, ok := m.Sources[FieldIDs]; !ok {
					m.Sources[FieldIDs] = n
				}
			}
		}
	}
	for _, n := range ord(FieldRatings) {
		for k, v := range by[n].Ratings {
			if _, ok := m.Ratings[k]; !ok {
				m.Ratings[k] = v
				if _, ok := m.Sources[FieldRatings]; !ok {
					m.Sources[FieldRatings] = n
				}
			}
		}
	}
	for _, n := range ord(FieldAltTitles) {
		for k, v := range by[n].AltTitles {
			if _, ok := m.AltTitles[k]; !ok && v != "" {
				m.AltTitles[k] = v
				if _, ok := m.Sources[FieldAltTitles]; !ok {
					m.Sources[FieldAltTitles] = n
				}
			}
		}
	}

	for _, t := range imageTypesIn(recs) {
		field := ImageField(t)
		seen := map[string]bool{}
		for _, n := range ord(field) {
			for _, img := range by[n].Images {
				if img.Type != t || img.URL == "" || seen[img.URL] {
					continue
				}
				seen[img.URL] = true
				m.Images = append(m.Images, img)
				if _, ok := m.Sources[field]; !ok {
					m.Sources[field] = n
				}
			}
		}
	}

	m.Seasons = mergeSeasons(by, ord(FieldSeasons), m.Sources)
	m.Provider = m.Sources[FieldTitle]
	return m
}

// imageTypesIn returns ImageTypes plus any other type found in recs.
func imageTypesIn(recs []Record) []string {
	out := append([]string(nil), ImageTypes...)
	known := map[string]bool{}
	for _, t := range out {
		known[t] = true
	}
	for _, rec := range recs {
		for _, img := range rec.Images {
			if img.Type != "" && !known[img.Type] {
				known[img.Type] = true
				out = append(out, img.Type)
			}
		}
	}
	return out
}

func mergeSeasons(by map[string]Record, order []string, sources map[string]string) []SeasonRecord {
	var base []SeasonRecord
	baseSrc := ""
	for _, n := range order {
		if s := by[n].Seasons; len(s) > 0 {
			base = make([]SeasonRecord, len(s))
			for i, sr := range s {
				sr.Images = append([]Image(nil), sr.Images...)
				sr.Episodes = append([]EpisodeRecord(nil), sr.Episodes...)
				base[i] = sr
			}
			baseSrc = n
			sources[FieldSeasons] = n
			break
		}
	}
	if base == nil {
		return nil
	}
	idx := map[int]int{}
	for i, s := range base {
		idx[s.Number] = i
	}
	for _, n := range order {
		if n == baseSrc {
			continue
		}
		for _, s := range by[n].Seasons {
			i, ok := idx[s.Number]
			if !ok {
				continue
			}
			have := map[string]bool{}
			for _, img := range base[i].Images {
				have[img.URL] = true
			}
			for _, img := range s.Images {
				if img.URL != "" && !have[img.URL] {
					have[img.URL] = true
					base[i].Images = append(base[i].Images, img)
				}
			}
		}
	}
	return base
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): provider registry with per-field priority merge"
```

---

### Task 4: Fetcher (rate limit, retry with backoff, persisted response cache)

**Files:**
- Create: `server/internal/metadata/fetcher.go`
- Test: `server/internal/metadata/fetcher_test.go`
- Modify: `server/go.mod`, `server/go.sum` (adds `golang.org/x/time`)

**Interfaces:**
- Consumes: `db.DB`, `version.Version`, `ErrNotFound`, table `metadata_cache`.
- Produces: `type FetcherOptions struct{ HTTP *http.Client; RatePerSec float64; Burst int; TTL time.Duration; MaxRetries int; Now func() time.Time; Sleep func(context.Context, time.Duration) error }` (defaults: 20s client, 4/s, burst 4, TTL 7 days, MaxRetries 0 → 3, negative → none); `NewFetcher(d *db.DB, provider string, o FetcherOptions) *Fetcher` (`d` may be nil: no cache); `(*Fetcher) Get(ctx, rawURL, cacheKey string, h http.Header) ([]byte, error)`; `(*Fetcher) GetJSON(ctx, rawURL, cacheKey string, h http.Header, v any) error`; `WithNoCache(ctx) context.Context`; `type StatusError struct{ Provider, Key string; Code int }`.
  `cacheKey` is the request **without secrets** (e.g. `/movie/603?language=en-AU`); `""` disables caching. The DB key is `"<provider>:<cacheKey>"`. 404 → error wrapping `ErrNotFound` (not retried). 429 and 5xx and network errors are retried with backoff 0.5s·2ⁿ (max 30s) or `Retry-After` if longer. Errors never contain the request URL.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/fetcher_test.go`:
```go
package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
)

type sleepRecorder struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d = append(s.d, d)
	return nil
}

func TestFetcherCachesByKey(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"n":1}`))
	}))
	defer srv.Close()
	d := dbtest.Open(t)
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	f := NewFetcher(d, "test", FetcherOptions{RatePerSec: 1000, Burst: 1000, TTL: time.Hour,
		Now: func() time.Time { return now }})
	ctx := context.Background()
	var v struct {
		N int `json:"n"`
	}
	for i := 0; i < 2; i++ {
		if err := f.GetJSON(ctx, srv.URL+"/x?api_key=SECRET", "/x", nil, &v); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 || v.N != 1 {
		t.Fatalf("hits=%d v=%d", hits.Load(), v.N)
	}
	var key string
	if err := d.QueryRow(`SELECT key FROM metadata_cache`).Scan(&key); err != nil || key != "test:/x" {
		t.Fatalf("cache key %q err %v", key, err)
	}
	if err := f.GetJSON(WithNoCache(ctx), srv.URL+"/x", "/x", nil, &v); err != nil || hits.Load() != 2 {
		t.Fatalf("no-cache: hits=%d err=%v", hits.Load(), err)
	}
	now = now.Add(2 * time.Hour)
	if err := f.GetJSON(ctx, srv.URL+"/x", "/x", nil, &v); err != nil || hits.Load() != 3 {
		t.Fatalf("expired: hits=%d err=%v", hits.Load(), err)
	}
	if _, err := f.Get(ctx, srv.URL+"/y", "", nil); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM metadata_cache`).Scan(&n)
	if n != 1 {
		t.Fatalf("empty cacheKey must not be cached; rows=%d", n)
	}
}

func TestFetcherNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	f := NewFetcher(nil, "test", FetcherOptions{})
	_, err := f.Get(context.Background(), srv.URL+"/x", "/x", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetcherRetriesThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	s := &sleepRecorder{}
	f := NewFetcher(nil, "test", FetcherOptions{Sleep: s.sleep})
	if _, err := f.Get(context.Background(), srv.URL, "/x", nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || len(s.d) != 1 || s.d[0] != 2*time.Second {
		t.Fatalf("hits=%d sleeps=%v", hits.Load(), s.d)
	}
}

func TestFetcherGivesUp(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	s := &sleepRecorder{}
	f := NewFetcher(nil, "test", FetcherOptions{MaxRetries: 2, Sleep: s.sleep})
	_, err := f.Get(context.Background(), srv.URL, "/x", nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 502 {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 3 || len(s.d) != 2 || s.d[0] != 500*time.Millisecond || s.d[1] != time.Second {
		t.Fatalf("hits=%d sleeps=%v", hits.Load(), s.d)
	}
}

func TestFetcherNoRetryOnClientError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	f := NewFetcher(nil, "test", FetcherOptions{Sleep: (&sleepRecorder{}).sleep})
	if _, err := f.Get(context.Background(), srv.URL, "/x", nil); err == nil || hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
}

func TestFetcherErrorHidesSecret(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused from now on
	f := NewFetcher(nil, "test", FetcherOptions{MaxRetries: -1})
	_, err := f.Get(context.Background(), url+"/x?api_key=SECRET", "/x", nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewFetcher`

- [ ] **Step 3: Implement**

`server/internal/metadata/fetcher.go`:
```go
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/version"
)

const maxResponseBytes = 16 << 20

// FetcherOptions tunes a Fetcher. Zero values get defaults (see NewFetcher).
type FetcherOptions struct {
	HTTP       *http.Client
	RatePerSec float64
	Burst      int
	TTL        time.Duration
	MaxRetries int // 0 = default 3, negative = no retries
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
}

// Fetcher performs rate-limited, retried, cached GETs for one provider.
type Fetcher struct {
	db       *db.DB
	provider string
	o        FetcherOptions
	lim      *rate.Limiter
}

// NewFetcher returns a Fetcher. d may be nil (no response cache).
func NewFetcher(d *db.DB, provider string, o FetcherOptions) *Fetcher {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if o.RatePerSec <= 0 {
		o.RatePerSec = 4
	}
	if o.Burst <= 0 {
		o.Burst = 4
	}
	if o.TTL <= 0 {
		o.TTL = 7 * 24 * time.Hour
	}
	if o.MaxRetries == 0 {
		o.MaxRetries = 3
	} else if o.MaxRetries < 0 {
		o.MaxRetries = 0
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return &Fetcher{db: d, provider: provider, o: o, lim: rate.NewLimiter(rate.Limit(o.RatePerSec), o.Burst)}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type noCacheKey struct{}

// WithNoCache makes Fetchers skip cached responses (they still store new ones).
func WithNoCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, noCacheKey{}, true)
}

func noCache(ctx context.Context) bool {
	v, _ := ctx.Value(noCacheKey{}).(bool)
	return v
}

// StatusError is an unexpected HTTP status from a provider.
type StatusError struct {
	Provider string
	Key      string
	Code     int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.Provider, e.Key, e.Code)
}

// GetJSON is Get followed by json.Unmarshal into v.
func (f *Fetcher) GetJSON(ctx context.Context, rawURL, cacheKey string, h http.Header, v any) error {
	b, err := f.Get(ctx, rawURL, cacheKey, h)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s %s: decode: %w", f.provider, cacheKey, err)
	}
	return nil
}

// Get returns the body of a 200 response, from cache when fresh.
func (f *Fetcher) Get(ctx context.Context, rawURL, cacheKey string, h http.Header) ([]byte, error) {
	label := cacheKey
	if label == "" {
		label = "request"
	}
	key := f.provider + ":" + cacheKey
	useCache := cacheKey != "" && f.db != nil
	if useCache && !noCache(ctx) {
		if b, ok := f.readCache(ctx, key); ok {
			return b, nil
		}
	}
	b, err := f.fetch(ctx, rawURL, label, h)
	if err != nil {
		return nil, err
	}
	if useCache {
		f.writeCache(ctx, key, b)
	}
	return b, nil
}

func (f *Fetcher) readCache(ctx context.Context, key string) ([]byte, bool) {
	var body []byte
	var exp int64
	err := f.db.QueryRowContext(ctx, `SELECT body, expires_at FROM metadata_cache WHERE key = ?`, key).Scan(&body, &exp)
	if err != nil || exp <= f.o.Now().Unix() {
		return nil, false
	}
	return body, true
}

// writeCache is best effort: a failed cache write only costs a later refetch.
func (f *Fetcher) writeCache(ctx context.Context, key string, body []byte) {
	now := f.o.Now()
	_, _ = f.db.ExecContext(ctx, `INSERT INTO metadata_cache(key, provider, body, fetched_at, expires_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET body = excluded.body, fetched_at = excluded.fetched_at,
		  expires_at = excluded.expires_at`,
		key, f.provider, body, now.Unix(), now.Add(f.o.TTL).Unix())
}

func (f *Fetcher) fetch(ctx context.Context, rawURL, label string, h http.Header) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if err := f.lim.Wait(ctx); err != nil {
			return nil, err
		}
		body, retryAfter, err := f.once(ctx, rawURL, label, h)
		if err == nil {
			return body, nil
		}
		var se *StatusError
		retryable := !errors.Is(err, ErrNotFound) && (!errors.As(err, &se) || se.Code == http.StatusTooManyRequests || se.Code >= 500)
		if !retryable || ctx.Err() != nil || attempt >= f.o.MaxRetries {
			return nil, err
		}
		wait := time.Duration(500<<attempt) * time.Millisecond
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		if retryAfter > wait {
			wait = retryAfter
		}
		if err := f.o.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (f *Fetcher) once(ctx context.Context, rawURL, label string, h http.Header) ([]byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: build request: %w", f.provider, label, redact(err))
	}
	for k, vs := range h {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Lisimflix/"+version.Version)
	resp, err := f.o.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", f.provider, label, redact(err))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		if err != nil {
			return nil, 0, fmt.Errorf("%s %s: read: %w", f.provider, label, redact(err))
		}
		if len(b) > maxResponseBytes {
			return nil, 0, fmt.Errorf("%s %s: response too large", f.provider, label)
		}
		return b, 0, nil
	case http.StatusNotFound:
		return nil, 0, fmt.Errorf("%s %s: %w", f.provider, label, ErrNotFound)
	default:
		return nil, parseRetryAfter(resp.Header.Get("Retry-After")), &StatusError{Provider: f.provider, Key: label, Code: resp.StatusCode}
	}
}

// redact strips the URL (which may carry an api_key) from net/http errors.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	var d time.Duration
	if s, err := strconv.Atoi(v); err == nil {
		d = time.Duration(s) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	}
	if d < 0 {
		return 0
	}
	if d > 5*time.Minute {
		return 5 * time.Minute
	}
	return d
}
```

- [ ] **Step 4: Fetch the dependency and run tests**

Run: `make tidy && make test`
Expected: `go.mod` gains `golang.org/x/time`; all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/metadata
git commit -m "feat(metadata): rate-limited, retrying, cached provider fetcher"
```

---

### Task 5: Settings (env + server_settings, masking, validation)

**Files:**
- Create: `server/internal/metadata/settings.go`
- Test: `server/internal/metadata/settings_test.go`

**Interfaces:**
- Consumes: `db.DB.GetSetting/SetSetting`, `DefaultMatchThreshold`.
- Produces: keys `KeyTMDBAPIKey = "metadata.tmdb.api_key"`, `KeyFanartAPIKey = "metadata.fanart.api_key"`, `KeyLanguage`, `KeyFallbackLanguage`, `KeyCertCountries`, `KeyMatchThreshold`; env names `EnvTMDBAPIKey = "LISIMFLIX_TMDB_API_KEY"`, `EnvFanartAPIKey = "LISIMFLIX_FANART_API_KEY"`; `type Settings struct{ TMDBKey, FanartKey, Language, FallbackLanguage string; CertCountries []string; MatchThreshold float64 }`; `DefaultSettings()` (en-AU, en-US, [AU US], 0.80); `NewSettingsStore(d, getenv) *SettingsStore`; `(*SettingsStore) Load(ctx) (Settings, error)`; `SecretStatuses(ctx) (tmdb, fanart SecretStatus, err error)`; `PreferLocal(ctx, libraryID) (bool, error)` (default true); `Update(ctx, SettingsUpdate) error`; `type SettingsUpdate struct{ TMDBKey, FanartKey, Language, FallbackLanguage *string; CertCountries []string; MatchThreshold *float64; PreferLocal map[int64]bool }`; `type SecretStatus struct{ Set bool; Masked, Source string }` (JSON `set`, `masked`, `source`); `Mask(s) string`; `type ValidationError struct{ Field, Message string }`.
  Precedence for keys: a non-empty `server_settings` value wins, else the env var. Setting a key to `""` clears it (env applies again). Per-library "prefer local NFO" key: `metadata.library.<id>.prefer_local`.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/settings_test.go`:
```go
package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
)

func strp(s string) *string { return &s }

func TestSettingsDefaultsAndEnv(t *testing.T) {
	env := map[string]string{EnvTMDBAPIKey: "env-tmdb-key-123456"}
	s := NewSettingsStore(dbtest.Open(t), func(k string) string { return env[k] })
	st, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.TMDBKey != "env-tmdb-key-123456" || st.FanartKey != "" || st.Language != "en-AU" ||
		st.FallbackLanguage != "en-US" || len(st.CertCountries) != 2 || st.CertCountries[0] != "AU" ||
		st.MatchThreshold != DefaultMatchThreshold {
		t.Fatalf("%+v", st)
	}
	tm, fa, err := s.SecretStatuses(context.Background())
	if err != nil || tm.Source != "env" || !tm.Set || tm.Masked != "****3456" || fa.Set || fa.Source != "" {
		t.Fatalf("tmdb %+v fanart %+v err %v", tm, fa, err)
	}
}

func TestSettingsUpdateWinsOverEnvAndClears(t *testing.T) {
	ctx := context.Background()
	s := NewSettingsStore(dbtest.Open(t), func(k string) string {
		if k == EnvTMDBAPIKey {
			return "from-env"
		}
		return ""
	})
	th := 0.9
	err := s.Update(ctx, SettingsUpdate{TMDBKey: strp("from-db"), Language: strp("de-DE"),
		CertCountries: []string{"DE", "US"}, MatchThreshold: &th, PreferLocal: map[int64]bool{7: false}})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s.Load(ctx)
	if st.TMDBKey != "from-db" || st.Language != "de-DE" || st.CertCountries[0] != "DE" || st.MatchThreshold != 0.9 {
		t.Fatalf("%+v", st)
	}
	if p, _ := s.PreferLocal(ctx, 7); p {
		t.Fatal("prefer_local not saved")
	}
	if p, _ := s.PreferLocal(ctx, 8); !p {
		t.Fatal("prefer_local must default to true")
	}
	if err := s.Update(ctx, SettingsUpdate{TMDBKey: strp("")}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Load(ctx); st.TMDBKey != "from-env" {
		t.Fatalf("clearing should fall back to env, got %q", st.TMDBKey)
	}
}

func TestSettingsValidation(t *testing.T) {
	s := NewSettingsStore(dbtest.Open(t), nil)
	bad := 0.2
	for name, u := range map[string]SettingsUpdate{
		"language":  {Language: strp("english")},
		"countries": {CertCountries: []string{"aus"}},
		"empty":     {CertCountries: []string{}},
		"threshold": {MatchThreshold: &bad},
	} {
		var ve *ValidationError
		if err := s.Update(context.Background(), u); !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMask(t *testing.T) {
	if Mask("") != "" || Mask("short") != "****" || Mask("abcdefghijkl1234") != "****1234" {
		t.Fatal(Mask("abcdefghijkl1234"))
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewSettingsStore`

- [ ] **Step 3: Implement**

`server/internal/metadata/settings.go`:
```go
package metadata

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/db"
)

// server_settings keys and environment variables.
const (
	KeyTMDBAPIKey       = "metadata.tmdb.api_key"
	KeyFanartAPIKey     = "metadata.fanart.api_key"
	KeyLanguage         = "metadata.language"
	KeyFallbackLanguage = "metadata.fallback_language"
	KeyCertCountries    = "metadata.certification_countries"
	KeyMatchThreshold   = "metadata.match_threshold"

	EnvTMDBAPIKey   = "LISIMFLIX_TMDB_API_KEY"
	EnvFanartAPIKey = "LISIMFLIX_FANART_API_KEY"
)

func keyPreferLocal(libraryID int64) string {
	return fmt.Sprintf("metadata.library.%d.prefer_local", libraryID)
}

// Settings are the effective metadata settings.
type Settings struct {
	TMDBKey          string // v3 API key or v4 read access token
	FanartKey        string
	Language         string   // e.g. "en-AU"
	FallbackLanguage string   // used when Language has no translation, e.g. "en-US"
	CertCountries    []string // content rating countries in preference order
	MatchThreshold   float64
}

// DefaultSettings: Australian English first, US English fallback, AU then US ratings.
func DefaultSettings() Settings {
	return Settings{Language: "en-AU", FallbackLanguage: "en-US", CertCountries: []string{"AU", "US"},
		MatchThreshold: DefaultMatchThreshold}
}

// SettingsStore reads settings from server_settings with env fallback for keys.
type SettingsStore struct {
	db     *db.DB
	getenv func(string) string
}

// NewSettingsStore returns a store. getenv may be nil (no env).
func NewSettingsStore(d *db.DB, getenv func(string) string) *SettingsStore {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return &SettingsStore{db: d, getenv: getenv}
}

func (s *SettingsStore) get(ctx context.Context, key string) (string, error) {
	v, _, err := s.db.GetSetting(ctx, key)
	return strings.TrimSpace(v), err
}

func (s *SettingsStore) secret(ctx context.Context, key, env string) (value, source string, err error) {
	v, err := s.get(ctx, key)
	if err != nil {
		return "", "", err
	}
	if v != "" {
		return v, "settings", nil
	}
	if e := strings.TrimSpace(s.getenv(env)); e != "" {
		return e, "env", nil
	}
	return "", "", nil
}

// Load returns the effective settings.
func (s *SettingsStore) Load(ctx context.Context) (Settings, error) {
	st := DefaultSettings()
	var err error
	if st.TMDBKey, _, err = s.secret(ctx, KeyTMDBAPIKey, EnvTMDBAPIKey); err != nil {
		return Settings{}, err
	}
	if st.FanartKey, _, err = s.secret(ctx, KeyFanartAPIKey, EnvFanartAPIKey); err != nil {
		return Settings{}, err
	}
	vals := map[string]string{}
	for _, k := range []string{KeyLanguage, KeyFallbackLanguage, KeyCertCountries, KeyMatchThreshold} {
		if vals[k], err = s.get(ctx, k); err != nil {
			return Settings{}, err
		}
	}
	if v := vals[KeyLanguage]; v != "" {
		st.Language = v
	}
	if v := vals[KeyFallbackLanguage]; v != "" {
		st.FallbackLanguage = v
	}
	if v := vals[KeyCertCountries]; v != "" {
		st.CertCountries = strings.Split(v, ",")
	}
	if v := vals[KeyMatchThreshold]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			st.MatchThreshold = f
		}
	}
	return st, nil
}

// SecretStatus describes a secret without revealing it.
type SecretStatus struct {
	Set    bool   `json:"set"`
	Masked string `json:"masked"`
	Source string `json:"source"` // "settings", "env" or ""
}

// SecretStatuses reports the TMDB and Fanart.tv keys, masked.
func (s *SettingsStore) SecretStatuses(ctx context.Context) (tmdb, fanart SecretStatus, err error) {
	v, src, err := s.secret(ctx, KeyTMDBAPIKey, EnvTMDBAPIKey)
	if err != nil {
		return
	}
	tmdb = SecretStatus{Set: v != "", Masked: Mask(v), Source: src}
	v, src, err = s.secret(ctx, KeyFanartAPIKey, EnvFanartAPIKey)
	if err != nil {
		return
	}
	fanart = SecretStatus{Set: v != "", Masked: Mask(v), Source: src}
	return
}

// Mask hides a secret, keeping the last 4 characters of long ones.
func Mask(v string) string {
	switch {
	case v == "":
		return ""
	case len(v) < 12:
		return "****"
	default:
		return "****" + v[len(v)-4:]
	}
}

// PreferLocal reports whether local NFO data wins for a library (default true).
func (s *SettingsStore) PreferLocal(ctx context.Context, libraryID int64) (bool, error) {
	v, err := s.get(ctx, keyPreferLocal(libraryID))
	if err != nil || v == "" {
		return true, err
	}
	return v == "true", nil
}

// SettingsUpdate changes only the non-nil fields.
type SettingsUpdate struct {
	TMDBKey, FanartKey, Language, FallbackLanguage *string
	CertCountries                                  []string // nil = unchanged
	MatchThreshold                                 *float64
	PreferLocal                                    map[int64]bool
}

// ValidationError is bad settings input.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

var (
	langRe    = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Update validates every field first, then writes them.
func (s *SettingsStore) Update(ctx context.Context, u SettingsUpdate) error {
	if u.Language != nil && !langRe.MatchString(*u.Language) {
		return &ValidationError{"language", "must look like en or en-AU"}
	}
	if u.FallbackLanguage != nil && *u.FallbackLanguage != "" && !langRe.MatchString(*u.FallbackLanguage) {
		return &ValidationError{"fallback_language", "must look like en or en-US, or be empty"}
	}
	if u.CertCountries != nil {
		if len(u.CertCountries) == 0 {
			return &ValidationError{"certification_countries", "needs at least one country"}
		}
		for _, c := range u.CertCountries {
			if !countryRe.MatchString(c) {
				return &ValidationError{"certification_countries", "use ISO 3166-1 codes like AU"}
			}
		}
	}
	if u.MatchThreshold != nil && (*u.MatchThreshold < 0.5 || *u.MatchThreshold > 1) {
		return &ValidationError{"match_threshold", "must be between 0.5 and 1"}
	}
	writes := map[string]string{}
	if u.TMDBKey != nil {
		writes[KeyTMDBAPIKey] = strings.TrimSpace(*u.TMDBKey)
	}
	if u.FanartKey != nil {
		writes[KeyFanartAPIKey] = strings.TrimSpace(*u.FanartKey)
	}
	if u.Language != nil {
		writes[KeyLanguage] = *u.Language
	}
	if u.FallbackLanguage != nil {
		writes[KeyFallbackLanguage] = *u.FallbackLanguage
	}
	if u.CertCountries != nil {
		writes[KeyCertCountries] = strings.Join(u.CertCountries, ",")
	}
	if u.MatchThreshold != nil {
		writes[KeyMatchThreshold] = strconv.FormatFloat(*u.MatchThreshold, 'f', -1, 64)
	}
	for id, v := range u.PreferLocal {
		writes[keyPreferLocal(id)] = strconv.FormatBool(v)
	}
	for k, v := range writes {
		if err := s.db.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}
```

Note: the fallback language setting `""` means "no fallback" only when explicitly stored empty is
not distinguishable from unset; storing `""` restores the default `en-US`. To disable the
fallback, set it equal to `language`.

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): provider settings from env and server settings"
```

---

### Task 6: Metadata store (details, genres, people, images, cache pruning)

**Files:**
- Create: `server/internal/metadata/store.go`
- Test: `server/internal/metadata/store_test.go`

**Interfaces:**
- Consumes: `db.DB`, `Merged`, `Person`, `Image`, field constants, `URLHash`, seed helpers.
- Produces:
  - `type Details struct{ Overview, Released, ContentRating string; Genres, Studios, Tags []string; AltTitles map[string]string; Ratings map[string]float64; People []Person; Images []Image; Sources map[string]string }`
  - `DetailsFromMerged(m Merged) Details`; `DetailFields []string`; `AllDetailFields() []string` (DetailFields + one `images.<type>` per `ImageTypes`)
  - `NewStore(d) *Store`; `(*Store) Save(ctx, itemID, d Details, locked []string) error` (all detail fields except locked); `SaveFields(ctx, itemID, d, fields []string) error` (only `fields`, replacing each one wholesale); `Load(ctx, itemID) (View, bool, error)`; `Image(ctx, id) (ImageRow, error)` (missing → wraps `ErrNotFound`); `SetImageStatus(ctx, id, status) error`; `PrimaryImages(ctx, itemID) ([]ImageRow, error)`; `PruneCache(ctx) (int64, error)`
  - `type View struct{ Overview, Released, ContentRating string; Genres, Studios, Tags []string; AltTitles map[string]string; Ratings map[string]float64; People []PersonRef; Images []ImageRef; Sources map[string]string; RefreshedAt string }`
  - `type PersonRef struct{ ID int64; Name, Role, Character string; ImageID int64 }`, `type ImageRef struct{ ID int64; Type, Lang string; Width, Height int }`
  - `type ImageRow struct{ ID, ItemID, PersonID int64; Type, Provider, SourceURL, SHA, Status string; Width, Height, SortOrder int }`
  - unexported `unlockedFields(fields, locked []string) []string` (a lock on `images` hides every `images.*` field).
  Image rows keep their ID while their source URL stays in the set, so `/api/v1/images/{id}` URLs are stable; URLs that disappear are deleted. Max 20 images per type.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/store_test.go`:
```go
package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func seedMovie(t *testing.T, d *db.DB, title string) media.Item {
	t.Helper()
	lib := seedLibrary(t, d, LibMovies)
	return seedItem(t, d, media.Item{LibraryID: lib.ID, Kind: KindMovie, Title: title})
}

func matrixDetails() Details {
	return Details{
		Overview: "A hacker learns the truth.", Released: "1999-03-31", ContentRating: "M",
		Genres: []string{"Action", "Science Fiction", "action"}, Studios: []string{"Warner Bros. Pictures"},
		Tags: []string{"dystopia"}, AltTitles: map[string]string{"de": "Matrix"},
		Ratings: map[string]float64{"tmdb": 8.2},
		People: []Person{
			{Name: "Keanu Reeves", Role: RoleActor, Character: "Neo", ImageURL: "https://img/keanu.jpg"},
			{Name: "Lana Wachowski", Role: RoleDirector},
		},
		Images: []Image{
			{Type: ImagePoster, URL: "https://img/p1.jpg", Lang: "en", Width: 1000, Height: 1500},
			{Type: ImagePoster, URL: "https://img/p2.jpg"},
			{Type: ImageBackdrop, URL: "https://img/b1.jpg"},
		},
		Sources: map[string]string{FieldOverview: "tmdb", ImageField(ImagePoster): "tmdb"},
	}
}

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	d := dbtest.Open(t)
	it := seedMovie(t, d, "The Matrix")
	s := NewStore(d)
	ctx := context.Background()
	if _, ok, err := s.Load(ctx, it.ID); ok || err != nil {
		t.Fatalf("empty load ok=%v err=%v", ok, err)
	}
	if err := s.Save(ctx, it.ID, matrixDetails(), nil); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Load(ctx, it.ID)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if v.Overview != "A hacker learns the truth." || v.ContentRating != "M" || v.Released != "1999-03-31" {
		t.Fatalf("%+v", v)
	}
	if len(v.Genres) != 2 || v.Genres[1] != "Science Fiction" {
		t.Fatalf("genres %v (duplicates must be dropped, order kept)", v.Genres)
	}
	if v.Studios[0] != "Warner Bros. Pictures" || v.Tags[0] != "dystopia" || v.AltTitles["de"] != "Matrix" || v.Ratings["tmdb"] != 8.2 {
		t.Fatalf("%+v", v)
	}
	if len(v.People) != 2 || v.People[0].Character != "Neo" || v.People[0].ImageID == 0 || v.People[1].ImageID != 0 {
		t.Fatalf("people %+v", v.People)
	}
	if len(v.Images) != 3 || v.Sources[FieldOverview] != "tmdb" {
		t.Fatalf("images %+v sources %v", v.Images, v.Sources)
	}
	prim, err := s.PrimaryImages(ctx, it.ID)
	if err != nil || len(prim) != 2 {
		t.Fatalf("primary %+v err %v", prim, err)
	}
}

func TestStoreRespectsLocks(t *testing.T) {
	d := dbtest.Open(t)
	it := seedMovie(t, d, "The Matrix")
	s := NewStore(d)
	ctx := context.Background()
	if err := s.Save(ctx, it.ID, matrixDetails(), nil); err != nil {
		t.Fatal(err)
	}
	next := Details{Overview: "new", Genres: []string{"Drama"}, Images: []Image{{Type: ImagePoster, URL: "https://img/p9.jpg"}}}
	if err := s.Save(ctx, it.ID, next, []string{FieldOverview, FieldGenres, FieldImages}); err != nil {
		t.Fatal(err)
	}
	v, _, _ := s.Load(ctx, it.ID)
	if v.Overview != "A hacker learns the truth." || v.Genres[0] != "Action" || len(v.Images) != 3 {
		t.Fatalf("locked fields changed: %+v", v)
	}
	if len(v.People) != 0 {
		t.Fatalf("unlocked people should be replaced by the empty list: %+v", v.People)
	}
}

func TestStoreImageIDsStable(t *testing.T) {
	d := dbtest.Open(t)
	it := seedMovie(t, d, "The Matrix")
	s := NewStore(d)
	ctx := context.Background()
	save := func(urls ...string) map[string]int64 {
		var imgs []Image
		for _, u := range urls {
			imgs = append(imgs, Image{Type: ImagePoster, URL: u})
		}
		if err := s.SaveFields(ctx, it.ID, Details{Images: imgs}, []string{ImageField(ImagePoster)}); err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		rows, _ := d.Query(`SELECT source_url, id FROM images WHERE item_id = ?`, it.ID)
		defer rows.Close()
		for rows.Next() {
			var u string
			var id int64
			rows.Scan(&u, &id)
			out[u] = id
		}
		return out
	}
	first := save("a", "b")
	second := save("b", "c")
	if second["b"] != first["b"] || len(second) != 2 || second["a"] != 0 {
		t.Fatalf("first %v second %v", first, second)
	}
	row, err := s.Image(ctx, second["b"])
	if err != nil || row.SortOrder != 0 || row.SHA != URLHash("b") || row.Status != "pending" || row.ItemID != it.ID {
		t.Fatalf("row %+v err %v", row, err)
	}
	if err := s.SetImageStatus(ctx, row.ID, "ok"); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Image(ctx, row.ID); row.Status != "ok" {
		t.Fatalf("status %q", row.Status)
	}
	if _, err := s.Image(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing image err = %v", err)
	}
}

func TestStorePeopleSharedAcrossItems(t *testing.T) {
	d := dbtest.Open(t)
	a := seedMovie(t, d, "The Matrix")
	b := seedMovie(t, d, "John Wick")
	s := NewStore(d)
	ctx := context.Background()
	keanu := Details{People: []Person{{Name: "Keanu Reeves", Role: RoleActor, ImageURL: "https://img/keanu.jpg"}}}
	for _, id := range []int64{a.ID, b.ID} {
		if err := s.SaveFields(ctx, id, keanu, []string{FieldPeople}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM people`).Scan(&n)
	if n != 1 {
		t.Fatalf("people rows = %d", n)
	}
	if err := s.SaveFields(ctx, a.ID, Details{}, []string{"bogus"}); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestStorePruneCache(t *testing.T) {
	d := dbtest.Open(t)
	s := NewStore(d)
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	d.Exec(`INSERT INTO metadata_cache VALUES ('a', 'p', x'00', 0, ?), ('b', 'p', x'00', 0, ?)`,
		now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix())
	n, err := s.PruneCache(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("pruned %d err %v", n, err)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewStore`

- [ ] **Step 3: Implement**

`server/internal/metadata/store.go`:
```go
package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db"
)

const maxImagesPerType = 20

// Details is the metadata this package stores beyond 1C's item row.
type Details struct {
	Overview, Released, ContentRating string
	Genres, Studios, Tags             []string
	AltTitles                         map[string]string
	Ratings                           map[string]float64
	People                            []Person
	Images                            []Image
	Sources                           map[string]string // field -> provider ("manual" for edits)
}

// DetailsFromMerged copies the detail fields of a merged record.
func DetailsFromMerged(m Merged) Details {
	return Details{Overview: m.Overview, Released: m.Released, ContentRating: m.ContentRating,
		Genres: m.Genres, Studios: m.Studios, Tags: m.Tags, AltTitles: m.AltTitles, Ratings: m.Ratings,
		People: m.People, Images: m.Images, Sources: m.Sources}
}

// DetailFields are the non-image fields stored by Store.
var DetailFields = []string{FieldOverview, FieldReleased, FieldContentRating, FieldGenres,
	FieldStudios, FieldTags, FieldAltTitles, FieldRatings, FieldPeople}

// AllDetailFields is DetailFields plus one field per image type.
func AllDetailFields() []string {
	out := append([]string(nil), DetailFields...)
	for _, t := range ImageTypes {
		out = append(out, ImageField(t))
	}
	return out
}

func unlockedFields(fields, locked []string) []string {
	lk := lockSet(locked)
	var out []string
	for _, f := range fields {
		if lk[f] || (strings.HasPrefix(f, FieldImages+".") && lk[FieldImages]) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Store reads and writes the metadata tables.
type Store struct {
	db  *db.DB
	now func() time.Time
}

// NewStore returns a Store.
func NewStore(d *db.DB) *Store { return &Store{db: d, now: time.Now} }

// Save writes every detail field except the locked ones.
func (s *Store) Save(ctx context.Context, itemID int64, d Details, locked []string) error {
	return s.SaveFields(ctx, itemID, d, unlockedFields(AllDetailFields(), locked))
}

// SaveFields writes only the listed fields, each replaced wholesale.
func (s *Store) SaveFields(ctx context.Context, itemID int64, d Details, fields []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save metadata for item %d: %w", itemID, err)
	}
	defer tx.Rollback()
	if err := s.saveFields(ctx, tx, itemID, d, fields); err != nil {
		return fmt.Errorf("save metadata for item %d: %w", itemID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save metadata for item %d: %w", itemID, err)
	}
	return nil
}

func (s *Store) saveFields(ctx context.Context, tx *sql.Tx, itemID int64, d Details, fields []string) error {
	now := s.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO item_metadata(item_id, refreshed_at) VALUES(?, ?)
		ON CONFLICT(item_id) DO UPDATE SET refreshed_at = excluded.refreshed_at`, itemID, now); err != nil {
		return err
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT sources FROM item_metadata WHERE item_id = ?`, itemID).Scan(&raw); err != nil {
		return err
	}
	sources := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		sources = map[string]string{}
	}
	for _, f := range fields {
		if err := saveField(ctx, tx, itemID, d, f); err != nil {
			return fmt.Errorf("field %s: %w", f, err)
		}
		if src := d.Sources[f]; src != "" {
			sources[f] = src
		} else {
			delete(sources, f)
		}
	}
	b, err := json.Marshal(sources)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE item_metadata SET sources = ? WHERE item_id = ?`, string(b), itemID)
	return err
}

func saveField(ctx context.Context, tx *sql.Tx, itemID int64, d Details, f string) error {
	switch f {
	case FieldOverview:
		return setColumn(ctx, tx, itemID, "overview", d.Overview)
	case FieldReleased:
		return setColumn(ctx, tx, itemID, "released", d.Released)
	case FieldContentRating:
		return setColumn(ctx, tx, itemID, "content_rating", d.ContentRating)
	case FieldStudios:
		return setJSON(ctx, tx, itemID, "studios", cleanList(d.Studios))
	case FieldTags:
		return setJSON(ctx, tx, itemID, "tags", cleanList(d.Tags))
	case FieldAltTitles:
		alt := d.AltTitles
		if alt == nil {
			alt = map[string]string{}
		}
		return setJSON(ctx, tx, itemID, "alt_titles", alt)
	case FieldRatings:
		r := d.Ratings
		if r == nil {
			r = map[string]float64{}
		}
		return setJSON(ctx, tx, itemID, "ratings", r)
	case FieldGenres:
		return saveGenres(ctx, tx, itemID, d.Genres)
	case FieldPeople:
		return savePeople(ctx, tx, itemID, d.People, d.Sources[FieldPeople])
	}
	if t, ok := strings.CutPrefix(f, FieldImages+"."); ok && t != "" {
		return saveImages(ctx, tx, itemID, t, imagesOfType(d.Images, t), d.Sources[f])
	}
	return fmt.Errorf("unknown metadata field %q", f)
}

// setColumn: col always comes from the fixed switch in saveField, never from input.
func setColumn(ctx context.Context, tx *sql.Tx, itemID int64, col string, v any) error {
	_, err := tx.ExecContext(ctx, `UPDATE item_metadata SET `+col+` = ? WHERE item_id = ?`, v, itemID)
	return err
}

func setJSON(ctx context.Context, tx *sql.Tx, itemID int64, col string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return setColumn(ctx, tx, itemID, col, string(b))
}

// cleanList trims, drops empties and case-insensitive duplicates, keeps order.
func cleanList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}

func saveGenres(ctx context.Context, tx *sql.Tx, itemID int64, genres []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_genres WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for i, g := range cleanList(genres) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_genres(item_id, genre, position) VALUES(?, ?, ?)`, itemID, g, i); err != nil {
			return err
		}
	}
	return nil
}

func savePeople(ctx context.Context, tx *sql.Tx, itemID int64, people []Person, provider string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_people WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for i, p := range people {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		key := "name:" + strings.ToLower(name)
		if p.ImageURL != "" {
			key = "img:" + p.ImageURL
		}
		var pid int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO people(key, name, image_url) VALUES(?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET name = excluded.name,
			  image_url = CASE WHEN excluded.image_url <> '' THEN excluded.image_url ELSE people.image_url END
			RETURNING id`, key, name, p.ImageURL).Scan(&pid); err != nil {
			return err
		}
		role := p.Role
		if role == "" {
			role = RoleActor
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_people(item_id, person_id, role, character, sort_order)
			VALUES(?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, itemID, pid, role, p.Character, i); err != nil {
			return err
		}
		if p.ImageURL != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO images(person_id, type, provider, source_url, sha)
				VALUES(?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, pid, ImageThumb, provider, p.ImageURL, URLHash(p.ImageURL)); err != nil {
				return err
			}
		}
	}
	return nil
}

func imagesOfType(imgs []Image, t string) []Image {
	var out []Image
	seen := map[string]bool{}
	for _, im := range imgs {
		if im.Type != t || im.URL == "" || seen[im.URL] {
			continue
		}
		seen[im.URL] = true
		out = append(out, im)
		if len(out) == maxImagesPerType {
			break
		}
	}
	return out
}

func saveImages(ctx context.Context, tx *sql.Tx, itemID int64, t string, imgs []Image, provider string) error {
	for i, img := range imgs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO images(item_id, type, provider, source_url, sha, lang, width, height, sort_order)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(item_id, type, source_url) WHERE item_id IS NOT NULL DO UPDATE SET
			  provider = excluded.provider, lang = excluded.lang, width = excluded.width,
			  height = excluded.height, sort_order = excluded.sort_order`,
			itemID, t, provider, img.URL, URLHash(img.URL), img.Lang, img.Width, img.Height, i); err != nil {
			return err
		}
	}
	q := `DELETE FROM images WHERE item_id = ? AND type = ?`
	args := []any{itemID, t}
	if len(imgs) > 0 {
		q += ` AND source_url NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(imgs)), ",") + `)`
		for _, img := range imgs {
			args = append(args, img.URL)
		}
	}
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

// View is the stored metadata of one item, ready for the API.
type View struct {
	Overview, Released, ContentRating string
	Genres, Studios, Tags             []string
	AltTitles                         map[string]string
	Ratings                           map[string]float64
	People                            []PersonRef
	Images                            []ImageRef
	Sources                           map[string]string
	RefreshedAt                       string
}

// PersonRef is a credit with the person's image row (0 = none).
type PersonRef struct {
	ID                    int64
	Name, Role, Character string
	ImageID               int64
}

// ImageRef is one stored image of an item.
type ImageRef struct {
	ID            int64
	Type, Lang    string
	Width, Height int
}

// Load returns the stored metadata. ok is false when nothing is stored yet.
func (s *Store) Load(ctx context.Context, itemID int64) (View, bool, error) {
	var v View
	var studios, tags, alt, ratings, sources string
	err := s.db.QueryRowContext(ctx, `SELECT overview, released, content_rating, studios, tags,
		alt_titles, ratings, sources, refreshed_at FROM item_metadata WHERE item_id = ?`, itemID).
		Scan(&v.Overview, &v.Released, &v.ContentRating, &studios, &tags, &alt, &ratings, &sources, &v.RefreshedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, false, nil
	}
	if err != nil {
		return View{}, false, fmt.Errorf("load metadata for item %d: %w", itemID, err)
	}
	for _, p := range []struct {
		raw string
		dst any
	}{{studios, &v.Studios}, {tags, &v.Tags}, {alt, &v.AltTitles}, {ratings, &v.Ratings}, {sources, &v.Sources}} {
		if err := json.Unmarshal([]byte(p.raw), p.dst); err != nil {
			return View{}, false, fmt.Errorf("load metadata for item %d: %w", itemID, err)
		}
	}
	if v.Genres, err = s.genres(ctx, itemID); err != nil {
		return View{}, false, err
	}
	if v.People, err = s.people(ctx, itemID); err != nil {
		return View{}, false, err
	}
	if v.Images, err = s.images(ctx, itemID); err != nil {
		return View{}, false, err
	}
	return v, true, nil
}

func (s *Store) genres(ctx context.Context, itemID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT genre FROM item_genres WHERE item_id = ? ORDER BY position`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) people(ctx context.Context, itemID int64) ([]PersonRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.name, ip.role, ip.character,
		COALESCE((SELECT i.id FROM images i WHERE i.person_id = p.id AND i.source_url = p.image_url), 0)
		FROM item_people ip JOIN people p ON p.id = ip.person_id
		WHERE ip.item_id = ? ORDER BY ip.sort_order`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PersonRef{}
	for rows.Next() {
		var p PersonRef
		if err := rows.Scan(&p.ID, &p.Name, &p.Role, &p.Character, &p.ImageID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) images(ctx context.Context, itemID int64) ([]ImageRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, type, lang, width, height FROM images
		WHERE item_id = ? ORDER BY type, sort_order`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageRef{}
	for rows.Next() {
		var r ImageRef
		if err := rows.Scan(&r.ID, &r.Type, &r.Lang, &r.Width, &r.Height); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImageRow is a full images row (for downloading and serving).
type ImageRow struct {
	ID, ItemID, PersonID               int64
	Type, Provider, SourceURL, SHA     string
	Status                             string
	Width, Height, SortOrder           int
}

const imageCols = `id, COALESCE(item_id, 0), COALESCE(person_id, 0), type, provider, source_url, sha, status, width, height, sort_order`

type scanner interface{ Scan(dest ...any) error }

func scanImage(sc scanner) (ImageRow, error) {
	var r ImageRow
	err := sc.Scan(&r.ID, &r.ItemID, &r.PersonID, &r.Type, &r.Provider, &r.SourceURL, &r.SHA, &r.Status, &r.Width, &r.Height, &r.SortOrder)
	return r, err
}

// Image returns one image row; a missing row wraps ErrNotFound.
func (s *Store) Image(ctx context.Context, id int64) (ImageRow, error) {
	r, err := scanImage(s.db.QueryRowContext(ctx, `SELECT `+imageCols+` FROM images WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ImageRow{}, fmt.Errorf("image %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return ImageRow{}, fmt.Errorf("image %d: %w", id, err)
	}
	return r, nil
}

// SetImageStatus records "ok" or "failed" after a download attempt.
func (s *Store) SetImageStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE images SET status = ? WHERE id = ?`, status, id)
	return err
}

// PrimaryImages returns the first image of each type for an item.
func (s *Store) PrimaryImages(ctx context.Context, itemID int64) ([]ImageRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+imageCols+` FROM images WHERE item_id = ? AND sort_order = 0`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageRow
	for rows.Next() {
		r, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneCache deletes expired provider responses.
func (s *Store) PruneCache(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM metadata_cache WHERE expires_at <= ?`, s.now().Unix())
	if err != nil {
		return 0, fmt.Errorf("prune metadata cache: %w", err)
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`
If the image upsert fails with `ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE
constraint`, check that the conflict target's `WHERE item_id IS NOT NULL` matches the partial
index `images_item_src` exactly.

- [ ] **Step 5: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): store for details, genres, people and images"
```

---

### Task 7: Artwork cache (download, resized JPEG variants)

**Files:**
- Create: `server/internal/metadata/artwork/artwork.go`
- Test: `server/internal/metadata/artwork/artwork_test.go`
- Modify: `server/go.mod`, `server/go.sum` (adds `golang.org/x/image`)

**Interfaces:**
- Consumes: nothing from `metadata` (this package must not import it).
- Produces: `Widths map[string][]int` (`poster: 300, 600`; `backdrop: 1280`; `thumb: 480`; other types keep only the original, because logos/clear art need transparency); `New(dir string, client *http.Client) *Store` with exported fields `Dir`, `HTTP`, `MaxBytes` (default 25 MiB), `AllowedFileRoots []string`; `(*Store) Has(sha) bool`; `(*Store) Ensure(ctx, sha, srcURL, imageType string) error` (download once, write variants, then the original; `http(s)://` or `file://` inside `AllowedFileRoots`); `(*Store) Path(sha, imageType string, w int) (path string, variantWidth int, err error)` (smallest variant ≥ w, else the original; w = 0 → original; variantWidth 0 = original).
  Layout: `<Dir>/<sha[0:2]>/<sha>/orig` plus `w300.jpg` etc. JPEG quality 85, Catmull-Rom scaling. Variants are only made when narrower than the source.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/artwork/artwork_test.go`:
```go
package artwork

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

var testSHA = strings.Repeat("ab", 32)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func serve(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/missing.png" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func width(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width
}

func TestEnsureBuildsPosterVariants(t *testing.T) {
	srv, hits := serve(t, pngBytes(t, 1000, 1500))
	s := New(t.TempDir(), nil)
	ctx := context.Background()
	if s.Has(testSHA) {
		t.Fatal("empty store has image")
	}
	for i := 0; i < 2; i++ {
		if err := s.Ensure(ctx, testSHA, srv.URL+"/p.png", "poster"); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 || !s.Has(testSHA) {
		t.Fatalf("hits=%d has=%v", hits.Load(), s.Has(testSHA))
	}
	cases := []struct{ ask, wantVariant, wantWidth int }{
		{200, 300, 300}, {300, 300, 300}, {500, 600, 600}, {2000, 0, 1000}, {0, 0, 1000},
	}
	for _, c := range cases {
		p, vw, err := s.Path(testSHA, "poster", c.ask)
		if err != nil || vw != c.wantVariant || width(t, p) != c.wantWidth {
			t.Errorf("w=%d: path=%s variant=%d err=%v", c.ask, p, vw, err)
		}
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(mustPath(t, s)))), "ab") {
		t.Fatal("expected <dir>/ab/<sha>/ layout")
	}
}

func mustPath(t *testing.T, s *Store) string {
	p, _, err := s.Path(testSHA, "poster", 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnsureSmallSourceAndLogo(t *testing.T) {
	srv, _ := serve(t, pngBytes(t, 200, 300))
	s := New(t.TempDir(), nil)
	if err := s.Ensure(context.Background(), testSHA, srv.URL+"/p.png", "poster"); err != nil {
		t.Fatal(err)
	}
	if _, vw, _ := s.Path(testSHA, "poster", 300); vw != 0 {
		t.Fatalf("no variant wider than the source should exist, got %d", vw)
	}
	logoSHA := strings.Repeat("cd", 32)
	srv2, _ := serve(t, pngBytes(t, 800, 310))
	if err := s.Ensure(context.Background(), logoSHA, srv2.URL+"/l.png", "logo"); err != nil {
		t.Fatal(err)
	}
	if _, vw, _ := s.Path(logoSHA, "logo", 300); vw != 0 {
		t.Fatal("logos keep only the original")
	}
}

func TestEnsureErrors(t *testing.T) {
	srv, _ := serve(t, pngBytes(t, 10, 10))
	s := New(t.TempDir(), nil)
	ctx := context.Background()
	if err := s.Ensure(ctx, testSHA, srv.URL+"/missing.png", "poster"); err == nil || s.Has(testSHA) {
		t.Fatalf("404 must fail and cache nothing: %v", err)
	}
	if err := s.Ensure(ctx, "../../etc", srv.URL+"/p.png", "poster"); err == nil {
		t.Fatal("invalid sha accepted")
	}
	s.MaxBytes = 10
	if err := s.Ensure(ctx, testSHA, srv.URL+"/p.png", "poster"); err == nil {
		t.Fatal("oversized image accepted")
	}
}

func TestEnsureLocalFiles(t *testing.T) {
	media := t.TempDir()
	poster := filepath.Join(media, "Movie (1999)", "poster.png")
	os.MkdirAll(filepath.Dir(poster), 0o755)
	os.WriteFile(poster, pngBytes(t, 700, 1000), 0o644)
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(poster)}).String()

	s := New(t.TempDir(), nil)
	if err := s.Ensure(context.Background(), testSHA, fileURL, "poster"); err == nil {
		t.Fatal("file outside allowed roots accepted")
	}
	s.AllowedFileRoots = []string{media}
	if err := s.Ensure(context.Background(), testSHA, fileURL, "poster"); err != nil {
		t.Fatal(err)
	}
	if _, vw, _ := s.Path(testSHA, "poster", 600); vw != 600 {
		t.Fatalf("variant %d", vw)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: New`

- [ ] **Step 3: Implement**

`server/internal/metadata/artwork/artwork.go`:
```go
// Package artwork downloads images into the config directory and builds
// resized JPEG variants. It never writes under the media root.
package artwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Widths lists the variant widths built per image type. Other types (logos,
// clear art, banners) keep only the original because they need transparency.
var Widths = map[string][]int{
	"poster":   {300, 600},
	"backdrop": {1280},
	"thumb":    {480},
}

const origName = "orig"

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store is the on-disk artwork cache.
type Store struct {
	Dir              string
	HTTP             *http.Client
	MaxBytes         int64
	AllowedFileRoots []string // file:// sources must be inside one of these
	locks            [64]sync.Mutex
}

// New returns a Store rooted at dir. client may be nil.
func New(dir string, client *http.Client) *Store {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Store{Dir: dir, HTTP: client, MaxBytes: 25 << 20}
}

func (s *Store) folder(sha string) (string, error) {
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("artwork: invalid sha %q", sha)
	}
	return filepath.Join(s.Dir, sha[:2], sha), nil
}

func (s *Store) lock(sha string) *sync.Mutex {
	n, _ := strconv.ParseUint(sha[:2], 16, 8)
	return &s.locks[n%64]
}

func variantName(w int) string { return "w" + strconv.Itoa(w) + ".jpg" }

// Has reports whether the original is cached.
func (s *Store) Has(sha string) bool {
	dir, err := s.folder(sha)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, origName))
	return err == nil
}

// Ensure downloads src once and builds the variants for imageType. The
// original is written last, so its presence means the entry is complete.
func (s *Store) Ensure(ctx context.Context, sha, src, imageType string) error {
	dir, err := s.folder(sha)
	if err != nil {
		return err
	}
	mu := s.lock(sha)
	mu.Lock()
	defer mu.Unlock()
	orig := filepath.Join(dir, origName)
	if _, err := os.Stat(orig); err == nil {
		return nil
	}
	data, err := s.read(ctx, src)
	if err != nil {
		return fmt.Errorf("artwork %s: %w", sha[:12], err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("artwork %s: decode: %w", sha[:12], err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for _, w := range Widths[imageType] {
		if w >= img.Bounds().Dx() {
			continue
		}
		if err := writeVariant(filepath.Join(dir, variantName(w)), img, w); err != nil {
			return fmt.Errorf("artwork %s: variant %d: %w", sha[:12], w, err)
		}
	}
	return writeAtomic(orig, data)
}

// Path returns the file to serve for width w and that file's width (0 = original).
func (s *Store) Path(sha, imageType string, w int) (string, int, error) {
	dir, err := s.folder(sha)
	if err != nil {
		return "", 0, err
	}
	if w > 0 {
		ws := append([]int(nil), Widths[imageType]...)
		sort.Ints(ws)
		for _, vw := range ws {
			if vw < w {
				continue
			}
			p := filepath.Join(dir, variantName(vw))
			if _, err := os.Stat(p); err == nil {
				return p, vw, nil
			}
		}
	}
	p := filepath.Join(dir, origName)
	if _, err := os.Stat(p); err != nil {
		return "", 0, fmt.Errorf("artwork %s: not cached: %w", sha[:12], err)
	}
	return p, 0, nil
}

func (s *Store) read(ctx context.Context, src string) ([]byte, error) {
	u, err := url.Parse(src)
	if err != nil {
		return nil, errors.New("bad source url")
	}
	switch u.Scheme {
	case "http", "https":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
		}
		return readLimited(resp.Body, s.MaxBytes)
	case "file":
		p := filepath.Clean(filepath.FromSlash(u.Path))
		if !s.fileAllowed(p) {
			return nil, errors.New("local file outside allowed roots")
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readLimited(f, s.MaxBytes)
	default:
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
}

func (s *Store) fileAllowed(p string) bool {
	for _, root := range s.AllowedFileRoots {
		rel, err := filepath.Rel(filepath.Clean(root), p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("image too large")
	}
	return b, nil
}

func writeVariant(path string, src image.Image, w int) error {
	b := src.Bounds()
	h := int(float64(b.Dy())*float64(w)/float64(b.Dx()) + 0.5)
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes())
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Fetch the dependency and run tests**

Run: `make tidy && make test`
Expected: `go.mod` gains `golang.org/x/image`; `internal/metadata/artwork` PASS.

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/metadata/artwork
git commit -m "feat(metadata): artwork cache with resized JPEG variants"
```

---

### Task 8: TMDB provider (search, movie, TV with seasons, find, certifications)

**Files:**
- Create: `server/internal/metadata/tmdb/tmdb.go`, `server/internal/metadata/tmdb/movie.go`, `server/internal/metadata/tmdb/tv.go`
- Create fixtures: `server/internal/metadata/tmdb/testdata/` `3_configuration.json`, `3_search_movie.json`, `3_movie_603.json`, `3_movie_603.en-US.json`, `3_find_tt0133093.json`, `3_search_tv.json`, `3_tv_1396.json`, `3_tv_1396_season_1.json`, `3_tv_1396_season_1.en-US.json`
- Test: `server/internal/metadata/tmdb/tmdb_test.go`, `server/internal/metadata/tmdb/live_test.go` (build tag `live`)

**Interfaces:**
- Consumes: `metadata.Provider`, `Query`, `Candidate`, `Record`, `Image`, `Person`, `SeasonRecord`, `EpisodeRecord`, `Settings`, `DefaultSettings`, `Fetcher`, `NewFetcher`, `FetcherOptions`, `ErrNotFound`, `ErrNoUsableID`, `ErrNotConfigured`, `Kind*`, `Lib*`, `Image*`, `Role*`.
- Produces: `tmdb.DefaultBaseURL = "https://api.themoviedb.org/3"`; `type Options struct{ BaseURL string; Settings func(context.Context) (metadata.Settings, error); Fetcher *metadata.Fetcher }`; `tmdb.New(o Options) *Provider` (name `"tmdb"`, supports `movie`/`show` in every library type except `homevideos`); `IsBearerToken(key) bool`.

TMDB API v3 facts this task relies on (from developer.themoviedb.org):
- Auth: either query `api_key=<v3 key>` or header `Authorization: Bearer <v4 read access token>` (a JWT, starts with `eyJ`, two dots). Both work on every v3 endpoint.
- `GET /configuration` → `images.secure_base_url` (`https://image.tmdb.org/t/p/`). Image URL = base + size + `file_path`; sizes used: `original` (artwork, resized locally), `h632` (profile photos).
- `GET /search/movie?query=&year=&language=&include_adult=false` → `results[]{id,title,original_title,release_date}`; `GET /search/tv?query=&first_air_date_year=` → `results[]{id,name,original_name,first_air_date}`.
- `GET /movie/{id}?language=&append_to_response=credits,images,release_dates,external_ids,keywords&include_image_language=en,null` → `release_dates.results[]{iso_3166_1, release_dates[]{certification,type}}` (type 3 = theatrical), `keywords.keywords[]`, `credits.cast[]{name,character,order,profile_path}`, `credits.crew[]{name,job,department,profile_path}`, `images.{posters,backdrops,logos}[]{file_path,width,height,iso_639_1,vote_average}`.
- `GET /tv/{id}?append_to_response=credits,images,content_ratings,external_ids,keywords` → `content_ratings.results[]{iso_3166_1,rating}`, `keywords.results[]`, `external_ids.{imdb_id,tvdb_id}`, `seasons[]{season_number,...}`, `networks[]`, `created_by[]`.
- `GET /tv/{id}/season/{n}?language=` → `episodes[]{season_number,episode_number,name,overview,air_date,still_path}`.
- `GET /find/{external_id}?external_source=imdb_id|tvdb_id` → `movie_results[]`, `tv_results[]`.
- Missing translations come back as empty `overview` (and episode names like `"Episode 2"`), so the provider re-asks in `FallbackLanguage` and fills only the gaps.

Behaviour: search uses the year filter first and repeats without it when nothing comes back;
candidates get provider score `max(0.5, 1 − 0.05 × rank)` and a second candidate for the original
title when it differs (`metadata.Rank` collapses them). Content rating = first country in
`CertCountries` with a value (movies prefer theatrical type 3). Images: preferred language, then
`en`, then textless; backdrops textless first; max 10 per type. People: creators, top 20 cast,
directors, writing department, producers.

- [ ] **Step 1: Write the fixtures** (trimmed real responses: one season, two episodes, a few credits)

`server/internal/metadata/tmdb/testdata/3_configuration.json`:
```json
{
  "change_keys": ["adult", "air_date", "also_known_as"],
  "images": {
    "base_url": "http://image.tmdb.org/t/p/",
    "secure_base_url": "https://image.tmdb.org/t/p/",
    "backdrop_sizes": ["w300", "w780", "w1280", "original"],
    "logo_sizes": ["w45", "w92", "w154", "w185", "w300", "w500", "original"],
    "poster_sizes": ["w92", "w154", "w185", "w342", "w500", "w780", "original"],
    "profile_sizes": ["w45", "w185", "h632", "original"],
    "still_sizes": ["w92", "w185", "w300", "original"]
  }
}
```

`server/internal/metadata/tmdb/testdata/3_search_movie.json`:
```json
{
  "page": 1,
  "results": [
    {"adult": false, "backdrop_path": "/ncEsesgOJDNrTUED89hYbA117wo.jpg", "genre_ids": [28, 878], "id": 603,
     "original_language": "en", "original_title": "The Matrix", "overview": "Set in the 22nd century, The Matrix tells the story of a computer hacker...",
     "popularity": 95.2, "poster_path": "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg", "release_date": "1999-03-31",
     "title": "The Matrix", "video": false, "vote_average": 8.2, "vote_count": 26280},
    {"adult": false, "backdrop_path": "/gYyLZCmdmBGWiXUbNMyWvGqKDBC.jpg", "genre_ids": [12, 28, 878], "id": 604,
     "original_language": "en", "original_title": "The Matrix Reloaded", "overview": "Six months after the events depicted in The Matrix...",
     "popularity": 50.1, "poster_path": "/9TGHDvWrqKBzwDxDodHYXEmOE6J.jpg", "release_date": "2003-05-15",
     "title": "The Matrix Reloaded", "video": false, "vote_average": 7.1, "vote_count": 10800}
  ],
  "total_pages": 1,
  "total_results": 2
}
```

`server/internal/metadata/tmdb/testdata/3_movie_603.json` (overview empty on purpose: no en-AU translation):
```json
{
  "adult": false,
  "backdrop_path": "/ncEsesgOJDNrTUED89hYbA117wo.jpg",
  "belongs_to_collection": {"id": 2344, "name": "The Matrix Collection", "poster_path": "/bV9qTVHTVf0gkW0j7p7M0ILD4pG.jpg", "backdrop_path": "/bRm2DEgUiYciDw3myHuYFInD7la.jpg"},
  "budget": 63000000,
  "genres": [{"id": 28, "name": "Action"}, {"id": 878, "name": "Science Fiction"}],
  "homepage": "http://www.warnerbros.com/matrix",
  "id": 603,
  "imdb_id": "tt0133093",
  "original_language": "en",
  "original_title": "The Matrix",
  "overview": "",
  "popularity": 95.2,
  "poster_path": "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg",
  "production_companies": [
    {"id": 79, "logo_path": "/at4uYdwAAgNRKhZuuFX8ShKSybw.png", "name": "Village Roadshow Pictures", "origin_country": "US"},
    {"id": 174, "logo_path": "/zhD3hhtKB5qyv7ZeL4uLpNxgMVU.png", "name": "Warner Bros. Pictures", "origin_country": "US"}
  ],
  "release_date": "1999-03-31",
  "revenue": 463517383,
  "runtime": 136,
  "status": "Released",
  "tagline": "Welcome to the Real World.",
  "title": "The Matrix",
  "video": false,
  "vote_average": 8.2,
  "vote_count": 26280,
  "credits": {
    "cast": [
      {"adult": false, "gender": 2, "id": 6384, "known_for_department": "Acting", "name": "Keanu Reeves", "original_name": "Keanu Reeves", "popularity": 40.1, "profile_path": "/4D0PpNI0kmP58hgrwGC3wCjxhnm.jpg", "cast_id": 34, "character": "Thomas A. Anderson / Neo", "credit_id": "52fe425bc3a36847f80181c1", "order": 0},
      {"adult": false, "gender": 1, "id": 530, "known_for_department": "Acting", "name": "Carrie-Anne Moss", "original_name": "Carrie-Anne Moss", "popularity": 20.3, "profile_path": "/xD4jTA3KmVp5Rq3aHcymL9DUGjD.jpg", "cast_id": 21, "character": "Trinity", "credit_id": "52fe425bc3a36847f80181c9", "order": 2},
      {"adult": false, "gender": 2, "id": 2975, "known_for_department": "Acting", "name": "Laurence Fishburne", "original_name": "Laurence Fishburne", "popularity": 25.7, "profile_path": "/iwx7h0AfUNmkSmYVQb5w4vU9rRN.jpg", "cast_id": 20, "character": "Morpheus", "credit_id": "52fe425bc3a36847f80181c5", "order": 1}
    ],
    "crew": [
      {"adult": false, "gender": 1, "id": 9340, "known_for_department": "Directing", "name": "Lana Wachowski", "profile_path": "/qKw2vKdJMqx5Uh8jPHFmd5pVGJR.jpg", "credit_id": "52fe425bc3a36847f8018183", "department": "Directing", "job": "Director"},
      {"adult": false, "gender": 1, "id": 9339, "known_for_department": "Directing", "name": "Lilly Wachowski", "profile_path": "/8l2wQbdEpGJdmsmcWHAp8m0oQuS.jpg", "credit_id": "52fe425bc3a36847f801818f", "department": "Directing", "job": "Director"},
      {"adult": false, "gender": 1, "id": 9340, "known_for_department": "Directing", "name": "Lana Wachowski", "profile_path": "/qKw2vKdJMqx5Uh8jPHFmd5pVGJR.jpg", "credit_id": "52fe425bc3a36847f8018195", "department": "Writing", "job": "Writer"},
      {"adult": false, "gender": 2, "id": 1091, "known_for_department": "Production", "name": "Joel Silver", "profile_path": "/xqx5f7n8qXo0dYJHT9X8a9w7Z5m.jpg", "credit_id": "52fe425bc3a36847f80181a1", "department": "Production", "job": "Producer"},
      {"adult": false, "gender": 2, "id": 2033, "known_for_department": "Camera", "name": "Bill Pope", "profile_path": null, "credit_id": "52fe425bc3a36847f80181ab", "department": "Camera", "job": "Director of Photography"}
    ]
  },
  "images": {
    "backdrops": [
      {"aspect_ratio": 1.778, "height": 1080, "iso_639_1": "en", "file_path": "/2u1Kv8ULD6b0ROJLDu0Ks8ACXIF.jpg", "vote_average": 5.4, "vote_count": 4, "width": 1920},
      {"aspect_ratio": 1.778, "height": 2160, "iso_639_1": null, "file_path": "/ncEsesgOJDNrTUED89hYbA117wo.jpg", "vote_average": 5.6, "vote_count": 10, "width": 3840}
    ],
    "logos": [
      {"aspect_ratio": 3.2, "height": 500, "iso_639_1": "en", "file_path": "/9Nw5Gyw6cEGRIBuSc5pJQhJvTOo.png", "vote_average": 5.3, "vote_count": 3, "width": 1600}
    ],
    "posters": [
      {"aspect_ratio": 0.667, "height": 3000, "iso_639_1": null, "file_path": "/dXNAPwY7VrqMAo51EKhhCJfaGb5.jpg", "vote_average": 5.2, "vote_count": 4, "width": 2000},
      {"aspect_ratio": 0.667, "height": 1500, "iso_639_1": "en", "file_path": "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg", "vote_average": 5.7, "vote_count": 20, "width": 1000}
    ]
  },
  "release_dates": {
    "results": [
      {"iso_3166_1": "AU", "release_dates": [
        {"certification": "", "descriptors": [], "iso_639_1": "", "note": "Premiere", "release_date": "1999-03-31T00:00:00.000Z", "type": 1},
        {"certification": "M", "descriptors": [], "iso_639_1": "", "note": "", "release_date": "1999-04-08T00:00:00.000Z", "type": 3}
      ]},
      {"iso_3166_1": "DE", "release_dates": [
        {"certification": "16", "descriptors": [], "iso_639_1": "", "note": "", "release_date": "1999-06-17T00:00:00.000Z", "type": 3}
      ]},
      {"iso_3166_1": "US", "release_dates": [
        {"certification": "R", "descriptors": [], "iso_639_1": "", "note": "", "release_date": "1999-03-31T00:00:00.000Z", "type": 3}
      ]}
    ]
  },
  "external_ids": {"imdb_id": "tt0133093", "wikidata_id": "Q83495", "facebook_id": "TheMatrixMovie", "instagram_id": null, "twitter_id": null},
  "keywords": {"keywords": [
    {"id": 83, "name": "saving the world"},
    {"id": 310, "name": "artificial intelligence (a.i.)"},
    {"id": 4565, "name": "dystopia"}
  ]}
}
```

`server/internal/metadata/tmdb/testdata/3_movie_603.en-US.json`:
```json
{"id": 603, "title": "The Matrix", "overview": "Set in the 22nd century, The Matrix tells the story of a computer hacker who joins a group of underground insurgents fighting the vast and powerful computers who now rule the earth."}
```

`server/internal/metadata/tmdb/testdata/3_find_tt0133093.json`:
```json
{
  "movie_results": [{"adult": false, "backdrop_path": "/ncEsesgOJDNrTUED89hYbA117wo.jpg", "id": 603, "title": "The Matrix", "original_title": "The Matrix", "media_type": "movie", "release_date": "1999-03-31"}],
  "person_results": [],
  "tv_results": [],
  "tv_episode_results": [],
  "tv_season_results": []
}
```

`server/internal/metadata/tmdb/testdata/3_search_tv.json`:
```json
{
  "page": 1,
  "results": [
    {"adult": false, "backdrop_path": "/tsRy63Mu5cu8etL1X7ZLyf7UP1M.jpg", "genre_ids": [18, 80], "id": 1396,
     "origin_country": ["US"], "original_language": "en", "original_name": "Breaking Bad",
     "overview": "Walter White, a New Mexico chemistry teacher...", "popularity": 280.5,
     "poster_path": "/ztkUQFLlC19CCMYHW9o1zWhJRNq.jpg", "first_air_date": "2008-01-20", "name": "Breaking Bad",
     "vote_average": 8.9, "vote_count": 15000}
  ],
  "total_pages": 1,
  "total_results": 1
}
```

`server/internal/metadata/tmdb/testdata/3_tv_1396.json`:
```json
{
  "backdrop_path": "/tsRy63Mu5cu8etL1X7ZLyf7UP1M.jpg",
  "created_by": [{"id": 66633, "credit_id": "52542286760ee31328001a7b", "name": "Vince Gilligan", "original_name": "Vince Gilligan", "gender": 2, "profile_path": "/z3E0DhBg1V1PZVEtS9vfFPzOWYB.jpg"}],
  "episode_run_time": [],
  "first_air_date": "2008-01-20",
  "genres": [{"id": 18, "name": "Drama"}, {"id": 80, "name": "Crime"}],
  "id": 1396,
  "in_production": false,
  "languages": ["en"],
  "last_air_date": "2013-09-29",
  "name": "Breaking Bad",
  "networks": [{"id": 174, "logo_path": "/alqLicR1ZMHMaZGP3xRQxn9sq7p.png", "name": "AMC", "origin_country": "US"}],
  "number_of_episodes": 62,
  "number_of_seasons": 5,
  "origin_country": ["US"],
  "original_language": "en",
  "original_name": "Breaking Bad",
  "overview": "Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer and given a prognosis of only two years left to live. He becomes filled with a sense of fearlessness and an unrelenting desire to secure his family's financial future at any cost as he enters the dangerous world of drugs and crime.",
  "popularity": 280.5,
  "poster_path": "/ztkUQFLlC19CCMYHW9o1zWhJRNq.jpg",
  "production_companies": [
    {"id": 11073, "logo_path": "/aCbASRcI1MI7DXjPbSW9Fcv9uGR.png", "name": "Sony Pictures Television Studios", "origin_country": "US"},
    {"id": 33742, "logo_path": "/ggABRl2wQgF8Ap0nlRDK2vF3TkJ.png", "name": "High Bridge Productions", "origin_country": "US"}
  ],
  "seasons": [
    {"air_date": "2008-01-20", "episode_count": 7, "id": 3572, "name": "Season 1", "overview": "High school chemistry teacher Walter White's life is suddenly transformed by a dire medical diagnosis.", "poster_path": "/1BP4xYv9ZG4ZVHkL7ocOziBbSYH.jpg", "season_number": 1, "vote_average": 8.3}
  ],
  "status": "Ended",
  "type": "Scripted",
  "vote_average": 8.9,
  "vote_count": 15000,
  "credits": {
    "cast": [
      {"adult": false, "gender": 2, "id": 17419, "known_for_department": "Acting", "name": "Bryan Cranston", "original_name": "Bryan Cranston", "popularity": 30.2, "profile_path": "/7Jahy5LZX2Fo8fGJltMreAI49hC.jpg", "character": "Walter White", "credit_id": "52542282760ee313280017f9", "order": 0},
      {"adult": false, "gender": 2, "id": 84497, "known_for_department": "Acting", "name": "Aaron Paul", "original_name": "Aaron Paul", "popularity": 18.1, "profile_path": "/8Kce1utfytAG5m1PbtVoDzmDZJH.jpg", "character": "Jesse Pinkman", "credit_id": "52542282760ee31328001845", "order": 1}
    ],
    "crew": []
  },
  "images": {
    "backdrops": [{"aspect_ratio": 1.778, "height": 1080, "iso_639_1": null, "file_path": "/tsRy63Mu5cu8etL1X7ZLyf7UP1M.jpg", "vote_average": 5.5, "vote_count": 8, "width": 1920}],
    "logos": [{"aspect_ratio": 5.0, "height": 200, "iso_639_1": "en", "file_path": "/chw44B2VnVhUaFVBfWr8Odq7Kxq.png", "vote_average": 5.3, "vote_count": 3, "width": 1000}],
    "posters": [{"aspect_ratio": 0.667, "height": 1500, "iso_639_1": "en", "file_path": "/ztkUQFLlC19CCMYHW9o1zWhJRNq.jpg", "vote_average": 5.6, "vote_count": 12, "width": 1000}]
  },
  "content_ratings": {"results": [
    {"descriptors": [], "iso_3166_1": "US", "rating": "TV-MA"},
    {"descriptors": [], "iso_3166_1": "AU", "rating": "MA15+"}
  ]},
  "external_ids": {"imdb_id": "tt0903747", "freebase_mid": "/m/03d34x8", "freebase_id": "/en/breaking_bad", "tvdb_id": 81189, "tvrage_id": 18164, "wikidata_id": "Q1079", "facebook_id": "BreakingBad", "instagram_id": "breakingbad", "twitter_id": "BreakingBad"},
  "keywords": {"results": [{"id": 818, "name": "based on novel or book"}, {"id": 4344, "name": "drug dealer"}]}
}
```

`server/internal/metadata/tmdb/testdata/3_tv_1396_season_1.json` (episode 2 untranslated on purpose):
```json
{
  "_id": "5256c89f19c2956ff6046d47",
  "air_date": "2008-01-20",
  "episodes": [
    {"air_date": "2008-01-20", "episode_number": 1, "episode_type": "standard", "id": 62085, "name": "Pilot",
     "overview": "When an unassuming high school chemistry teacher discovers he has a rare form of lung cancer, he decides to team up with a former student and create a top of the line crystal meth in a used RV, to provide for his family once he is gone.",
     "production_code": "", "runtime": 59, "season_number": 1, "show_id": 1396, "still_path": "/ydlY3iPfeOAvu8gVqrxPoMvzNCn.jpg", "vote_average": 8.1, "vote_count": 200},
    {"air_date": "2008-01-27", "episode_number": 2, "episode_type": "standard", "id": 62086, "name": "Episode 2",
     "overview": "", "production_code": "", "runtime": 49, "season_number": 1, "show_id": 1396, "still_path": "/tjDNvbokPLtEnpFyFPyXMOd6Zr1.jpg", "vote_average": 7.7, "vote_count": 150}
  ],
  "name": "Season 1",
  "overview": "High school chemistry teacher Walter White's life is suddenly transformed by a dire medical diagnosis.",
  "id": 3572,
  "poster_path": "/1BP4xYv9ZG4ZVHkL7ocOziBbSYH.jpg",
  "season_number": 1,
  "vote_average": 8.3
}
```

`server/internal/metadata/tmdb/testdata/3_tv_1396_season_1.en-US.json`:
```json
{
  "_id": "5256c89f19c2956ff6046d47",
  "air_date": "2008-01-20",
  "episodes": [
    {"air_date": "2008-01-20", "episode_number": 1, "id": 62085, "name": "Pilot", "overview": "When an unassuming high school chemistry teacher discovers he has a rare form of lung cancer...", "season_number": 1, "still_path": "/ydlY3iPfeOAvu8gVqrxPoMvzNCn.jpg"},
    {"air_date": "2008-01-27", "episode_number": 2, "id": 62086, "name": "Cat's in the Bag...", "overview": "Walt and Jesse attempt to tie up loose ends. The desperate situation gets more complicated with the flip of a coin.", "season_number": 1, "still_path": "/tjDNvbokPLtEnpFyFPyXMOd6Zr1.jpg"}
  ],
  "name": "Season 1",
  "overview": "High school chemistry teacher Walter White's life is suddenly transformed by a dire medical diagnosis.",
  "id": 3572,
  "poster_path": "/1BP4xYv9ZG4ZVHkL7ocOziBbSYH.jpg",
  "season_number": 1
}
```

- [ ] **Step 2: Write the failing tests**

The fixture server maps a request path to a file name (`/3/movie/603` → `3_movie_603`) and tries
`<name>.<language>.json` before `<name>.json`.

`server/internal/metadata/tmdb/tmdb_test.go`:
```go
package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

const testKey = "0123456789abcdef0123456789abcdef"

func staticSettings(key string, mod ...func(*metadata.Settings)) func(context.Context) (metadata.Settings, error) {
	return func(context.Context) (metadata.Settings, error) {
		st := metadata.DefaultSettings()
		st.TMDBKey = key
		for _, m := range mod {
			m(&st)
		}
		return st, nil
	}
}

func fixtureServer(t *testing.T, check func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		name := strings.ReplaceAll(strings.Trim(r.URL.Path, "/"), "/", "_")
		var tries []string
		if l := r.URL.Query().Get("language"); l != "" {
			tries = append(tries, name+"."+l+".json")
		}
		tries = append(tries, name+".json")
		for _, f := range tries {
			if b, err := os.ReadFile(filepath.Join("testdata", f)); err == nil {
				w.Header().Set("Content-Type", "application/json")
				w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newProvider(srv *httptest.Server, settings func(context.Context) (metadata.Settings, error)) *Provider {
	return New(Options{BaseURL: srv.URL + "/3", Settings: settings,
		Fetcher: metadata.NewFetcher(nil, "tmdb", metadata.FetcherOptions{RatePerSec: 1000, Burst: 1000, MaxRetries: -1})})
}

func urlsOf(imgs []metadata.Image, typ string) []string {
	var out []string
	for _, im := range imgs {
		if im.Type == typ {
			out = append(out, im.URL)
		}
	}
	return out
}

func TestSearchMovie(t *testing.T) {
	srv := fixtureServer(t, func(r *http.Request) {
		if r.URL.Query().Get("api_key") != testKey || r.Header.Get("Authorization") != "" {
			t.Errorf("v3 key must go in the query: %s", r.URL.RawQuery)
		}
	})
	p := newProvider(srv, staticSettings(testKey))
	cands, err := p.Search(context.Background(), metadata.Query{Kind: metadata.KindMovie, LibType: metadata.LibMovies, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 || cands[0].IDs["tmdb"] != "603" || cands[0].Year != 1999 || cands[0].Score != 1 || cands[1].Score != 0.95 {
		t.Fatalf("%+v", cands)
	}
}

func TestSearchRetriesWithoutYear(t *testing.T) {
	var withYear, without atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("first_air_date_year") != "" {
			withYear.Add(1)
			w.Write([]byte(`{"page":1,"results":[],"total_pages":0,"total_results":0}`))
			return
		}
		without.Add(1)
		b, _ := os.ReadFile("testdata/3_search_tv.json")
		w.Write(b)
	}))
	defer srv.Close()
	p := newProvider(srv, staticSettings(testKey))
	cands, err := p.Search(context.Background(), metadata.Query{Kind: metadata.KindShow, LibType: metadata.LibTV, Title: "Breaking Bad", Year: 2009})
	if err != nil || len(cands) != 1 || cands[0].IDs["tmdb"] != "1396" || withYear.Load() != 1 || without.Load() != 1 {
		t.Fatalf("cands=%+v err=%v with=%d without=%d", cands, err, withYear.Load(), without.Load())
	}
}

func TestFetchMovie(t *testing.T) {
	p := newProvider(fixtureServer(t, nil), staticSettings(testKey))
	rec, err := p.Fetch(context.Background(), metadata.KindMovie, map[string]string{"tmdb": "603"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Provider != "tmdb" || rec.Title != "The Matrix" || rec.Year != 1999 || rec.Released != "1999-03-31" {
		t.Fatalf("%+v", rec)
	}
	if !strings.HasPrefix(rec.Overview, "Set in the 22nd century") {
		t.Fatalf("overview not filled from fallback language: %q", rec.Overview)
	}
	if rec.IDs["imdb"] != "tt0133093" || rec.ContentRating != "M" || rec.Ratings["tmdb"] != 8.2 {
		t.Fatalf("ids %v rating %q ratings %v", rec.IDs, rec.ContentRating, rec.Ratings)
	}
	if strings.Join(rec.Genres, ",") != "Action,Science Fiction" || len(rec.Studios) != 2 || len(rec.Tags) != 3 {
		t.Fatalf("genres %v studios %v tags %v", rec.Genres, rec.Studios, rec.Tags)
	}
	posters := urlsOf(rec.Images, metadata.ImagePoster)
	if len(posters) != 2 || posters[0] != "https://image.tmdb.org/t/p/original/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg" {
		t.Fatalf("posters %v", posters)
	}
	if b := urlsOf(rec.Images, metadata.ImageBackdrop); b[0] != "https://image.tmdb.org/t/p/original/ncEsesgOJDNrTUED89hYbA117wo.jpg" {
		t.Fatalf("textless backdrop must come first: %v", b)
	}
	if len(urlsOf(rec.Images, metadata.ImageLogo)) != 1 {
		t.Fatal("logo missing")
	}
	var roles []string
	for _, p := range rec.People {
		roles = append(roles, p.Role+":"+p.Name)
	}
	want := "actor:Keanu Reeves,actor:Laurence Fishburne,actor:Carrie-Anne Moss,director:Lana Wachowski,director:Lilly Wachowski,writer:Lana Wachowski,producer:Joel Silver"
	if strings.Join(roles, ",") != want {
		t.Fatalf("people\n got %s\nwant %s", strings.Join(roles, ","), want)
	}
	if rec.People[0].Character != "Thomas A. Anderson / Neo" || rec.People[0].ImageURL != "https://image.tmdb.org/t/p/h632/4D0PpNI0kmP58hgrwGC3wCjxhnm.jpg" {
		t.Fatalf("%+v", rec.People[0])
	}
}

func TestCertificationCountryOrder(t *testing.T) {
	p := newProvider(fixtureServer(t, nil), staticSettings(testKey, func(s *metadata.Settings) { s.CertCountries = []string{"NZ", "US"} }))
	rec, err := p.Fetch(context.Background(), metadata.KindMovie, map[string]string{"tmdb": "603"})
	if err != nil || rec.ContentRating != "R" {
		t.Fatalf("rating %q err %v", rec.ContentRating, err)
	}
}

func TestFetchByIMDbUsesFind(t *testing.T) {
	p := newProvider(fixtureServer(t, nil), staticSettings(testKey))
	rec, err := p.Fetch(context.Background(), metadata.KindMovie, map[string]string{"imdb": "tt0133093"})
	if err != nil || rec.IDs["tmdb"] != "603" {
		t.Fatalf("rec ids %v err %v", rec.IDs, err)
	}
	if _, err := p.Fetch(context.Background(), metadata.KindMovie, map[string]string{"anidb": "1"}); !errors.Is(err, metadata.ErrNoUsableID) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Fetch(context.Background(), metadata.KindMovie, map[string]string{"tmdb": "999"}); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchShowWithSeasons(t *testing.T) {
	bearer := "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ0ZXN0In0.c2lnbmF0dXJl"
	srv := fixtureServer(t, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bearer || r.URL.Query().Get("api_key") != "" {
			t.Errorf("v4 token must go in the Authorization header")
		}
	})
	p := newProvider(srv, staticSettings(bearer))
	rec, err := p.Fetch(context.Background(), metadata.KindShow, map[string]string{"tmdb": "1396"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Title != "Breaking Bad" || rec.Year != 2008 || rec.ContentRating != "MA15+" ||
		rec.IDs["tvdb"] != "81189" || rec.IDs["imdb"] != "tt0903747" {
		t.Fatalf("%+v", rec)
	}
	if rec.People[0].Role != metadata.RoleCreator || rec.People[0].Name != "Vince Gilligan" {
		t.Fatalf("creator first: %+v", rec.People[0])
	}
	if rec.Studios[0] != "AMC" {
		t.Fatalf("network first: %v", rec.Studios)
	}
	if len(rec.Seasons) != 1 || rec.Seasons[0].Number != 1 || len(rec.Seasons[0].Episodes) != 2 {
		t.Fatalf("seasons %+v", rec.Seasons)
	}
	ep2 := rec.Seasons[0].Episodes[1]
	if ep2.Season != 1 || ep2.Number != 2 || ep2.Title != "Cat's in the Bag..." || ep2.Overview == "" || ep2.AirDate != "2008-01-27" {
		t.Fatalf("episode 2 not filled from fallback: %+v", ep2)
	}
	if len(ep2.Images) != 1 || ep2.Images[0].Type != metadata.ImageThumb {
		t.Fatalf("still missing: %+v", ep2.Images)
	}
	if len(rec.Seasons[0].Images) != 1 || rec.Seasons[0].Images[0].Type != metadata.ImagePoster {
		t.Fatalf("season poster missing")
	}
}

func TestNotConfigured(t *testing.T) {
	p := newProvider(fixtureServer(t, nil), staticSettings(""))
	if _, err := p.Search(context.Background(), metadata.Query{Kind: metadata.KindMovie, LibType: metadata.LibMovies, Title: "x"}); !errors.Is(err, metadata.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	if p.Supports(metadata.KindMovie, metadata.LibHomeVideos) || p.Supports(metadata.KindEpisode, metadata.LibTV) {
		t.Fatal("unexpected support")
	}
}

func TestIsBearerToken(t *testing.T) {
	if IsBearerToken(testKey) || !IsBearerToken("eyJa.eyJb.c") {
		t.Fatal("bearer detection")
	}
}
```

`server/internal/metadata/tmdb/live_test.go`:
```go
//go:build live

package tmdb

import (
	"context"
	"os"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

// TestLiveTMDB hits the real API. Run with -tags live and LISIMFLIX_TMDB_API_KEY set.
func TestLiveTMDB(t *testing.T) {
	key := os.Getenv("LISIMFLIX_TMDB_API_KEY")
	if key == "" {
		t.Skip("LISIMFLIX_TMDB_API_KEY not set")
	}
	p := New(Options{Settings: staticSettings(key), Fetcher: metadata.NewFetcher(nil, "tmdb", metadata.FetcherOptions{})})
	ctx := context.Background()
	cands, err := p.Search(ctx, metadata.Query{Kind: metadata.KindMovie, LibType: metadata.LibMovies, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	best, ok := metadata.PickBest(metadata.Query{Title: "The Matrix", Year: 1999}, cands, metadata.DefaultMatchThreshold)
	if !ok || best.IDs["tmdb"] != "603" {
		t.Fatalf("best %+v ok=%v", best, ok)
	}
	rec, err := p.Fetch(ctx, metadata.KindMovie, map[string]string{"tmdb": "603"})
	if err != nil || rec.Title != "The Matrix" || rec.IDs["imdb"] != "tt0133093" || rec.ContentRating == "" || len(rec.Images) == 0 || rec.Overview == "" {
		t.Fatalf("movie %+v err %v", rec, err)
	}
	show, err := p.Fetch(ctx, metadata.KindShow, map[string]string{"imdb": "tt0903747"})
	if err != nil || show.IDs["tvdb"] != "81189" || len(show.Seasons) == 0 || len(show.Seasons[0].Episodes) == 0 {
		t.Fatalf("show %+v err %v", show.IDs, err)
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: New` in `internal/metadata/tmdb`

- [ ] **Step 4: Implement the client and search**

`server/internal/metadata/tmdb/tmdb.go`:
```go
// Package tmdb is the TMDB (themoviedb.org) API v3 metadata provider.
package tmdb

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

const (
	// DefaultBaseURL is the TMDB v3 API root.
	DefaultBaseURL   = "https://api.themoviedb.org/3"
	defaultImageBase = "https://image.tmdb.org/t/p/"
	maxImagesPerType = 10
	maxCast          = 20
	maxCandidates    = 10
)

var imdbRe = regexp.MustCompile(`^tt\d{5,10}$`)

// Options configures the provider.
type Options struct {
	BaseURL  string
	Settings func(ctx context.Context) (metadata.Settings, error)
	Fetcher  *metadata.Fetcher
}

// Provider implements metadata.Provider for TMDB.
type Provider struct {
	o         Options
	mu        sync.Mutex
	imageBase string
}

var _ metadata.Provider = (*Provider)(nil)

// New returns a TMDB provider.
func New(o Options) *Provider {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.Fetcher == nil {
		o.Fetcher = metadata.NewFetcher(nil, "tmdb", metadata.FetcherOptions{})
	}
	return &Provider{o: o}
}

// Name implements metadata.Provider.
func (p *Provider) Name() string { return "tmdb" }

// Supports implements metadata.Provider: movies and shows outside home videos.
func (p *Provider) Supports(kind media.Kind, lt library.Type) bool {
	if lt == metadata.LibHomeVideos {
		return false
	}
	return kind == metadata.KindMovie || kind == metadata.KindShow
}

// IsBearerToken reports whether key is a v4 read access token (a JWT) rather than a v3 API key.
func IsBearerToken(key string) bool {
	return strings.HasPrefix(key, "eyJ") && strings.Count(key, ".") == 2
}

func (p *Provider) settings(ctx context.Context) (metadata.Settings, error) {
	st, err := p.o.Settings(ctx)
	if err != nil {
		return st, err
	}
	if st.TMDBKey == "" {
		return st, fmt.Errorf("tmdb: %w", metadata.ErrNotConfigured)
	}
	return st, nil
}

// get calls path with q. The cache key never contains the API key.
func (p *Provider) get(ctx context.Context, st metadata.Settings, path string, q url.Values, v any) error {
	if q == nil {
		q = url.Values{}
	}
	cacheKey := path
	if len(q) > 0 {
		cacheKey += "?" + q.Encode()
	}
	full := url.Values{}
	for k, vs := range q {
		full[k] = vs
	}
	h := http.Header{}
	if IsBearerToken(st.TMDBKey) {
		h.Set("Authorization", "Bearer "+st.TMDBKey)
	} else {
		full.Set("api_key", st.TMDBKey)
	}
	u := p.o.BaseURL + path
	if len(full) > 0 {
		u += "?" + full.Encode()
	}
	return p.o.Fetcher.GetJSON(ctx, u, cacheKey, h, v)
}

// imageBaseURL reads /configuration once per process, falling back to the default.
func (p *Provider) imageBaseURL(ctx context.Context, st metadata.Settings) string {
	p.mu.Lock()
	b := p.imageBase
	p.mu.Unlock()
	if b != "" {
		return b
	}
	var cfg struct {
		Images struct {
			SecureBaseURL string `json:"secure_base_url"`
		} `json:"images"`
	}
	if err := p.get(ctx, st, "/configuration", nil, &cfg); err != nil || cfg.Images.SecureBaseURL == "" {
		return defaultImageBase
	}
	p.mu.Lock()
	p.imageBase = cfg.Images.SecureBaseURL
	p.mu.Unlock()
	return cfg.Images.SecureBaseURL
}

type searchResult struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	FirstAirDate  string `json:"first_air_date"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

// Search implements metadata.Provider.
func (p *Provider) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	if !p.Supports(q.Kind, q.LibType) || strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	st, err := p.settings(ctx)
	if err != nil {
		return nil, err
	}
	path, yearParam := "/search/movie", "year"
	if q.Kind == metadata.KindShow {
		path, yearParam = "/search/tv", "first_air_date_year"
	}
	params := url.Values{"query": {q.Title}, "include_adult": {"false"}, "language": {st.Language}, "page": {"1"}}
	var resp searchResponse
	if q.Year > 0 {
		withYear := url.Values{yearParam: {strconv.Itoa(q.Year)}}
		for k, v := range params {
			withYear[k] = v
		}
		if err := p.get(ctx, st, path, withYear, &resp); err != nil {
			return nil, err
		}
	}
	if len(resp.Results) == 0 {
		if err := p.get(ctx, st, path, params, &resp); err != nil {
			return nil, err
		}
	}
	var out []metadata.Candidate
	for i, r := range resp.Results {
		if i >= maxCandidates {
			break
		}
		title, orig, date := r.Title, r.OriginalTitle, r.ReleaseDate
		if q.Kind == metadata.KindShow {
			title, orig, date = r.Name, r.OriginalName, r.FirstAirDate
		}
		score := math.Max(0.5, 1-0.05*float64(i))
		id := strconv.Itoa(r.ID)
		out = append(out, metadata.Candidate{Provider: "tmdb", IDs: map[string]string{"tmdb": id}, Title: title, Year: yearOf(date), Score: score})
		if orig != "" && orig != title {
			out = append(out, metadata.Candidate{Provider: "tmdb", IDs: map[string]string{"tmdb": id}, Title: orig, Year: yearOf(date), Score: score})
		}
	}
	return out, nil
}

// Fetch implements metadata.Provider. It uses ids["tmdb"], or finds the TMDB
// ID from ids["imdb"] (movies and shows) or ids["tvdb"] (shows).
func (p *Provider) Fetch(ctx context.Context, kind media.Kind, ids map[string]string) (metadata.Record, error) {
	if kind != metadata.KindMovie && kind != metadata.KindShow {
		return metadata.Record{}, metadata.ErrNoUsableID
	}
	st, err := p.settings(ctx)
	if err != nil {
		return metadata.Record{}, err
	}
	id := ids["tmdb"]
	if id == "" {
		if id, err = p.find(ctx, st, kind, ids); err != nil {
			return metadata.Record{}, err
		}
	}
	if !isDigits(id) {
		return metadata.Record{}, fmt.Errorf("tmdb: bad id %q: %w", id, metadata.ErrNoUsableID)
	}
	if kind == metadata.KindMovie {
		return p.movie(ctx, st, id)
	}
	return p.show(ctx, st, id)
}

func (p *Provider) find(ctx context.Context, st metadata.Settings, kind media.Kind, ids map[string]string) (string, error) {
	var ext, source string
	switch {
	case imdbRe.MatchString(ids["imdb"]):
		ext, source = ids["imdb"], "imdb_id"
	case kind == metadata.KindShow && isDigits(ids["tvdb"]):
		ext, source = ids["tvdb"], "tvdb_id"
	default:
		return "", metadata.ErrNoUsableID
	}
	type hit struct {
		ID int `json:"id"`
	}
	var resp struct {
		MovieResults []hit `json:"movie_results"`
		TVResults    []hit `json:"tv_results"`
	}
	if err := p.get(ctx, st, "/find/"+ext, url.Values{"external_source": {source}}, &resp); err != nil {
		return "", err
	}
	list := resp.MovieResults
	if kind == metadata.KindShow {
		list = resp.TVResults
	}
	if len(list) == 0 {
		return "", fmt.Errorf("tmdb find %s: %w", ext, metadata.ErrNotFound)
	}
	return strconv.Itoa(list[0].ID), nil
}

// --- shared response types and helpers ---

type named struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type castMember struct {
	Name        string `json:"name"`
	Character   string `json:"character"`
	Order       int    `json:"order"`
	ProfilePath string `json:"profile_path"`
}

type crewMember struct {
	Name        string `json:"name"`
	Job         string `json:"job"`
	Department  string `json:"department"`
	ProfilePath string `json:"profile_path"`
}

type creator struct {
	Name        string `json:"name"`
	ProfilePath string `json:"profile_path"`
}

type credits struct {
	Cast []castMember `json:"cast"`
	Crew []crewMember `json:"crew"`
}

type imageInfo struct {
	FilePath    string  `json:"file_path"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Lang        *string `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
}

type imageSet struct {
	Posters   []imageInfo `json:"posters"`
	Backdrops []imageInfo `json:"backdrops"`
	Logos     []imageInfo `json:"logos"`
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

func lang2(lang string) string {
	if i := strings.IndexByte(lang, '-'); i > 0 {
		return lang[:i]
	}
	return lang
}

// imageLangs is the include_image_language value: preferred, English, textless.
func imageLangs(lang string) string {
	if l := lang2(lang); l != "en" && l != "" {
		return l + ",en,null"
	}
	return "en,null"
}

func needsFallback(st metadata.Settings, title, overview string) bool {
	return st.FallbackLanguage != "" && st.FallbackLanguage != st.Language && (title == "" || overview == "")
}

func names(list []named) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		if n.Name != "" {
			out = append(out, n.Name)
		}
	}
	return out
}

func langOf(im imageInfo) string {
	if im.Lang == nil {
		return ""
	}
	return *im.Lang
}

func sortImages(list []imageInfo, pref string, textlessFirst bool) []imageInfo {
	rank := func(im imageInfo) int {
		l := langOf(im)
		switch {
		case textlessFirst && l == "":
			return 0
		case l == pref:
			return 1
		case l == "en":
			return 2
		case l == "":
			return 3
		default:
			return 4
		}
	}
	out := append([]imageInfo(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].VoteAverage > out[j].VoteAverage
	})
	return out
}

// convertImages turns TMDB image lists into metadata images (original size).
func convertImages(base, lang string, set imageSet, posterPath, backdropPath string) []metadata.Image {
	var out []metadata.Image
	pref := lang2(lang)
	add := func(typ string, list []imageInfo, textlessFirst bool, fallback string) {
		if len(list) == 0 && fallback != "" {
			out = append(out, metadata.Image{Type: typ, URL: base + "original" + fallback})
			return
		}
		for i, im := range sortImages(list, pref, textlessFirst) {
			if i >= maxImagesPerType {
				break
			}
			out = append(out, metadata.Image{Type: typ, URL: base + "original" + im.FilePath, Lang: langOf(im), Width: im.Width, Height: im.Height})
		}
	}
	add(metadata.ImagePoster, set.Posters, false, posterPath)
	add(metadata.ImageBackdrop, set.Backdrops, true, backdropPath)
	add(metadata.ImageLogo, set.Logos, false, "")
	return out
}

// people lists creators, then cast by billing order, then key crew.
func people(base string, c credits, creators []creator) []metadata.Person {
	img := func(path string) string {
		if path == "" {
			return ""
		}
		return base + "h632" + path
	}
	var out []metadata.Person
	for _, cr := range creators {
		out = append(out, metadata.Person{Name: cr.Name, Role: metadata.RoleCreator, ImageURL: img(cr.ProfilePath), Order: len(out)})
	}
	cast := append([]castMember(nil), c.Cast...)
	sort.SliceStable(cast, func(i, j int) bool { return cast[i].Order < cast[j].Order })
	for i, m := range cast {
		if i >= maxCast {
			break
		}
		out = append(out, metadata.Person{Name: m.Name, Role: metadata.RoleActor, Character: m.Character, ImageURL: img(m.ProfilePath), Order: len(out)})
	}
	seen := map[string]bool{}
	for _, m := range c.Crew {
		role := ""
		switch {
		case m.Job == "Director":
			role = metadata.RoleDirector
		case m.Department == "Writing":
			role = metadata.RoleWriter
		case m.Job == "Producer":
			role = metadata.RoleProducer
		}
		if role == "" || seen[role+"|"+m.Name] {
			continue
		}
		seen[role+"|"+m.Name] = true
		out = append(out, metadata.Person{Name: m.Name, Role: role, ImageURL: img(m.ProfilePath), Order: len(out)})
	}
	return out
}
```

- [ ] **Step 5: Implement movies**

`server/internal/metadata/tmdb/movie.go`:
```go
package tmdb

import (
	"context"
	"net/url"
	"strconv"

	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

type releaseCountry struct {
	Country      string `json:"iso_3166_1"`
	ReleaseDates []struct {
		Certification string `json:"certification"`
		Type          int    `json:"type"`
	} `json:"release_dates"`
}

type movieDetails struct {
	ID                  int      `json:"id"`
	IMDbID              string   `json:"imdb_id"`
	Title               string   `json:"title"`
	OriginalTitle       string   `json:"original_title"`
	Overview            string   `json:"overview"`
	ReleaseDate         string   `json:"release_date"`
	VoteAverage         float64  `json:"vote_average"`
	VoteCount           int      `json:"vote_count"`
	PosterPath          string   `json:"poster_path"`
	BackdropPath        string   `json:"backdrop_path"`
	Genres              []named  `json:"genres"`
	ProductionCompanies []named  `json:"production_companies"`
	Credits             credits  `json:"credits"`
	Images              imageSet `json:"images"`
	ReleaseDates        struct {
		Results []releaseCountry `json:"results"`
	} `json:"release_dates"`
	ExternalIDs struct {
		IMDbID string `json:"imdb_id"`
	} `json:"external_ids"`
	Keywords struct {
		Keywords []named `json:"keywords"`
	} `json:"keywords"`
}

func (p *Provider) movie(ctx context.Context, st metadata.Settings, id string) (metadata.Record, error) {
	var d movieDetails
	params := url.Values{
		"language":               {st.Language},
		"append_to_response":     {"credits,images,release_dates,external_ids,keywords"},
		"include_image_language": {imageLangs(st.Language)},
	}
	if err := p.get(ctx, st, "/movie/"+id, params, &d); err != nil {
		return metadata.Record{}, err
	}
	if needsFallback(st, d.Title, d.Overview) {
		var fb struct {
			Title    string `json:"title"`
			Overview string `json:"overview"`
		}
		if err := p.get(ctx, st, "/movie/"+id, url.Values{"language": {st.FallbackLanguage}}, &fb); err == nil {
			if d.Title == "" {
				d.Title = fb.Title
			}
			if d.Overview == "" {
				d.Overview = fb.Overview
			}
		}
	}
	base := p.imageBaseURL(ctx, st)
	rec := metadata.Record{
		Provider:      "tmdb",
		IDs:           map[string]string{"tmdb": strconv.Itoa(d.ID)},
		Title:         d.Title,
		OriginalTitle: d.OriginalTitle,
		Overview:      d.Overview,
		Year:          yearOf(d.ReleaseDate),
		Released:      d.ReleaseDate,
		Genres:        names(d.Genres),
		Studios:       names(d.ProductionCompanies),
		Tags:          names(d.Keywords.Keywords),
		Ratings:       map[string]float64{},
		People:        people(base, d.Credits, nil),
		Images:        convertImages(base, st.Language, d.Images, d.PosterPath, d.BackdropPath),
		ContentRating: movieCertification(d.ReleaseDates.Results, st.CertCountries),
	}
	imdb := d.IMDbID
	if imdb == "" {
		imdb = d.ExternalIDs.IMDbID
	}
	if imdb != "" {
		rec.IDs["imdb"] = imdb
	}
	if d.VoteCount > 0 {
		rec.Ratings["tmdb"] = d.VoteAverage
	}
	return rec, nil
}

// movieCertification picks the first country (in preference order) with a
// certification, preferring the theatrical release (type 3).
func movieCertification(results []releaseCountry, countries []string) string {
	for _, c := range countries {
		for _, r := range results {
			if r.Country != c {
				continue
			}
			for _, rd := range r.ReleaseDates {
				if rd.Type == 3 && rd.Certification != "" {
					return rd.Certification
				}
			}
			for _, rd := range r.ReleaseDates {
				if rd.Certification != "" {
					return rd.Certification
				}
			}
		}
	}
	return ""
}
```

- [ ] **Step 6: Implement TV shows**

`server/internal/metadata/tmdb/tv.go`:
```go
package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"

	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

var placeholderEpisode = regexp.MustCompile(`^Episode \d+$`)

type seasonSummary struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path"`
}

type tvDetails struct {
	ID                  int             `json:"id"`
	Name                string          `json:"name"`
	OriginalName        string          `json:"original_name"`
	Overview            string          `json:"overview"`
	FirstAirDate        string          `json:"first_air_date"`
	VoteAverage         float64         `json:"vote_average"`
	VoteCount           int             `json:"vote_count"`
	PosterPath          string          `json:"poster_path"`
	BackdropPath        string          `json:"backdrop_path"`
	Genres              []named         `json:"genres"`
	Networks            []named         `json:"networks"`
	ProductionCompanies []named         `json:"production_companies"`
	CreatedBy           []creator       `json:"created_by"`
	Seasons             []seasonSummary `json:"seasons"`
	Credits             credits         `json:"credits"`
	Images              imageSet        `json:"images"`
	ContentRatings      struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	ExternalIDs struct {
		IMDbID string `json:"imdb_id"`
		TVDBID int    `json:"tvdb_id"`
	} `json:"external_ids"`
	Keywords struct {
		Results []named `json:"results"`
	} `json:"keywords"`
}

type episode struct {
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	StillPath     string `json:"still_path"`
}

type seasonDetails struct {
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []episode `json:"episodes"`
}

func (p *Provider) show(ctx context.Context, st metadata.Settings, id string) (metadata.Record, error) {
	var d tvDetails
	params := url.Values{
		"language":               {st.Language},
		"append_to_response":     {"credits,images,content_ratings,external_ids,keywords"},
		"include_image_language": {imageLangs(st.Language)},
	}
	if err := p.get(ctx, st, "/tv/"+id, params, &d); err != nil {
		return metadata.Record{}, err
	}
	if needsFallback(st, d.Name, d.Overview) {
		var fb struct {
			Name     string `json:"name"`
			Overview string `json:"overview"`
		}
		if err := p.get(ctx, st, "/tv/"+id, url.Values{"language": {st.FallbackLanguage}}, &fb); err == nil {
			if d.Name == "" {
				d.Name = fb.Name
			}
			if d.Overview == "" {
				d.Overview = fb.Overview
			}
		}
	}
	base := p.imageBaseURL(ctx, st)
	studios := append(names(d.Networks), names(d.ProductionCompanies)...)
	rec := metadata.Record{
		Provider:      "tmdb",
		IDs:           map[string]string{"tmdb": strconv.Itoa(d.ID)},
		Title:         d.Name,
		OriginalTitle: d.OriginalName,
		Overview:      d.Overview,
		Year:          yearOf(d.FirstAirDate),
		Released:      d.FirstAirDate,
		Genres:        names(d.Genres),
		Studios:       studios,
		Tags:          names(d.Keywords.Results),
		Ratings:       map[string]float64{},
		People:        people(base, d.Credits, d.CreatedBy),
		Images:        convertImages(base, st.Language, d.Images, d.PosterPath, d.BackdropPath),
	}
	if d.ExternalIDs.IMDbID != "" {
		rec.IDs["imdb"] = d.ExternalIDs.IMDbID
	}
	if d.ExternalIDs.TVDBID > 0 {
		rec.IDs["tvdb"] = strconv.Itoa(d.ExternalIDs.TVDBID)
	}
	if d.VoteCount > 0 {
		rec.Ratings["tmdb"] = d.VoteAverage
	}
countries:
	for _, c := range st.CertCountries {
		for _, r := range d.ContentRatings.Results {
			if r.Country == c && r.Rating != "" {
				rec.ContentRating = r.Rating
				break countries
			}
		}
	}
	for _, s := range d.Seasons {
		sr, err := p.season(ctx, st, id, s.SeasonNumber, base)
		if errors.Is(err, metadata.ErrNotFound) {
			sr = metadata.SeasonRecord{Number: s.SeasonNumber, Title: s.Name, Overview: s.Overview}
			if s.PosterPath != "" {
				sr.Images = []metadata.Image{{Type: metadata.ImagePoster, URL: base + "original" + s.PosterPath}}
			}
		} else if err != nil {
			return metadata.Record{}, err
		}
		rec.Seasons = append(rec.Seasons, sr)
	}
	return rec, nil
}

func (p *Provider) season(ctx context.Context, st metadata.Settings, id string, n int, base string) (metadata.SeasonRecord, error) {
	path := fmt.Sprintf("/tv/%s/season/%d", id, n)
	var sd seasonDetails
	if err := p.get(ctx, st, path, url.Values{"language": {st.Language}}, &sd); err != nil {
		return metadata.SeasonRecord{}, err
	}
	if st.FallbackLanguage != "" && st.FallbackLanguage != st.Language && seasonNeedsFallback(sd) {
		var fb seasonDetails
		if err := p.get(ctx, st, path, url.Values{"language": {st.FallbackLanguage}}, &fb); err == nil {
			fillSeason(&sd, fb)
		}
	}
	sr := metadata.SeasonRecord{Number: n, Title: sd.Name, Overview: sd.Overview}
	if sd.PosterPath != "" {
		sr.Images = []metadata.Image{{Type: metadata.ImagePoster, URL: base + "original" + sd.PosterPath}}
	}
	for _, e := range sd.Episodes {
		er := metadata.EpisodeRecord{Season: e.SeasonNumber, Number: e.EpisodeNumber, Title: e.Name, Overview: e.Overview, AirDate: e.AirDate}
		if er.Season == 0 {
			er.Season = n
		}
		if e.StillPath != "" {
			er.Images = []metadata.Image{{Type: metadata.ImageThumb, URL: base + "original" + e.StillPath}}
		}
		sr.Episodes = append(sr.Episodes, er)
	}
	return sr, nil
}

func seasonNeedsFallback(sd seasonDetails) bool {
	if sd.Overview == "" {
		return true
	}
	for _, e := range sd.Episodes {
		if e.Overview == "" || e.Name == "" || placeholderEpisode.MatchString(e.Name) {
			return true
		}
	}
	return false
}

func fillSeason(sd *seasonDetails, fb seasonDetails) {
	if sd.Name == "" {
		sd.Name = fb.Name
	}
	if sd.Overview == "" {
		sd.Overview = fb.Overview
	}
	byNum := map[int]episode{}
	for _, e := range fb.Episodes {
		byNum[e.EpisodeNumber] = e
	}
	for i := range sd.Episodes {
		e := &sd.Episodes[i]
		f, ok := byNum[e.EpisodeNumber]
		if !ok {
			continue
		}
		if (e.Name == "" || placeholderEpisode.MatchString(e.Name)) && f.Name != "" {
			e.Name = f.Name
		}
		if e.Overview == "" {
			e.Overview = f.Overview
		}
	}
}
```

- [ ] **Step 7: Run tests, expect PASS**

Run: `make test`
Expected: `ok  .../internal/metadata/tmdb`. The live test is not compiled without the tag.

Optional live check (needs a real key; never in CI):
```bash
docker run --rm -v "$PWD/server":/src -w /src -v lisimflix-gomod:/go/pkg/mod -e CGO_ENABLED=0 \
  -e LISIMFLIX_TMDB_API_KEY golang:1.27-trixie go test -tags live -run Live -v ./internal/metadata/tmdb/
```
Expected: `--- PASS: TestLiveTMDB` (or `SKIP` when the variable is unset).

- [ ] **Step 8: Commit**

```bash
git add server/internal/metadata/tmdb
git commit -m "feat(metadata): TMDB provider for movies and TV with fixtures"
```

---

### Task 9: Fanart.tv provider

**Files:**
- Create: `server/internal/metadata/fanart/fanart.go`
- Create fixtures: `server/internal/metadata/fanart/testdata/movies_603.json`, `server/internal/metadata/fanart/testdata/tv_81189.json`
- Test: `server/internal/metadata/fanart/fanart_test.go`

**Interfaces:**
- Consumes: `metadata.Provider`, `Record`, `Image`, `SeasonRecord`, `Settings`, `Fetcher`, errors, `Kind*`, `Lib*`, `Image*`.
- Produces: `fanart.DefaultBaseURL = "https://webservice.fanart.tv/v3"`; `type Options struct{ BaseURL string; Settings func(context.Context) (metadata.Settings, error); Fetcher *metadata.Fetcher }`; `fanart.New(o) *Provider` (name `"fanart"`). `Search` returns nothing (Fanart.tv has no search). `Fetch`: movies by `ids["tmdb"]` (or `ids["imdb"]`) at `GET /movies/{id}?api_key=`, shows by `ids["tvdb"]` at `GET /tv/{id}?api_key=`; no usable ID → `ErrNoUsableID`; no key → `ErrNotConfigured`; 404 → `ErrNotFound`.

Fanart.tv v3 response keys: movies `hdmovielogo`, `movielogo`, `hdmovieclearart`, `movieart`,
`movieposter`, `moviebackground`, `moviebanner`, `moviethumb`; TV `hdtvlogo`, `clearlogo`,
`hdclearart`, `clearart`, `tvposter`, `showbackground`, `tvbanner`, `tvthumb`, `seasonposter`
(with `season`). Each entry: `{id, url, lang, likes}` (`likes` is a string; `lang` `""`/`"00"` =
textless). Mapping: logos (HD then SD) → `logo`; clear art (HD then SD) → `clearart`; posters →
`poster`; backgrounds → `backdrop`; banners → `banner`; thumbs → `thumb`; season posters →
`Record.Seasons[n].Images`. Order within a list: preferred language, `en`, textless, others,
then most likes; backgrounds put textless first. Fanart.tv does not report sizes, so the
documented fixed sizes are used (HD logo 800×310, logo 400×155, HD clear art 1000×562, clear art
500×281, poster 1000×1426, background 1920×1080, banner 1000×185, movie thumb 1000×562, TV thumb 500×281).

- [ ] **Step 1: Write the fixtures**

`server/internal/metadata/fanart/testdata/movies_603.json`:
```json
{
  "name": "The Matrix",
  "tmdb_id": "603",
  "imdb_id": "tt0133093",
  "hdmovielogo": [
    {"id": "61340", "url": "https://assets.fanart.tv/fanart/movies/603/hdmovielogo/the-matrix-574c9ad8c4a3a.png", "lang": "de", "likes": "3"},
    {"id": "9853", "url": "https://assets.fanart.tv/fanart/movies/603/hdmovielogo/the-matrix-5160d50e6e6a6.png", "lang": "en", "likes": "11"}
  ],
  "movielogo": [
    {"id": "1112", "url": "https://assets.fanart.tv/fanart/movies/603/movielogo/the-matrix-4f1b6a0a0d7d1.png", "lang": "en", "likes": "2"}
  ],
  "hdmovieclearart": [
    {"id": "9854", "url": "https://assets.fanart.tv/fanart/movies/603/hdmovieclearart/the-matrix-5160d5f4e9a0b.png", "lang": "en", "likes": "8"}
  ],
  "movieposter": [
    {"id": "20341", "url": "https://assets.fanart.tv/fanart/movies/603/movieposter/the-matrix-52b4ae6a41b4e.jpg", "lang": "en", "likes": "6"}
  ],
  "moviebackground": [
    {"id": "1201", "url": "https://assets.fanart.tv/fanart/movies/603/moviebackground/the-matrix-4fe4e4d5b0b0e.jpg", "lang": "", "likes": "9"},
    {"id": "1202", "url": "https://assets.fanart.tv/fanart/movies/603/moviebackground/the-matrix-4fe4e4f6c1c1f.jpg", "lang": "", "likes": "14"},
    {"id": "1203", "url": "https://assets.fanart.tv/fanart/movies/603/moviebackground/the-matrix-50f1a2b3c4d5e.jpg", "lang": "en", "likes": "20"}
  ],
  "moviebanner": [
    {"id": "5001", "url": "https://assets.fanart.tv/fanart/movies/603/moviebanner/the-matrix-51d4a9c3b8f19.jpg", "lang": "en", "likes": "4"}
  ],
  "moviethumb": [
    {"id": "6001", "url": "https://assets.fanart.tv/fanart/movies/603/moviethumb/the-matrix-5214b1c1e4d6a.jpg", "lang": "en", "likes": "5"}
  ]
}
```

`server/internal/metadata/fanart/testdata/tv_81189.json`:
```json
{
  "name": "Breaking Bad",
  "thetvdb_id": "81189",
  "hdtvlogo": [
    {"id": "16346", "url": "https://assets.fanart.tv/fanart/tv/81189/hdtvlogo/breaking-bad-4fe3e9b3a6e8d.png", "lang": "en", "likes": "10"}
  ],
  "clearlogo": [
    {"id": "3207", "url": "https://assets.fanart.tv/fanart/tv/81189/clearlogo/breaking-bad-4dbd7a8b4c1d6.png", "lang": "en", "likes": "4"}
  ],
  "hdclearart": [
    {"id": "16349", "url": "https://assets.fanart.tv/fanart/tv/81189/hdclearart/breaking-bad-50afb6c4be9d1.png", "lang": "en", "likes": "7"}
  ],
  "showbackground": [
    {"id": "21000", "url": "https://assets.fanart.tv/fanart/tv/81189/showbackground/breaking-bad-4fd5f6ec8d3e2.jpg", "lang": "", "likes": "6", "season": "all"}
  ],
  "tvposter": [
    {"id": "30001", "url": "https://assets.fanart.tv/fanart/tv/81189/tvposter/breaking-bad-5223b4f9b2d4c.jpg", "lang": "en", "likes": "5"}
  ],
  "tvbanner": [
    {"id": "31001", "url": "https://assets.fanart.tv/fanart/tv/81189/tvbanner/breaking-bad-4fd5f8a9b3c2d.jpg", "lang": "en", "likes": "3"}
  ],
  "tvthumb": [
    {"id": "32001", "url": "https://assets.fanart.tv/fanart/tv/81189/tvthumb/breaking-bad-5203a1b2c3d4e.jpg", "lang": "en", "likes": "2"}
  ],
  "seasonposter": [
    {"id": "40001", "url": "https://assets.fanart.tv/fanart/tv/81189/seasonposter/breaking-bad-5223b6a1c2f3d.jpg", "lang": "en", "likes": "3", "season": "1"},
    {"id": "40002", "url": "https://assets.fanart.tv/fanart/tv/81189/seasonposter/breaking-bad-5223b6b2d3e4f.jpg", "lang": "en", "likes": "2", "season": "2"},
    {"id": "40003", "url": "https://assets.fanart.tv/fanart/tv/81189/seasonposter/breaking-bad-5223b6c3e4f5a.jpg", "lang": "en", "likes": "1", "season": "all"}
  ]
}
```

- [ ] **Step 2: Write the failing tests**

`server/internal/metadata/fanart/fanart_test.go`:
```go
package fanart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

func newProvider(t *testing.T, key string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "fanart-key" {
			t.Errorf("api_key missing: %s", r.URL.RawQuery)
		}
		name := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/v3/"), "/", "_") + ".json"
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"status":"error","error message":"Not found"}`))
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return New(Options{BaseURL: srv.URL + "/v3",
		Settings: func(context.Context) (metadata.Settings, error) {
			st := metadata.DefaultSettings()
			st.FanartKey = key
			return st, nil
		},
		Fetcher: metadata.NewFetcher(nil, "fanart", metadata.FetcherOptions{RatePerSec: 1000, Burst: 1000, MaxRetries: -1})})
}

func urls(imgs []metadata.Image, typ string) []string {
	var out []string
	for _, im := range imgs {
		if im.Type == typ {
			out = append(out, im.URL)
		}
	}
	return out
}

func TestFetchMovie(t *testing.T) {
	p := newProvider(t, "fanart-key")
	rec, err := p.Fetch(context.Background(), metadata.KindMovie, map[string]string{"tmdb": "603"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Provider != "fanart" || rec.IDs["tmdb"] != "603" || rec.IDs["imdb"] != "tt0133093" {
		t.Fatalf("%+v", rec)
	}
	logos := urls(rec.Images, metadata.ImageLogo)
	if len(logos) != 3 || !strings.Contains(logos[0], "5160d50e6e6a6") || !strings.Contains(logos[1], "574c9ad8c4a3a") || !strings.Contains(logos[2], "/movielogo/") {
		t.Fatalf("logo order (HD en, HD de, SD) wrong: %v", logos)
	}
	bgs := urls(rec.Images, metadata.ImageBackdrop)
	if len(bgs) != 3 || !strings.Contains(bgs[0], "4fe4e4f6c1c1f") || !strings.Contains(bgs[2], "50f1a2b3c4d5e") {
		t.Fatalf("backgrounds must be textless first, then likes: %v", bgs)
	}
	for _, typ := range []string{metadata.ImageClearArt, metadata.ImagePoster, metadata.ImageBanner, metadata.ImageThumb} {
		if len(urls(rec.Images, typ)) != 1 {
			t.Errorf("%s missing", typ)
		}
	}
	for _, im := range rec.Images {
		if im.Type == metadata.ImageLogo && strings.Contains(im.URL, "hdmovielogo") && (im.Width != 800 || im.Height != 310) {
			t.Fatalf("hd logo size %dx%d", im.Width, im.Height)
		}
	}
}

func TestFetchShowSeasonPosters(t *testing.T) {
	p := newProvider(t, "fanart-key")
	rec, err := p.Fetch(context.Background(), metadata.KindShow, map[string]string{"tvdb": "81189", "tmdb": "1396"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.IDs["tvdb"] != "81189" || len(urls(rec.Images, metadata.ImageLogo)) != 2 {
		t.Fatalf("%+v", rec)
	}
	if len(rec.Seasons) != 2 || rec.Seasons[0].Number != 1 || rec.Seasons[1].Number != 2 || len(rec.Seasons[0].Images) != 1 {
		t.Fatalf("seasons %+v", rec.Seasons)
	}
}

func TestFetchErrors(t *testing.T) {
	p := newProvider(t, "fanart-key")
	ctx := context.Background()
	if _, err := p.Fetch(ctx, metadata.KindShow, map[string]string{"tmdb": "1396"}); !errors.Is(err, metadata.ErrNoUsableID) {
		t.Fatalf("show without tvdb: %v", err)
	}
	if _, err := p.Fetch(ctx, metadata.KindMovie, map[string]string{"tmdb": "1"}); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("unknown movie: %v", err)
	}
	if _, err := newProvider(t, "").Fetch(ctx, metadata.KindMovie, map[string]string{"tmdb": "603"}); !errors.Is(err, metadata.ErrNotConfigured) {
		t.Fatalf("no key: %v", err)
	}
	if c, err := p.Search(ctx, metadata.Query{Title: "x"}); c != nil || err != nil {
		t.Fatal("search must be empty")
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: New` in `internal/metadata/fanart`

- [ ] **Step 4: Implement**

`server/internal/metadata/fanart/fanart.go`:
```go
// Package fanart is the Fanart.tv v3 artwork provider (logos, clear art,
// backgrounds, posters, banners, thumbs and season posters).
package fanart

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

// DefaultBaseURL is the Fanart.tv v3 API root.
const DefaultBaseURL = "https://webservice.fanart.tv/v3"

const maxPerType = 10

var (
	digitsRe = regexp.MustCompile(`^\d+$`)
	imdbRe   = regexp.MustCompile(`^tt\d{5,10}$`)
)

// Options configures the provider.
type Options struct {
	BaseURL  string
	Settings func(ctx context.Context) (metadata.Settings, error)
	Fetcher  *metadata.Fetcher
}

// Provider implements metadata.Provider for Fanart.tv.
type Provider struct{ o Options }

var _ metadata.Provider = (*Provider)(nil)

// New returns a Fanart.tv provider.
func New(o Options) *Provider {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.Fetcher == nil {
		o.Fetcher = metadata.NewFetcher(nil, "fanart", metadata.FetcherOptions{})
	}
	return &Provider{o: o}
}

// Name implements metadata.Provider.
func (p *Provider) Name() string { return "fanart" }

// Supports implements metadata.Provider.
func (p *Provider) Supports(kind media.Kind, lt library.Type) bool {
	if lt == metadata.LibHomeVideos {
		return false
	}
	return kind == metadata.KindMovie || kind == metadata.KindShow
}

// Search implements metadata.Provider. Fanart.tv has no search.
func (p *Provider) Search(context.Context, metadata.Query) ([]metadata.Candidate, error) {
	return nil, nil
}

type art struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Lang   string `json:"lang"`
	Likes  string `json:"likes"`
	Season string `json:"season"`
}

type movieResp struct {
	Name       string `json:"name"`
	TMDBID     string `json:"tmdb_id"`
	IMDbID     string `json:"imdb_id"`
	HDLogo     []art  `json:"hdmovielogo"`
	Logo       []art  `json:"movielogo"`
	HDClearArt []art  `json:"hdmovieclearart"`
	ClearArt   []art  `json:"movieart"`
	Poster     []art  `json:"movieposter"`
	Background []art  `json:"moviebackground"`
	Banner     []art  `json:"moviebanner"`
	Thumb      []art  `json:"moviethumb"`
}

type tvResp struct {
	Name         string `json:"name"`
	TVDBID       string `json:"thetvdb_id"`
	HDLogo       []art  `json:"hdtvlogo"`
	Logo         []art  `json:"clearlogo"`
	HDClearArt   []art  `json:"hdclearart"`
	ClearArt     []art  `json:"clearart"`
	Poster       []art  `json:"tvposter"`
	Background   []art  `json:"showbackground"`
	Banner       []art  `json:"tvbanner"`
	Thumb        []art  `json:"tvthumb"`
	SeasonPoster []art  `json:"seasonposter"`
}

type size struct{ w, h int }

var (
	sizeHDLogo     = size{800, 310}
	sizeLogo       = size{400, 155}
	sizeHDClearArt = size{1000, 562}
	sizeClearArt   = size{500, 281}
	sizePoster     = size{1000, 1426}
	sizeBackground = size{1920, 1080}
	sizeBanner     = size{1000, 185}
	sizeMovieThumb = size{1000, 562}
	sizeTVThumb    = size{500, 281}
)

// Fetch implements metadata.Provider.
func (p *Provider) Fetch(ctx context.Context, kind media.Kind, ids map[string]string) (metadata.Record, error) {
	st, err := p.o.Settings(ctx)
	if err != nil {
		return metadata.Record{}, err
	}
	var path string
	switch kind {
	case metadata.KindMovie:
		id := ids["tmdb"]
		if !digitsRe.MatchString(id) {
			id = ids["imdb"]
			if !imdbRe.MatchString(id) {
				return metadata.Record{}, metadata.ErrNoUsableID
			}
		}
		path = "/movies/" + id
	case metadata.KindShow:
		id := ids["tvdb"]
		if !digitsRe.MatchString(id) {
			return metadata.Record{}, metadata.ErrNoUsableID
		}
		path = "/tv/" + id
	default:
		return metadata.Record{}, metadata.ErrNoUsableID
	}
	if st.FanartKey == "" {
		return metadata.Record{}, fmt.Errorf("fanart: %w", metadata.ErrNotConfigured)
	}
	u := p.o.BaseURL + path + "?api_key=" + url.QueryEscape(st.FanartKey)
	pref := st.Language
	if i := strings.IndexByte(pref, '-'); i > 0 {
		pref = pref[:i]
	}
	rec := metadata.Record{Provider: "fanart", IDs: map[string]string{}}
	add := func(typ string, list []art, sz size, textlessFirst bool) {
		rec.Images = append(rec.Images, toImages(typ, list, sz, pref, textlessFirst)...)
	}
	if kind == metadata.KindMovie {
		var r movieResp
		if err := p.o.Fetcher.GetJSON(ctx, u, path, nil, &r); err != nil {
			return metadata.Record{}, err
		}
		rec.Title = r.Name
		setID(rec.IDs, "tmdb", r.TMDBID)
		setID(rec.IDs, "imdb", r.IMDbID)
		add(metadata.ImageLogo, r.HDLogo, sizeHDLogo, false)
		add(metadata.ImageLogo, r.Logo, sizeLogo, false)
		add(metadata.ImageClearArt, r.HDClearArt, sizeHDClearArt, false)
		add(metadata.ImageClearArt, r.ClearArt, sizeClearArt, false)
		add(metadata.ImagePoster, r.Poster, sizePoster, false)
		add(metadata.ImageBackdrop, r.Background, sizeBackground, true)
		add(metadata.ImageBanner, r.Banner, sizeBanner, false)
		add(metadata.ImageThumb, r.Thumb, sizeMovieThumb, false)
		return rec, nil
	}
	var r tvResp
	if err := p.o.Fetcher.GetJSON(ctx, u, path, nil, &r); err != nil {
		return metadata.Record{}, err
	}
	rec.Title = r.Name
	setID(rec.IDs, "tvdb", r.TVDBID)
	add(metadata.ImageLogo, r.HDLogo, sizeHDLogo, false)
	add(metadata.ImageLogo, r.Logo, sizeLogo, false)
	add(metadata.ImageClearArt, r.HDClearArt, sizeHDClearArt, false)
	add(metadata.ImageClearArt, r.ClearArt, sizeClearArt, false)
	add(metadata.ImagePoster, r.Poster, sizePoster, false)
	add(metadata.ImageBackdrop, r.Background, sizeBackground, true)
	add(metadata.ImageBanner, r.Banner, sizeBanner, false)
	add(metadata.ImageThumb, r.Thumb, sizeTVThumb, false)
	rec.Seasons = seasonPosters(r.SeasonPoster, pref)
	return rec, nil
}

func setID(ids map[string]string, k, v string) {
	if v != "" {
		ids[k] = v
	}
}

func textless(a art) bool { return a.Lang == "" || a.Lang == "00" }

func sortArt(list []art, pref string, textlessFirst bool) []art {
	rank := func(a art) int {
		switch {
		case textlessFirst && textless(a):
			return 0
		case a.Lang == pref:
			return 1
		case a.Lang == "en":
			return 2
		case textless(a):
			return 3
		default:
			return 4
		}
	}
	likes := func(a art) int { n, _ := strconv.Atoi(a.Likes); return n }
	out := append([]art(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return likes(out[i]) > likes(out[j])
	})
	return out
}

func toImages(typ string, list []art, sz size, pref string, textlessFirst bool) []metadata.Image {
	var out []metadata.Image
	for _, a := range sortArt(list, pref, textlessFirst) {
		if a.URL == "" {
			continue
		}
		lang := a.Lang
		if lang == "00" {
			lang = ""
		}
		out = append(out, metadata.Image{Type: typ, URL: a.URL, Lang: lang, Width: sz.w, Height: sz.h})
		if len(out) == maxPerType {
			break
		}
	}
	return out
}

func seasonPosters(list []art, pref string) []metadata.SeasonRecord {
	by := map[int][]art{}
	for _, a := range list {
		n, err := strconv.Atoi(a.Season)
		if err != nil {
			continue // "all"
		}
		by[n] = append(by[n], a)
	}
	nums := make([]int, 0, len(by))
	for n := range by {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var out []metadata.SeasonRecord
	for _, n := range nums {
		out = append(out, metadata.SeasonRecord{Number: n, Images: toImages(metadata.ImagePoster, by[n], sizePoster, pref, false)})
	}
	return out
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/metadata/fanart
git commit -m "feat(metadata): Fanart.tv artwork provider"
```

---

### Task 10: NFO provider (Kodi movie.nfo, tvshow.nfo, episode .nfo, local artwork)

**Files:**
- Create: `server/internal/metadata/nfo/nfo.go`
- Create fixtures: `server/internal/metadata/nfo/testdata/movie.nfo`, `tvshow.nfo`, `episode.nfo`, `url_only.nfo`
- Test: `server/internal/metadata/nfo/nfo_test.go`

**Interfaces:**
- Consumes: `metadata.Provider`, `LocalEpisodeReader`, `Record`, `EpisodeRecord`, `Person`, `Image`, `LocalProviderName`, `LocalPathKey`, errors, `Kind*`, `Image*`, `Role*`. `golang.org/x/text/encoding/charmap` (already a dependency).
- Produces: `nfo.New() *Provider` (name `"nfo"`, supports `movie` and `show` in every library type); `Fetch(ctx, kind, ids)` reads `ids[metadata.LocalPathKey]`: for a movie the video file path (tries `<base>.nfo` then `movie.nfo` in its folder), for a show the show folder (`tvshow.nfo`); also returns local artwork files as `file://` images; nothing found → `ErrNotFound`; no path → `ErrNoUsableID`; `Search` returns nothing; `ReadEpisode(videoPath) (metadata.EpisodeRecord, bool, error)` reads `<video base>.nfo` (`<episodedetails>`).

NFO mapping (Kodi v19+ format): `title`, `originaltitle`, `sorttitle`, `year` (or year of
`premiered`), `premiered`/`aired` → Released, `plot` (or `outline`) → Overview, `mpaa`
(`"Rated R"`, `"US:R"`, `"Australia:M"` → `"R"`/`"M"`) → ContentRating, `genre` (also split on
`" / "`), `studio`, `tag`, `uniqueid type=…` (plus legacy `id`, `tmdbid`, `imdbid`, `tvdbid`) →
IDs, `ratings/rating name=… max=…` → Ratings scaled to 0..10 (`themoviedb` → `tmdb`,
`tomatometerallcritics` → `rt`), `actor` → actors, `director` → directors, `credits` → writers,
`thumb aspect=poster|banner|clearlogo|clearart|landscape` and `fanart/thumb` (http(s) only).
Non-XML NFOs that only contain links yield IDs (IMDb `tt…`, `themoviedb.org/movie|tv/<id>`).
XML after the root element (Kodi allows a trailing URL) is ignored. Legacy ISO-8859-1 /
Windows-1252 files are decoded. Local artwork names: `poster|folder|cover`, `fanart|backdrop|background`,
`clearlogo|logo`, `banner`, `clearart`, `landscape|thumb` with `.jpg/.jpeg/.png/.webp`; for movies
`<base>-<name>.<ext>` is tried first. Local images come before NFO URLs.

- [ ] **Step 1: Write the fixtures**

`server/internal/metadata/nfo/testdata/movie.nfo`:
```xml
<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>
<movie>
  <title>The Matrix</title>
  <originaltitle>The Matrix</originaltitle>
  <sorttitle>Matrix 1</sorttitle>
  <ratings>
    <rating name="imdb" max="10" default="true"><value>8.7</value><votes>2100000</votes></rating>
    <rating name="tomatometerallcritics" max="100"><value>83</value><votes>200</votes></rating>
  </ratings>
  <userrating>0</userrating>
  <plot>A hacker discovers reality is a simulation. (local NFO plot)</plot>
  <outline>Short outline.</outline>
  <tagline>Welcome to the Real World.</tagline>
  <runtime>136</runtime>
  <thumb aspect="poster">https://image.tmdb.org/t/p/original/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg</thumb>
  <thumb aspect="clearlogo">https://assets.fanart.tv/fanart/movies/603/hdmovielogo/the-matrix-5160d50e6e6a6.png</thumb>
  <fanart>
    <thumb>https://image.tmdb.org/t/p/original/ncEsesgOJDNrTUED89hYbA117wo.jpg</thumb>
  </fanart>
  <mpaa>Australia:M</mpaa>
  <uniqueid type="imdb">tt0133093</uniqueid>
  <uniqueid type="tmdb" default="true">603</uniqueid>
  <genre>Action</genre>
  <genre>Science Fiction</genre>
  <tag>cyberpunk</tag>
  <country>United States of America</country>
  <credits>Lana Wachowski</credits>
  <director>Lilly Wachowski</director>
  <premiered>1999-03-31</premiered>
  <year>1999</year>
  <studio>Warner Bros. Pictures</studio>
  <actor>
    <name>Keanu Reeves</name>
    <role>Neo</role>
    <order>0</order>
    <thumb>https://image.tmdb.org/t/p/h632/4D0PpNI0kmP58hgrwGC3wCjxhnm.jpg</thumb>
  </actor>
  <actor>
    <name>Laurence Fishburne</name>
    <role>Morpheus</role>
    <order>1</order>
  </actor>
</movie>
https://www.themoviedb.org/movie/603
```

`server/internal/metadata/nfo/testdata/tvshow.nfo`:
```xml
<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>
<tvshow>
  <title>Breaking Bad</title>
  <plot>A chemistry teacher turns to crime. (local NFO plot)</plot>
  <mpaa>TV-MA</mpaa>
  <premiered>2008-01-20</premiered>
  <genre>Drama / Crime</genre>
  <studio>AMC</studio>
  <id>81189</id>
  <uniqueid type="imdb">tt0903747</uniqueid>
  <actor>
    <name>Bryan Cranston</name>
    <role>Walter White</role>
    <order>0</order>
  </actor>
</tvshow>
```

`server/internal/metadata/nfo/testdata/episode.nfo`:
```xml
<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>
<episodedetails>
  <title>Pilot</title>
  <season>1</season>
  <episode>1</episode>
  <plot>Walter White makes a fateful decision. (local NFO plot)</plot>
  <aired>2008-01-20</aired>
  <uniqueid type="tvdb" default="true">349232</uniqueid>
  <thumb>https://artworks.thetvdb.com/banners/episodes/81189/349232.jpg</thumb>
</episodedetails>
```

`server/internal/metadata/nfo/testdata/url_only.nfo`:
```
https://www.imdb.com/title/tt0133093/
```

- [ ] **Step 2: Write the failing tests**

`server/internal/metadata/nfo/nfo_test.go`:
```go
package nfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

func copyFixture(t *testing.T, name, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMovieNFO(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "The Matrix (1999)")
	video := filepath.Join(dir, "The Matrix (1999).mkv")
	touch(t, video)
	copyFixture(t, "movie.nfo", filepath.Join(dir, "The Matrix (1999).nfo"))
	touch(t, filepath.Join(dir, "poster.jpg"))
	touch(t, filepath.Join(dir, "The Matrix (1999)-fanart.png"))

	rec, err := New().Fetch(context.Background(), metadata.KindMovie, map[string]string{metadata.LocalPathKey: video})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Provider != "nfo" || rec.Title != "The Matrix" || rec.SortTitle != "Matrix 1" || rec.Year != 1999 ||
		rec.Released != "1999-03-31" || rec.ContentRating != "M" || !strings.Contains(rec.Overview, "local NFO plot") {
		t.Fatalf("%+v", rec)
	}
	if rec.IDs["tmdb"] != "603" || rec.IDs["imdb"] != "tt0133093" {
		t.Fatalf("ids %v", rec.IDs)
	}
	if rec.Ratings["imdb"] != 8.7 || rec.Ratings["rt"] != 8.3 {
		t.Fatalf("ratings %v", rec.Ratings)
	}
	if strings.Join(rec.Genres, ",") != "Action,Science Fiction" || rec.Studios[0] != "Warner Bros. Pictures" || rec.Tags[0] != "cyberpunk" {
		t.Fatalf("lists %v %v %v", rec.Genres, rec.Studios, rec.Tags)
	}
	var people []string
	for _, p := range rec.People {
		people = append(people, p.Role+":"+p.Name+":"+p.Character)
	}
	if strings.Join(people, ",") != "actor:Keanu Reeves:Neo,actor:Laurence Fishburne:Morpheus,director:Lilly Wachowski:,writer:Lana Wachowski:" {
		t.Fatalf("people %v", people)
	}
	var imgs []string
	for _, im := range rec.Images {
		imgs = append(imgs, im.Type+"="+filepath.Base(im.URL))
	}
	want := "poster=poster.jpg,backdrop=The%20Matrix%20%281999%29-fanart.png,poster=f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg,logo=the-matrix-5160d50e6e6a6.png,backdrop=ncEsesgOJDNrTUED89hYbA117wo.jpg"
	if strings.Join(imgs, ",") != want {
		t.Fatalf("images\n got %s\nwant %s", strings.Join(imgs, ","), want)
	}
	if !strings.HasPrefix(rec.Images[0].URL, "file:///") {
		t.Fatalf("local image url %q", rec.Images[0].URL)
	}
}

func TestMovieNFOFallbackNameAndURLOnly(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "matrix.mkv")
	touch(t, video)
	copyFixture(t, "url_only.nfo", filepath.Join(dir, "movie.nfo"))
	rec, err := New().Fetch(context.Background(), metadata.KindMovie, map[string]string{metadata.LocalPathKey: video})
	if err != nil || rec.IDs["imdb"] != "tt0133093" || rec.Title != "" {
		t.Fatalf("rec %+v err %v", rec, err)
	}
}

func TestShowNFO(t *testing.T) {
	show := filepath.Join(t.TempDir(), "Breaking Bad")
	copyFixture(t, "tvshow.nfo", filepath.Join(show, "tvshow.nfo"))
	rec, err := New().Fetch(context.Background(), metadata.KindShow, map[string]string{metadata.LocalPathKey: show})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Title != "Breaking Bad" || rec.Year != 2008 || rec.IDs["tvdb"] != "81189" || rec.IDs["imdb"] != "tt0903747" {
		t.Fatalf("%+v", rec)
	}
	if strings.Join(rec.Genres, ",") != "Drama,Crime" {
		t.Fatalf("genres %v", rec.Genres)
	}
}

func TestReadEpisode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Breaking Bad", "Season 01")
	video := filepath.Join(dir, "Breaking Bad - S01E01 - Pilot.mkv")
	touch(t, video)
	p := New()
	if _, ok, err := p.ReadEpisode(video); ok || err != nil {
		t.Fatalf("no nfo: ok=%v err=%v", ok, err)
	}
	copyFixture(t, "episode.nfo", strings.TrimSuffix(video, ".mkv")+".nfo")
	ep, ok, err := p.ReadEpisode(video)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ep.Season != 1 || ep.Number != 1 || ep.Title != "Pilot" || ep.AirDate != "2008-01-20" || len(ep.Images) != 1 {
		t.Fatalf("%+v", ep)
	}
}

func TestFetchErrors(t *testing.T) {
	p := New()
	ctx := context.Background()
	if _, err := p.Fetch(ctx, metadata.KindMovie, map[string]string{}); !errors.Is(err, metadata.ErrNoUsableID) {
		t.Fatalf("no path: %v", err)
	}
	video := filepath.Join(t.TempDir(), "x.mkv")
	touch(t, video)
	if _, err := p.Fetch(ctx, metadata.KindMovie, map[string]string{metadata.LocalPathKey: video}); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("nothing local: %v", err)
	}
	copyFixture(t, "tvshow.nfo", strings.TrimSuffix(video, ".mkv")+".nfo")
	if _, err := p.Fetch(ctx, metadata.KindMovie, map[string]string{metadata.LocalPathKey: video}); err == nil {
		t.Fatal("a <tvshow> root in a movie nfo must be rejected")
	}
}

func TestLatin1NFO(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "amelie.mkv")
	touch(t, video)
	body := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<movie><title>Am\xe9lie</title></movie>")
	os.WriteFile(filepath.Join(dir, "amelie.nfo"), body, 0o644)
	rec, err := New().Fetch(context.Background(), metadata.KindMovie, map[string]string{metadata.LocalPathKey: video})
	if err != nil || rec.Title != "Amélie" {
		t.Fatalf("title %q err %v", rec.Title, err)
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: New` in `internal/metadata/nfo`

- [ ] **Step 4: Implement**

`server/internal/metadata/nfo/nfo.go`:
```go
// Package nfo reads Kodi-style .nfo files and local artwork next to media
// files. It only ever reads under the media root.
package nfo

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/charmap"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/metadata"
)

const maxNFOBytes = 1 << 20

var (
	imdbRe     = regexp.MustCompile(`^tt\d{5,10}$`)
	digitsRe   = regexp.MustCompile(`^\d+$`)
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	imdbInText = regexp.MustCompile(`tt\d{7,8}`)
	tmdbInText = regexp.MustCompile(`themoviedb\.org/(?:movie|tv)/(\d+)`)
)

// Provider implements metadata.Provider and metadata.LocalEpisodeReader.
type Provider struct{}

var (
	_ metadata.Provider           = (*Provider)(nil)
	_ metadata.LocalEpisodeReader = (*Provider)(nil)
)

// New returns the NFO provider.
func New() *Provider { return &Provider{} }

// Name implements metadata.Provider.
func (*Provider) Name() string { return metadata.LocalProviderName }

// Supports implements metadata.Provider.
func (*Provider) Supports(kind media.Kind, _ library.Type) bool {
	return kind == metadata.KindMovie || kind == metadata.KindShow
}

// Search implements metadata.Provider. Local data is read with Fetch.
func (*Provider) Search(context.Context, metadata.Query) ([]metadata.Candidate, error) {
	return nil, nil
}

// Fetch reads the NFO and local artwork for ids[metadata.LocalPathKey]: the
// video file of a movie, or the folder of a show.
func (p *Provider) Fetch(_ context.Context, kind media.Kind, ids map[string]string) (metadata.Record, error) {
	path := ids[metadata.LocalPathKey]
	if path == "" {
		return metadata.Record{}, metadata.ErrNoUsableID
	}
	var candidates []string
	var root, artDir, base string
	switch kind {
	case metadata.KindMovie:
		artDir = filepath.Dir(path)
		base = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		candidates = []string{filepath.Join(artDir, base+".nfo"), filepath.Join(artDir, "movie.nfo")}
		root = "movie"
	case metadata.KindShow:
		artDir = path
		candidates = []string{filepath.Join(path, "tvshow.nfo")}
		root = "tvshow"
	default:
		return metadata.Record{}, metadata.ErrNoUsableID
	}
	var rec metadata.Record
	found := false
	for _, c := range candidates {
		r, err := readNFO(c, root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return metadata.Record{}, err
		}
		rec, found = r, true
		break
	}
	local := localArtwork(artDir, base)
	if !found && len(local) == 0 {
		return metadata.Record{}, fmt.Errorf("nfo %s: %w", path, metadata.ErrNotFound)
	}
	rec.Provider = metadata.LocalProviderName
	if rec.IDs == nil {
		rec.IDs = map[string]string{}
	}
	rec.Images = append(local, rec.Images...)
	return rec, nil
}

// ReadEpisode implements metadata.LocalEpisodeReader for "<video base>.nfo".
func (p *Provider) ReadEpisode(videoPath string) (metadata.EpisodeRecord, bool, error) {
	path := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".nfo"
	b, err := readLimited(path)
	if errors.Is(err, os.ErrNotExist) {
		return metadata.EpisodeRecord{}, false, nil
	}
	if err != nil {
		return metadata.EpisodeRecord{}, false, err
	}
	doc, ok, err := parseXML(b)
	if err != nil {
		return metadata.EpisodeRecord{}, false, fmt.Errorf("nfo %s: %w", path, err)
	}
	if !ok || doc.XMLName.Local != "episodedetails" {
		return metadata.EpisodeRecord{}, false, nil
	}
	er := metadata.EpisodeRecord{
		Season:   atoi(doc.Season),
		Number:   atoi(doc.Episode),
		Title:    strings.TrimSpace(doc.Title),
		Overview: first(doc.Plot, doc.Outline),
		AirDate:  first(validDate(doc.Aired), validDate(doc.Premiered)),
	}
	for _, th := range doc.Thumbs {
		if u := strings.TrimSpace(th.Value); isHTTP(u) {
			er.Images = append(er.Images, metadata.Image{Type: metadata.ImageThumb, URL: u})
		}
	}
	return er, true, nil
}

type uniqueID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type ratingXML struct {
	Name  string `xml:"name,attr"`
	Max   string `xml:"max,attr"`
	Value string `xml:"value"`
}

type actorXML struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Order string `xml:"order"`
	Thumb string `xml:"thumb"`
}

type thumbXML struct {
	Aspect string `xml:"aspect,attr"`
	Value  string `xml:",chardata"`
}

type nfoDoc struct {
	XMLName       xml.Name
	Title         string      `xml:"title"`
	OriginalTitle string      `xml:"originaltitle"`
	SortTitle     string      `xml:"sorttitle"`
	Year          string      `xml:"year"`
	Premiered     string      `xml:"premiered"`
	Aired         string      `xml:"aired"`
	Plot          string      `xml:"plot"`
	Outline       string      `xml:"outline"`
	MPAA          string      `xml:"mpaa"`
	Genres        []string    `xml:"genre"`
	Studios       []string    `xml:"studio"`
	Tags          []string    `xml:"tag"`
	Directors     []string    `xml:"director"`
	Credits       []string    `xml:"credits"`
	UniqueIDs     []uniqueID  `xml:"uniqueid"`
	ID            string      `xml:"id"`
	TMDBID        string      `xml:"tmdbid"`
	IMDBID        string      `xml:"imdbid"`
	TVDBID        string      `xml:"tvdbid"`
	Ratings       []ratingXML `xml:"ratings>rating"`
	Actors        []actorXML  `xml:"actor"`
	Thumbs        []thumbXML  `xml:"thumb"`
	Fanart        []string    `xml:"fanart>thumb"`
	Season        string      `xml:"season"`
	Episode       string      `xml:"episode"`
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxNFOBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxNFOBytes {
		return nil, fmt.Errorf("nfo %s: file too large", path)
	}
	return b, nil
}

// parseXML decodes the first element. ok is false when the file is not XML.
func parseXML(b []byte) (nfoDoc, bool, error) {
	t := bytes.TrimSpace(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
	if len(t) == 0 || t[0] != '<' {
		return nfoDoc{}, false, nil
	}
	dec := xml.NewDecoder(bytes.NewReader(t))
	dec.Strict = false
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "iso-8859-1", "latin1", "latin-1":
			return charmap.ISO8859_1.NewDecoder().Reader(input), nil
		case "windows-1252", "cp1252":
			return charmap.Windows1252.NewDecoder().Reader(input), nil
		}
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	var d nfoDoc
	if err := dec.Decode(&d); err != nil {
		return nfoDoc{}, false, err
	}
	return d, true, nil
}

func readNFO(path, root string) (metadata.Record, error) {
	b, err := readLimited(path)
	if err != nil {
		return metadata.Record{}, err
	}
	doc, ok, err := parseXML(b)
	if err != nil {
		return metadata.Record{}, fmt.Errorf("nfo %s: %w", path, err)
	}
	if !ok {
		return metadata.Record{IDs: idsFromText(string(b))}, nil
	}
	if doc.XMLName.Local != root {
		return metadata.Record{}, fmt.Errorf("nfo %s: root <%s>, want <%s>", path, doc.XMLName.Local, root)
	}
	return doc.record(root), nil
}

func (d nfoDoc) record(root string) metadata.Record {
	r := metadata.Record{
		Title:         strings.TrimSpace(d.Title),
		OriginalTitle: strings.TrimSpace(d.OriginalTitle),
		SortTitle:     strings.TrimSpace(d.SortTitle),
		Overview:      first(d.Plot, d.Outline),
		Released:      first(validDate(d.Premiered), validDate(d.Aired)),
		ContentRating: cleanMPAA(d.MPAA),
		Genres:        splitList(d.Genres),
		Studios:       splitList(d.Studios),
		Tags:          splitList(d.Tags),
		IDs:           d.ids(root),
		Ratings:       d.ratings(),
	}
	r.Year = atoi(d.Year)
	if r.Year == 0 && len(r.Released) >= 4 {
		r.Year = atoi(r.Released[:4])
	}
	for i, a := range d.Actors {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		order := i
		if a.Order != "" {
			order = atoi(a.Order)
		}
		img := strings.TrimSpace(a.Thumb)
		if !isHTTP(img) {
			img = ""
		}
		r.People = append(r.People, metadata.Person{Name: name, Role: metadata.RoleActor, Character: strings.TrimSpace(a.Role), ImageURL: img, Order: order})
	}
	for _, n := range splitList(d.Directors) {
		r.People = append(r.People, metadata.Person{Name: n, Role: metadata.RoleDirector, Order: len(r.People)})
	}
	for _, n := range splitList(d.Credits) {
		r.People = append(r.People, metadata.Person{Name: n, Role: metadata.RoleWriter, Order: len(r.People)})
	}
	for _, th := range d.Thumbs {
		u := strings.TrimSpace(th.Value)
		if !isHTTP(u) {
			continue
		}
		typ := metadata.ImagePoster
		switch strings.ToLower(th.Aspect) {
		case "", "poster":
		case "banner":
			typ = metadata.ImageBanner
		case "clearlogo", "logo":
			typ = metadata.ImageLogo
		case "clearart":
			typ = metadata.ImageClearArt
		case "landscape", "thumb":
			typ = metadata.ImageThumb
		default:
			continue
		}
		r.Images = append(r.Images, metadata.Image{Type: typ, URL: u})
	}
	for _, f := range d.Fanart {
		if u := strings.TrimSpace(f); isHTTP(u) {
			r.Images = append(r.Images, metadata.Image{Type: metadata.ImageBackdrop, URL: u})
		}
	}
	return r
}

func (d nfoDoc) ids(root string) map[string]string {
	ids := map[string]string{}
	for _, u := range d.UniqueIDs {
		t := strings.ToLower(strings.TrimSpace(u.Type))
		if t == "themoviedb" {
			t = "tmdb"
		}
		if v := strings.TrimSpace(u.Value); t != "" && v != "" {
			ids[t] = v
		}
	}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" && ids[k] == "" {
			ids[k] = v
		}
	}
	set("tmdb", d.TMDBID)
	set("imdb", d.IMDBID)
	set("tvdb", d.TVDBID)
	if id := strings.TrimSpace(d.ID); imdbRe.MatchString(id) {
		set("imdb", id)
	} else if digitsRe.MatchString(id) {
		if root == "tvshow" {
			set("tvdb", id)
		} else {
			set("tmdb", id)
		}
	}
	return ids
}

func (d nfoDoc) ratings() map[string]float64 {
	out := map[string]float64{}
	for _, r := range d.Ratings {
		name := strings.ToLower(strings.TrimSpace(r.Name))
		switch name {
		case "themoviedb":
			name = "tmdb"
		case "tomatometerallcritics":
			name = "rt"
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(r.Value), 64)
		if name == "" || err != nil {
			continue
		}
		if max, err := strconv.ParseFloat(r.Max, 64); err == nil && max > 0 && max != 10 {
			v = v * 10 / max
		}
		out[name] = v
	}
	return out
}

func idsFromText(s string) map[string]string {
	ids := map[string]string{}
	if m := imdbInText.FindString(s); m != "" {
		ids["imdb"] = m
	}
	if m := tmdbInText.FindStringSubmatch(s); m != nil {
		ids["tmdb"] = m[1]
	}
	return ids
}

var artNames = []struct {
	typ   string
	names []string
}{
	{metadata.ImagePoster, []string{"poster", "folder", "cover"}},
	{metadata.ImageBackdrop, []string{"fanart", "backdrop", "background"}},
	{metadata.ImageLogo, []string{"clearlogo", "logo"}},
	{metadata.ImageBanner, []string{"banner"}},
	{metadata.ImageClearArt, []string{"clearart"}},
	{metadata.ImageThumb, []string{"landscape", "thumb"}},
}

var artExts = []string{".jpg", ".jpeg", ".png", ".webp"}

// localArtwork finds one local image per type. base is the movie file name
// without extension ("" for shows).
func localArtwork(dir, base string) []metadata.Image {
	var out []metadata.Image
	for _, a := range artNames {
		var tries []string
		for _, n := range a.names {
			for _, ext := range artExts {
				if base != "" {
					tries = append(tries, base+"-"+n+ext)
				}
			}
		}
		for _, n := range a.names {
			for _, ext := range artExts {
				tries = append(tries, n+ext)
			}
		}
		for _, name := range tries {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, metadata.Image{Type: a.typ, URL: fileURL(p)})
				break
			}
		}
	}
	return out
}

func fileURL(p string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(p)}).String()
}

func isHTTP(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

func first(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func validDate(s string) string {
	if s = strings.TrimSpace(s); dateRe.MatchString(s) {
		return s
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// cleanMPAA turns "Rated R", "US:R" or "Australia:M" into "R" / "M".
func cleanMPAA(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "Rated "))
}

// splitList trims entries and splits Kodi's "A / B" form.
func splitList(in []string) []string {
	var out []string
	for _, s := range in {
		for _, part := range strings.Split(s, " / ") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`
If `TestMovieNFO` fails only on the escaped fanart file name, compare with
`(&url.URL{Scheme: "file", Path: p}).String()` output for that name and fix the expected string
(the URL must stay escaped; `artwork.Store` unescapes it via `url.Parse`).

- [ ] **Step 6: Commit**

```bash
git add server/internal/metadata/nfo
git commit -m "feat(metadata): Kodi NFO and local artwork provider"
```

---

### Task 11: Background work queue (dedupe, workers, retry with backoff)

**Files:**
- Create: `server/internal/metadata/queue.go`
- Test: `server/internal/metadata/queue_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `type QueueOptions struct{ Workers int; Backoff []time.Duration }` (defaults 2 workers; backoff 1m, 5m, 30m; `len(Backoff)` = max retries); `NewQueue(run func(ctx context.Context, id int64, force bool) error, o QueueOptions, log *slog.Logger) *Queue`; `(*Queue) Enqueue(id int64, force bool)` (deduplicates by ID, ORs `force`, re-runs once if enqueued while running); `(*Queue) Len() int`; `(*Queue) Run(ctx)` (blocks until ctx ends and workers stop).
  Provider rate limits live in each `Fetcher`, so workers only bound concurrency.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/queue_test.go`:
```go
package metadata

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type calls struct {
	mu    sync.Mutex
	n     map[int64]int
	force map[int64]bool
}

func newCalls() *calls { return &calls{n: map[int64]int{}, force: map[int64]bool{}} }

func (c *calls) get(id int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[id]
}

func startQueue(t *testing.T, q *Queue) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestQueueDedupes(t *testing.T) {
	c := newCalls()
	q := NewQueue(func(_ context.Context, id int64, force bool) error {
		c.mu.Lock()
		c.n[id]++
		c.mu.Unlock()
		return nil
	}, QueueOptions{}, nil)
	q.Enqueue(7, false)
	q.Enqueue(7, false)
	q.Enqueue(8, false)
	q.Enqueue(7, false)
	if q.Len() != 2 {
		t.Fatalf("len = %d", q.Len())
	}
	startQueue(t, q)
	waitUntil(t, func() bool { return q.Len() == 0 })
	if c.get(7) != 1 || c.get(8) != 1 {
		t.Fatalf("calls %v", c.n)
	}
}

func TestQueueRetriesThenSucceeds(t *testing.T) {
	c := newCalls()
	q := NewQueue(func(_ context.Context, id int64, _ bool) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.n[id]++
		if c.n[id] < 3 {
			return errors.New("temporary")
		}
		return nil
	}, QueueOptions{Workers: 1, Backoff: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}, nil)
	q.Enqueue(1, false)
	startQueue(t, q)
	waitUntil(t, func() bool { return q.Len() == 0 })
	if c.get(1) != 3 {
		t.Fatalf("calls = %d", c.get(1))
	}
}

func TestQueueGivesUp(t *testing.T) {
	c := newCalls()
	q := NewQueue(func(_ context.Context, id int64, _ bool) error {
		c.mu.Lock()
		c.n[id]++
		c.mu.Unlock()
		return errors.New("permanent")
	}, QueueOptions{Workers: 1, Backoff: []time.Duration{time.Millisecond}}, nil)
	q.Enqueue(1, false)
	startQueue(t, q)
	waitUntil(t, func() bool { return q.Len() == 0 })
	if c.get(1) != 2 {
		t.Fatalf("calls = %d, want 1 try + 1 retry", c.get(1))
	}
}

func TestQueueForceIsSticky(t *testing.T) {
	c := newCalls()
	q := NewQueue(func(_ context.Context, id int64, force bool) error {
		c.mu.Lock()
		c.n[id]++
		c.force[id] = force
		c.mu.Unlock()
		return nil
	}, QueueOptions{}, nil)
	q.Enqueue(5, false)
	q.Enqueue(5, true)
	startQueue(t, q)
	waitUntil(t, func() bool { return c.get(5) == 1 })
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.force[5] {
		t.Fatal("force flag lost")
	}
}

func TestQueueReenqueueWhileRunning(t *testing.T) {
	c := newCalls()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	q := NewQueue(func(_ context.Context, id int64, _ bool) error {
		c.mu.Lock()
		c.n[id]++
		first := c.n[id] == 1
		c.mu.Unlock()
		if first {
			started <- struct{}{}
			<-release
		}
		return nil
	}, QueueOptions{Workers: 1}, nil)
	q.Enqueue(1, false)
	startQueue(t, q)
	<-started
	q.Enqueue(1, false) // changed again while matching
	close(release)
	waitUntil(t, func() bool { return c.get(1) == 2 && q.Len() == 0 })
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewQueue`

- [ ] **Step 3: Implement**

`server/internal/metadata/queue.go`:
```go
package metadata

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// QueueOptions tunes a Queue.
type QueueOptions struct {
	Workers int             // default 2
	Backoff []time.Duration // delay before retry 1, 2, ...; default 1m, 5m, 30m
}

type job struct {
	id       int64
	force    bool
	attempts int
	due      time.Time
	running  bool
	again    bool
}

// Queue runs a function per item ID in the background, de-duplicating
// pending IDs and retrying failures with backoff.
type Queue struct {
	run   func(ctx context.Context, id int64, force bool) error
	o     QueueOptions
	log   *slog.Logger
	mu    sync.Mutex
	jobs  map[int64]*job
	order []int64
	wake  chan struct{} // closed and replaced to wake all workers
}

// NewQueue returns a stopped queue; call Run to process it.
func NewQueue(run func(ctx context.Context, id int64, force bool) error, o QueueOptions, log *slog.Logger) *Queue {
	if o.Workers <= 0 {
		o.Workers = 2
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Queue{run: run, o: o, log: log, jobs: map[int64]*job{}, wake: make(chan struct{})}
}

// Enqueue schedules id now. A pending id is not added twice; an id that is
// running is run once more afterwards.
func (q *Queue) Enqueue(id int64, force bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[id]; ok {
		j.force = j.force || force
		if j.running {
			j.again = true
		} else {
			j.due, j.attempts = time.Time{}, 0
		}
	} else {
		q.jobs[id] = &job{id: id, force: force}
		q.order = append(q.order, id)
	}
	q.notifyLocked()
}

func (q *Queue) notifyLocked() {
	close(q.wake)
	q.wake = make(chan struct{})
}

// Len is the number of pending or running jobs.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

// Run processes jobs until ctx is cancelled.
func (q *Queue) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < q.o.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.worker(ctx)
		}()
	}
	wg.Wait()
}

func (q *Queue) worker(ctx context.Context) {
	for {
		q.mu.Lock()
		j, wait := q.nextLocked(time.Now())
		wake := q.wake
		force := false
		if j != nil {
			force = j.force
		}
		q.mu.Unlock()
		if j == nil {
			var timer *time.Timer
			var tc <-chan time.Time
			if wait > 0 {
				timer = time.NewTimer(wait)
				tc = timer.C
			}
			select {
			case <-ctx.Done():
			case <-wake:
			case <-tc:
			}
			if timer != nil {
				timer.Stop()
			}
			if ctx.Err() != nil {
				return
			}
			continue
		}
		err := q.run(ctx, j.id, force)
		q.finish(ctx, j, err)
		if ctx.Err() != nil {
			return
		}
	}
}

// nextLocked returns a due job (marked running) or how long to wait (-1 = until woken).
func (q *Queue) nextLocked(now time.Time) (*job, time.Duration) {
	wait := time.Duration(-1)
	var picked *job
	live := q.order[:0]
	for _, id := range q.order {
		j, ok := q.jobs[id]
		if !ok {
			continue
		}
		live = append(live, id)
		if picked != nil || j.running {
			continue
		}
		if !j.due.After(now) {
			picked = j
			continue
		}
		if d := j.due.Sub(now); wait < 0 || d < wait {
			wait = d
		}
	}
	q.order = live
	if picked != nil {
		picked.running = true
		return picked, 0
	}
	return nil, wait
}

func (q *Queue) finish(ctx context.Context, j *job, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.running = false
	if j.again {
		j.again, j.attempts, j.due = false, 0, time.Time{}
		q.notifyLocked()
		return
	}
	if err == nil || ctx.Err() != nil {
		delete(q.jobs, j.id)
		return
	}
	if j.attempts < len(q.o.Backoff) {
		j.due = time.Now().Add(q.o.Backoff[j.attempts])
		j.attempts++
		q.log.Warn("metadata match failed, will retry", "item", j.id, "attempt", j.attempts, "err", err)
		q.notifyLocked()
		return
	}
	q.log.Error("metadata match failed, giving up until the next scan", "item", j.id, "err", err)
	delete(q.jobs, j.id)
}
```

The in-place filter of `q.order` is safe because the write index never passes the read index.

- [ ] **Step 4: Run tests, expect PASS** (also with the race detector)

Run: `make test` and
`docker run --rm -v "$PWD/server":/src -w /src -v lisimflix-gomod:/go/pkg/mod golang:1.27-trixie go test -race ./internal/metadata/ -run Queue`
(`-race` needs CGO, so this one command omits `CGO_ENABLED=0`; CI runs `-race` too.)

- [ ] **Step 5: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): retrying background match queue"
```

---

### Task 12: Matcher core (search, fetch, merge, apply; locks, manual, fix match)

**Files:**
- Create: `server/internal/metadata/matcher.go`
- Test: `server/internal/metadata/matcher_test.go`, `server/internal/metadata/matcher_helpers_test.go`

**Interfaces:**
- Consumes: `Registry`, `Store`, `SettingsStore`, `Queue`, `PickBest`, `Rank`, `SortTitle`, `DetailsFromMerged`, `WithNoCache`, `media.Store` methods (`GetItem`, `Children`, `UpdateItem`, `Files`, `ItemsNeedingMatch`), `library.Store` (`List`, `Get`).
- Produces:
  - `type ItemStore interface{ GetItem; Children; UpdateItem; Files; ItemsNeedingMatch }` and `type LibraryStore interface{ List; Get }` (satisfied by `*media.Store` / `*library.Store`)
  - `type ImagePrefetcher interface{ PrefetchItem(ctx, itemID int64) error }`
  - `type MatcherOptions struct{ Registry *Registry; Items ItemStore; Libraries LibraryStore; Store *Store; Settings *SettingsStore; Images ImagePrefetcher /* optional */; Log *slog.Logger; Queue QueueOptions }`
  - `NewMatcher(o) *Matcher`; `(*Matcher) MatchItem(ctx, itemID int64, force bool) error` (seasons/episodes resolve to their show; `video` items are skipped; `force` bypasses the response cache); `(*Matcher) Search(ctx, itemID int64, title string, year int) ([]Scored, error)`; `(*Matcher) ApplyMatch(ctx, itemID int64, provider string, ids map[string]string) error` (validates with the chosen provider first, then stores the IDs, sets `MatchState "manual"` and refreshes)
  - errors `ErrUnsupportedKind`, `ErrBadMatch`; `ShowDir(roots []string, file string) string`
  - unexported `apply(ctx, it, lib, rec Merged, state string, preferLocal bool) error` (Task 13 extends it for shows).

Flow of `MatchItem` for a movie or show:
1. Read the local NFO (provider `nfo`) at the movie file / show folder (`ShowDir` of the first
   episode file). Its IDs fill missing IDs, or replace them when the library prefers local data
   and the item is not `manual`.
2. Expand IDs with the registry's `IDMapper` (no-op until plan 1E).
3. No remote ID and not `manual` → search every supporting remote provider with the item title
   and year (the NFO title/year when present), `PickBest` with `match_threshold`. Below the
   threshold the item stays `unmatched` (local NFO data is still applied) and is retried by the
   daily sweep or fixed by an admin.
4. Fetch from every supporting remote provider; providers that answer `ErrNoUsableID` are retried
   after others have added IDs (Fanart.tv needs the TVDB ID that TMDB returns).
5. Merge (local record included), apply to the item (respecting `LockedFields`; `KeepOriginalTitle`
   uses the original title), save details, prefetch primary images. State becomes `matched`
   unless it is `manual`. A transient provider failure with nothing fetched returns the error so
   the queue retries.

- [ ] **Step 1: Write the test environment**

`server/internal/metadata/matcher_helpers_test.go`:
```go
package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

type testEnv struct {
	d        *db.DB
	reg      *Registry
	items    *media.Store
	libs     *library.Store
	store    *Store
	settings *SettingsStore
	lib      library.Library
	m        *Matcher
}

func newEnv(t *testing.T, lt library.Type, providers ...Provider) *testEnv {
	return newEnvOpts(t, lt, library.Options{}, providers...)
}

func newEnvOpts(t *testing.T, lt library.Type, opts library.Options, providers ...Provider) *testEnv {
	t.Helper()
	d := dbtest.Open(t)
	e := &testEnv{d: d, reg: NewRegistry(), items: media.NewStore(d), libs: library.NewStore(d),
		store: NewStore(d), settings: NewSettingsStore(d, nil)}
	for _, p := range providers {
		e.reg.Register(p)
	}
	e.lib = seedLibrary(t, d, lt, opts)
	e.m = NewMatcher(MatcherOptions{Registry: e.reg, Items: e.items, Libraries: e.libs, Store: e.store,
		Settings: e.settings, Queue: QueueOptions{Workers: 1, Backoff: []time.Duration{time.Millisecond}}})
	return e
}

func (e *testEnv) item(t *testing.T, it media.Item) media.Item {
	t.Helper()
	it.LibraryID = e.lib.ID
	return seedItem(t, e.d, it)
}

func (e *testEnv) get(t *testing.T, id int64) media.Item {
	t.Helper()
	it, err := e.items.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func (e *testEnv) view(t *testing.T, id int64) View {
	t.Helper()
	v, _, err := e.store.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// tmdbFake answers like TMDB for The Matrix (603) and Breaking Bad (1396).
func tmdbFake() *fakeProvider {
	return &fakeProvider{name: "tmdb", kinds: []media.Kind{KindMovie, KindShow}, idKey: "tmdb",
		cands: []Candidate{
			{Provider: "tmdb", IDs: map[string]string{"tmdb": "604"}, Title: "The Matrix Reloaded", Year: 2003, Score: 1},
			{Provider: "tmdb", IDs: map[string]string{"tmdb": "603"}, Title: "The Matrix", Year: 1999, Score: 0.95},
		},
		records: map[string]Record{
			"603": {Provider: "tmdb", IDs: map[string]string{"tmdb": "603", "imdb": "tt0133093"},
				Title: "The Matrix", OriginalTitle: "The Matrix", Year: 1999, Overview: "A hacker learns the truth.",
				Genres: []string{"Action", "Science Fiction"},
				Images: []Image{{Type: ImagePoster, URL: "https://img.example/p603.jpg"}}},
			"1396": {Provider: "tmdb", IDs: map[string]string{"tmdb": "1396", "tvdb": "81189"},
				Title: "Breaking Bad", Year: 2008, Overview: "Chemistry teacher.",
				Seasons: []SeasonRecord{{Number: 1, Title: "Season 1", Overview: "Season one.",
					Images: []Image{{Type: ImagePoster, URL: "https://img.example/s1.jpg"}},
					Episodes: []EpisodeRecord{
						{Season: 1, Number: 1, Title: "Pilot", Overview: "Ep one.", AirDate: "2008-01-20", Absolute: intp(1),
							Images: []Image{{Type: ImageThumb, URL: "https://img.example/e1.jpg"}}},
						{Season: 1, Number: 2, Title: "Cat's in the Bag...", Overview: "Ep two.", AirDate: "2008-01-27", Absolute: intp(2)},
					}}}},
		}}
}
```

- [ ] **Step 2: Write the failing tests**

`server/internal/metadata/matcher_test.go`:
```go
package metadata

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func TestMatchMovieBySearch(t *testing.T) {
	tm := tmdbFake()
	e := newEnv(t, LibMovies, tm)
	it := e.item(t, media.Item{Kind: KindMovie, Title: "the matrix", Year: 1999})
	if err := e.m.MatchItem(context.Background(), it.ID, false); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, it.ID)
	if got.Title != "The Matrix" || got.SortTitle != "Matrix" || got.MatchState != StateMatched ||
		got.ExternalIDs["tmdb"] != "603" || got.ExternalIDs["imdb"] != "tt0133093" {
		t.Fatalf("%+v", got)
	}
	v := e.view(t, it.ID)
	if v.Overview != "A hacker learns the truth." || len(v.Genres) != 2 || len(v.Images) != 1 {
		t.Fatalf("%+v", v)
	}
}

func TestMatchBelowThresholdStaysUnmatched(t *testing.T) {
	tm := tmdbFake()
	e := newEnv(t, LibMovies, tm)
	it := e.item(t, media.Item{Kind: KindMovie, Title: "Completely Different", Year: 1999})
	if err := e.m.MatchItem(context.Background(), it.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, it.ID); got.MatchState != StateUnmatched || got.Title != "Completely Different" {
		t.Fatalf("%+v", got)
	}
	if s, f := tm.counts(); s != 1 || f != 0 {
		t.Fatalf("searches=%d fetches=%d", s, f)
	}
}

func TestMatchWithKnownIDSkipsSearch(t *testing.T) {
	tm := tmdbFake()
	e := newEnv(t, LibMovies, tm)
	it := e.item(t, media.Item{Kind: KindMovie, Title: "Matrix 1080p", ExternalIDs: map[string]string{"tmdb": "603"}})
	if err := e.m.MatchItem(context.Background(), it.ID, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := tm.counts(); s != 0 {
		t.Fatalf("searched %d times", s)
	}
	if got := e.get(t, it.ID); got.Title != "The Matrix" || got.MatchState != StateMatched {
		t.Fatalf("%+v", got)
	}
}

func TestLockedFieldsSurvive(t *testing.T) {
	e := newEnv(t, LibMovies, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "My Matrix", ExternalIDs: map[string]string{"tmdb": "603"},
		LockedFields: []string{FieldTitle, FieldOverview}})
	ctx := context.Background()
	if err := e.store.SaveFields(ctx, it.ID, Details{Overview: "Mine"}, []string{FieldOverview}); err != nil {
		t.Fatal(err)
	}
	if err := e.m.MatchItem(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, it.ID); got.Title != "My Matrix" || got.Year != 1999 {
		t.Fatalf("%+v", got)
	}
	if v := e.view(t, it.ID); v.Overview != "Mine" || len(v.Genres) != 2 {
		t.Fatalf("%+v", v)
	}
}

func TestManualItemNeverSearched(t *testing.T) {
	tm := tmdbFake()
	e := newEnv(t, LibMovies, tm)
	ctx := context.Background()
	noIDs := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", Year: 1999, MatchState: StateManual})
	if err := e.m.MatchItem(ctx, noIDs.ID, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := tm.counts(); s != 0 {
		t.Fatal("manual item was searched")
	}
	withIDs := e.item(t, media.Item{Kind: KindMovie, Title: "x", MatchState: StateManual, ExternalIDs: map[string]string{"tmdb": "603"}})
	if err := e.m.MatchItem(ctx, withIDs.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, withIDs.ID); got.MatchState != StateManual || got.Title != "The Matrix" {
		t.Fatalf("%+v", got)
	}
}

func TestLaterProviderGetsDiscoveredIDs(t *testing.T) {
	fan := &fakeProvider{name: "fanart", kinds: []media.Kind{KindMovie}, idKey: "imdb",
		records: map[string]Record{"tt0133093": {Provider: "fanart", Images: []Image{{Type: ImageLogo, URL: "https://img.example/logo.png"}}}}}
	e := newEnv(t, LibMovies, fan, tmdbFake()) // fanart registered first on purpose
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", Year: 1999})
	if err := e.m.MatchItem(context.Background(), it.ID, false); err != nil {
		t.Fatal(err)
	}
	var logo bool
	for _, im := range e.view(t, it.ID).Images {
		logo = logo || im.Type == ImageLogo
	}
	if !logo {
		t.Fatal("fanart was not retried with the imdb id from tmdb")
	}
}

func TestTransientErrorIsReturned(t *testing.T) {
	tm := tmdbFake()
	tm.searchErr = errors.New("connection reset")
	e := newEnv(t, LibMovies, tm)
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", Year: 1999})
	if err := e.m.MatchItem(context.Background(), it.ID, false); err == nil {
		t.Fatal("want error so the queue retries")
	}
	tm.searchErr = ErrNotConfigured
	if err := e.m.MatchItem(context.Background(), it.ID, false); err != nil {
		t.Fatalf("unconfigured provider is not an error: %v", err)
	}
}

func TestApplyMatch(t *testing.T) {
	e := newEnv(t, LibMovies, tmdbFake())
	ctx := context.Background()
	it := e.item(t, media.Item{Kind: KindMovie, Title: "Matrx", ExternalIDs: map[string]string{"tmdb": "604"}})
	if err := e.m.ApplyMatch(ctx, it.ID, "nope", map[string]string{"tmdb": "603"}); !errors.Is(err, ErrBadMatch) {
		t.Fatalf("unknown provider: %v", err)
	}
	if err := e.m.ApplyMatch(ctx, it.ID, "tmdb", map[string]string{"tmdb": "1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing title: %v", err)
	}
	if got := e.get(t, it.ID); got.ExternalIDs["tmdb"] != "604" {
		t.Fatal("failed apply must not change the item")
	}
	if err := e.m.ApplyMatch(ctx, it.ID, "tmdb", map[string]string{"tmdb": "603"}); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, it.ID); got.MatchState != StateManual || got.Title != "The Matrix" || got.ExternalIDs["tmdb"] != "603" {
		t.Fatalf("%+v", got)
	}
}

func TestSearchRanks(t *testing.T) {
	e := newEnv(t, LibMovies, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "whatever"})
	res, err := e.m.Search(context.Background(), it.ID, "The Matrix", 1999)
	if err != nil || len(res) != 2 || res[0].IDs["tmdb"] != "603" {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestLocalNFOPreferred(t *testing.T) {
	video := "/media/movies/The Matrix (1999)/The Matrix (1999).mkv"
	nfo := &fakeProvider{name: LocalProviderName, kinds: []media.Kind{KindMovie, KindShow}, idKey: LocalPathKey,
		records: map[string]Record{video: {Provider: LocalProviderName, IDs: map[string]string{"tmdb": "603"}, Overview: "From NFO"}}}
	tm := tmdbFake()
	e := newEnv(t, LibMovies, nfo, tm)
	ctx := context.Background()
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", Year: 1999})
	seedFile(t, e.d, e.lib.ID, it.ID, video)
	if err := e.m.MatchItem(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := tm.counts(); s != 0 {
		t.Fatal("NFO id should make search unnecessary")
	}
	if v := e.view(t, it.ID); v.Overview != "From NFO" {
		t.Fatalf("overview %q", v.Overview)
	}
	if err := e.settings.Update(ctx, SettingsUpdate{PreferLocal: map[int64]bool{e.lib.ID: false}}); err != nil {
		t.Fatal(err)
	}
	if err := e.m.MatchItem(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	if v := e.view(t, it.ID); v.Overview != "A hacker learns the truth." {
		t.Fatalf("overview %q", v.Overview)
	}
}

func TestKeepOriginalTitle(t *testing.T) {
	tm := tmdbFake()
	rec := tm.records["603"]
	rec.OriginalTitle = "Matrix (Original)"
	tm.records["603"] = rec
	e := newEnvOpts(t, LibMovies, library.Options{KeepOriginalTitle: true}, tm)
	it := e.item(t, media.Item{Kind: KindMovie, Title: "x", ExternalIDs: map[string]string{"tmdb": "603"}})
	if err := e.m.MatchItem(context.Background(), it.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, it.ID); got.Title != "Matrix (Original)" {
		t.Fatalf("title %q", got.Title)
	}
}

func TestShowDir(t *testing.T) {
	roots := []string{"/media/tv", "/media/tv2"}
	cases := map[string]string{
		"/media/tv/Breaking Bad/Season 01/S01E01.mkv": filepath.Join("/media/tv", "Breaking Bad"),
		"/media/tv2/Show/E01.mkv":                     filepath.Join("/media/tv2", "Show"),
		"/media/tv/loose.mkv":                         "/media/tv",
		"/elsewhere/Show/S01/E01.mkv":                 "/elsewhere/Show/S01",
	}
	for in, want := range cases {
		if got := ShowDir(roots, in); got != want {
			t.Errorf("ShowDir(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewMatcher`

- [ ] **Step 4: Implement**

`server/internal/metadata/matcher.go`:
```go
package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// ItemStore is the part of *media.Store the matcher uses.
type ItemStore interface {
	GetItem(ctx context.Context, id int64) (media.Item, error)
	Children(ctx context.Context, parentID int64) ([]media.Item, error)
	UpdateItem(ctx context.Context, it media.Item) error
	Files(ctx context.Context, itemID int64) ([]media.File, error)
	ItemsNeedingMatch(ctx context.Context, libraryID int64) ([]media.Item, error)
}

// LibraryStore is the part of *library.Store the matcher uses.
type LibraryStore interface {
	List(ctx context.Context) ([]library.Library, error)
	Get(ctx context.Context, id int64) (library.Library, error)
}

var (
	_ ItemStore    = (*media.Store)(nil)
	_ LibraryStore = (*library.Store)(nil)
)

// ImagePrefetcher downloads an item's primary images after a match.
type ImagePrefetcher interface {
	PrefetchItem(ctx context.Context, itemID int64) error
}

// Matcher errors.
var (
	ErrUnsupportedKind = errors.New("metadata: only movies and shows can be matched")
	ErrBadMatch        = errors.New("metadata: invalid match request")
)

// MatcherOptions wires a Matcher.
type MatcherOptions struct {
	Registry  *Registry
	Items     ItemStore
	Libraries LibraryStore
	Store     *Store
	Settings  *SettingsStore
	Images    ImagePrefetcher // optional
	Log       *slog.Logger
	Queue     QueueOptions
}

// Matcher matches library items to provider metadata.
type Matcher struct {
	o     MatcherOptions
	log   *slog.Logger
	queue *Queue
}

// NewMatcher returns a matcher. Call Start to run its background queue.
func NewMatcher(o MatcherOptions) *Matcher {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	m := &Matcher{o: o, log: log.With("module", "metadata")}
	m.queue = NewQueue(m.MatchItem, o.Queue, m.log)
	return m
}

func matchable(k media.Kind) bool { return k == KindMovie || k == KindShow }

// MatchItem matches or refreshes one item. Seasons and episodes resolve to
// their show; other kinds are skipped. force bypasses the response cache.
func (m *Matcher) MatchItem(ctx context.Context, itemID int64, force bool) error {
	it, err := m.topLevel(ctx, itemID)
	if err != nil {
		return err
	}
	if !matchable(it.Kind) {
		return nil
	}
	lib, err := m.o.Libraries.Get(ctx, it.LibraryID)
	if err != nil {
		return fmt.Errorf("match item %d: library: %w", it.ID, err)
	}
	if force {
		ctx = WithNoCache(ctx)
	}
	return m.match(ctx, it, lib)
}

func (m *Matcher) topLevel(ctx context.Context, id int64) (media.Item, error) {
	it, err := m.o.Items.GetItem(ctx, id)
	for hops := 0; err == nil && it.ParentID != 0 && hops < 3; hops++ {
		it, err = m.o.Items.GetItem(ctx, it.ParentID)
	}
	if err != nil {
		return media.Item{}, fmt.Errorf("match item %d: %w", id, err)
	}
	return it, nil
}

func (m *Matcher) match(ctx context.Context, it media.Item, lib library.Library) error {
	reg := m.o.Registry
	st, err := m.o.Settings.Load(ctx)
	if err != nil {
		return err
	}
	preferLocal, err := m.o.Settings.PreferLocal(ctx, lib.ID)
	if err != nil {
		return err
	}
	manual := it.MatchState == StateManual
	ids := PublicIDs(it.ExternalIDs)

	// 1. Local NFO.
	var local *Record
	if p, ok := reg.Provider(LocalProviderName); ok && p.Supports(it.Kind, lib.Type) {
		if path := m.localPath(ctx, lib, it); path != "" {
			lids := cloneIDs(ids)
			lids[LocalPathKey] = path
			rec, err := p.Fetch(ctx, it.Kind, lids)
			switch {
			case err == nil:
				rec.Provider = LocalProviderName
				local = &rec
				override := preferLocal && !manual
				for k, v := range PublicIDs(rec.IDs) {
					if _, has := ids[k]; !has || override {
						ids[k] = v
					}
				}
			case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoUsableID):
			default:
				m.log.Warn("read local metadata", "item", it.ID, "path", path, "err", err)
			}
		}
	}

	// 2. Cross-reference IDs (anime-lists, plan 1E).
	if exp, err := reg.CurrentIDMapper().Expand(ctx, cloneIDs(ids)); err != nil {
		m.log.Warn("expand ids", "item", it.ID, "err", err)
	} else {
		for k, v := range PublicIDs(exp) {
			if _, has := ids[k]; !has {
				ids[k] = v
			}
		}
	}

	// 3. Search when there is nothing to fetch by.
	if !hasRemoteID(ids) && !manual {
		q := Query{Kind: it.Kind, LibType: lib.Type, Title: it.Title, Year: it.Year, IDs: cloneIDs(ids)}
		if local != nil && local.Title != "" {
			q.Title = local.Title
			if local.Year > 0 {
				q.Year = local.Year
			}
		}
		cands, err := m.searchAll(ctx, q)
		if err != nil {
			return fmt.Errorf("match item %d: search: %w", it.ID, err)
		}
		best, ok := PickBest(q, cands, st.MatchThreshold)
		if !ok {
			m.log.Info("no confident match", "item", it.ID, "title", q.Title, "year", q.Year)
			if local != nil {
				return m.apply(ctx, it, lib, reg.Merge(lib.Type, []Record{*local}, true), it.MatchState, preferLocal)
			}
			return nil
		}
		for k, v := range PublicIDs(best.IDs) {
			ids[k] = v
		}
	}

	// 4. Fetch from every remote provider that can use the IDs.
	recs, err := m.fetchAll(ctx, it.Kind, lib.Type, ids)
	if err != nil && len(recs) == 0 {
		return fmt.Errorf("match item %d: %w", it.ID, err)
	}
	state := it.MatchState
	if len(recs) > 0 && !manual {
		state = StateMatched
	}
	if local != nil {
		recs = append(recs, *local)
	}
	if len(recs) == 0 {
		return nil
	}
	merged := reg.Merge(lib.Type, recs, preferLocal)
	for k, v := range ids {
		merged.IDs[k] = v
	}
	return m.apply(ctx, it, lib, merged, state, preferLocal)
}

// searchAll asks every supporting remote provider. It fails only when no
// provider answered and at least one failed for a reason other than missing configuration.
func (m *Matcher) searchAll(ctx context.Context, q Query) ([]Candidate, error) {
	var all []Candidate
	var firstErr error
	answered := 0
	for _, p := range m.o.Registry.Supporting(q.Kind, q.LibType) {
		if p.Name() == LocalProviderName {
			continue
		}
		cs, err := p.Search(ctx, q)
		if err != nil {
			if !errors.Is(err, ErrNotConfigured) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		answered++
		for _, c := range cs {
			if c.Provider == "" {
				c.Provider = p.Name()
			}
			all = append(all, c)
		}
	}
	if answered == 0 && firstErr != nil {
		return nil, firstErr
	}
	return all, nil
}

// fetchAll fetches from remote providers. IDs learned from one provider are
// added to ids, and providers that lacked a usable ID are asked again.
func (m *Matcher) fetchAll(ctx context.Context, kind media.Kind, lt library.Type, ids map[string]string) ([]Record, error) {
	var pending []Provider
	for _, p := range m.o.Registry.Supporting(kind, lt) {
		if p.Name() != LocalProviderName {
			pending = append(pending, p)
		}
	}
	var recs []Record
	var firstErr error
	for progress := true; progress && len(pending) > 0; {
		progress = false
		var next []Provider
		for _, p := range pending {
			rec, err := p.Fetch(ctx, kind, cloneIDs(ids))
			switch {
			case err == nil:
				rec.Provider = p.Name()
				recs = append(recs, rec)
				progress = true
				for k, v := range PublicIDs(rec.IDs) {
					if _, has := ids[k]; !has {
						ids[k] = v
					}
				}
			case errors.Is(err, ErrNoUsableID):
				next = append(next, p)
			case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotConfigured):
				m.log.Debug("provider has nothing", "provider", p.Name(), "err", err)
			default:
				m.log.Warn("metadata fetch failed", "provider", p.Name(), "err", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		pending = next
	}
	return recs, firstErr
}

// apply writes a merged record to the item and the metadata tables.
func (m *Matcher) apply(ctx context.Context, it media.Item, lib library.Library, rec Merged, state string, preferLocal bool) error {
	locked := lockSet(it.LockedFields)
	title := rec.Title
	if lib.Options.KeepOriginalTitle && rec.OriginalTitle != "" {
		title = rec.OriginalTitle
	}
	if title != "" && !locked[FieldTitle] {
		it.Title = title
	}
	if rec.OriginalTitle != "" && !locked[FieldOriginalTitle] {
		it.OriginalTitle = rec.OriginalTitle
	}
	if !locked[FieldSortTitle] {
		if rec.SortTitle != "" {
			it.SortTitle = rec.SortTitle
		} else {
			it.SortTitle = SortTitle(it.Title)
		}
	}
	if rec.Year > 0 && !locked[FieldYear] {
		it.Year = rec.Year
	}
	if ids := PublicIDs(rec.IDs); len(ids) > 0 {
		it.ExternalIDs = ids
	}
	it.MatchState = state
	if err := m.o.Items.UpdateItem(ctx, it); err != nil {
		return fmt.Errorf("update item %d: %w", it.ID, err)
	}
	if err := m.o.Store.Save(ctx, it.ID, DetailsFromMerged(rec), it.LockedFields); err != nil {
		return err
	}
	if m.o.Images != nil {
		if err := m.o.Images.PrefetchItem(ctx, it.ID); err != nil {
			m.log.Warn("prefetch images", "item", it.ID, "err", err)
		}
	}
	return nil
}

// localPath is the movie's first file, or the show's folder.
func (m *Matcher) localPath(ctx context.Context, lib library.Library, it media.Item) string {
	switch it.Kind {
	case KindMovie:
		files, err := m.o.Items.Files(ctx, it.ID)
		if err != nil || len(files) == 0 {
			return ""
		}
		return files[0].Path
	case KindShow:
		if f := m.firstEpisodeFile(ctx, it.ID, 2); f != "" {
			return ShowDir(lib.Paths, f)
		}
	}
	return ""
}

func (m *Matcher) firstEpisodeFile(ctx context.Context, parentID int64, depth int) string {
	kids, err := m.o.Items.Children(ctx, parentID)
	if err != nil {
		return ""
	}
	for _, k := range kids {
		switch {
		case k.Kind == KindEpisode:
			if files, err := m.o.Items.Files(ctx, k.ID); err == nil && len(files) > 0 {
				return files[0].Path
			}
		case k.Kind == KindSeason && depth > 1:
			if p := m.firstEpisodeFile(ctx, k.ID, depth-1); p != "" {
				return p
			}
		}
	}
	return ""
}

// ShowDir returns the show folder for an episode file: the first folder
// below the library root that contains it, or the file's folder.
func ShowDir(roots []string, file string) string {
	for _, root := range roots {
		rel, err := filepath.Rel(root, file)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 2 {
			return filepath.Join(root, parts[0])
		}
	}
	return filepath.Dir(file)
}

// Search lists ranked candidates for the fix-match screen.
func (m *Matcher) Search(ctx context.Context, itemID int64, title string, year int) ([]Scored, error) {
	it, err := m.o.Items.GetItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if !matchable(it.Kind) {
		return nil, ErrUnsupportedKind
	}
	lib, err := m.o.Libraries.Get(ctx, it.LibraryID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = it.Title
	}
	q := Query{Kind: it.Kind, LibType: lib.Type, Title: title, Year: year}
	cands, err := m.searchAll(ctx, q)
	if err != nil {
		return nil, err
	}
	return Rank(q, cands), nil
}

// ApplyMatch sets an admin-chosen match. The chosen provider must know the
// IDs; then the item gets exactly those IDs, MatchState "manual", and a refresh.
func (m *Matcher) ApplyMatch(ctx context.Context, itemID int64, provider string, ids map[string]string) error {
	it, err := m.o.Items.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	if !matchable(it.Kind) {
		return ErrUnsupportedKind
	}
	p, ok := m.o.Registry.Provider(provider)
	if !ok || provider == LocalProviderName {
		return fmt.Errorf("%w: unknown provider %q", ErrBadMatch, provider)
	}
	pub := PublicIDs(ids)
	if len(pub) == 0 {
		return fmt.Errorf("%w: no ids", ErrBadMatch)
	}
	lib, err := m.o.Libraries.Get(ctx, it.LibraryID)
	if err != nil {
		return err
	}
	ctx = WithNoCache(ctx)
	if _, err := p.Fetch(ctx, it.Kind, cloneIDs(pub)); err != nil {
		return fmt.Errorf("apply match via %s: %w", provider, err)
	}
	it.ExternalIDs = pub
	it.MatchState = StateManual
	return m.match(ctx, it, lib)
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): matcher with scoring, merge, locks and manual matches"
```

---

### Task 13: Seasons and episodes, scan listener, background start

**Files:**
- Create: `server/internal/metadata/shows.go`, `server/internal/metadata/listener.go`
- Modify: `server/internal/metadata/matcher.go` (`apply`: one added block)
- Test: `server/internal/metadata/shows_test.go`, `server/internal/metadata/listener_test.go`

**Interfaces:**
- Consumes: Task 12 matcher, `Registry.CurrentEpisodeMapper`, `LocalEpisodeReader`, `Store.SaveFields`, `Store.PruneCache`, `Queue`.
- Produces: `(*Matcher) ItemsChanged(ctx, libraryID int64, itemIDs []int64)` (implements `scanner.ScanListener`: resolves each ID to its top-level movie/show, de-duplicates, enqueues, never blocks on network); `(*Matcher) Run(ctx)` (blocking) and `(*Matcher) Start(ctx)` (non-blocking): run the queue and a maintenance loop that sweeps `ItemsNeedingMatch` at start and daily, and prunes `metadata_cache`); `(*Matcher) Sweep(ctx) error`; `(*Matcher) Enqueue(itemID int64, force bool)`; `(*Matcher) QueueLen() int`.

Episode mapping: if the registry's `EpisodeMapper` returns ok, its (season, episode) is used
(falling back to its absolute number); otherwise the item's own season/episode numbers; an
episode with only an absolute number is looked up by `EpisodeRecord.Absolute`. A local episode
NFO (`<video>.nfo`) overlays the provider data (wins when the library prefers local data, fills
gaps otherwise). Each season gets its title (item), overview and poster; each episode its title,
air date (`Item.AirDate`), overview, released and thumb, all respecting the child's own
`LockedFields`. Unmatched episodes found in the provider data become `matched`.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/shows_test.go`:
```go
package metadata

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

type showItems struct{ show, season, ep1, ep2 media.Item }

func seedShow(t *testing.T, e *testEnv) showItems {
	t.Helper()
	var s showItems
	s.show = e.item(t, media.Item{Kind: KindShow, Title: "Breaking Bad", Year: 2008})
	s.season = e.item(t, media.Item{Kind: KindSeason, ParentID: s.show.ID, Title: "Season 1", SeasonNumber: intp(1)})
	s.ep1 = e.item(t, media.Item{Kind: KindEpisode, ParentID: s.season.ID, Title: "S01E01", SeasonNumber: intp(1), EpisodeNumber: intp(1)})
	s.ep2 = e.item(t, media.Item{Kind: KindEpisode, ParentID: s.season.ID, Title: "S01E02", SeasonNumber: intp(1), EpisodeNumber: intp(2),
		LockedFields: []string{FieldTitle}})
	return s
}

func showFake() *fakeProvider {
	tm := tmdbFake()
	tm.cands = []Candidate{{Provider: "tmdb", IDs: map[string]string{"tmdb": "1396"}, Title: "Breaking Bad", Year: 2008, Score: 1}}
	return tm
}

func TestShowAppliesSeasonsAndEpisodes(t *testing.T) {
	e := newEnv(t, LibTV, showFake())
	s := seedShow(t, e)
	if err := e.m.MatchItem(context.Background(), s.ep1.ID, false); err != nil { // episode id resolves to the show
		t.Fatal(err)
	}
	if got := e.get(t, s.show.ID); got.MatchState != StateMatched || got.ExternalIDs["tvdb"] != "81189" {
		t.Fatalf("show %+v", got)
	}
	ep1 := e.get(t, s.ep1.ID)
	if ep1.Title != "Pilot" || ep1.MatchState != StateMatched || ep1.AirDate != "2008-01-20" {
		t.Fatalf("ep1 %+v", ep1)
	}
	if v := e.view(t, s.ep1.ID); v.Overview != "Ep one." || v.Released != "2008-01-20" || len(v.Images) != 1 || v.Images[0].Type != ImageThumb {
		t.Fatalf("ep1 view %+v", v)
	}
	if ep2 := e.get(t, s.ep2.ID); ep2.Title != "S01E02" {
		t.Fatalf("locked episode title changed: %q", ep2.Title)
	}
	if v := e.view(t, s.ep2.ID); v.Overview != "Ep two." {
		t.Fatalf("ep2 overview %q", v.Overview)
	}
	if v := e.view(t, s.season.ID); v.Overview != "Season one." || len(v.Images) != 1 {
		t.Fatalf("season view %+v", v)
	}
}

type swapMapper struct{}

// Map sends every episode to S01E02, like an absolute->season mapping would.
func (swapMapper) Map(context.Context, Record, media.Item) (int, int, *int, bool) { return 1, 2, nil, true }

func TestEpisodeMapperIsUsed(t *testing.T) {
	e := newEnv(t, LibTV, showFake())
	e.reg.SetEpisodeMapper(swapMapper{})
	s := seedShow(t, e)
	if err := e.m.MatchItem(context.Background(), s.show.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, s.ep1.ID); got.Title != "Cat's in the Bag..." {
		t.Fatalf("mapper ignored: %q", got.Title)
	}
}

func TestAbsoluteOnlyEpisode(t *testing.T) {
	e := newEnv(t, LibAnime, showFake())
	show := e.item(t, media.Item{Kind: KindShow, Title: "Breaking Bad", Year: 2008})
	ep := e.item(t, media.Item{Kind: KindEpisode, ParentID: show.ID, Title: "02", AbsoluteNumber: intp(2)})
	if err := e.m.MatchItem(context.Background(), show.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, ep.ID); got.Title != "Cat's in the Bag..." {
		t.Fatalf("absolute lookup failed: %q", got.Title)
	}
}

type fakeLocal struct {
	*fakeProvider
	episodes map[string]EpisodeRecord
}

func (f *fakeLocal) ReadEpisode(p string) (EpisodeRecord, bool, error) {
	e, ok := f.episodes[p]
	return e, ok, nil
}

func TestLocalEpisodeNFOOverlay(t *testing.T) {
	path := "/media/tv/Breaking Bad/Season 01/S01E01.mkv"
	local := &fakeLocal{
		fakeProvider: &fakeProvider{name: LocalProviderName, kinds: []media.Kind{KindShow}, idKey: LocalPathKey},
		episodes:     map[string]EpisodeRecord{path: {Title: "Pilot (Director's Cut)"}},
	}
	e := newEnv(t, LibTV, local, showFake())
	s := seedShow(t, e)
	seedFile(t, e.d, e.lib.ID, s.ep1.ID, path)
	if err := e.m.MatchItem(context.Background(), s.show.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, s.ep1.ID); got.Title != "Pilot (Director's Cut)" {
		t.Fatalf("title %q", got.Title)
	}
	if v := e.view(t, s.ep1.ID); v.Overview != "Ep one." {
		t.Fatalf("provider overview should fill the gap: %q", v.Overview)
	}
}
```

`server/internal/metadata/listener_test.go`:
```go
package metadata

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func TestItemsChangedEnqueuesTopLevelOnce(t *testing.T) {
	e := newEnv(t, LibTV, showFake())
	s := seedShow(t, e)
	video := e.item(t, media.Item{Kind: KindVideo, Title: "home movie"})
	e.m.ItemsChanged(context.Background(), e.lib.ID, []int64{s.ep1.ID, s.ep2.ID, s.season.ID, video.ID, 99999})
	if n := e.m.QueueLen(); n != 1 {
		t.Fatalf("queue len %d, want only the show", n)
	}
}

func TestStartMatchesInBackground(t *testing.T) {
	e := newEnv(t, LibMovies, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", Year: 1999})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.m.Start(ctx) // the start-up sweep finds the unmatched movie
	waitUntil(t, func() bool { return e.get(t, it.ID).MatchState == StateMatched })
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `e.m.ItemsChanged undefined` and show tests failing (episodes untouched).

- [ ] **Step 3: Implement seasons and episodes**

`server/internal/metadata/shows.go`:
```go
package metadata

import (
	"context"
	"fmt"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

type episodeIndex struct {
	bySE  map[[2]int]EpisodeRecord
	byAbs map[int]EpisodeRecord
}

// applyChildren writes season and episode records to the show's child items.
func (m *Matcher) applyChildren(ctx context.Context, show media.Item, rec Merged, preferLocal bool) error {
	seasons := map[int]SeasonRecord{}
	idx := episodeIndex{bySE: map[[2]int]EpisodeRecord{}, byAbs: map[int]EpisodeRecord{}}
	for _, s := range rec.Seasons {
		seasons[s.Number] = s
		for _, e := range s.Episodes {
			idx.bySE[[2]int{e.Season, e.Number}] = e
			if e.Absolute != nil {
				idx.byAbs[*e.Absolute] = e
			}
		}
	}
	var reader LocalEpisodeReader
	if p, ok := m.o.Registry.Provider(LocalProviderName); ok {
		reader, _ = p.(LocalEpisodeReader)
	}
	kids, err := m.o.Items.Children(ctx, show.ID)
	if err != nil {
		return fmt.Errorf("children of %d: %w", show.ID, err)
	}
	for _, k := range kids {
		switch k.Kind {
		case KindSeason:
			if k.SeasonNumber != nil {
				if sr, ok := seasons[*k.SeasonNumber]; ok {
					if err := m.applySeason(ctx, k, sr); err != nil {
						return err
					}
				}
			}
			eps, err := m.o.Items.Children(ctx, k.ID)
			if err != nil {
				return fmt.Errorf("children of %d: %w", k.ID, err)
			}
			for _, ep := range eps {
				if ep.Kind == KindEpisode {
					if err := m.applyEpisode(ctx, rec, ep, idx, reader, preferLocal); err != nil {
						return err
					}
				}
			}
		case KindEpisode:
			if err := m.applyEpisode(ctx, rec, k, idx, reader, preferLocal); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Matcher) applySeason(ctx context.Context, it media.Item, sr SeasonRecord) error {
	locked := lockSet(it.LockedFields)
	if sr.Title != "" && !locked[FieldTitle] && it.Title != sr.Title {
		it.Title = sr.Title
		if err := m.o.Items.UpdateItem(ctx, it); err != nil {
			return fmt.Errorf("update season %d: %w", it.ID, err)
		}
	}
	d := Details{Overview: sr.Overview, Images: sr.Images}
	return m.o.Store.SaveFields(ctx, it.ID, d, unlockedFields([]string{FieldOverview, ImageField(ImagePoster)}, it.LockedFields))
}

func (m *Matcher) applyEpisode(ctx context.Context, show Merged, ep media.Item, idx episodeIndex, reader LocalEpisodeReader, preferLocal bool) error {
	var er EpisodeRecord
	found := false
	if s, e, abs, ok := m.o.Registry.CurrentEpisodeMapper().Map(ctx, show.Record, ep); ok {
		er, found = idx.bySE[[2]int{s, e}]
		if !found && abs != nil {
			er, found = idx.byAbs[*abs]
		}
	} else if ep.SeasonNumber != nil && ep.EpisodeNumber != nil {
		er, found = idx.bySE[[2]int{*ep.SeasonNumber, *ep.EpisodeNumber}]
	}
	if !found && ep.AbsoluteNumber != nil {
		er, found = idx.byAbs[*ep.AbsoluteNumber]
	}
	if reader != nil {
		if files, err := m.o.Items.Files(ctx, ep.ID); err == nil && len(files) > 0 {
			local, ok, err := reader.ReadEpisode(files[0].Path)
			switch {
			case err != nil:
				m.log.Warn("read episode nfo", "item", ep.ID, "err", err)
			case ok:
				er = overlayEpisode(er, local, preferLocal || !found)
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	locked := lockSet(ep.LockedFields)
	changed := false
	if er.Title != "" && !locked[FieldTitle] && ep.Title != er.Title {
		ep.Title, changed = er.Title, true
	}
	if er.AirDate != "" && !locked[FieldReleased] && ep.AirDate != er.AirDate {
		ep.AirDate, changed = er.AirDate, true
	}
	if ep.MatchState == StateUnmatched || ep.MatchState == "" {
		ep.MatchState, changed = StateMatched, true
	}
	if changed {
		if err := m.o.Items.UpdateItem(ctx, ep); err != nil {
			return fmt.Errorf("update episode %d: %w", ep.ID, err)
		}
	}
	d := Details{Overview: er.Overview, Released: er.AirDate, Images: er.Images}
	return m.o.Store.SaveFields(ctx, ep.ID, d,
		unlockedFields([]string{FieldOverview, FieldReleased, ImageField(ImageThumb)}, ep.LockedFields))
}

// overlayEpisode puts local NFO values over provider values (or only into gaps).
func overlayEpisode(base, local EpisodeRecord, localWins bool) EpisodeRecord {
	pick := func(b, l string) string {
		if l != "" && (localWins || b == "") {
			return l
		}
		return b
	}
	base.Title = pick(base.Title, local.Title)
	base.Overview = pick(base.Overview, local.Overview)
	base.AirDate = pick(base.AirDate, local.AirDate)
	if len(local.Images) > 0 && (localWins || len(base.Images) == 0) {
		base.Images = local.Images
	}
	return base
}
```

In `server/internal/metadata/matcher.go`, inside `apply`, insert this block right after the
`m.o.Store.Save(...)` error check and before the `m.o.Images` prefetch:
```go
	if it.Kind == KindShow {
		if err := m.applyChildren(ctx, it, rec, preferLocal); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: Implement the listener and background start**

`server/internal/metadata/listener.go`:
```go
package metadata

import (
	"context"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// ItemsChanged implements scanner.ScanListener. It only resolves IDs to
// their movie or show and queues them; matching happens in the background.
func (m *Matcher) ItemsChanged(ctx context.Context, libraryID int64, itemIDs []int64) {
	top := map[int64]media.Item{} // any visited id -> its top-level item
	queued := map[int64]bool{}
	for _, id := range itemIDs {
		var chain []int64
		cur := id
		var it media.Item
		ok := false
		for hops := 0; hops < 4; hops++ {
			if t, hit := top[cur]; hit {
				it, ok = t, true
				break
			}
			x, err := m.o.Items.GetItem(ctx, cur)
			if err != nil {
				m.log.Warn("items changed: resolve", "library", libraryID, "item", id, "err", err)
				break
			}
			chain = append(chain, cur)
			if x.ParentID == 0 {
				it, ok = x, true
				break
			}
			cur = x.ParentID
		}
		if !ok {
			continue
		}
		for _, c := range chain {
			top[c] = it
		}
		if matchable(it.Kind) && !queued[it.ID] {
			queued[it.ID] = true
			m.queue.Enqueue(it.ID, false)
		}
	}
}

// Enqueue schedules a background match/refresh of one item.
func (m *Matcher) Enqueue(itemID int64, force bool) { m.queue.Enqueue(itemID, force) }

// QueueLen is the number of pending or running matches.
func (m *Matcher) QueueLen() int { return m.queue.Len() }

// Run processes the queue and runs the maintenance loop (sweep at start and
// daily, cache pruning). It blocks until ctx ends and the workers stop.
func (m *Matcher) Run(ctx context.Context) {
	go m.maintain(ctx)
	m.queue.Run(ctx)
}

// Start is Run in the background; it returns at once.
func (m *Matcher) Start(ctx context.Context) { go m.Run(ctx) }

// Sweep queues every unmatched movie and show of every library.
func (m *Matcher) Sweep(ctx context.Context) error {
	libs, err := m.o.Libraries.List(ctx)
	if err != nil {
		return err
	}
	for _, l := range libs {
		items, err := m.o.Items.ItemsNeedingMatch(ctx, l.ID)
		if err != nil {
			return err
		}
		for _, it := range items {
			if matchable(it.Kind) {
				m.queue.Enqueue(it.ID, false)
			}
		}
	}
	return nil
}

func (m *Matcher) maintain(ctx context.Context) {
	if err := m.Sweep(ctx); err != nil && ctx.Err() == nil {
		m.log.Warn("metadata sweep", "err", err)
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.Sweep(ctx); err != nil {
				m.log.Warn("metadata sweep", "err", err)
			}
			if n, err := m.o.Store.PruneCache(ctx); err != nil {
				m.log.Warn("prune metadata cache", "err", err)
			} else if n > 0 {
				m.log.Info("pruned metadata cache", "rows", n)
			}
		}
	}
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): season/episode metadata, scan listener and background sweep"
```

---

### Task 14: HTTP module: item metadata, images, settings

**Files:**
- Create: `server/internal/metadata/images.go`, `server/internal/metadata/module.go`, `server/internal/metadata/api_read.go`, `server/internal/metadata/api_settings.go`
- Test: `server/internal/metadata/api_test.go`

**Interfaces:**
- Consumes: `artwork.Store`, `Store`, `SettingsStore`, `Matcher`, `Registry`, `library.Visibility`, `library.CanSee`, `media.ErrNotFound`, `httpx.*`, `authctx.*`, chi.
- Produces:
  - `NewImages(s *Store, a *artwork.Store, log *slog.Logger) *Images`; `(*Images) PrefetchItem(ctx, itemID) error` (satisfies `ImagePrefetcher`); `(*Images) File(ctx, row ImageRow, w int) (path, etag string, err error)` (downloads on first use, records `ok`/`failed`).
  - `type ModuleOptions struct{ Matcher *Matcher; Registry *Registry; Items ItemStore; Libraries LibraryStore; Store *Store; Settings *SettingsStore; Images *Images; Visibility library.Visibility; Log *slog.Logger }`; `NewModule(o) *Module`; `(*Module) Name() string` = `"metadata"`; `(*Module) Mount(r chi.Router, g authctx.Guard)`.
  - Routes (flat, next to 1C's `GET /items/{id}`):
    - `GET /api/v1/items/{id}/metadata` (user; 404 when the library is not visible) → `metadataResponse` JSON:
      `item_id, kind, title, original_title, sort_title, year, season_number?, episode_number?, air_date?, match_state, locked_fields[], external_ids{}, overview, released, content_rating, genres[], studios[], tags[], alt_titles{}, ratings{}, people[{id,name,role,character?,image_url?}], images{type:[{id,url,lang?,width?,height?}]}, sources{}, refreshed_at?`. Image URLs are `/api/v1/images/{id}`.
    - `GET /api/v1/images/{id}?w=300` (user) → the image bytes (smallest cached variant ≥ w, else original), `Cache-Control: private, max-age=31536000, immutable`, `ETag: "<sha16>-<variantWidth>"`, 304 on `If-None-Match`; `w` must be 1..4096 (400 `bad_request`); 502 `image_unavailable` when the source cannot be fetched. Item images obey library visibility; person images are visible to any user.
    - `GET /api/v1/metadata/settings` (admin) → `{"tmdb_api_key":{"set","masked","source"},"fanart_api_key":{...},"language","fallback_language","certification_countries":[],"match_threshold","prefer_local":{"<libraryID>":bool}}` (secrets never returned).
    - `PUT /api/v1/metadata/settings` (admin) with any subset of `{"tmdb_api_key","fanart_api_key","language","fallback_language","certification_countries","match_threshold","prefer_local"}` → the GET body; bad values → 400 `invalid_setting`.

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/api_test.go`:
```go
package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/app"
	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/metadata/artwork"
)

var _ app.Module = (*Module)(nil)

// fakeItemsModule registers routes the way 1C's media module does.
type fakeItemsModule struct{}

func (fakeItemsModule) Name() string { return "media" }
func (fakeItemsModule) Mount(r chi.Router, g authctx.Guard) {
	r.With(g.RequireUser).Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]string{"from": "media-module"})
	})
}

// visibility lets admins see everything and others only the listed libraries.
type visibility struct{ ids []int64 }

func (v visibility) VisibleLibraryIDs(_ context.Context, p authctx.Principal) ([]int64, error) {
	if p.IsAdmin {
		return []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, nil
	}
	return v.ids, nil
}

var (
	admin = authctx.Principal{UserID: 1, IsAdmin: true}
	user  = authctx.Principal{UserID: 2, ProfileID: 3}
)

type apiEnv struct {
	*testEnv
	h func(p authctx.Principal) http.Handler
}

func newAPIEnv(t *testing.T, vis visibility, providers ...Provider) *apiEnv {
	t.Helper()
	e := newEnv(t, LibMovies, providers...)
	images := NewImages(e.store, artwork.New(t.TempDir(), nil), nil)
	mod := NewModule(ModuleOptions{Matcher: e.m, Registry: e.reg, Items: e.items, Libraries: e.libs, Store: e.store,
		Settings: e.settings, Images: images, Visibility: vis})
	log := slog.New(slog.DiscardHandler)
	return &apiEnv{testEnv: e, h: func(p authctx.Principal) http.Handler {
		return app.New(config.Config{}, e.d, log, authctx.StaticGuard{P: p}, fakeItemsModule{}, mod).Handler()
	}}
}

func call(h http.Handler, method, path string, body any, hdr ...string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func path(format string, id int64) string { return strings.Replace(format, "{id}", itoa(id), 1) }

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestGetMetadata(t *testing.T) {
	e := newAPIEnv(t, visibility{})
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", Year: 1999, ExternalIDs: map[string]string{"tmdb": "603"}})
	if err := e.store.Save(context.Background(), it.ID, matrixDetails(), nil); err != nil {
		t.Fatal(err)
	}
	rec := call(e.h(admin), "GET", path("/api/v1/items/{id}/metadata", it.ID), nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got metadataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Matrix" || got.ExternalIDs["tmdb"] != "603" || got.Overview == "" || len(got.Genres) != 2 {
		t.Fatalf("%+v", got)
	}
	if len(got.Images[ImagePoster]) != 2 || !strings.HasPrefix(got.Images[ImagePoster][0].URL, "/api/v1/images/") {
		t.Fatalf("images %+v", got.Images)
	}
	if got.People[0].Character != "Neo" || !strings.HasPrefix(got.People[0].ImageURL, "/api/v1/images/") || got.People[1].ImageURL != "" {
		t.Fatalf("people %+v", got.People)
	}
	if got.LockedFields == nil || got.Tags == nil {
		t.Fatal("lists must be [] not null")
	}
}

func TestGetMetadataVisibility(t *testing.T) {
	e := newAPIEnv(t, visibility{})
	it := e.item(t, media.Item{Kind: KindMovie, Title: "Hidden"})
	if c := call(e.h(user), "GET", path("/api/v1/items/{id}/metadata", it.ID), nil).Code; c != 404 {
		t.Fatalf("invisible library: %d", c)
	}
	e2 := newAPIEnv(t, visibility{ids: []int64{1}})
	it2 := e2.item(t, media.Item{Kind: KindMovie, Title: "Visible"})
	if c := call(e2.h(user), "GET", path("/api/v1/items/{id}/metadata", it2.ID), nil).Code; c != 200 {
		t.Fatalf("visible library: %d (library id %d)", c, e2.lib.ID)
	}
	if c := call(e.h(admin), "GET", "/api/v1/items/999999/metadata", nil).Code; c != 404 {
		t.Fatalf("missing item: %d", c)
	}
	if c := call(e.h(admin), "GET", "/api/v1/items/abc/metadata", nil).Code; c != 400 {
		t.Fatalf("bad id: %d", c)
	}
}

func TestItemsRoutesCoexist(t *testing.T) {
	e := newAPIEnv(t, visibility{})
	rec := call(e.h(admin), "GET", "/api/v1/items/5", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "media-module") {
		t.Fatalf("media module route broken: %d %s", rec.Code, rec.Body)
	}
}

func pngServer(t *testing.T) *httptest.Server {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	var b bytes.Buffer
	png.Encode(&b, img)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.png" {
			http.NotFound(w, r)
			return
		}
		w.Write(b.Bytes())
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetImage(t *testing.T) {
	srv := pngServer(t)
	e := newAPIEnv(t, visibility{})
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix"})
	ctx := context.Background()
	d := Details{Images: []Image{{Type: ImagePoster, URL: srv.URL + "/p.png"}, {Type: ImageBackdrop, URL: srv.URL + "/missing.png"}}}
	if err := e.store.SaveFields(ctx, it.ID, d, []string{ImageField(ImagePoster), ImageField(ImageBackdrop)}); err != nil {
		t.Fatal(err)
	}
	v := e.view(t, it.ID)
	var posterID, backdropID int64
	for _, im := range v.Images {
		if im.Type == ImagePoster {
			posterID = im.ID
		} else {
			backdropID = im.ID
		}
	}
	h := e.h(admin)
	rec := call(h, "GET", path("/api/v1/images/{id}", posterID)+"?w=300", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("ETag") == "" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if cfg, _, err := image.DecodeConfig(rec.Body); err != nil || cfg.Width != 300 {
		t.Fatalf("variant width %d err %v", cfg.Width, err)
	}
	if c := call(h, "GET", path("/api/v1/images/{id}", posterID)+"?w=300", nil, "If-None-Match", rec.Header().Get("ETag")).Code; c != 304 {
		t.Fatalf("conditional GET: %d", c)
	}
	if c := call(h, "GET", path("/api/v1/images/{id}", posterID)+"?w=abc", nil).Code; c != 400 {
		t.Fatalf("bad w: %d", c)
	}
	if c := call(h, "GET", path("/api/v1/images/{id}", backdropID), nil).Code; c != 502 {
		t.Fatalf("unfetchable source: %d", c)
	}
	if c := call(e.h(user), "GET", path("/api/v1/images/{id}", posterID), nil).Code; c != 404 {
		t.Fatalf("invisible image: %d", c)
	}
}

func TestSettingsAPI(t *testing.T) {
	e := newAPIEnv(t, visibility{})
	h := e.h(admin)
	rec := call(h, "PUT", "/api/v1/metadata/settings", map[string]any{
		"tmdb_api_key": "abcdefghijklmnop1234", "language": "en-GB",
		"prefer_local": map[string]bool{itoa(e.lib.ID): false},
	})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, "abcdefghijklmnop1234") || !strings.Contains(body, `"masked":"****1234"`) ||
		!strings.Contains(body, `"language":"en-GB"`) || !strings.Contains(body, `"`+itoa(e.lib.ID)+`":false`) {
		t.Fatalf("body %s", body)
	}
	if c := call(h, "PUT", "/api/v1/metadata/settings", map[string]any{"language": "english"}).Code; c != 400 {
		t.Fatalf("invalid language: %d", c)
	}
	if c := call(h, "PUT", "/api/v1/metadata/settings", map[string]any{"bogus": 1}).Code; c != 400 {
		t.Fatalf("unknown field: %d", c)
	}
	if c := call(e.h(user), "GET", "/api/v1/metadata/settings", nil).Code; c != 403 {
		t.Fatalf("non-admin: %d", c)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewImages`, `undefined: NewModule`, `undefined: metadataResponse`

- [ ] **Step 3: Implement the images service**

`server/internal/metadata/images.go`:
```go
package metadata

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Lisimflix/lisimflix/server/internal/metadata/artwork"
)

// Images downloads artwork on first use and after matches.
type Images struct {
	store *Store
	art   *artwork.Store
	log   *slog.Logger
}

var _ ImagePrefetcher = (*Images)(nil)

// NewImages returns the images service. log may be nil.
func NewImages(s *Store, a *artwork.Store, log *slog.Logger) *Images {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Images{store: s, art: a, log: log}
}

func (im *Images) ensure(ctx context.Context, row ImageRow) error {
	if im.art.Has(row.SHA) {
		if row.Status != "ok" {
			return im.store.SetImageStatus(ctx, row.ID, "ok")
		}
		return nil
	}
	err := im.art.Ensure(ctx, row.SHA, row.SourceURL, row.Type)
	status := "ok"
	if err != nil {
		status = "failed"
	}
	if serr := im.store.SetImageStatus(ctx, row.ID, status); serr != nil && err == nil {
		err = serr
	}
	return err
}

// PrefetchItem downloads the primary image of each type for an item.
func (im *Images) PrefetchItem(ctx context.Context, itemID int64) error {
	rows, err := im.store.PrimaryImages(ctx, itemID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := im.ensure(ctx, r); err != nil {
			im.log.Warn("prefetch image", "image", r.ID, "err", err)
		}
	}
	return nil
}

// File returns the cached file for row at width w (0 = original) and its ETag.
func (im *Images) File(ctx context.Context, row ImageRow, w int) (string, string, error) {
	if err := im.ensure(ctx, row); err != nil {
		return "", "", err
	}
	p, vw, err := im.art.Path(row.SHA, row.Type, w)
	if err != nil {
		return "", "", err
	}
	return p, fmt.Sprintf(`"%s-%d"`, row.SHA[:16], vw), nil
}
```

- [ ] **Step 4: Implement the module and read handlers**

`server/internal/metadata/module.go`:
```go
package metadata

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

// ModuleOptions wires the HTTP module.
type ModuleOptions struct {
	Matcher    *Matcher
	Registry   *Registry
	Items      ItemStore
	Libraries  LibraryStore
	Store      *Store
	Settings   *SettingsStore
	Images     *Images
	Visibility library.Visibility // *auth.Service once plan 1B is wired; library.AdminVisibility before
	Log        *slog.Logger
}

// Module serves the metadata API.
type Module struct {
	o   ModuleOptions
	log *slog.Logger
}

// NewModule returns the metadata HTTP module.
func NewModule(o ModuleOptions) *Module {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{o: o, log: log.With("module", "metadata")}
}

// Name implements app.Module.
func (m *Module) Name() string { return "metadata" }

// Mount implements app.Module. Routes are flat (no r.Route("/items")) so they
// sit beside the media module's GET /items/{id}.
func (m *Module) Mount(r chi.Router, g authctx.Guard) {
	r.With(g.RequireUser).Get("/items/{id}/metadata", m.getMetadata)
	r.With(g.RequireUser).Get("/images/{id}", m.getImage)
	r.With(g.RequireAdmin).Get("/metadata/settings", m.getSettings)
	r.With(g.RequireAdmin).Put("/metadata/settings", m.putSettings)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func (m *Module) internal(w http.ResponseWriter, err error) {
	m.log.Error("metadata api", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func (m *Module) visible(r *http.Request, libraryID int64) (bool, error) {
	p, _ := authctx.FromContext(r.Context())
	return library.CanSee(r.Context(), m.o.Visibility, p, libraryID)
}

// loadItem reads {id}, enforcing library visibility (invisible = 404).
func (m *Module) loadItem(w http.ResponseWriter, r *http.Request) (media.Item, bool) {
	id, ok := pathID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid item id")
		return media.Item{}, false
	}
	it, err := m.o.Items.GetItem(r.Context(), id)
	if errors.Is(err, media.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such item")
		return media.Item{}, false
	}
	if err != nil {
		m.internal(w, err)
		return media.Item{}, false
	}
	ok, err = m.visible(r, it.LibraryID)
	if err != nil {
		m.internal(w, err)
		return media.Item{}, false
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such item")
		return media.Item{}, false
	}
	return it, true
}
```

`server/internal/metadata/api_read.go`:
```go
package metadata

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
	"github.com/Lisimflix/lisimflix/server/internal/media"
)

type imageJSON struct {
	ID     int64  `json:"id"`
	URL    string `json:"url"`
	Lang   string `json:"lang,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type personJSON struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Character string `json:"character,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
}

type metadataResponse struct {
	ItemID        int64                  `json:"item_id"`
	Kind          string                 `json:"kind"`
	Title         string                 `json:"title"`
	OriginalTitle string                 `json:"original_title"`
	SortTitle     string                 `json:"sort_title"`
	Year          int                    `json:"year"`
	SeasonNumber  *int                   `json:"season_number,omitempty"`
	EpisodeNumber *int                   `json:"episode_number,omitempty"`
	AirDate       string                 `json:"air_date,omitempty"`
	MatchState    string                 `json:"match_state"`
	LockedFields  []string               `json:"locked_fields"`
	ExternalIDs   map[string]string      `json:"external_ids"`
	Overview      string                 `json:"overview"`
	Released      string                 `json:"released"`
	ContentRating string                 `json:"content_rating"`
	Genres        []string               `json:"genres"`
	Studios       []string               `json:"studios"`
	Tags          []string               `json:"tags"`
	AltTitles     map[string]string      `json:"alt_titles"`
	Ratings       map[string]float64     `json:"ratings"`
	People        []personJSON           `json:"people"`
	Images        map[string][]imageJSON `json:"images"`
	Sources       map[string]string      `json:"sources"`
	RefreshedAt   string                 `json:"refreshed_at,omitempty"`
}

func imageURL(id int64) string { return "/api/v1/images/" + strconv.FormatInt(id, 10) }

func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (m *Module) metadataJSON(ctx context.Context, it media.Item) (metadataResponse, error) {
	v, _, err := m.o.Store.Load(ctx, it.ID)
	if err != nil {
		return metadataResponse{}, err
	}
	resp := metadataResponse{
		ItemID: it.ID, Kind: string(it.Kind), Title: it.Title, OriginalTitle: it.OriginalTitle,
		SortTitle: it.SortTitle, Year: it.Year, SeasonNumber: it.SeasonNumber, EpisodeNumber: it.EpisodeNumber,
		AirDate: it.AirDate, MatchState: it.MatchState, LockedFields: strs(it.LockedFields),
		ExternalIDs: PublicIDs(it.ExternalIDs), Overview: v.Overview, Released: v.Released,
		ContentRating: v.ContentRating, Genres: strs(v.Genres), Studios: strs(v.Studios), Tags: strs(v.Tags),
		AltTitles: v.AltTitles, Ratings: v.Ratings, People: []personJSON{}, Images: map[string][]imageJSON{},
		Sources: v.Sources, RefreshedAt: v.RefreshedAt,
	}
	if resp.AltTitles == nil {
		resp.AltTitles = map[string]string{}
	}
	if resp.Ratings == nil {
		resp.Ratings = map[string]float64{}
	}
	if resp.Sources == nil {
		resp.Sources = map[string]string{}
	}
	for _, p := range v.People {
		pj := personJSON{ID: p.ID, Name: p.Name, Role: p.Role, Character: p.Character}
		if p.ImageID > 0 {
			pj.ImageURL = imageURL(p.ImageID)
		}
		resp.People = append(resp.People, pj)
	}
	for _, im := range v.Images {
		resp.Images[im.Type] = append(resp.Images[im.Type], imageJSON{ID: im.ID, URL: imageURL(im.ID), Lang: im.Lang, Width: im.Width, Height: im.Height})
	}
	return resp, nil
}

func (m *Module) getMetadata(w http.ResponseWriter, r *http.Request) {
	it, ok := m.loadItem(w, r)
	if !ok {
		return
	}
	resp, err := m.metadataJSON(r.Context(), it)
	if err != nil {
		m.internal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (m *Module) getImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid image id")
		return
	}
	ctx := r.Context()
	row, err := m.o.Store.Image(ctx, id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such image")
		return
	}
	if err != nil {
		m.internal(w, err)
		return
	}
	if row.ItemID != 0 {
		it, err := m.o.Items.GetItem(ctx, row.ItemID)
		if err != nil {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "no such image")
			return
		}
		ok, err := m.visible(r, it.LibraryID)
		if err != nil {
			m.internal(w, err)
			return
		}
		if !ok {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "no such image")
			return
		}
	}
	width := 0
	if s := r.URL.Query().Get("w"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 4096 {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "w must be between 1 and 4096")
			return
		}
		width = n
	}
	p, etag, err := m.o.Images.File(ctx, row, width)
	if err != nil {
		m.log.Warn("image unavailable", "image", row.ID, "err", err)
		httpx.WriteError(w, http.StatusBadGateway, "image_unavailable", "the image could not be fetched")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		m.internal(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		m.internal(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, "", st.ModTime(), f)
}
```

`server/internal/metadata/api_settings.go`:
```go
package metadata

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

type settingsResponse struct {
	TMDBAPIKey             SecretStatus    `json:"tmdb_api_key"`
	FanartAPIKey           SecretStatus    `json:"fanart_api_key"`
	Language               string          `json:"language"`
	FallbackLanguage       string          `json:"fallback_language"`
	CertificationCountries []string        `json:"certification_countries"`
	MatchThreshold         float64         `json:"match_threshold"`
	PreferLocal            map[string]bool `json:"prefer_local"`
}

type settingsRequest struct {
	TMDBAPIKey             *string         `json:"tmdb_api_key"`
	FanartAPIKey           *string         `json:"fanart_api_key"`
	Language               *string         `json:"language"`
	FallbackLanguage       *string         `json:"fallback_language"`
	CertificationCountries []string        `json:"certification_countries"`
	MatchThreshold         *float64        `json:"match_threshold"`
	PreferLocal            map[string]bool `json:"prefer_local"`
}

func (m *Module) getSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := m.o.Settings.Load(ctx)
	if err != nil {
		m.internal(w, err)
		return
	}
	tm, fa, err := m.o.Settings.SecretStatuses(ctx)
	if err != nil {
		m.internal(w, err)
		return
	}
	libs, err := m.o.Libraries.List(ctx)
	if err != nil {
		m.internal(w, err)
		return
	}
	prefer := map[string]bool{}
	for _, l := range libs {
		v, err := m.o.Settings.PreferLocal(ctx, l.ID)
		if err != nil {
			m.internal(w, err)
			return
		}
		prefer[strconv.FormatInt(l.ID, 10)] = v
	}
	httpx.WriteJSON(w, http.StatusOK, settingsResponse{
		TMDBAPIKey: tm, FanartAPIKey: fa, Language: st.Language, FallbackLanguage: st.FallbackLanguage,
		CertificationCountries: st.CertCountries, MatchThreshold: st.MatchThreshold, PreferLocal: prefer,
	})
}

func (m *Module) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	u := SettingsUpdate{TMDBKey: req.TMDBAPIKey, FanartKey: req.FanartAPIKey, Language: req.Language,
		FallbackLanguage: req.FallbackLanguage, CertCountries: req.CertificationCountries, MatchThreshold: req.MatchThreshold}
	if len(req.PreferLocal) > 0 {
		u.PreferLocal = map[int64]bool{}
		for k, v := range req.PreferLocal {
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil || id <= 0 {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_setting", "prefer_local keys must be library ids")
				return
			}
			u.PreferLocal[id] = v
		}
	}
	if err := m.o.Settings.Update(r.Context(), u); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_setting", ve.Error())
			return
		}
		m.internal(w, err)
		return
	}
	m.getSettings(w, r)
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): item metadata, image and settings API"
```

---

### Task 15: Fix match, metadata edits with locks, refresh

**Files:**
- Create: `server/internal/metadata/api_write.go`
- Modify: `server/internal/metadata/module.go` (`Mount`: four added routes)
- Test: `server/internal/metadata/api_write_test.go`

**Interfaces:**
- Consumes: `Matcher.Search`, `Matcher.ApplyMatch`, `Matcher.Enqueue`, `Store.SaveFields`, `ItemStore.UpdateItem`, `Module.loadItem`, `Module.metadataJSON`.
- Produces (all admin-only):
  - `GET /api/v1/items/{id}/match/search?q=&year=` → `{"query":{"title","year"},"candidates":[{"provider","ids","title","year","provider_score","score"}]}` (defaults: item title and year; 400 `unsupported_kind` for seasons/episodes/videos; 502 `provider_error`).
  - `POST /api/v1/items/{id}/match` body `{"provider":"tmdb","ids":{"tmdb":"603"}}` → 200 metadata JSON; item becomes `manual`. 400 `invalid_match`, 422 `match_not_found`, 409 `provider_not_configured`, 502 `provider_error`.
  - `PATCH /api/v1/items/{id}/metadata` body any of `title, original_title, sort_title, year, overview, released, content_rating, genres, studios, tags` plus `unlock: [field...]` → 200 metadata JSON. Every edited field is added to `LockedFields`; `unlock` removes locks (so the next refresh may overwrite). 400 `invalid_field` on bad values (nothing is written).
  - `POST /api/v1/items/{id}/refresh` → 202 `{"status":"queued"}` (background, bypasses the response cache).

- [ ] **Step 1: Write the failing tests**

`server/internal/metadata/api_write_test.go`:
```go
package metadata

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/media"
)

func TestMatchSearchAPI(t *testing.T) {
	e := newAPIEnv(t, visibility{}, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "the matrix", Year: 1999})
	rec := call(e.h(admin), "GET", path("/api/v1/items/{id}/match/search", it.ID), nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Query      struct{ Title string; Year int } `json:"query"`
		Candidates []struct {
			Provider string            `json:"provider"`
			IDs      map[string]string `json:"ids"`
			Score    float64           `json:"score"`
		} `json:"candidates"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Query.Title != "the matrix" || got.Query.Year != 1999 || len(got.Candidates) != 2 || got.Candidates[0].IDs["tmdb"] != "603" {
		t.Fatalf("%+v", got)
	}
	rec = call(e.h(admin), "GET", path("/api/v1/items/{id}/match/search", it.ID)+"?q=The+Matrix+Reloaded&year=2003", nil)
	if !strings.Contains(rec.Body.String(), `"title":"The Matrix Reloaded","year":2003`) {
		t.Fatalf("query params ignored: %s", rec.Body)
	}
	if c := call(e.h(user), "GET", path("/api/v1/items/{id}/match/search", it.ID), nil).Code; c != 403 {
		t.Fatalf("non-admin: %d", c)
	}
	season := e.item(t, media.Item{Kind: KindSeason, ParentID: it.ID, SeasonNumber: intp(1)})
	if c := call(e.h(admin), "GET", path("/api/v1/items/{id}/match/search", season.ID), nil).Code; c != 400 {
		t.Fatalf("season: %d", c)
	}
}

func TestApplyMatchAPI(t *testing.T) {
	e := newAPIEnv(t, visibility{}, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "Matrx"})
	h := e.h(admin)
	if c := call(h, "POST", path("/api/v1/items/{id}/match", it.ID), map[string]any{"provider": "nope", "ids": map[string]string{"tmdb": "603"}}).Code; c != 400 {
		t.Fatalf("bad provider: %d", c)
	}
	if c := call(h, "POST", path("/api/v1/items/{id}/match", it.ID), map[string]any{"provider": "tmdb", "ids": map[string]string{"tmdb": "1"}}).Code; c != 422 {
		t.Fatalf("unknown title: %d", c)
	}
	rec := call(h, "POST", path("/api/v1/items/{id}/match", it.ID), map[string]any{"provider": "tmdb", "ids": map[string]string{"tmdb": "603"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"match_state":"manual"`) || !strings.Contains(rec.Body.String(), `"title":"The Matrix"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPatchMetadataLocksFields(t *testing.T) {
	e := newAPIEnv(t, visibility{}, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix", ExternalIDs: map[string]string{"tmdb": "603"}})
	h := e.h(admin)
	ctx := context.Background()
	rec := call(h, "PATCH", path("/api/v1/items/{id}/metadata", it.ID), map[string]any{
		"title": "Matrix (Family Cut)", "overview": "Our own words.", "genres": []string{"Family"},
	})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := e.get(t, it.ID)
	locks := strings.Join(got.LockedFields, ",")
	if got.Title != "Matrix (Family Cut)" || locks != "genres,overview,title" {
		t.Fatalf("title %q locks %q", got.Title, locks)
	}
	if err := e.m.MatchItem(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	v := e.view(t, it.ID)
	if e.get(t, it.ID).Title != "Matrix (Family Cut)" || v.Overview != "Our own words." || v.Genres[0] != "Family" || v.Sources[FieldOverview] != "manual" {
		t.Fatalf("edits did not survive a refresh: %+v", v)
	}
	if c := call(h, "PATCH", path("/api/v1/items/{id}/metadata", it.ID), map[string]any{"unlock": []string{"overview"}}).Code; c != 200 {
		t.Fatalf("unlock: %d", c)
	}
	if err := e.m.MatchItem(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	if v := e.view(t, it.ID); v.Overview != "A hacker learns the truth." {
		t.Fatalf("unlocked overview not refreshed: %q", v.Overview)
	}
}

func TestPatchMetadataValidation(t *testing.T) {
	e := newAPIEnv(t, visibility{})
	it := e.item(t, media.Item{Kind: KindMovie, Title: "Keep"})
	h := e.h(admin)
	for name, body := range map[string]map[string]any{
		"empty title": {"title": " "},
		"bad year":    {"year": 3000, "title": "Changed"},
		"bad date":    {"released": "31/03/1999"},
		"bad unlock":  {"unlock": []string{"password"}},
	} {
		if c := call(h, "PATCH", path("/api/v1/items/{id}/metadata", it.ID), body).Code; c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	if got := e.get(t, it.ID); got.Title != "Keep" || len(got.LockedFields) != 0 {
		t.Fatalf("rejected patch changed the item: %+v", got)
	}
}

func TestRefreshQueues(t *testing.T) {
	e := newAPIEnv(t, visibility{}, tmdbFake())
	it := e.item(t, media.Item{Kind: KindMovie, Title: "The Matrix"})
	rec := call(e.h(admin), "POST", path("/api/v1/items/{id}/refresh", it.ID), nil)
	if rec.Code != 202 || e.m.QueueLen() != 1 {
		t.Fatalf("%d queue=%d", rec.Code, e.m.QueueLen())
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL (the new routes return 404 `not_found`).

- [ ] **Step 3: Implement**

Add to `Mount` in `server/internal/metadata/module.go`, after the existing four lines:
```go
	r.With(g.RequireAdmin).Patch("/items/{id}/metadata", m.patchMetadata)
	r.With(g.RequireAdmin).Get("/items/{id}/match/search", m.matchSearch)
	r.With(g.RequireAdmin).Post("/items/{id}/match", m.applyMatch)
	r.With(g.RequireAdmin).Post("/items/{id}/refresh", m.refresh)
```

`server/internal/metadata/api_write.go`:
```go
package metadata

import (
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type candidateJSON struct {
	Provider      string            `json:"provider"`
	IDs           map[string]string `json:"ids"`
	Title         string            `json:"title"`
	Year          int               `json:"year"`
	ProviderScore float64           `json:"provider_score"`
	Score         float64           `json:"score"`
}

func (m *Module) matchSearch(w http.ResponseWriter, r *http.Request) {
	it, ok := m.loadItem(w, r)
	if !ok {
		return
	}
	if !matchable(it.Kind) {
		httpx.WriteError(w, http.StatusBadRequest, "unsupported_kind", "only movies and shows can be matched")
		return
	}
	q := r.URL.Query()
	title := strings.TrimSpace(q.Get("q"))
	if title == "" {
		title = it.Title
	}
	year := it.Year
	if s := q.Get("year"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 2200 {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "year must be a number")
			return
		}
		year = n
	}
	res, err := m.o.Matcher.Search(r.Context(), it.ID, title, year)
	if err != nil {
		m.log.Warn("match search", "item", it.ID, "err", err)
		httpx.WriteError(w, http.StatusBadGateway, "provider_error", "metadata providers did not answer")
		return
	}
	cands := []candidateJSON{}
	for _, c := range res {
		cands = append(cands, candidateJSON{Provider: c.Provider, IDs: PublicIDs(c.IDs), Title: c.Title, Year: c.Year,
			ProviderScore: c.Score, Score: c.Match})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"query":      map[string]any{"title": title, "year": year},
		"candidates": cands,
	})
}

func (m *Module) applyMatch(w http.ResponseWriter, r *http.Request) {
	it, ok := m.loadItem(w, r)
	if !ok {
		return
	}
	var req struct {
		Provider string            `json:"provider"`
		IDs      map[string]string `json:"ids"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := m.o.Matcher.ApplyMatch(r.Context(), it.ID, req.Provider, req.IDs)
	switch {
	case err == nil:
	case errors.Is(err, ErrBadMatch), errors.Is(err, ErrUnsupportedKind), errors.Is(err, ErrNoUsableID):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_match", err.Error())
		return
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "match_not_found", "the provider has no title with these ids")
		return
	case errors.Is(err, ErrNotConfigured):
		httpx.WriteError(w, http.StatusConflict, "provider_not_configured", "set the provider's API key first")
		return
	default:
		m.log.Warn("apply match", "item", it.ID, "err", err)
		httpx.WriteError(w, http.StatusBadGateway, "provider_error", "the metadata provider did not answer")
		return
	}
	m.respondItem(w, r, it.ID)
}

func (m *Module) respondItem(w http.ResponseWriter, r *http.Request, id int64) {
	it, err := m.o.Items.GetItem(r.Context(), id)
	if err != nil {
		m.internal(w, err)
		return
	}
	resp, err := m.metadataJSON(r.Context(), it)
	if err != nil {
		m.internal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type patchRequest struct {
	Title         *string   `json:"title"`
	OriginalTitle *string   `json:"original_title"`
	SortTitle     *string   `json:"sort_title"`
	Year          *int      `json:"year"`
	Overview      *string   `json:"overview"`
	Released      *string   `json:"released"`
	ContentRating *string   `json:"content_rating"`
	Genres        *[]string `json:"genres"`
	Studios       *[]string `json:"studios"`
	Tags          *[]string `json:"tags"`
	Unlock        []string  `json:"unlock"`
}

func lockable(f string) bool {
	switch f {
	case FieldTitle, FieldOriginalTitle, FieldSortTitle, FieldYear, FieldOverview, FieldReleased,
		FieldContentRating, FieldGenres, FieldStudios, FieldTags, FieldAltTitles, FieldRatings,
		FieldPeople, FieldImages:
		return true
	}
	for _, t := range ImageTypes {
		if f == ImageField(t) {
			return true
		}
	}
	return false
}

func (m *Module) patchMetadata(w http.ResponseWriter, r *http.Request) {
	it, ok := m.loadItem(w, r)
	if !ok {
		return
	}
	var req patchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	bad := func(msg string) { httpx.WriteError(w, http.StatusBadRequest, "invalid_field", msg) }
	// Validate everything before changing anything.
	for _, f := range req.Unlock {
		if !lockable(f) {
			bad("cannot unlock unknown field " + strconv.Quote(f))
			return
		}
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		bad("title cannot be empty")
		return
	}
	if req.Year != nil && *req.Year != 0 && (*req.Year < 1870 || *req.Year > 2200) {
		bad("year must be 0 or between 1870 and 2200")
		return
	}
	if req.Released != nil && *req.Released != "" && !dateRe.MatchString(*req.Released) {
		bad("released must be YYYY-MM-DD")
		return
	}

	locked := lockSet(it.LockedFields)
	for _, f := range req.Unlock {
		delete(locked, f)
	}
	setItem := func(field string, dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
			locked[field] = true
		}
	}
	setItem(FieldTitle, &it.Title, req.Title)
	setItem(FieldOriginalTitle, &it.OriginalTitle, req.OriginalTitle)
	setItem(FieldSortTitle, &it.SortTitle, req.SortTitle)
	if req.Year != nil {
		it.Year = *req.Year
		locked[FieldYear] = true
	}
	d := Details{Sources: map[string]string{}}
	var fields []string
	detail := func(field string) {
		fields = append(fields, field)
		d.Sources[field] = "manual"
		locked[field] = true
	}
	if req.Overview != nil {
		d.Overview = strings.TrimSpace(*req.Overview)
		detail(FieldOverview)
	}
	if req.Released != nil {
		d.Released = *req.Released
		detail(FieldReleased)
	}
	if req.ContentRating != nil {
		d.ContentRating = strings.TrimSpace(*req.ContentRating)
		detail(FieldContentRating)
	}
	if req.Genres != nil {
		d.Genres = *req.Genres
		detail(FieldGenres)
	}
	if req.Studios != nil {
		d.Studios = *req.Studios
		detail(FieldStudios)
	}
	if req.Tags != nil {
		d.Tags = *req.Tags
		detail(FieldTags)
	}
	it.LockedFields = make([]string, 0, len(locked))
	for f := range locked {
		it.LockedFields = append(it.LockedFields, f)
	}
	sort.Strings(it.LockedFields)
	if err := m.o.Items.UpdateItem(r.Context(), it); err != nil {
		m.internal(w, err)
		return
	}
	if len(fields) > 0 {
		if err := m.o.Store.SaveFields(r.Context(), it.ID, d, fields); err != nil {
			m.internal(w, err)
			return
		}
	}
	m.respondItem(w, r, it.ID)
}

func (m *Module) refresh(w http.ResponseWriter, r *http.Request) {
	it, ok := m.loadItem(w, r)
	if !ok {
		return
	}
	m.o.Matcher.Enqueue(it.ID, true)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/metadata
git commit -m "feat(metadata): fix-match, metadata edit and refresh API"
```

---

### Task 16: Wire metadata into the server

**Files:**
- Create: `server/cmd/lisimflix/metadata.go`
- Modify: `server/cmd/lisimflix/main.go` (the `libraryModules` line from 1C and one added line)
- Test: `server/cmd/lisimflix/metadata_test.go`

**Interfaces:**
- Consumes: everything above; 1C's `libraryModules(ctx, cfg, d, log) ([]app.Module, *scanner.Scanner, func())`, `scanner.Scanner.AddListener`, `library.Visibility`, `library.AdminVisibility`; 1B's `authSvc` (`*auth.Service`, a `library.Visibility`) when merged.
- Produces: `metadataModule(ctx, cfg, d, log, scan *scanner.Scanner, vis library.Visibility) (app.Module, func())` in package `main`: builds settings, store, registry (`nfo`, `tmdb` 20 req/s, `fanart` 2 req/s, both with the persisted cache), artwork store under `cfg.ConfigDir/artwork` (file:// allowed only inside `cfg.MediaDir`), images service, matcher (subscribed to the scanner, running in the background) and the HTTP module; the returned func stops the matcher and waits for its workers.

- [ ] **Step 1: Write the failing test**

`server/cmd/lisimflix/metadata_test.go`:
```go
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/app"
	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/library"
)

func TestMetadataModuleWired(t *testing.T) {
	tmp := t.TempDir()
	d := dbtest.Open(t)
	d.SetSetting(context.Background(), settingScanInterval, "0")
	cfg := config.Config{ConfigDir: filepath.Join(tmp, "config"), MediaDir: filepath.Join(tmp, "media"),
		FFprobePath: filepath.Join(tmp, "no-ffprobe")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mods, scan, stopScanner := libraryModules(context.Background(), cfg, d, log)
	defer stopScanner()
	md, stop := metadataModule(context.Background(), cfg, d, log, scan, library.AdminVisibility{Store: library.NewStore(d)})
	defer stop()
	h := app.New(cfg, d, log, authctx.StaticGuard{P: authctx.Principal{UserID: 1, IsAdmin: true}}, append(mods, md)...).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/metadata/settings", nil))
	if rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/items/1/metadata", nil))
	if rec.Code != 404 {
		t.Fatalf("missing item should be a JSON 404 from the metadata module, got %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: metadataModule`

- [ ] **Step 3: Implement**

`server/cmd/lisimflix/metadata.go`:
```go
package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Lisimflix/lisimflix/server/internal/app"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/media"
	"github.com/Lisimflix/lisimflix/server/internal/metadata"
	"github.com/Lisimflix/lisimflix/server/internal/metadata/artwork"
	"github.com/Lisimflix/lisimflix/server/internal/metadata/fanart"
	"github.com/Lisimflix/lisimflix/server/internal/metadata/nfo"
	"github.com/Lisimflix/lisimflix/server/internal/metadata/tmdb"
	"github.com/Lisimflix/lisimflix/server/internal/scanner"
)

// metadataModule builds the metadata pieces (plan 1D), subscribes the matcher
// to the scanner, starts it, and returns the HTTP module and a stop function.
func metadataModule(ctx context.Context, cfg config.Config, d *db.DB, log *slog.Logger,
	scan *scanner.Scanner, vis library.Visibility) (app.Module, func()) {
	mlog := log.With("module", "metadata")
	settings := metadata.NewSettingsStore(d, os.Getenv)
	store := metadata.NewStore(d)

	reg := metadata.NewRegistry()
	reg.Register(nfo.New())
	reg.Register(tmdb.New(tmdb.Options{
		Settings: settings.Load,
		Fetcher:  metadata.NewFetcher(d, "tmdb", metadata.FetcherOptions{RatePerSec: 20, Burst: 20}),
	}))
	reg.Register(fanart.New(fanart.Options{
		Settings: settings.Load,
		Fetcher:  metadata.NewFetcher(d, "fanart", metadata.FetcherOptions{RatePerSec: 2, Burst: 4}),
	}))

	art := artwork.New(filepath.Join(cfg.ConfigDir, "artwork"), nil)
	art.AllowedFileRoots = []string{cfg.MediaDir}
	images := metadata.NewImages(store, art, mlog)

	items, libs := media.NewStore(d), library.NewStore(d)
	matcher := metadata.NewMatcher(metadata.MatcherOptions{
		Registry: reg, Items: items, Libraries: libs, Store: store, Settings: settings, Images: images, Log: log,
	})
	scan.AddListener(matcher)

	mctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		matcher.Run(mctx)
	}()
	stop := func() {
		cancel()
		<-done
	}

	mod := metadata.NewModule(metadata.ModuleOptions{
		Matcher: matcher, Registry: reg, Items: items, Libraries: libs, Store: store,
		Settings: settings, Images: images, Visibility: vis, Log: log,
	})
	return mod, stop
}
```

`NewSettingsStore` gets `os.Getenv` here because `run`'s `getenv` is not passed down by 1C's
`libraryModules`; tests use their own `SettingsStore`. (If you prefer, thread `getenv` through:
add a parameter and pass `getenv` from `run`.)

In `server/cmd/lisimflix/main.go`, inside `run`, change 1C's lines
```go
	mods, _, stopScanner := libraryModules(ctx, cfg, d, log)
	defer stopScanner()
```
to
```go
	mods, scan, stopScanner := libraryModules(ctx, cfg, d, log)
	defer stopScanner()
	mdModule, stopMetadata := metadataModule(ctx, cfg, d, log, scan, library.AdminVisibility{Store: library.NewStore(d)})
	defer stopMetadata()
	mods = append(mods, mdModule)
```
and add the import `"github.com/Lisimflix/lisimflix/server/internal/library"` if `main.go` does
not have it. **If plan 1B is merged** (`run` has `authSvc := auth.NewService(...)` before
`app.New`), move these lines below `authSvc` and pass `authSvc` instead of
`library.AdminVisibility{...}`. Defers run in reverse order, so the matcher stops before the
scanner and both before the database closes.

- [ ] **Step 4: Run all tests and vet**

Run: `make fmt && make test && make vet`
Expected: every package PASS, including `cmd/lisimflix` (`TestServeHealthcheckAndShutdown` still
shuts down cleanly with the matcher running).

- [ ] **Step 5: Verify against real files on a reference installation** (read-only mount; needs 1B for a real login)

```bash
make image
T=$(mktemp -d)
docker run -d --name lisimflix-1d -p 8421:8420 -v $T/config:/config -v $T/cache:/cache \
  -e LISIMFLIX_TMDB_API_KEY -e LISIMFLIX_FANART_API_KEY \
  -v /mnt/user/dataz/media:/media:ro lisimflix:dev
```
Sign in as admin, create Movies and TV libraries (`POST /api/v1/libraries`), wait for the scan,
then check a sample:
- `GET /api/v1/libraries/{id}/items` → most items have `"match_state":"matched"`.
- `GET /api/v1/items/{id}/metadata` for a few movies and shows → overview, genres, cast, poster and backdrop URLs; episodes have titles.
- `GET /api/v1/images/{posterId}?w=300` returns a 300-px JPEG; `ls $T/config/artwork` shows `xx/<sha>/orig` and `w300.jpg`.
- Pick one unmatched item, `GET .../match/search`, `POST .../match`, confirm `manual` and that a rescan does not undo it.
- `docker exec lisimflix-1d sh -c 'ls /media >/dev/null && touch /media/x'` must fail (read-only).
Record mismatches (title, year) as new scoring test cases in `score_test.go`, then
`docker rm -f lisimflix-1d && rm -rf $T`.

- [ ] **Step 6: Commit and open the PR**

```bash
git add server/cmd/lisimflix
git commit -m "feat(metadata): wire metadata matching into the server"
# DO NOT RUN: archived draft. See the catalogue plans map.
git push -u origin HEAD
gh pr create --base main --title "feat(metadata): metadata core with TMDB, Fanart.tv and NFO" --body-file - <<'EOF'
## What changed
Metadata core (plan 1D): provider registry with field-priority merge, TMDB / Fanart.tv / NFO
providers, cached and rate-limited fetcher, background matcher subscribed to the scanner,
artwork cache with resized variants, and the item metadata, image, fix-match, edit and settings API.

## Source
Spec §3.2 Metadata (plan `docs/superpowers/plans/2026-10-24-meta-1-3-metadata.md`)

## How it was tested
`make test` and `make vet` (fixtures only, no network); live TMDB test with `-tags live`;
manual check against the reference library (Task 16 step 5).
EOF
```
The reviewer runs `/code-review` on the branch first, then reads and approves; after approval
the author squash-merges (CONTRIBUTING.md).

---

## Done when

- `make test` and `make vet` pass; CI is green on the PR.
- A scan of the reference installation's Movies and TV libraries leaves most items `matched`, with overview,
  genres, cast, ratings, content rating (AU first), posters/backdrops/logos served from
  `/api/v1/images/{id}`, and episode titles.
- Fix match, edits (locked fields) and manual matches survive a rescan and a refresh.
- No secret appears in logs, errors or `GET /api/v1/metadata/settings`.

## Later (out of scope for 1D)

- TVDB provider (optional per spec) and TVDB/TMDB alternate episode orders.
- Anime providers, ID mapping and absolute numbering: plan 1E plugs in through `Registry.Register`,
  `SetIDMapper`, `SetEpisodeMapper` and `SetPriority(LibAnime, ...)`.
- MusicBrainz (music libraries come after phase 3).
- Bulk metadata editing and an "unmatched items" admin list endpoint (phase 3 admin UI).
- Missing-episode display from metadata (spec 3.1) needs the season/episode lists stored per show.

## Self-review notes

- Spec 3.2 coverage: provider interface + merge by field priority per library type (Tasks 3, 12),
  artwork cached locally with resized variants (Tasks 7, 14), TMDB primary + Fanart.tv + local NFO
  (Tasks 8–10), fix match + per-item editing + locks surviving rescans (Tasks 12, 15),
  "keep original title" per library (Task 12), title search across alternate titles (TMDB
  original-title candidates, Task 8). Anime parts are plan 1E.
- Index contracts used exactly; 1C names come from the finished 1C plan; no contract changes.
- Type names cross-checked: `Merged`, `Details`, `View`, `ImageRow`, `Scored`, `MatcherOptions`,
  `ModuleOptions`, `QueueOptions`, `FetcherOptions`, `SettingsUpdate` are defined once and used
  with the same fields in later tasks.
