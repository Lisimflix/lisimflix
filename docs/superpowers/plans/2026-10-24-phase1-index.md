# Phase 1 (Foundation): Plan Index and Shared Contracts

**Spec:** `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (section 9, phase 1)

Phase 1 is split into six plans so Zac, Lisim and Ondo (and their agents) can work in
parallel without stepping on each other. **1A must merge first.** It creates the skeleton,
conventions and the shared interfaces listed below. After that the plans follow the
dependency graph.

```
1A Core skeleton ──┬── 1B Auth & accounts ───────────┐
                   ├── 1C Libraries & scanner ── 1D Metadata core ── 1E Anime metadata
                   │                                  │
                   └──────────────────────────────────┴── 1F User data & Plex import (needs 1B + 1C + 1D)
```

| Plan | File | Can start after | Suggested owner |
|---|---|---|---|
| 1A Core skeleton | `2026-10-24-phase1a-core.md` | none | Zac / Claude |
| 1B Auth & accounts | `2026-10-24-phase1b-auth.md` | 1A merged | any |
| 1C Libraries & scanner | `2026-10-24-phase1c-scanner.md` | 1A merged | any |
| 1D Metadata core | `2026-10-24-phase1d-metadata.md` | 1C merged | any |
| 1E Anime metadata | `2026-10-24-phase1e-anime.md` | 1D merged | any |
| 1F User data & Plex import | `2026-10-24-phase1f-userdata-plex.md` | 1B + 1D merged | any |

Owners claim a plan by assigning themselves the plan's tracking issue on the project board.

**Phase 1 done when** (from spec): the real zraid libraries scan and match accurately
(movies, TV, anime), and users/profiles work via the API.

**Scope note:** phase 1 scans the **video** library types (Movies, TV Shows, Anime, Home
Videos). Music and Photos libraries (spec 3.1) get their own plan after phase 3, so the
first release stays focused on video.

## Global constraints (apply to every plan)

- Go **1.27** (`golang:1.27-trixie` image). Module `github.com/Lisimflix/lisimflix/server` in `server/`.
- Nobody needs Go installed locally: `make test`, `make build`, `make run` wrap Docker.
- SQLite via `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`). Driver name `"sqlite"`.
- HTTP router `github.com/go-chi/chi/v5`. Logging `log/slog` only.
- All API routes live under `/api/v1`. JSON only. Errors always use the envelope
  `{"error":{"code":"snake_case_code","message":"human text"}}` via `httpx.WriteError`.
- `/media` is **read-only**. Code must never write, rename or delete under the media root.
- jellyfin-ffmpeg **8.1.3-1** portable build at `/usr/lib/jellyfin-ffmpeg/{ffmpeg,ffprobe}`.
- Default port **8420**. Volumes `/config`, `/media` (ro), `/cache`.
- Migrations: one folder `server/internal/db/migrations/`, file names
  `YYYYMMDDHHMMSS_<module>_<what>.sql` (UTC timestamp at time of writing). **Never edit a
  migration that has been merged to `main`**: add a new one instead. The timestamps keep
  parallel branches from colliding.
- Tests: every task is TDD. `make test` must pass before pushing. No network calls in unit
  tests: external APIs are tested against recorded fixtures in `testdata/` via `httptest.Server`.
- Commits and PR titles: Conventional Commits (`feat(scanner): ...`, `fix(auth): ...`), ending
  with a `Co-Authored-By:` line when an AI agent wrote the change. Never commit to or push
  `main`, never `--no-verify`, never merge your own PR (see `CLAUDE.md`).
- No secrets in the repo. API keys come from env or the admin settings table.

## Shared contracts

These names and signatures are fixed. A plan may **add** to its own package, but must not
change a contract another plan consumes without a PR touching this file, reviewed by the
owners of the affected plans.

### From 1A (core)

```go
// package config  (server/internal/config)
type Config struct {
    ConfigDir   string // LISIMFLIX_CONFIG_DIR, default "/config"
    MediaDir    string // LISIMFLIX_MEDIA_DIR,  default "/media"
    CacheDir    string // LISIMFLIX_CACHE_DIR,  default "/cache"
    HTTPAddr    string // LISIMFLIX_HTTP_ADDR,  default ":8420"
    PublicURL   string // LISIMFLIX_PUBLIC_URL, default "" (e.g. "https://watch.example.com")
    LogLevel    string // LISIMFLIX_LOG_LEVEL,  default "info"
    FFprobePath string // LISIMFLIX_FFPROBE,    default "/usr/lib/jellyfin-ffmpeg/ffprobe"
    FFmpegPath  string // LISIMFLIX_FFMPEG,     default "/usr/lib/jellyfin-ffmpeg/ffmpeg"
}
func Load(getenv func(string) string) (Config, error)

