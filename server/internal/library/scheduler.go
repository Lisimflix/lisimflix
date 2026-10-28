package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// SchedulerOptions tune the scheduler; zero values take the defaults.
type SchedulerOptions struct {
	FullScanEvery time.Duration // whole-root scan of every root; default 6h (spec §3.1), negative turns it off
	Debounce      time.Duration // a watched folder must be quiet this long before it is scanned; default 10s
	MaxDelay      time.Duration // a folder that keeps changing is still scanned this often; default 2m
	Recheck       time.Duration // offline roots and lost watches are checked this often; default 1m
	Quiet         time.Duration // LIB-1's quiet period before a probe; default DefaultQuiet
	Workers       int           // probes at once; default DefaultWorkers
	ProbeBatch    int           // files per probe turn, before another root gets a turn; default 200
	MaxScopes     int           // pending folders per root before one whole-root scan replaces them; default 64
	NoWatch       bool          // periodic scans only
}

func (o SchedulerOptions) withDefaults() SchedulerOptions {
	o.FullScanEvery = cmp.Or(o.FullScanEvery, 6*time.Hour)
	o.Debounce = cmp.Or(o.Debounce, 10*time.Second)
	o.MaxDelay = cmp.Or(o.MaxDelay, 2*time.Minute)
	o.Recheck = cmp.Or(o.Recheck, time.Minute)
	o.Quiet = cmp.Or(o.Quiet, DefaultQuiet)
	o.Workers = cmp.Or(o.Workers, DefaultWorkers)
	o.ProbeBatch = cmp.Or(o.ProbeBatch, 200)
	o.MaxScopes = cmp.Or(o.MaxScopes, 64)
	return o
}

// Scheduler runs every scan and probe through two lanes: one scan at a time
// across all roots (scans of one root must never overlap: a whole-root scan
// marks unseen files missing), and one probe turn at a time with Workers
// probes. Watcher hints, the periodic scan, rechecks and Scan now all feed
// the same coalescing queue (spec §3.6).
type Scheduler struct {
	sc     *Scanner
	prober Prober
	log    *slog.Logger
	opt    SchedulerOptions
	now    func() time.Time

	mu        sync.Mutex
	roots     map[string]*rootSched
	paused    bool
	scanning  *job
	probing   *job
	w         *watcher
	scanWake  chan struct{}
	probeWake chan struct{}
	probeNext string // round robin: the root after the last probe turn
}

type rootSched struct {
	Root
	abs      string            // absolute folder, for the watcher
	full     string            // cause of a pending whole-root scan; "" if none
	scopes   map[string]string // pending scoped scans → their cause
	first    time.Time         // first pending hint, for MaxDelay
	due      time.Time         // pending work may start then
	nextFull time.Time
	watch    string
	probe    bool      // probes may be due now
	probeAt  time.Time // a deferred probe becomes due then
}

func (r *rootSched) pending() bool { return r.full != "" || len(r.scopes) > 0 }

// job is a running scan or probe turn.
type job struct {
	root    string
	full    string            // whole-root scan cause
	scopes  []string          // or these scoped scans
	causes  map[string]string // each scope's cause
	cancel  context.CancelFunc
	requeue bool // paused: put the work back when it stops
}

// NewScheduler returns a scheduler over sc's store and media root. log may
// be nil.
func NewScheduler(sc *Scanner, p Prober, log *slog.Logger, opt SchedulerOptions) *Scheduler {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Scheduler{sc: sc, prober: p, log: log, opt: opt.withDefaults(), now: time.Now,
		roots: map[string]*rootSched{}, scanWake: make(chan struct{}, 1), probeWake: make(chan struct{}, 1)}
}

