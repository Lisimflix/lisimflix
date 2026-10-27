package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// fakeProber records calls and runs an optional hook inside each probe.
type fakeProber struct {
	calls   atomic.Int32
	running atomic.Int32
	maxRun  atomic.Int32
	hook    func(ctx context.Context, rel string) error
}

func (p *fakeProber) Probe(ctx context.Context, root *os.Root, rel string) (probe.Result, error) {
	p.calls.Add(1)
	n := p.running.Add(1)
	defer p.running.Add(-1)
	for {
		m := p.maxRun.Load()
		if n <= m || p.maxRun.CompareAndSwap(m, n) {
			break
		}
	}
	if p.hook != nil {
		if err := p.hook(ctx, rel); err != nil {
			return probe.Result{}, err
		}
	}
	return probe.Result{Container: "fake:" + rel}, nil
}

// runQueue starts the queue, lets its workers go idle, adds rels, and returns
// outcomes by path once want of them have arrived.
func runQueue(t *testing.T, root *os.Root, p Prober, workers, want int, rels ...string) map[string]Outcome {
	t.Helper()
	var mu sync.Mutex
	got := map[string]Outcome{}
	all := make(chan struct{})
	q := NewQueue(root, p, DefaultQuiet, func(o Outcome) {
		mu.Lock()
		defer mu.Unlock()
		got[o.Path] = o
		if len(got) == want {
			close(all)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx, workers); close(done) }()
	time.Sleep(20 * time.Millisecond) // workers are now waiting for work
	for _, r := range rels {
		q.Add(r)
	}
	select {
	case <-all:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out with %d of %d outcomes", len(got), want)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return got
}

func TestQueueProbesSettledFile(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	p := &fakeProber{}
	got := runQueue(t, openRoot(t, dir), p, 1, 1, "movie.mkv")
	o := got["movie.mkv"]
	info, _ := os.Stat(filepath.Join(dir, "movie.mkv"))
	if o.Err != nil || o.Probe.Container != "fake:movie.mkv" || o.Rev != RevisionOf(info) {
		t.Fatalf("outcome %+v", o)
	}
}

func TestQueueWaitsForQuietPeriod(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	now := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "movie.mkv"), now, now); err != nil {
		t.Fatal(err)
	}
	p := &fakeProber{}
	o := runQueue(t, openRoot(t, dir), p, 1, 1, "movie.mkv")["movie.mkv"]
	if !errors.Is(o.Err, ErrNotStable) || o.Rev.ModTime != now.UnixNano() || p.calls.Load() != 0 {
		t.Fatalf("outcome %+v, %d probes", o, p.calls.Load())
	}
}

func TestQueueRejectsResultWhenFileChangesDuringProbe(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	p := &fakeProber{hook: func(context.Context, string) error {
		f, err := os.OpenFile(filepath.Join(dir, "movie.mkv"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("more")
		return err
	}}
	o := runQueue(t, openRoot(t, dir), p, 1, 1, "movie.mkv")["movie.mkv"]
	info, _ := os.Stat(filepath.Join(dir, "movie.mkv"))
	if !errors.Is(o.Err, ErrChanged) || o.Rev != RevisionOf(info) || o.Probe.Container != "" {
		t.Fatalf("outcome %+v", o)
	}
}

func TestQueueReportsMissingFileAndProbeErrors(t *testing.T) {
	dir := writeTree(t, "bad.mkv")
	boom := errors.New("ffprobe failed")
	p := &fakeProber{hook: func(context.Context, string) error { return boom }}
	got := runQueue(t, openRoot(t, dir), p, 1, 2, "gone.mkv", "bad.mkv")
	if !errors.Is(got["gone.mkv"].Err, ErrGone) {
		t.Fatalf("gone: %+v", got["gone.mkv"])
	}
	if o := got["bad.mkv"]; !errors.Is(o.Err, boom) || o.Rev.Size == 0 {
		t.Fatalf("bad: %+v", o)
	}
}

func TestQueueCoalescesDuplicates(t *testing.T) {
	dir := writeTree(t, "movie.mkv", "last.mkv")
	p := &fakeProber{}
	finished := make(chan string, 4)
	q := NewQueue(openRoot(t, dir), p, DefaultQuiet, func(o Outcome) { finished <- o.Path })
	for _, r := range []string{"movie.mkv", "movie.mkv", "movie.mkv", "last.mkv"} {
		q.Add(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx, 1)
	// One worker takes jobs in order, so every earlier job is done once last.mkv is.
	for path := range finished {
		if path == "last.mkv" {
			break
		}
	}
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("%d probes, want 2", n)
	}
}

func TestQueueBoundsConcurrency(t *testing.T) {
	files := []string{"1.mkv", "2.mkv", "3.mkv", "4.mkv", "5.mkv", "6.mkv"}
	dir := writeTree(t, files...)
	p := &fakeProber{hook: func(context.Context, string) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	}}
	got := runQueue(t, openRoot(t, dir), p, 2, len(files), files...)
	if len(got) != 6 || p.maxRun.Load() != 2 {
		t.Fatalf("%d outcomes, %d probes at once (want 6, 2)", len(got), p.maxRun.Load())
	}
}

func TestQueueCancelStopsProbesAndDropsTheirResults(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	started := make(chan struct{})
	p := &fakeProber{hook: func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	var reported atomic.Int32
	q := NewQueue(openRoot(t, dir), p, DefaultQuiet, func(Outcome) { reported.Add(1) })
	q.Add("movie.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx, 2); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if reported.Load() != 0 {
		t.Fatal("a cancelled job was reported")
	}
}
