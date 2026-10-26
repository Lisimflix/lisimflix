# OPS-2: Durable Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Draft for review, revision 4 (addresses @lisim's reviews on #14). Claimed by @Lammy93 in
#11; reviewer @lisim. Not implemented. Every code block below was compiled and passed its tests
(`go vet`, `go test -race`, `CGO_ENABLED=0` build) in a scratch module on Go 1.27.3 before this
plan was written. That is evidence for the plan, not module acceptance.
**Goal:** Give every module one SQLite database that opens safely, migrates durably, stores settings,
and can be snapshotted and restored consistently.
**Architecture:** One package, `server/internal/storage`, wraps `database/sql` with the pure-Go
`modernc.org/sqlite` driver. Each module registers its own embedded migrations as a `Source`. The
runner merges them by timestamp, applies the whole pending batch in one transaction and records a
checksum for each.
Snapshots use `VACUUM INTO` plus a checksummed manifest, and restore validates before an atomic
file swap. No HTTP, CLI or scheduler wiring is included: integration owners call these functions.
**Tech Stack:** Go 1.27.3, `modernc.org/sqlite v1.60.1`, stdlib only otherwise.
**Spec:** [Design §§2.1, 10.2–10.3, 11.3–11.4](../specs/2026-10-24-lisimflix-design.md);
acceptance BASE-03, OPS-01 (G1 storage part), PERF-07 inputs.

## Global constraints

- Feature **OPS-2** (P0 / G1). Initial target **Linux x86-64/amd64 containers**. The database lives
  under `/config` on **local disk, never SMB/NFS** (spec §10.2). WAL permits one writer at a time.
- `CGO_ENABLED=0` builds. The driver version is pinned to **v1.60.1** (released 2026-09-29; v1.61.0
  was less than a week old when this was written).
- Source media is never touched. Snapshots exclude media and `/cache` (spec §11.3).
- Migrations: one `Source` per module, file names `YYYYMMDDHHMMSS_<module>_<what>.sql` (UTC), the
  module prefix must match its `Source`, and timestamps are unique across all modules. A merged
  migration is never edited: the runner records a SHA-256 and refuses an edited file.
