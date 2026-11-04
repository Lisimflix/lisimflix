package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

var (
	grantAll  = Grant{All: true}
	grantNone = Grant{}
)

// benv is a catalogue with a media tree, a scanner and a scripted prober.
type benv struct {
	t  *testing.T
	s  *Store
	c  *clock
	m  *media
	sc *Scanner
}

func newBrowse(t *testing.T) *benv {
	t.Helper()
	s, c := newStore(t)
	m := newMedia(t)
	return &benv{t: t, s: s, c: c, m: m, sc: m.scanner(s)}
}

// library writes files under each folder, creates the library over the
// folders and scans every root.
func (e *benv) library(name, typ string, folders []string, files ...string) Library {
	e.t.Helper()
	for _, f := range files {
		e.m.write(f, 10)
	}
	for _, d := range folders {
		if err := os.MkdirAll(filepath.Join(e.m.dir, d), 0o755); err != nil {
			e.t.Fatal(err)
		}
	}
	lib, err := e.s.CreateLibrary(context.Background(), name, typ, folders)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range lib.Roots {
		e.scan(r.ID)
	}
	return lib
}

func (e *benv) scan(rootID string) Run { e.t.Helper(); return scan(e.t, e.sc, rootID) }

type resProber map[string]probe.Result // by file base name

func (p resProber) Probe(_ context.Context, _ *os.Root, rel string) (probe.Result, error) {
	return p[filepath.Base(rel)], nil
}

func (e *benv) probe(rootID string, p Prober) { e.t.Helper(); probeDue(e.t, e.sc, rootID, p) }

func (e *benv) itemID(title string) string {
	e.t.Helper()
	var id string
	if err := e.s.db.QueryRow(`SELECT id FROM library_items WHERE title = ?`, title).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func ids(l contracts.LibraryList) (out []string) {
	for _, x := range l.Items {
		out = append(out, x.Name)
	}
	return
}

func TestLibrariesListsGrantedOnly(t *testing.T) {
	e := newBrowse(t)
	b := e.library("Zoo", "movies", []string{"Z"}, "Z/A (2001)/A (2001).mkv")
	a := e.library("Alpha", "movies", []string{"A"}, "A/B (2002)/B (2002).mkv")
	ctx := context.Background()
	got, err := e.s.Libraries(ctx, Grant{LibraryIDs: []string{a.ID}}, "", 0)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"Alpha"}) {
		t.Fatalf("granted A: %v %v", ids(got), err)
	}
	if got, err = e.s.Libraries(ctx, grantNone, "", 0); err != nil || len(got.Items) != 0 || got.NextCursor != nil {
		t.Fatalf("none: %+v %v", got, err)
	}
	if got, err = e.s.Libraries(ctx, grantAll, "", 0); err != nil || !reflect.DeepEqual(ids(got), []string{"Alpha", "Zoo"}) {
		t.Fatalf("all: %v %v", ids(got), err)
	}
	p1, err := e.s.Libraries(ctx, grantAll, "", 1)
	if err != nil || p1.NextCursor == nil || ids(p1)[0] != "Alpha" {
		t.Fatalf("page 1: %+v %v", p1, err)
	}
	p2, err := e.s.Libraries(ctx, grantAll, *p1.NextCursor, 1)
	if err != nil || p2.NextCursor != nil || ids(p2)[0] != "Zoo" || p2.Items[0].ID != b.ID {
		t.Fatalf("page 2: %+v %v", p2, err)
	}
	if _, err := e.s.Libraries(ctx, grantAll, "junk", 1); !errorsIs(err, ErrBadRequest) {
		t.Fatalf("junk cursor: %v", err)
	}
	if _, err := e.s.Libraries(ctx, grantAll, "", 201); !errorsIs(err, ErrBadRequest) {
		t.Fatalf("limit 201: %v", err)
	}
}

func TestLibraryAvailabilityPartialAndOffline(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"M1", "M2"}, "M1/A (2001)/A (2001).mkv", "M2/B (2002)/B (2002).mkv")
	avail := func() string {
		l, err := e.s.Libraries(context.Background(), grantAll, "", 0)
		if err != nil || len(l.Items) != 1 {
			t.Fatalf("%v %v", l, err)
		}
		return l.Items[0].Availability
	}
	set := func(root, state string) {
		if _, err := e.s.db.Exec(`UPDATE library_roots SET state = ? WHERE id = ?`, state, root); err != nil {
			t.Fatal(err)
		}
	}
	if a := avail(); a != "available" {
		t.Fatalf("both up: %s", a)
	}
	set(lib.Roots[0].ID, "offline")
	if a := avail(); a != "partial" {
		t.Fatalf("one offline: %s", a)
	}
	set(lib.Roots[1].ID, "offline")
	if a := avail(); a != "offline" {
		t.Fatalf("both offline: %s", a)
	}
	set(lib.Roots[0].ID, "degraded")
	set(lib.Roots[1].ID, "degraded")
	if a := avail(); a != "available" {
		t.Fatalf("degraded: %s", a)
	}
}

