package library

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Run history is bounded per kind, so scoped runs never push out the
// whole-root runs retirement relies on.
func TestPruneRunsKeepsEachKind(t *testing.T) {
	s, c, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	for range keepRuns + 20 {
		c.t = c.t.Add(time.Second)
		scanScope(t, sc, root, "A (2001)")
	}
	if err := s.pruneRuns(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	var full, scoped int
	s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE scope IS NULL`).Scan(&full)
	s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE scope IS NOT NULL`).Scan(&scoped)
	if full != 1 || scoped != keepRuns {
		t.Fatalf("kept %d whole-root and %d scoped runs", full, scoped)
	}
}

// UI-2 shows a first scan's progress ("1,204 files found"), so a running
// scan's counters are written after each batch, not only when it finishes.
func TestRunCountersAreLive(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/real.mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	mid, release := make(chan struct{}), make(chan struct{})
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		for i := range batchSize + 100 {
			if i == batchSize {
				close(mid) // the first batch has been written
				<-release
			}
			if err := found(File{Path: fmt.Sprintf("F (2000)/f%03d.mkv", i), Rev: Revision{Size: 10, ModTime: 1}}); err != nil {
				return err
			}
		}
		return nil
	}
	done := make(chan Run, 1)
	go func() { run, _ := sc.ScanRoot(context.Background(), root); done <- run }()
	<-mid
	ri, err := s.lastRun(context.Background(), root)
	if err != nil || ri == nil || ri.Status != RunRunning || ri.Seen != batchSize || ri.Added != batchSize {
		t.Fatalf("mid-scan run %+v, %v; want running with %d seen and added", ri, err, batchSize)
	}
	close(release)
	if run := <-done; run.Seen != batchSize+100 {
		t.Fatalf("final run %+v", run)
	}
}
