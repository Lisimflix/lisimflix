package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// Scan run statuses and failure reasons.
const (
	RunRunning     = "running"
	RunCompleted   = "completed"
	RunFailed      = "failed"
	RunCancelled   = "cancelled"
	RunInterrupted = "interrupted"

	ReasonRootUnavailable   = "root_unavailable"   // the folder can't be opened
	ReasonRootEmpty         = "root_empty"         // empty, while the catalogue has files there
	ReasonEnumerationFailed = "enumeration_failed" // the walk stopped part-way
	ReasonDatabaseError     = "database_error"     // the catalogue couldn't be written
)

// Run is the outcome of one scan of one root.
type Run struct {
	ID, Status, Reason                                string
	Scope, Cause                                      string // "." for the whole root
	Seen, Added, Changed, Returned, Missing, Unplaced int
}

// Scanner reconciles the catalogue with the files under each root.
type Scanner struct {
	Store *Store
	Media *os.Root // the media root; library roots are folders inside it
	// Discover walks a root; nil means the package's Discover. Tests replace it.
	Discover func(context.Context, *os.Root, func(File) error) error
}

// batchSize bounds each write transaction, so a large scan never holds the
// database's single writer for long (spec §10.2).
const batchSize = 500

// Scan causes, recorded on each run.
const (
	CauseStartup  = "startup"  // reconciliation after the server starts
	CauseSchedule = "schedule" // the periodic full scan
	CauseWatch    = "watch"    // a file watcher event
	CauseRecheck  = "recheck"  // an offline or degraded root is reachable again
	CauseManual   = "manual"   // Scan now
	CauseAdded    = "added"    // a new library folder
	CauseImport   = "import"   // a Sonarr/Radarr import notification (LIB-4)
)

// ScanRoot walks one root and brings its files, items and versions up to
// date: Scan of the whole root, started by hand.
func (sc *Scanner) ScanRoot(ctx context.Context, rootID string) (Run, error) {
	return sc.Scan(ctx, rootID, ".", CauseManual)
}