func wake(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Run ends runs a crash left running, queues a whole-root scan of every
// root (restart reconciliation, spec §3.6), starts the watcher and works
// until ctx ends. It returns once running scans and probes have stopped;
// cancelled work keeps what it finished.
func (s *Scheduler) Run(ctx context.Context) error {
	if n, err := s.sc.Store.MarkInterrupted(ctx); err != nil {
		return err
	} else if n > 0 {
		s.log.Info("library.recover", "interrupted_runs", n)
	}
	var wg sync.WaitGroup
	if !s.opt.NoWatch {
		w, err := newWatcher(s.hooks())
		if err != nil {
			s.log.Warn("library.watch", "state", WatchOff, "err", err.Error())
		} else {
			s.mu.Lock()
			s.w = w
			s.mu.Unlock()
			wg.Go(func() { w.run(ctx) })
		}
	}
	if err := s.reload(ctx, CauseStartup); err != nil {
		return err
	}
	wg.Go(func() { s.probeLoop(ctx) })
	s.scanLoop(ctx)
	wg.Wait()
	return nil
}

// Reload re-reads the libraries' roots: new ones are watched and scanned,
// removed ones dropped. Run calls it every Recheck.
// Planned: LIB-2c (the libraries API calls it after a change).
func (s *Scheduler) Reload(ctx context.Context) error { return s.reload(ctx, CauseAdded) }

func (s *Scheduler) reload(ctx context.Context, cause string) error {
	roots, err := s.sc.Store.roots(ctx)
	if err != nil {
		return err
	}
	base, err := filepath.Abs(s.sc.Media.Name())
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r.ID] = true
		if rs, ok := s.roots[r.ID]; ok {
			rs.Root = r
			continue
		}
		s.roots[r.ID] = &rootSched{Root: r, abs: filepath.Join(base, filepath.FromSlash(r.Path)), full: cause, due: now,
			nextFull: now.Add(s.opt.FullScanEvery), watch: WatchOff, scopes: map[string]string{}}
	}
	for id := range s.roots {
		if seen[id] {
			continue
		}
		delete(s.roots, id)
		if s.w != nil {
			s.w.unwatch(id)
		}
		for _, j := range []*job{s.scanning, s.probing} {
			if j != nil && j.root == id {
				j.cancel()
			}
		}
	}
	wake(s.scanWake)
	return nil
}

// ScanNow queues a whole-root scan of every root of the library (spec §3.6
// Scan now). A scan already running finishes, and the new one follows it.
// Planned: LIB-2c (the Scan now action).
func (s *Scheduler) ScanNow(libraryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, rs := range s.roots {
		if rs.LibraryID == libraryID {
			s.queueFull(rs, CauseManual, s.now())
			n++
		}
	}
	if n == 0 {
		return ErrNotFound
	}
	wake(s.scanWake)
	return nil
}

