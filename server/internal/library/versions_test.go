package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const heatKey = "movie:heat|1995"

// vrow is a version as these tests read it, with its flag if any.
type vrow struct {
	ID, Label, Path string
	Ord, Seq        int
	Fresh, Present  bool
	Of, State       string
}

// vers lists the versions of the item with scan key key, in seq order.
func (s *Store) vers(t *testing.T, key string) []vrow {
	t.Helper()
	rows, err := s.db.Query(`SELECT v.id, COALESCE(v.label, ''), f.path, v.ordinal, v.seq, v.fresh, f.state = 'present',
		COALESCE(g.of_id, ''), COALESCE(g.state, '')
		FROM library_versions v JOIN library_files f ON f.id = v.file_id JOIN library_items i ON i.id = v.item_id
		LEFT JOIN library_version_flags g ON g.version_id = v.id WHERE i.scan_key = ? ORDER BY v.seq, v.id`, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []vrow
	for rows.Next() {
		var v vrow
		if err := rows.Scan(&v.ID, &v.Label, &v.Path, &v.Ord, &v.Seq, &v.Fresh, &v.Present, &v.Of, &v.State); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func (s *Store) versionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library_versions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// noBadFlags checks no version is flagged against itself or against a
// version flagged against it.
func noBadFlags(t *testing.T, s *Store) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library_version_flags a LEFT JOIN library_version_flags b
		ON b.version_id = a.of_id AND b.of_id = a.version_id WHERE a.version_id = a.of_id OR b.version_id IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d self or mutual flags", n)
	}
}

func vflags(t *testing.T, s *Store, libID string) []VersionFlag {
	t.Helper()
	f, err := s.VersionFlags(context.Background(), libID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// lib is a library of typ over one folder holding files, not yet scanned.
func lib(t *testing.T, typ, folder string, files ...string) (*Store, *media, *Scanner, Library) {
	t.Helper()
	s, _ := newStore(t)
	m := newMedia(t)
	for _, f := range files {
		m.write(folder+"/"+f, 10)
	}
	l, err := s.CreateLibrary(context.Background(), folder, typ, []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	return s, m, m.scanner(s), l
}

func movieLib(t *testing.T, files ...string) (*Store, *media, *Scanner, Library) {
	return lib(t, "movies", "Movies", files...)
}

func (m *media) rm(rel string) {
	m.t.Helper()
	if err := os.Remove(filepath.Join(m.dir, rel)); err != nil {
		m.t.Fatal(err)
	}
}

func (m *media) mv(from, to string) {
	m.t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(m.dir, to)), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(m.dir, from), filepath.Join(m.dir, to)); err != nil {
		m.t.Fatal(err)
	}
}

// only checks the item has one version, id, on path, ordinal 1 and unflagged.
func only(t *testing.T, got []vrow, id, path string) {
	t.Helper()
	if len(got) != 1 || got[0].ID != id || got[0].Path != path || got[0].Ord != 1 || got[0].Of != "" {
		t.Fatalf("versions %+v, want only %s on %s, ordinal 1, no flag", got, id, path)
	}
}

const (
	heat1080   = "Heat (1995)/Heat (1995) - 1080p.mkv"
	heatRepack = "Heat (1995)/Heat (1995) - 1080p REPACK.mkv"
	heatProper = "Heat (1995)/Heat (1995) - 1080p Proper.mkv"
	heatBluray = "Heat (1995)/Heat (1995) - Bluray-1080p.mkv"
)

func TestUpgradedFileKeepsVersionID(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080)
	root := l.Roots[0].ID
	scan(t, sc, root)
	id := s.vers(t, heatKey)[0].ID
	m.rm("Movies/" + heat1080)
	m.write("Movies/"+heatRepack, 20)
	scan(t, sc, root)
	only(t, s.vers(t, heatKey), id, heatRepack)
}

// The old version is still fresh after a scoped import; that must not block
// adoption.
func TestScopedImportThenScopedUpgradeKeepsVersionID(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080)
	root := l.Roots[0].ID
	scanScope(t, sc, root, "Heat (1995)")
	v := s.vers(t, heatKey)
	if len(v) != 1 || !v[0].Fresh {
		t.Fatalf("after a scoped import %+v, want one fresh version", v)
	}
	m.rm("Movies/" + heat1080)
	m.write("Movies/"+heatRepack, 20)
	scanScope(t, sc, root, "Heat (1995)")
	only(t, s.vers(t, heatKey), v[0].ID, heatRepack)
}

func TestScopedScanKeepsFreshUntilFullScan(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080)
	root := l.Roots[0].ID
	scan(t, sc, root)
	v := s.vers(t, heatKey)
	if v[0].Fresh {
		t.Fatal("a version placed by a whole-root scan is still fresh")
	}
	// The old file sits outside the scanned scope, so it stays present.
	m.mv("Movies/"+heat1080, "Movies/Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv")
	scanScope(t, sc, root, "Heat (1995) {tmdb-949}")
	if n := s.versionCount(t); n != 2 {
		t.Fatalf("after the scoped scan %d versions, want 2", n)
	}
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	only(t, got, v[0].ID, "Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv")
	if got[0].Fresh || s.versionCount(t) != 1 {
		t.Fatalf("after the whole-root scan %+v (%d versions), want one, not fresh", got, s.versionCount(t))
	}
}

