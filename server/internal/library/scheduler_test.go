package library

import (
	"context"
	"errors"
	"fmt"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fast options for tests; the clock for DB times stays the test clock.
var fastOpts = SchedulerOptions{Debounce: 30 * time.Millisecond, MaxDelay: 200 * time.Millisecond,
	Recheck: 50 * time.Millisecond, FullScanEvery: time.Hour}

type schedFixture struct {
	s    *Store
	m    *media
	sc   *Scanner
	sch  *Scheduler
	lib  Library
	root string
	stop func()
}

func startScheduler(t *testing.T, libType string, opts SchedulerOptions, p Prober, files ...string) *schedFixture {
	t.Helper()
	s, _ := newStore(t)
	s.now = time.Now // real time: the scheduler compares probe_after with it
	m := newMedia(t)
	dir := map[string]string{"movies": "Movies", "tv": "TV"}[libType]
	for _, f := range files {
		m.write(dir+"/"+f, 10)
	}
	lib, err := s.CreateLibrary(context.Background(), dir, libType, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	sc := m.scanner(s)
	sch := NewScheduler(sc, p, nil, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Run didn't return after cancel")
			}
		})
	}
	t.Cleanup(stop)
	return &schedFixture{s: s, m: m, sc: sc, sch: sch, lib: lib, root: lib.Roots[0].ID, stop: stop}
}

func (f *schedFixture) status(t *testing.T) RootStatus {
	t.Helper()
	st, err := f.sch.Status(context.Background())
	if err != nil || len(st) != 1 {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	return st[0]
}

func (f *schedFixture) runs(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *schedFixture) probed(t *testing.T, rel string) bool {
	t.Helper()
	var n int
	f.s.db.QueryRow(`SELECT COUNT(*) FROM library_files WHERE path = ? AND state = 'present' AND probe_json IS NOT NULL
		AND probe_size = size AND probe_mtime_ns = mtime_ns`, rel).Scan(&n)
	return n == 1
}

// Startup reconciles every root, probes what it found and watches the root;
// a later change is found through the watcher with a scoped scan.
func TestSchedulerStartupWatchAndProbe(t *testing.T) {
	f := startScheduler(t, "tv", fastOpts, &scriptedProber{}, "Show (2019)/Season 01/Show - S01E01.mkv")
	eventually(t, "startup scan and probe", func() bool { return f.probed(t, "Show (2019)/Season 01/Show - S01E01.mkv") })
	st := f.status(t)
	if st.LastRun == nil || st.LastRun.Cause != CauseStartup || st.LastRun.Status != RunCompleted || st.Watch != WatchOn {
		t.Fatalf("status after startup %+v %+v", st, st.LastRun)
	}

	f.m.write("TV/Show (2019)/Season 01/Show - S01E02.mkv", 10)
	eventually(t, "watched file probed", func() bool { return f.probed(t, "Show (2019)/Season 01/Show - S01E02.mkv") })
	if n := f.runs(t, `cause = 'watch' AND scope IS NOT NULL`); n == 0 {
		t.Fatal("no scoped watch run")
	}
	if n := f.runs(t, `scope IS NULL`); n != 1 {
		t.Fatalf("%d whole-root runs; a watched change must not walk the root", n)
	}

	os.RemoveAll(filepath.Join(f.m.dir, "TV/Show (2019)/Season 01"))
	eventually(t, "deleted season missing", func() bool {
		return f.s.fileStates(t, f.root)["Show (2019)/Season 01/Show - S01E01.mkv"] == "missing"
	})
	if st := f.status(t); st.Files.Missing != 2 || st.Files.Present != 0 {
		t.Fatalf("counts %+v", st.Files)
	}
}

// Hints coalesce: a scope inside another pending scope adds nothing, and too
// many scopes become one whole-root scan.
func TestSchedulerCoalescesHints(t *testing.T) {
	opts := fastOpts
	opts.NoWatch, opts.MaxScopes, opts.Debounce = true, 3, 300*time.Millisecond
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	h := f.sch.hooks()
	h.hint(f.root, "A (2001)/A (2001).mkv")
	h.hint(f.root, "A (2001)")
	h.hint(f.root, "A (2001)/A (2001).mkv")
	h.hint(f.root, "B (2002)")
	eventually(t, "scoped runs", func() bool { return f.runs(t, `cause = 'watch'`) == 2 })
	time.Sleep(400 * time.Millisecond)
	if n := f.runs(t, `cause = 'watch'`); n != 2 {
		t.Fatalf("%d watch runs, want 2 (A and B)", n)
	}
	if n := f.runs(t, `scope = 'A (2001)'`) + f.runs(t, `scope = 'B (2002)'`); n != 2 {
		t.Fatalf("scopes %d", n)
	}

	for _, p := range []string{"a", "b", "c", "d"} {
		h.hint(f.root, p)
	}
	eventually(t, "whole-root scan for many hints", func() bool { return f.runs(t, `cause = 'watch' AND scope IS NULL`) == 1 })
}

// SCAN-01 with the scheduler: an offline root is retried only once it can
// be read again, then reconciled; nothing goes missing meanwhile.
func TestSchedulerOfflineRootReturns(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	away := filepath.Join(f.m.dir, "away")
	os.Rename(filepath.Join(f.m.dir, "Movies"), away)
	if err := f.sch.ScanNow(f.lib.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.sch.ScanNow("lib-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ScanNow(unknown) = %v", err)
	}
	eventually(t, "root offline", func() bool { return f.status(t).State == RootOffline })
	time.Sleep(300 * time.Millisecond) // several rechecks
	failed := f.runs(t, `status = 'failed'`)
	if failed > 2 {
		t.Fatalf("%d failed runs while offline: rechecks must wait for the root", failed)
	}
	if got := f.s.fileStates(t, f.root)["A (2001)/A (2001).mkv"]; got != "present" {
		t.Fatalf("file is %s while offline", got)
	}
	os.Rename(away, filepath.Join(f.m.dir, "Movies"))
	eventually(t, "root back", func() bool {
		st := f.status(t)
		return st.State == RootAvailable && st.Watch == WatchOn
	})
	if n := f.runs(t, `cause = 'recheck' AND status = 'completed'`); n != 1 {
		t.Fatalf("%d recheck runs", n)
	}
}

// An unmount drops inotify watches without an event; the recheck notices,
// re-arms and rescans the root.
func TestSchedulerRearmsLostWatch(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "watching", func() bool { return f.status(t).Watch == WatchOn })
	f.sch.mu.Lock()
	w := f.sch.w
	f.sch.mu.Unlock()
	for _, p := range w.fw.WatchList() {
		w.fw.Remove(p) // as an unmount would
	}
	eventually(t, "recheck scan", func() bool { return f.runs(t, `cause = 'recheck' AND status = 'completed'`) == 1 })
	eventually(t, "re-armed", func() bool { return f.status(t).Watch == WatchOn })
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	eventually(t, "watched again", func() bool { return f.probed(t, "B (2002)/B (2002).mkv") })
}

