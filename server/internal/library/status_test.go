package library

import (
	"context"
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
