package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func scanScope(t *testing.T, sc *Scanner, rootID, scope string) Run {
	t.Helper()
	run, err := sc.Scan(context.Background(), rootID, scope, CauseWatch)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func setupTV(t *testing.T, files ...string) (*Store, *clock, *media, *Scanner, string) {
	t.Helper()
	s, c := newStore(t)
	m := newMedia(t)
	for _, f := range files {
		m.write("TV/"+f, 10)
	}
	lib, err := s.CreateLibrary(context.Background(), "TV", "tv", []string{"TV"})
	if err != nil {
		t.Fatal(err)
	}
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	return s, c, m, sc, lib.Roots[0].ID
}

// A scoped scan reconciles only its folder: files elsewhere that have gone
// stay present until a scan covers them.
func TestScopedScanTouchesOnlyItsScope(t *testing.T) {
	s, _, m, sc, root := setupTV(t,
		"Show (2019)/Season 01/Show - S01E01.mkv",
		"Show (2019)/Season 02/Show - S02E01.mkv",
		"Show (2019) 2/Season 01/Show (2019) 2 - S01E01.mkv", // sorts just before "Show (2019)/"
		"Show (2019)0/Season 01/Show (2019)0 - S01E01.mkv",   // sorts just after
		"Other (2020)/Season 01/Other - S01E01.mkv")
	os.RemoveAll(filepath.Join(m.dir, "TV/Show (2019) 2"))
	os.RemoveAll(filepath.Join(m.dir, "TV/Show (2019)0"))
	os.Remove(filepath.Join(m.dir, "TV/Other (2020)/Season 01/Other - S01E01.mkv"))
	m.write("TV/Show (2019)/Season 01/Show - S01E02.mkv", 10)

	run := scanScope(t, sc, root, "Show (2019)/Season 01")
	if run.Status != RunCompleted || run.Scope != "Show (2019)/Season 01" || run.Seen != 2 || run.Added != 1 || run.Missing != 0 {
		t.Fatalf("scoped run %+v", run)
	}
	for p, st := range s.fileStates(t, root) {
		if st != "present" {
			t.Fatalf("%s is %s after a scan of another folder", p, st)
		}
	}
}

// A scope that no longer exists has its files marked missing, without a walk
// of its parent: renaming a top-level folder mustn't walk the whole root.
func TestScopedScanOfDeletedPath(t *testing.T) {
	s, _, m, sc, root := setupTV(t,
		"Show (2019)/Season 01/Show - S01E01.mkv",
		"Show (2019)/Season 02/Show - S02E01.mkv",
		"Other (2020)/Season 01/Other - S01E01.mkv")
	os.RemoveAll(filepath.Join(m.dir, "TV/Show (2019)/Season 02"))
	run := scanScope(t, sc, root, "Show (2019)/Season 02/Show - S02E01.mkv")
	if run.Status != RunCompleted || run.Scope != "Show (2019)/Season 02" || run.Seen != 0 || run.Missing != 1 {
		t.Fatalf("run %+v", run)
	}
	if st := s.fileStates(t, root)["Show (2019)/Season 02/Show - S02E01.mkv"]; st != "missing" {
		t.Fatalf("deleted episode is %s", st)
	}

	// A whole show gone: the highest gone folder is the scope.
	os.RemoveAll(filepath.Join(m.dir, "TV/Other (2020)"))
	if run := scanScope(t, sc, root, "Other (2020)/Season 01"); run.Scope != "Other (2020)" || run.Missing != 1 {
		t.Fatalf("show folder gone: %+v", run)
	}
	if got := s.fileStates(t, root)["Show (2019)/Season 01/Show - S01E01.mkv"]; got != "present" {
		t.Fatalf("other show's episode is %s", got)
	}
}

// A single file can be the scope: a loose file in the root doesn't need a
// walk of the whole root.
func TestScopedScanOfOneFile(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	m.write("Movies/Loose (2020).mkv", 10)
	m.write("Movies/notes.txt", 10)
	if run := scanScope(t, sc, root, "Loose (2020).mkv"); run.Seen != 1 || run.Added != 1 {
		t.Fatalf("file scope %+v", run)
	}
	if run := scanScope(t, sc, root, "notes.txt"); run.Seen != 0 || run.Status != RunCompleted {
		t.Fatalf("non-video scope %+v", run)
	}
	os.Remove(filepath.Join(m.dir, "Movies/Loose (2020).mkv"))
	if run := scanScope(t, sc, root, "Loose (2020).mkv"); run.Scope != "Loose (2020).mkv" || run.Missing != 1 {
		t.Fatalf("deleted file %+v", run)
	}
	if got := s.fileStates(t, root); got["A (2001)/A (2001).mkv"] != "present" {
		t.Fatalf("states %v", got)
	}
}

// SCAN-01 for scoped scans: an emptied or unreachable root still never marks
// anything missing, and sets the root offline.
func TestScopedScanOfflineRoot(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	os.Rename(filepath.Join(m.dir, "Movies"), filepath.Join(m.dir, "away"))
	if run := scanScope(t, sc, root, "A (2001)"); run.Status != RunFailed || run.Reason != ReasonRootUnavailable {
		t.Fatalf("unreachable %+v", run)
	}
	if st, _ := rootState(t, s, root); st != RootOffline {
		t.Fatalf("root %s", st)
	}
	os.Mkdir(filepath.Join(m.dir, "Movies"), 0o755)
	if run := scanScope(t, sc, root, "A (2001)"); run.Status != RunFailed || run.Reason != ReasonRootEmpty {
		t.Fatalf("empty %+v", run)
	}
	for p, st := range s.fileStates(t, root) {
		if st != "present" {
			t.Fatalf("%s is %s", p, st)
		}
	}
}

// Only whole-root scans change the root's state or count towards retirement.
func TestScopedScanLeavesRootStateAndRetirementAlone(t *testing.T) {
	s, c, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE library_roots SET state = 'degraded', reason = 'enumeration_failed' WHERE id = ?`, root); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	if run := scanScope(t, sc, root, "B (2002)"); run.Missing != 1 {
		t.Fatalf("run %+v", run)
	}
	if st, why := rootState(t, s, root); st != RootDegraded || why != ReasonEnumerationFailed {
		t.Fatalf("root %s %s after a scoped scan", st, why)
	}
	c.t = c.t.Add(25 * time.Hour)
	scanScope(t, sc, root, "B (2002)")
	if cs, _ := s.RetirementCandidates(ctx, root); len(cs) != 0 {
		t.Fatalf("scoped scans made candidates %+v", cs)
	}
	scan(t, sc, root)
	if cs, _ := s.RetirementCandidates(ctx, root); len(cs) != 1 {
		t.Fatalf("whole-root scan: candidates %+v", cs)
	}
	if st, _ := rootState(t, s, root); st != RootAvailable {
		t.Fatalf("root %s after a whole-root scan", st)
	}
}

func TestScopeMustStayInsideTheRoot(t *testing.T) {
	_, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	for _, scope := range []string{"../etc", "/etc", ".grab/x.mkv", "A (2001)/@eaDir/x.mkv", "A (2001)/.hidden.mkv"} {
		if _, err := sc.Scan(context.Background(), root, scope, CauseWatch); !errors.Is(err, ErrInvalid) {
			t.Errorf("scope %q: %v, want ErrInvalid", scope, err)
		}
	}
}

// A folder rename seen by the watcher (new folder, then old one gone) is
// folded like a rename found by a whole-root scan: the item keeps its ID.
func TestScopedScansFoldARename(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	var before string
	s.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'movie'`).Scan(&before)
	os.Rename(filepath.Join(m.dir, "Movies/Heat (1995)"), filepath.Join(m.dir, "Movies/Heat (1995) {tmdb-949}"))
	scanScope(t, sc, root, "Heat (1995) {tmdb-949}")
	scanScope(t, sc, root, "Heat (1995)")
	var ids []string
	rows, _ := s.db.Query(`SELECT id FROM library_items WHERE kind = 'movie'`)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != before {
		t.Fatalf("movies after rename %v, want [%s]", ids, before)
	}
}

// Two folders that both exist are flagged only by a whole-root scan.
func TestScopedScanLeavesLivePairPending(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	m.write("Movies/Heat.1995.2160p/Heat.1995.2160p.mkv", 10)
	scanScope(t, sc, root, "Heat.1995.2160p")
	state := func() string {
		var st string
		s.db.QueryRow(`SELECT state FROM library_duplicates`).Scan(&st)
		return st
	}
	if got := state(); got != "pending" {
		t.Fatalf("after scoped scan: %s", got)
	}
	scan(t, sc, root)
	if got := state(); got != "flagged" {
		t.Fatalf("after whole-root scan: %s", got)
	}
}
