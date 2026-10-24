# Phase 1A: Core Skeleton Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A running single-container Lisimflix server with config, SQLite + migrations, the
`/api/v1` router with health check, module mounting, auth-guard interface, embedded
placeholder web page, Docker image with jellyfin-ffmpeg, and CI, plus the team conventions
that every later plan builds on.

**Architecture:** One Go binary (`server/cmd/lisimflix`) wires small internal packages:
`config` (env), `db` (SQLite + embedded migrations), `httpx` (JSON helpers), `authctx`
(principal + guard interface), `web` (embedded SPA), `app` (router + modules). The Docker
image adds the jellyfin-ffmpeg portable build and runs the binary as a non-root user.

**Tech Stack:** Go 1.27, chi v5, modernc.org/sqlite, log/slog, Docker (debian trixie-slim),
jellyfin-ffmpeg 8.1.3-1, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (sections 2.1, 2.2, 7, 8)
**Index / shared contracts:** `docs/superpowers/plans/2026-10-24-phase1-index.md`

## Global Constraints

- Go 1.27 (`golang:1.27-trixie`). Module `github.com/Lisimflix/lisimflix/server` rooted at `server/`.
- `CGO_ENABLED=0` for builds. SQLite driver `modernc.org/sqlite`, registered name `"sqlite"`.
- Router `github.com/go-chi/chi/v5`. Logging `log/slog` (JSON handler in production).
- API under `/api/v1`. Error envelope `{"error":{"code":"...","message":"..."}}`.
- `/media` read-only, never written to.
- jellyfin-ffmpeg 8.1.3-1 portable, sha256 `b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a`, installed to `/usr/lib/jellyfin-ffmpeg/`.
- Port 8420. Volumes `/config`, `/media` (ro), `/cache`. Runs as PUID/PGID (default 99/100).
- Migration file names `YYYYMMDDHHMMSS_<module>_<what>.sql`.
- Contract names in the index are fixed. Do not rename them.

## File Structure

```
.editorconfig                       editor defaults (tabs for Go/Makefile)
.gitignore                          (exists) append .dev/ and .superpowers/
.dockerignore                       keeps build context small
Makefile                            Docker-wrapped go test/vet/fmt/tidy, image, run
README.md                           what it is, quick start
CONTRIBUTING.md                     (exists, from PR #1) append "Development" and "Code rules" sections
CLAUDE.md                           (exists, from PR #1) append "Code rules for agents" section
Dockerfile                          build → ffmpeg → runtime stages
deploy/entrypoint.sh                drop to PUID/PGID, exec binary
deploy/docker-compose.yml           reference deployment
deploy/unraid/lisimflix.xml         Unraid template
.github/workflows/ci.yml            test on PR, push image on main/tags
server/go.mod, go.sum
server/cmd/lisimflix/main.go        entrypoint: run(), healthcheck subcommand
server/cmd/lisimflix/main_test.go
server/internal/version/version.go(+_test)
server/internal/config/config.go(+_test)
server/internal/db/db.go            Open, settings
server/internal/db/migrate.go       embedded migration runner
server/internal/db/db_test.go, migrate_test.go
server/internal/db/dbtest/dbtest.go test helper
server/internal/db/migrations/20261024000000_core_settings.sql
server/internal/httpx/json.go(+_test)
server/internal/authctx/authctx.go(+_test)
server/internal/web/web.go(+_test)
server/internal/web/dist/index.html, robots.txt   placeholder until phase 3
server/internal/app/app.go(+_test)
web/README.md                       placeholder; React app arrives in phase 3
```

---

### Task 1: Repo conventions, Go module, version package, Makefile

