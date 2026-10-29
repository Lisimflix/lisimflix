package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// scriptedProber returns a result naming the file, or the error set for it, and
// runs during() inside the probe when set.
type scriptedProber struct {
	mu     sync.Mutex
	calls  []string
	fail   map[string]error
	during func(rel string)
}

func (f *scriptedProber) Probe(_ context.Context, _ *os.Root, rel string) (probe.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, rel)
	err := f.fail[rel]
	during := f.during
	f.mu.Unlock()
	if during != nil {
		during(rel)
	}
	if err != nil {
		return probe.Result{}, err
	}
	return probe.Result{Container: "fake:" + rel, DurationMs: 1000}, nil
}

const quiet = 30 * time.Second

func probeDue(t *testing.T, sc *Scanner, root string, p Prober) ProbeRun {
	t.Helper()
	run, err := sc.ProbeDue(context.Background(), root, p, 2, quiet, 0)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func probeColumns(t *testing.T, s *Store, rel string) (json, probeErr *string, after *int64) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT probe_json, probe_error, probe_after FROM library_files WHERE path = ?`, rel).
		Scan(&json, &probeErr, &after); err != nil {
		t.Fatal(err)
	}
	return
}

func setupMovies(t *testing.T, files ...string) (*Store, *clock, *media, *Scanner, string) {
	t.Helper()
	s, c := newStore(t)
	m := newMedia(t)
	for _, f := range files {
		m.write("Movies/"+f, 10)
	}
	lib, err := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	return s, c, m, sc, lib.Roots[0].ID
}

// SCALE-01: a probed file isn't probed again until its revision changes.
func TestProbeDueProbesEachRevisionOnce(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	p := &scriptedProber{}
	if run := probeDue(t, sc, root, p); run.Queued != 2 || run.Stored != 2 {
		t.Fatalf("first %+v", run)
	}
	if js, _, _ := probeColumns(t, s, "A (2001)/A (2001).mkv"); js == nil || *js == "" {
		t.Fatal("no probe stored")
	}
	if run := probeDue(t, sc, root, p); run.Queued != 0 || len(p.calls) != 2 {
		t.Fatalf("unchanged files were probed again: %+v %v", run, p.calls)
	}
	m.write("Movies/A (2001)/A (2001).mkv", 11)
	scan(t, sc, root)
	if run := probeDue(t, sc, root, p); run.Queued != 1 || run.Stored != 1 {
		t.Fatalf("after a change %+v", run)
	}
}

func TestProbeFailureIsKeptUntilRetried(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{fail: map[string]error{"A (2001)/A (2001).mkv": probe.ErrOutputTooLarge}}
	if run := probeDue(t, sc, root, p); run.Failed != 1 {
		t.Fatalf("%+v", run)
	}
	if _, perr, _ := probeColumns(t, s, "A (2001)/A (2001).mkv"); perr == nil || *perr != probe.ErrOutputTooLarge.Error() {
		t.Fatalf("probe_error %v", perr)
	}
	if run := probeDue(t, sc, root, p); run.Queued != 0 {
		t.Fatalf("a failed probe was retried without a change or a retry: %+v", run)
	}
	var id string
	s.db.QueryRow(`SELECT id FROM library_files`).Scan(&id)
	if err := s.RetryProbe(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	p.fail = nil
	if run := probeDue(t, sc, root, p); run.Stored != 1 {
		t.Fatalf("after RetryProbe %+v", run)
	}
	if err := s.RetryProbe(context.Background(), "file-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown file: %v", err)
	}
}

// #62: a file still being written waits for the quiet period instead of
// being re-queued in a loop.
func TestStillChangingFileWaitsForQuietPeriod(t *testing.T) {
	s, c, m, sc, root := setupMovies(t)
	c.t = time.Now()
	p := filepath.Join(m.dir, "Movies/New (2026)/New (2026).mkv")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, make([]byte, 10), 0o644) // mtime: now
	scan(t, sc, root)
	if run := probeDue(t, sc, root, &scriptedProber{}); run.Deferred != 1 || run.Stored != 0 {
		t.Fatalf("fresh file %+v", run)
	}
	_, _, after := probeColumns(t, s, "New (2026)/New (2026).mkv")
	info, _ := os.Stat(p)
	if after == nil || *after != info.ModTime().Add(quiet).UnixMilli() {
		t.Fatalf("probe_after %v, want mtime + quiet", after)
	}
	if run := probeDue(t, sc, root, &scriptedProber{}); run.Queued != 0 {
		t.Fatalf("queued again before the quiet period ended: %+v", run)
	}
	c.t = info.ModTime().Add(quiet + time.Second)
	if run := probeDue(t, sc, root, &scriptedProber{}); run.Queued != 1 {
		t.Fatalf("not queued after the quiet period: %+v", run)
	}
}

// A result for a revision the catalogue no longer holds is dropped.
func TestStaleProbeResultIsDropped(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{during: func(string) {
		// A scan records a newer revision while the probe runs.
		s.db.Exec(`UPDATE library_files SET size = size + 1`)
	}}
	if run := probeDue(t, sc, root, p); run.Stale != 1 || run.Stored != 0 {
		t.Fatalf("%+v", run)
	}
	if js, _, _ := probeColumns(t, s, "A (2001)/A (2001).mkv"); js != nil {
		t.Fatal("stale result stored")
	}
}

// A file that changes on disk during its probe is deferred with its new revision.
func TestFileChangedDuringProbeIsDeferred(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{during: func(rel string) { m.write("Movies/"+rel, 99) }}
	if run := probeDue(t, sc, root, p); run.Deferred != 1 {
		t.Fatalf("%+v", run)
	}
	var size int64
	s.db.QueryRow(`SELECT size FROM library_files`).Scan(&size)
	if size != 99 {
		t.Fatalf("catalogue size %d, want the new revision's 99", size)
	}
}

// A deferred outcome never replaces a newer revision a scan recorded while
// the probe ran (#90 review).
func TestDeferredRevisionNeverMovesBackwards(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	const later = int64(1) << 62
	p := &scriptedProber{during: func(rel string) {
		m.write("Movies/"+rel, 99)                                // the probe sees the file change...
		s.db.Exec(`UPDATE library_files SET mtime_ns = ?`, later) // ...and a scan records a newer revision
	}}
	if run := probeDue(t, sc, root, p); run.Deferred != 1 {
		t.Fatalf("%+v", run)
	}
	var mtime int64
	s.db.QueryRow(`SELECT mtime_ns FROM library_files`).Scan(&mtime)
	if mtime != later {
		t.Fatalf("mtime_ns %d: the deferred outcome overwrote the newer revision", mtime)
	}
}

// A limit takes the oldest changes first and leaves the rest for later calls.
func TestProbeDueLimit(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv", "C (2003)/C (2003).mkv")
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(filepath.Join(m.dir, "Movies/C (2003)/C (2003).mkv"), old, old)
	scan(t, sc, root)
	p := &scriptedProber{}
	run, err := sc.ProbeDue(context.Background(), root, p, 2, quiet, 1)
	if err != nil || run.Queued != 1 || run.Stored != 1 || len(p.calls) != 1 || p.calls[0] != "C (2003)/C (2003).mkv" {
		t.Fatalf("limit 1: %+v %v calls %v", run, err, p.calls)
	}
	if run := probeDue(t, sc, root, p); run.Queued != 2 {
		t.Fatalf("rest: %+v", run)
	}
	_ = s
}

// #109 point 2: a file that changed after the last scan and is quiet now is
// probed once, not on every ProbeDue turn until the next scan. Its newer
// revision is recorded with the result.
func TestFileChangedSinceTheScanIsProbedOnce(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	m.write("Movies/A (2001)/A (2001).mkv", 99) // changed, and quiet (an hour old), with no scan since
	p := &scriptedProber{}
	if run := probeDue(t, sc, root, p); run.Stored != 1 || run.Stale != 0 {
		t.Fatalf("first %+v", run)
	}
	if run := probeDue(t, sc, root, p); run.Queued != 0 || len(p.calls) != 1 {
		t.Fatalf("probed again before a scan: %+v %v", run, p.calls)
	}
	var size int64
	s.db.QueryRow(`SELECT size FROM library_files`).Scan(&size)
	if size != 99 {
		t.Fatalf("catalogue size %d, want the probed revision's 99", size)
	}
}

// #109 point 3: workers <= 0 means the default, not "probe nothing".
func TestProbeDueWithoutWorkersUsesTheDefault(t *testing.T) {
	_, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	run, err := sc.ProbeDue(context.Background(), root, &scriptedProber{}, 0, quiet, 0)
	if err != nil || run.Stored != 1 {
		t.Fatalf("%+v %v", run, err)
	}
}

// #109 point 1: an ffprobe timeout (a disk spinning up, a busy NAS) is
// retried after 15 min, 1 h, 6 h and 24 h. Only a fifth timeout in a row is
// kept as a failure for the admin list.
func TestProbeTimeoutIsRetriedWithBackoff(t *testing.T) {
	s, c, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	const rel = "A (2001)/A (2001).mkv"
	p := &scriptedProber{fail: map[string]error{rel: probe.ErrTimeout}}
	for i, wait := range []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour} {
		if run := probeDue(t, sc, root, p); run.TimedOut != 1 || run.Failed != 0 {
			t.Fatalf("timeout %d: %+v", i+1, run)
		}
		_, perr, after := probeColumns(t, s, rel)
		if perr != nil || after == nil || *after != ms(c.t.Add(wait)) {
			t.Fatalf("timeout %d: probe_error %v, probe_after %v, want %v", i+1, perr, after, ms(c.t.Add(wait)))
		}
		if run := probeDue(t, sc, root, p); run.Queued != 0 {
			t.Fatalf("timeout %d: retried before its wait: %+v", i+1, run)
		}
		c.t = c.t.Add(wait)
	}
	if run := probeDue(t, sc, root, p); run.Failed != 1 || run.TimedOut != 0 {
		t.Fatalf("fifth timeout: %+v", run)
	}
	if _, perr, after := probeColumns(t, s, rel); perr == nil || *perr != probe.ErrTimeout.Error() || after != nil {
		t.Fatalf("after the fifth timeout: probe_error %v, probe_after %v", perr, after)
	}
	c.t = c.t.Add(48 * time.Hour)
	if run := probeDue(t, sc, root, p); run.Queued != 0 {
		t.Fatalf("a kept failure was retried: %+v", run)
	}
}

// A stored result, or the admin's retry, starts the timeout count again.
func TestProbeTimeoutCountStartsAgain(t *testing.T) {
	s, c, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	const rel = "A (2001)/A (2001).mkv"
	p := &scriptedProber{fail: map[string]error{rel: probe.ErrTimeout}}
	probeDue(t, sc, root, p)
	c.t = c.t.Add(15 * time.Minute)
	probeDue(t, sc, root, p) // second timeout: 1 h
	c.t = c.t.Add(time.Hour)
	p.fail = nil
	if run := probeDue(t, sc, root, p); run.Stored != 1 {
		t.Fatalf("%+v", run)
	}
	var id string
	s.db.QueryRow(`SELECT id FROM library_files`).Scan(&id)
	if err := s.RetryProbe(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	p.fail = map[string]error{rel: probe.ErrTimeout}
	probeDue(t, sc, root, p)
	if _, _, after := probeColumns(t, s, rel); after == nil || *after != ms(c.t.Add(15*time.Minute)) {
		t.Fatalf("probe_after %v, want the first wait again", after)
	}
}

// A timeout for an older revision than the catalogue's is stale like any
// other result: it neither defers nor counts as a timeout.
func TestStaleProbeTimeoutIsDropped(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	p := &scriptedProber{fail: map[string]error{"A (2001)/A (2001).mkv": probe.ErrTimeout}, during: func(string) {
		s.db.Exec(`UPDATE library_files SET size = size + 1`) // a scan records a newer revision
	}}
	if run := probeDue(t, sc, root, p); run.Stale != 1 || run.TimedOut != 0 {
		t.Fatalf("%+v", run)
	}
	if _, _, after := probeColumns(t, s, "A (2001)/A (2001).mkv"); after != nil {
		t.Fatalf("stale timeout deferred the file: probe_after %v", *after)
	}
}
