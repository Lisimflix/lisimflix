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