// twoCopies imports two 1080p files of Heat through a scoped scan, so both
// stay fresh, and returns them in seq order.
func twoCopies(t *testing.T) (*Store, *media, *Scanner, Library, vrow, vrow) {
	t.Helper()
	s, m, sc, l := movieLib(t, heat1080, heatRepack)
	scanScope(t, sc, l.Roots[0].ID, "Heat (1995)")
	v := s.vers(t, heatKey)
	if len(v) != 2 || !v[0].Fresh || !v[1].Fresh || v[1].Of != v[0].ID {
		t.Fatalf("versions %+v, want two fresh, the second flagged against the first", v)
	}
	return s, m, sc, l, v[0], v[1]
}

func TestNewerCopyDeletedWhileOlderFreshAdoptsNothing(t *testing.T) {
	s, m, sc, l, v1, v2 := twoCopies(t)
	m.rm("Movies/" + v2.Path)
	scanScope(t, sc, l.Roots[0].ID, "Heat (1995)")
	got := s.vers(t, heatKey)
	if len(got) != 2 || got[0].ID != v1.ID || got[0].Path != v1.Path || got[0].Ord != 1 || got[0].Of != "" || got[1].ID != v2.ID {
		t.Fatalf("versions %+v, want both kept as they were", got)
	}
	if f := vflags(t, s, l.ID); len(f) != 0 {
		t.Fatalf("flags %+v, want none listed (the copy is missing)", f)
	}
}

func TestOlderCopyDeletedPairsCleanly(t *testing.T) {
	s, m, sc, l, v1, v2 := twoCopies(t)
	m.rm("Movies/" + v1.Path)
	scanScope(t, sc, l.Roots[0].ID, "Heat (1995)")
	only(t, s.vers(t, heatKey), v1.ID, v2.Path)
	noBadFlags(t, s)
}

func TestAdoptionNeedsOneToOnePairing(t *testing.T) {
	t.Run("two new files", func(t *testing.T) {
		s, m, sc, l := movieLib(t, heat1080)
		root := l.Roots[0].ID
		scan(t, sc, root)
		old := s.vers(t, heatKey)[0]
		m.rm("Movies/" + heat1080)
		m.write("Movies/"+heatRepack, 10)
		m.write("Movies/"+heatProper, 10)
		scan(t, sc, root)
		got := s.vers(t, heatKey)
		if len(got) != 3 || got[0].ID != old.ID || got[0].Path != heat1080 {
			t.Fatalf("versions %+v, want the old one untouched beside two new", got)
		}
	})
	t.Run("no new file", func(t *testing.T) {
		s, m, sc, l := movieLib(t, heat1080, heatRepack)
		root := l.Roots[0].ID
		scan(t, sc, root)
		before := s.vers(t, heatKey)
		m.rm("Movies/" + before[1].Path)
		scan(t, sc, root)
		got := s.vers(t, heatKey)
		if len(got) != 2 || got[0].ID != before[0].ID || got[0].Path != before[0].Path || got[1].ID != before[1].ID || got[1].Path != before[1].Path {
			t.Fatalf("versions %+v, want %+v unchanged", got, before)
		}
	})
}

func TestInPlaceReplacementKeepsVersion(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080)
	root := l.Roots[0].ID
	scan(t, sc, root)
	id := s.vers(t, heatKey)[0].ID
	m.write("Movies/"+heat1080, 30)
	scan(t, sc, root)
	only(t, s.vers(t, heatKey), id, heat1080)
}

func TestSameLabelKeepsBothAndFlags(t *testing.T) {
	s, _, sc, l := movieLib(t, heat1080, heatRepack)
	scan(t, sc, l.Roots[0].ID)
	v := s.vers(t, heatKey)
	if len(v) != 2 || v[0].Ord != 1 || v[0].Of != "" || v[1].Ord != 2 || v[1].Of != v[0].ID || v[1].State != "flagged" {
		t.Fatalf("versions %+v, want ordinals 1 and 2, the second flagged", v)
	}
	f := vflags(t, s, l.ID)
	if len(f) != 1 || f[0].VersionID != v[1].ID || f[0].OfID != v[0].ID || f[0].Title != "Heat" || f[0].Label == nil || *f[0].Label != "1080p" {
		t.Fatalf("flags %+v", f)
	}
	if u := s.unplaced(t); len(u) != 0 {
		t.Fatalf("unplaced %v", u)
	}
}

