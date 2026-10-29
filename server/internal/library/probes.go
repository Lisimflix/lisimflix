package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// ProbeRun counts what one ProbeDue call did.
type ProbeRun struct {
	Queued   int // files due for a probe
	Stored   int // results saved
	Stale    int // results dropped: a scan saw a newer revision meanwhile
	Deferred int // still changing: retried after the quiet period
	TimedOut int // ffprobe timeouts, retried later with backoff (#109)
	Failed   int // probe errors, kept for the admin list (spec §3.1)
	Gone     int // vanished; the next scan marks them missing
}

// maxProbeError bounds the stored error text.
const maxProbeError = 500

// timeoutRetries are the waits before probing again after the 1st, 2nd, …
// ffprobe timeout in a row: a disk spinning up or a busy NAS usually
// recovers. The next timeout after the last wait is kept as a failure (#109).
var timeoutRetries = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// ProbeDue probes the root's present files whose probe doesn't match their
// current revision, using LIB-1's queue with workers at a time. Unchanged
// files are never probed again (SCALE-01). A result is saved only if the
// catalogue holds the revision it describes, or an older one (compare-and-set
// on size and mtime; see resultFits). A file that is still changing gets a
// not-before time instead of being re-queued at once (#62). workers <= 0
// means DefaultWorkers. limit bounds the files taken in one call (0: all),
// oldest change first, so a large backlog is worked through in turns with
// other roots.
func (sc *Scanner) ProbeDue(ctx context.Context, rootID string, p Prober, workers int, quiet time.Duration, limit int) (ProbeRun, error) {
	st := sc.Store
	root, _, err := st.root(ctx, rootID)
	if err != nil {
		return ProbeRun{}, err
	}
	paths, err := queryStrings(ctx, st.db, `SELECT path FROM library_files WHERE root_id = ? AND state = 'present'
		AND (probe_size IS NOT size OR probe_mtime_ns IS NOT mtime_ns)
		AND (probe_after IS NULL OR probe_after <= ?) ORDER BY mtime_ns, path LIMIT ?`, rootID, ms(st.now()), cmp.Or(limit, -1))
	if err != nil || len(paths) == 0 {
		return ProbeRun{}, err
	}
	dir, err := sc.Media.OpenRoot(root.Path)
	if err != nil {
		return ProbeRun{}, fmt.Errorf("library: root %s unavailable: %w", rootID, err)
	}
	defer dir.Close()

	if workers <= 0 {
		workers = DefaultWorkers
	}
	run := ProbeRun{Queued: len(paths)}
	var mu sync.Mutex
	var firstErr error
	left := len(paths)
	qctx, stop := context.WithCancel(ctx)
	defer stop()
	q := NewQueue(dir, p, quiet, func(o Outcome) {
		mu.Lock()
		defer mu.Unlock()
		if err := st.record(context.WithoutCancel(ctx), rootID, o, quiet, &run); err != nil && firstErr == nil {
			firstErr = err
		}
		if left--; left == 0 {
			stop()
		}
	})
	for _, p := range paths {
		q.Add(p)
	}
	q.Run(qctx, workers)
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return run, firstErr
}

// record saves one probe outcome. A result is for the catalogue's revision,
// or for a newer one (a file changed since the last scan and quiet now), which
// it records with the result so the file isn't probed again before the next
// scan (#109). A result older than the catalogue's revision is stale.
const resultFits = `root_id = ? AND path = ? AND ((size = ? AND mtime_ns = ?) OR mtime_ns < ?)`

// record saves one probe outcome.
func (s *Store) record(ctx context.Context, rootID string, o Outcome, quiet time.Duration, run *ProbeRun) error {
	switch {
	case o.Err == nil:
		js, err := json.Marshal(o.Probe)
		if err != nil {
			return err
		}
		res, err := s.db.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, probe_size = ?, probe_mtime_ns = ?, probe_json = ?,
			probe_error = NULL, probe_after = NULL, probe_timeouts = 0 WHERE `+resultFits,
			o.Rev.Size, o.Rev.ModTime, o.Rev.Size, o.Rev.ModTime, string(js), rootID, o.Path, o.Rev.Size, o.Rev.ModTime, o.Rev.ModTime)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			run.Stale++
			return nil
		}
		run.Stored++
	case errors.Is(o.Err, ErrNotStable), errors.Is(o.Err, ErrChanged):
		// Record the newer revision and wait until it has been quiet.
		after := time.Unix(0, o.Rev.ModTime).Add(quiet)
		// Only ever forward: a scan may already have recorded a newer revision.
		_, err := s.db.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, probe_after = ?
			WHERE root_id = ? AND path = ? AND mtime_ns <= ?`, o.Rev.Size, o.Rev.ModTime, ms(after), rootID, o.Path, o.Rev.ModTime)
		if err != nil {
			return err
		}
		run.Deferred++
	case errors.Is(o.Err, ErrGone):
		run.Gone++
	default:
		if errors.Is(o.Err, probe.ErrTimeout) {
			if done, err := s.retryTimeout(ctx, rootID, o, run); done || err != nil {
				return err
			}
		}
		msg := o.Err.Error()
		if len(msg) > maxProbeError {
			msg = msg[:maxProbeError]
		}
		res, err := s.db.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, probe_size = ?, probe_mtime_ns = ?, probe_json = NULL,
			probe_error = ?, probe_after = NULL, probe_timeouts = 0 WHERE `+resultFits,
			o.Rev.Size, o.Rev.ModTime, o.Rev.Size, o.Rev.ModTime, msg, rootID, o.Path, o.Rev.Size, o.Rev.ModTime, o.Rev.ModTime)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			run.Stale++
			return nil
		}
		run.Failed++
	}
	return nil
}

// retryTimeout defers a file whose probe timed out, unless it has run out of
// retries, and reports whether it dealt with the outcome. Like a stored
// result, it applies only to the catalogue's revision or a newer one.
func (s *Store) retryTimeout(ctx context.Context, rootID string, o Outcome, run *ProbeRun) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT probe_timeouts FROM library_files WHERE root_id = ? AND path = ?`, rootID, o.Path).Scan(&n)
	if err != nil || n >= len(timeoutRetries) {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE library_files SET size = ?, mtime_ns = ?, probe_timeouts = ?, probe_after = ?, probe_error = NULL
		WHERE `+resultFits, o.Rev.Size, o.Rev.ModTime, n+1, ms(s.now().Add(timeoutRetries[n])), rootID, o.Path, o.Rev.Size, o.Rev.ModTime, o.Rev.ModTime)
	if err != nil {
		return false, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		run.Stale++
	} else {
		run.TimedOut++
	}
	return true, nil
}

// RetryProbe clears a file's probe so the next ProbeDue probes it again: the
// admin's explicit retry after a failure (spec §3.6).
// Planned: LIB-2c (the admin retry action).
func (s *Store) RetryProbe(ctx context.Context, fileID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE library_files SET probe_size = NULL, probe_mtime_ns = NULL, probe_json = NULL,
		probe_error = NULL, probe_after = NULL, probe_timeouts = 0 WHERE id = ?`, fileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
