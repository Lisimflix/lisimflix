package library

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// Spec §3.6 defaults.
const (
	DefaultQuiet   = 30 * time.Second // a file must be unchanged this long before it is probed
	DefaultWorkers = 2                // probes at once
)

// Prober probes one file inside a root. probe.Runner implements it.
type Prober interface {
	Probe(ctx context.Context, root *os.Root, rel string) (probe.Result, error)
}

var (
	ErrNotStable = errors.New("library: file changed within the quiet period")
	ErrChanged   = errors.New("library: file changed while it was probed")
	ErrGone      = errors.New("library: file is no longer available")
)

// Outcome is the result of one probe job. Rev is the revision the result
// belongs to; a consumer must not let it overwrite data from a newer revision.
// On ErrNotStable and ErrChanged, Rev is the file's latest revision and the
// caller queues the file again later.
type Outcome struct {
	Path  string
	Rev   Revision
	Probe probe.Result
	Err   error
}

// Queue probes files with a fixed number of workers. Adding a path that is
// already waiting does nothing, so duplicate work coalesces.
type Queue struct {
	root   *os.Root
	prober Prober
	quiet  time.Duration
	done   func(Outcome)

	mu      sync.Mutex
	pending map[string]bool
	order   []string
	wake    chan struct{}
}

// NewQueue returns a queue over root. done is called from worker goroutines,
// possibly at the same time, once per finished job.
func NewQueue(root *os.Root, p Prober, quiet time.Duration, done func(Outcome)) *Queue {
	return &Queue{root: root, prober: p, quiet: quiet, done: done,
		pending: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// Add queues rel for probing unless it is already waiting.
func (q *Queue) Add(rel string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[rel] {
		return
	}
	q.pending[rel] = true
	q.order = append(q.order, rel)
	q.signal()
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) next() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return "", false
	}
	rel := q.order[0]
	q.order = q.order[1:]
	delete(q.pending, rel)
	if len(q.order) > 0 {
		q.signal() // wake another idle worker
	}
	return rel, true
}

// Run probes queued files with the given number of workers until ctx ends.
// Cancelling stops running probes; jobs already finished have been reported,
// and cancelled ones are not.
func (q *Queue) Run(ctx context.Context, workers int) {
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for ctx.Err() == nil {
				rel, ok := q.next()
				if !ok {
					select {
					case <-ctx.Done():
					case <-q.wake:
					}
					continue
				}
				if out := q.job(ctx, rel); ctx.Err() == nil {
					q.done(out)
				}
			}
		})
	}
	wg.Wait()
}

// job checks the file has been quiet, probes it, then checks it again so a
// result never describes a different revision from the one it reports.
func (q *Queue) job(ctx context.Context, rel string) Outcome {
	before, err := q.stat(rel)
	if err != nil {
		return Outcome{Path: rel, Err: err}
	}
	// shortcut: quiet is judged from mtime alone, so a source whose clock runs
	// behind can look settled early; the re-check after probing still catches
	// a file that grows meanwhile. Track first-seen times if that shows up.
	if time.Since(time.Unix(0, before.ModTime)) < q.quiet {
		return Outcome{Path: rel, Rev: before, Err: ErrNotStable}
	}
	res, err := q.prober.Probe(ctx, q.root, rel)
	after, statErr := q.stat(rel)
	switch {
	case statErr != nil:
		return Outcome{Path: rel, Err: statErr}
	case after != before:
		return Outcome{Path: rel, Rev: after, Err: ErrChanged}
	case err != nil:
		return Outcome{Path: rel, Rev: before, Err: err}
	}
	return Outcome{Path: rel, Rev: before, Probe: res}
}

func (q *Queue) stat(rel string) (Revision, error) {
	info, err := q.root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return Revision{}, ErrGone
	}
	return RevisionOf(info), nil
}