// blockingProber holds every probe until released.
type blockingProber struct {
	started chan string
	release chan struct{}
}

func (b *blockingProber) Probe(ctx context.Context, r *os.Root, rel string) (probe.Result, error) {
	select {
	case b.started <- rel:
	default:
	}
	select {
	case <-b.release:
		return (&scriptedProber{}).Probe(ctx, r, rel)
	case <-ctx.Done():
		return probe.Result{}, ctx.Err()
	}
}

// Pause stops running probes and holds new work; Resume finishes it.
func TestSchedulerPauseAndResume(t *testing.T) {
	bp := &blockingProber{started: make(chan string, 10), release: make(chan struct{})}
	f := startScheduler(t, "movies", fastOpts, bp, "A (2001)/A (2001).mkv")
	<-bp.started
	f.sch.Pause()
	eventually(t, "probe stopped", func() bool {
		f.sch.mu.Lock()
		defer f.sch.mu.Unlock()
		return f.sch.probing == nil
	})
	if st := f.status(t); st.Activity != ActivityPaused || st.Files.ProbesDue != 1 {
		t.Fatalf("paused status %+v %+v", st.Activity, st.Files)
	}
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	time.Sleep(300 * time.Millisecond)
	if n := f.runs(t, `cause = 'watch'`); n != 0 {
		t.Fatalf("%d scans while paused", n)
	}
	close(bp.release)
	f.sch.Resume()
	eventually(t, "both probed", func() bool { return f.probed(t, "A (2001)/A (2001).mkv") && f.probed(t, "B (2002)/B (2002).mkv") })
}

// Shutdown stops a running scan promptly; it is recorded as cancelled.
func TestSchedulerShutdownCancelsScan(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	inWalk := make(chan struct{})
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		close(inWalk)
		<-ctx.Done()
		return ctx.Err()
	}
	sch := NewScheduler(sc, &scriptedProber{}, nil, fastOpts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	<-inWalk
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop")
	}
	var status string
	s.db.QueryRow(`SELECT status FROM library_scan_runs WHERE root_id = ?`, lib.Roots[0].ID).Scan(&status)
	if status != RunCancelled {
		t.Fatalf("run %s", status)
	}
}