// Cancel stops the library's running scan and probes and drops its queued
// work. Completed work is kept (spec §3.6).
// Planned: LIB-2c (the Cancel action).
func (s *Scheduler) Cancel(libraryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, rs := range s.roots {
		if rs.LibraryID != libraryID {
			continue
		}
		n++
		rs.full, rs.scopes, rs.first = "", map[string]string{}, time.Time{}
		rs.probe, rs.probeAt = false, time.Time{}
		for _, j := range []*job{s.scanning, s.probing} {
			if j != nil && j.root == rs.ID {
				j.cancel()
			}
		}
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Pause stops starting scans and probes, and stops running ones; their work
// is queued again for Resume (spec §11.2: operators can pause maintenance).
// Watcher hints keep queuing meanwhile.
// Planned: LIB-2c (the admin pause control).
func (s *Scheduler) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
	for _, j := range []*job{s.scanning, s.probing} {
		if j != nil {
			j.requeue = true
			j.cancel()
		}
	}
}

// Resume undoes Pause.
// Planned: LIB-2c (the admin pause control).
func (s *Scheduler) Resume() {
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	wake(s.scanWake)
	wake(s.probeWake)
}

// Status reports every root.
// Planned: LIB-2c (the scan status API).
func (s *Scheduler) Status(ctx context.Context) ([]RootStatus, error) {
	type mem struct{ activity, watch string }
	s.mu.Lock()
	m := map[string]mem{}
	for id, rs := range s.roots {
		a := ActivityIdle
		switch {
		case s.scanning != nil && s.scanning.root == id:
			a = ActivityScanning
		case s.paused && (rs.pending() || rs.probe):
			a = ActivityPaused
		case rs.pending():
			a = ActivityQueued
		case s.probing != nil && s.probing.root == id:
			a = ActivityProbing
		}
		m[id] = mem{a, rs.watch}
	}
	s.mu.Unlock()
	roots, err := s.sc.Store.roots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RootStatus, 0, len(roots))
	for _, r := range roots {
		st := RootStatus{Root: r, Activity: cmp.Or(m[r.ID].activity, ActivityQueued), Watch: cmp.Or(m[r.ID].watch, WatchOff)}
		if st.LastRun, err = s.sc.Store.lastRun(ctx, r.ID); err != nil {
			return nil, err
		}
		if st.Files, err = s.sc.Store.fileCounts(ctx, r.ID); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// queueFull queues a whole-root scan, which replaces pending scoped ones.
func (s *Scheduler) queueFull(rs *rootSched, cause string, due time.Time) {
	if rs.full == "" || cause == CauseManual {
		rs.full = cause
	}
	clear(rs.scopes)
	if rs.due.IsZero() || due.Before(rs.due) {
		rs.due = due
	}
}

func (s *Scheduler) hooks() watchHooks {
	return watchHooks{
		hint: func(rootID, scope string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if rs, ok := s.roots[rootID]; ok {
				s.queueScope(rs, scope, CauseWatch)
			}
		},
		lost: func(rootID string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if rs, ok := s.roots[rootID]; ok {
				rs.watch = WatchOff
				s.queueFull(rs, CauseRecheck, s.now())
				wake(s.scanWake)
			}
			s.log.Warn("library.watch", "root_id", rootID, "state", WatchOff, "reason", "root_moved")
		},
		limited: func(rootID string, err error) {
			s.mu.Lock()
			if rs, ok := s.roots[rootID]; ok {
				rs.watch = WatchLimited
			}
			s.mu.Unlock()
			s.log.Warn("library.watch", "root_id", rootID, "state", WatchLimited, "err", err.Error())
		},
		overflow: func() {
			s.mu.Lock()
			for _, rs := range s.roots {
				if rs.watch != WatchOff {
					s.queueFull(rs, CauseWatch, s.now())
				}
			}
			s.mu.Unlock()
			wake(s.scanWake)
			s.log.Warn("library.watch", "state", "overflow")
		},
		failed: func(err error) { s.log.Warn("library.watch", "state", "error", "err", err.Error()) },
	}
}

// Hint queues a scoped scan of scope (a folder or file inside the root) as
// a watcher event would, coalescing with pending work. Import notifications
// (LIB-4) use it with CauseImport.
// Planned: LIB-4 (the Sonarr/Radarr webhook queues imported paths).
func (s *Scheduler) Hint(rootID, scope, cause string) error {
	scope = path.Clean(scope)
	if !fs.ValidPath(scope) || skippedPath(scope) {
		return fmt.Errorf("%w: scope %q", ErrInvalid, scope)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, ok := s.roots[rootID]
	if !ok {
		return ErrNotFound
	}
	s.queueScope(rs, scope, cause)
	return nil
}

// queueScope adds a scoped scan after the debounce: a scope inside a pending
// one adds nothing, pending scopes inside it are replaced, and too many
// become one whole-root scan. A pending whole-root scan covers everything.
func (s *Scheduler) queueScope(rs *rootSched, scope, cause string) {
	if rs.full != "" {
		return
	}
	now := s.now()
	if rs.first.IsZero() {
		rs.first = now
	}
	rs.due = minTime(now.Add(s.opt.Debounce), rs.first.Add(s.opt.MaxDelay))
	for p := range rs.scopes {
		if within(scope, p) {
			return
		}
		if within(p, scope) {
			delete(rs.scopes, p)
		}
	}
	rs.scopes[scope] = cause
	if len(rs.scopes) > s.opt.MaxScopes {
		rs.full = cause
		clear(rs.scopes)
	}
	wake(s.scanWake)
}

// within reports whether p is q or inside it.
func within(p, q string) bool {
	return q == "." || p == q || strings.HasPrefix(p, q+"/")
}

// idleTimer fires after wait, or never when wait is 0 (nothing scheduled).
func idleTimer(wait time.Duration) *time.Timer {
	if wait <= 0 {
		t := time.NewTimer(time.Hour)
		t.Stop()
		return t
	}
	return time.NewTimer(wait)
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func (s *Scheduler) scanLoop(ctx context.Context) {
	tick := time.NewTicker(s.opt.Recheck)
	defer tick.Stop()
	for {
		j, wait := s.nextScan()
		if j != nil {
			s.execute(ctx, j)
			continue
		}
		t := idleTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.scanWake:
		case <-t.C:
		case <-tick.C:
			s.recheck(ctx)
		}
		t.Stop()
	}
}

// nextScan takes the root whose pending work is due first, or says how long
// until some is.
func (s *Scheduler) nextScan() (*job, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused || s.scanning != nil {
		return nil, 0
	}
	now := s.now()
	var best *rootSched
	wait := time.Duration(0)
	for _, rs := range s.roots {
		if s.opt.FullScanEvery > 0 && !now.Before(rs.nextFull) && rs.full == "" {
			s.queueFull(rs, CauseSchedule, now)
		}
		if s.opt.FullScanEvery > 0 {
			if d := rs.nextFull.Sub(now); wait == 0 || d < wait {
				wait = d
			}
		}
		if !rs.pending() {
			continue
		}
		if rs.due.After(now) {
			if d := rs.due.Sub(now); wait == 0 || d < wait {
				wait = d
			}
			continue
		}
		if best == nil || rs.due.Before(best.due) {
			best = rs
		}
	}
	if best == nil {
		return nil, wait
	}
	j := &job{root: best.ID, full: best.full, scopes: slices.Sorted(maps.Keys(best.scopes)), causes: maps.Clone(best.scopes)}
	if j.full != "" {
		j.scopes = nil
		best.nextFull = now.Add(s.opt.FullScanEvery)
	}
	best.full, best.first, best.due = "", time.Time{}, time.Time{}
	clear(best.scopes)
	s.scanning = j
	return j, 0
}

// execute runs one job's scans.
func (s *Scheduler) execute(ctx context.Context, j *job) {
	jctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	j.cancel = cancel
	rs := s.roots[j.root]
	w := s.w
	var abs, watch string
	if rs != nil {
		abs, watch = rs.abs, rs.watch
	}
	s.mu.Unlock()

	scopes := j.scopes
	if j.full != "" {
		scopes = []string{"."}
		// Watch before the walk, so a change during the scan is hinted.
		if w != nil && watch != WatchOn && rs != nil && s.sc.reachable(rs.Path) {
			st, err := w.watch(j.root, abs)
			s.mu.Lock()
			rs.watch = st
			s.mu.Unlock()
			if err != nil {
				s.log.Warn("library.watch", "root_id", j.root, "state", st, "err", err.Error())
			}
		}
	}
	for i, scope := range scopes {
		if jctx.Err() != nil {
			s.finishJob(j, scopes[i:])
			return
		}
		cause := cmp.Or(j.full, j.causes[scope])
		start := time.Now()
		run, err := s.sc.Scan(jctx, j.root, scope, cause)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				s.log.Error("library.scan", "root_id", j.root, "scope", scope, "cause", cause, "err", err.Error())
			}
			continue
		}
		s.logRun(j.root, run, time.Since(start))
		s.after(ctx, j.root, run)
	}
	s.finishJob(j, nil)
}

// finishJob clears the running job; paused work goes back on the queue.
func (s *Scheduler) finishJob(j *job, left []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanning = nil
	rs := s.roots[j.root]
	if rs == nil || !j.requeue {
		return
	}
	if j.full != "" {
		s.queueFull(rs, j.full, s.now())
		return
	}
	if rs.full == "" {
		for _, p := range left {
			rs.scopes[p] = j.causes[p]
		}
		rs.due = s.now()
	}
}

// after updates the root's queue from a finished run.
func (s *Scheduler) after(ctx context.Context, rootID string, run Run) {
	bg := context.WithoutCancel(ctx)
	if err := s.sc.Store.pruneRuns(bg, rootID); err != nil {
		s.log.Error("library.scan", "root_id", rootID, "err", err.Error())
	}
	root, _, err := s.sc.Store.root(bg, rootID)
	s.mu.Lock()
	defer s.mu.Unlock()
	rs := s.roots[rootID]
	if rs == nil || err != nil {
		return
	}
	rs.Root = root
	switch {
	case run.Status == RunCompleted:
		rs.probe = true
		wake(s.probeWake)
		// A scoped scan worked, but the last whole-root one didn't: the
		// root is back, so reconcile all of it.
		if run.Scope != "." && root.State != RootAvailable {
			s.queueFull(rs, CauseRecheck, s.now())
		}
	case run.Reason == ReasonRootUnavailable || run.Reason == ReasonRootEmpty:
		if s.w != nil {
			s.w.unwatch(rootID)
		}
		rs.watch = WatchOff
	}
}

// recheck re-reads the roots, then queues a whole-root scan for an offline
// root that is reachable again, and for a root whose watch was dropped
// (an unmount drops it without an event; spec §3.6 remount reconciliation).
func (s *Scheduler) recheck(ctx context.Context) {
	if err := s.reload(ctx, CauseAdded); err != nil {
		s.log.Error("library.recheck", "err", err.Error())
		return
	}
	s.mu.Lock()
	w := s.w
	s.mu.Unlock()
	var watching map[string]bool
	if w != nil {
		watching = w.watching()
	}
	s.mu.Lock()
	type cand struct {
		rs   *rootSched
		lost bool
	}
	var check []cand
	for id, rs := range s.roots {
		if rs.pending() || s.scanning != nil && s.scanning.root == id {
			continue
		}
		if w != nil && rs.watch == WatchOn && !watching[id] {
			rs.watch = WatchOff
		}
		switch {
		case rs.State == RootOffline:
			check = append(check, cand{rs, false})
		case w != nil && rs.watch == WatchOff:
			check = append(check, cand{rs, true})
		}
	}
	s.mu.Unlock()
	for _, c := range check {
		// An offline root is scanned only once it can be read again, so an
		// outage doesn't add a failed run every minute. A lost watch is
		// scanned either way: it records the outage, or re-arms and catches
		// up on what the watch missed.
		if c.lost || s.sc.reachable(c.rs.Path) {
			s.mu.Lock()
			s.queueFull(c.rs, CauseRecheck, s.now())
			s.mu.Unlock()
			wake(s.scanWake)
		}
	}
}

func (s *Scheduler) probeLoop(ctx context.Context) {
	for {
		id, wait := s.nextProbe()
		if id == "" {
			t := idleTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-s.probeWake:
			case <-t.C:
			}
			t.Stop()
			continue
		}
		s.probeTurn(ctx, id)
	}
}