- All pending migrations run as **one batch in one transaction**: either every one applies or none
  does, so a failed upgrade always leaves the previous schema intact and restorable by the previous
  version. The transaction has `foreign_keys` on, so `PRAGMA foreign_keys=OFF` inside a
  migration does nothing. For a table rebuild (SQLite's 12-step procedure), start with
  `PRAGMA defer_foreign_keys=ON;` and end with `PRAGMA foreign_key_check;`.
- Startup refuses a database with migrations this binary doesn't know (`ErrSchemaTooNew`); recovery
  is restoring a compatible snapshot with a compatible image (spec §11.4). No binary downgrades.
- The live database (and so its `-wal`/`-shm`), snapshot directories and snapshot files are mode
  0600/0700. They can contain secrets (settings, `/config/m1.json`): never log their contents.
- Network filesystems are refused: NFS, SMB/CIFS, 9p, Ceph. FUSE is allowed, because it also
  covers local tools such as Unraid's `/mnt/user`.
- The nightly hour is in the container's time zone (`TZ`, UTC if unset).
- A pre-upgrade snapshot is reused only if it has the same schema **and** is under 1 hour old
  (`ReuseWindow`). This collapses a crash loop to one snapshot, while a later upgrade always gets a
  fresh one.
- Nightly snapshots keep the last **7**. Failed or partial snapshots never displace a usable one.
  Backups are stale after **26 hours** without success (spec §11.3).
- Standalone check: `go -C server test ./internal/storage/...`. No server, container, auth or media
  needed. Without a local Go toolchain:
  `docker run --rm -v "$PWD/server":/src -w /src golang:1.27.3-trixie go test -race ./internal/storage/...`.
- Before the PR: `make check` locally on the finished branch, per CONTRIBUTING and CLAUDE.md.

## Dependencies

| Kind | Dependency | Why |
|---|---|---|
| Start | CTR-1 (#8) merged | Gives `server/go.mod` and `make check`; storage needs nothing else from it |
| Shared-file edit | `server/go.mod`/`go.sum` + `modernc.org/sqlite v1.60.1` | After CTR-1; whichever of OPS-1/OPS-2 lands first (agreed in #14). If this is the first dependency, the same PR switches `cache-dependency-path` in `.github/workflows/check.yml` to `server/go.sum` |
| Integration | OPS-1 runtime | Calls `Open` + `MigrateWithBackup` at startup and reports readiness; restore runs while serving is stopped |
| Integration | ACC-1, LIB-2, META-1, DATA-1 | First modules to register a `Source` |
| Integration | UI-6 / OPS-5 | Admin backup/restore actions and backup freshness on screen |

## Review focus

1. A failed or interrupted upgrade leaves no partial schema: the whole batch rolls back and nothing is recorded. Readiness stays blocked.
2. A snapshot is consistent while the app is writing, passes `integrity_check`, and appears atomically.
3. Restore never changes the current installation unless every check passes, and always keeps a pre-restore snapshot.
4. Manifest file names cannot escape the snapshot or config directory.
5. The API is small enough that modules don't need a second database layer.

## Ownership and interfaces

Owns `server/internal/storage/**` (including `storage/migrations/` for storage's own tables). Modules
keep their migrations in their own package, e.g. `server/internal/library/migrations/`, and pass
them as a `Source`. Migration ordering stays with the OPS-2 owner while OPS-2 is open.

```go
// open.go
type DB struct{ *sql.DB }
var ErrNetworkFilesystem error
func Open(ctx context.Context, path string) (*DB, error)
func (d *DB) Path() string

// migrate.go
type Source struct{ Module string; FS fs.FS }
var ErrSchemaTooNew, ErrMigrationEdited error
func Core() Source                                     // storage's own migrations
func (d *DB) Applied(ctx context.Context) ([]string, error)
func (d *DB) Pending(ctx context.Context, srcs []Source) ([]string, error)
func (d *DB) Migrate(ctx context.Context, srcs []Source, now func() time.Time) ([]string, error)

// settings.go
func (d *DB) GetSetting(ctx context.Context, key string) (string, bool, error)
func (d *DB) SetSetting(ctx context.Context, key, value string, now time.Time) error

// snapshot.go
type Manifest struct{ Format int; CreatedAt time.Time; Reason, AppVersion string; Schema []string; Files []ManifestFile }
type ManifestFile struct{ Name string; Size int64; SHA256 string }
type SnapshotOptions struct{ Dir, Reason, AppVersion string; ExtraFiles []string; Now time.Time }
var Keep map[string]int
const StaleAfter = 26 * time.Hour
func (d *DB) Snapshot(ctx context.Context, o SnapshotOptions) (string, Manifest, error)
func Prune(dir string) ([]string, error)
func Latest(dir string) (Manifest, bool, error)
func Stale(latest Manifest, found bool, now time.Time) bool

// restore.go
type RestoreOptions struct{ SnapshotDir, DBPath, ConfigDir, BackupsDir string; ExtraFiles []string; Known []Source; AppVersion string; Now time.Time }
var ErrInvalidSnapshot error
func Restore(ctx context.Context, o RestoreOptions) (preRestore string, err error)

// backup.go
const ReuseWindow = time.Hour // a same-schema pre-upgrade snapshot younger than this is reused
func (d *DB) MigrateWithBackup(ctx context.Context, srcs []Source, o SnapshotOptions, now func() time.Time) ([]string, error)
func NextRun(now time.Time, hour int) time.Time
type Nightly struct{ DB *DB; Opts SnapshotOptions; Hour int; Now func() time.Time; After func(time.Duration) <-chan time.Time; Log *slog.Logger } // zero fields default: 3, time.Now, time.After, slog.Default()
func (n *Nightly) RunOnce(ctx context.Context) error
func (n *Nightly) Run(ctx context.Context)
```

---

### Task 1: Open the database and migrate durably (BASE-03)

**Files:**
- Modify (coordinated shared-file edit): `server/go.mod`, `server/go.sum`; `.github/workflows/check.yml`
  only if this is the first PR to add a dependency
- Create: `server/internal/storage/open.go`, `fs_linux.go`, `fs_other.go`, `migrate.go`, `settings.go`,
  `migrations/20261026000000_storage_settings.sql`
- Test: `server/internal/storage/migrate_test.go`

**Interfaces:**
- Consumes: nothing from other modules.
- Produces: `Open`, `DB`, `Source`, `Core`, `Applied`, `Pending`, `Migrate`, `GetSetting`, `SetSetting`,
  `ErrNetworkFilesystem`, `ErrSchemaTooNew`, `ErrMigrationEdited`.

- [ ] **Step 1: Branch and add the driver** (after CTR-1 merges and the `go.mod` edit is agreed with OPS-1)

```bash
git switch main && git pull && git switch -c feat/ops-2-storage
go -C server get modernc.org/sqlite@v1.60.1
```
If `server/go.sum` didn't exist before this step, this is the first dependency: in
`.github/workflows/check.yml`, set `cache-dependency-path: server/go.sum` and delete the
`shortcut:` comment above it.

- [ ] **Step 2: Write the failing tests**

`server/internal/storage/migrate_test.go`:
```go
package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC) }

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lisimflix.db")
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}

func tableExists(t *testing.T, d *DB, name string) bool {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func src(module string, files map[string]string) Source {
	m := fstest.MapFS{}
	for n, body := range files {
		m[n] = &fstest.MapFile{Data: []byte(body)}
	}
	return Source{Module: module, FS: m}
}

func TestOpenUsesWALAndForeignKeys(t *testing.T) {
	d, _ := openTemp(t)
	var mode string
	var fk int
	d.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if mode != "wal" || fk != 1 {
		t.Fatalf("journal_mode=%s foreign_keys=%d", mode, fk)
	}
}

// BASE-03: empty config creates the DB; a restart keeps data and does not
// reapply completed migrations.
func TestMigrateSurvivesRestartWithoutReapplying(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lisimflix.db")
	srcs := []Source{Core(), src("library", map[string]string{
		"20261027000000_library_roots.sql": `CREATE TABLE roots (id TEXT PRIMARY KEY) STRICT;`,
	})}

	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done, err := d.Migrate(ctx, srcs, fixedNow)
	if err != nil || len(done) != 2 {
		t.Fatalf("first migrate: %v %v", done, err)
	}
	if err := d.SetSetting(ctx, "server.name", "test server", fixedNow()); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	done, err = d2.Migrate(ctx, srcs, fixedNow)
	if err != nil || len(done) != 0 {
		t.Fatalf("second migrate reapplied: %v %v", done, err)
	}
	v, ok, err := d2.GetSetting(ctx, "server.name")
	if err != nil || !ok || v != "test server" {
		t.Fatalf("setting after restart: %q %v %v", v, ok, err)
	}
}

func TestMigrateOrdersAcrossModulesByTimestamp(t *testing.T) {
	d, _ := openTemp(t)
	srcs := []Source{
		src("userdata", map[string]string{"20261027000002_userdata_progress.sql": `CREATE TABLE progress (root TEXT REFERENCES roots(id)) STRICT;`}),
		src("library", map[string]string{"20261027000001_library_roots.sql": `CREATE TABLE roots (id TEXT PRIMARY KEY) STRICT;`}),
	}
	done, err := d.Migrate(context.Background(), srcs, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(done, ",") != "20261027000001_library_roots,20261027000002_userdata_progress" {
		t.Fatalf("order %v", done)
	}
}

func TestMigrationFailureRollsBackAndStops(t *testing.T) {
	d, _ := openTemp(t)
	srcs := []Source{src("library", map[string]string{
		"20261027000001_library_bad.sql":   `CREATE TABLE half (id TEXT) STRICT; NOT SQL;`,
		"20261027000002_library_later.sql": `CREATE TABLE later (id TEXT) STRICT;`,
	})}
	done, err := d.Migrate(context.Background(), srcs, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "20261027000001_library_bad") || len(done) != 0 {
		t.Fatalf("want failure naming the file, got %v %v", done, err)
	}
	if tableExists(t, d, "half") || tableExists(t, d, "later") {
		t.Fatal("failed migration left changes or later migrations ran")
	}
}

func TestMigrationNamesAreValidated(t *testing.T) {
	d, _ := openTemp(t)
	cases := map[string][]Source{
		"bad name":       {src("library", map[string]string{"001_init.sql": `SELECT 1;`})},
		"wrong module":   {src("library", map[string]string{"20261027000001_auth_users.sql": `SELECT 1;`})},
		"same timestamp": {src("library", map[string]string{"20261027000001_library_a.sql": `SELECT 1;`}), src("auth", map[string]string{"20261027000001_auth_b.sql": `SELECT 1;`})},
	}
	for name, srcs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := d.Migrate(context.Background(), srcs, fixedNow); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestUnknownAppliedMigrationIsTooNew(t *testing.T) {
	d, _ := openTemp(t)
	ctx := context.Background()
	newer := []Source{Core(), src("library", map[string]string{"20261101000000_library_new.sql": `CREATE TABLE n (id TEXT) STRICT;`})}
	if _, err := d.Migrate(ctx, newer, fixedNow); err != nil {
		t.Fatal(err)
	}
	_, err := d.Migrate(ctx, []Source{Core()}, fixedNow)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("want ErrSchemaTooNew, got %v", err)
	}
}

func TestEditedAppliedMigrationIsRejected(t *testing.T) {
	d, _ := openTemp(t)
	ctx := context.Background()
	name := "20261027000001_library_roots.sql"
	if _, err := d.Migrate(ctx, []Source{src("library", map[string]string{name: `CREATE TABLE roots (id TEXT) STRICT;`})}, fixedNow); err != nil {
		t.Fatal(err)
	}
	_, err := d.Pending(ctx, []Source{src("library", map[string]string{name: `CREATE TABLE roots (id TEXT, x TEXT) STRICT;`})})
	if !errors.Is(err, ErrMigrationEdited) {
		t.Fatalf("want ErrMigrationEdited, got %v", err)
	}
}
```

- [ ] **Step 3: Run them and confirm they fail**

Run: `go -C server test ./internal/storage/...`
Expected: FAIL, `undefined: Open` (and the other missing names).

- [ ] **Step 4: Implement**

`server/internal/storage/open.go`:
```go
// Package storage owns Lisimflix's SQLite database: opening it safely on local
// disk, applying module migrations, durable settings, and consistent
// snapshot/restore (spec §§10.2, 11.3–11.4; BASE-03, OPS-01).
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite"
)

// DB is the shared connection pool. Modules own their SQL through it.
type DB struct {
	*sql.DB
	path string
}

// ErrNetworkFilesystem means the database directory is on NFS/SMB/CIFS, where
// SQLite WAL is unsafe (spec §10.2).
var ErrNetworkFilesystem = errors.New("storage: database must be on local disk, not a network filesystem")

// Open opens or creates the database at path with WAL, foreign keys, a busy
// timeout and IMMEDIATE write transactions on every connection.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := checkLocalFilesystem(path); err != nil {
		return nil, err
	}
	// Create the file 0600 before SQLite does: it holds settings and secrets,
	// and SQLite gives -wal/-shm files the database file's mode.
	if err := ensurePrivateFile(path); err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	sdb, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	var mode string
	if err := sdb.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	if mode != "wal" {
		sdb.Close()
		return nil, fmt.Errorf("storage: open: journal_mode is %q, want wal", mode)
	}
	return &DB{DB: sdb, path: path}, nil
}

// ensurePrivateFile creates path with mode 0600, or tightens an existing file.
func ensurePrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	return os.Chmod(path, 0o600)
}

// Path is the database file path.
func (d *DB) Path() string { return d.path }
```

`server/internal/storage/fs_linux.go`:
```go
//go:build linux

package storage

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// Filesystem magic numbers from statfs(2) that must not hold a WAL database.
// FUSE is allowed: it also covers local tools such as Unraid's /mnt/user.
var networkFS = map[int64]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x01021997: "9p",
	0x00c36400: "ceph",
}

func checkLocalFilesystem(dbPath string) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(dbPath), &st); err != nil {
		return fmt.Errorf("storage: check %s: %w", filepath.Dir(dbPath), err)
	}
	if name, bad := networkFS[int64(st.Type)]; bad {
		return fmt.Errorf("%w (%s)", ErrNetworkFilesystem, name)
	}
	return nil
}
```

`server/internal/storage/fs_other.go`:
```go
//go:build !linux

package storage

// The server image targets Linux; other OSes are development-only.
func checkLocalFilesystem(string) error { return nil }
```

`server/internal/storage/migrate.go`:
```go
package storage

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Source is one module's migrations: FS holds *.sql files at its root, each
// named YYYYMMDDHHMMSS_<module>_<what>.sql with <module> equal to Module.
type Source struct {
	Module string
	FS     fs.FS
}

var (
	// ErrSchemaTooNew means the database has migrations this binary doesn't
	// know, e.g. after a downgrade. Restore a compatible snapshot instead.
	ErrSchemaTooNew = errors.New("storage: database schema is newer than this version")
	// ErrMigrationEdited means an applied migration's file changed.
	ErrMigrationEdited = errors.New("storage: applied migration was edited")
)

var migrationName = regexp.MustCompile(`^(\d{14})_([a-z0-9]+)_([a-z0-9_]+)\.sql$`)

type migration struct {
	version string // file name without .sql
	sum     string // sha256 hex of the file
	body    string
}

//go:embed migrations/*.sql
var coreFS embed.FS

// Core is storage's own migrations (the settings table).
func Core() Source {
	sub, err := fs.Sub(coreFS, "migrations")
	if err != nil {
		panic(err) // embedded at build time
	}
	return Source{Module: "storage", FS: sub}
}

func collect(srcs []Source) ([]migration, error) {
	var out []migration
	stamps := map[string]string{}
	for _, src := range srcs {
		names, err := fs.Glob(src.FS, "*.sql")
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			m := migrationName.FindStringSubmatch(n)
			if m == nil {
				return nil, fmt.Errorf("storage: migration %s: name must be YYYYMMDDHHMMSS_<module>_<what>.sql", n)
			}
			if m[2] != src.Module {
				return nil, fmt.Errorf("storage: migration %s: module %q registered it", n, src.Module)
			}
			if other, dup := stamps[m[1]]; dup {
				return nil, fmt.Errorf("storage: migrations %s and %s share timestamp %s", other, n, m[1])
			}
			stamps[m[1]] = n
			b, err := fs.ReadFile(src.FS, n)
			if err != nil {
				return nil, fmt.Errorf("storage: migration %s: %w", n, err)
			}
			h := sha256.Sum256(b)
			out = append(out, migration{version: strings.TrimSuffix(n, ".sql"), sum: hex.EncodeToString(h[:]), body: string(b)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (d *DB) ensureLedger(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		checksum   TEXT NOT NULL,
		applied_at TEXT NOT NULL
	) STRICT`)
	if err != nil {
		return fmt.Errorf("storage: create schema_migrations: %w", err)
	}
	return nil
}

// Applied returns applied migration versions in order.
func (d *DB) Applied(ctx context.Context) ([]string, error) {
	if err := d.ensureLedger(ctx); err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("storage: read schema_migrations: %w", err)
	}
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

// Pending validates srcs against the database and returns the versions not
// yet applied. It fails with ErrSchemaTooNew or ErrMigrationEdited.
func (d *DB) Pending(ctx context.Context, srcs []Source) ([]string, error) {
	ms, err := collect(srcs)
	if err != nil {
		return nil, err
	}
	_, pending, err := d.plan(ctx, ms)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(pending))
	for i, m := range pending {
		out[i] = m.version
	}
	return out, nil
}

func (d *DB) plan(ctx context.Context, ms []migration) (known map[string]migration, pending []migration, err error) {
	if err := d.ensureLedger(ctx); err != nil {
		return nil, nil, err
	}
	known = map[string]migration{}
	for _, m := range ms {
		known[m.version] = m
	}
	rows, err := d.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, nil, fmt.Errorf("storage: read schema_migrations: %w", err)
	}
	applied := map[string]bool{}
	for rows.Next() {
		var v, sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return nil, nil, err
		}
		m, ok := known[v]
		switch {
		case !ok:
			rows.Close()
			return nil, nil, fmt.Errorf("%w: unknown applied migration %s", ErrSchemaTooNew, v)
		case m.sum != sum:
			rows.Close()
			return nil, nil, fmt.Errorf("%w: %s", ErrMigrationEdited, v)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, m := range ms {
		if !applied[m.version] {
			pending = append(pending, m)
		}
	}
	return known, pending, nil
}

// Migrate applies all pending migrations in version order in one transaction:
// either every one applies or none does. Any error means the database is not
// ready (BASE-03).
func (d *DB) Migrate(ctx context.Context, srcs []Source, now func() time.Time) ([]string, error) {
	ms, err := collect(srcs)
	if err != nil {
		return nil, err
	}
	_, pending, err := d.plan(ctx, ms)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	// One transaction for the whole batch: a failing upgrade leaves the old
	// schema intact, so every crash-loop snapshot stays restorable by the old version.
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: migrate: begin: %w", err)
	}
	defer tx.Rollback()
	var done []string
	for _, m := range pending {
		if _, err := tx.ExecContext(ctx, m.body); err != nil {
			return nil, fmt.Errorf("storage: migration %s: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?, ?, ?)`,
			m.version, m.sum, now().UTC().Format(time.RFC3339)); err != nil {
			return nil, fmt.Errorf("storage: migration %s: record: %w", m.version, err)
		}
		done = append(done, m.version)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: migrate: commit: %w", err)
	}
	return done, nil
}
```

`server/internal/storage/migrations/20261026000000_storage_settings.sql`:
```sql
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
```

`server/internal/storage/settings.go`:
```go
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetSetting returns a durable setting; ok is false when it is unset. Values
// may be secrets: callers must not log them.
func (d *DB) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = d.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("storage: get setting %s: %w", key, err)
	}
	return value, true, nil
}