// The periodic whole-root scan repeats.
func TestSchedulerPeriodicScan(t *testing.T) {
	opts := fastOpts
	opts.FullScanEvery, opts.NoWatch = 100*time.Millisecond, true
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "scheduled scans", func() bool { return f.runs(t, `cause = 'schedule' AND scope IS NULL`) >= 2 })
}

// A scoped scan that works on a root whose last whole-root scan didn't
// means the root is back: the whole root is reconciled.
func TestSchedulerScopedSuccessOnDegradedRootRechecks(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool {
		st := f.status(t)
		return st.Watch == WatchOn && st.LastRun != nil && st.LastRun.Status == RunCompleted
	})
	if _, err := f.s.db.Exec(`UPDATE library_roots SET state = 'degraded', reason = 'enumeration_failed'`); err != nil {
		t.Fatal(err)
	}
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	eventually(t, "recheck", func() bool { return f.runs(t, `cause = 'recheck' AND scope IS NULL AND status = 'completed'`) == 1 })
	if st := f.status(t); st.State != RootAvailable {
		t.Fatalf("root %s", st.State)
	}
}

// A folder that never stops changing (a file written in place) is still
// scanned every MaxDelay, not postponed forever by the debounce.
func TestSchedulerBusyFolderStillScanned(t *testing.T) {
	opts := fastOpts
	opts.NoWatch, opts.Debounce, opts.MaxDelay = true, 100*time.Millisecond, 150*time.Millisecond
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	h := f.sch.hooks()
	for end := time.Now().Add(700 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		h.hint(f.root, "A (2001)")
	}
	if n := f.runs(t, `cause = 'watch'`); n < 2 {
		t.Fatalf("%d scans of a busy folder in 700 ms, want at least 2 (MaxDelay 150 ms)", n)
	}
}

// A root over the inotify watch limit is reported, and relies on periodic
// scans instead of a recheck loop.
func TestSchedulerWatchLimitIsReported(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.status(t).LastRun != nil && f.status(t).LastRun.Status == RunCompleted })
	f.sch.hooks().limited(f.root, errors.New("no space left on device"))
	if st := f.status(t); st.Watch != WatchLimited {
		t.Fatalf("watch %s", st.Watch)
	}
	time.Sleep(300 * time.Millisecond) // several rechecks
	if n := f.runs(t, `cause = 'recheck'`); n != 0 {
		t.Fatalf("%d recheck scans for a limited watch", n)
	}
}

// Hint queues a scoped scan with the caller's cause, through the same
// coalescing queue as watcher events (LIB-4's import notifications).
func TestSchedulerHint(t *testing.T) {
	opts := fastOpts
	opts.NoWatch = true
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	if err := f.sch.Hint("root-nope", "A (2001)", CauseImport); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown root: %v", err)
	}
	for _, bad := range []string{"../x", "/abs", ".grab/x.mkv"} {
		if err := f.sch.Hint(f.root, bad, CauseImport); !errors.Is(err, ErrInvalid) {
			t.Fatalf("scope %q: %v", bad, err)
		}
	}
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	if err := f.sch.Hint(f.root, "B (2002)/B (2002).mkv", CauseImport); err != nil {
		t.Fatal(err)
	}
	f.sch.Hint(f.root, "B (2002)", CauseImport) // the folder replaces the file
	eventually(t, "import scan", func() bool {
		return f.runs(t, `cause = 'import' AND scope = 'B (2002)' AND status = 'completed'`) == 1
	})
	if n := f.runs(t, `cause = 'import'`); n != 1 {
		t.Fatalf("%d import runs, want 1", n)
	}
}

