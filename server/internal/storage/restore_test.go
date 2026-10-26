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