// Scan reconciles scope, a folder or file inside the root ("." for the whole
// root). Files that weren't found are marked missing only after a complete
// walk of a reachable, non-empty root, and only inside scope (spec §3.6,
// SCAN-01); a failed or cancelled scan keeps the work it did and marks
// nothing missing. A scope that no longer exists, inside a folder that does,
// has its files marked missing without a walk. Only a
// whole-root scan changes the root's state, except that an unreachable or
// empty root is offline whatever the scope. The error is for database
// failures and invalid scopes; a database failure after the run starts also
// ends it as failed with ReasonDatabaseError (#108). Any other failed scan
// is reported in Run alone.
//
// The caller runs at most one scan of a root at a time: two overlapping scans
// could each mark missing the files the other saw last (#108). The
// scheduler runs one scan at a time.
func (sc *Scanner) Scan(ctx context.Context, rootID, scope, cause string) (Run, error) {
	scope = path.Clean(scope)
	if !fs.ValidPath(scope) || skippedPath(scope) {
		return Run{}, fmt.Errorf("%w: scope %q", ErrInvalid, scope)
	}
	st := sc.Store
	root, libType, err := st.root(ctx, rootID)
	if err != nil {
		return Run{}, err
	}
	// The run's bookkeeping must be written even if ctx was cancelled.
	bg := context.WithoutCancel(ctx)

	dir, err := sc.Media.OpenRoot(root.Path)
	if err == nil {
		var info os.FileInfo
		if info, err = dir.Stat("."); err == nil && !info.IsDir() {
			err = fmt.Errorf("library: %s is not a folder", root.Path)
		}
		if err != nil {
			dir.Close()
		}
	}
	var target fs.FileInfo
	if err != nil {
		err = errRootGone
	} else {
		defer dir.Close()
		scope, target, err = resolve(dir, scope)
	}
	run, rerr := st.startRun(ctx, root, scope, cause)
	full := scope == "."
	// From here on a database error ends the run as failed, best effort,
	// instead of leaving it running until the next restart (#108). Like any
	// failure, it changes the root's state only for a whole-root scan.
	dbFailed := func(err error) (Run, error) {
		run, _ = sc.finish(bg, root, run, full, RunFailed, ReasonDatabaseError, RootDegraded)
		return run, err
	}
	if rerr != nil {
		if run.ID == "" { // the run wasn't recorded
			return Run{}, rerr
		}
		return dbFailed(rerr)
	}
	if err != nil {
		if errors.Is(err, errRootGone) {
			return sc.finish(bg, root, run, true, RunFailed, ReasonRootUnavailable, RootOffline)
		}
		return sc.finish(bg, root, run, full, RunFailed, ReasonEnumerationFailed, RootDegraded)
	}

	var batch []File
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := st.applyBatch(bg, root, libType, run.ID, batch, &run)
		batch = batch[:0]
		if err != nil {
			return err
		}
		// Live counters, so a first scan shows its progress (UI-2).
		_, err = st.db.ExecContext(bg, `UPDATE library_scan_runs SET seen = ?, added = ?, changed = ?, returned = ?, unplaced = ?
			WHERE id = ?`, run.Seen, run.Added, run.Changed, run.Returned, run.Unplaced, run.ID)
		return err
	}
	// A flush error stops the walk too; it is kept apart so it isn't
	// reported as a walk failure.
	var flushErr error
	add := func(f File) error {
		if batch = append(batch, f); len(batch) == batchSize {
			flushErr = flush()
			return flushErr
		}
		return nil
	}
	var walkErr error
	switch {
	case target == nil:
		// Gone: nothing to walk; its files are marked missing below.
	case target.IsDir():
		discover := sc.Discover
		if discover == nil {
			discover = Discover
		}
		sub := dir
		if !full {
			if sub, walkErr = dir.OpenRoot(scope); walkErr == nil {
				defer sub.Close()
			}
		}
		if walkErr == nil {
			walkErr = discover(ctx, sub, func(f File) error {
				if !full {
					f.Path = scope + "/" + f.Path
				}
				return add(f)
			})
		}
	case target.Mode().IsRegular() && isVideo(path.Base(scope)):
		walkErr = add(File{Path: scope, Rev: RevisionOf(target)})
	}
	if flushErr == nil {
		flushErr = flush()
	}
	if flushErr != nil {
		return dbFailed(flushErr)
	}
	switch {
	case ctx.Err() != nil:
		return sc.finish(bg, root, run, full, RunCancelled, "", root.State)
	case walkErr != nil:
		return sc.finish(bg, root, run, full, RunFailed, ReasonEnumerationFailed, RootDegraded)
	}
	if run.Seen == 0 {
		// An empty folder where files were is far more likely an unmounted
		// disk than a deleted library. Checked for scoped scans too: a
		// scope can look empty only because its root is.
		empty, err := rootEmpty(dir)
		if err != nil {
			return sc.finish(bg, root, run, full, RunFailed, ReasonEnumerationFailed, RootDegraded)
		}
		if empty {
			var present int
			if err := st.db.QueryRowContext(bg, `SELECT COUNT(*) FROM library_files WHERE root_id = ? AND state = 'present'`, rootID).Scan(&present); err != nil {
				return dbFailed(err)
			}
			if present > 0 {
				return sc.finish(bg, root, run, true, RunFailed, ReasonRootEmpty, RootOffline)
			}
		}
	}
	// One statement, by index, not batched by 500 files: an exception to
	// spec §10.2's bound, to measure in LIB-2c's scale run (#110).
	q := `UPDATE library_files SET state = 'missing', missing_since = COALESCE(missing_since, ?)
		WHERE root_id = ? AND state = 'present' AND last_seen_run IS NOT ?`
	args := []any{ms(st.now()), rootID, run.ID}
	if !full {
		// The scope itself, or anything under it ('0' sorts right after '/').
		q += ` AND (path = ? OR (path >= ? AND path < ?))`
		args = append(args, scope, scope+"/", scope+"0")
	}
	res, err := st.db.ExecContext(bg, q, args...)
	if err != nil {
		return dbFailed(err)
	}
	n, _ := res.RowsAffected()
	run.Missing = int(n)
	if err := st.settle(bg, rootID, full); err != nil {
		return dbFailed(err)
	}
	return sc.finish(bg, root, run, full, RunCompleted, "", RootAvailable)
}

var errRootGone = errors.New("library: root unavailable")

// resolve returns scope and its info if it exists. If it doesn't, it returns
// the highest gone folder on scope's path whose parent does exist, with nil
// info: everything under it is gone, and the existing parent proves the
// root is still there. errRootGone means the root itself can't be read.
func resolve(dir *os.Root, scope string) (string, fs.FileInfo, error) {
	info, err := dir.Stat(scope)
	switch {
	case err == nil:
		return scope, info, nil
	case scope == ".":
		return scope, nil, errRootGone
	case !errors.Is(err, fs.ErrNotExist):
		return scope, nil, err
	}
	gone := scope
	for p := path.Dir(scope); ; p = path.Dir(p) {
		_, err := dir.Stat(p)
		switch {
		case err == nil:
			return gone, nil, nil
		case p == ".":
			return p, nil, errRootGone
		case !errors.Is(err, fs.ErrNotExist):
			return p, nil, err
		}
		gone = p
	}
}