**Files:**
- Create: `.editorconfig`, `.dockerignore`, `Makefile`, `README.md`, `web/README.md`
- Modify (append only, keep the existing text from PR #1): `.gitignore`, `CONTRIBUTING.md`, `CLAUDE.md`

**Before starting:** `git config core.hooksPath .githooks`, then `git switch main && git pull && git switch -c feat/1a-core`.
- Create: `server/go.mod`, `server/internal/version/version.go`
- Test: `server/internal/version/version_test.go`

**Interfaces:**
- Consumes: none
- Produces: `version.Version` (string var, set by ldflags), `version.String() string`; `make test|vet|fmt|tidy|image|run`.

- [ ] **Step 1: Write repo housekeeping files**

`.editorconfig`:
```ini
root = true

[*]
end_of_line = lf
insert_final_newline = true
charset = utf-8
trim_trailing_whitespace = true
indent_style = space
indent_size = 2

[{*.go,Makefile,go.mod}]
indent_style = tab
indent_size = 4
```

`.gitignore`: append these lines (the Go, Node, secrets and editor entries already exist):
```

# Local dev data and brainstorm mockups
.dev/
.superpowers/
```
(Skip `.superpowers/` if it is already present. The `docs/phase1-plans` PR adds it.)

`.dockerignore`:
```
.git
.github
.superpowers
.dev
docs
**/*_test.go
**/testdata
```

`web/README.md`:
```markdown
# web

The React + TypeScript web app lands here in phase 3. Until then the server embeds a
placeholder page from `server/internal/web/dist/`.
```

- [ ] **Step 2: Write `Makefile`** (recipe lines start with a TAB character)

```make
GO_IMAGE   := golang:1.27-trixie
DOCKER_GO  := docker run --rm -v $(CURDIR)/server:/src -w /src \
              -v lisimflix-gomod:/go/pkg/mod -v lisimflix-gocache:/root/.cache/go-build \
              -e CGO_ENABLED=0 $(GO_IMAGE)
VERSION    ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
MEDIA      ?= $(CURDIR)/.dev/media

.PHONY: test vet fmt tidy image run

test:
	$(DOCKER_GO) go test ./...

vet:
	$(DOCKER_GO) go vet ./...

fmt:
	$(DOCKER_GO) gofmt -l -w .

tidy:
	$(DOCKER_GO) go mod tidy

image:
	docker build -t lisimflix:dev --build-arg VERSION=$(VERSION) .

run: image
	mkdir -p .dev/config .dev/cache $(MEDIA)
	docker run --rm --name lisimflix-dev -p 8420:8420 \
		-v $(CURDIR)/.dev/config:/config -v $(CURDIR)/.dev/cache:/cache \
		-v $(MEDIA):/media:ro lisimflix:dev
```

- [ ] **Step 3: Write `README.md`**

````markdown
# Lisimflix

Self-hosted media server: libraries, movie/TV/anime metadata, multiple accounts and
profiles, and adaptive GPU streaming, all in one container.

Design: [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](docs/superpowers/specs/2026-10-24-lisimflix-design.md)

## Quick start (Docker)

```bash
docker run -d --name lisimflix -p 8420:8420 \
  -v /path/to/config:/config -v /path/to/media:/media:ro -v /path/to/cache:/cache \
  ghcr.io/lisimflix/lisimflix:edge
```

Open `http://<host>:8420`. NVIDIA GPU: add `--runtime=nvidia -e NVIDIA_VISIBLE_DEVICES=all`.

## Development

Only Docker and make are needed. Go runs inside a container.

```bash
make test    # unit tests
make vet     # static checks
make fmt     # format code
make run     # build image and run on :8420 with ./.dev folders
```

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR.
````

- [ ] **Step 4: Append to `CONTRIBUTING.md`** (after the existing "Never commit" section; leave the existing text unchanged)

```markdown

## Picking up plan work

- Plans live in `docs/superpowers/plans/`. Each has an issue on the project board. Assign yourself first.
- Read the plan's **Global Constraints** and the shared contracts in the phase index.
  Contract names and signatures are fixed. Changing one needs a PR that edits the index,
  reviewed by the owners of every plan that uses it.

## Development

Only Docker and make are needed. Go runs inside a container.

- `make fmt test vet` before every push. CI runs the same checks.
- `make run` builds the image and runs it on :8420 with `./.dev` folders.

## Code rules

- Go code lives in `server/`. TDD: write the failing test first.
- No network in unit tests. Use `httptest.Server` with recorded fixtures under the package's `testdata/`.
- `/media` is read-only. Nothing may write, move or delete under the media root.
- HTTP handler errors use `httpx.WriteError(w, status, "snake_code", "message")`.
- Each module owns its tables (see the phase index). Don't write to another module's tables;
  call that module's Store.
- AI-written commits end with a `Co-Authored-By:` line naming the model.

## Database migrations

- One folder: `server/internal/db/migrations/`.
- Name: `YYYYMMDDHHMMSS_<module>_<what>.sql` (UTC time when you write it), e.g.
  `20261025093000_auth_users.sql`. The runner rejects other names.
- Never edit a migration after it is merged to `main`. Add a new one.
- Use `STRICT` tables and explicit foreign keys with `ON DELETE` behaviour.
```

- [ ] **Step 5: Append to `CLAUDE.md`** (leave the existing rules unchanged)

```markdown

## Code rules for agents

- Also read the phase index in `docs/superpowers/plans/` (shared contracts).
- Several humans and agents work in parallel. Stay inside the files your plan task lists.
  If you need a contract change, stop and say so. Don't change it silently.
- Go is not installed on the host. Use `make test`, `make vet`, `make fmt` (Docker-wrapped).
- TDD always: failing test → minimal code → passing test → commit.
- Never write under the media root.
- Migrations: new timestamped file only, never edit merged ones.
- Match surrounding style: small focused files, `slog` for logs, errors wrapped with `%w` and
  context (`fmt.Errorf("scan %s: %w", path, err)`).
- End commit messages with a `Co-Authored-By:` line for the model you are.
```

- [ ] **Step 6: Write the failing version test**

`server/go.mod`:
```
module github.com/Lisimflix/lisimflix/server

go 1.27
```

`server/internal/version/version_test.go`:
```go
package version

import "testing"

func TestStringDefaultsToDev(t *testing.T) {
	if got := String(); got != "lisimflix dev" {
		t.Fatalf("String() = %q, want %q", got, "lisimflix dev")
	}
}
```

- [ ] **Step 7: Run it and confirm it fails**

Run: `make test`
Expected: FAIL, `undefined: String`

- [ ] **Step 8: Implement**

`server/internal/version/version.go`:
```go
// Package version holds the build version, injected with
// -ldflags "-X github.com/Lisimflix/lisimflix/server/internal/version.Version=...".
package version

// Version is the build version. "dev" for local builds.
var Version = "dev"

// String returns the human-readable version line.
func String() string { return "lisimflix " + Version }
```

- [ ] **Step 9: Run tests, expect PASS**

Run: `make test`
Expected: `ok  github.com/Lisimflix/lisimflix/server/internal/version`

- [ ] **Step 10: Commit**

```bash
git add .editorconfig .gitignore .dockerignore Makefile README.md CONTRIBUTING.md CLAUDE.md web/README.md server/
git commit -m "chore(core): dev tooling, Go module and version package"
```

---

### Task 2: Config from environment

**Files:**
- Create: `server/internal/config/config.go`
- Test: `server/internal/config/config_test.go`

**Interfaces:**
- Consumes: none
- Produces: `config.Config` (fields exactly as in the index), `config.Load(getenv func(string) string) (Config, error)`, `(Config) DBPath() string`, `(Config) SlogLevel() slog.Level`.

- [ ] **Step 1: Write the failing tests**

`server/internal/config/config_test.go`:
```go
package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		ConfigDir:   "/config",
		MediaDir:    "/media",
		CacheDir:    "/cache",
		HTTPAddr:    ":8420",
		PublicURL:   "",
		LogLevel:    "info",
		FFprobePath: "/usr/lib/jellyfin-ffmpeg/ffprobe",
		FFmpegPath:  "/usr/lib/jellyfin-ffmpeg/ffmpeg",
	}
	if c != want {
		t.Fatalf("Load() = %+v, want %+v", c, want)
	}
	if c.DBPath() != "/config/lisimflix.db" {
		t.Fatalf("DBPath() = %q", c.DBPath())
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"LISIMFLIX_CONFIG_DIR": "/data/cfg",
		"LISIMFLIX_HTTP_ADDR":  "127.0.0.1:9000",
		"LISIMFLIX_PUBLIC_URL": "https://watch.example.com/",
		"LISIMFLIX_LOG_LEVEL":  " debug ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfigDir != "/data/cfg" || c.HTTPAddr != "127.0.0.1:9000" {
		t.Fatalf("overrides not applied: %+v", c)
	}
	if c.PublicURL != "https://watch.example.com" {
		t.Fatalf("PublicURL trailing slash not trimmed: %q", c.PublicURL)
	}
	if c.SlogLevel() != slog.LevelDebug {
		t.Fatalf("SlogLevel() = %v", c.SlogLevel())
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string]map[string]string{
		"relative dir":  {"LISIMFLIX_MEDIA_DIR": "media"},
		"bad log level": {"LISIMFLIX_LOG_LEVEL": "loud"},
		"bad url":       {"LISIMFLIX_PUBLIC_URL": "watch.example.com"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(m))
			if err == nil {
				t.Fatal("expected error")
			}
			for k := range m {
				if !strings.Contains(err.Error(), k) {
					t.Fatalf("error %q should name %s", err, k)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Config` / `undefined: Load`

- [ ] **Step 3: Implement**

`server/internal/config/config.go`:
```go
// Package config loads server settings from LISIMFLIX_* environment variables.
package config

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// Config is the process configuration. See the phase 1 index for the contract.
type Config struct {
	ConfigDir   string
	MediaDir    string
	CacheDir    string
	HTTPAddr    string
	PublicURL   string
	LogLevel    string
	FFprobePath string
	FFmpegPath  string
}

// Load reads configuration using getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		ConfigDir:   get("LISIMFLIX_CONFIG_DIR", "/config"),
		MediaDir:    get("LISIMFLIX_MEDIA_DIR", "/media"),
		CacheDir:    get("LISIMFLIX_CACHE_DIR", "/cache"),
		HTTPAddr:    get("LISIMFLIX_HTTP_ADDR", ":8420"),
		PublicURL:   strings.TrimRight(get("LISIMFLIX_PUBLIC_URL", ""), "/"),
		LogLevel:    strings.ToLower(get("LISIMFLIX_LOG_LEVEL", "info")),
		FFprobePath: get("LISIMFLIX_FFPROBE", "/usr/lib/jellyfin-ffmpeg/ffprobe"),
		FFmpegPath:  get("LISIMFLIX_FFMPEG", "/usr/lib/jellyfin-ffmpeg/ffmpeg"),
	}
	dirs := []struct{ key, val string }{
		{"LISIMFLIX_CONFIG_DIR", c.ConfigDir},
		{"LISIMFLIX_MEDIA_DIR", c.MediaDir},
		{"LISIMFLIX_CACHE_DIR", c.CacheDir},
	}
	for _, d := range dirs {
		if !filepath.IsAbs(d.val) {
			return Config{}, fmt.Errorf("%s must be an absolute path, got %q", d.key, d.val)
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("LISIMFLIX_LOG_LEVEL must be debug, info, warn or error, got %q", c.LogLevel)
	}
	if c.PublicURL != "" && !strings.HasPrefix(c.PublicURL, "https://") && !strings.HasPrefix(c.PublicURL, "http://") {
		return Config{}, fmt.Errorf("LISIMFLIX_PUBLIC_URL must start with https:// or http://, got %q", c.PublicURL)
	}
	return c, nil
}

// DBPath is the SQLite database file inside ConfigDir.
func (c Config) DBPath() string { return filepath.Join(c.ConfigDir, "lisimflix.db") }

// SlogLevel converts LogLevel for slog.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`
Expected: `ok  .../internal/config`

- [ ] **Step 5: Commit**

```bash
git add server/internal/config
git commit -m "feat(config): load settings from LISIMFLIX_* env vars"
```

---

### Task 3: SQLite database, migration runner, settings, test helper

**Files:**
- Create: `server/internal/db/db.go`, `server/internal/db/migrate.go`, `server/internal/db/dbtest/dbtest.go`, `server/internal/db/migrations/20261024000000_core_settings.sql`
- Test: `server/internal/db/db_test.go`, `server/internal/db/migrate_test.go`
- Modify: `server/go.mod`, `server/go.sum` (via `make tidy`)

**Interfaces:**
- Consumes: none
- Produces: `db.DB{*sql.DB}`, `db.Open(path string) (*DB, error)`, `(*DB) Migrate(ctx) error`, `(*DB) GetSetting(ctx, key) (string, bool, error)`, `(*DB) SetSetting(ctx, key, value) error`, `dbtest.Open(t testing.TB) *db.DB`. Table `server_settings(key TEXT PK, value TEXT)`.

- [ ] **Step 1: Write the failing migration-runner tests**

`server/internal/db/migrate_test.go`:
```go
package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func openRaw(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func tableExists(t *testing.T, d *DB, name string) bool {
	t.Helper()
	var n int
	err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrateAppliesInOrderAndIsIdempotent(t *testing.T) {
	d := openRaw(t)
	fsys := fstest.MapFS{
		"20260101000002_test_b.sql": {Data: []byte(`ALTER TABLE a ADD COLUMN extra TEXT;`)},
		"20260101000001_test_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER PRIMARY KEY); CREATE TABLE a2 (id INTEGER);`)},
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := d.migrateFS(ctx, fsys); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if !tableExists(t, d, "a") || !tableExists(t, d, "a2") {
		t.Fatal("multi-statement migration not applied")
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("schema_migrations rows = %d, want 2", n)
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	d := openRaw(t)
	fsys := fstest.MapFS{
		"20260101000001_test_bad.sql": {Data: []byte(`CREATE TABLE ok_t (id INTEGER); THIS IS NOT SQL;`)},
	}
	err := d.migrateFS(context.Background(), fsys)
	if err == nil || !strings.Contains(err.Error(), "20260101000001_test_bad.sql") {
		t.Fatalf("want error naming the file, got %v", err)
	}
	if tableExists(t, d, "ok_t") {
		t.Fatal("partial migration was not rolled back")
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != 0 {
		t.Fatal("failed migration was recorded as applied")
	}
}

func TestMigrateRejectsBadNames(t *testing.T) {
	d := openRaw(t)
	fsys := fstest.MapFS{"001_init.sql": {Data: []byte(`SELECT 1;`)}}
	err := d.migrateFS(context.Background(), fsys)
	if err == nil || !strings.Contains(err.Error(), "001_init.sql") {
		t.Fatalf("want name error, got %v", err)
	}
}

func TestEmbeddedMigrations(t *testing.T) {
	d := openRaw(t)
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, d, "server_settings") {
		t.Fatal("server_settings missing")
	}
}
```

`server/internal/db/db_test.go`:
```go
package db_test

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
)

