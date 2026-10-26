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