func TestLibraryScanProgress(t *testing.T) {
	e := newBrowse(t)
	lib, err := e.s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.db.Exec(`INSERT INTO library_scan_runs(id, root_id, started_at, status, seen) VALUES('run-1', ?, 1, 'running', 120)`, lib.Roots[0].ID); err != nil {
		t.Fatal(err)
	}
	scanOf := func() contracts.LibraryScan {
		l, err := e.s.Libraries(context.Background(), grantAll, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		return l.Items[0].Scan
	}
	if got := scanOf(); got.State != "scanning" || got.FilesSeen != 120 || got.FilesTotal != nil {
		t.Fatalf("first scan: %+v", got)
	}
	if _, err := e.s.db.Exec(`UPDATE library_scan_runs SET status = 'interrupted' WHERE id = 'run-1'`); err != nil {
		t.Fatal(err)
	}
	e.m.write("Movies/A (2001)/A (2001).mkv", 10)
	e.scan(lib.Roots[0].ID)
	if got := scanOf(); got.State != "idle" || got.FilesSeen != 0 || got.FilesTotal != nil {
		t.Fatalf("after scan: %+v", got)
	}
}

func TestFacetsFromPresentFiles(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/Uhd (2020)/Uhd (2020).mkv", "Movies/Hd (1995)/Hd (1995).mkv", "Movies/Gone (2001)/Gone (2001).mkv")
	root := lib.Roots[0].ID
	vid := func(w, h int, hdr probe.HDRType) []probe.VideoStream {
		return []probe.VideoStream{{Index: 0, Codec: "hevc", Width: w, Height: h, HDR: hdr, Default: true}}
	}
	e.probe(root, resProber{
		"Uhd (2020).mkv":  {Container: "matroska", Video: vid(3840, 2160, probe.HDR10), Audio: []probe.AudioStream{{Index: 1, Language: "ger"}}},
		"Hd (1995).mkv":   {Container: "matroska", Video: vid(1920, 1080, ""), Audio: []probe.AudioStream{{Index: 1, Language: "eng"}}},
		"Gone (2001).mkv": {Container: "matroska", Video: vid(1280, 720, ""), Audio: []probe.AudioStream{{Index: 1, Language: "fre"}}},
	})
	if err := os.Remove(filepath.Join(e.m.dir, "Movies/Gone (2001)/Gone (2001).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(root)
	ctx := context.Background()
	f, err := e.s.Facets(ctx, grantAll, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := contracts.LibraryFacets{Genres: []string{}, Years: &contracts.YearRange{Min: 1995, Max: 2020},
		AudioLanguages: []string{"deu", "eng"}, Resolutions: []string{"4k", "1080p"}, HDR: true, SDR: true}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("facets %+v\nwant   %+v", f, want)
	}
	if err := e.s.SetDisplay(ctx, e.itemID("Hd"), Display{Genres: []string{"Drama"}}); err != nil {
		t.Fatal(err)
	}
	if f, _ = e.s.Facets(ctx, grantAll, lib.ID); !reflect.DeepEqual(f.Genres, []string{"Drama"}) {
		t.Fatalf("genres %v", f.Genres)
	}

	// An audio-only file stores hdr = 0 with no resolution: it must not make sdr true.
	e2 := newBrowse(t)
	l2 := e2.library("Movies", "movies", []string{"Movies"}, "Movies/Song (2010)/Song (2010).mkv", "Movies/Uhd (2020)/Uhd (2020).mkv")
	e2.probe(l2.Roots[0].ID, resProber{
		"Song (2010).mkv": {Container: "matroska", Audio: []probe.AudioStream{{Index: 0, Language: "eng"}}},
		"Uhd (2020).mkv":  {Container: "matroska", Video: vid(3840, 2160, probe.HDR10)},
	})
	f2, err := e2.s.Facets(ctx, grantAll, l2.ID)
	if err != nil || !f2.HDR || f2.SDR || !reflect.DeepEqual(f2.Resolutions, []string{"4k"}) {
		t.Fatalf("audio-only: %+v %v", f2, err)
	}
	// Nothing probed and no year: empty facets, years null.
	e3 := newBrowse(t)
	l3 := e3.library("Movies", "movies", []string{"Movies"}, "Movies/Nameless/Nameless.mkv")
	if f3, err := e3.s.Facets(ctx, grantAll, l3.ID); err != nil || f3.Years != nil || f3.HDR || f3.SDR || len(f3.Resolutions) != 0 {
		t.Fatalf("empty: %+v %v", f3, err)
	}
}

func TestHiddenLibraryIs404LikeUnknown(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, "Movies/A (2001)/A (2001).mkv")
	ctx := context.Background()
	_, e1 := e.s.Facets(ctx, grantNone, lib.ID)
	_, e2 := e.s.Facets(ctx, grantAll, "lib-unknown")
	_, e3 := e.s.Facets(ctx, Grant{LibraryIDs: []string{"lib-other"}}, lib.ID)
	if e1 != ErrNotFound || e2 != ErrNotFound || e3 != ErrNotFound {
		t.Fatalf("ungranted %v, unknown %v, other grant %v", e1, e2, e3)
	}
}

func TestM1Access(t *testing.T) {
	acc := M1Access(m1Viewer())
	if g := acc(contracts.Principal{AccountID: "acc", ProfileID: "prof"}); !g.All || !g.Admin {
		t.Fatalf("m1 viewer: %+v", g)
	}
	for _, p := range []contracts.Principal{{}, {AccountID: "other", ProfileID: "prof"}, {AccountID: "acc", ProfileID: "x"}} {
		if g := acc(p); g.All || g.Admin || len(g.LibraryIDs) != 0 {
			t.Fatalf("%+v got %+v", p, g)
		}
	}
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func m1Viewer() m1config.Viewer { return m1config.Viewer{AccountID: "acc", ProfileID: "prof"} }