// rootEmpty reports whether the root folder has no entries at all.
func rootEmpty(dir *os.Root) (bool, error) {
	f, err := dir.Open(".")
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return len(names) == 0, err
}

// skippedPath reports whether Discover would never reach p: one of its
// folders is skipped (hidden, recycle bins, samples).
func skippedPath(p string) bool {
	if p == "." {
		return false
	}
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if skipDir(d) {
			return true
		}
	}
	return strings.HasPrefix(parts[len(parts)-1], ".")
}

// startRun records a running scan. A whole-root scan marks the root scanning.
func (s *Store) startRun(ctx context.Context, root Root, scope, cause string) (Run, error) {
	run := Run{ID: newID("scan"), Status: RunRunning, Scope: scope, Cause: cause}
	var sc any
	if scope != "." {
		sc = scope
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO library_scan_runs(id, root_id, started_at, status, scope, cause) VALUES(?, ?, ?, 'running', ?, ?)`,
		run.ID, root.ID, ms(s.now()), sc, cause); err != nil {
		return Run{}, err
	}
	if scope == "." {
		return run, s.setRoot(ctx, root.ID, RootScanning, root.Reason, false)
	}
	return run, nil
}

// finish records the run's outcome and the root's resulting state.
// The root's state is set only when setRoot is true: whole-root scans, and
// any scan that found the root unreachable or empty.
func (sc *Scanner) finish(ctx context.Context, root Root, run Run, setRoot bool, status, reason, rootState string) (Run, error) {
	run.Status, run.Reason = status, reason
	_, err := sc.Store.db.ExecContext(ctx, `UPDATE library_scan_runs SET status = ?, reason = NULLIF(?, ''), finished_at = ?,
		seen = ?, added = ?, changed = ?, returned = ?, missing = ?, unplaced = ? WHERE id = ?`,
		status, reason, ms(sc.Store.now()), run.Seen, run.Added, run.Changed, run.Returned, run.Missing, run.Unplaced, run.ID)
	if err != nil {
		return run, err
	}
	if !setRoot {
		return run, nil
	}
	if status == RunCancelled {
		reason = root.Reason
	}
	return run, sc.Store.setRoot(ctx, root.ID, rootState, reason, status == RunCompleted)
}

func (s *Store) setRoot(ctx context.Context, id, state, reason string, ok bool) error {
	q := `UPDATE library_roots SET state = ?, reason = NULLIF(?, '') WHERE id = ?`
	args := []any{state, reason, id}
	if ok {
		q = `UPDATE library_roots SET state = ?, reason = NULLIF(?, ''), last_ok_at = ? WHERE id = ?`
		args = []any{state, reason, ms(s.now()), id}
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// applyBatch records one batch of discovered files in a single transaction:
// new files are added and placed, changed revisions replace the old ones
// (making the probe stale), and missing files that are back become present.
// The batch's counts are added to run only once it has committed (#108).
func (s *Store) applyBatch(ctx context.Context, root Root, libType, runID string, files []File, run *Run) error {
	if len(files) == 0 {
		return nil
	}
	var n Run
	if err := s.applyFiles(ctx, root, libType, runID, files, &n); err != nil {
		return err
	}
	run.Seen += n.Seen
	run.Added += n.Added
	run.Changed += n.Changed
	run.Returned += n.Returned
	run.Unplaced += n.Unplaced
	return nil
}

func (s *Store) applyFiles(ctx context.Context, root Root, libType, runID string, files []File, run *Run) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range files {
		run.Seen++
		var id, state string
		var size, mtime int64
		var unplaced sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, size, mtime_ns, state, unplaced FROM library_files WHERE root_id = ? AND path = ?`,
			root.ID, f.Path).Scan(&id, &size, &mtime, &state, &unplaced)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id = newID("file")
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_files(id, root_id, path, size, mtime_ns, last_seen_run) VALUES(?, ?, ?, ?, ?, ?)`,
				id, root.ID, f.Path, f.Rev.Size, f.Rev.ModTime, runID); err != nil {
				return err
			}
			run.Added++
		case err != nil:
			return err
		default:
			if size != f.Rev.Size || mtime != f.Rev.ModTime {
				run.Changed++
			}
			if state != "present" {
				run.Returned++
			}
			if _, err := tx.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, state = 'present', missing_since = NULL,
				last_seen_run = ? WHERE id = ?`, f.Rev.Size, f.Rev.ModTime, runID, id); err != nil {
				return err
			}
			if !unplaced.Valid {
				continue // already placed
			}
		}
		if err := s.place(ctx, tx, root, libType, id, f, run); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// place attaches a file to its items (creating them as needed), or records
