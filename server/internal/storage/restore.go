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
