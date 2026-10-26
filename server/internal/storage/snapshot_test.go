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