func TestRenamedSecondCopyReconciles(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080, heatRepack)
	root := l.Roots[0].ID
	scan(t, sc, root)
	v := s.vers(t, heatKey)
	m.mv("Movies/"+v[1].Path, "Movies/"+heatProper)
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	if len(got) != 2 || got[0].ID != v[0].ID || got[1].ID != v[1].ID || got[1].Path != heatProper || got[1].Ord != 2 || got[1].Of != v[0].ID {
		t.Fatalf("versions %+v, want the second copy's version on %s, still flagged", got, heatProper)
	}
}

// grp places versions on one movie directly through version, with no scan,
// so the state at placement can be checked before any adoption.
type grp struct {
	t             *testing.T
	s             *Store
	lib, root, it string
	n             int
}

func newGrp(t *testing.T) *grp {
	t.Helper()
	s, _ := newStore(t)
	l, err := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	g := &grp{t: t, s: s, lib: l.ID, root: l.Roots[0].ID}
	g.it = g.item(heatKey)
	return g
}

func (g *grp) item(key string) string {
	g.t.Helper()
	ctx := context.Background()
	tx, err := g.s.db.BeginTx(ctx, nil)
	if err != nil {
		g.t.Fatal(err)
	}
	defer tx.Rollback()
	id, err := g.s.item(ctx, tx, g.lib, item{kind: "movie", key: key, title: "Heat", year: 1995})
	if err != nil {
		g.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		g.t.Fatal(err)
	}
	return id
}

// add places a new present file on item with label and returns its version.
// path defaults to a unique name.
func (g *grp) add(item, label, path string) string {
	g.t.Helper()
	g.n++
	if path == "" {
		path = "Heat (1995)/" + itoa(g.n) + ".mkv"
	}
	ctx := context.Background()
	tx, err := g.s.db.BeginTx(ctx, nil)
	if err != nil {
		g.t.Fatal(err)
	}
	defer tx.Rollback()
	fileID := newID("file")
	if _, err := tx.Exec(`INSERT INTO library_files(id, root_id, path, size, mtime_ns) VALUES(?, ?, ?, 10, 0)`, fileID, g.root, path); err != nil {
		g.t.Fatal(err)
	}
	if err := g.s.version(ctx, tx, item, fileID, label); err != nil {
		g.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		g.t.Fatal(err)
	}
	var id string
	if err := g.s.db.QueryRow(`SELECT id FROM library_versions WHERE file_id = ?`, fileID).Scan(&id); err != nil {
		g.t.Fatal(err)
	}
	return id
}

func (g *grp) gone(versionID string) {
	g.t.Helper()
	if _, err := g.s.db.Exec(`UPDATE library_files SET state = 'missing' WHERE id = (SELECT file_id FROM library_versions WHERE id = ?)`, versionID); err != nil {
		g.t.Fatal(err)
	}
}

// at returns a version's ordinal and what it is flagged against.
func (g *grp) at(versionID string) (int, string) {
	g.t.Helper()
	var ord int
	var of string
	if err := g.s.db.QueryRow(`SELECT v.ordinal, COALESCE(f.of_id, '') FROM library_versions v
		LEFT JOIN library_version_flags f ON f.version_id = v.id WHERE v.id = ?`, versionID).Scan(&ord, &of); err != nil {
		g.t.Fatal(err)
	}
	return ord, of
}

