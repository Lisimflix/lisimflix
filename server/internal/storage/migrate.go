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