func TestSettingsRoundTrip(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	if _, ok, err := d.GetSetting(ctx, "setup_complete"); err != nil || ok {
		t.Fatalf("missing key: ok=%v err=%v", ok, err)
	}
	if err := d.SetSetting(ctx, "setup_complete", "false"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSetting(ctx, "setup_complete", "true"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := d.GetSetting(ctx, "setup_complete")
	if err != nil || !ok || v != "true" {
		t.Fatalf("got %q ok=%v err=%v", v, ok, err)
	}
}

func TestPragmas(t *testing.T) {
	d := dbtest.Open(t)
	var fk int
	var mode string
	d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	d.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if fk != 1 || mode != "wal" {
		t.Fatalf("foreign_keys=%d journal_mode=%s", fk, mode)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Open` (and missing `dbtest` package)

- [ ] **Step 3: Implement `db.go`**

`server/internal/db/db.go`:
```go
// Package db opens the SQLite database and runs embedded migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB so modules share one connection pool.
type DB struct{ *sql.DB }

// Open opens (creating if needed) the SQLite file at path with WAL, foreign keys
// and a busy timeout applied to every connection.
func Open(path string) (*DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if err := sdb.Ping(); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	return &DB{sdb}, nil
}

// GetSetting returns a server setting. ok is false when the key is unset.
func (d *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := d.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %s: %w", key, err)
	}
	return v, true, nil
}

// SetSetting inserts or replaces a server setting.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO server_settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}
```

- [ ] **Step 4: Implement `migrate.go` and the first migration**

`server/internal/db/migrate.go`:
```go
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var migrationName = regexp.MustCompile(`^\d{14}_[a-z0-9]+_[a-z0-9_]+\.sql$`)

// Migrate applies every embedded migration that has not been applied yet.
func (d *DB) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	return d.migrateFS(ctx, sub)
}

func (d *DB) migrateFS(ctx context.Context, fsys fs.FS) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	) STRICT`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	for _, n := range names {
		if !migrationName.MatchString(n) {
			return fmt.Errorf("migration %s: name must match YYYYMMDDHHMMSS_<module>_<what>.sql", n)
		}
	}
	sort.Strings(names)

	applied := map[string]bool{}
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migration %s: begin: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
			version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: record: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: commit: %w", name, err)
		}
	}
	return nil
}
```

`server/internal/db/migrations/20261024000000_core_settings.sql`:
```sql
CREATE TABLE server_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
```

`server/internal/db/dbtest/dbtest.go`:
```go
// Package dbtest provides a migrated temporary database for tests.
package dbtest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db"
)

// Open returns a migrated DB in t.TempDir(), closed automatically.
func Open(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}
```

- [ ] **Step 5: Fetch the dependency and run tests**

Run: `make tidy && make test`
Expected: `go.mod` gains `modernc.org/sqlite`. All `internal/db` tests PASS.
If `TestMigrateFailureRollsBack` fails because the table exists, the driver ran
statements outside the transaction. Check that `tx.ExecContext` (not `d.ExecContext`) is used.

- [ ] **Step 6: Commit**

```bash
git add server/go.mod server/go.sum server/internal/db
git commit -m "feat(db): SQLite open, embedded migration runner, server settings"
```

---

### Task 4: JSON helpers (`httpx`)

**Files:**
- Create: `server/internal/httpx/json.go`
- Test: `server/internal/httpx/json_test.go`

**Interfaces:**
- Consumes: none
- Produces: `httpx.WriteJSON(w, status, v)`, `httpx.WriteError(w, status, code, message)`, `httpx.DecodeJSON(r, v) error`.

- [ ] **Step 1: Write the failing tests**

`server/internal/httpx/json_test.go`:
```go
package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, 201, map[string]int{"n": 1})
	if rec.Code != 201 {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"n":1}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, 404, "not_found", "no such item")
	want := `{"error":{"code":"not_found","message":"no such item"}}`
	if rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	ok := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"x"}`))
	var v in
	if err := DecodeJSON(ok, &v); err != nil || v.Name != "x" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	for name, body := range map[string]string{
		"unknown field": `{"name":"x","admin":true}`,
		"trailing data": `{"name":"x"} {"name":"y"}`,
		"too large":     `{"name":"` + strings.Repeat("a", 1<<20) + `"}`,
		"not json":      `name=x`,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			var v in
			if err := DecodeJSON(r, &v); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: WriteJSON`

- [ ] **Step 3: Implement**

`server/internal/httpx/json.go`:
```go
// Package httpx holds small HTTP helpers shared by all API modules.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const maxBodyBytes = 1 << 20

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes v as JSON with the given status. A nil v writes no body.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// DecodeJSON decodes one JSON object from the request body into v. It rejects
// bodies over 1 MiB, unknown fields and trailing data.
func DecodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if dec.More() {
		return errors.New("invalid JSON body: unexpected data after object")
	}
	return nil
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/httpx
git commit -m "feat(httpx): JSON response, error envelope and strict body decoding"
```

---

### Task 5: Principal context and guard interface (`authctx`)

**Files:**
- Create: `server/internal/authctx/authctx.go`
- Test: `server/internal/authctx/authctx_test.go`

**Interfaces:**
- Consumes: `httpx.WriteError`
- Produces: `authctx.Principal`, `WithPrincipal`, `FromContext`, `Guard` interface, `DenyAllGuard`, `StaticGuard` (exactly as in the index). Error codes: 401 `unauthenticated`, 403 `forbidden`.

- [ ] **Step 1: Write the failing tests**

`server/internal/authctx/authctx_test.go`:
```go
package authctx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler(t *testing.T, want Principal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok || p != want {
			t.Errorf("principal = %+v ok=%v, want %+v", p, ok, want)
		}
		w.WriteHeader(200)
	})
}

