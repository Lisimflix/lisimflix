package library

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

// items lists q and fails the test on an error.
func (e *benv) items(g Grant, q ItemQuery) contracts.ItemPage {
	e.t.Helper()
	p, err := e.s.Items(context.Background(), g, q)
	if err != nil {
		e.t.Fatalf("Items(%+v): %v", q, err)
	}
	return p
}

func titles(p contracts.ItemPage) []string {
	out := []string{}
	for _, x := range p.Items {
		out = append(out, x.Title)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func TestItemsNeedsExactlyOneScope(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, "Movies/A (2001)/A (2001).mkv")
	ctx := context.Background()
	for name, q := range map[string]ItemQuery{
		"neither": {},
		"both":    {LibraryID: lib.ID, ParentID: e.itemID("A")},
	} {
		if _, err := e.s.Items(ctx, grantAll, q); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
}

func TestItemsRejectsInapplicable(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("TV", "tv", []string{"TV"}, "TV/Show (2019)/Season 01/Show - S01E01.mkv")
	show := e.itemID("Show")
	ctx := context.Background()
	for name, q := range map[string]ItemQuery{
		"from with added":        {LibraryID: lib.ID, Sort: "added", SortGiven: true, From: "A"},
		"from with year":         {LibraryID: lib.ID, Sort: "year", SortGiven: true, From: "A"},
		"from not a letter":      {LibraryID: lib.ID, From: "ab"},
		"unknown sort":           {LibraryID: lib.ID, Sort: "rating", SortGiven: true},
		"genre with parent":      {ParentID: show, Genre: "Drama"},
		"resolution with parent": {ParentID: show, Resolution: "4k"},
		"hdr with parent":        {ParentID: show, HDR: ptr(true)},
		"year with parent":       {ParentID: show, YearFrom: ptr(2000)},
		"audio with parent":      {ParentID: show, AudioLanguage: "eng"},
		"from with parent":       {ParentID: show, From: "A"},
		"sort with parent":       {ParentID: show, Sort: "title", SortGiven: true},
		"yearFrom > yearTo":      {LibraryID: lib.ID, YearFrom: ptr(2001), YearTo: ptr(2000)},
		"watched true":           {LibraryID: lib.ID, Watched: ptr(true)},
		"watched false":          {LibraryID: lib.ID, Watched: ptr(false)},
		"unknown resolution":     {LibraryID: lib.ID, Resolution: "8k"},
		"limit 201":              {LibraryID: lib.ID, Limit: 201},
	} {
		if _, err := e.s.Items(ctx, grantAll, q); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
	// Omitting sort with parentId is fine: the chosen order.
	if _, err := e.s.Items(ctx, grantAll, ItemQuery{ParentID: show}); err != nil {
		t.Fatalf("children: %v", err)
	}
}

func TestTitleSortGroupsAndJump(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/2001 A Space Odyssey (1968)/2001 A Space Odyssey (1968).mkv",
		"Movies/ガンダム (1979)/ガンダム (1979).mkv",
		"Movies/Élite (2018)/Élite (2018).mkv",
		"Movies/The Matrix (1999)/The Matrix (1999).mkv",
		"Movies/Alien (1979)/Alien (1979).mkv",
		"Movies/Zodiac (2007)/Zodiac (2007).mkv")
	all := []string{"2001 A Space Odyssey", "ガンダム", "Alien", "Élite", "The Matrix", "Zodiac"}
	if got := titles(e.items(grantAll, ItemQuery{LibraryID: lib.ID})); !reflect.DeepEqual(got, all) {
		t.Fatalf("default sort %v\nwant %v", got, all)
	}
	if got := titles(e.items(grantAll, ItemQuery{LibraryID: lib.ID, Sort: "title", SortGiven: true})); !reflect.DeepEqual(got, all) {
		t.Fatalf("sort=title %v", got)
	}
	for from, want := range map[string][]string{"#": all, "A": all[2:], "E": all[3:], "M": all[4:], "N": all[5:]} {
		p := e.items(grantAll, ItemQuery{LibraryID: lib.ID, From: from})
		if got := titles(p); !reflect.DeepEqual(got, want) {
			t.Errorf("from=%s: %v, want %v", from, got, want)
		}
		if p.Total == nil || *p.Total != int64(len(want)) {
			t.Errorf("from=%s total %v", from, p.Total)
		}
	}
	// Paging after a jump continues from the jump.
	p1 := e.items(grantAll, ItemQuery{LibraryID: lib.ID, From: "E", Limit: 1})
	if got := titles(p1); !reflect.DeepEqual(got, []string{"Élite"}) || p1.NextCursor == nil {
		t.Fatalf("from=E page 1 %v", got)
	}
	p2 := e.items(grantAll, ItemQuery{LibraryID: lib.ID, From: "E", Limit: 5, Cursor: *p1.NextCursor})
	if got := titles(p2); !reflect.DeepEqual(got, []string{"The Matrix", "Zodiac"}) || p2.NextCursor != nil || p2.Total != nil {
		t.Fatalf("from=E page 2 %v %v %v", got, p2.NextCursor, p2.Total)
	}
}

func TestJumpPastTheEnd(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, "Movies/Alien (1979)/Alien (1979).mkv", "Movies/Yes (2004)/Yes (2004).mkv")
	p := e.items(grantAll, ItemQuery{LibraryID: lib.ID, From: "Z"})
	if p.Items == nil || len(p.Items) != 0 || p.NextCursor != nil || p.Total == nil || *p.Total != 0 {
		t.Fatalf("from=Z: %+v", p)
	}
}

func TestSortAddedAndYear(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, "Movies/Old (1950)/Old (1950).mkv")
	root := lib.Roots[0].ID
	e.c.t = e.c.t.Add(time.Hour)
	e.m.write("Movies/Nameless/Nameless.mkv", 10)
	e.m.write("Movies/New (2020)/New (2020).mkv", 10)
	e.m.write("Movies/Mid (1990)/Mid (1990).mkv", 10)
	e.scan(root)
	e.c.t = e.c.t.Add(time.Hour)
	e.m.write("Movies/Newest (1980)/Newest (1980).mkv", 10)
	e.scan(root)

	// The three added together tie on created_at and break by ID.
	mid := []string{"Nameless", "New", "Mid"}
	slices.SortFunc(mid, func(a, b string) int { return strings.Compare(e.itemID(a), e.itemID(b)) })
	want := append(append([]string{"Newest"}, mid...), "Old")
	added := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Sort: "added", SortGiven: true})
	if got := titles(added); !reflect.DeepEqual(got, want) {
		t.Fatalf("added %v, want %v", got, want)
	}
	if !added.Items[0].AddedAt.Equal(e.c.t) || !added.Items[4].AddedAt.Equal(e.c.t.Add(-2*time.Hour)) {
		t.Fatalf("addedAt %v %v", added.Items[0].AddedAt, added.Items[4].AddedAt)
	}
	// Paged one at a time, the order is the same.
	if got := pageAll(t, e, ItemQuery{LibraryID: lib.ID, Sort: "added", SortGiven: true, Limit: 1}); !reflect.DeepEqual(got, want) {
		t.Fatalf("added paged %v", got)
	}

	// SetDisplay's year wins over the parsed one.
	if err := e.s.SetDisplay(context.Background(), e.itemID("Old"), Display{Year: ptr(2021)}); err != nil {
		t.Fatal(err)
	}
	wantYear := []string{"Old", "New", "Mid", "Newest", "Nameless"}
	year := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Sort: "year", SortGiven: true})
	if got := titles(year); !reflect.DeepEqual(got, wantYear) {
		t.Fatalf("year %v, want %v", got, wantYear)
	}
	if *year.Items[0].Year != 2021 || year.Items[4].Year != nil {
		t.Fatalf("years %v %v", year.Items[0].Year, year.Items[4].Year)
	}
	if got := pageAll(t, e, ItemQuery{LibraryID: lib.ID, Sort: "year", SortGiven: true, Limit: 1}); !reflect.DeepEqual(got, wantYear) {
		t.Fatalf("year paged %v", got)
	}
}