// package db  (server/internal/db)
type DB struct{ *sql.DB }
func Open(path string) (*DB, error)              // WAL, foreign_keys=ON, busy_timeout=5000
func (d *DB) Migrate(ctx context.Context) error  // applies embedded migrations in name order
func (d *DB) GetSetting(ctx context.Context, key string) (string, bool, error)
func (d *DB) SetSetting(ctx context.Context, key, value string) error

// package dbtest  (server/internal/db/dbtest): test-only helper, kept out of the binary
func Open(t testing.TB) *db.DB                   // temp-file DB, migrated, closed on cleanup

// package httpx  (server/internal/httpx)
func WriteJSON(w http.ResponseWriter, status int, v any)
func WriteError(w http.ResponseWriter, status int, code, message string)
func DecodeJSON(r *http.Request, v any) error    // 1 MiB limit, unknown fields rejected

// package authctx  (server/internal/authctx)
type Principal struct {
    UserID    int64
    ProfileID int64 // 0 = no profile selected yet
    IsAdmin   bool
}
func WithPrincipal(ctx context.Context, p Principal) context.Context
func FromContext(ctx context.Context) (Principal, bool)
type Guard interface {
    RequireUser(next http.Handler) http.Handler   // 401 if no principal
    RequireAdmin(next http.Handler) http.Handler  // 401 if none, 403 if not admin
}
type DenyAllGuard struct{}          // default until 1B wires the real guard
type StaticGuard struct{ P Principal } // tests: injects P on every request

// package app  (server/internal/app)
type Module interface {
    Name() string
    Mount(r chi.Router, g authctx.Guard) // r is already scoped to /api/v1
}
func New(cfg config.Config, d *db.DB, log *slog.Logger, g authctx.Guard, mods ...Module) *App
func (a *App) Handler() http.Handler
```

### From 1B (auth)

```go
// package auth  (server/internal/auth)
type Service struct{ /* ... */ }
func NewService(d *db.DB, cfg config.Config, log *slog.Logger, now func() time.Time) *Service
func (s *Service) Guard() authctx.Guard   // the real guard; replaces DenyAllGuard in main
// Policy queries used by other modules:
// Admins: every library ID. Users: their explicit allow-list (empty list = no libraries).
func (s *Service) VisibleLibraryIDs(ctx context.Context, p authctx.Principal) ([]int64, error)
func (s *Service) MaxContentRating(ctx context.Context, p authctx.Principal) (string, error) // "" = unrestricted
```
Tables owned: `users`, `user_identities`, `profiles`, `sessions`, `invites`,
`signup_requests`, `user_library_access`, `oauth_providers`.

### From 1C (libraries & scanner)

```go
// package library  (server/internal/library)
type Type string // "movies" | "tv" | "anime" | "homevideos"
type Library struct {
    ID      int64
    Name    string
    Type    Type
    Paths   []string // absolute, each inside cfg.MediaDir
    Options Options
}
type Options struct {
    AnimeNumbering string // "absolute" | "season"; anime only, default "season"
    KeepOriginalTitle bool
}
type Store struct{ /* ... */ }
func NewStore(d *db.DB) *Store
func (s *Store) List(ctx context.Context) ([]Library, error)
func (s *Store) Get(ctx context.Context, id int64) (Library, error)

// package media  (server/internal/media)
type Kind string // "movie" | "show" | "season" | "episode" | "video"
type Item struct {
    ID             int64
    LibraryID      int64
    ParentID       int64  // 0 for top level (movie, show, video)
    Kind           Kind
    Title          string
    OriginalTitle  string
    SortTitle      string
    Year           int    // 0 = unknown
    SeasonNumber   *int   // season, episode
    EpisodeNumber  *int   // episode
    AbsoluteNumber *int   // episode (anime)
    ExternalIDs    map[string]string // keys: "tmdb","tvdb","imdb","anidb","anilist","mal"
    MatchState     string // "unmatched" | "matched" | "manual"
    LockedFields   []string
    AddedAt, UpdatedAt time.Time
}
type File struct {
    ID      int64
    ItemID  int64
    Path    string // absolute
    Size    int64
    ModTime time.Time
    Probe   probe.Result
    Version string // "" or label like "Director's Cut"; groups multiple files per movie
}
type Store struct{ /* ... */ }
func NewStore(d *db.DB) *Store
func (s *Store) GetItem(ctx context.Context, id int64) (Item, error)
func (s *Store) Children(ctx context.Context, parentID int64) ([]Item, error)
func (s *Store) ListTopLevel(ctx context.Context, libraryID int64) ([]Item, error)
func (s *Store) UpdateItem(ctx context.Context, it Item) error // respects nothing; callers check LockedFields
func (s *Store) Files(ctx context.Context, itemID int64) ([]File, error)
func (s *Store) ItemsNeedingMatch(ctx context.Context, libraryID int64) ([]Item, error)

