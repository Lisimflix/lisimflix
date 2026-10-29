package library

import (
	"context"
	"database/sql"
	"time"
)

// RootStatus is one root's scan state for the admin status view (spec §3.6:
// queued, waiting for source, scanning, completed and failed, with reasons).
type RootStatus struct {
	Root              // State and Reason are the last whole-root outcome
	Activity string   // ActivityIdle, ActivityQueued, ActivityScanning, ActivityProbing or ActivityPaused
	Watch    string   // WatchOn, WatchLimited or WatchOff
	LastRun  *RunInfo // nil before the first scan
	Files    FileCounts
}

// Activities.
const (
	ActivityIdle     = "idle"
	ActivityQueued   = "queued"
	ActivityScanning = "scanning"
	ActivityProbing  = "probing"
	ActivityPaused   = "paused"
)

// RunInfo is a recorded scan run.
type RunInfo struct {
	Run
	Started, Finished time.Time // Finished is zero while running
}

// FileCounts counts the root's files by what they are waiting for.
type FileCounts struct {
	Present, Missing, Unplaced int
	ProbesDue                  int // present, probe out of date and may run now
	ProbesWaiting              int // still changing: waiting for the source to settle
	ProbeErrors                int // the current revision failed to probe
}

// roots lists every root with its library's type.
func (s *Store) roots(ctx context.Context) ([]Root, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM library_roots ORDER BY path`)
	if err != nil {
		return nil, err
	}
	ids, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	out := make([]Root, 0, len(ids))
	for _, id := range ids {
		r, _, err := s.root(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// lastRun returns the root's newest run, or nil.
func (s *Store) lastRun(ctx context.Context, rootID string) (*RunInfo, error) {
	var ri RunInfo
	var reason, scope sql.NullString
	var started int64
	var finished sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id, status, reason, scope, cause, started_at, finished_at,
		seen, added, changed, returned, missing, unplaced FROM library_scan_runs
		WHERE root_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1`, rootID).
		Scan(&ri.ID, &ri.Status, &reason, &scope, &ri.Cause, &started, &finished,
			&ri.Seen, &ri.Added, &ri.Changed, &ri.Returned, &ri.Missing, &ri.Unplaced)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ri.Reason, ri.Scope = reason.String, scope.String
	if !scope.Valid {
		ri.Scope = "."
	}
	ri.Started = time.UnixMilli(started)
	if finished.Valid {
		ri.Finished = time.UnixMilli(finished.Int64)
	}
	return &ri, nil
}

// fileCounts counts the root's files in one pass.
func (s *Store) fileCounts(ctx context.Context, rootID string) (FileCounts, error) {
	var c FileCounts
	now := ms(s.now())
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(state = 'present'), 0),
		COALESCE(SUM(state = 'missing'), 0),
		COALESCE(SUM(state = 'present' AND unplaced IS NOT NULL), 0),
		COALESCE(SUM(state = 'present' AND (probe_size IS NOT size OR probe_mtime_ns IS NOT mtime_ns)
			AND (probe_after IS NULL OR probe_after <= ?)), 0),
		COALESCE(SUM(state = 'present' AND (probe_size IS NOT size OR probe_mtime_ns IS NOT mtime_ns)
			AND probe_after > ?), 0),
		COALESCE(SUM(state = 'present' AND probe_error IS NOT NULL AND probe_size = size AND probe_mtime_ns = mtime_ns), 0)
		FROM library_files WHERE root_id = ?`, now, now, rootID).
		Scan(&c.Present, &c.Missing, &c.Unplaced, &c.ProbesDue, &c.ProbesWaiting, &c.ProbeErrors)
	return c, err
}

// nextProbeAt returns when the root's earliest deferred probe becomes due.
func (s *Store) nextProbeAt(ctx context.Context, rootID string) (time.Time, bool, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(probe_after) FROM library_files WHERE root_id = ? AND state = 'present'
		AND (probe_size IS NOT size OR probe_mtime_ns IS NOT mtime_ns) AND probe_after > ?`, rootID, ms(s.now())).Scan(&at)
	if err != nil || !at.Valid {
		return time.Time{}, false, err
	}
	return time.UnixMilli(at.Int64), true, nil
}

// keepRuns is how many finished whole-root and scoped runs each root keeps,
// so a busy watcher can't grow the table without bound.
const keepRuns = 100

// pruneRuns deletes the root's oldest finished runs beyond keepRuns of each
// kind. Whole-root runs are counted apart, so scoped runs never push out
// the ones retirement relies on.
func (s *Store) pruneRuns(ctx context.Context, rootID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM library_scan_runs WHERE id IN (
		SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY scope IS NULL ORDER BY started_at DESC, rowid DESC) AS n
			FROM library_scan_runs WHERE root_id = ? AND status != 'running') WHERE n > ?)`, rootID, keepRuns)
	return err
}