// pageAll follows nextCursor to the end and returns every title in order.
func pageAll(t *testing.T, e *benv, q ItemQuery) []string {
	t.Helper()
	var out []string
	for range 1000 {
		p := e.items(grantAll, q)
		out = append(out, titles(p)...)
		if p.NextCursor == nil {
			return out
		}
		q.Cursor = *p.NextCursor
	}
	t.Fatal("cursor never ends")
	return nil
}

func TestFiltersMatchAnyPresentFile(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/Uhd (2020)/Uhd (2020).mkv", "Movies/Hd (1995)/Hd (1995).mkv",
		"Movies/Two (2010)/Two (2010) - 1080p.mkv", "Movies/Two (2010)/Two (2010) - 2160p.mkv",
		"Movies/Gone (2001)/Gone (2001).mkv")
	vid := func(w, h int, hdr probe.HDRType) []probe.VideoStream {
		return []probe.VideoStream{{Index: 0, Codec: "hevc", Width: w, Height: h, HDR: hdr, Default: true}}
	}
	aud := func(lang string) []probe.AudioStream { return []probe.AudioStream{{Index: 1, Language: lang}} }
	e.probe(lib.Roots[0].ID, resProber{
		"Uhd (2020).mkv":         {Container: "matroska", Video: vid(3840, 2160, probe.HDR10), Audio: aud("ger")},
		"Hd (1995).mkv":          {Container: "matroska", Video: vid(1920, 1080, ""), Audio: aud("eng")},
		"Two (2010) - 1080p.mkv": {Container: "matroska", DurationMs: 1000, Video: vid(1920, 1080, ""), Audio: aud("eng")},
		"Two (2010) - 2160p.mkv": {Container: "matroska", DurationMs: 2000, Video: vid(3840, 2160, ""), Audio: aud("fre")},
		"Gone (2001).mkv":        {Container: "matroska", Video: vid(3840, 2160, probe.HDR10), Audio: aud("jpn")},
	})
	// Gone's only file goes missing: it stays listed but matches no file filter.
	if err := os.Remove(filepath.Join(e.m.dir, "Movies/Gone (2001)/Gone (2001).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(lib.Roots[0].ID)
	if err := e.s.SetDisplay(context.Background(), e.itemID("Hd"), Display{Genres: []string{"Drama"}}); err != nil {
		t.Fatal(err)
	}
	// durationMs is the default version's: the 4k one, both being labelled.
	for _, x := range e.items(grantAll, ItemQuery{LibraryID: lib.ID}).Items {
		if want := map[string]int64{"Two": 2000}[x.Title]; (want == 0) != (x.DurationMs == nil) || (want != 0 && *x.DurationMs != want) {
			t.Errorf("%s durationMs %v", x.Title, x.DurationMs)
		}
	}
	for name, c := range map[string]struct {
		q    ItemQuery
		want []string
	}{
		"none":          {ItemQuery{}, []string{"Gone", "Hd", "Two", "Uhd"}},
		"4k":            {ItemQuery{Resolution: "4k"}, []string{"Two", "Uhd"}},
		"1080p":         {ItemQuery{Resolution: "1080p"}, []string{"Hd", "Two"}},
		"720p":          {ItemQuery{Resolution: "720p"}, []string{}},
		"hdr":           {ItemQuery{HDR: ptr(true)}, []string{"Uhd"}},
		"sdr":           {ItemQuery{HDR: ptr(false)}, []string{"Hd", "Two"}},
		"4k sdr":        {ItemQuery{Resolution: "4k", HDR: ptr(false)}, []string{"Two"}},
		"deu":           {ItemQuery{AudioLanguage: "deu"}, []string{"Uhd"}},
		"fra":           {ItemQuery{AudioLanguage: "fra"}, []string{"Two"}},
		"jpn":           {ItemQuery{AudioLanguage: "jpn"}, []string{}},
		"yearFrom":      {ItemQuery{YearFrom: ptr(2001)}, []string{"Gone", "Two", "Uhd"}},
		"yearTo":        {ItemQuery{YearTo: ptr(2001)}, []string{"Gone", "Hd"}},
		"year range":    {ItemQuery{YearFrom: ptr(2001), YearTo: ptr(2010)}, []string{"Gone", "Two"}},
		"genre":         {ItemQuery{Genre: "Drama"}, []string{"Hd"}},
		"genre unknown": {ItemQuery{Genre: "Western"}, []string{}},
		"genre and 4k":  {ItemQuery{Genre: "Drama", Resolution: "4k"}, []string{}},
	} {
		c.q.LibraryID = lib.ID
		p := e.items(grantAll, c.q)
		if got := titles(p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
		if p.Total == nil || *p.Total != int64(len(c.want)) {
			t.Errorf("%s: total %v", name, p.Total)
		}
	}

	// A show matches through any of its episodes.
	tv := e.library("TV", "tv", []string{"TV"},
		"TV/Show (2019)/Season 01/Show - S01E01.mkv", "TV/Show (2019)/Season 02/Show - S02E01.mkv",
		"TV/Other (2020)/Season 01/Other - S01E01.mkv")
	e.probe(tv.Roots[0].ID, resProber{
		"Show - S01E01.mkv":  {Container: "matroska", Video: vid(1920, 1080, ""), Audio: aud("eng")},
		"Show - S02E01.mkv":  {Container: "matroska", Video: vid(3840, 2160, probe.HDR10), Audio: aud("chi")},
		"Other - S01E01.mkv": {Container: "matroska", Video: vid(1920, 1080, ""), Audio: aud("eng")},
	})
	for name, c := range map[string]struct {
		q    ItemQuery
		want []string
	}{
		"4k":    {ItemQuery{Resolution: "4k"}, []string{"Show"}},
		"hdr":   {ItemQuery{HDR: ptr(true)}, []string{"Show"}},
		"sdr":   {ItemQuery{HDR: ptr(false)}, []string{"Other", "Show"}},
		"zho":   {ItemQuery{AudioLanguage: "zho"}, []string{"Show"}},
		"1080p": {ItemQuery{Resolution: "1080p"}, []string{"Other", "Show"}},
	} {
		c.q.LibraryID = tv.ID
		if got := titles(e.items(grantAll, c.q)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("tv %s: %v, want %v", name, got, c.want)
		}
	}
}

func TestChildrenInChosenOrder(t *testing.T) {
	e := newBrowse(t)
	tv := e.library("TV", "tv", []string{"TV"},
		"TV/Show (2019)/Season 00/Show - S00E01.mkv",
		"TV/Show (2019)/Season 02/Show - S02E01.mkv",
		"TV/Show (2019)/Season 01/Show - S01E01.mkv", "TV/Show (2019)/Season 01/Show - S01E02.mkv",
		"TV/Show (2019)/Season 01/Show - S01E03.mkv", "TV/Show (2019)/Season 01/Show - S01E04.mkv")
	show := e.itemID("Show")
	shows := e.items(grantAll, ItemQuery{LibraryID: tv.ID})
	if len(shows.Items) != 1 {
		t.Fatalf("shows %v", titles(shows))
	}
	s := shows.Items[0]
	if s.Kind != "show" || s.ChildCount == nil || *s.ChildCount != 3 || s.ShowTitle != nil || s.SeasonIndex != nil ||
		s.Available != nil || s.ParentID != nil || s.DurationMs != nil || s.Year == nil || *s.Year != 2019 {
		t.Fatalf("show %+v", s)
	}

	seasons := e.items(grantAll, ItemQuery{ParentID: show})
	if got := titles(seasons); !reflect.DeepEqual(got, []string{"Season 1", "Season 2", "Specials"}) {
		t.Fatalf("seasons %v", got)
	}
	if seasons.Total == nil || *seasons.Total != 3 {
		t.Fatalf("seasons total %v", seasons.Total)
	}
	for i, wantIdx := range []int{1, 2, 0} {
		x := seasons.Items[i]
		wantKids := int64(1)
		if wantIdx == 1 {
			wantKids = 4
		}
		if x.Kind != "season" || x.Index == nil || *x.Index != wantIdx || x.ParentID == nil || *x.ParentID != show ||
			x.ShowTitle == nil || *x.ShowTitle != "Show" || x.SeasonIndex != nil || x.ChildCount == nil || *x.ChildCount != wantKids ||
			x.Available != nil || x.DurationMs != nil {
			t.Errorf("season %d: %+v", i, x)
		}
	}
	if got := pageAll(t, e, ItemQuery{ParentID: show, Limit: 1}); !reflect.DeepEqual(got, []string{"Season 1", "Season 2", "Specials"}) {
		t.Fatalf("seasons paged %v", got)
	}

	// Episodes: by idx, then air_date, then ID; a NULL idx or air_date last.
	season := seasons.Items[0].ID
	set := func(title string, idx, air any) {
		if _, err := e.s.db.Exec(`UPDATE library_items SET idx = ?, air_date = ? WHERE parent_id = ? AND title = ?`, idx, air, season, title); err != nil {
			t.Fatal(err)
		}
	}
	set("Episode 1", nil, "2019-01-02")
	set("Episode 4", nil, nil)
	set("Episode 3", 3, "2019-01-01")
	want := []string{"Episode 2", "Episode 3", "Episode 1", "Episode 4"}
	eps := e.items(grantAll, ItemQuery{ParentID: season})
	if got := titles(eps); !reflect.DeepEqual(got, want) {
		t.Fatalf("episodes %v, want %v", got, want)
	}
	for _, x := range eps.Items {
		if x.Kind != "episode" || x.ParentID == nil || *x.ParentID != season || x.ShowTitle == nil || *x.ShowTitle != "Show" ||
			x.SeasonIndex == nil || *x.SeasonIndex != 1 || x.ChildCount != nil || x.Available == nil || !*x.Available {
			t.Errorf("episode %+v", x)
		}
	}
	if *eps.Items[0].Index != 2 || eps.Items[2].Index != nil {
		t.Fatalf("index %v %v", eps.Items[0].Index, eps.Items[2].Index)
	}
	if got := pageAll(t, e, ItemQuery{ParentID: season, Limit: 1}); !reflect.DeepEqual(got, want) {
		t.Fatalf("episodes paged %v", got)
	}
}

func TestPagingAndTotal(t *testing.T) {
	e := newBrowse(t)
	var files []string
	for i := range 120 {
		n := fmt.Sprintf("Movie %03d (2000)", i)
		files = append(files, "Movies/"+n+"/"+n+".mkv")
	}
	lib := e.library("Movies", "movies", []string{"Movies"}, files...)
	q := ItemQuery{LibraryID: lib.ID}
	seen := map[string]bool{}
	var sizes []int
	for page := 0; ; page++ {
		p := e.items(grantAll, q)
		sizes = append(sizes, len(p.Items))
		if page == 0 {
			if p.Total == nil || *p.Total != 120 {
				t.Fatalf("first page total %v", p.Total)
			}
		} else if p.Total != nil {
			t.Fatalf("page %d total %v", page, *p.Total)
		}
		for _, x := range p.Items {
			if seen[x.ID] {
				t.Fatalf("%s twice", x.Title)
			}
			seen[x.ID] = true
		}
		if p.NextCursor == nil {
			break
		}
		if len(*p.NextCursor) > maxCursor {
			t.Fatalf("cursor %d bytes", len(*p.NextCursor))
		}
		q.Cursor = *p.NextCursor
	}
	if !reflect.DeepEqual(sizes, []int{50, 50, 20}) || len(seen) != 120 {
		t.Fatalf("pages %v, %d items", sizes, len(seen))
	}
	// Exactly a page's worth: no cursor to an empty page.
	if p := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Limit: 200}); len(p.Items) != 120 || p.NextCursor != nil {
		t.Fatalf("limit 200: %d %v", len(p.Items), p.NextCursor)
	}
}