func status(h http.Handler) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	return rec.Code
}

func TestContextRoundTrip(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("empty context should have no principal")
	}
	p := Principal{UserID: 7, ProfileID: 3, IsAdmin: true}
	got, ok := FromContext(WithPrincipal(context.Background(), p))
	if !ok || got != p {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestDenyAllGuard(t *testing.T) {
	g := DenyAllGuard{}
	if c := status(g.RequireUser(okHandler(t, Principal{}))); c != 401 {
		t.Fatalf("RequireUser = %d", c)
	}
	if c := status(g.RequireAdmin(okHandler(t, Principal{}))); c != 401 {
		t.Fatalf("RequireAdmin = %d", c)
	}
}

func TestStaticGuard(t *testing.T) {
	user := Principal{UserID: 2, ProfileID: 5}
	g := StaticGuard{P: user}
	if c := status(g.RequireUser(okHandler(t, user))); c != 200 {
		t.Fatalf("user RequireUser = %d", c)
	}
	if c := status(g.RequireAdmin(okHandler(t, user))); c != 403 {
		t.Fatalf("user RequireAdmin = %d", c)
	}
	admin := Principal{UserID: 1, IsAdmin: true}
	if c := status(StaticGuard{P: admin}.RequireAdmin(okHandler(t, admin))); c != 200 {
		t.Fatalf("admin RequireAdmin = %d", c)
	}
}

var _ Guard = DenyAllGuard{}
var _ Guard = StaticGuard{}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Principal`

- [ ] **Step 3: Implement**

`server/internal/authctx/authctx.go`:
```go
// Package authctx carries the authenticated principal through request contexts
// and defines the Guard interface that the auth module implements.
package authctx

import (
	"context"
	"net/http"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// Principal identifies who is making a request.
type Principal struct {
	UserID    int64
	ProfileID int64 // 0 = no profile selected yet
	IsAdmin   bool
}

type ctxKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal, if any.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Guard protects routes. Implemented for real by the auth module (plan 1B).
type Guard interface {
	RequireUser(next http.Handler) http.Handler
	RequireAdmin(next http.Handler) http.Handler
}

// DenyAllGuard rejects every protected request. Used until auth is wired.
type DenyAllGuard struct{}

func (DenyAllGuard) RequireUser(http.Handler) http.Handler  { return http.HandlerFunc(unauthenticated) }
func (DenyAllGuard) RequireAdmin(http.Handler) http.Handler { return http.HandlerFunc(unauthenticated) }

// StaticGuard injects a fixed principal. For tests only.
type StaticGuard struct{ P Principal }

func (g StaticGuard) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), g.P)))
	})
}