func (g *grp) merge(into, from string) {
	g.t.Helper()
	ctx := context.Background()
	tx, err := g.s.db.BeginTx(ctx, nil)
	if err != nil {
		g.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := moveInto(ctx, tx, into, from); err != nil {
		g.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		g.t.Fatal(err)
	}
}

func TestFlagTargetPrefersPresentVersion(t *testing.T) {
	t.Run("placement", func(t *testing.T) {
		g := newGrp(t)
		v1 := g.add(g.it, "1080p", "")
		v2 := g.add(g.it, "1080p", "")
		g.gone(v1)
		v3 := g.add(g.it, "1080p", "")
		if ord, of := g.at(v3); ord != 3 || of != v2 {
			t.Fatalf("v3 ordinal %d against %s, want 3 against v2 %s", ord, of, v2)
		}
		f := vflags(t, g.s, g.lib)
		if len(f) != 1 || f[0].VersionID != v3 || f[0].Title != "Heat" {
			t.Fatalf("flags %+v, want v3 listed", f)
		}
	})
	t.Run("placement fallback", func(t *testing.T) {
		g := newGrp(t)
		v1 := g.add(g.it, "1080p", "")
		v2 := g.add(g.it, "1080p", "")
		g.gone(v1)
		g.gone(v2)
		if _, of := g.at(g.add(g.it, "1080p", "")); of != v1 {
			t.Fatalf("flagged against %s, want v1 %s", of, v1)
		}
	})
	for _, v2Gone := range []bool{false, true} {
		// Distinct names: a "#01" suffix would end up in the database URI.
		t.Run("renumber "+map[bool]string{false: "present", true: "fallback"}[v2Gone], func(t *testing.T) {
			g := newGrp(t)
			v1 := g.add(g.it, "1080p", "")
			v2 := g.add(g.it, "1080p", "")
			g.gone(v1)
			if v2Gone {
				g.gone(v2)
			}
			other := g.item("dup:x")
			v3 := g.add(other, "1080p", "")
			g.merge(g.it, other)
			want := v2
			if v2Gone {
				want = v1
			}
			if ord, of := g.at(v3); ord != 3 || of != want {
				t.Fatalf("merged version ordinal %d against %s, want 3 against %s", ord, of, want)
			}
		})
	}
	t.Run("backfill", func(t *testing.T) {
		g := newGrp(t)
		d := g.add(g.it, "1080p", heat1080)
		p1 := g.add(g.it, "1080p", heatRepack)
		p2 := g.add(g.it, "1080p", heatBluray)
		g.gone(d)
		resetVersions(t, g.s)
		if _, err := g.s.BackfillVersions(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p2 < p1 {
			p1, p2 = p2, p1 // the backfill numbers present versions by ID
		}
		for _, c := range []struct {
			id, of string
			ord    int
		}{{d, "", 1}, {p1, d, 2}, {p2, p1, 3}} {
			if ord, of := g.at(c.id); ord != c.ord || of != c.of {
				t.Fatalf("%s ordinal %d against %q, want %d against %q", c.id, ord, of, c.ord, c.of)
			}
		}
		noBadFlags(t, g.s)
	})
	t.Run("leave group", func(t *testing.T) {
		g := newGrp(t)
		d := g.add(g.it, "1080p", "")
		p1 := g.add(g.it, "1080p", "")
		g.gone(d)
		p2 := g.add(g.it, "1080p", "")
		p3 := g.add(g.it, "1080p", "")
		if _, of := g.at(p3); of != p1 {
			t.Fatalf("p3 against %s, want p1", of)
		}
		if err := g.s.RelabelVersion(context.Background(), p1, "Director's Cut"); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			id, of string
			ord    int
		}{{d, "", 1}, {p1, "", 1}, {p2, d, 3}, {p3, p2, 4}} {
			if ord, of := g.at(c.id); ord != c.ord || of != c.of {
				t.Fatalf("%s ordinal %d against %q, want %d against %q", c.id, ord, of, c.ord, c.of)
			}
		}
		noBadFlags(t, g.s)
		if f := vflags(t, g.s, g.lib); len(f) != 1 || f[0].VersionID != p3 {
			t.Fatalf("flags %+v, want p3 only", f)
		}
	})
}

// resetVersions puts the versions back as they were before LIB-3.
func resetVersions(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`DELETE FROM library_version_flags`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE library_versions SET seq = 0, ordinal = 1, fresh = 0`); err != nil {
		t.Fatal(err)
	}
}

func TestAmbiguousAdoptionMakesNewVersion(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080, heatRepack)
	root := l.Roots[0].ID
	scan(t, sc, root)
	before := s.vers(t, heatKey)
	m.rm("Movies/" + heat1080)
	m.rm("Movies/" + heatRepack)
	m.write("Movies/"+heatBluray, 10)
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	if len(got) != 3 || got[2].Path != heatBluray || got[2].Ord != 3 {
		t.Fatalf("versions %+v, want a third, ordinal 3", got)
	}
	for i := range 2 {
		if got[i].ID != before[i].ID || got[i].Path != before[i].Path || got[i].Ord != before[i].Ord {
			t.Fatalf("old version %d changed: %+v, was %+v", i, got[i], before[i])
		}
	}
}

func TestResolutionUpgradeKeepsVersionID(t *testing.T) {
	ep := "show:show|2019|s1|e1"
	for _, c := range []struct {
		typ, folder, key, from, to, label string
	}{
		{"movies", "Movies", heatKey, "Heat (1995)/Heat (1995) - 720p.mkv", heat1080, "1080p"},
		{"movies", "Movies", heatKey, "Heat (1995)/Heat (1995) - DVD.mkv", heatBluray, "1080p"},
		{"tv", "TV", ep, "Show (2019)/Season 01/Show - S01E01 - 720p.mkv", "Show (2019)/Season 01/Show - S01E01 - 1080p.mkv", "1080p"},
		{"tv", "TV", ep, "Show (2019)/Season 01/Show - S01E01.mkv", "Show (2019)/Season 01/Show - S01E01 - 1080p.mkv", "1080p"},
		{"tv", "TV", ep, "Show (2019)/Season 01/Show - S01E01 - 1080i.mkv", "Show (2019)/Season 01/Show - S01E01 - 1080p.mkv", "1080p"},
	} {
		t.Run(c.from, func(t *testing.T) {
			s, m, sc, l := lib(t, c.typ, c.folder, c.from)
			root := l.Roots[0].ID
			scan(t, sc, root)
			v := s.vers(t, c.key)
			if len(v) != 1 {
				t.Fatalf("versions %+v", v)
			}
			m.rm(c.folder + "/" + c.from)
			m.write(c.folder+"/"+c.to, 20)
			scan(t, sc, root)
			got := s.vers(t, c.key)
			only(t, got, v[0].ID, c.to)
			if got[0].Label != c.label {
				t.Fatalf("label %q, want %q", got[0].Label, c.label)
			}
		})
	}
}

// threeCopies scans three present 720p copies A, B (flagged against A) and C.
func threeCopies(t *testing.T, n int) (*Store, *media, *Scanner, Library, []vrow) {
	t.Helper()
	files := []string{"Heat (1995)/Heat (1995) - 720p.mkv", "Heat (1995)/Heat (1995) - 720p REPACK.mkv", "Heat (1995)/Heat (1995) - HDTV-720p.mkv"}[:n]
	s, m, sc, l := movieLib(t, files...)
	scan(t, sc, l.Roots[0].ID)
	v := s.vers(t, heatKey)
	if len(v) != n || v[0].Ord != 1 || v[1].Of != v[0].ID {
		t.Fatalf("versions %+v", v)
	}
	return s, m, sc, l, v
}

// checkLeft checks the group after A left it: B is the head, C (if any) is
// flagged against B, and a new 720p copy is flagged against a present version.
func checkLeft(t *testing.T, s *Store, m *media, sc *Scanner, l Library, v []vrow) {
	t.Helper()
	byID := map[string]vrow{}
	for _, x := range s.vers(t, heatKey) {
		byID[x.ID] = x
	}
	if b := byID[v[1].ID]; b.Ord != 1 || b.Of != "" {
		t.Fatalf("B %+v, want ordinal 1, no flag", b)
	}
	if len(v) == 3 {
		if c := byID[v[2].ID]; c.Ord != 3 || c.Of != v[1].ID {
			t.Fatalf("C %+v, want ordinal 3 against B", c)
		}
	}
	noBadFlags(t, s)
	m.write("Movies/Heat (1995)/Heat (1995) - WEBDL-720p.mkv", 10)
	scan(t, sc, l.Roots[0].ID)
	for _, x := range s.vers(t, heatKey) {
		if x.Path == "Heat (1995)/Heat (1995) - WEBDL-720p.mkv" && x.Of != v[1].ID {
			t.Fatalf("new copy %+v, want flagged against B", x)
		}
	}
}

func TestAdoptionOutOfGroupRepointsFlags(t *testing.T) {
	for _, n := range []int{3, 2} {
		s, m, sc, l, v := threeCopies(t, n)
		m.rm("Movies/" + v[0].Path)
		m.write("Movies/"+heat1080, 10)
		scan(t, sc, l.Roots[0].ID)
		var a vrow
		for _, x := range s.vers(t, heatKey) {
			if x.ID == v[0].ID {
				a = x
			}
		}
		if a.Path != heat1080 || a.Label != "1080p" || a.Ord != 1 || a.Of != "" {
			t.Fatalf("A %+v, want adopted onto the 1080p file", a)
		}
		checkLeft(t, s, m, sc, l, v)
	}
}

func TestRelabelOutOfGroupRepointsFlags(t *testing.T) {
	for _, n := range []int{3, 2} {
		s, m, sc, l, v := threeCopies(t, n)
		if err := s.RelabelVersion(context.Background(), v[0].ID, "Director's Cut"); err != nil {
			t.Fatal(err)
		}
		checkLeft(t, s, m, sc, l, v)
	}
}

func TestAdoptionNeverFlagsAVersionAgainstItself(t *testing.T) {
	t.Run("same label", func(t *testing.T) {
		s, m, sc, l := movieLib(t, heat1080, heatRepack)
		root := l.Roots[0].ID
		scan(t, sc, root)
		v := s.vers(t, heatKey)
		m.rm("Movies/" + v[1].Path)
		m.write("Movies/"+heatBluray, 10)
		scan(t, sc, root)
		got := s.vers(t, heatKey)
		if len(got) != 2 || got[1].ID != v[1].ID || got[1].Path != heatBluray || got[1].Ord != 2 || got[1].Of != v[0].ID {
			t.Fatalf("versions %+v, want the flagged copy adopted, still ordinal 2 against the first", got)
		}
		noBadFlags(t, s)
	})
	t.Run("label changes", func(t *testing.T) {
		s, m, sc, l := movieLib(t, "Heat (1995)/Heat (1995) - 720p.mkv", "Heat (1995)/Heat (1995) - 720p REPACK.mkv")
		root := l.Roots[0].ID
		scan(t, sc, root)
		v := s.vers(t, heatKey)
		m.rm("Movies/" + v[1].Path)
		m.write("Movies/"+heat1080, 10)
		scan(t, sc, root)
		got := s.vers(t, heatKey)
		if len(got) != 2 || got[0].Ord != 1 || got[0].Of != "" || got[1].ID != v[1].ID || got[1].Label != "1080p" || got[1].Ord != 1 || got[1].Of != "" {
			t.Fatalf("versions %+v", got)
		}
		noBadFlags(t, s)
	})
}

func TestResolutionUpgradeIntoNumberedSibling(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080, "Heat (1995)/Heat (1995) - 720p.mkv")
	root := l.Roots[0].ID
	scan(t, sc, root)
	v := s.vers(t, heatKey)
	if len(v) != 2 || v[0].Label != "1080p" || v[1].Label != "720p" {
		t.Fatalf("versions %+v", v)
	}
	v3, n := v[0], v[1]
	m.rm("Movies/" + n.Path)
	m.write("Movies/"+heatRepack, 10)
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	if len(got) != 2 || got[0] != v3 {
		t.Fatalf("versions %+v, want %+v untouched", got, v3)
	}
	if x := got[1]; x.ID != n.ID || x.Path != heatRepack || x.Label != "1080p" || x.Ord != 2 || x.Of != v3.ID {
		t.Fatalf("adopted %+v, want N on the new file, 1080p, ordinal 2, against %s", x, v3.ID)
	}
	noBadFlags(t, s)
}

func TestRenamedFolderWithTwoResolutionsKeepsBothIDs(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080, "Heat (1995)/Heat (1995) - 2160p.mkv")
	root := l.Roots[0].ID
	scan(t, sc, root)
	before := map[string]string{}
	for _, x := range s.vers(t, heatKey) {
		before[x.Label] = x.ID
	}
	m.mv("Movies/Heat (1995)", "Movies/Heat (1995) {tmdb-949}")
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	if len(got) != 2 || s.versionCount(t) != 2 || countItems(t, s) != 1 {
		t.Fatalf("versions %+v, items %d", got, countItems(t, s))
	}
	for _, x := range got {
		if x.ID != before[x.Label] || x.Ord != 1 || x.Of != "" || !strings.HasPrefix(x.Path, "Heat (1995) {tmdb-949}/") {
			t.Fatalf("version %+v, want %s kept on the renamed folder", x, before[x.Label])
		}
	}
}

func TestCutChangeMakesNewVersion(t *testing.T) {
	s, m, sc, l := movieLib(t, "Heat (1995)/Heat (1995) {edition-Theatrical}.mkv")
	root := l.Roots[0].ID
	scan(t, sc, root)
	old := s.vers(t, heatKey)[0]
	m.rm("Movies/Heat (1995)/Heat (1995) {edition-Theatrical}.mkv")
	m.write("Movies/Heat (1995)/Heat (1995) {edition-Director's Cut}.mkv", 10)
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	if len(got) != 2 || got[0].ID != old.ID || got[0].Present || got[1].Label != "Director's Cut" {
		t.Fatalf("versions %+v, want the old cut missing beside the new one", got)
	}
}

func TestBackfillVersionsRecomputesLabels(t *testing.T) {
	s, _, sc, l := movieLib(t, "Heat (1995)/Heat (1995) - 4K.mkv", heatRepack, heatBluray)
	scan(t, sc, l.Roots[0].ID)
	resetVersions(t, s)
	if _, err := s.db.Exec(`UPDATE library_versions SET label = 'stale'`); err != nil {
		t.Fatal(err)
	}
	n, err := s.BackfillVersions(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("backfill = %d, %v", n, err)
	}
	got := map[string]vrow{}
	for _, x := range s.vers(t, heatKey) {
		got[x.Path] = x
	}
	if x := got["Heat (1995)/Heat (1995) - 4K.mkv"]; x.Label != "2160p" || x.Ord != 1 {
		t.Fatalf("4K %+v", x)
	}
	a, b := got[heatRepack], got[heatBluray]
	if a.Label != "1080p" || b.Label != "1080p" || a.Ord+b.Ord != 3 || a.Ord == b.Ord {
		t.Fatalf("1080p copies %+v %+v, want ordinals 1 and 2", a, b)
	}
}

func TestBackfillVersionsNumbersDeadPairFirst(t *testing.T) {
	for _, c := range []struct{ dead, live, label string }{
		{heat1080, heatBluray, "1080p"},
		{"Heat (1995)/Heat (1995) - 720p.mkv", heat1080, "1080p"},
	} {
		t.Run(c.dead, func(t *testing.T) {
			s, m, sc, l := movieLib(t, c.dead, c.live)
			root := l.Roots[0].ID
			scan(t, sc, root)
			// A rename made before LIB-3: the old file is missing, its version kept.
			resetVersions(t, s)
			for _, q := range []string{
				`UPDATE library_files SET state = 'missing' WHERE path = ?`,
				`UPDATE library_versions SET id = 'ver-z' WHERE file_id = (SELECT id FROM library_files WHERE path = ?)`,
			} {
				if _, err := s.db.Exec(q, c.dead); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`UPDATE library_versions SET id = 'ver-a' WHERE id != 'ver-z'`); err != nil {
				t.Fatal(err)
			}
			m.rm("Movies/" + c.dead)
			if _, err := s.BackfillVersions(context.Background()); err != nil {
				t.Fatal(err)
			}
			v := s.vers(t, heatKey)
			if len(v) != 2 || v[0].ID != "ver-z" || v[0].Seq != 1 || v[0].Ord != 1 || v[1].ID != "ver-a" || v[1].Seq != 2 || !v[1].Fresh {
				t.Fatalf("versions %+v, want the dead one first and the live one fresh", v)
			}
			if sameLabel(v[0].Label, v[1].Label) && (v[1].Ord != 2 || v[1].Of != "ver-z" || v[1].State != "flagged") {
				t.Fatalf("live copy %+v, want ordinal 2 flagged against the dead one", v[1])
			}
			if f := vflags(t, s, l.ID); len(f) != 0 {
				t.Fatalf("flags %+v, want none listed", f)
			}
			scan(t, sc, root)
			got := s.vers(t, heatKey)
			only(t, got, "ver-z", c.live)
			if got[0].Label != c.label {
				t.Fatalf("label %q, want %q", got[0].Label, c.label)
			}
		})
	}
}

func TestBackfillVersionsIsIdempotent(t *testing.T) {
	s, _, sc, l := movieLib(t, heat1080, heatRepack)
	scan(t, sc, l.Roots[0].ID)
	placed := s.vers(t, heatKey)
	ctx := context.Background()
	if n, err := s.BackfillVersions(ctx); err != nil || n != 0 {
		t.Fatalf("backfill of placed versions = %d, %v", n, err)
	}
	if got := s.vers(t, heatKey); !equalRows(got, placed) {
		t.Fatalf("placed versions changed: %+v, were %+v", got, placed)
	}
	resetVersions(t, s)
	if n, err := s.BackfillVersions(ctx); err != nil || n != 2 {
		t.Fatalf("backfill = %d, %v", n, err)
	}
	once := s.vers(t, heatKey)
	if n, err := s.BackfillVersions(ctx); err != nil || n != 0 {
		t.Fatalf("second backfill = %d, %v", n, err)
	}
	if got := s.vers(t, heatKey); !equalRows(got, once) {
		t.Fatalf("second backfill changed %+v to %+v", once, got)
	}
}

func equalRows(a, b []vrow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDecideMergeRenumbersSameLabel(t *testing.T) {
	for range 5 { // random version IDs: the order must not come from them
		s, _, sc, l := movieLib(t, heat1080, "Heat.1995/Heat.1995.1080p.mkv")
		scan(t, sc, l.Roots[0].ID)
		kept := s.vers(t, heatKey)
		d := dups(t, s, l.ID)
		if len(kept) != 1 || len(d) != 1 {
			t.Fatalf("versions %+v, duplicates %+v", kept, d)
		}
		if err := s.Decide(context.Background(), d[0].ID, true); err != nil {
			t.Fatal(err)
		}
		got := s.vers(t, heatKey)
		if len(got) != 2 || got[0].ID != kept[0].ID || got[0].Ord != 1 || got[0].Of != "" || got[1].Ord != 2 || got[1].Of != kept[0].ID {
			t.Fatalf("after merge %+v, want the folded-in version at ordinal 2 against %s", got, kept[0].ID)
		}
		if f := vflags(t, s, l.ID); len(f) != 1 {
			t.Fatalf("flags %+v", f)
		}
	}
}

func TestReturnedOriginalIsPlacedAgain(t *testing.T) {
	s, m, sc, l := movieLib(t, heat1080)
	root := l.Roots[0].ID
	scan(t, sc, root)
	id := s.vers(t, heatKey)[0].ID
	m.rm("Movies/" + heat1080)
	m.write("Movies/"+heatRepack, 20)
	scan(t, sc, root)
	m.write("Movies/"+heat1080, 10)
	scan(t, sc, root)
	got := s.vers(t, heatKey)
	if len(got) != 2 || got[0].ID != id || got[0].Path != heatRepack || got[0].Ord != 1 ||
		got[1].Path != heat1080 || got[1].Ord != 2 || got[1].Of != id {
		t.Fatalf("versions %+v, want the original placed again at ordinal 2", got)
	}
}

func TestKeepVersionsClearsFlag(t *testing.T) {
	s, _, sc, l := movieLib(t, heat1080, heatRepack)
	scan(t, sc, l.Roots[0].ID)
	v := s.vers(t, heatKey)
	ctx := context.Background()
	if err := s.KeepVersions(ctx, v[1].ID); err != nil {
		t.Fatal(err)
	}
	scan(t, sc, l.Roots[0].ID)
	if f := vflags(t, s, l.ID); len(f) != 0 || s.vers(t, heatKey)[1].State != "kept" {
		t.Fatalf("flags %+v after keeping", f)
	}
	if err := s.KeepVersions(ctx, v[1].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("keeping twice: %v", err)
	}
	if err := s.KeepVersions(ctx, "ver-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestRelabelResetsOrdinal(t *testing.T) {
	s, _, sc, l := movieLib(t, heat1080, heatRepack)
	scan(t, sc, l.Roots[0].ID)
	v := s.vers(t, heatKey)
	ctx := context.Background()
	if err := s.RelabelVersion(ctx, v[1].ID, "  Director's Cut "); err != nil {
		t.Fatal(err)
	}
	if x := s.vers(t, heatKey)[1]; x.Label != "Director's Cut" || x.Ord != 1 || x.Of != "" {
		t.Fatalf("relabelled %+v, want ordinal 1, no flag", x)
	}
	if err := s.RelabelVersion(ctx, v[1].ID, "1080P"); err != nil {
		t.Fatal(err)
	}
	if x := s.vers(t, heatKey)[1]; x.Label != "1080P" || x.Ord != 2 || x.Of != v[0].ID || x.State != "flagged" {
		t.Fatalf("relabelled to an existing label %+v, want ordinal 2 flagged", x)
	}
	if err := s.RelabelVersion(ctx, v[0].ID, "1080P"); err != nil { // the same label, folded: nothing changes
		t.Fatal(err)
	}
	if x := s.vers(t, heatKey)[0]; x.Label != "1080p" || x.Ord != 1 {
		t.Fatalf("same-label relabel changed %+v", x)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 91)} {
		if err := s.RelabelVersion(ctx, v[0].ID, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("label %q: %v", bad, err)
		}
	}
	if err := s.RelabelVersion(ctx, "ver-nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown version: %v", err)
	}
	long := strings.Repeat("é", 90)
	if err := s.RelabelVersion(ctx, v[1].ID, long); err != nil {
		t.Fatal(err)
	}
	if d := displayLabel(&long, 12); utf8.RuneCountInString(*d) > 100 || !strings.HasSuffix(*d, " (12)") {
		t.Fatalf("display %q", *d)
	}
}

func TestDisplayLabel(t *testing.T) {
	p := func(s string) *string { return &s }
	if displayLabel(nil, 1) != nil {
		t.Fatal("a null label at rank 1 must stay null")
	}
	for _, c := range []struct {
		label *string
		n     int
		want  string
	}{
		{nil, 2, "Version 2"},
		{p("1080p"), 2, "1080p (2)"},
		{p("1080p"), 1, "1080p"},
	} {
		if got := displayLabel(c.label, c.n); got == nil || *got != c.want {
			t.Fatalf("displayLabel(%v, %d) = %v, want %q", c.label, c.n, got, c.want)
		}
	}
	long := strings.Repeat("a", 100)
	if got := displayLabel(&long, 3); utf8.RuneCountInString(*got) != 100 || !strings.HasSuffix(*got, " (3)") {
		t.Fatalf("100-character label shows %q", *got)
	}
}

// #163: an upgrade delivered as two file-scoped scans, in either order.
func TestUpgradeAsTwoFileScopedScans(t *testing.T) {
	for _, newFirst := range []bool{true, false} {
		s, m, sc, l := movieLib(t, heat1080)
		root := l.Roots[0].ID
		scan(t, sc, root)
		id := s.vers(t, heatKey)[0].ID
		m.rm("Movies/" + heat1080)
		m.write("Movies/"+heatRepack, 20)
		scopes := []string{heatRepack, heat1080}
		if !newFirst {
			scopes[0], scopes[1] = scopes[1], scopes[0]
		}
		for _, sp := range scopes {
			scanScope(t, sc, root, sp)
		}
		only(t, s.vers(t, heatKey), id, heatRepack)
	}
}

// Controller ruling on Task 3: a moved version whose ordinal is free in into
// is still flagged at its target, so no flag points at a higher ordinal.
func TestRenumberKeepsFlagsBelow(t *testing.T) {
	g := newGrp(t)
	b := g.add(g.it, "1080p", "")
	gap := g.add(g.it, "1080p", "")
	c := g.add(g.it, "1080p", "")
	if err := g.s.RelabelVersion(context.Background(), gap, "Director's Cut"); err != nil { // into: B(1), C(3)
		t.Fatal(err)
	}
	if ord, _ := g.at(c); ord != 3 {
		t.Fatalf("C ordinal %d, want 3", ord)
	}
	other := g.item("dup:x")
	x := g.add(other, "1080p", "")
	f := g.add(other, "1080p", "") // F(2) against X(1)
	g.merge(g.it, other)
	if ord, of := g.at(x); ord != 4 || of != b {
		t.Fatalf("X ordinal %d against %s, want 4 against B", ord, of)
	}
	if ord, of := g.at(f); ord != 2 || of != b {
		t.Fatalf("F ordinal %d against %s, want 2 against B", ord, of)
	}
	var up int
	if err := g.s.db.QueryRow(`SELECT COUNT(*) FROM library_version_flags g JOIN library_versions v ON v.id = g.version_id
		JOIN library_versions o ON o.id = g.of_id WHERE o.ordinal >= v.ordinal`).Scan(&up); err != nil {
		t.Fatal(err)
	}
	if up != 0 {
		t.Fatalf("%d flags point at an equal or higher ordinal", up)
	}
	noBadFlags(t, g.s)
}
