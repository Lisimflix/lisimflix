# LIB-4 Import-Triggered Scans (Sonarr/Radarr Webhooks) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Sonarr and Radarr tell Lisimflix about imports, upgrades, renames and deletes, so the affected files are scanned within seconds instead of waiting for the watcher or the 6-hour scan. This holds even where inotify sees nothing (network shares, Unraid `/mnt/user`).

**Architecture:** A new package `library/arrhook`:
- **Sources:** each Sonarr or Radarr instance gets a revocable token (stored hashed), the library folders it may touch, and remote-to-local path mappings.
- **Endpoint:** `POST|PUT /hooks/v1/arr` receives the apps' standard webhook. It sits beside the API, outside its browser-only Origin/CSRF checks, and authenticates with HTTP Basic (source ID + token), which both apps send natively.
- **Validation:** the `User-Agent` must name the right app and a supported major version. The payload is parsed for the paths it names, each path is mapped onto the media root and must lie in one of the source's folders, and the result is grouped into scopes (a season pack is one folder).
- **Scanning:** each scope is queued with LIB-2b's `Scheduler.Hint` and cause `import`, so the normal pipeline does the work: coalescing, stable-file checks, probes, offline-root safety.
- **Receipts:** a receipt records what each notification did. A `lisimflix hook` command manages sources until the admin screens exist.

