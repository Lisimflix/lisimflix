package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func itemID(t *testing.T, s *Store, key string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM library_items WHERE scan_key = ?`, key).Scan(&id); err != nil {
		t.Fatalf("item %s: %v", key, err)
	}
	return id
}

func dups(t *testing.T, s *Store, libID string) []Duplicate {
	t.Helper()
	d, err := s.Duplicates(context.Background(), libID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Two folders that exist at the same time and give the same name ("Doctor
// Who" with no year: 1963 or 2005?) are never merged by the scanner.
func TestCoexistingFoldersAreFlaggedNotMerged(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E01.mkv", 20)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)

	want := []string{"episode Episode 1 1 v1", "episode Episode 1 1 v1", "season Season 1 1 v0", "season Season 1 1 v0", "show Doctor Who - v0", "show Doctor Who - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) {
		t.Fatalf("items %v, want two separate shows", got)
	}
	d := dups(t, s, lib.ID)
	if len(d) != 1 {
		t.Fatalf("duplicates %+v", d)
	}
	if err := s.Decide(ctx, d[0].ID, false); err != nil {
		t.Fatal(err)
	}
	scan(t, sc, lib.Roots[0].ID)
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) || len(dups(t, s, lib.ID)) != 0 {
		t.Fatalf("after keeping them separate: %v, flags %v", got, dups(t, s, lib.ID))
	}
	if err := s.Decide(ctx, d[0].ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deciding twice: %v", err)
	}
	if err := s.Decide(ctx, "dup-nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown pair: %v", err)
	}
}

// Merging folds the other folder's show into the existing one: matching
// episodes get both versions, the rest move across, and the existing IDs stay.
func TestMergeDecisionFoldsItemsTogether(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E01.mkv", 20)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E02.mkv", 30)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	show, ep1 := itemID(t, s, "show:doctor who|0"), itemID(t, s, "show:doctor who|0|s1|e1")

	d := dups(t, s, lib.ID)
	if len(d) != 1 || d[0].ItemID != show {
		t.Fatalf("duplicates %+v", d)
	}
	if err := s.Decide(ctx, d[0].ID, true); err != nil {
		t.Fatal(err)
	}
	want := []string{"episode Episode 1 1 v2", "episode Episode 2 2 v1", "season Season 1 1 v0", "show Doctor Who - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) {
		t.Fatalf("after merge %v, want %v", got, want)
	}
	if itemID(t, s, "show:doctor who|0") != show || itemID(t, s, "show:doctor who|0|s1|e1") != ep1 {
		t.Fatal("the existing items' IDs changed")
	}
	// The decision sticks: the next scan neither splits nor flags them, and a
	// new episode in the merged folder joins the same show.
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E03.mkv", 40)
	scan(t, sc, lib.Roots[0].ID)
	want = append(want[:2:2], append([]string{"episode Episode 3 3 v1"}, want[2:]...)...)
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) || len(dups(t, s, lib.ID)) != 0 {
		t.Fatalf("after rescan %v, want %v; flags %v", got, want, dups(t, s, lib.ID))
	}
}

// A rename is not a duplicate: the old folder is gone by the end of the scan,
// so the new folder folds into the existing items and every ID is kept.
func TestRenamedFolderKeepsItemIDs(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Show.Name/Season 1/Show.Name.S01E01.mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	show, ep := itemID(t, s, "show:show name|0"), itemID(t, s, "show:show name|0|s1|e1")

	os.RemoveAll(filepath.Join(m.dir, "TV/Show.Name"))
	m.write("TV/Show Name/Season 01/Show Name - S01E01.mkv", 10)
	scan(t, sc, lib.Roots[0].ID)
	if itemID(t, s, "show:show name|0") != show || itemID(t, s, "show:show name|0|s1|e1") != ep {
		t.Fatal("rename changed the item IDs")
	}
	want := []string{"episode Episode 1 1 v2", "season Season 1 1 v0", "show Show Name - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) || len(dups(t, s, lib.ID)) != 0 {
		t.Fatalf("after rename %v, flags %v", got, dups(t, s, lib.ID))
	}
}

// #103: scan keys are unique per library only. The same show in another
// library, with files present, must not make a renamed folder look like two
// live folders.
func TestRenameFoldsWhenAnotherLibraryHasTheSameShow(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	m.write("Anime/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	ctx := context.Background()
	tv, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	anime, _ := s.CreateLibrary(ctx, "Anime", "anime", []string{"Anime"})
	sc := m.scanner(s)
	scan(t, sc, tv.Roots[0].ID)
	scan(t, sc, anime.Roots[0].ID)
	var before string
	s.db.QueryRow(`SELECT id FROM library_items WHERE library_id = ? AND kind = 'show'`, tv.ID).Scan(&before)

	os.Rename(filepath.Join(m.dir, "TV/Show (2019)"), filepath.Join(m.dir, "TV/Show (2019) {tvdb-1}"))
	scan(t, sc, tv.Roots[0].ID)
	if d := dups(t, s, tv.ID); len(d) != 0 {
		t.Fatalf("rename flagged as a duplicate: %+v", d)
	}
	var shows []string
	rows, _ := s.db.Query(`SELECT id FROM library_items WHERE library_id = ? AND kind = 'show'`, tv.ID)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		shows = append(shows, id)
	}
	rows.Close()
	if len(shows) != 1 || shows[0] != before {
		t.Fatalf("TV shows after rename %v, want [%s]", shows, before)
	}
}

// Review finding: folding a renamed folder moves seasons the existing item
// doesn't have. Their parent must be found in the same library, never in
// another library with the same show (where a cascade delete would then
// take them).
func TestFoldKeepsParentsInTheLibrary(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV4K/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	m.write("TV4K/Show (2019)/Season 02/Show - S02E01.mkv", 10)
	m.write("TV/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	ctx := context.Background()
	uhd, _ := s.CreateLibrary(ctx, "TV 4K", "tv", []string{"TV4K"})
	tv, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, uhd.Roots[0].ID)
	scan(t, sc, tv.Roots[0].ID)

	// Renamed, with a season the old folder didn't have: folded in.
	os.Rename(filepath.Join(m.dir, "TV/Show (2019)"), filepath.Join(m.dir, "TV/Show (2019) {tvdb-1}"))
	m.write("TV/Show (2019) {tvdb-1}/Season 02/Show - S02E01.mkv", 10)
	scan(t, sc, tv.Roots[0].ID)

	rows, err := s.db.Query(`SELECT c.scan_key, p.library_id FROM library_items c JOIN library_items p ON p.id = c.parent_id
		WHERE c.library_id = ?`, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var key, parentLib string
		rows.Scan(&key, &parentLib)
		if parentLib != tv.ID {
			t.Errorf("TV item %s has its parent in library %s", key, parentLib)
		}
		n++
	}
	if n != 4 { // s1, s1e1, s2, s2e1
		t.Fatalf("%d TV items with a parent, want 4", n)
	}
	if len(dups(t, s, tv.ID)) != 0 {
		t.Fatal("rename flagged")
	}
}

func shows(t *testing.T, s *Store, libID string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id FROM library_items WHERE library_id = ? AND kind = 'show' ORDER BY id`, libID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

// Review finding: after "keep separate", renaming the separate folder is a
// rename of that folder: it folds into its own item, without a new flag or
// a third show.
func TestKeptSeparateFolderCanBeRenamed(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E01.mkv", 20)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	d := dups(t, s, lib.ID)
	if len(d) != 1 {
		t.Fatalf("duplicates %+v", d)
	}
	if err := s.Decide(ctx, d[0].ID, false); err != nil {
		t.Fatal(err)
	}
	before := shows(t, s, lib.ID)

	os.Rename(filepath.Join(m.dir, "TV/Doctor.Who"), filepath.Join(m.dir, "TV/Doctor Who {tvdb-2}"))
	scan(t, sc, lib.Roots[0].ID)
	if got := shows(t, s, lib.ID); !slices.Equal(got, before) {
		t.Fatalf("shows %v, want the same two %v", got, before)
	}
	if d := dups(t, s, lib.ID); len(d) != 0 {
		t.Fatalf("the decision didn't stick: %+v", d)
	}
}

// Review finding: after "merge", renaming either merged folder is a rename
// into the merged item, even though its other folder still has files.
func TestMergedFolderCanBeRenamed(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E02.mkv", 20)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	d := dups(t, s, lib.ID)
	if err := s.Decide(ctx, d[0].ID, true); err != nil {
		t.Fatal(err)
	}
	before := shows(t, s, lib.ID)

	os.Rename(filepath.Join(m.dir, "TV/Doctor.Who"), filepath.Join(m.dir, "TV/Doctor Who {tvdb-2}"))
	scan(t, sc, lib.Roots[0].ID)
	if got := shows(t, s, lib.ID); len(before) != 1 || !slices.Equal(got, before) {
		t.Fatalf("shows %v, want the merged one %v", got, before)
	}
	if d := dups(t, s, lib.ID); len(d) != 0 {
		t.Fatalf("the merge didn't stick: %+v", d)
	}
	// Episode 2 has the renamed file plus its old, now missing, path (kept
	// until retirement), as for any rename.
	want := []string{"episode Episode 1 1 v1", "episode Episode 2 2 v2", "season Season 1 1 v0", "show Doctor Who - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) {
		t.Fatalf("tree %v, want %v", got, want)
	}
}

// When more than one gone folder could be the source of a rename, the
// scanner doesn't guess: the new folder is flagged for the admin.
func TestAmbiguousRenameIsFlagged(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Doctor Who/Season 01/Doctor Who - S01E01.mkv", 10)
	m.write("TV/Doctor.Who/Season 01/Doctor.Who.S01E01.mkv", 20)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	if err := s.Decide(ctx, dups(t, s, lib.ID)[0].ID, false); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(m.dir, "TV/Doctor Who"))
	os.RemoveAll(filepath.Join(m.dir, "TV/Doctor.Who"))
	m.write("TV/Doctor Who {tvdb-9}/Season 01/Doctor Who - S01E01.mkv", 30)
	scan(t, sc, lib.Roots[0].ID)
	if d := dups(t, s, lib.ID); len(d) != 1 {
		t.Fatalf("ambiguous rename: flags %+v, want one", d)
	}
	if n := len(shows(t, s, lib.ID)); n != 3 {
		t.Fatalf("%d shows, want 3 (nothing folded)", n)
	}
}