// SetSetting inserts or replaces a durable setting.
func (d *DB) SetSetting(ctx context.Context, key, value string, now time.Time) error {
	_, err := d.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("storage: set setting %s: %w", key, err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go -C server vet ./internal/storage/... && go -C server test -race ./internal/storage/...`
Expected: `ok  .../internal/storage`. The 7 tests cover WAL/foreign keys, restart without reapplying
(BASE-03), cross-module order, rollback of the whole batch on failure, name validation, a too-new
schema and an edited migration.

- [ ] **Step 6: Commit**

```bash
git add server/go.mod server/go.sum server/internal/storage .github/workflows/check.yml
git commit -m "feat(platform): SQLite storage with module migrations and settings"
```

---

### Task 2: Consistent snapshots, retention and freshness

**Files:**
- Create: `server/internal/storage/snapshot.go`
- Test: `server/internal/storage/snapshot_test.go`

**Interfaces:**
- Consumes: `Open`, `Applied`, `Migrate`, `Core`, `SetSetting` (Task 1); test helpers `openTemp`, `fixedNow` (Task 1 test file).
- Produces: `Manifest`, `ManifestFile`, `SnapshotOptions`, `Snapshot`, `Keep`, `Prune`, `Latest`, `Stale`,
  `StaleAfter`; unexported `ensurePrivateFile` (Task 1), `describe`, `copyFile`, `syncDir`, `checkSnapshotDB`,
  `readManifest`, `latest(dir, reason)`, used by Tasks 3–4.

- [ ] **Step 1: Write the failing tests**

`server/internal/storage/snapshot_test.go`:
```go
package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seeded(t *testing.T) (*DB, string, []Source) {
	t.Helper()
	d, path := openTemp(t)
	srcs := []Source{Core()}
	ctx := context.Background()
	if _, err := d.Migrate(ctx, srcs, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSetting(ctx, "server.name", "before", fixedNow()); err != nil {
		t.Fatal(err)
	}
	return d, path, srcs
}

func TestSnapshotIsConsistentAndChecksummed(t *testing.T) {
	d, path, _ := seeded(t)
	cfg := filepath.Dir(path)
	extra := filepath.Join(cfg, "m1.json")
	os.WriteFile(extra, []byte(`{"viewer":"synthetic"}`), 0o600)
	dir := filepath.Join(cfg, "backups")

	snap, m, err := d.Snapshot(context.Background(), SnapshotOptions{Dir: dir, Reason: "manual", AppVersion: "test", ExtraFiles: []string{extra}, Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(snap) != "20261026T030000Z-manual" || len(m.Files) != 2 || len(m.Schema) != 1 {
		t.Fatalf("snapshot %s manifest %+v", snap, m)
	}
	for _, f := range m.Files {
		got, err := describe(filepath.Join(snap, f.Name))
		if err != nil || got != f {
			t.Fatalf("file %s: %+v vs %+v (%v)", f.Name, got, f, err)
		}
	}
	if st, _ := os.Stat(snap); st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("snapshot dir mode %v", st.Mode())
	}
	// The live database, its WAL and every snapshot file hold secrets: 0600.
	for _, p := range []string{path, path + "-wal", filepath.Join(snap, "lisimflix.db"), filepath.Join(snap, "m1.json"), filepath.Join(snap, "manifest.json")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", filepath.Base(p), st.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("staging left behind: %v", entries)
	}
}

func TestSnapshotFailureLeavesNothing(t *testing.T) {
	d, path, _ := seeded(t)
	dir := filepath.Join(filepath.Dir(path), "backups")
	_, _, err := d.Snapshot(context.Background(), SnapshotOptions{Dir: dir, Reason: "manual", ExtraFiles: []string{"/nonexistent/m1.json"}, Now: fixedNow()})
	if err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("failed snapshot left %v", entries)
	}
}

func TestPruneKeepsNewestPerReasonAndIgnoresBroken(t *testing.T) {
	d, path, _ := seeded(t)
	dir := filepath.Join(filepath.Dir(path), "backups")
	ctx := context.Background()
	base := fixedNow()
	for i := range 9 {
		if _, _, err := d.Snapshot(ctx, SnapshotOptions{Dir: dir, Reason: "nightly", Now: base.Add(time.Duration(i) * 24 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	os.Mkdir(filepath.Join(dir, "20991231T000000Z-nightly"), 0o700) // no manifest: never counts
	os.Mkdir(filepath.Join(dir, ".tmp-leftover"), 0o700)
	future := filepath.Join(dir, "20200101T000000Z-weekly") // a reason from a newer version
	os.Mkdir(future, 0o700)
	os.WriteFile(filepath.Join(future, "manifest.json"), []byte(`{"format":1,"reason":"weekly","createdAt":"2020-01-01T00:00:00Z","newField":true}`), 0o600)
	removed, err := Prune(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0] != "20261026T030000Z-nightly" || removed[1] != "20261027T030000Z-nightly" {
		t.Fatalf("removed %v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tmp-leftover")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging leftover not cleaned")
	}
	if _, err := os.Stat(future); err != nil {
		t.Fatal("snapshot with an unknown reason was deleted")
	}
	latest, found, _ := Latest(dir)
	if !found || !latest.CreatedAt.Equal(base.Add(8*24*time.Hour)) {
		t.Fatalf("latest %+v", latest)
	}
	if Stale(latest, found, latest.CreatedAt.Add(25*time.Hour)) || !Stale(latest, found, latest.CreatedAt.Add(27*time.Hour)) {
		t.Fatal("stale threshold")
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go -C server test ./internal/storage/...`
Expected: FAIL, `undefined: SnapshotOptions`.

- [ ] **Step 3: Implement**

`server/internal/storage/snapshot.go`:
```go
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	manifestName = "manifest.json"
	snapshotDB   = "lisimflix.db"
	maxManifest  = 1 << 20
)

// Manifest describes one snapshot directory.
type Manifest struct {
	Format     int            `json:"format"` // 1
	CreatedAt  time.Time      `json:"createdAt"`
	Reason     string         `json:"reason"` // nightly | pre-upgrade | pre-restore | manual
	AppVersion string         `json:"appVersion"`
	Schema     []string       `json:"schema"` // applied migration versions
	Files      []ManifestFile `json:"files"`
}

// ManifestFile is one file in a snapshot. Name is a bare file name.
type ManifestFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// SnapshotOptions configures Snapshot.
type SnapshotOptions struct {
	Dir        string // backups root, e.g. /config/backups
	Reason     string // nightly | pre-upgrade | pre-restore | manual
	AppVersion string
	ExtraFiles []string // durable config files to include, e.g. /config/m1.json
	Now        time.Time
}

var validReasons = map[string]bool{"nightly": true, "pre-upgrade": true, "pre-restore": true, "manual": true}

// Snapshot writes a consistent copy of the live database (VACUUM INTO) plus
// ExtraFiles and a checksummed manifest into a new directory under Dir. The
// directory appears atomically; a failed snapshot leaves nothing behind.
func (d *DB) Snapshot(ctx context.Context, o SnapshotOptions) (string, Manifest, error) {
	if !validReasons[o.Reason] {
		return "", Manifest{}, fmt.Errorf("storage: snapshot reason %q", o.Reason)
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return "", Manifest{}, fmt.Errorf("storage: snapshot dir: %w", err)
	}
	name := o.Now.UTC().Format("20060102T150405Z") + "-" + o.Reason
	final := filepath.Join(o.Dir, name)
	if _, err := os.Stat(final); err == nil {
		return "", Manifest{}, fmt.Errorf("storage: snapshot %s already exists", name)
	}
	staging, err := os.MkdirTemp(o.Dir, ".tmp-"+name+"-")
	if err != nil {
		return "", Manifest{}, fmt.Errorf("storage: snapshot staging: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(staging)
		}
	}()

	dbCopy := filepath.Join(staging, snapshotDB)
	// VACUUM INTO accepts an existing empty file, so create it 0600 first.
	if err := ensurePrivateFile(dbCopy); err != nil {
		return "", Manifest{}, fmt.Errorf("storage: snapshot: %w", err)
	}
	if _, err := d.ExecContext(ctx, `VACUUM INTO ?`, dbCopy); err != nil {
		return "", Manifest{}, fmt.Errorf("storage: snapshot: %w", err)
	}
	schema, err := checkSnapshotDB(ctx, dbCopy)
	if err != nil {
		return "", Manifest{}, err
	}
	dbFile, err := describe(dbCopy)
	if err != nil {
		return "", Manifest{}, err
	}
	m := Manifest{Format: 1, CreatedAt: o.Now.UTC(), Reason: o.Reason, AppVersion: o.AppVersion, Schema: schema, Files: []ManifestFile{dbFile}}
	seen := map[string]bool{snapshotDB: true, manifestName: true}
	for _, src := range o.ExtraFiles {
		base := filepath.Base(src)
		if seen[base] {
			return "", Manifest{}, fmt.Errorf("storage: snapshot: duplicate file name %s", base)
		}
		seen[base] = true
		if err := copyFile(src, filepath.Join(staging, base)); err != nil {
			return "", Manifest{}, fmt.Errorf("storage: snapshot %s: %w", base, err)
		}
		f, err := describe(filepath.Join(staging, base))
		if err != nil {
			return "", Manifest{}, err
		}
		m.Files = append(m.Files, f)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := writeFileSync(filepath.Join(staging, manifestName), b); err != nil {
		return "", Manifest{}, err
	}
	if err := syncDir(staging); err != nil {
		return "", Manifest{}, err
	}
	if err := os.Rename(staging, final); err != nil {
		return "", Manifest{}, fmt.Errorf("storage: snapshot publish: %w", err)
	}
	if err := syncDir(o.Dir); err != nil {
		return "", Manifest{}, err
	}
	ok = true
	return final, m, nil
}

// checkSnapshotDB opens a snapshot copy, runs integrity_check and returns its
// applied migrations.
func checkSnapshotDB(ctx context.Context, path string) ([]string, error) {
	c, err := Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("storage: open snapshot: %w", err)
	}
	defer c.Close()
	var res string
	if err := c.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&res); err != nil || res != "ok" {
		return nil, fmt.Errorf("storage: snapshot integrity check failed: %q %v", res, err)
	}
	return c.Applied(ctx)
}

func describe(path string) (ManifestFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return ManifestFile{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return ManifestFile{}, err
	}
	return ManifestFile{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFileSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Keep is how many completed snapshots of each reason Prune retains.
var Keep = map[string]int{"nightly": 7, "pre-upgrade": 3, "pre-restore": 3, "manual": 10}

// Prune deletes the oldest completed snapshots beyond Keep for each reason.
// Only directories with a readable manifest count, so a failed or partial
// snapshot never displaces a usable one. Reasons this version doesn't know are
// kept. Staging leftovers (.tmp-*) are removed.
func Prune(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byReason := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".tmp-") {
			os.RemoveAll(filepath.Join(dir, e.Name()))
			continue
		}
		m, err := readManifest(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		byReason[m.Reason] = append(byReason[m.Reason], e.Name())
	}
	var removed []string
	for reason, names := range byReason {
		sort.Strings(names) // names start with a UTC timestamp
		keep, known := Keep[reason]
		if !known {
			continue // written by a newer version: never delete it
		}
		for len(names) > keep {
			if err := os.RemoveAll(filepath.Join(dir, names[0])); err != nil {
				return removed, err
			}
			removed = append(removed, names[0])
			names = names[1:]
		}
	}
	sort.Strings(removed)
	return removed, nil
}

// Latest returns the newest completed snapshot's manifest, for backup
// freshness (warn after 26h without success, spec §11.3).
func Latest(dir string) (Manifest, bool, error) { return latest(dir, "") }

// latest returns the newest completed snapshot, of reason if non-empty.
func latest(dir, reason string) (Manifest, bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	var best Manifest
	found := false
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		m, err := readManifest(filepath.Join(dir, e.Name()))
		if err != nil || (reason != "" && m.Reason != reason) {
			continue
		}
		if !found || m.CreatedAt.After(best.CreatedAt) {
			best, found = m, true
		}
	}
	return best, found, nil
}

func readManifest(snapDir string) (Manifest, error) {
	f, err := os.Open(filepath.Join(snapDir, manifestName))
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	var m Manifest
	// Unknown fields are ignored so a newer version's snapshots stay visible to
	// Prune and Latest; Format guards incompatible layouts.
	if err := json.NewDecoder(io.LimitReader(f, maxManifest)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("storage: manifest: %w", err)
	}
	if m.Format != 1 {
		return Manifest{}, fmt.Errorf("storage: manifest format %d", m.Format)
	}
	return m, nil
}

// StaleAfter is how long without a successful snapshot before backups are
// reported stale (spec §11.3: 24h RPO, warn after 26h).
const StaleAfter = 26 * time.Hour

// Stale reports whether backups need attention at now.
func Stale(latest Manifest, found bool, now time.Time) bool {
	return !found || now.Sub(latest.CreatedAt) > StaleAfter
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go -C server test -race ./internal/storage/...`
Expected: PASS. Snapshot files match their manifest checksums. The live database, its WAL and
every snapshot file are 0600 and directories 0700. A failed snapshot leaves nothing behind.
`Prune` keeps the newest 7 nightly snapshots, ignores directories without a manifest, and never
deletes a reason it doesn't know. `Stale` turns on after 26 hours.

- [ ] **Step 5: Commit**

```bash
git add server/internal/storage/snapshot.go server/internal/storage/snapshot_test.go
git commit -m "feat(platform): consistent snapshots with checksummed manifests and retention"
```

---

### Task 3: Validated restore with a pre-restore snapshot

**Files:**
- Create: `server/internal/storage/restore.go`
- Test: `server/internal/storage/restore_test.go`

**Interfaces:**
- Consumes: `Snapshot`, `readManifest`, `describe`, `copyFile`, `checkSnapshotDB`, `syncDir` (Task 2); `collect`, `Open` (Task 1).
- Produces: `RestoreOptions`, `Restore`, `ErrInvalidSnapshot`. The caller must have stopped serving,
  closed the database and stopped jobs, and must revoke sessions afterwards.

- [ ] **Step 1: Write the failing tests**

`server/internal/storage/restore_test.go`:
```go
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestoreRoundTripWithPreRestoreSnapshot(t *testing.T) {
	d, path, srcs := seeded(t)
	ctx := context.Background()
	cfg := filepath.Dir(path)
	dir := filepath.Join(cfg, "backups")
	extra := filepath.Join(cfg, "m1.json")
	os.WriteFile(extra, []byte(`{"v":1}`), 0o600)
	snap, _, err := d.Snapshot(ctx, SnapshotOptions{Dir: dir, Reason: "manual", ExtraFiles: []string{extra}, Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	d.SetSetting(ctx, "server.name", "after", fixedNow())
	os.WriteFile(extra, []byte(`{"v":2}`), 0o600)
	d.Close()

	pre, err := Restore(ctx, RestoreOptions{SnapshotDir: snap, DBPath: path, ConfigDir: cfg, BackupsDir: dir, ExtraFiles: []string{extra}, Known: srcs, Now: fixedNow().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pre, "-pre-restore") {
		t.Fatalf("pre-restore snapshot %q", pre)
	}
	// The pre-restore snapshot keeps the config as it was just before restore.
	pm, err := readManifest(pre)
	if err != nil || len(pm.Files) != 2 {
		t.Fatalf("pre-restore manifest %+v %v", pm, err)
	}
	if b, _ := os.ReadFile(filepath.Join(pre, "m1.json")); string(b) != `{"v":2}` {
		t.Fatalf("pre-restore m1.json %s", b)
	}
	r, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, _, _ := r.GetSetting(ctx, "server.name"); v != "before" {
		t.Fatalf("restored setting %q", v)
	}
	if b, _ := os.ReadFile(extra); string(b) != `{"v":1}` {
		t.Fatalf("restored extra file %s", b)
	}
}

func TestRestoreRejectsTamperedOrUnsafeSnapshots(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(snap string){
		"checksum mismatch": func(snap string) {
			os.WriteFile(filepath.Join(snap, "m1.json"), []byte(`{"tampered":true}`), 0o600)
		},
		"path traversal": func(snap string) {
			editManifest(t, snap, func(m *Manifest) { m.Files[1].Name = "../escape.json" })
		},
		"newer schema": func(snap string) {
			editManifest(t, snap, func(m *Manifest) { m.Schema = append(m.Schema, "20991231000000_future_x") })
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			d, path, srcs := seeded(t)
			cfg := filepath.Dir(path)
			extra := filepath.Join(cfg, "m1.json")
			os.WriteFile(extra, []byte(`{"v":1}`), 0o600)
			dir := filepath.Join(cfg, "backups")
			snap, _, err := d.Snapshot(ctx, SnapshotOptions{Dir: dir, Reason: "manual", ExtraFiles: []string{extra}, Now: fixedNow()})
			if err != nil {
				t.Fatal(err)
			}
			d.SetSetting(ctx, "server.name", "current", fixedNow())
			d.Close()
			corrupt(snap)

			_, err = Restore(ctx, RestoreOptions{SnapshotDir: snap, DBPath: path, ConfigDir: cfg, BackupsDir: dir, Known: srcs, Now: fixedNow().Add(time.Hour)})
			if !errors.Is(err, ErrInvalidSnapshot) && !errors.Is(err, ErrSchemaTooNew) {
				t.Fatalf("want rejection, got %v", err)
			}
			r, _ := Open(ctx, path)
			defer r.Close()
			if v, _, _ := r.GetSetting(ctx, "server.name"); v != "current" {
				t.Fatalf("current install changed: %q", v)
			}
		})
	}
}

func editManifest(t *testing.T, snap string, edit func(*Manifest)) {
	t.Helper()
	m, err := readManifest(snap)
	if err != nil {
		t.Fatal(err)
	}
	edit(&m)
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(snap, manifestName), b, 0o600)
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go -C server test ./internal/storage/...`
Expected: FAIL, `undefined: Restore`.

- [ ] **Step 3: Implement**

`server/internal/storage/restore.go`:
```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// RestoreOptions configures Restore. The live database must be closed: the
// caller has entered maintenance and stopped jobs (spec §11.3).
type RestoreOptions struct {
	SnapshotDir string   // the snapshot to restore
	DBPath      string   // live database path, e.g. /config/lisimflix.db
	ConfigDir   string   // where extra files are restored, e.g. /config
	BackupsDir  string   // where the pre-restore snapshot goes
	ExtraFiles  []string // current durable config files, kept in the pre-restore snapshot
	Known       []Source // migrations this binary knows
	AppVersion  string
	Now         time.Time
}

// ErrInvalidSnapshot means a snapshot failed validation; nothing was changed.
var ErrInvalidSnapshot = errors.New("storage: invalid snapshot")

// Restore validates a snapshot, takes a pre-restore snapshot of the current
// database and config files, then atomically replaces the database. On any
// validation failure the current installation is left untouched. Extra files
// are replaced one by one after the database; if that fails part-way, restore
// the pre-restore snapshot. The caller revokes sessions afterwards.
func Restore(ctx context.Context, o RestoreOptions) (preRestore string, err error) {
	m, err := readManifest(o.SnapshotDir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	hasDB := false
	for _, f := range m.Files {
		if f.Name != filepath.Base(f.Name) || f.Name == "." || f.Name == ".." || f.Name == manifestName || f.Name[0] == '.' {
			return "", fmt.Errorf("%w: bad file name %q", ErrInvalidSnapshot, f.Name)
		}
		got, err := describe(filepath.Join(o.SnapshotDir, f.Name))
		if err != nil || got.Size != f.Size || got.SHA256 != f.SHA256 {
			return "", fmt.Errorf("%w: %s does not match its checksum", ErrInvalidSnapshot, f.Name)
		}
		hasDB = hasDB || f.Name == snapshotDB
	}
	if !hasDB {
		return "", fmt.Errorf("%w: no database file", ErrInvalidSnapshot)
	}
	known, err := collect(o.Known)
	if err != nil {
		return "", err
	}
	knownSet := map[string]bool{}
	for _, k := range known {
		knownSet[k.version] = true
	}
	for _, v := range m.Schema {
		if !knownSet[v] {
			return "", fmt.Errorf("%w: snapshot migration %s", ErrSchemaTooNew, v)
		}
	}

	// Stage the database next to the live file so the final rename is atomic.
	staged := o.DBPath + ".restore-tmp"
	os.Remove(staged)
	if err := copyFile(filepath.Join(o.SnapshotDir, snapshotDB), staged); err != nil {
		return "", fmt.Errorf("storage: restore stage: %w", err)
	}
	defer os.Remove(staged)
	schema, err := checkSnapshotDB(ctx, staged)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if !slices.Equal(schema, m.Schema) {
		return "", fmt.Errorf("%w: database schema differs from manifest", ErrInvalidSnapshot)
	}
	os.Remove(staged + "-wal")
	os.Remove(staged + "-shm")

	if _, err := os.Stat(o.DBPath); err == nil {
		cur, err := Open(ctx, o.DBPath)
		if err != nil {
			return "", fmt.Errorf("storage: open current for pre-restore snapshot: %w", err)
		}
		preRestore, _, err = cur.Snapshot(ctx, SnapshotOptions{Dir: o.BackupsDir, Reason: "pre-restore", AppVersion: o.AppVersion, ExtraFiles: existing(o.ExtraFiles), Now: o.Now})
		cur.Close()
		if err != nil {
			return "", fmt.Errorf("storage: pre-restore snapshot: %w", err)
		}
	}

	os.Remove(o.DBPath + "-wal")
	os.Remove(o.DBPath + "-shm")
	if err := os.Rename(staged, o.DBPath); err != nil {
		return preRestore, fmt.Errorf("storage: restore switch: %w", err)
	}
	for _, f := range m.Files {
		if f.Name == snapshotDB {
			continue
		}
		dst := filepath.Join(o.ConfigDir, f.Name)
		tmp := dst + ".restore-tmp"
		os.Remove(tmp)
		if err := copyFile(filepath.Join(o.SnapshotDir, f.Name), tmp); err != nil {
			return preRestore, fmt.Errorf("storage: restore %s: %w", f.Name, err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return preRestore, fmt.Errorf("storage: restore %s: %w", f.Name, err)
		}
	}
	return preRestore, syncDir(filepath.Dir(o.DBPath))
}

// existing drops paths that don't exist yet (e.g. a config file on a fresh install).
func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go -C server test -race ./internal/storage/...`
Expected: PASS. A good snapshot restores the database and config files and leaves a
`…-pre-restore` snapshot containing the database **and** the config as it was before the restore.
A checksum mismatch, a `../` file name or a newer schema is rejected, and the current installation
is unchanged.

- [ ] **Step 5: Commit**

```bash
git add server/internal/storage/restore.go server/internal/storage/restore_test.go
git commit -m "feat(platform): validated restore with pre-restore snapshot"
```

---

### Task 4: Pre-upgrade backup and the nightly job

**Files:**
- Create: `server/internal/storage/backup.go`
- Test: `server/internal/storage/backup_test.go`

**Interfaces:**
- Consumes: `Pending`, `Applied`, `Migrate` (Task 1); `Snapshot`, `Prune`, `Latest` (Task 2).
- Produces: `MigrateWithBackup` (what OPS-1 calls at startup), `ReuseWindow`, `NextRun`, `Nightly` (`RunOnce`, `Run`).

- [ ] **Step 1: Write the failing tests**

`server/internal/storage/backup_test.go`:
```go
package storage

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestMigrateWithBackupSnapshotsExistingDatabaseOnly(t *testing.T) {
	ctx := context.Background()
	d, path := openTemp(t)
	dir := filepath.Join(filepath.Dir(path), "backups")
	o := SnapshotOptions{Dir: dir, AppVersion: "test"}
	if _, err := d.MigrateWithBackup(ctx, []Source{Core()}, o, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Latest(dir); found {
		t.Fatal("new database should not take a pre-upgrade snapshot")
	}
	upgrade := []Source{Core(), src("library", map[string]string{"20261027000000_library_roots.sql": `CREATE TABLE roots (id TEXT) STRICT;`})}
	done, err := d.MigrateWithBackup(ctx, upgrade, o, fixedNow)
	if err != nil || len(done) != 1 {
		t.Fatalf("upgrade %v %v", done, err)
	}
	m, found, _ := Latest(dir)
	if !found || m.Reason != "pre-upgrade" || len(m.Schema) != 1 {
		t.Fatalf("pre-upgrade snapshot %+v", m)
	}
}

// A container restarting on a failing migration must not write a new
// pre-upgrade copy on every start.
func TestFailingUpgradeCrashLoopReusesOneSnapshot(t *testing.T) {
	ctx := context.Background()
	d, path := openTemp(t)
	dir := filepath.Join(filepath.Dir(path), "backups")
	o := SnapshotOptions{Dir: dir, AppVersion: "test"}
	if _, err := d.MigrateWithBackup(ctx, []Source{Core()}, o, fixedNow); err != nil {
		t.Fatal(err)
	}
	broken := []Source{Core(), src("library", map[string]string{"20261027000000_library_bad.sql": `NOT SQL;`})}
	for i := range 10 {
		now := func() time.Time { return fixedNow().Add(time.Duration(i) * 10 * time.Second) }
		if _, err := d.MigrateWithBackup(ctx, broken, o, now); err == nil {
			t.Fatal("broken migration succeeded")
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("want 1 pre-upgrade snapshot, got %d", len(entries))
	}
}

// After a failed upgrade, a rollback and a month of use, the next upgrade must
// take a fresh snapshot: same schema doesn't mean same data.
func TestRollbackThenUpgradeTakesFreshSnapshot(t *testing.T) {
	ctx := context.Background()
	d, path := openTemp(t)
	dir := filepath.Join(filepath.Dir(path), "backups")
	o := SnapshotOptions{Dir: dir, AppVersion: "test"}
	if _, err := d.MigrateWithBackup(ctx, []Source{Core()}, o, fixedNow); err != nil {
		t.Fatal(err)
	}
	broken := []Source{Core(), src("library", map[string]string{"20261027000000_library_bad.sql": `NOT SQL;`})}
	if _, err := d.MigrateWithBackup(ctx, broken, o, fixedNow); err == nil {
		t.Fatal("broken migration succeeded")
	}
	if err := d.SetSetting(ctx, "after", "month-of-data", fixedNow()); err != nil {
		t.Fatal(err)
	}
	later := func() time.Time { return fixedNow().Add(30 * 24 * time.Hour) }
	good := []Source{Core(), src("library", map[string]string{"20261127000000_library_ok.sql": `CREATE TABLE x (a TEXT) STRICT;`})}
	if _, err := d.MigrateWithBackup(ctx, good, o, later); err != nil {
		t.Fatal(err)
	}
	m, found, err := latest(dir, "pre-upgrade")
	if err != nil || !found || !m.CreatedAt.Equal(later()) {
		t.Fatalf("day-31 upgrade reused the snapshot from %v (%v)", m.CreatedAt, err)
	}
	snap, err := Open(ctx, filepath.Join(dir, "20261125T030000Z-pre-upgrade", "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if v, _, _ := snap.GetSetting(ctx, "after"); v != "month-of-data" {
		t.Fatalf("fresh pre-upgrade snapshot is missing recent data: %q", v)
	}
}

// An upgrade whose first migration applies and second fails, restarting every
// 10 minutes for 4 hours: a pre-upgrade snapshot at the original schema must
// survive, or rolling back to the previous version becomes impossible.
func TestLongCrashLoopKeepsOriginalSchemaSnapshot(t *testing.T) {
	ctx := context.Background()
	d, path := openTemp(t)
	dir := filepath.Join(filepath.Dir(path), "backups")
	o := SnapshotOptions{Dir: dir, AppVersion: "test"}
	if _, err := d.MigrateWithBackup(ctx, []Source{Core()}, o, fixedNow); err != nil {
		t.Fatal(err)
	}
	orig, _ := d.Applied(ctx)
	up := []Source{Core(), src("library", map[string]string{
		"20261027000000_library_ok.sql":  `CREATE TABLE y (a TEXT) STRICT;`,
		"20261027000001_library_bad.sql": `NOT SQL;`,
	})}
	for i := 0; i <= 24; i++ {
		at := fixedNow().Add(time.Duration(i) * 10 * time.Minute)
		if _, err := d.MigrateWithBackup(ctx, up, o, func() time.Time { return at }); err == nil {
			t.Fatal("broken upgrade succeeded")
		}
	}
	if after, _ := d.Applied(ctx); !slices.Equal(after, orig) {
		t.Fatalf("failed upgrade left schema %v, want %v", after, orig)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if m, err := readManifest(filepath.Join(dir, e.Name())); err == nil && m.Reason == "pre-upgrade" && slices.Equal(m.Schema, orig) {
			return
		}
	}
	t.Fatal("no pre-upgrade snapshot left at the original schema; rollback to the old version is impossible")
}

func TestNextRunAndNightlyOnce(t *testing.T) {
	loc := time.FixedZone("AEST", 10*3600)
	if got := NextRun(time.Date(2026, 10, 26, 2, 30, 0, 0, loc), 3); !got.Equal(time.Date(2026, 10, 26, 3, 0, 0, 0, loc)) {
		t.Fatalf("before hour: %v", got)
	}
	if got := NextRun(time.Date(2026, 10, 26, 3, 0, 0, 0, loc), 3); !got.Equal(time.Date(2026, 10, 27, 3, 0, 0, 0, loc)) {
		t.Fatalf("at hour: %v", got)
	}
	d, path, _ := seeded(t)
	// After and Hour are left unset: the defaults (time.After, 3) apply.
	n := &Nightly{DB: d, Opts: SnapshotOptions{Dir: filepath.Join(filepath.Dir(path), "backups")}, Now: fixedNow,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := n.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m, found, _ := Latest(n.Opts.Dir); !found || m.Reason != "nightly" {
		t.Fatalf("nightly %+v", m)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go -C server test ./internal/storage/...`
Expected: FAIL, `undefined: MigrateWithBackup`.

- [ ] **Step 3: Implement**

`server/internal/storage/backup.go`:
```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"
)

// ReuseWindow is how recent a same-schema pre-upgrade snapshot must be to be
// reused instead of taking a new one. It covers a crash loop (minutes), not a
// later upgrade after a rollback, whose data may have changed since.
const ReuseWindow = time.Hour

// MigrateWithBackup takes a verified "pre-upgrade" snapshot before applying
// pending migrations to an existing database (spec §11.4). A brand-new
// database is migrated without one. If the newest pre-upgrade snapshot has this
// database's schema and is younger than ReuseWindow, it is reused, so a
// container restarting on a failing migration doesn't write a copy every start.
func (d *DB) MigrateWithBackup(ctx context.Context, srcs []Source, o SnapshotOptions, now func() time.Time) ([]string, error) {
	pending, err := d.Pending(ctx, srcs)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	applied, err := d.Applied(ctx)
	if err != nil {
		return nil, err
	}
	if len(applied) > 0 {
		prev, found, err := latest(o.Dir, "pre-upgrade")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		t := now()
		if !found || !slices.Equal(prev.Schema, applied) || t.Sub(prev.CreatedAt) >= ReuseWindow {
			o.Reason, o.Now = "pre-upgrade", t
			if _, _, err := d.Snapshot(ctx, o); err != nil {
				return nil, fmt.Errorf("storage: pre-upgrade snapshot: %w", err)
			}
			if _, err := Prune(o.Dir); err != nil {
				return nil, fmt.Errorf("storage: prune: %w", err)
			}
		}
	}
	return d.Migrate(ctx, srcs, now)
}

// NextRun is the next local time at hour:00 strictly after now.
func NextRun(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Nightly takes one snapshot a day and prunes old ones. Failures are logged
// and never prune a usable snapshot.
type Nightly struct {
	DB    *DB
	Opts  SnapshotOptions                      // Dir, AppVersion, ExtraFiles; Reason and Now are set per run
	Hour  int                                  // hour in the container's time zone (TZ; UTC if unset), default 3
	Now   func() time.Time                     // default time.Now
	After func(time.Duration) <-chan time.Time // default time.After
	Log   *slog.Logger                         // default slog.Default()
}

func (n *Nightly) defaults() {
	if n.Hour == 0 {
		n.Hour = 3
	}
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.After == nil {
		n.After = time.After
	}
	if n.Log == nil {
		n.Log = slog.Default()
	}
}

// RunOnce takes a nightly snapshot, then prunes.
func (n *Nightly) RunOnce(ctx context.Context) error {
	n.defaults()
	o := n.Opts
	o.Reason, o.Now = "nightly", n.Now()
	path, _, err := n.DB.Snapshot(ctx, o)
	if err != nil {
		return err
	}
	removed, err := Prune(o.Dir)
	if err != nil {
		return fmt.Errorf("storage: prune: %w", err)
	}
	n.Log.Info("backup snapshot complete", "snapshot", path, "pruned", len(removed))
	return nil
}

// Run loops until ctx is cancelled.
func (n *Nightly) Run(ctx context.Context) {
	n.defaults()
	for {
		now := n.Now()
		wait := NextRun(now, n.Hour).Sub(now)
		select {
		case <-ctx.Done():
			return
		case <-n.After(wait):
		}
		if err := n.RunOnce(ctx); err != nil {
			n.Log.Error("backup snapshot failed", "err", err)
		}
	}
}
```

- [ ] **Step 4: Run the full package and the repository check**

Run: `go -C server test -race ./internal/storage/... && CGO_ENABLED=0 go -C server build ./... && make check`
Expected: all 17 storage tests pass. They include 10 failing restarts leaving exactly one pre-upgrade
snapshot; a rollback followed by an upgrade a month later taking a fresh snapshot that contains the
month's data; and a 4-hour crash loop on a half-failing upgrade that leaves the schema untouched and
keeps a pre-upgrade snapshot at the original schema. The static build succeeds and `make check` passes.

- [ ] **Step 5: Commit and open the PR**

```bash
git add server/internal/storage/backup.go server/internal/storage/backup_test.go
git commit -m "feat(platform): pre-upgrade snapshot and nightly backup job"
git push -u origin feat/ops-2-storage
```
Open a PR titled `feat(platform): OPS-2 durable storage`. Fill in the template: Source "OPS-2 (#11);
BASE-03, OPS-01 storage part", the local `make check` result and the Actions result. Reviewer @lisim.
The author squash-merges after approval.

---

## Handoff (integration, not this plan)

Storage is **module accepted** when this plan's tests pass. It is **integrated** only when:
- **OPS-1** opens `/config/lisimflix.db` at startup, calls `MigrateWithBackup` with every registered
  `Source` and `SnapshotOptions{Dir: "/config/backups", AppVersion: version, ExtraFiles: ["/config/m1.json"]}`,
  and reports not-ready on any error, naming `ErrSchemaTooNew` with the recovery instruction. It
  starts `Nightly.Run` (zero-value fields take their defaults). The deployment docs state the
  container `TZ` used for the nightly hour.
- **Restore** runs as an owner action while serving is stopped, with
  `ExtraFiles: ["/config/m1.json"]` so the pre-restore snapshot keeps the current config. The admin
  UI (UI-6) adds re-authentication and confirmation, and sessions are revoked afterwards.
- **OPS-01** evidence: fresh setup, a nightly snapshot, a clean-instance restore, a forced migration
  failure blocking readiness, and the documented rollback (old image + `pre-upgrade` snapshot).
- **Backup copies off the host** (spec §11.3) are the owner's job: document copying `/config/backups`
  to a second location, and encryption before secrets-bearing snapshots leave the host (D12).
- **Artwork is not included** in snapshots. It is rebuildable from providers and local files, and the
  restore runbook must say so (spec §11.3).
- **Not in this plan:** M1 config ID preservation (ACC-1 imports `/config/m1.json` into its tables),
  MIGRATE-01 compatibility fixtures (DATA-1/CTR-2), backup health in diagnostics (OPS-5), and the
  restore duration measurement (PERF-07).