// #106: a scoped scan (an import or a watched change) runs before scheduled
// whole-root scans of other roots, and a scope for a root whose only pending
// work is a scheduled whole-root scan isn't folded into it.
func TestScopedScansRunBeforeScheduledFullScans(t *testing.T) {
	s, _ := newStore(t)
	s.now = time.Now
	m := newMedia(t)
	for _, r := range []string{"A", "B", "C"} {
		m.write("Movies"+r+"/X (2001)/X (2001).mkv", 10)
	}
	lib, err := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"MoviesA", "MoviesB", "MoviesC"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, r := range lib.Roots {
		ids[r.Path] = r.ID
	}
	opts := fastOpts
	opts.NoWatch, opts.Debounce = true, time.Millisecond
	sch := NewScheduler(m.scanner(s), &scriptedProber{}, nil, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	defer func() { cancel(); <-done }()
	runs := func(where string) int {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE ` + where).Scan(&n)
		return n
	}
	eventually(t, "startup scans", func() bool { return runs(`status = 'completed'`) == 3 })

	sch.Pause()
	eventually(t, "idle", func() bool { sch.mu.Lock(); defer sch.mu.Unlock(); return sch.scanning == nil })
	sch.mu.Lock()
	for _, rs := range sch.roots {
		// Every root's scheduled scan is queued and due, as at the end of
		// an interval.
		sch.queueFull(rs, CauseSchedule, time.Now())
		rs.nextFull = time.Now().Add(time.Hour)
	}
	sch.mu.Unlock()
	if err := sch.Hint(ids["MoviesC"], "X (2001)", CauseImport); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // past the debounce: all four are due
	sch.Resume()
	eventually(t, "all scans", func() bool {
		return runs(`cause IN ('import', 'schedule') AND status = 'completed'`) == 4
	})
	var first, firstCause string
	s.db.QueryRow(`SELECT root_id, cause FROM library_scan_runs WHERE cause IN ('import', 'schedule') ORDER BY rowid LIMIT 1`).Scan(&first, &firstCause)
	if first != ids["MoviesC"] || firstCause != CauseImport {
		t.Fatalf("first scan after resume: root %s cause %s, want the import on MoviesC", first, firstCause)
	}
	if n := runs(`cause = 'schedule' AND scope IS NULL`); n != 3 {
		t.Fatalf("%d scheduled whole-root scans, want 3 (the import didn't replace C's)", n)
	}
}

// #106: roots added together don't all fall due for their periodic scan at
// once; each first periodic scan is spread over half to one and a half
// intervals.
func TestPeriodicScansAreStaggered(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	var roots []string
	for i := range 10 {
		roots = append(roots, fmt.Sprintf("M%d", i))
	}
	if _, err := s.CreateLibrary(context.Background(), "Movies", "movies", roots); err != nil {
		t.Fatal(err)
	}
	opts := fastOpts
	opts.FullScanEvery = time.Hour
	sch := NewScheduler(m.scanner(s), &scriptedProber{}, nil, opts)
	start := time.Now()
	if err := sch.reload(context.Background(), CauseStartup); err != nil {
		t.Fatal(err)
	}
	seen := map[time.Duration]bool{}
	for _, rs := range sch.roots {
		d := rs.nextFull.Sub(start)
		if d < 30*time.Minute || d > 91*time.Minute {
			t.Fatalf("first periodic scan after %v, want between 30 and 90 minutes", d)
		}
		seen[d.Truncate(time.Second)] = true
	}
	if len(seen) < 8 {
		t.Fatalf("only %d distinct first scan times for 10 roots", len(seen))
	}
}

// #106: a hint for a root whose scheduled scan is already due doesn't push
// that scan back by the debounce. The root is scanned now, and the
// whole-root scan covers the import.
func TestHintDoesNotDelayADueRoot(t *testing.T) {
	opts := fastOpts
	opts.NoWatch, opts.Debounce, opts.MaxDelay = true, time.Hour, time.Hour
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	f.sch.Pause()
	eventually(t, "idle", func() bool { f.sch.mu.Lock(); defer f.sch.mu.Unlock(); return f.sch.scanning == nil })
	f.sch.mu.Lock()
	f.sch.queueFull(f.sch.roots[f.root], CauseSchedule, time.Now())
	f.sch.mu.Unlock()
	if err := f.sch.Hint(f.root, "A (2001)", CauseImport); err != nil {
		t.Fatal(err)
	}
	f.sch.Resume()
	eventually(t, "scheduled scan runs now", func() bool {
		return f.runs(t, `cause = 'schedule' AND status = 'completed'`) == 1
	})
	time.Sleep(100 * time.Millisecond)
	if n := f.runs(t, `cause = 'import'`); n != 0 {
		t.Fatalf("%d separate import scans; the whole-root scan covers it", n)
	}
}

// Review (LIB-2b): a stream of hints for a root whose scheduled scan is
// queued keeps its debounce, and can't starve that scheduled scan.
func TestHintStreamCannotStarveScheduledScan(t *testing.T) {
	opts := fastOpts
	opts.NoWatch, opts.Debounce, opts.MaxDelay = true, 100*time.Millisecond, 300*time.Millisecond
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	f.sch.Pause()
	eventually(t, "idle", func() bool { f.sch.mu.Lock(); defer f.sch.mu.Unlock(); return f.sch.scanning == nil })
	f.sch.mu.Lock()
	f.sch.queueFull(f.sch.roots[f.root], CauseSchedule, time.Now())
	f.sch.mu.Unlock()
	f.sch.Resume()
	for end := time.Now().Add(1500 * time.Millisecond); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		f.sch.Hint(f.root, "A (2001)/A (2001).mkv", CauseWatch)
	}
	if n := f.runs(t, `cause = 'schedule' AND status = 'completed'`); n != 1 {
		t.Fatalf("%d scheduled scans ran during the hint stream, want 1", n)
	}
	if n := f.runs(t, `cause = 'watch'`); n > 8 {
		t.Fatalf("%d scoped scans in 1.5 s: the debounce (100 ms, max 300 ms) wasn't kept", n)
	}
}

// Review (LIB-2b): the stagger survives the startup scans, so roots'
// periodic scans don't all fall due together afterwards.
func TestStaggerSurvivesStartup(t *testing.T) {
	s, _ := newStore(t)
	s.now = time.Now
	m := newMedia(t)
	var roots []string
	for i := range 6 {
		r := fmt.Sprintf("M%d", i)
		roots = append(roots, r)
		m.write(r+"/X (2001)/X (2001).mkv", 10)
	}
	if _, err := s.CreateLibrary(context.Background(), "Movies", "movies", roots); err != nil {
		t.Fatal(err)
	}
	opts := fastOpts
	opts.NoWatch, opts.FullScanEvery = true, time.Hour
	sch := NewScheduler(m.scanner(s), &scriptedProber{}, nil, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, "startup scans", func() bool {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE status = 'completed'`).Scan(&n)
		return n == 6
	})
	sch.mu.Lock()
	lo, hi := time.Duration(1<<62), time.Duration(0)
	for _, rs := range sch.roots {
		d := time.Until(rs.nextFull)
		lo, hi = min(lo, d), max(hi, d)
	}
	sch.mu.Unlock()
	if hi-lo < 10*time.Minute {
		t.Fatalf("next periodic scans span %v after startup, want them spread", hi-lo)
	}
}

