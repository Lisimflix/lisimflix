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
