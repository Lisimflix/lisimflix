package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetirementNeedsTwoSuccessfulScansADayApart(t *testing.T) {
	s, c, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	ctx := context.Background()
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	scan(t, sc, root) // B first missed now
	candidates := func() int {
		t.Helper()
		cs, err := s.RetirementCandidates(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		return len(cs)
	}

	c.t = c.t.Add(time.Hour)
	scan(t, sc, root)
	if n := candidates(); n != 0 {
		t.Fatalf("%d candidates after 1 h", n)
	}

	// An outage: a day passes, but the root is unreachable, so failed scans
	// don't confirm anything.
	os.Rename(filepath.Join(m.dir, "Movies"), filepath.Join(m.dir, "away"))
	c.t = c.t.Add(25 * time.Hour)
	if run := scan(t, sc, root); run.Status != RunFailed {
		t.Fatalf("outage scan %+v", run)
	}
	if n := candidates(); n != 0 {
		t.Fatalf("%d candidates from failed scans", n)
	}

	os.Rename(filepath.Join(m.dir, "away"), filepath.Join(m.dir, "Movies"))
	scan(t, sc, root)
	cs, _ := s.RetirementCandidates(ctx, root)
	if len(cs) != 1 || cs[0].Path != "B (2002)/B (2002).mkv" {
		t.Fatalf("candidates %+v", cs)
	}

	var presentID string
	s.db.QueryRow(`SELECT id FROM library_files WHERE state = 'present'`).Scan(&presentID)
	if n, err := s.Retire(ctx, root, []string{cs[0].FileID, presentID}); n != 1 || err != nil {
		t.Fatalf("Retire = %d, %v (the present file must be refused)", n, err)
	}
	if got := s.fileStates(t, root); got["B (2002)/B (2002).mkv"] != "retired" || got["A (2001)/A (2001).mkv"] != "present" {
		t.Fatalf("states %v", got)
	}
	// The item and its user-facing identity remain.
	if got := s.tree(t, mustLibID(t, s, root)); len(got) != 2 {
		t.Fatalf("items %v", got)
	}

	// A retired file that comes back is present again.
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	if run := scan(t, sc, root); run.Returned != 1 {
		t.Fatalf("return %+v", run)
	}
}

func mustLibID(t *testing.T, s *Store, root string) string {
	t.Helper()
	r, _, err := s.root(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return r.LibraryID
}