func TestCursorRejectedWhenTamperedOrReused(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/A (2001)/A (2001).mkv", "Movies/B (2002)/B (2002).mkv", "Movies/C (2003)/C (2003).mkv")
	ctx := context.Background()
	p := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Limit: 1})
	good := *p.NextCursor
	raw, err := base64.RawURLEncoding.DecodeString(good)
	if err != nil {
		t.Fatal(err)
	}
	v2 := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"v":1`, `"v":2`, 1)))
	wrongTypes := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"q":"` + queryHash(canonicalQuery(ItemQuery{LibraryID: lib.ID}, contracts.SortTitle)) + `","k":["x",1,2]}`))
	for name, c := range map[string]ItemQuery{
		"garbage":         {LibraryID: lib.ID, Cursor: "garbage!"},
		"truncated":       {LibraryID: lib.ID, Cursor: good[:len(good)-4]},
		"other version":   {LibraryID: lib.ID, Cursor: v2},
		"wrong key types": {LibraryID: lib.ID, Cursor: wrongTypes},
		"other genre":     {LibraryID: lib.ID, Genre: "Drama", Cursor: good},
		"other sort":      {LibraryID: lib.ID, Sort: "added", SortGiven: true, Cursor: good},
		"other from":      {LibraryID: lib.ID, From: "A", Cursor: good},
		"too long":        {LibraryID: lib.ID, Cursor: strings.Repeat("A", 513)},
	} {
		if _, err := e.s.Items(ctx, grantAll, c); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
	// The same query with another limit keeps its cursor.
	if p2 := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Limit: 5, Cursor: good}); !reflect.DeepEqual(titles(p2), []string{"B", "C"}) {
		t.Fatalf("other limit %v", titles(p2))
	}
	// So does sort=title given explicitly: it is the same query.
	if p2 := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Sort: "title", SortGiven: true, Cursor: good}); !reflect.DeepEqual(titles(p2), []string{"B", "C"}) {
		t.Fatalf("explicit sort %v", titles(p2))
	}
}

