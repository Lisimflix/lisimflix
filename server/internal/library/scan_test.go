package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// media is a temporary media root with library folders under it.
type media struct {
	t   *testing.T
	dir string
}

func newMedia(t *testing.T) *media { return &media{t: t, dir: t.TempDir()} }

// write creates rel with size bytes and an mtime an hour old, so it is past
// the quiet period.
func (m *media) write(rel string, size int) {
	m.t.Helper()
	p := filepath.Join(m.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		m.t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		m.t.Fatal(err)
	}
}

func (m *media) scanner(s *Store) *Scanner {
	m.t.Helper()
	r, err := os.OpenRoot(m.dir)
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(func() { r.Close() })
	return &Scanner{Store: s, Media: r}
}

func scan(t *testing.T, sc *Scanner, rootID string) Run {
	t.Helper()
	run, err := sc.ScanRoot(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func counts(r Run) [6]int {
	return [6]int{r.Seen, r.Added, r.Changed, r.Returned, r.Missing, r.Unplaced}
}

func (s *Store) fileStates(t *testing.T, rootID string) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT path, state FROM library_files WHERE root_id = ?`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, st string
		if err := rows.Scan(&p, &st); err != nil {
			t.Fatal(err)
		}
		out[p] = st
	}
	return out
}

// tree lists items as "kind title [idx] ← versions" lines, sorted.
func (s *Store) tree(t *testing.T, libID string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT i.kind, i.title, COALESCE(i.idx, -1),
		(SELECT COUNT(*) FROM library_versions v WHERE v.item_id = i.id)
		FROM library_items i WHERE i.library_id = ?`, libID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, title string
		var idx, versions int
		if err := rows.Scan(&kind, &title, &idx, &versions); err != nil {
			t.Fatal(err)
		}
		out = append(out, kind+" "+title+" "+itoa(idx)+" v"+itoa(versions))
	}
	slices.Sort(out)
	return out
}

