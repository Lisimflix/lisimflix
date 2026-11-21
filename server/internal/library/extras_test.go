package library

import (
	"context"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// extras lists "itemKey type title" lines, sorted.
func (s *Store) extras(t *testing.T) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT i.scan_key, x.type, x.title FROM library_extras x JOIN library_items i ON i.id = x.item_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k, typ, title string
		if err := rows.Scan(&k, &typ, &title); err != nil {
			t.Fatal(err)
		}
		out = append(out, k+" "+typ+" "+title)
	}
	slices.Sort(out)
	return out
}

// unplaced maps each unplaced file's path to its reason.
func (s *Store) unplaced(t *testing.T) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT path, unplaced FROM library_files WHERE unplaced IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, r string
		if err := rows.Scan(&p, &r); err != nil {
			t.Fatal(err)
		}
		out[p] = r
	}
	return out
}

func countItems(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library_items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExtraPlacedUnderMovie(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 10)
	m.write("Movies/Heat (1995)/Featurettes/Making Of.mkv", 10)
	m.write("Movies/Heat (1995)/Heat (1995)-trailer.mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	scan(t, m.scanner(s), lib.Roots[0].ID)

	want := []string{"movie:heat|1995 featurette Making Of", "movie:heat|1995 trailer Trailer"}
	if got := s.extras(t); !slices.Equal(got, want) {
		t.Fatalf("extras %v, want %v", got, want)
	}
	if got := s.tree(t, lib.ID); !slices.Equal(got, []string{"movie Heat - v1"}) {
		t.Fatalf("items %v", got)
	}
	if u := s.unplaced(t); len(u) != 0 {
		t.Fatalf("unplaced %v", u)
	}
}

func TestExtraPlacedAtOnceIsNotCountedUnplaced(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	m.write("Movies/Heat (1995)/Featurettes/a.mkv", 10)
	if run := scan(t, sc, lib.Roots[0].ID); run.Unplaced != 0 {
		t.Fatalf("%+v", run)
	}
	if got := s.extras(t); !slices.Equal(got, []string{"movie:heat|1995 featurette a"}) {
		t.Fatalf("extras %v", got)
	}
}

// A file name can't be 300 characters on disk, so placeExtra is called directly.
func TestExtraTitleIsCapped(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	scan(t, m.scanner(s), lib.Roots[0].ID)
	root, _, err := s.root(ctx, lib.Roots[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	rel := "Heat (1995)/Featurettes/" + strings.Repeat("é", 300) + ".mkv"
	if _, err := s.db.Exec(`INSERT INTO library_files(id, root_id, path, size, mtime_ns) VALUES('file-long', ?, ?, 1, 1)`, root.ID, rel); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	placed, err := s.placeExtra(ctx, tx, root, "file-long", rel)
	if err != nil || !placed {
		t.Fatalf("placed %v, %v", placed, err)
	}
	var title string
	tx.QueryRow(`SELECT title FROM library_extras WHERE file_id = 'file-long'`).Scan(&title)
	if n := utf8.RuneCountInString(title); n != 200 {
		t.Fatalf("title has %d runes", n)
	}
}

func TestSuffixExtrasAttachByFolderOrKey(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 10)
	m.write("Movies/Heat (1995)/Teaser-trailer.mkv", 10)
	m.write("TV/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	m.write("TV/Show (2019)/Season 01/Making Of-featurette.mkv", 10)
	m.write("TV/Show (2019)/Show (2019)-trailer.mkv", 10)
	ctx := context.Background()
	movies, _ := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	tv, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, movies.Roots[0].ID)
	scan(t, sc, tv.Roots[0].ID)
	want := []string{"movie:heat|1995 trailer Teaser", "show:show|2019 featurette Making Of", "show:show|2019 trailer Trailer"}
	if got := s.extras(t); !slices.Equal(got, want) {
		t.Fatalf("extras %v, want %v", got, want)
	}
	if u := s.unplaced(t); len(u) != 0 {
		t.Fatalf("unplaced %v", u)
	}
}

func TestShowSeasonExtraAttachesToShow(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Show (2019)/Season 01/S01E01.mkv", 10)
	m.write("TV/Show (2019)/Season 01/Featurettes/x.mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "TV", "tv", []string{"TV"})
	scan(t, m.scanner(s), lib.Roots[0].ID)
	if got := s.extras(t); !slices.Equal(got, []string{"show:show|2019 featurette x"}) {
		t.Fatalf("extras %v", got)
	}
	want := []string{"episode Episode 1 1 v1", "season Season 1 1 v0", "show Show - v0"}
	if got := s.tree(t, lib.ID); !slices.Equal(got, want) {
		t.Fatalf("items %v", got)
	}
}

func TestExtraBeforeParentIsAdopted(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995)/Featurettes/a.mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	if u := s.unplaced(t); u["Heat (1995)/Featurettes/a.mkv"] != "extra" {
		t.Fatalf("unplaced %v", u)
	}
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 10)
	scan(t, sc, lib.Roots[0].ID)
	if u := s.unplaced(t); len(u) != 0 {
		t.Fatalf("unplaced %v", u)
	}
	if got := s.extras(t); !slices.Equal(got, []string{"movie:heat|1995 featurette a"}) {
		t.Fatalf("extras %v", got)
	}
}

func TestExtraWithoutParentStaysUnplaced(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Heat (1995).mkv", 10)
	m.write("Movies/Extras/x.mkv", 10)
	m.write("Movies/Marvel/Iron Man (2008).mkv", 10)
	m.write("Movies/Marvel/Thor (2011).mkv", 10)
	m.write("Movies/Marvel/Featurettes/y.mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	items := countItems(t, s)
	scan(t, sc, lib.Roots[0].ID)
	want := map[string]string{"Extras/x.mkv": "extra", "Marvel/Featurettes/y.mkv": "extra"}
	if u := s.unplaced(t); len(u) != 2 || u["Extras/x.mkv"] != "extra" || u["Marvel/Featurettes/y.mkv"] != "extra" {
		t.Fatalf("unplaced %v, want %v", u, want)
	}
	if got := s.extras(t); len(got) != 0 {
		t.Fatalf("extras %v", got)
	}
	if n := countItems(t, s); n != items || n != 3 {
		t.Fatalf("items %d, then %d", items, n)
	}
}

func TestExtraNeverClimbsIntoAnotherMovieFolder(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/Marvel/Iron Man (2008).mkv", 10)
	m.write("Movies/Marvel/The Avengers (2012)/Behind The Scenes/x.mkv", 10)
	m.write("Movies/Marvel/Thor (2011)-trailer.mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	scan(t, sc, lib.Roots[0].ID)
	u := s.unplaced(t)
	if len(u) != 2 || u["Marvel/The Avengers (2012)/Behind The Scenes/x.mkv"] != "extra" || u["Marvel/Thor (2011)-trailer.mkv"] != "extra" {
		t.Fatalf("unplaced %v", u)
	}
	if got := s.extras(t); len(got) != 0 {
		t.Fatalf("extras %v", got)
	}

	m.write("Movies/Marvel/The Avengers (2012)/The Avengers (2012).mkv", 10)
	m.write("Movies/Marvel/Thor (2011).mkv", 10)
	scan(t, sc, lib.Roots[0].ID)
	want := []string{"movie:the avengers|2012 behindTheScenes x", "movie:thor|2011 trailer Thor (2011)"}
	if got := s.extras(t); !slices.Equal(got, want) {
		t.Fatalf("extras %v, want %v", got, want)
	}
	if u := s.unplaced(t); len(u) != 0 {
		t.Fatalf("unplaced %v", u)
	}
}

// Files misread as extras before LIB-3 are classified again by adopt, on any
// scan of the root, scoped ones included (decision 6c).
func TestAdoptHealsMisreadEpisode(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("TV/Show (2019)/Season 01/Show - S01E02 - The Crime-Scene.mkv", 10)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	// As if an older version had read it as an extra.
	s.db.Exec(`DELETE FROM library_items`)
	s.db.Exec(`UPDATE library_files SET unplaced = 'extra'`)
	m.write("TV/Other (2020)/Season 01/Other - S01E01.mkv", 10)
	if _, err := sc.Scan(ctx, lib.Roots[0].ID, "Other (2020)", CauseWatch); err != nil {
		t.Fatal(err)
	}
	if u := s.unplaced(t); len(u) != 0 {
		t.Fatalf("unplaced %v", u)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM library_versions v JOIN library_items i ON i.id = v.item_id
		WHERE i.scan_key = 'show:show|2019|s1|e2'`).Scan(&n)
	if n != 1 || len(s.extras(t)) != 0 {
		t.Fatalf("episode 2 versions %d, extras %v", n, s.extras(t))
	}
}