func (g StaticGuard) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.P.IsAdmin {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), g.P)))
	})
}

func unauthenticated(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/authctx
git commit -m "feat(authctx): principal context and Guard interface with deny-all default"
```

---

### Task 6: Embedded web placeholder with SPA fallback

**Files:**
- Create: `server/internal/web/web.go`, `server/internal/web/dist/index.html`, `server/internal/web/dist/robots.txt`
- Test: `server/internal/web/web_test.go`

**Interfaces:**
- Consumes: none
- Produces: `web.Handler() http.Handler`: serves `dist/` files, and `index.html` for `/`, unknown paths and directories (SPA routing). Phase 3 replaces `dist/` with the Vite build output.

- [ ] **Step 1: Write the failing tests**

`server/internal/web/web_test.go`:
```go
package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestIndexAndFallback(t *testing.T) {
	for _, p := range []string{"/", "/index.html", "/library/12", "/assets/"} {
		rec := get(p)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Lisimflix") {
			t.Fatalf("%s: code=%d body=%q", p, rec.Code, rec.Body.String())
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: content-type %q", p, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: index must not be cached", p)
		}
	}
}

func TestStaticFile(t *testing.T) {
	rec := get("/robots.txt")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Disallow: /") {
		t.Fatalf("robots: %d %q", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: Handler`

- [ ] **Step 3: Implement**

`server/internal/web/dist/index.html`:
```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lisimflix</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         background: #0b0d12; color: #e8eaf0; font-family: system-ui, sans-serif; }
  main { text-align: center; padding: 16px; }
  p { color: #9aa0ae; }
</style>
</head>
<body>
<main>
  <h1>Lisimflix</h1>
  <p>The server is running. The web app arrives in phase 3.</p>
</main>
</body>
</html>
```

`server/internal/web/dist/robots.txt`:
```
User-agent: *
Disallow: /
```

`server/internal/web/web.go`:
```go
// Package web serves the embedded web app with single-page-app fallback.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var dist embed.FS

// Handler serves static files from dist/, and index.html for any path that
// is not a file so client-side routes work on reload.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // embedded at build time; cannot fail at runtime
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(sub, p); err == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/web
git commit -m "feat(web): embedded placeholder page with SPA fallback"
```

---

### Task 7: App router, health check, middleware, module mounting

**Files:**
- Create: `server/internal/app/app.go`
- Test: `server/internal/app/app_test.go`

**Interfaces:**
- Consumes: `config.Config`, `db.DB`, `dbtest.Open`, `authctx.Guard`, `authctx.DenyAllGuard`, `authctx.StaticGuard`, `httpx.WriteJSON/WriteError`, `web.Handler`, `version.Version`
- Produces: `app.Module` interface, `app.New(cfg, d, log, g, mods...) *App`, `(*App) Handler() http.Handler`.
  `GET /api/v1/health` → `200 {"status":"ok","version":"...","db":"ok"}` or `503` with `"status":"degraded","db":"error"`.
  Unknown `/api/v1/*` → `404 not_found` JSON. Every response carries `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`.
  **Module convention:** each module mounts under its own prefix (`r.Route("/libraries", ...)`).

- [ ] **Step 1: Write the failing tests**

`server/internal/app/app_test.go`:
```go
package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

type pingModule struct{}

func (pingModule) Name() string { return "ping" }
func (pingModule) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/ping", func(r chi.Router) {
		r.With(g.RequireUser).Get("/", func(w http.ResponseWriter, r *http.Request) {
			p, _ := authctx.FromContext(r.Context())
			httpx.WriteJSON(w, 200, map[string]int64{"user": p.UserID})
		})
		r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
}

func newApp(t *testing.T, g authctx.Guard) (*App, http.Handler) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(config.Config{}, dbtest.Open(t), log, g, pingModule{})
	return a, a.Handler()
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthOK(t *testing.T) {
	_, h := newApp(t, authctx.DenyAllGuard{})
	rec := do(h, "GET", "/api/v1/health")
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body["status"] != "ok" || body["db"] != "ok" || body["version"] == "" {
		t.Fatalf("%d %v", rec.Code, body)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("X-Frame-Options") != "DENY" ||
		rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("security headers missing: %v", rec.Header())
	}
}

func TestHealthDegradedWhenDBClosed(t *testing.T) {
	a, h := newApp(t, authctx.DenyAllGuard{})
	a.db.Close()
	rec := do(h, "GET", "/api/v1/health")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"degraded"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestModuleMountedWithGuard(t *testing.T) {
	_, deny := newApp(t, authctx.DenyAllGuard{})
	if c := do(deny, "GET", "/api/v1/ping/").Code; c != 401 {
		t.Fatalf("deny guard: %d", c)
	}
	_, allow := newApp(t, authctx.StaticGuard{P: authctx.Principal{UserID: 9}})
	rec := do(allow, "GET", "/api/v1/ping/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"user":9`) {
		t.Fatalf("static guard: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	_, h := newApp(t, authctx.DenyAllGuard{})
	rec := do(h, "GET", "/api/v1/nope")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestNonAPIServesWebApp(t *testing.T) {
	_, h := newApp(t, authctx.DenyAllGuard{})
	rec := do(h, "GET", "/movies/42")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Lisimflix") {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPanicRecovered(t *testing.T) {
	_, h := newApp(t, authctx.DenyAllGuard{})
	if c := do(h, "GET", "/api/v1/ping/panic").Code; c != 500 {
		t.Fatalf("panic: %d", c)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: New`

- [ ] **Step 3: Implement**

`server/internal/app/app.go`:
```go
// Package app builds the HTTP handler: middleware, /api/v1 modules, web app.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
	"github.com/Lisimflix/lisimflix/server/internal/version"
	"github.com/Lisimflix/lisimflix/server/internal/web"
)

// Module is a feature area that mounts its routes under /api/v1.
// Each module mounts under its own prefix, e.g. r.Route("/libraries", ...).
type Module interface {
	Name() string
	Mount(r chi.Router, g authctx.Guard)
}

// App holds shared dependencies for the HTTP layer.
type App struct {
	cfg   config.Config
	db    *db.DB
	log   *slog.Logger
	guard authctx.Guard
	mods  []Module
}

// New creates the app. g protects module routes.
func New(cfg config.Config, d *db.DB, log *slog.Logger, g authctx.Guard, mods ...Module) *App {
	return &App{cfg: cfg, db: d, log: log, guard: g, mods: mods}
}

// Handler returns the root HTTP handler.
func (a *App) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, a.logRequests, middleware.Recoverer, securityHeaders)
	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/health", a.health)
		for _, m := range a.mods {
			m.Mount(api, a.guard)
		}
		api.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "no such API route")
		})
	})
	r.Handle("/*", web.Handler())
	return r
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status, dbStatus, code := "ok", "ok", http.StatusOK
	if err := a.db.PingContext(ctx); err != nil {
		status, dbStatus, code = "degraded", "error", http.StatusServiceUnavailable
		a.log.Warn("health: database ping failed", "err", err)
	}
	httpx.WriteJSON(w, code, map[string]string{
		"status": status, "version": version.Version, "db": dbStatus,
	})
}

func (a *App) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		lvl := slog.LevelInfo
		if r.URL.Path == "/api/v1/health" {
			lvl = slog.LevelDebug
		}
		// Path only: query strings may carry tokens.
		a.log.Log(r.Context(), lvl, "http",
			"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"bytes", ww.BytesWritten(), "dur_ms", time.Since(start).Milliseconds(),
			"req_id", middleware.GetReqID(r.Context()))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Fetch chi and run tests**

Run: `make tidy && make test`
Expected: all PASS. (`go.mod` gains `github.com/go-chi/chi/v5`.)

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/app
git commit -m "feat(app): API router with health check, modules, middleware and web fallback"
```

---

### Task 8: `main`: startup, graceful shutdown, healthcheck subcommand

**Files:**
- Create: `server/cmd/lisimflix/main.go`
- Test: `server/cmd/lisimflix/main_test.go`

**Interfaces:**
- Consumes: `config.Load`, `db.Open`, `(*DB) Migrate`, `app.New`, `authctx.DenyAllGuard`, `version.String`
- Produces: binary `lisimflix` with subcommands `version` and `healthcheck` (exit 0 when `/api/v1/health` returns 200). `run(ctx, args, getenv, stdout, stderr) int` is the testable entry. Plan 1B replaces `authctx.DenyAllGuard{}` here with `authSvc.Guard()`, and later plans append their modules to `app.New`.

- [ ] **Step 1: Write the failing tests**

`server/cmd/lisimflix/main_test.go`:
```go
package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestVersionSubcommand(t *testing.T) {
	var out bytes.Buffer
	code := run(context.Background(), []string{"version"}, func(string) string { return "" }, &out, &out)
	if code != 0 || !strings.HasPrefix(out.String(), "lisimflix ") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestBadConfigExits2(t *testing.T) {
	var out bytes.Buffer
	getenv := func(k string) string {
		if k == "LISIMFLIX_LOG_LEVEL" {
			return "loud"
		}
		return ""
	}
	if code := run(context.Background(), nil, getenv, &out, &out); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestServeHealthcheckAndShutdown(t *testing.T) {
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

	deadline := time.Now().Add(10 * time.Second)
	for {
		var out bytes.Buffer
		if run(context.Background(), []string{"healthcheck"}, getenv, &out, &out) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy; logs:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d; logs:\n%s", code, logs.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("server did not shut down")
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: run`

- [ ] **Step 3: Implement**

`server/cmd/lisimflix/main.go`:
```go
// Command lisimflix is the Lisimflix media server.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/app"
	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main without process globals, so tests can drive it. It serves until
// ctx is cancelled. Exit codes: 0 ok, 1 runtime error, 2 bad configuration.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version":
			fmt.Fprintln(stdout, version.String())
			return 0
		case "healthcheck":
			return healthcheck(getenv, stderr)
		default:
			fmt.Fprintf(stderr, "unknown command %q (commands: version, healthcheck)\n", args[0])
			return 2
		}
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))

	for _, dir := range []string{cfg.ConfigDir, cfg.CacheDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			log.Error("create directory", "dir", dir, "err", err)
			return 1
		}
	}
	if _, err := os.Stat(cfg.MediaDir); err != nil {
		log.Warn("media directory not available", "dir", cfg.MediaDir, "err", err)
	}

	d, err := db.Open(cfg.DBPath())
	if err != nil {
		log.Error("open database", "err", err)
		return 1
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		log.Error("migrate database", "err", err)
		return 1
	}

	a := app.New(cfg, d, log, authctx.DenyAllGuard{})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: video streams are long-lived responses.
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("lisimflix started", "addr", cfg.HTTPAddr, "version", version.Version)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			return 1
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
		return 1
	}
	log.Info("lisimflix stopped")
	return 0
}

// healthcheck calls the local health endpoint. Used by Docker HEALTHCHECK.
func healthcheck(getenv func(string) string, stderr io.Writer) int {
	addr := getenv("LISIMFLIX_HTTP_ADDR")
	if addr == "" {
		addr = ":8420"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/api/v1/health")
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`
Expected: all packages PASS, including `cmd/lisimflix`.

- [ ] **Step 5: Commit**

```bash
git add server/cmd
git commit -m "feat: server entrypoint with graceful shutdown and healthcheck command"
```

---

### Task 9: Docker image, entrypoint, compose and Unraid template

**Files:**
- Create: `Dockerfile`, `deploy/entrypoint.sh`, `deploy/docker-compose.yml`, `deploy/unraid/lisimflix.xml`

**Interfaces:**
- Consumes: the `lisimflix` binary (`version`, `healthcheck` subcommands)
- Produces: image with `/app/lisimflix`, `/usr/lib/jellyfin-ffmpeg/{ffmpeg,ffprobe}`, env defaults, `HEALTHCHECK`, running as `PUID:PGID`.

- [ ] **Step 1: Write `Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.27-trixie AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/Lisimflix/lisimflix/server/internal/version.Version=${VERSION}" \
    -o /out/lisimflix ./cmd/lisimflix

FROM debian:trixie-slim AS ffmpeg
ARG FFMPEG_VERSION=8.1.3-1
ARG FFMPEG_SHA256=b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
 && curl -fsSL -o /tmp/ff.tar.xz \
    "https://github.com/jellyfin/jellyfin-ffmpeg/releases/download/v${FFMPEG_VERSION}/jellyfin-ffmpeg_${FFMPEG_VERSION}_portable_linux64-gpl.tar.xz" \
 && echo "${FFMPEG_SHA256}  /tmp/ff.tar.xz" | sha256sum -c - \
 && mkdir -p /opt/ffmpeg && tar -xJf /tmp/ff.tar.xz -C /opt/ffmpeg

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg /opt/ffmpeg/ /usr/lib/jellyfin-ffmpeg/
COPY --from=build /out/lisimflix /app/lisimflix
COPY deploy/entrypoint.sh /entrypoint.sh
ENV LISIMFLIX_CONFIG_DIR=/config \
    LISIMFLIX_MEDIA_DIR=/media \
    LISIMFLIX_CACHE_DIR=/cache \
    LISIMFLIX_HTTP_ADDR=:8420 \
    PUID=99 PGID=100 \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
EXPOSE 8420
VOLUME ["/config", "/cache"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["/app/lisimflix", "healthcheck"]
ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 2: Write `deploy/entrypoint.sh`** and make it executable (`chmod +x`)

```sh
#!/bin/sh
# Create writable dirs, then drop root and run as PUID:PGID.
set -eu
PUID="${PUID:-99}"
PGID="${PGID:-100}"
if [ "$(id -u)" = "0" ]; then
    for d in "$LISIMFLIX_CONFIG_DIR" "$LISIMFLIX_CACHE_DIR"; do
        mkdir -p "$d"
        chown "$PUID:$PGID" "$d"
    done
    exec setpriv --reuid="$PUID" --regid="$PGID" --clear-groups --inh-caps=-all /app/lisimflix "$@"
fi
exec /app/lisimflix "$@"
```

- [ ] **Step 3: Write `deploy/docker-compose.yml`**

```yaml
services:
  lisimflix:
    image: ghcr.io/lisimflix/lisimflix:edge
    container_name: lisimflix
    restart: unless-stopped
    ports:
      - "8420:8420"
    environment:
      - PUID=99
      - PGID=100
      - TZ=Australia/Brisbane
      # - LISIMFLIX_PUBLIC_URL=https://watch.example.com
      # NVIDIA GPU: also uncomment `runtime` below
      # - NVIDIA_VISIBLE_DEVICES=all
    # runtime: nvidia
    volumes:
      - ./config:/config
      - /path/to/media:/media:ro
      - ./cache:/cache
```

- [ ] **Step 4: Write `deploy/unraid/lisimflix.xml`**

```xml
<?xml version="1.0"?>
<Container version="2">
  <Name>Lisimflix</Name>
  <Repository>ghcr.io/lisimflix/lisimflix:edge</Repository>
  <Registry>https://github.com/Lisimflix/lisimflix/pkgs/container/lisimflix</Registry>
  <Network>bridge</Network>
  <Shell>sh</Shell>
  <Privileged>false</Privileged>
  <Overview>Self-hosted media server with anime metadata, profiles and adaptive GPU streaming.</Overview>
  <Category>MediaServer:Video</Category>
  <WebUI>http://[IP]:[PORT:8420]/</WebUI>
  <ExtraParams>--runtime=nvidia</ExtraParams>
  <Config Name="Web UI" Target="8420" Default="8420" Mode="tcp" Type="Port" Display="always" Required="true">8420</Config>
  <Config Name="Config" Target="/config" Default="/mnt/user/appdata/lisimflix" Mode="rw" Type="Path" Display="always" Required="true">/mnt/user/appdata/lisimflix</Config>
  <Config Name="Media (read-only)" Target="/media" Default="/mnt/user/dataz/media" Mode="ro" Type="Path" Display="always" Required="true">/mnt/user/dataz/media</Config>
  <Config Name="Transcode cache" Target="/cache" Default="/mnt/cache/appdata/lisimflix-cache" Mode="rw" Type="Path" Display="always" Required="true">/mnt/cache/appdata/lisimflix-cache</Config>
  <Config Name="PUID" Target="PUID" Default="99" Type="Variable" Display="advanced" Required="true">99</Config>
  <Config Name="PGID" Target="PGID" Default="100" Type="Variable" Display="advanced" Required="true">100</Config>
  <Config Name="NVIDIA_VISIBLE_DEVICES" Target="NVIDIA_VISIBLE_DEVICES" Default="all" Type="Variable" Display="advanced" Required="false">all</Config>
  <Config Name="Public URL" Target="LISIMFLIX_PUBLIC_URL" Default="" Type="Variable" Display="always" Required="false"></Config>
</Container>
```

- [ ] **Step 5: Build and verify the image on zraid**

Run:
```bash
make image
T=$(mktemp -d)
docker run -d --name lisimflix-verify -p 8421:8420 \
  -v $T/config:/config -v $T/cache:/cache -v $T:/media:ro lisimflix:dev
sleep 3
curl -s http://127.0.0.1:8421/api/v1/health
docker exec lisimflix-verify /app/lisimflix healthcheck && echo HEALTHY
docker exec lisimflix-verify /usr/lib/jellyfin-ffmpeg/ffprobe -version | head -1
docker exec lisimflix-verify sh -c 'ps -o user= -p 1 2>/dev/null || stat -c %u /proc/1'
ls -ln $T/config
docker rm -f lisimflix-verify
```
Expected:
- health JSON `{"db":"ok","status":"ok","version":"<git describe>"}`
- `HEALTHY`
- `ffprobe version 8.1.3-Jellyfin ...`
- PID 1 runs as UID 99
- `lisimflix.db` in `$T/config` owned by `99 100`

- [ ] **Step 6: Verify the GPU is visible (zraid only)**

Run:
```bash
docker run --rm --runtime=nvidia -e NVIDIA_VISIBLE_DEVICES=all --entrypoint /usr/lib/jellyfin-ffmpeg/ffmpeg \
  lisimflix:dev -hide_banner -hwaccels
```
Expected: the list includes `cuda`.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile deploy
git commit -m "build: Docker image with jellyfin-ffmpeg, non-root entrypoint, compose and Unraid template"
```

---

### Task 10: CI: test on every PR, publish image from `main` and tags

**Prerequisite:** the pushing account's GitHub token has the `workflow` scope
(`gh auth refresh -s project,workflow`). Without it, pushing this file is rejected.

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `server/` tests, `Dockerfile`
- Produces: `ghcr.io/lisimflix/lisimflix:edge` on every push to `main`, `:X.Y.Z` + `:latest` on `vX.Y.Z` tags, `:sha-<short>` always.

- [ ] **Step 1: Write the workflow**

```yaml
name: ci

on:
  pull_request:
  push:
    branches: [main]
    tags: ["v*"]

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-24.04
    defaults:
      run:
        working-directory: server
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version: "1.27.x"
          cache-dependency-path: server/go.sum
      - name: gofmt
        run: |
          unformatted=$(gofmt -l .)
          if [ -n "$unformatted" ]; then echo "$unformatted"; exit 1; fi
      - name: vet
        run: go vet ./...
      - name: test
        run: go test -race ./...

  image:
    needs: test
    if: github.event_name == 'push'
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v5
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/lisimflix/lisimflix
          tags: |
            type=edge,branch=main
            type=semver,pattern={{version}}
            type=sha
      - uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: VERSION=${{ github.ref_name }}-${{ github.sha }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 2: Validate locally**

Run: `docker run --rm -v "$PWD":/repo -w /repo rhysd/actionlint:1.7.7 -color`
Expected: no output (no errors).

- [ ] **Step 3: Commit, push the `feat/1a-core` branch, open the PR, and confirm the `test` job passes**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: test on PRs, publish image to GHCR from main and tags"
git push -u origin feat/1a-core
gh pr create --base main --title "feat(core): server skeleton, Docker image and CI" \
  --body "$(printf '## What changed\nPlan 1A: core skeleton (docs/superpowers/plans/2026-10-24-phase1a-core.md)\n\n## How it was tested\nmake test/vet; image verified on zraid (Task 9)\n\nCloses #')"
gh pr checks --watch
```
Expected: `test` passes and `image` is skipped (PR event). Another member reviews and
squash-merges it. Don't merge it yourself.

- [ ] **Step 4: After merge, confirm the image is published**

Run: `gh run list --branch main --limit 1` then
`docker pull ghcr.io/lisimflix/lisimflix:edge` (after `echo $TOKEN | docker login ghcr.io -u <user> --password-stdin`
with a token that has `read:packages`)
Expected: pull succeeds. The package page is private to org members.

---

## Done when

- `make test` and `make vet` pass. CI is green on `main`.
- `ghcr.io/lisimflix/lisimflix:edge` exists, starts on zraid, reports healthy, runs as 99:100,
  and its ffmpeg lists `cuda` with `--runtime=nvidia`.
- Plans 1B and 1C can start: the contracts in the index compile as written for 1A's packages.