func itoa(n int) string {
	if n < 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func TestScanBuildsItemsFromNames(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv", 10)
	m.write("Movies/Heat (1995) {tmdb-949}/Heat (1995) - 2160p.mkv", 20)
	m.write("Movies/Heat.1995.Remux/Heat.1995.Remux.mkv", 30) // same name, another folder: flagged, not merged
	m.write("Movies/Heat (1995) {tmdb-949}/Featurettes/Making Of.mkv", 5)
	m.write("TV/Show (2019)/Season 01/Show - S01E01E02 - Pilot.mkv", 40)
	m.write("TV/Show (2019)/Season 01/Show - S01E03.mkv", 50)
	ctx := context.Background()
	movies, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	tv, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)

	if got := counts(scan(t, sc, movies.Roots[0].ID)); got != [6]int{4, 4, 0, 0, 0, 1} {
		t.Fatalf("movies run %v", got)
	}
	if got, want := s.tree(t, movies.ID), []string{"movie Heat - v1", "movie Heat - v2"}; !slices.Equal(got, want) {
		t.Fatalf("movies %v, want %v", got, want)
	}
	if dups, err := s.Duplicates(ctx, movies.ID); err != nil || len(dups) != 1 || dups[0].Title != "Heat" {
		t.Fatalf("duplicates %+v %v", dups, err)
	}
	var hint string
	if err := s.db.QueryRow(`SELECT namespace || '-' || value FROM library_id_hints`).Scan(&hint); err != nil || hint != "tmdb-949" {
		t.Fatalf("hint %q %v", hint, err)
	}
	if got := s.fileStates(t, movies.Roots[0].ID); len(got) != 4 {
		t.Fatalf("files %v", got)
	}

	scan(t, sc, tv.Roots[0].ID)
	// The two-episode file gives both episodes a version on the same file. Its
	// title ("Pilot") names the file, not one episode, so both keep a
	// placeholder until META-1 fetches real titles.
	want := []string{"episode Episode 1 1 v1", "episode Episode 2 2 v1", "episode Episode 3 3 v1", "season Season 1 1 v0", "show Show - v0"}
	if got := s.tree(t, tv.ID); !slices.Equal(got, want) {
		t.Fatalf("tv %v, want %v", got, want)
	}
	var shared int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library_versions v JOIN library_files f ON f.id = v.file_id
		WHERE f.path = 'Show (2019)/Season 01/Show - S01E01E02 - Pilot.mkv'`).Scan(&shared); err != nil || shared != 2 {
		t.Fatalf("versions on the two-episode file: %d %v", shared, err)
	}
}

func TestRescanAndChanges(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)

	if got := counts(scan(t, sc, root)); got != [6]int{2, 0, 0, 0, 0, 0} {
		t.Fatalf("unchanged rescan %v", got)
	}
	m.write("Movies/A (2001)/A (2001).mkv", 11)
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	if got := counts(scan(t, sc, root)); got != [6]int{1, 0, 1, 0, 1, 0} {
		t.Fatalf("change + delete %v", got)
	}
	if st := s.fileStates(t, root)["B (2002)/B (2002).mkv"]; st != "missing" {
		t.Fatalf("deleted file is %q", st)
	}
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	if got := counts(scan(t, sc, root)); got != [6]int{2, 0, 1, 1, 0, 0} {
		t.Fatalf("return %v", got) // its mtime is new, so it also counts as changed
	}
	var missingSince *int64
	s.db.QueryRow(`SELECT missing_since FROM library_files WHERE path = 'B (2002)/B (2002).mkv'`).Scan(&missingSince)
	if missingSince != nil || s.fileStates(t, root)["B (2002)/B (2002).mkv"] != "present" {
		t.Fatal("returned file isn't present again")
	}
	if got := s.tree(t, lib.ID); len(got) != 2 {
		t.Fatalf("items %v", got)
	}
}

func TestMovedFileKeepsItsItem(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat.1995.1080p/Heat.1995.1080p.mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	// Radarr renames the folder and file: same title and year, same item.
	os.RemoveAll(filepath.Join(m.dir, "Movies/Heat.1995.1080p"))
	m.write("Movies/Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv", 10)
	if got := counts(scan(t, sc, lib.Roots[0].ID)); got != [6]int{1, 1, 0, 0, 1, 0} {
		t.Fatalf("rename run %v", got)
	}
	if got := s.tree(t, lib.ID); !slices.Equal(got, []string{"movie Heat - v2"}) {
		t.Fatalf("items %v", got) // the old version's file is missing; retirement removes it later
	}
}

func rootState(t *testing.T, s *Store, id string) (string, string) {
	t.Helper()
	r, _, err := s.root(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r.State, r.Reason
}

// SCAN-01: an unreachable, emptied or partly walked root never marks files missing.
func TestOfflineRootsNeverMarkFilesMissing(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)
	allPresent := func(when string) {
		t.Helper()
		for p, st := range s.fileStates(t, root) {
			if st != "present" {
				t.Fatalf("%s: %s is %s", when, p, st)
			}
		}
	}

	// Unmounted: the folder is gone.
	os.Rename(filepath.Join(m.dir, "Movies"), filepath.Join(m.dir, "Movies.away"))
	if run := scan(t, sc, root); run.Status != RunFailed || run.Reason != ReasonRootUnavailable {
		t.Fatalf("missing folder: %+v", run)
	}
	if st, why := rootState(t, s, root); st != RootOffline || why != ReasonRootUnavailable {
		t.Fatalf("root %s %s", st, why)
	}
	allPresent("missing folder")

	// Mounted but empty: an empty mount point, not a deleted library.
	os.Mkdir(filepath.Join(m.dir, "Movies"), 0o755)
	if run := scan(t, sc, root); run.Status != RunFailed || run.Reason != ReasonRootEmpty {
		t.Fatalf("empty folder: %+v", run)
	}
	allPresent("empty folder")

	// Back: a normal scan, root available again.
	os.Remove(filepath.Join(m.dir, "Movies"))
	os.Rename(filepath.Join(m.dir, "Movies.away"), filepath.Join(m.dir, "Movies"))
	if run := scan(t, sc, root); run.Status != RunCompleted || run.Missing != 0 {
		t.Fatalf("returned root: %+v", run)
	}
	if st, why := rootState(t, s, root); st != RootAvailable || why != "" {
		t.Fatalf("root %s %q", st, why)
	}

	// A walk that stops part-way (an unreadable folder) keeps what it saw
	// and marks nothing missing.
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		if err := Discover(ctx, r, func(f File) error {
			if f.Path == "B (2002)/B (2002).mkv" {
				return errors.New("permission denied")
			}
			return found(f)
		}); err != nil {
			return err
		}
		return nil
	}
	if run := scan(t, sc, root); run.Status != RunFailed || run.Reason != ReasonEnumerationFailed || run.Seen != 1 {
		t.Fatalf("partial walk: %+v", run)
	}
	if st, _ := rootState(t, s, root); st != RootDegraded {
		t.Fatalf("root after partial walk: %s", st)
	}
	allPresent("partial walk")
}

func TestCancelledScanKeepsWorkAndMarksNothing(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	m.write("Movies/C (2003)/C (2003).mkv", 10)

	cctx, cancel := context.WithCancel(ctx)
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		return Discover(ctx, r, func(f File) error {
			err := found(f)
			if f.Path == "C (2003)/C (2003).mkv" {
				cancel() // stop after the new file
			}
			return err
		})
	}
	run, err := sc.ScanRoot(cctx, root)
	if err != nil || run.Status != RunCancelled || run.Added != 1 || run.Missing != 0 {
		t.Fatalf("cancelled run %+v %v", run, err)
	}
	if got := s.fileStates(t, root); got["C (2003)/C (2003).mkv"] != "present" || got["B (2002)/B (2002).mkv"] != "present" {
		t.Fatalf("after cancel %v", got) // C kept; B not marked missing
	}
	if st, _ := rootState(t, s, root); st != RootAvailable {
		t.Fatalf("root after cancel: %s", st)
	}
}

func TestMarkInterrupted(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	s.db.Exec(`INSERT INTO library_scan_runs(id, root_id, started_at, status) VALUES('scan-x', ?, 1, 'running')`, root)
	s.setRoot(ctx, root, RootScanning, "", false)
	if n, err := s.MarkInterrupted(ctx); n != 1 || err != nil {
		t.Fatalf("MarkInterrupted = %d, %v", n, err)
	}
	var status string
	s.db.QueryRow(`SELECT status FROM library_scan_runs WHERE id = 'scan-x'`).Scan(&status)
	if st, why := rootState(t, s, root); status != RunInterrupted || st != RootDegraded || why != "interrupted" {
		t.Fatalf("run %s, root %s %s", status, st, why)
	}
}

// A file naming couldn't place is tried again on every scan, so files that a
// later version's naming rules understand are picked up without a rescan of
// everything.
func TestUnplacedFilesArePlacedAgain(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	// As if an older version had failed to place it.
	s.db.Exec(`DELETE FROM library_items`)
	s.db.Exec(`UPDATE library_files SET unplaced = 'unrecognised'`)
	if run := scan(t, sc, lib.Roots[0].ID); run.Unplaced != 0 {
		t.Fatalf("%+v", run)
	}
	var unplaced *string
	s.db.QueryRow(`SELECT unplaced FROM library_files`).Scan(&unplaced)
	if got := s.tree(t, lib.ID); unplaced != nil || !slices.Equal(got, []string{"movie A - v1"}) {
		t.Fatalf("items %v, unplaced %v", got, unplaced)
	}
}

// fakeFiles is a Discover that reports n made-up files, enough to fill
// batches without writing them to disk.
func fakeFiles(n int) func(context.Context, *os.Root, func(File) error) error {
	return func(_ context.Context, _ *os.Root, found func(File) error) error {
		for i := range n {
			if err := found(File{Path: "F " + itoa(i) + " (2001)/F (2001).mkv", Rev: Revision{Size: 10, ModTime: 1}}); err != nil {
				return err
			}
		}
		return nil
	}
}

func scanRunRow(t *testing.T, s *Store, id string) (status, reason string, seen int) {
	t.Helper()
	var r *string
	if err := s.db.QueryRow(`SELECT status, reason, seen FROM library_scan_runs WHERE id = ?`, id).Scan(&status, &r, &seen); err != nil {
		t.Fatal(err)
	}
	if r != nil {
		reason = *r
	}
	return
}

// #108 points 1 and 3: a database error while the walk is running fails the
// run as a database error (not enumeration_failed), returns the error, and
// counts nothing from the rolled-back batch.
func TestDatabaseErrorDuringWalkFailsTheRun(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	os.MkdirAll(filepath.Join(m.dir, "Movies"), 0o755)
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	sc.Discover = fakeFiles(batchSize + 1) // the first flush happens inside the walk
	if _, err := s.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON library_files BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	run, err := sc.ScanRoot(ctx, root)
	if err == nil {
		t.Fatalf("no error returned; run %+v", run)
	}
	if run.Status != RunFailed || run.Reason != ReasonDatabaseError || run.Seen != 0 || run.Added != 0 {
		t.Fatalf("run %+v", run)
	}
	if status, reason, seen := scanRunRow(t, s, run.ID); status != RunFailed || reason != ReasonDatabaseError || seen != 0 {
		t.Fatalf("stored run %s %s seen=%d", status, reason, seen)
	}
	if st, why := rootState(t, s, root); st != RootDegraded || why != ReasonDatabaseError {
		t.Fatalf("root %s %s", st, why)
	}
}

// #108 point 2: a database error after the walk (here, marking files
// missing) ends the run as failed instead of leaving it running and the
// root scanning until the next restart.
func TestDatabaseErrorAfterWalkFailsTheRun(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	m.write("Movies/B (2002)/B (2002).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	root := lib.Roots[0].ID
	sc := m.scanner(s)
	scan(t, sc, root)
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	if _, err := s.db.Exec(`CREATE TRIGGER boom BEFORE UPDATE OF state ON library_files WHEN NEW.state = 'missing'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	run, err := sc.ScanRoot(ctx, root)
	if err == nil || run.Status != RunFailed || run.Reason != ReasonDatabaseError {
		t.Fatalf("run %+v, err %v", run, err)
	}
	if status, _, _ := scanRunRow(t, s, run.ID); status != RunFailed {
		t.Fatalf("stored run %s", status)
	}
	if st, _ := rootState(t, s, root); st != RootDegraded {
		t.Fatalf("root %s", st)
	}
}