// why naming couldn't place it.
func (s *Store) place(ctx context.Context, tx *sql.Tx, root Root, libType, fileID string, f File, run *Run) error {
	libID := root.LibraryID
	e, err := naming.Classify(libType, f.Path, f.Rev.Size)
	if err != nil {
		run.Unplaced++
		reason := "unrecognised"
		switch {
		case errors.Is(err, naming.ErrExtra):
			reason = "extra"
		case errors.Is(err, naming.ErrSample):
			reason = "sample"
		}
		_, err := tx.ExecContext(ctx, `UPDATE library_files SET unplaced = ? WHERE id = ?`, reason, fileID)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_files SET unplaced = NULL WHERE id = ?`, fileID); err != nil {
		return err
	}
	var year any
	if e.Year != 0 {
		year = e.Year
	}
	if e.Kind == naming.Movie {
		movie, _, err := s.groupItem(ctx, tx, root, e, item{kind: "movie", title: e.Title, year: year})
		if err != nil {
			return err
		}
		if err := s.hints(ctx, tx, movie, e.IDs); err != nil {
			return err
		}
		return s.version(ctx, tx, movie, fileID, e.Version)
	}
	show, showKey, err := s.groupItem(ctx, tx, root, e, item{kind: "show", title: e.Title, year: year})
	if err != nil {
		return err
	}
	if err := s.hints(ctx, tx, show, e.IDs); err != nil {
		return err
	}
	seasonTitle := "Season " + strconv.Itoa(e.Season)
	if e.Season == 0 {
		seasonTitle = "Specials"
	}
	seasonKey := showKey + "|s" + strconv.Itoa(e.Season)
	season, err := s.item(ctx, tx, libID, item{kind: "season", key: seasonKey, title: seasonTitle, parent: show, idx: e.Season})
	if err != nil {
		return err
	}
	type ep struct {
		key, title string
		idx, abs   any
		airDate    any
	}
	var eps []ep
	if e.AirDate != "" {
		eps = []ep{{key: seasonKey + "|d" + e.AirDate, title: firstNonEmpty(e.EpisodeTitle, e.AirDate), airDate: e.AirDate}}
	}
	for _, n := range e.Episodes {
		x := ep{key: seasonKey + "|e" + strconv.Itoa(n), title: "Episode " + strconv.Itoa(n), idx: n}
		if len(e.Episodes) == 1 {
			x.title = firstNonEmpty(e.EpisodeTitle, x.title)
			if e.Absolute != nil {
				x.abs = *e.Absolute
			}
		}
		eps = append(eps, x)
	}
	for _, x := range eps {
		id, err := s.item(ctx, tx, libID, item{kind: "episode", key: x.key, title: x.title, parent: season,
			idx: x.idx, absolute: x.abs, airDate: x.airDate})
		if err != nil {
			return err
		}
		// Every episode in a multi-episode file gets a version on the same
		// file (CTR-2: same fileId, unknown segment).
		if err := s.version(ctx, tx, id, fileID, e.Version); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// idPrefix makes item IDs readable in logs; clients treat them as opaque.
var idPrefix = map[string]string{"movie": "mov", "show": "shw", "season": "sea", "episode": "epi"}

type item struct {
	kind, key, title             string
	parent                       any // item ID or nil
	year, idx, absolute, airDate any
}

// item returns the ID of the library's item with it.key, creating it if
// needed. An existing item keeps its title: later edits (META-1) win.
func (s *Store) item(ctx context.Context, tx *sql.Tx, libID string, it item) (string, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_items(id, library_id, kind, parent_id, scan_key, title, sort_title, year, idx, air_date, absolute, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(library_id, scan_key) DO NOTHING`,
		newID(idPrefix[it.kind]), libID, it.kind, it.parent, it.key, it.title, naming.SortTitle(it.title), it.year, it.idx, it.airDate, it.absolute, ms(s.now())); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM library_items WHERE library_id = ? AND scan_key = ?`, libID, it.key).Scan(&id)
	return id, err
}

func (s *Store) version(ctx context.Context, tx *sql.Tx, itemID, fileID, label string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO library_versions(id, item_id, file_id, label) VALUES(?, ?, ?, NULLIF(?, ''))
		ON CONFLICT(item_id, file_id) DO NOTHING`, newID("ver"), itemID, fileID, label)
	return err
}

func (s *Store) hints(ctx context.Context, tx *sql.Tx, itemID string, ids map[string]string) error {
	for ns, v := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_id_hints(item_id, namespace, value) VALUES(?, ?, ?) ON CONFLICT DO NOTHING`,
			itemID, ns, v); err != nil {
			return err
		}
	}
	return nil
}