// Review (LIB-2b): a library folder that is a symlink is watched through
// its target, so rechecks don't rescan it every minute.
func TestSymlinkedRootIsWatched(t *testing.T) {
	s, _ := newStore(t)
	s.now = time.Now
	m := newMedia(t)
	m.write("Real/A (2001)/A (2001).mkv", 10)
	if err := os.Symlink("Real", filepath.Join(m.dir, "Movies")); err != nil {
		t.Fatal(err)
	}
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sch := NewScheduler(m.scanner(s), &scriptedProber{}, nil, fastOpts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, "watching", func() bool {
		st, _ := sch.Status(context.Background())
		return len(st) == 1 && st[0].Watch == WatchOn && st[0].LastRun != nil && st[0].LastRun.Status == RunCompleted
	})
	time.Sleep(300 * time.Millisecond) // several rechecks
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE cause = 'recheck' AND root_id = ?`, lib.Roots[0].ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d recheck scans of a symlinked root", n)
	}
	m.write("Real/B (2002)/B (2002).mkv", 10)
	eventually(t, "change seen through the symlink", func() bool {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE cause = 'watch'`).Scan(&n)
		return n >= 1
	})
}

// Review (LIB-2b): the starvation bound. A scheduled scan waits behind
// scoped work (priority 1) only until it is MaxDelay overdue; then it
// competes as priority 0 and, being the oldest, runs next.
func TestOverdueScheduledScanIsPromoted(t *testing.T) {
	sch := NewScheduler(&Scanner{}, &scriptedProber{}, nil, SchedulerOptions{MaxDelay: time.Minute})
	now := time.Now()
	rs := &rootSched{full: CauseSchedule, scopes: map[string]string{"x": CauseWatch}, due: now, fullDue: now}
	prio := func() map[bool]int {
		out := map[bool]int{}
		for _, c := range sch.candidates(rs, now) {
			out[c.full] = c.prio
		}
		return out
	}
	if p := prio(); p[false] != 0 || p[true] != 1 {
		t.Fatalf("just due: scoped %d, scheduled %d; want 0 and 1", p[false], p[true])
	}
	rs.fullDue = now.Add(-time.Minute)
	if p := prio(); p[true] != 0 {
		t.Fatalf("a minute overdue: scheduled priority %d, want 0", p[true])
	}
	rs.full = CauseManual // urgent: one whole-root candidate, priority 0
	if c := sch.candidates(rs, now); len(c) != 1 || !c[0].full || c[0].prio != 0 {
		t.Fatalf("urgent: %+v", c)
	}
}