**Tech Stack:** Go 1.27.3 standard library (`net/http`, `crypto/sha256`, `crypto/subtle`), OPS-2 storage, LIB-2a/2b's `library` package. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §3.6: "LIB-4 — import-triggered scans" and the notification rules after it.
- §9.4: the LIB-4 row.
- §13.4: **SCAN-02**.
- §6.6: untrusted input.
- §11.2: bounded queues.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-4** (import-triggered scans, P0 / G1 library milestone) |
| Issue | #98 |
| Owner / reviewers | Lammy (claimant) / Pete and Ondo |
| Acceptance | **SCAN-02** (fixtures, §13.4):<br>• authenticated import, upgrade and test events<br>• deduplicated season packs<br>• path escapes and mappings<br>• unavailable roots<br>• missed-event reconciliation (LIB-2b's scans)<br>• receipt versus completion status<br><br>§3.6 rules: revocable per-source credentials limited to configured roots; redaction; bounded payloads and rates; explicit path mappings; no deletes or access grants; a connection test that doesn't scan; validated app version and event schema |
| File scope | **New:**<br>• `server/internal/library/arrhook/{store,paths,payload,handler}.go` + tests<br>• `server/internal/library/arrhook/migrations/20261028130000_arrhook_sources.sql`<br>• `server/internal/library/arrhook/testdata/*.json` + `README.md`<br>• `server/cmd/lisimflix/hook.go` + test<br>• `docs/contracts/arr-webhook.md`<br><br>**Changed:**<br>• `server/internal/app/{app,app_test}.go` (the `Mount` option)<br>• `server/cmd/lisimflix/{serve,main,library}.go`<br>• `README.md`, `deploy/README.md`<br><br>No OpenAPI change (Decision 2) |
| Consumes | **LIB-2b (#97, revised for LIB-4):** `Scheduler.Hint(rootID, scope, cause string) error`, `library.CauseImport` and the migration's `import` cause.<br>**LIB-2a:** `library_roots`, `library.Migrations`, `CreateLibrary`.<br>**OPS-2:** `storage`.<br>**OPS-5:** `obs.Log`, `obs.RequestID`.<br>**CTR-1:** `contracts.WriteError` and its existing codes |
| Produces | `arrhook.Migrations`, `Store` (`Add`, `Authenticate`, `Sources`, `Revoke`, `Receipts`), `Source`, `Mapping`, `Root`, `Scope`, `Receipt` and the result constants, `Handler`, `NewHandler`, `Hinter`, `Path`.<br>`app.Mount`.<br>The `lisimflix hook add\|list\|revoke\|receipts` command.<br>The `library.hook` log event |
| Start dependency | LIB-2b's code merged (it adds `Hint`). Tasks 1 and 2 need only LIB-2a's schema and can start once LIB-2a's code is in |
| Standalone test | `go -C server test -race -count=1 ./internal/library/arrhook/ ./internal/app/ ./cmd/...`, then `make check` |
| Module proof | Payload fixtures written from the apps' current source, a real database, a recording `Hinter`. Mutation-checked (see Self-review) |
| Integration proof | Task 5: the real image beside **real Sonarr 4.0.20 and Radarr 6.4.4** in throwaway containers.<br>• Both apps' connection Test succeeds, and a wrong token is refused.<br>• A Radarr manual import and delete send their real webhooks, which become `import` scans.<br><br>Record "integrated" on #98 |

## Global Constraints

- Go 1.27.3. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`. Run `make check` before pushing.
- **Read-only media (SAFE-01):** nothing here touches media. A notification only queues scans; a deleted file is only marked missing by the scan.
- Paths are untrusted (§6.6). Every remote path is cleaned, mapped by an explicit mapping, and checked to lie inside one of the source's library folders. The scan then reads through `os.Root` as always.
- Secrets: tokens are shown once and stored as SHA-256; the `Authorization` header and tokens are never logged.
- Migrations: one new file, `20261028130000_arrhook_sources.sql`, module `arrhook`. Its timestamp is after LIB-2a's and LIB-2b's, because it references `library_roots`.
- Code without a production caller carries `// Planned: <Feature ID> (<caller>).` (#60).
- Workflow:
  - Branch `feat/lib-4-arr-webhooks`. PR title `feat(library): Sonarr/Radarr import notifications queue scoped scans (LIB-4)`.
  - Labels `area:library`, `area:api`. Reviewers lisim and ondona.

## Decisions taken in this plan (please review)

1. **The apps' own webhook, not a plugin or API polling.** Both apps send it natively: Settings → Connect → Webhook, with URL, POST/PUT, Username/Password and custom headers. Lisimflix never calls Sonarr or Radarr back.
2. **`/hooks/v1/arr`, outside `/api/`.**
   - The API's Origin and CSRF checks are for browsers; a server sends neither. `app.New` gains a variadic `Mount` option for routes served beside the API, with the same security headers, request IDs and error logging (`observe`), but not the browser checks. The API keeps them, and a test proves it.
   - The endpoint isn't in the OpenAPI contract, which describes the client API (#87 is editing it). It's documented in `docs/contracts/arr-webhook.md`. Error bodies reuse `contracts.WriteError` and its existing codes, so `TestErrorCodesMatchOpenAPI` is unaffected.
3. **One source per app instance, with HTTP Basic auth.** The username is the source ID and the password is the token, `lfh_` plus 26 random base32 characters (130 bits). Both apps send Basic credentials preemptively (Sonarr's `ManagedHttpDispatcher` sets the header without waiting for a challenge).
   - The token is stored as SHA-256; a slow hash is pointless for a random 130-bit secret. It's compared in constant time, and an unknown source ID compares against a zero hash, so timing doesn't reveal which IDs exist.
   - Revoking takes effect on the next request, because sources are read from the database on each request.
4. **Version check from the `User-Agent`.** Both apps send `Sonarr/4.0.20.3014 (ubuntu 22.04)`; the payload carries no version.
   - Supported: **Sonarr 4**, **Radarr 5 and 6** (v6.4.4 is current; Sonarr v3 is end of life).
   - The app must match the source's kind: a Sonarr token used by Radarr is refused.
   - An unsupported version is refused with a reason Sonarr/Radarr show in their UI, rather than accepted with a payload this code may misread. Adding the next major version is a one-line change after checking its webhook classes.
   - Custom headers that replace the `User-Agent` break this; the deploy README says not to.
5. **Events:**

   | eventType | Paths queued |
   |---|---|
   | `Test` | none: 200 `test`, recorded (the §3.6 connection test) |
   | `Download` (import, upgrade, Sonarr's import-complete with `episodeFiles`) | each file's `path` (else the series/movie folder + `relativePath`), plus `deletedFiles[].path` (an upgrade's replaced files) |
   | `Rename` | each renamed file's `path` and `previousPath`; with no list, the folder |
   | `EpisodeFileDelete` / `MovieFileDelete` | the file |
   | `SeriesDelete` / `MovieDelete` | the series/movie folder |
   | `Grab`, `Health`, `HealthRestored`, `ApplicationUpdate`, `ManualInteractionRequired`, `SeriesAdd`, `MovieAdded`, and unknown types | none: 200 `ignored`, recorded. Unknown types are ignored so a new event in a later minor release doesn't fail |

   - `deletedFiles` is a list on `Download` but a boolean on `SeriesDelete`/`MovieDelete`, so it's decoded only when it's a list. The first fixture run caught this.
   - A known event without the paths it should carry is a schema error, 400, so a changed payload is visible in the app.
   - At most 1,000 paths and 1 MiB per notification.
6. **All or nothing.** If any path can't be mapped into the source's folders (unmapped, an escape, another library, relative), the whole notification is rejected with 400 and the reason, and nothing is queued. That's a setup mistake the admin should see as a failed notification in Sonarr/Radarr, not a silently partial scan. The receipt records the reason.
7. **Path mapping:**
   - `--map REMOTE=LOCAL`, longest match by whole folders: `/tv` doesn't cover `/tvshows`.
   - Windows paths (`C:\Media\TV`, `\\nas\tv`) get forward slashes. Remote paths are cleaned before mapping, so `..` can't climb into another mapping. `LOCAL` is relative to the media root, and `.` is allowed.
   - The result must be inside one of the source's folders, which must be existing library folders.
8. **Grouping.**
   - Files of one notification that share a folder become one scan of that folder: a season pack (`episodeFiles`), or an upgrade's new and replaced file. A single file stays a file scan.
   - Duplicates go, and a scope inside another is dropped.
   - The scheduler then coalesces with pending work: a repeated notification, or the watcher seeing the same import, adds nothing. If the watcher's scope was queued first, the run is recorded with its `watch` cause.
9. **Receipt, not completion (§3.6).**
   - Answers: 202 `queued` with the receipt ID and scopes; 200 `test`/`ignored`; 400 `invalid_request` with the reason; 401 `unauthenticated` with a `WWW-Authenticate: Basic` challenge; 429 `rate_limited`; 503 `source_unavailable` if the scheduler doesn't know the root yet (a library added under a minute ago).
   - Every authenticated notification gets a receipt row: event, app, result, reason, scopes, request ID. The newest 200 per source are kept.
   - Completion is the scan runs with cause `import` and those scopes (LIB-2b's status), so a receipt never claims the item is playable.
10. **Limits (§3.6, §11.2):**
    - 60 notifications per source per minute; beyond that, 429. Sonarr doesn't retry, and the missed change is found by the watcher or the 6-hour scan.
    - 20 failed sign-ins per peer per minute (IPv4 address or IPv6 /64, as `auth` keys it); beyond that, every request from the peer gets 429, including a right token.
    - The counter maps are bounded at 4,096 entries; when full of live entries, new peers are refused rather than the map growing.
11. **The `hook` command until UI-6:**
    - `add --name --kind sonarr|radarr --folder F --map R=L` prints the exact Sonarr/Radarr settings, including the token once.
    - `list`, `revoke --name`, `receipts --name [--n 20]`.
    - It shares `openForCLI` with LIB-2b's `library` command, which this plan factors out of it.
12. **Log event** `library.hook`:
    - INFO for `queued`/`test`/`ignored`; WARN for `rejected`, `unauthenticated` and `rate_limited`.
    - Fields: `source_id`, `source`, `event`, `app`, `result`, `reason`, `scopes` (count), `request_id`, and `peer` for failed sign-ins.
    - Never the token, the `Authorization` header or the body.
13. **Not in this slice:**
    - Admin screens for sources and receipts (UI-6).
    - Using the payload's TVDB/TMDB/IMDb IDs as match hints (META-1 may later; the folder's own `{tmdb-…}` hint already works).
    - Lidarr/Readarr (no music or books in G1).
    - Per-source path-mapping tests from the UI.
    - The M1-config startup failure found in Task 5 (#99).

## Review Focus

1. **Docker path mapping is the commonest setup mistake**: Sonarr sees `/tv`, Lisimflix sees `/media/TV`. An unmapped path must fail loudly in Sonarr's UI and the receipt, never queue nothing silently → `TestPathsOutsideTheSourceAreRejected`, `TestResolve`.
2. **An upgrade replaces a file**: the old file must go missing and the new one appear in one scan → `TestImportQueuesScopedScans` (new + `deletedFiles` → one folder scan); proven live in Task 5 (added 1, missing 1).
3. **A season pack of 20+ files** must be one scan, not 20, and well under `MaxScopes` → `TestSeasonPackIsOneScan`.
4. **Sonarr's Test button** must not scan anything, and a wrong token must show as a failure in Sonarr → `TestConnectionTestQueuesNothing`, `TestAuthenticationAndLimits`; proven live in Task 5.
5. **Deleting the last item of a library** empties the library folder, and LIB-2a's SCAN-01 rule then reports the root as offline (`root_empty`) instead of marking the item missing (seen live in Task 5). That's the safe choice, since an empty folder looks like an unmounted disk. It goes back to normal when the folder has content again; 2c's admin screen can offer "this folder is really empty". No change here; the deploy README notes it.

---

### Task 1: Sources, path mapping and receipts

**Files:**
- Create: `server/internal/library/arrhook/migrations/20261028130000_arrhook_sources.sql`, `server/internal/library/arrhook/store.go`, `server/internal/library/arrhook/paths.go`
- Test: `server/internal/library/arrhook/store_test.go`, `server/internal/library/arrhook/paths_test.go`

**Interfaces:**
- Consumes: `library.Migrations()`, `library.NewStore(db).CreateLibrary` (tests), `storage.Open`, `storage.Core`, `Migrate`.
- Produces:
  - `func Migrations() storage.Source` (module `arrhook`); `Sonarr = "sonarr"`, `Radarr = "radarr"`; `ErrNotFound`, `ErrInvalid`, `ErrUnauthorized`.
  - `type Mapping struct{ Remote, Local string }`, `type Root struct{ ID, Path string }`, `type Source struct{ ID, Name, Kind string; Roots []Root; Mappings []Mapping; Revoked bool }`.
  - `func NewStore(db *storage.DB) *Store`; `(s *Store) Add(ctx, name, kind string, roots []string, maps []Mapping) (Source, string, error)`; `Authenticate(ctx, id, token string) (Source, error)`; `Sources(ctx) ([]Source, error)`; `Revoke(ctx, name string) error`.
  - `type Scope struct{ RootID string \`json:"rootId"\`; Path string \`json:"path"\` }`; `type Receipt struct{ ID, SourceID string; At time.Time; Event, App, Result, Reason string; Scopes []Scope; RequestID string }`; `ResultQueued`, `ResultTest`, `ResultIgnored`, `ResultRejected`; `const keepReceipts = 200`; unexported `record(ctx, Receipt) error`; `Receipts(ctx, sourceID string, n int) ([]Receipt, error)`.
  - Unexported in `paths.go`: `normRemote(p string) (string, bool)`, `(src Source) resolve(remote string) (Scope, error)` with `errUnmapped`, `errOutside`, `errNotAbsolute`, and `group([]Scope) []Scope`. Test helper `newStore(t) (*Store, tvRootID, moviesRootID string)`.

- [ ] **Step 1: Write the failing tests**

**Create `server/internal/library/arrhook/store_test.go`:**

```go
package arrhook

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// newStore returns a store on a fresh database with a TV library over
// "TV" and a movie library over "Movies"; it returns their root IDs too.
func newStore(t *testing.T) (*Store, string, string) {
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
	ls := library.NewStore(db)
	tv, err := ls.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ls.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db), tv.Roots[0].ID, mv.Roots[0].ID
}

func TestAddAndAuthenticate(t *testing.T) {
	st, tvRoot, _ := newStore(t)
	ctx := context.Background()
	src, token, err := st.Add(ctx, "sonarr", Sonarr, []string{"TV"}, []Mapping{{`/tv/`, "./TV"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "lfh_") || len(token) < 30 || src.Roots[0].ID != tvRoot || src.Mappings[0] != (Mapping{"/tv", "TV"}) {
		t.Fatalf("Add = %+v, %q", src, token)
	}
	got, err := st.Authenticate(ctx, src.ID, token)
	if err != nil || got.Name != "sonarr" || len(got.Roots) != 1 || len(got.Mappings) != 1 {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	for name, c := range map[string][2]string{
		"wrong token":    {src.ID, token + "x"},
		"unknown source": {"src-nope", token},
		"empty":          {"", ""},
	} {
		if _, err := st.Authenticate(ctx, c[0], c[1]); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The token isn't stored, only its hash.
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM arrhook_sources WHERE CAST(token_hash AS TEXT) LIKE '%' || ? || '%'`, token[4:]).Scan(&n)
	if n != 0 {
		t.Fatal("token stored in clear")
	}
	if err := st.Revoke(ctx, "sonarr"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Authenticate(ctx, src.ID, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked: %v", err)
	}
	if err := st.Revoke(ctx, "sonarr"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
}

func TestAddChecksInput(t *testing.T) {
	st, _, _ := newStore(t)
	ctx := context.Background()
	ok := []Mapping{{"/tv", "TV"}}
	if _, _, err := st.Add(ctx, "sonarr", Sonarr, []string{"TV"}, ok); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		name, kind string
		roots      []string
		maps       []Mapping
	}{
		"duplicate name":  {"sonarr", Sonarr, []string{"TV"}, ok},
		"bad name":        {"Sonarr Main", Sonarr, []string{"TV"}, ok},
		"bad kind":        {"lidarr", "lidarr", []string{"TV"}, ok},
		"no folders":      {"s2", Sonarr, nil, ok},
		"not a library":   {"s2", Sonarr, []string{"Music"}, ok},
		"no mappings":     {"s2", Sonarr, []string{"TV"}, nil},
		"relative remote": {"s2", Sonarr, []string{"TV"}, []Mapping{{"tv", "TV"}}},
		"local escapes":   {"s2", Sonarr, []string{"TV"}, []Mapping{{"/tv", "../etc"}}},
		"absolute local":  {"s2", Sonarr, []string{"TV"}, []Mapping{{"/tv", "/media/TV"}}},
	} {
		if _, _, err := st.Add(ctx, c.name, c.kind, c.roots, c.maps); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestReceiptsAreBounded(t *testing.T) {
	st, tvRoot, _ := newStore(t)
	ctx := context.Background()
	src, _, _ := st.Add(ctx, "sonarr", Sonarr, []string{"TV"}, []Mapping{{"/tv", "TV"}})
	at := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	for i := range keepReceipts + 5 {
		r := Receipt{ID: "rcpt-" + string(rune('a'+i%26)) + time.Duration(i).String(), SourceID: src.ID,
			At: at.Add(time.Duration(i) * time.Second), Event: "Download", App: "Sonarr/4.0.20.3014", Result: ResultQueued,
			Scopes: []Scope{{tvRoot, "Show"}}}
		if err := st.record(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := st.Receipts(ctx, src.ID, 1000)
	if err != nil || len(rs) != keepReceipts {
		t.Fatalf("%d receipts, %v", len(rs), err)
	}
	if !rs[0].At.Equal(at.Add((keepReceipts+4)*time.Second)) || rs[0].Scopes[0] != (Scope{tvRoot, "Show"}) {
		t.Fatalf("newest %+v", rs[0])
	}
}
```

**Create `server/internal/library/arrhook/paths_test.go`:**

```go
package arrhook

import (
	"errors"
	"slices"
	"testing"
)

func TestResolve(t *testing.T) {
	src := Source{
		Roots: []Root{{"root-tv", "TV"}, {"root-anime", "Media/Anime"}},
		Mappings: []Mapping{
			{"/tv", "TV"},
			{"/data", "Media"},
			{"/data/series", "TV"}, // longer than /data: wins for paths under it
			{"C:/Media/TV", "TV"},
		},
	}
	for _, c := range []struct {
		remote string
		want   Scope
		err    error
	}{
		{"/tv/Show (2019)/Season 01/x.mkv", Scope{"root-tv", "Show (2019)/Season 01/x.mkv"}, nil},
		{"/tv", Scope{"root-tv", "."}, nil},
		{"/tv/", Scope{"root-tv", "."}, nil},
		{"/data/series/Show/x.mkv", Scope{"root-tv", "Show/x.mkv"}, nil},
		{"/data/Anime/Frieren/x.mkv", Scope{"root-anime", "Frieren/x.mkv"}, nil},
		{`C:\Media\TV\Show\x.mkv`, Scope{"root-tv", "Show/x.mkv"}, nil},
		{"/tvshows/x.mkv", Scope{}, errUnmapped},                        // a prefix, not a folder
		{"/tv/../etc/passwd", Scope{}, errUnmapped},                     // cleaned first
		{"/data/Movies/x.mkv", Scope{}, errOutside},                     // mapped, but no root of this source
		{"/data/series/../../tv/x.mkv", Scope{"root-tv", "x.mkv"}, nil}, // = /tv/x.mkv
		{"Show/x.mkv", Scope{}, errNotAbsolute},
		{"", Scope{}, errNotAbsolute},
	} {
		got, err := src.resolve(c.remote)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("resolve(%q) = %+v, %v; want %+v, %v", c.remote, got, err, c.want, c.err)
		}
	}
	// A mapping onto the media root itself works too.
	whole := Source{Roots: []Root{{"root-tv", "TV"}}, Mappings: []Mapping{{"/media", "."}}}
	if got, err := whole.resolve("/media/TV/x.mkv"); err != nil || got != (Scope{"root-tv", "x.mkv"}) {
		t.Errorf("mapping to the media root: %+v %v", got, err)
	}
}

func TestGroup(t *testing.T) {
	tv := func(p string) Scope { return Scope{"root-tv", p} }
	for _, c := range []struct {
		name     string
		in, want []Scope
	}{
		{"one file", []Scope{tv("Show/Season 01/a.mkv")}, []Scope{tv("Show/Season 01/a.mkv")}},
		{"season pack", []Scope{tv("Show/Season 02/a.mkv"), tv("Show/Season 02/b.mkv"), tv("Show/Season 02/c.mkv")},
			[]Scope{tv("Show/Season 02")}},
		{"upgrade: new and replaced file", []Scope{tv("Show/S1/new.mkv"), tv("Show/S1/old.mkv")}, []Scope{tv("Show/S1")}},
		{"duplicates", []Scope{tv("Show/a.mkv"), tv("Show/a.mkv")}, []Scope{tv("Show/a.mkv")}},
		{"inside another", []Scope{tv("Show"), tv("Show/S1/a.mkv")}, []Scope{tv("Show")}},
		{"different roots stay apart", []Scope{tv("a.mkv"), {"root-anime", "a.mkv"}},
			[]Scope{{"root-anime", "a.mkv"}, tv("a.mkv")}},
		{"whole root", []Scope{tv("."), tv("x/a.mkv")}, []Scope{tv(".")}},
	} {
		if got := group(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -race -count=1 ./internal/library/arrhook/
```

Expected: FAIL to build: `undefined: Store`, `undefined: Source`, `undefined: Root`, `undefined: Mapping`.

- [ ] **Step 3: Add the migration**

**Create `server/internal/library/arrhook/migrations/20261028130000_arrhook_sources.sql`:**

```sql
-- Import notifications (LIB-4): Sonarr and Radarr instances allowed to queue
-- scans, the library folders each may touch, how their paths map onto the
-- media root, and the last receipts for the admin view. Times are Unix
-- milliseconds (UTC).

CREATE TABLE arrhook_sources (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('sonarr', 'radarr')),
    token_hash BLOB NOT NULL,          -- SHA-256 of the token; the token itself is shown once
    created_at INTEGER NOT NULL,
    revoked_at INTEGER                 -- set: every request is refused
) STRICT;

-- The library folders a source may queue scans in.
CREATE TABLE arrhook_roots (
    source_id TEXT NOT NULL REFERENCES arrhook_sources(id) ON DELETE CASCADE,
    root_id   TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    PRIMARY KEY (source_id, root_id)
) STRICT;

-- remote: a folder as the source reports it ("/tv"); local: the same folder
-- relative to the media root ("TV"). Paths are mapped by the longest match.
CREATE TABLE arrhook_mappings (
    source_id TEXT NOT NULL REFERENCES arrhook_sources(id) ON DELETE CASCADE,
    remote    TEXT NOT NULL,
    local     TEXT NOT NULL,
    PRIMARY KEY (source_id, remote)
) STRICT;

-- What each notification did. A receipt means queued, not scanned: the scan
-- runs (cause 'import') record the outcome.
CREATE TABLE arrhook_receipts (
    id          TEXT PRIMARY KEY,
    source_id   TEXT NOT NULL REFERENCES arrhook_sources(id) ON DELETE CASCADE,
    received_at INTEGER NOT NULL,
    event       TEXT NOT NULL,         -- eventType as sent, e.g. Download
    app         TEXT NOT NULL,         -- from User-Agent, e.g. Sonarr/4.0.20.3014
    result      TEXT NOT NULL CHECK (result IN ('queued', 'test', 'ignored', 'rejected')),
    reason      TEXT,                  -- why it was rejected or ignored
    scopes      TEXT NOT NULL DEFAULT '[]', -- JSON [{"rootId","path"}] queued
    request_id  TEXT
) STRICT;
CREATE INDEX arrhook_receipts_source ON arrhook_receipts(source_id, received_at);
```

- [ ] **Step 4: Implement `store.go` and `paths.go`**

- **Package comment** (on `store.go`): what LIB-4 does, that notifications are hints and that nothing here deletes media or catalogue entries.
- **`Add`:**
  - Validation, each failure wrapping `ErrInvalid`: the name matches `^[a-z0-9][a-z0-9-]{0,62}$`; the kind is `sonarr` or `radarr`; there's at least one folder and one mapping.
  - Each mapping's remote goes through `normRemote` (must be absolute); its local is `path.Clean` and must pass `fs.ValidPath`.
  - Each folder must equal a `library_roots.path`.
  - The ID is `"src-" + strings.ToLower(rand.Text())` and the token `"lfh_" + rand.Text()`; `token_hash` is `sha256.Sum256(token)`. A duplicate name is `ErrInvalid`.
  - One transaction.
- **`Authenticate`:** load `token_hash` and `revoked_at` by ID. An unknown ID compares against a zero 32-byte hash, so timing doesn't reveal it. `subtle.ConstantTimeCompare`. Any mismatch, unknown ID or revoked source returns `ErrUnauthorized`; otherwise the full source (roots joined from `library_roots`, ordered by path; mappings by remote).
- **`Revoke`:** sets `revoked_at` where it's NULL; no row is `ErrNotFound`.
- **`record`:** insert the receipt (scopes as JSON, never `null`), then delete the source's rows beyond the newest `keepReceipts` (`ORDER BY received_at DESC, rowid DESC`), in one transaction.
- **`Receipts`:** newest first.
- **`normRemote`:** a drive-letter (`^[A-Za-z]:[\\/]`) or UNC (`\\`) path gets `\` → `/`. Not starting with `/` or a drive → false. Then `path.Clean`.
- **`under(p, dir)`:** p is dir, or starts with `dir + "/"` (whole folders), or dir is `/`.
- **`resolve`:** clean the remote path; pick the covering mapping with the longest `Remote`; join `Local` with the rest. The first of the source's roots containing the result gives `Scope{root.ID, rel}`, with `"."` for the root itself and `rel` checked with `fs.ValidPath`. Otherwise `errOutside`.
- **`group`:** sort and deduplicate. A file whose folder holds two or more of the scopes becomes that folder (not for `"."`). Drop scopes inside another scope of the same root. Return sorted by root then path.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go -C server test -race -count=1 ./internal/library/arrhook/
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/library/arrhook
git commit -m "feat(library): import sources, path mapping and receipts (LIB-4)"
```

---

### Task 2: Payloads and app versions

**Files:**
- Create: `server/internal/library/arrhook/payload.go`, `server/internal/library/arrhook/testdata/*.json`, `server/internal/library/arrhook/testdata/README.md`
- Test: `server/internal/library/arrhook/payload_test.go`

**Interfaces:**
- Consumes: Task 1's `Sonarr`, `Radarr`.
- Produces (unexported): `type event struct{ Type, Kind string; Paths []string }`; `kindScan = "scan"`, `kindTest = "test"`, `kindIgnore = "ignore"`; `func parse(kind string, body []byte) (event, error)` with errors wrapping `errSchema`; `const maxPaths = 1000`; `func checkAgent(kind, ua string) (app string, err error)` with errors wrapping `errVersion`; test helper `fixture(t, name) []byte`.

- [ ] **Step 1: Add the fixtures**

Each is a webhook body shaped as the apps send it: camelCase properties, PascalCase `eventType`, from `src/NzbDrone.Core/Notifications/Webhook/` on `develop` (October 2026), plus `testdata/README.md` saying so.

**`server/internal/library/arrhook/testdata/README.md`:**

```markdown
Webhook payloads shaped as Sonarr v4 and Radarr v6 send them, written from
their `develop` source (`src/NzbDrone.Core/Notifications/Webhook/`, October
2026): camelCase properties, PascalCase `eventType`. Values are made up; only
the fields LIB-4 reads matter. `sonarr_test.json` mirrors Sonarr's own
connection-test payload (`BuildTestPayload`).
```

**`server/internal/library/arrhook/testdata/radarr_download.json`:**

```json
{"movie":{"id":5,"title":"Heat","year":1995,"filePath":"/movies/Heat (1995)/Heat (1995) [Bluray-2160p].mkv","releaseDate":"1996-06-25","folderPath":"/movies/Heat (1995)","tmdbId":949,"imdbId":"tt0113277"},
 "remoteMovie":{"tmdbId":949,"imdbId":"tt0113277","title":"Heat","year":1995},
 "movieFile":{"id":7,"relativePath":"Heat (1995) [Bluray-2160p].mkv","path":"/movies/Heat (1995)/Heat (1995) [Bluray-2160p].mkv","quality":"Bluray-2160p","qualityVersion":1,"size":1,"indexerFlags":"0"},
 "isUpgrade":false,"downloadClient":"qBittorrent","downloadId":"X","eventType":"Download","instanceName":"Radarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/radarr_download_relative.json`:**

```json
{"movie":{"id":5,"title":"Heat","year":1995,"folderPath":"/movies/Heat (1995)"},
 "movieFile":{"id":7,"relativePath":"Heat (1995).mkv"},"eventType":"Download","instanceName":"Radarr"}
```

**`server/internal/library/arrhook/testdata/radarr_movie_delete.json`:**

```json
{"movie":{"id":5,"title":"Heat","year":1995,"folderPath":"/movies/Heat (1995)"},"deletedFiles":true,"movieFolderSize":123,"eventType":"MovieDelete","instanceName":"Radarr"}
```

**`server/internal/library/arrhook/testdata/radarr_movie_file_delete.json`:**

```json
{"movie":{"id":5,"title":"Heat","year":1995,"folderPath":"/movies/Heat (1995)"},"movieFile":{"id":7,"relativePath":"Heat (1995).mkv","path":"/movies/Heat (1995)/Heat (1995).mkv"},"deleteReason":"upgrade","eventType":"MovieFileDelete","instanceName":"Radarr"}
```

**`server/internal/library/arrhook/testdata/radarr_rename.json`:**

```json
{"movie":{"id":5,"title":"Heat","year":1995,"folderPath":"/movies/Heat (1995)"},
 "renamedMovieFiles":[{"id":7,"relativePath":"Heat (1995) {tmdb-949}.mkv","path":"/movies/Heat (1995)/Heat (1995) {tmdb-949}.mkv","previousRelativePath":"Heat (1995).mkv","previousPath":"/movies/Heat (1995)/Heat (1995).mkv"}],
 "eventType":"Rename","instanceName":"Radarr"}
```

**`server/internal/library/arrhook/testdata/sonarr_download.json`:**

```json
{"series":{"id":1,"title":"Show","titleSlug":"show","path":"/tv/Show (2019)","tvdbId":123,"tvMazeId":0,"tmdbId":0,"type":"standard","year":2019},
 "episodes":[{"id":10,"episodeNumber":2,"seasonNumber":1,"title":"Two"}],
 "episodeFile":{"id":20,"relativePath":"Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv","path":"/tv/Show (2019)/Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv","quality":"WEBDL-1080p","qualityVersion":1,"size":1234567,"dateAdded":"2026-10-28T00:00:00Z","sourcePath":"/downloads/complete/Show.S01E02.1080p.WEB.mkv"},
 "isUpgrade":true,"downloadClient":"SABnzbd","downloadClientType":"SABnzbd","downloadId":"SABnzbd_nzo_1",
 "deletedFiles":[{"id":19,"relativePath":"Season 01/Show - S01E02 - Two [HDTV-720p].mkv","path":"/tv/Show (2019)/Season 01/Show - S01E02 - Two [HDTV-720p].mkv","quality":"HDTV-720p","qualityVersion":1,"size":700000,"dateAdded":"2026-10-01T00:00:00Z","recycleBinPath":"/recycle/Show - S01E02 - Two [HDTV-720p].mkv"}],
 "eventType":"Download","instanceName":"Sonarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/sonarr_episode_delete.json`:**

```json
{"series":{"id":1,"title":"Show","path":"/tv/Show (2019)"},"episodes":[{"id":10}],
 "episodeFile":{"id":20,"relativePath":"Season 01/Show - S01E02.mkv","path":"/tv/Show (2019)/Season 01/Show - S01E02.mkv"},
 "deleteReason":"manual","eventType":"EpisodeFileDelete","instanceName":"Sonarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/sonarr_grab.json`:**

```json
{"series":{"id":1,"title":"Show","path":"/tv/Show (2019)"},"episodes":[{"id":10}],"release":{"releaseTitle":"Show.S01E02.1080p"},"downloadClient":"SABnzbd","downloadId":"x","eventType":"Grab","instanceName":"Sonarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/sonarr_import_complete.json`:**

```json
{"series":{"id":1,"title":"Show","path":"/tv/Show (2019)","tvdbId":123},
 "episodes":[{"id":11,"episodeNumber":1,"seasonNumber":2},{"id":12,"episodeNumber":2,"seasonNumber":2},{"id":13,"episodeNumber":3,"seasonNumber":2}],
 "episodeFiles":[
  {"id":31,"relativePath":"Season 02/Show - S02E01.mkv","path":"/tv/Show (2019)/Season 02/Show - S02E01.mkv","quality":"Bluray-1080p","size":1},
  {"id":32,"relativePath":"Season 02/Show - S02E02.mkv","path":"/tv/Show (2019)/Season 02/Show - S02E02.mkv","quality":"Bluray-1080p","size":1},
  {"id":33,"relativePath":"Season 02/Show - S02E03.mkv","path":"/tv/Show (2019)/Season 02/Show - S02E03.mkv","quality":"Bluray-1080p","size":1}],
 "downloadClient":"qBittorrent","downloadId":"ABC","sourcePath":"/downloads/Show.S02.1080p","destinationPath":"/tv/Show (2019)",
 "eventType":"Download","instanceName":"Sonarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/sonarr_rename.json`:**

```json
{"series":{"id":1,"title":"Show","path":"/tv/Show (2019)","tvdbId":123},
 "renamedEpisodeFiles":[{"id":20,"relativePath":"Season 01/Show (2019) - S01E02 - Two.mkv","path":"/tv/Show (2019)/Season 01/Show (2019) - S01E02 - Two.mkv","previousRelativePath":"Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv","previousPath":"/tv/Show (2019)/Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv"}],
 "eventType":"Rename","instanceName":"Sonarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/sonarr_series_delete.json`:**

```json
{"series":{"id":1,"title":"Show","path":"/tv/Show (2019)"},"deletedFiles":true,"eventType":"SeriesDelete","instanceName":"Sonarr","applicationUrl":""}
```

**`server/internal/library/arrhook/testdata/sonarr_test.json`:**

```json
{"series":{"id":1,"title":"Test Title","path":"C:\\testpath","tvdbId":1234,"tags":["test-tag"]},
 "episodes":[{"id":123,"episodeNumber":1,"seasonNumber":1,"title":"Test title"}],
 "eventType":"Test","instanceName":"Sonarr","applicationUrl":""}
```

- [ ] **Step 2: Write the failing test**

**Create `server/internal/library/arrhook/payload_test.go`:**

```go
package arrhook

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	const show = "/tv/Show (2019)/"
	for _, c := range []struct {
		kind, file, typ, want string
		paths                 []string
	}{
		{Sonarr, "sonarr_download.json", "Download", kindScan, []string{
			show + "Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv",
			show + "Season 01/Show - S01E02 - Two [HDTV-720p].mkv"}}, // the upgrade's replaced file
		{Sonarr, "sonarr_import_complete.json", "Download", kindScan, []string{
			show + "Season 02/Show - S02E01.mkv", show + "Season 02/Show - S02E02.mkv", show + "Season 02/Show - S02E03.mkv"}},
		{Sonarr, "sonarr_rename.json", "Rename", kindScan, []string{
			show + "Season 01/Show (2019) - S01E02 - Two.mkv", show + "Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv"}},
		{Sonarr, "sonarr_episode_delete.json", "EpisodeFileDelete", kindScan, []string{show + "Season 01/Show - S01E02.mkv"}},
		{Sonarr, "sonarr_series_delete.json", "SeriesDelete", kindScan, []string{"/tv/Show (2019)"}},
		{Sonarr, "sonarr_test.json", "Test", kindTest, nil},
		{Sonarr, "sonarr_grab.json", "Grab", kindIgnore, nil},
		{Radarr, "radarr_download.json", "Download", kindScan, []string{"/movies/Heat (1995)/Heat (1995) [Bluray-2160p].mkv"}},
		{Radarr, "radarr_download_relative.json", "Download", kindScan, []string{"/movies/Heat (1995)/Heat (1995).mkv"}},
		{Radarr, "radarr_rename.json", "Rename", kindScan, []string{
			"/movies/Heat (1995)/Heat (1995) {tmdb-949}.mkv", "/movies/Heat (1995)/Heat (1995).mkv"}},
		{Radarr, "radarr_movie_file_delete.json", "MovieFileDelete", kindScan, []string{"/movies/Heat (1995)/Heat (1995).mkv"}},
		{Radarr, "radarr_movie_delete.json", "MovieDelete", kindScan, []string{"/movies/Heat (1995)"}},
	} {
		e, err := parse(c.kind, fixture(t, c.file))
		if err != nil || e.Type != c.typ || e.Kind != c.want || !slices.Equal(e.Paths, c.paths) {
			t.Errorf("%s: %+v %v\nwant %s %s %q", c.file, e, err, c.typ, c.want, c.paths)
		}
	}
}

func TestParseRefusesBrokenPayloads(t *testing.T) {
	for name, c := range map[string]struct{ kind, body string }{
		"not JSON":                               {Sonarr, `eventType=Download`},
		"no eventType":                           {Sonarr, `{"series":{"path":"/tv/x"}}`},
		"download without file":                  {Sonarr, `{"eventType":"Download","series":{"path":"/tv/x"},"episodes":[]}`},
		"radarr payload sent to a sonarr source": {Sonarr, string(fixture(t, "radarr_download.json"))},
		"delete without file":                    {Radarr, `{"eventType":"MovieFileDelete","movie":{"folderPath":"/movies/x"}}`},
	} {
		if _, err := parse(c.kind, []byte(c.body)); !errors.Is(err, errSchema) {
			t.Errorf("%s: %v, want a schema error", name, err)
		}
	}
	// A new event type in a later release is acknowledged, not an error.
	if e, err := parse(Sonarr, []byte(`{"eventType":"SomethingNew"}`)); err != nil || e.Kind != kindIgnore {
		t.Errorf("unknown event: %+v %v", e, err)
	}
	// eventType in another case still matches.
	if e, err := parse(Radarr, []byte(`{"eventType":"test"}`)); err != nil || e.Kind != kindTest {
		t.Errorf("lower-case test: %+v %v", e, err)
	}
}

func TestCheckAgent(t *testing.T) {
	for _, c := range []struct {
		kind, ua, app string
		ok            bool
	}{
		{Sonarr, "Sonarr/4.0.20.3014 (ubuntu 22.04)", "Sonarr/4.0.20.3014", true},
		{Radarr, "Radarr/6.4.4.10685 (alpine 3.22.2)", "Radarr/6.4.4.10685", true},
		{Radarr, "Radarr/5.26.2.10099", "Radarr/5.26.2.10099", true},
		{Sonarr, "Sonarr/3.0.10.1567 (ubuntu 20.04)", "Sonarr/3.0.10.1567", false},  // v3 is end of life
		{Sonarr, "Sonarr/5.0.0.1 (ubuntu 24.04)", "Sonarr/5.0.0.1", false},          // not known yet
		{Sonarr, "Radarr/6.4.4.10685 (alpine 3.22.2)", "Radarr/6.4.4.10685", false}, // wrong app for the token
		{Sonarr, "Radarr/4.0.0.1 (ubuntu 22.04)", "Radarr/4.0.0.1", false},          // wrong app, though 4 is a Sonarr version
		{Radarr, "curl/8.5.0", "curl/8.5.0", false},
		{Radarr, "", "", false},
	} {
		app, err := checkAgent(c.kind, c.ua)
		if app != c.app || (err == nil) != c.ok {
			t.Errorf("checkAgent(%s, %q) = %q, %v", c.kind, c.ua, app, err)
		}
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
go -C server test -race -count=1 -run 'TestParse|TestCheckAgent' ./internal/library/arrhook/
```

Expected: FAIL to build: `undefined: kindScan` (then `parse`, `checkAgent`).

- [ ] **Step 4: Implement `payload.go`**

- `wire` decodes only:
  - `eventType`;
  - `series.path` and `movie.folderPath`;
  - `episodeFile`, `episodeFiles[]` and `movieFile` (`path`, `relativePath`);
  - `deletedFiles` as `json.RawMessage`, decoded as files only when it starts with `[`;
  - `renamedEpisodeFiles[]` and `renamedMovieFiles[]` (`path`, `relativePath`, `previousPath`).

  Unknown fields are ignored.
- A file's path is its `path`, else the kind's folder + `/` + `relativePath`.
- Event types compare case-insensitively, with the rules of Decision 5. Sonarr's `EpisodeFileDelete`/`SeriesDelete` count only for a Sonarr source, and Radarr's `MovieFileDelete`/`MovieDelete` only for a Radarr one.
- Errors: not JSON, no `eventType`, a known scan event with no path, more than `maxPaths`.
- `checkAgent`: the product before the first space, split at `/`. The app must equal the kind (case-insensitive), and the major version (before the first `.`) must be in `supported = {sonarr: {4}, radarr: {5, 6}}`. The returned app is the product (`Sonarr/4.0.20.3014`), even on error, for the receipt.

- [ ] **Step 5: Run the tests to verify they pass, then commit**

```bash
go -C server test -race -count=1 ./internal/library/arrhook/
git add server/internal/library/arrhook
git commit -m "feat(library): read Sonarr/Radarr webhook payloads and versions (LIB-4)"
```

---

### Task 3: The webhook handler

**Files:**
- Create: `server/internal/library/arrhook/handler.go`
- Test: `server/internal/library/arrhook/handler_test.go`

**Interfaces:**
- Consumes: Tasks 1–2; `contracts.WriteError` and `CodeInvalidRequest`, `CodeUnauthenticated`, `CodeRateLimited`, `CodeSourceUnavailable`, `CodeInternalError`; `obs.Log`, `obs.RequestID`.
- Produces:
  - `const Path = "/hooks/v1/arr"`; `type Hinter interface{ Hint(rootID, scope, cause string) error }` (satisfied by `*library.Scheduler`); `const CauseImport = "import"` (equal to `library.CauseImport`).
  - `func NewHandler(st *Store, h Hinter, log *slog.Logger) *Handler`; `(*Handler).ServeHTTP`.
  - Limits: `maxBodyBytes = 1 << 20`, `perSource = 60`, `failuresPerIP = 20`, `window = time.Minute`, `maxPeerEntries = 4096`.
  - The test seam `Handler.now func() time.Time`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/arrhook/handler_test.go`:**

```go
package arrhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const sonarrUA = "Sonarr/4.0.20.3014 (ubuntu 22.04)"

// hints records Hint calls; err makes them fail.
type hints struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (h *hints) Hint(rootID, scope, cause string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return h.err
	}
	h.calls = append(h.calls, rootID+" "+scope+" "+cause)
	return nil
}

func (h *hints) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

type hookFixture struct {
	st            *Store
	h             *Handler
	hints         *hints
	src           Source
	token, tvRoot string
	clock         time.Time
}

func newHook(t *testing.T) *hookFixture {
	t.Helper()
	st, tvRoot, _ := newStore(t)
	src, token, err := st.Add(context.Background(), "sonarr", Sonarr, []string{"TV"}, []Mapping{{"/tv", "TV"}})
	if err != nil {
		t.Fatal(err)
	}
	f := &hookFixture{st: st, hints: &hints{}, src: src, token: token, tvRoot: tvRoot, clock: time.Now()}
	f.h = NewHandler(st, f.hints, nil)
	f.h.now = func() time.Time { return f.clock }
	return f
}

// send posts body as the source would, with the given user agent and
// credentials, from peer 192.0.2.1.
func (f *hookFixture) send(method, ua, user, pass string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, Path, bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.1:5000"
	req.Header.Set("Content-Type", "application/json")
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f *hookFixture) ok(body []byte) *httptest.ResponseRecorder {
	return f.send(http.MethodPost, sonarrUA, f.src.ID, f.token, body)
}

func (f *hookFixture) lastReceipt(t *testing.T) Receipt {
	t.Helper()
	rs, err := f.st.Receipts(context.Background(), f.src.ID, 1)
	if err != nil || len(rs) != 1 {
		t.Fatalf("receipts %+v %v", rs, err)
	}
	return rs[0]
}

type answer struct {
	Result    string  `json:"result"`
	ReceiptID string  `json:"receiptId"`
	Scopes    []Scope `json:"scopes"`
	Code      string  `json:"code"` // error responses (contracts.APIError)
	Message   string  `json:"message"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) answer {
	t.Helper()
	var a answer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return a
}

// SCAN-02: an import queues a scoped scan of the file and of the file it
// replaced; the answer is a receipt, not a promise that it's scanned.
func TestImportQueuesScopedScans(t *testing.T) {
	f := newHook(t)
	rec := f.ok(fixture(t, "sonarr_download.json"))
	a := decode(t, rec)
	if rec.Code != http.StatusAccepted || a.Result != ResultQueued || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %+v", rec.Code, a)
	}
	want := []string{f.tvRoot + " Show (2019)/Season 01 import"} // new and replaced file share a folder
	if got := f.hints.list(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("hints %q, want %q", got, want)
	}
	r := f.lastReceipt(t)
	if r.ID != a.ReceiptID || r.Event != "Download" || r.App != "Sonarr/4.0.20.3014" || r.Result != ResultQueued ||
		len(r.Scopes) != 1 || r.Scopes[0].Path != "Show (2019)/Season 01" {
		t.Fatalf("receipt %+v", r)
	}
}

// A season pack is one scan of the season folder, and a repeated
// notification is coalesced by the scheduler's queue (one pending scope).
func TestSeasonPackIsOneScan(t *testing.T) {
	f := newHook(t)
	f.ok(fixture(t, "sonarr_import_complete.json"))
	if got := f.hints.list(); len(got) != 1 || got[0] != f.tvRoot+" Show (2019)/Season 02 import" {
		t.Fatalf("hints %q", got)
	}
}

// The connection test checks credentials and reachability and queues nothing.
func TestConnectionTestQueuesNothing(t *testing.T) {
	f := newHook(t)
	rec := f.ok(fixture(t, "sonarr_test.json"))
	if a := decode(t, rec); rec.Code != http.StatusOK || a.Result != ResultTest || len(f.hints.list()) != 0 {
		t.Fatalf("%d %+v %q", rec.Code, a, f.hints.list())
	}
	if r := f.lastReceipt(t); r.Result != ResultTest {
		t.Fatalf("receipt %+v", r)
	}
	rec = f.ok(fixture(t, "sonarr_grab.json"))
	if a := decode(t, rec); rec.Code != http.StatusOK || a.Result != ResultIgnored || len(f.hints.list()) != 0 {
		t.Fatalf("grab: %d %+v", rec.Code, a)
	}
}

// Paths that can't be placed inside the source's folders reject the whole
// notification: nothing is queued, and Sonarr shows the failure.
func TestPathsOutsideTheSourceAreRejected(t *testing.T) {
	f := newHook(t)
	for name, body := range map[string]string{
		"unmapped":       `{"eventType":"Download","series":{"path":"/anime/x"},"episodeFile":{"path":"/anime/x/a.mkv"}}`,
		"escape":         `{"eventType":"Download","series":{"path":"/tv/x"},"episodeFile":{"path":"/tv/../../etc/passwd"}}`,
		"one bad of two": `{"eventType":"Download","series":{"path":"/tv/x"},"episodeFile":{"path":"/tv/x/a.mkv"},"deletedFiles":[{"path":"/elsewhere/a.mkv"}]}`,
		"relative":       `{"eventType":"EpisodeFileDelete","series":{"path":"/tv/x"},"episodeFile":{"path":"x/a.mkv"}}`,
	} {
		rec := f.ok([]byte(body))
		if a := decode(t, rec); rec.Code != http.StatusBadRequest || a.Code != "invalid_request" {
			t.Errorf("%s: %d %+v", name, rec.Code, a)
		}
		if r := f.lastReceipt(t); r.Result != ResultRejected || r.Reason == "" {
			t.Errorf("%s: receipt %+v", name, r)
		}
	}
	if got := f.hints.list(); len(got) != 0 {
		t.Fatalf("queued %q", got)
	}
	// The source's whole folder is a valid scope (a series deleted at the root is unusual but harmless).
	if rec := f.ok([]byte(`{"eventType":"SeriesDelete","series":{"path":"/tv"}}`)); rec.Code != http.StatusAccepted {
		t.Fatalf("root scope: %d", rec.Code)
	}
}

func TestAuthenticationAndLimits(t *testing.T) {
	f := newHook(t)
	body := fixture(t, "sonarr_download.json")
	rec := f.send(http.MethodPost, sonarrUA, "", "", body)
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("no credentials: %d %v", rec.Code, rec.Header())
	}
	if rec := f.send(http.MethodPost, sonarrUA, f.src.ID, "lfh_wrong", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := f.send(http.MethodGet, sonarrUA, f.src.ID, f.token, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := f.send(http.MethodPut, sonarrUA, f.src.ID, f.token, body); rec.Code != http.StatusAccepted {
		t.Fatalf("PUT: %d", rec.Code) // Sonarr can be set to PUT
	}

	// Wrong app or an unsupported version: rejected with the reason.
	for _, ua := range []string{"Radarr/6.4.4.10685 (alpine 3.22.2)", "Sonarr/3.0.10.1567 (ubuntu 20.04)", ""} {
		rec := f.send(http.MethodPost, ua, f.src.ID, f.token, body)
		if a := decode(t, rec); rec.Code != http.StatusBadRequest || !strings.Contains(a.Message, "version") && !strings.Contains(a.Message, "came from") {
			t.Errorf("UA %q: %d %+v", ua, rec.Code, a)
		}
	}

	// Too large: a valid notification padded past the limit is refused.
	valid := fixture(t, "sonarr_download.json")
	big := append([]byte(`{"pad":"`+strings.Repeat("x", maxBodyBytes)+`",`), valid[1:]...)
	if rec := f.ok(big); rec.Code != http.StatusBadRequest {
		t.Fatalf("large body: %d", rec.Code)
	}

	// Per-source rate: beyond perSource in a window → 429; the next window is fine.
	f.clock = f.clock.Add(time.Hour)
	for i := range perSource {
		if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusOK {
			t.Fatalf("notification %d: %d", i, rec.Code)
		}
	}
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", rec.Code)
	}
	f.clock = f.clock.Add(window)
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusOK {
		t.Fatalf("next window: %d", rec.Code)
	}

	// Repeated failed sign-ins from one peer lock it out, even with the right token.
	for range failuresPerIP {
		f.send(http.MethodPost, sonarrUA, f.src.ID, "lfh_wrong", body)
	}
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after failures: %d", rec.Code)
	}

	// Revoked: refused at once.
	f.clock = f.clock.Add(window)
	f.st.Revoke(context.Background(), "sonarr")
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", rec.Code)
	}
}

// If the scheduler can't take the hint (a library added moments ago), the
// source is told to retry later; the next reconciliation scan covers it.
func TestSchedulerNotReady(t *testing.T) {
	f := newHook(t)
	f.hints.err = errors.New("library: not found")
	rec := f.ok(fixture(t, "sonarr_download.json"))
	if a := decode(t, rec); rec.Code != http.StatusServiceUnavailable || a.Code != "source_unavailable" {
		t.Fatalf("%d %+v", rec.Code, a)
	}
	if r := f.lastReceipt(t); r.Result != ResultRejected || !strings.Contains(r.Reason, "not ready") {
		t.Fatalf("receipt %+v", r)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 -run 'Import|Season|Connection|Paths|Authentication|SchedulerNotReady' ./internal/library/arrhook/
```

Expected: FAIL to build: `undefined: Handler`, `undefined: NewHandler`, `undefined: Path`, `undefined: maxBodyBytes`.

- [ ] **Step 3: Implement `handler.go`**

`ServeHTTP`, in order:
1. `Cache-Control: no-store`. A method other than POST/PUT → 405 with `Allow: POST, PUT`.
2. If the peer (`peerKey`, copied from `auth`) has `failuresPerIP` failures this window → 429.
3. `r.BasicAuth()` then `Authenticate`:
   - a database error → 500;
   - no credentials or `ErrUnauthorized` → count a failure for the peer, log `library.hook` WARN `unauthenticated` with `peer`, and answer 401 with `WWW-Authenticate: Basic realm="lisimflix-hooks", charset="UTF-8"`.
4. If the source exceeds `perSource` this window → 429, logged, no receipt.
5. The receipt (`"rcpt-" + rand.Text()`, the request ID). `checkAgent` gives the app. Read the body through `http.MaxBytesReader(maxBodyBytes)` (an error → reject). Then `parse`.
6. Version error → reject. Parse error → reject. `kindTest` → 200 `test`. `kindIgnore` → 200 `ignored` ("event doesn't change files").
7. `resolve` each path; the first error rejects the whole notification with `"path <p>: <err>"` (Decision 6).
8. `group`, then `Hint(root, path, CauseImport)` for each. An error → 503, receipt `rejected` with reason "library not ready: …".
9. 202 `queued`.

`finish`: record the receipt (an error is logged, not fatal), log `library.hook` (Decision 12), then answer:
- 400 / 503 through `contracts.WriteError` with "Notification rejected: <reason>" or "The library isn't ready yet; the next scan will catch up.";
- otherwise JSON `{"result","receiptId","scopes"}` with `scopes` never `null`.

The counters are `map[string]*counter{start time.Time; n int}`, reset when a window has passed. When a map is full, expired entries are dropped first; if it's still full, the request is refused.

- [ ] **Step 4: Run the tests to verify they pass, then commit**

```bash
go -C server test -race -count=1 ./internal/library/arrhook/
git add server/internal/library/arrhook
git commit -m "feat(library): authenticated, bounded Sonarr/Radarr webhook endpoint (LIB-4)"
```

---

### Task 4: Mount, `serve` wiring, the `hook` command and docs

**Files:**
- Modify: `server/internal/app/app.go`, `server/internal/app/app_test.go`, `server/cmd/lisimflix/serve.go`, `server/cmd/lisimflix/main.go`, `server/cmd/lisimflix/library.go`, `README.md`, `deploy/README.md`
- Create: `server/cmd/lisimflix/hook.go`, `server/cmd/lisimflix/hook_test.go`, `docs/contracts/arr-webhook.md`

**Interfaces:**
- Consumes: Task 3's `Handler`, `Path`; Task 1's `Store`; LIB-2b's `server.scans`, `fastLibrary`, `waitFor`, `newFixture`, `libraryOptions`.
- Produces: `type Mount struct{ Pattern string; Handler http.Handler }` and `app.New(..., log *slog.Logger, mounts ...Mount)`; `openForCLI(ctx, cfgPath, dbPath, backups string) (m1config.Config, *storage.DB, error)`; `cmdHook(args []string, stdout io.Writer) error`.

- [ ] **Step 1: Write the failing tests**

In `app_test.go`, `TestNewRejectsInconsistentInputs` gains three cases:

```go
		"mount under /api/": func() error {
			_, err := New(cfg, plainAM, play, web, ready, nil, Mount{"/api/v1/hooks", http.NotFoundHandler()})
			return err
		},
		"mount under /health/": func() error {
			_, err := New(cfg, plainAM, play, web, ready, nil, Mount{"/health/x", http.NotFoundHandler()})
			return err
		},
		"mount without handler": func() error { _, err := New(cfg, plainAM, play, web, ready, nil, Mount{"/hooks/x", nil}); return err },
```

and this test is appended:

```go
// A mount is served outside the API's Origin/CSRF checks (its callers are
// servers, not browsers), with the same security headers and request IDs,
// and the API stays protected.
func TestMountSkipsBrowserChecks(t *testing.T) {
	cfg := m1config.Config{PublicOrigin: lanOrigin, CSRFKey: bytes.Repeat([]byte{3}, 32),
		Viewer: m1config.Viewer{AccountID: "a", ProfileID: "p", Username: "viewer", PasswordHash: viewerHash}}
	am, _ := auth.NewManager(cfg.Viewer, false, func(string) {})
	hook := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	h, err := New(cfg, am, &fakePlayback{}, web, func(context.Context) error { return nil }, nil, Mount{"/hooks/v1/arr", hook})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/hooks/v1/arr", "application/json", strings.NewReader(`{}`)) // no Origin, no CSRF token
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Request-Id") == "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("mount: %d %v", resp.StatusCode, resp.Header)
	}
	resp, err = http.Post(srv.URL+"/api/v1/auth/logout", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("API without Origin: %d, want 403", resp.StatusCode)
	}
}
```

**Create `server/cmd/lisimflix/hook_test.go`:**

```go
package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/library"
)

// LIB-4 wiring: a Radarr source added with the CLI notifies serve, without
// browser headers, and the imported file is scanned with cause "import".
func TestServeImportNotification(t *testing.T) {
	fastLibrary(t)
	libraryOptions.NoWatch = true // only the notification can find the new film
	f := newFixture(t)
	films := filepath.Join(f.dir, "media", "Films")
	os.MkdirAll(filepath.Join(films, "Alien (1979)"), 0o755)
	os.WriteFile(filepath.Join(films, "Alien (1979)", "Alien (1979).mkv"), []byte("x"), 0o644)
	run1 := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if code := run(append(args, "--config", f.cfgPath), nil, &out, &out); code != 0 {
			t.Fatalf("%v: %s", args, out.String())
		}
		return out.String()
	}
	run1("library", "add", "--name", "Films", "--type", "movies", "--folder", "Films")
	out := run1("hook", "add", "--name", "radarr", "--kind", "radarr", "--folder", "Films", "--map", "/movies=Films")
	user := regexp.MustCompile(`Username:\s+(\S+)`).FindStringSubmatch(out)
	pass := regexp.MustCompile(`Password:\s+(\S+)`).FindStringSubmatch(out)
	if user == nil || pass == nil || !strings.Contains(out, "/hooks/v1/arr") {
		t.Fatalf("hook add output:\n%s", out)
	}
	status := func() library.RootStatus {
		st, _ := f.s.scans.Status(context.Background())
		for _, s := range st {
			if s.Path == "Films" {
				return s
			}
		}
		return library.RootStatus{}
	}
	waitFor(t, "library picked up", func() bool { s := status(); return s.LastRun != nil && s.LastRun.Status == library.RunCompleted })

	os.MkdirAll(filepath.Join(films, "Heat (1995)"), 0o755)
	os.WriteFile(filepath.Join(films, "Heat (1995)", "Heat (1995).mkv"), []byte("x"), 0o644)
	notify := func(pw string) int {
		req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/hooks/v1/arr", strings.NewReader(
			`{"eventType":"Download","movie":{"title":"Heat","year":1995,"folderPath":"/movies/Heat (1995)"},`+
				`"movieFile":{"relativePath":"Heat (1995).mkv","path":"/movies/Heat (1995)/Heat (1995).mkv"},"isUpgrade":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Radarr/6.4.4.10685 (alpine 3.22.2)")
		req.SetBasicAuth(user[1], pw)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := notify(pass[1]); code != http.StatusAccepted {
		t.Fatalf("notification: %d", code)
	}
	waitFor(t, "import scan", func() bool {
		s := status()
		return s.LastRun != nil && s.LastRun.Cause == library.CauseImport && s.LastRun.Status == library.RunCompleted && s.Files.Present == 2
	})
	if out := run1("hook", "receipts", "--name", "radarr"); !strings.Contains(out, "queued") || !strings.Contains(out, "Heat (1995)") {
		t.Fatalf("receipts:\n%s", out)
	}
	run1("hook", "revoke", "--name", "radarr")
	if code := notify(pass[1]); code != http.StatusUnauthorized {
		t.Fatalf("after revoke: %d", code)
	}
	if out := run1("hook", "list"); !strings.Contains(out, "revoked") {
		t.Fatalf("list:\n%s", out)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -race -count=1 ./internal/app/ ./cmd/...
```

Expected: FAIL to build in `app`: `undefined: Mount`, `too many arguments in call to New`. In `cmd`, `TestServeImportNotification` fails: `unknown command "hook"`.

- [ ] **Step 3: Implement**

- **`app.go`:**
  - `New` gains `mounts ...Mount`, so existing callers are unchanged.
  - Each pattern must start with `/` and not be under `/api/` or `/health/`, and its handler must be non-nil, else an error.
  - Mounts register on the root mux before the SPA fallback, inside `observe(securityHeaders(…))`.
- **`library.go`:** factor `openForCLI` (load the config, `storagePaths`, `openStorage` with the snapshot options) out of `cmdLibrary` and use it there.
- **`serve.go`:**
  - `openStorage` registers `arrhook.Migrations()` after `library.Migrations()`.
  - When the scheduler exists, `compose` mounts `app.Mount{Pattern: arrhook.Path, Handler: arrhook.NewHandler(arrhook.NewStore(s.db), s.scans, log)}`. Without storage there's no mount.
- **`hook.go`:** `cmdHook` with `add|list|revoke|receipts`.
  - **Flags:** `--config`, `--db`, `--backups`, `--name`, `--kind`, `--n` (default 20), repeated `--folder` and `--map REMOTE=LOCAL`.
  - **`add` prints** `added <id> (<name>)`, then the exact settings: where to click (Settings > Connect > + > Webhook); the triggers (On Import, On Upgrade, On Rename, On File Delete, On Series/Movie Delete, and On Import Complete); `URL: <publicOrigin>/hooks/v1/arr`; `Method: POST`; `Username: <id>`; `Password: <token>`. Then: "The password is shown only now. Press Test in <App>; `lisimflix hook receipts --name <name>` shows the result."
  - **`list`:** a tabwriter table `SOURCE NAME KIND FOLDERS MAPPINGS STATE`.
  - **`receipts`:** `RECEIVED EVENT APP RESULT SCANS REASON`, newest first; an unknown name is `ErrNotFound`.
- **`main.go`:** the `hook` command, and `hook` in the usage line and package comment.
- **`docs/contracts/arr-webhook.md`:** the endpoint (path, methods), authentication, the `User-Agent` rule and supported versions, the events table (Decision 5), path mapping and the all-or-nothing rule, the response table (Decision 9) with example bodies, the limits, and the receipt-versus-completion note.
- **`README.md`:** extend the LIB-2 status sentence with: "`lisimflix hook add` connects Sonarr or Radarr, so imports are scanned at once (LIB-4)."
- **`deploy/README.md`,** a section **6. Sonarr and Radarr (optional)**, after Libraries:
  1. `docker compose … exec lisimflix lisimflix hook add --name sonarr --kind sonarr --folder TV --map /tv=TV`, explaining `--map`: Sonarr's root folder as Sonarr sees it, then the same folder as given to `library add`.
  2. Paste the printed settings into Sonarr (Settings → Connect → Webhook) and press Test.
  3. `hook receipts` shows each notification and why one was rejected.

  Plus these notes:
  - Sonarr/Radarr must reach the URL (the same Docker network or the host address).
  - Don't override `User-Agent` in the webhook's custom headers.
  - `hook revoke --name` stops a token at once.
  - Deleting the last item of a library shows the folder as offline until it has content again (Review Focus 5).

- [ ] **Step 4: Run the full check**

```bash
go -C server test -race -count=3 ./internal/library/... ./internal/app/ ./cmd/...
make check
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server docs/contracts/arr-webhook.md README.md deploy/README.md
git commit -m "feat(library): serve the import webhook; hook command and docs (LIB-4)"
```

---

### Task 5: Integration run with real Sonarr and Radarr

**Files:** none. The evidence goes on #98.

- [ ] **Step 1: Set up throwaway containers**

1. `make image`, then a scratch media folder with `TV/Show (2019)/Season 01/…` and `Movies/Heat (1995)/…`.
2. Create a private Docker network and run the image on it as `lsf-app`, with origin `http://lsf-app:8420`.
3. Run `library add` TV and Movies, then `hook add` sonarr (`--folder TV --map /tv=TV`) and radarr (`--folder Movies --map /movies=Movies`).
4. Start **fresh** Sonarr and Radarr containers on the same network, each with its own empty config folder. Never use an existing installation: this adds a connection and imports files. Mount Radarr's `/movies` on the scratch `Movies` folder and `/import` on a folder holding one clip.

- [ ] **Step 2: Exercise it through the apps' own API**

| Action | Expected |
|---|---|
| Each app: `POST /api/v3/notification/test` with the printed settings | HTTP 200 from the app; `hook receipts` shows `Test`, the app's real version, result `test`; nothing scanned |
| The same with a wrong password | the app reports `401:Unauthorized`; Lisimflix logs `unauthenticated` with the peer |
| Radarr: add Heat (TMDB 949) at `/movies/Heat (1995)`, add the webhook, `ManualImport` the clip | `library.hook` `Download` (and `MovieFileDelete` for the replaced file) queued; one `library.scan` `cause":"import"` of `Heat (1995)`: added 1, missing 1; then probed |
| Radarr: delete the movie with its files | `MovieDelete` queued; the scoped scan finds the library folder empty → `root_empty`, nothing marked missing (Review Focus 5) |

- [ ] **Step 3: Record and clean up**

Post the log lines and receipts on #98 as LIB-4 "integrated". Remove the containers, network and scratch folders.

---

## Self-review

**Spec coverage:**

| Requirement (§3.6, SCAN-02) | Where |
|---|---|
| Admin configures Sonarr/Radarr to notify after import/upgrade | Task 4 `hook add` + deploy README; Task 5 with the real apps |
| A validated event queues only the affected file/folder/library | Tasks 1–3: `resolve`, `group`, `Hint` of scopes; never a whole-catalogue scan per file |
| Batch/season packs and duplicates coalesce by source/revision | `group` (Task 1) + the scheduler's coalescing (LIB-2b); `TestSeasonPackIsOneScan` |
| Receipt means queued, not playable or matched | Decision 9; 202 + receipts; completion = `import` runs (`TestServeImportNotification`) |
| Notifications are hints, not proof of stability or permission | scans run the normal pipeline (quiet period, probes); paths confined to the source's folders |
| Revocable credentials limited to configured roots | Task 1 (`Add`, `Revoke`, `arrhook_roots`), Task 3 |
| Redact credentials; bound payload and event rates | Decision 12; Task 3 limits (`TestAuthenticationAndLimits`) |
| Explicit remote-to-container path mappings; reject escapes and unmapped roots | Task 1 `resolve` (`TestResolve`), Task 3 all-or-nothing |
| An absent/offline file is retryable, never a reason to retire the library | scans mark missing only (LIB-2a/2b rules); an empty root is offline (Task 5, Review Focus 5) |
| Never execute payload commands; no callback deletes media, grants access or bypasses checks | the handler only queues scans; no other action exists |
| Validate the supported app version and event schema | `checkAgent`, `parse` (Task 2) |
| Connection test validates reachability/auth without a full scan | `Test` → 200, no hints (Task 3); live in Task 5 |
| Missed notifications recovered by normal reconciliation | LIB-2b's watcher, rechecks and 6-hour scans; 429/503 answers say so |
| SCAN-02 fixture list | import/upgrade/test (Tasks 2–3), duplicate/batch (`TestGroup`, season pack), path-mapping/escapes (`TestResolve`, rejections), unavailable roots (503, `root_empty` live), missed events (2b), receipt vs completion (receipts + runs) |

**Verified while writing this plan:**
- **Prototype:** all of it ran on the LIB-2b prototype (plus 2b's `Hint`). `make check` passed, and the library, app and command tests passed 5 times in a row under `-race`.
- **Rebuild:** each task was rebuilt in order on the 2b prototype. Each failed with its Step 2 messages before its implementation and passed after.
- **Real apps (Task 5):** run on zraid against fresh **Sonarr 4.0.20.3014** and **Radarr 6.4.4.10685** containers. Every row of the table matched. The import was a real upgrade: Radarr replaced the existing file and sent `MovieFileDelete` then `Download`, and both became one scoped scan (added 1, missing 1).
- **Mutation check:** each of these 27 made a test fail:
  - mappings matched by string prefix
  - the first mapping used instead of the longest
  - paths outside the source's folders accepted
  - Windows paths not converted
  - relative paths accepted
  - season packs not grouped
  - duplicates kept
  - covered scopes kept
  - an upgrade's replaced files not scanned
  - Test queuing a scan
  - unknown events failing
  - no `relativePath` fallback
  - a rename's previous path dropped
  - the app not matched to the kind
  - the major version not checked
  - revoked sources accepted
  - tokens not compared
  - unknown folders accepted
  - receipts not pruned
  - no per-source limit
  - no failed sign-in lockout
  - partial acceptance of a notification
  - a `Hint` failure answered 202
  - no body limit
  - a mount allowed under `/api/`
  - `Hint` not validating its scope
  - `Hint` dropping its cause
- **Found while prototyping:**
  - `deletedFiles` is a boolean on delete events (Decision 5).
  - A duplicate path was counted as a two-file folder.
  - The first body-limit and app-kind tests passed for the wrong reason (both strengthened).
  - Radarr deleting the M1 movie stops `serve` from starting: filed as #99.
- **Placeholders:** none.
- **Names and types** match between tasks: Task 3's tests use Task 1's `newStore` and Task 2's `fixture`; Task 4 uses LIB-2b's `fastLibrary`, `waitFor` and `newFixture`.