func TestLongTitleCursorCarriesOnlyTheID(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/Alien (1979)/Alien (1979).mkv", "Movies/Zodiac (2007)/Zodiac (2007).mkv", "Movies/Heat (1995)/Heat (1995).mkv")
	long := strings.Repeat("ガ", 200)
	ctx := context.Background()
	if err := e.s.SetDisplay(ctx, e.itemID("Heat"), Display{SortTitle: &long}); err != nil {
		t.Fatal(err)
	}
	q := ItemQuery{LibraryID: lib.ID, Limit: 1}
	p1 := e.items(grantAll, q)
	if got := titles(p1); !reflect.DeepEqual(got, []string{"Heat"}) || p1.NextCursor == nil {
		t.Fatalf("page 1 %v", got)
	}
	if n := len(*p1.NextCursor); n > maxCursor {
		t.Fatalf("cursor is %d bytes", n)
	}
	q.Limit = 5
	q.Cursor = *p1.NextCursor
	if got := titles(e.items(grantAll, q)); !reflect.DeepEqual(got, []string{"Alien", "Zodiac"}) {
		t.Fatalf("page 2 %v", got)
	}
	// An ID cursor for an item that's gone, or outside the listing, is a 400.
	e.library("Other", "movies", []string{"Other"}, "Other/Gone (2000)/Gone (2000).mkv")
	canon := canonicalQuery(ItemQuery{LibraryID: lib.ID}, contracts.SortTitle)
	for name, c := range map[string]string{
		"unknown":       encodeCursor(canon, "mov-unknown"),
		"other library": encodeCursor(canon, e.itemID("Gone")),
	} {
		if _, err := e.s.Items(ctx, grantAll, ItemQuery{LibraryID: lib.ID, Cursor: c}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
}

func TestCursorStableWhileScanAdds(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/Bravo (2001)/Bravo (2001).mkv", "Movies/Delta (2001)/Delta (2001).mkv",
		"Movies/Foxtrot (2001)/Foxtrot (2001).mkv", "Movies/Hotel (2001)/Hotel (2001).mkv")
	p1 := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Limit: 2})
	if got := titles(p1); !reflect.DeepEqual(got, []string{"Bravo", "Delta"}) {
		t.Fatalf("page 1 %v", got)
	}
	for _, n := range []string{"Alpha (2001)", "Charlie (2001)", "Echo (2001)", "Golf (2001)"} {
		e.m.write("Movies/"+n+"/"+n+".mkv", 10)
	}
	e.scan(lib.Roots[0].ID)
	rest := pageAll(t, e, ItemQuery{LibraryID: lib.ID, Limit: 2, Cursor: *p1.NextCursor})
	got := append(titles(p1), rest...)
	count := map[string]int{}
	for _, x := range got {
		count[x]++
	}
	for _, x := range []string{"Bravo", "Delta", "Foxtrot", "Hotel"} {
		if count[x] != 1 {
			t.Fatalf("%s seen %d times in %v", x, count[x], got)
		}
	}
	for x, n := range count {
		if n != 1 {
			t.Fatalf("%s seen %d times in %v", x, n, got)
		}
	}
	if !reflect.DeepEqual(rest, []string{"Echo", "Foxtrot", "Golf", "Hotel"}) {
		t.Fatalf("rest %v", rest)
	}
}