// package probe  (server/internal/probe): wraps ffprobe
type Result struct {
    Container  string
    DurationMs int64
    BitRate    int64
    Video      []VideoStream
    Audio      []AudioStream
    Subtitles  []SubtitleStream
    Chapters   []Chapter
}
// (stream structs are defined in 1C; they carry codec, profile, level, width, height,
//  bit depth, HDR type, channels, language, title, default/forced flags)
func Run(ctx context.Context, ffprobePath, file string) (Result, error)

// package scanner  (server/internal/scanner): scan engine + events, consumed by 1D to trigger matching
type ScanListener interface { ItemsChanged(ctx context.Context, libraryID int64, itemIDs []int64) }
func (s *Scanner) AddListener(l ScanListener)
```
Tables owned: `libraries`, `library_paths`, `items`, `files`, `item_external_ids`.

### From 1D (metadata core)

```go
// package metadata  (server/internal/metadata)
type Query struct {
    Kind     media.Kind
    LibType  library.Type
    Title    string
    Year     int
    IDs      map[string]string // known external IDs
}
type Candidate struct {
    Provider string
    IDs      map[string]string
    Title    string
    Year     int
    Score    float64 // 0..1, provider-side confidence
}
type Record struct {
    Provider      string
    IDs           map[string]string
    Title, OriginalTitle, SortTitle string
    AltTitles     map[string]string // lang/script → title, e.g. "en","x-jat"(romaji),"ja"
    Overview      string
    Year          int
    Released      string // YYYY-MM-DD
    Genres, Studios, Tags []string
    ContentRating string // e.g. "PG-13", "TV-14"
    Ratings       map[string]float64 // "tmdb","imdb","rt","anilist","mal" → 0..10
    People        []Person
    Images        []Image
    Seasons       []SeasonRecord // shows only
}
type Image struct{ Type string /* poster|backdrop|logo|thumb|banner */; URL string; Lang string; Width, Height int }
type Person struct{ Name, Role, Character string; ImageURL string; Order int }
type SeasonRecord struct{ Number int; Title, Overview string; Episodes []EpisodeRecord; Images []Image }
type EpisodeRecord struct{ Season, Number int; Absolute *int; Title, Overview, AirDate string; Images []Image }

type Provider interface {
    Name() string
    Supports(kind media.Kind, lt library.Type) bool
    Search(ctx context.Context, q Query) ([]Candidate, error)
    Fetch(ctx context.Context, kind media.Kind, ids map[string]string) (Record, error)
}
type Registry struct{ /* ordered providers + per-library-type field priority */ }
func (r *Registry) Register(p Provider)
// IDMapper lets 1E plug in the anime-lists cross-reference.
type IDMapper interface {
    Expand(ctx context.Context, ids map[string]string) (map[string]string, error)
}
func (r *Registry) SetIDMapper(m IDMapper)
// Episode numbering lets 1E plug in absolute↔season mapping.
type EpisodeMapper interface {
    Map(ctx context.Context, show Record, ep media.Item) (season, episode int, absolute *int, ok bool)
}
func (r *Registry) SetEpisodeMapper(m EpisodeMapper)
```
Tables owned: `item_metadata`, `item_people`, `people`, `images`, `item_genres`, `metadata_cache`.

### From 1F (user data)

```go
// package userdata  (server/internal/userdata)
type State struct {
    ProfileID   int64
    ItemID      int64
    PositionMs  int64
    Watched     bool
    PlayCount   int
    LastPlayed  time.Time
    HiddenFromContinue bool
}
type Store struct{ /* ... */ }
func NewStore(d *db.DB) *Store
func (s *Store) Get(ctx context.Context, profileID, itemID int64) (State, error)
func (s *Store) Put(ctx context.Context, st State) error
```
Tables owned: `user_item_state`, `watch_history`, `watchlist`, `ratings`, `track_prefs`.

## Branch and review flow

`CONTRIBUTING.md` is the source of truth. If anything here disagrees with it, `CONTRIBUTING.md` wins. In short:

1. One-time: `git config core.hooksPath .githooks` (the pre-push hook blocks pushes to `main`).
2. Assign yourself the plan's issue, then branch off an up-to-date `main` using the
   CONTRIBUTING prefixes: `feat/1b-auth`, `feat/1c-scanner`, `docs/...`, `fix/...`.
3. Work task by task. Each plan task ends in a commit on your branch. Push often.
4. Open a PR (fill in the template, `Closes #N`). The PR title uses Conventional Commits with
   the area as scope, e.g. `feat(auth): local admin login and sessions`. Another member reviews.
5. **Squash and merge.** A large plan may be split into several PRs, one per group of tasks.