// nextProbe picks the next root with probes due, round robin.
func (s *Scheduler) nextProbe() (string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused || s.probing != nil {
		return "", 0
	}
	now := s.now()
	ids := slices.Sorted(maps.Keys(s.roots))
	start, _ := slices.BinarySearch(ids, s.probeNext)
	var wait time.Duration
	for i := range ids {
		id := ids[(start+i)%len(ids)]
		rs := s.roots[id]
		if !rs.probeAt.IsZero() && !now.Before(rs.probeAt) {
			rs.probe, rs.probeAt = true, time.Time{}
		}
		if !rs.probeAt.IsZero() {
			if d := rs.probeAt.Sub(now); wait == 0 || d < wait {
				wait = d
			}
		}
		if rs.probe && rs.State != RootOffline {
			rs.probe = false
			s.probing = &job{root: id}
			s.probeNext = id + "\x00" // the next ID after this one
			return id, 0
		}
	}
	return "", wait
}

func (s *Scheduler) probeTurn(ctx context.Context, id string) {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	j := s.probing
	j.cancel = cancel
	s.mu.Unlock()
	start := time.Now()
	run, err := s.sc.ProbeDue(pctx, id, s.prober, s.opt.Workers, s.opt.Quiet, s.opt.ProbeBatch)
	if run.Queued > 0 {
		s.log.Info("library.probe", "root_id", id, "queued", run.Queued, "stored", run.Stored, "stale", run.Stale,
			"deferred", run.Deferred, "failed", run.Failed, "gone", run.Gone, "duration_ms", time.Since(start).Milliseconds())
	}
	if err != nil && pctx.Err() == nil {
		s.log.Warn("library.probe", "root_id", id, "err", err.Error())
	}
	at, ok, aerr := s.sc.Store.nextProbeAt(context.WithoutCancel(ctx), id)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probing = nil
	rs := s.roots[id]
	if rs == nil {
		return
	}
	// More files than one batch, or stopped by Pause: another turn later.
	if run.Queued == s.opt.ProbeBatch && err == nil || j.requeue {
		rs.probe = true
	}
	if ok && aerr == nil {
		rs.probeAt = at
	}
}

func (s *Scheduler) logRun(rootID string, run Run, d time.Duration) {
	level := slog.LevelInfo
	if run.Status == RunFailed {
		level = slog.LevelWarn
	}
	s.log.Log(context.Background(), level, "library.scan", "job_id", run.ID, "root_id", rootID, "scope", run.Scope,
		"cause", run.Cause, "status", run.Status, "reason", run.Reason, "duration_ms", d.Milliseconds(),
		"seen", run.Seen, "added", run.Added, "changed", run.Changed, "returned", run.Returned,
		"missing", run.Missing, "unplaced", run.Unplaced)
}

// reachable reports whether a root can be read and has entries: the cheap
// check before rescanning an offline root.
func (sc *Scanner) reachable(rootPath string) bool {
	dir, err := sc.Media.OpenRoot(rootPath)
	if err != nil {
		return false
	}
	defer dir.Close()
	empty, err := rootEmpty(dir)
	return err == nil && !empty
}