func TestCountsOnlyCountGrantedItems(t *testing.T) {
	e := newBrowse(t)
	a := e.library("A", "tv", []string{"A"}, "A/Mine (2019)/Season 01/Mine - S01E01.mkv")
	b := e.library("B", "tv", []string{"B"},
		"B/Theirs (2019)/Season 01/Theirs - S01E01.mkv", "B/Theirs (2019)/Season 01/Theirs - S01E02.mkv",
		"B/Theirs (2019)/Season 02/Theirs - S02E01.mkv", "B/Extra (2020)/Season 01/Extra - S01E01.mkv")
	ctx := context.Background()
	onlyA := Grant{LibraryIDs: []string{a.ID}}
	theirs := e.itemID("Theirs")
	for name, q := range map[string]ItemQuery{
		"ungranted parent":  {ParentID: theirs},
		"unknown parent":    {ParentID: "shw-unknown"},
		"ungranted library": {LibraryID: b.ID},
		"unknown library":   {LibraryID: "lib-unknown"},
	} {
		if _, err := e.s.Items(ctx, onlyA, q); err != ErrNotFound {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := e.s.Items(ctx, grantNone, ItemQuery{LibraryID: a.ID}); err != ErrNotFound {
		t.Errorf("no grant: %v", err)
	}
	p := e.items(onlyA, ItemQuery{LibraryID: a.ID})
	if p.Total == nil || *p.Total != 1 || len(p.Items) != 1 || *p.Items[0].ChildCount != 1 {
		t.Fatalf("A: %+v", p)
	}
	seasons := e.items(onlyA, ItemQuery{ParentID: e.itemID("Mine")})
	if *seasons.Total != 1 || *seasons.Items[0].ChildCount != 1 {
		t.Fatalf("A seasons: total %v child %v", *seasons.Total, *seasons.Items[0].ChildCount)
	}
	pb := e.items(grantAll, ItemQuery{LibraryID: b.ID})
	if *pb.Total != 2 || !reflect.DeepEqual(titles(pb), []string{"Extra", "Theirs"}) || *pb.Items[1].ChildCount != 2 {
		t.Fatalf("B: %v %v", titles(pb), *pb.Total)
	}
}

func TestOfflineRootItemsStayListedUnavailable(t *testing.T) {
	e := newBrowse(t)
	movies := e.library("Movies", "movies", []string{"Movies"}, "Movies/A (2001)/A (2001).mkv", "Movies/B (2002)/B (2002).mkv")
	tv := e.library("TV", "tv", []string{"TV"}, "TV/Show (2019)/Season 01/Show - S01E01.mkv")
	avail := func(p contracts.ItemPage) []any {
		var out []any
		for _, x := range p.Items {
			if x.Available == nil {
				out = append(out, nil)
			} else {
				out = append(out, *x.Available)
			}
		}
		return out
	}
	if got := avail(e.items(grantAll, ItemQuery{LibraryID: movies.ID})); !reflect.DeepEqual(got, []any{true, true}) {
		t.Fatalf("online %v", got)
	}
	// A missing file stays listed, unavailable.
	if err := os.Remove(filepath.Join(e.m.dir, "Movies/B (2002)/B (2002).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(movies.Roots[0].ID)
	if got := avail(e.items(grantAll, ItemQuery{LibraryID: movies.ID})); !reflect.DeepEqual(got, []any{true, false}) {
		t.Fatalf("B missing %v", got)
	}
	for _, r := range []string{movies.Roots[0].ID, tv.Roots[0].ID} {
		if _, err := e.s.db.Exec(`UPDATE library_roots SET state = 'offline' WHERE id = ?`, r); err != nil {
			t.Fatal(err)
		}
	}
	mp := e.items(grantAll, ItemQuery{LibraryID: movies.ID})
	if got := avail(mp); !reflect.DeepEqual(titles(mp), []string{"A", "B"}) || !reflect.DeepEqual(got, []any{false, false}) {
		t.Fatalf("offline movies %v %v", titles(mp), got)
	}
	shows := e.items(grantAll, ItemQuery{LibraryID: tv.ID})
	if got := avail(shows); !reflect.DeepEqual(got, []any{nil}) {
		t.Fatalf("show %v", got)
	}
	seasons := e.items(grantAll, ItemQuery{ParentID: shows.Items[0].ID})
	if got := avail(seasons); !reflect.DeepEqual(got, []any{nil}) {
		t.Fatalf("season %v", got)
	}
	eps := e.items(grantAll, ItemQuery{ParentID: seasons.Items[0].ID})
	if got := avail(eps); !reflect.DeepEqual(got, []any{false}) {
		t.Fatalf("episode %v", got)
	}
}

func TestCombinedFiltersMatchFilesIndependently(t *testing.T) {
	e := newBrowse(t)
	vid := func(w, h int) []probe.VideoStream {
		return []probe.VideoStream{{Index: 0, Codec: "hevc", Width: w, Height: h, Default: true}}
	}
	aud := func(lang string) []probe.AudioStream { return []probe.AudioStream{{Index: 1, Language: lang}} }
	lib := e.library("Movies", "movies", []string{"Movies"},
		"Movies/Mix (2015)/Mix (2015) - 2160p.mkv", "Movies/Mix (2015)/Mix (2015) - 1080p.mkv",
		"Movies/Plain (2016)/Plain (2016).mkv")
	e.probe(lib.Roots[0].ID, resProber{
		"Mix (2015) - 2160p.mkv": {Container: "matroska", Video: vid(3840, 2160), Audio: aud("eng")},
		"Mix (2015) - 1080p.mkv": {Container: "matroska", Video: vid(1920, 1080), Audio: aud("ger")},
		"Plain (2016).mkv":       {Container: "matroska", Video: vid(1920, 1080), Audio: aud("eng")},
	})
	tv := e.library("TV", "tv", []string{"TV"},
		"TV/Show (2019)/Season 01/Show - S01E01.mkv", "TV/Show (2019)/Season 02/Show - S02E01.mkv")
	e.probe(tv.Roots[0].ID, resProber{
		"Show - S01E01.mkv": {Container: "matroska", Video: vid(3840, 2160), Audio: aud("eng")},
		"Show - S02E01.mkv": {Container: "matroska", Video: vid(1920, 1080), Audio: aud("ger")},
	})
	// Each filter may be met by a different file (or episode).
	q := ItemQuery{Resolution: "4k", AudioLanguage: "deu"}
	for libID, want := range map[string][]string{lib.ID: {"Mix"}, tv.ID: {"Show"}} {
		q.LibraryID = libID
		p := e.items(grantAll, q)
		if got := titles(p); !reflect.DeepEqual(got, want) || *p.Total != 1 {
			t.Errorf("4k + deu: %v total %v, want %v", got, *p.Total, want)
		}
	}
	if got := titles(e.items(grantAll, ItemQuery{LibraryID: lib.ID, Resolution: "4k", HDR: ptr(false), AudioLanguage: "eng"})); !reflect.DeepEqual(got, []string{"Mix"}) {
		t.Errorf("4k + sdr + eng: %v", got)
	}
}

func TestParentMustBeShowOrSeason(t *testing.T) {
	e := newBrowse(t)
	movies := e.library("Movies", "movies", []string{"Movies"}, "Movies/A (2001)/A (2001).mkv")
	e.library("TV", "tv", []string{"TV"}, "TV/Show (2019)/Season 01/Show - S01E01.mkv")
	ctx := context.Background()
	for name, id := range map[string]string{"movie": e.itemID("A"), "episode": e.itemID("Episode 1")} {
		if _, err := e.s.Items(ctx, grantAll, ItemQuery{ParentID: id}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s parent: err = %v, want ErrBadRequest", name, err)
		}
	}
	// A movie the caller may not see is the same 404 as an unknown ID.
	other := Grant{LibraryIDs: []string{"lib-other"}}
	_, hidden := e.s.Items(ctx, other, ItemQuery{ParentID: e.itemID("A")})
	_, unknown := e.s.Items(ctx, grantAll, ItemQuery{ParentID: "mov-unknown"})
	if hidden != ErrNotFound || unknown != ErrNotFound {
		t.Fatalf("hidden %v, unknown %v (library %s)", hidden, unknown, movies.ID)
	}
}

func TestYearOutsideRangeIsNull(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, "Movies/Old (1950)/Old (1950).mkv", "Movies/New (2020)/New (2020).mkv",
		"Movies/None/None.mkv")
	if _, err := e.s.db.Exec(`UPDATE library_items SET year = 1500 WHERE title = 'Old'`); err != nil {
		t.Fatal(err)
	}
	// 1500 reads as no year: served null, sorted with the NULLs, never matched by a year filter.
	p := e.items(grantAll, ItemQuery{LibraryID: lib.ID, Sort: "year", SortGiven: true})
	nullFirst := []string{"None", "Old"}
	slices.SortFunc(nullFirst, func(a, b string) int { return strings.Compare(e.itemID(a), e.itemID(b)) })
	if got := titles(p); !reflect.DeepEqual(got, append([]string{"New"}, nullFirst...)) {
		t.Fatalf("year sort %v", got)
	}
	for _, x := range p.Items {
		if x.Title == "Old" && x.Year != nil {
			t.Fatalf("Old year %v", *x.Year)
		}
	}
	if got := titles(e.items(grantAll, ItemQuery{LibraryID: lib.ID, YearTo: ptr(2000)})); len(got) != 0 {
		t.Fatalf("yearTo 2000: %v", got)
	}
}
