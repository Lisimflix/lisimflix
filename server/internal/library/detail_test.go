package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// filmFiles are a movie with two versions: an unlabelled 1080p file and a 4k
// HDR10 one labelled Director's Cut.
const (
	filmHD  = "Movies/Film (2001)/Film (2001).mkv"
	filmCut = "Movies/Film (2001)/Film (2001) - Director's Cut.mkv"
)

var filmProbes = resProber{
	"Film (2001).mkv": {Container: "matroska,webm", DurationMs: 5000,
		Video:     []probe.VideoStream{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, Default: true}},
		Audio:     []probe.AudioStream{{Index: 1, Codec: "aac", Channels: 2, Language: "eng", Default: true}},
		Subtitles: []probe.SubtitleStream{{Index: 2, Codec: "subrip", Language: "eng"}}},
	"Film (2001) - Director's Cut.mkv": {Container: "matroska,webm", DurationMs: 6000,
		Video: []probe.VideoStream{{Index: 0, Codec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDR10, Default: true}},
		Audio: []probe.AudioStream{{Index: 1, Codec: "truehd", Profile: "Dolby TrueHD + Dolby Atmos", Channels: 8, Language: "english", Title: "Main"}},
		Subtitles: []probe.SubtitleStream{{Index: 2, Codec: "hdmv_pgs_subtitle", Language: "und", Forced: true},
			{Index: 3, Codec: "ass", Language: "jpn", Title: "Signs"}}},
}

func filmLibrary(e *benv) Library {
	e.t.Helper()
	lib := e.library("Movies", "movies", []string{"Movies"}, filmHD, filmCut)
	e.probe(lib.Roots[0].ID, filmProbes)
	return lib
}

func (e *benv) detail(g Grant, id string) contracts.ItemDetail {
	e.t.Helper()
	d, err := e.s.Item(context.Background(), g, id)
	if err != nil {
		e.t.Fatalf("Item(%s): %v", id, err)
	}
	return d
}

// revisionOf is the file's sourceRevision as the catalogue records it now.
func (e *benv) revisionOf(fileID string) string {
	e.t.Helper()
	var size, mtime int64
	if err := e.s.db.QueryRow(`SELECT size, mtime_ns FROM library_files WHERE id = ?`, fileID).Scan(&size, &mtime); err != nil {
		e.t.Fatal(err)
	}
	return contracts.SourceRevision(fileID, size, mtime)
}

func TestItemDetailVersionsAndStreams(t *testing.T) {
	e := newBrowse(t)
	lib := filmLibrary(e)
	id := e.itemID("Film")
	d := e.detail(grantAll, id)

	if d.Item.ID != id || d.Item.Kind != contracts.KindMovie || d.Item.Available == nil || !*d.Item.Available {
		t.Fatalf("summary %+v", d.Item)
	}
	if len(d.Versions) != 2 {
		t.Fatalf("versions %+v", d.Versions)
	}
	// Both available: the unlabelled one first, though the cut is 4k.
	hd, cut := d.Versions[0], d.Versions[1]
	if hd.Label != nil || cut.Label == nil || *cut.Label != "Director's Cut" {
		t.Fatalf("order: %v then %v", hd.Label, cut.Label)
	}
	if d.Item.DurationMs == nil || *d.Item.DurationMs != 5000 {
		t.Fatalf("summary durationMs %v, want the default version's 5000", d.Item.DurationMs)
	}
	for i, v := range d.Versions {
		if v.SourceRevision != e.revisionOf(v.FileID) || v.Segment != nil || v.Container != "matroska,webm" ||
			v.SizeBytes == nil || *v.SizeBytes != 10 || !v.Available || v.FileName != nil || v.ID == "" || v.FileID == "" {
			t.Errorf("version %d: %+v", i, v)
		}
	}
	if hd.DurationMs == nil || *hd.DurationMs != 5000 || cut.DurationMs == nil || *cut.DurationMs != 6000 {
		t.Fatalf("durations %v %v", hd.DurationMs, cut.DurationMs)
	}
	if len(hd.Video) != 1 || hd.Video[0] != (contracts.VideoStream{Index: 0, Codec: "h264", Width: 1920, Height: 1080, HDR: contracts.HDRNone, Default: true}) {
		t.Fatalf("hd video %+v", hd.Video)
	}
	if len(hd.Audio) != 1 || hd.Audio[0].Language == nil || *hd.Audio[0].Language != "eng" || hd.Audio[0].Profile != nil {
		t.Fatalf("hd audio %+v", hd.Audio)
	}
	if len(hd.Subtitles) != 1 || hd.Subtitles[0].Kind != contracts.SubtitleText {
		t.Fatalf("hd subtitles %+v", hd.Subtitles)
	}
	if cut.Video[0].HDR != contracts.HDR10 || cut.Audio[0].Language != nil || cut.Audio[0].Channels != 8 ||
		cut.Audio[0].Profile == nil || *cut.Audio[0].Profile != "Dolby TrueHD + Dolby Atmos" {
		t.Fatalf("cut streams %+v %+v", cut.Video, cut.Audio)
	}
	if len(cut.Subtitles) != 2 || cut.Subtitles[0].Kind != contracts.SubtitleImage || cut.Subtitles[0].Language != nil ||
		!cut.Subtitles[0].Forced || cut.Subtitles[1].Kind != contracts.SubtitleASS || *cut.Subtitles[1].Language != "jpn" {
		t.Fatalf("cut subtitles %+v", cut.Subtitles)
	}
	if d.ExternalIDs == nil || len(d.ExternalIDs) != 0 || len(d.Genres) != 0 || d.Overview != nil || d.OriginalTitle != nil {
		t.Fatalf("descriptive fields %+v", d)
	}

	// The 1080p file goes missing: the cut is now the default and the only
	// version listed (LIB-3 decision 2 hides a missing version beside a present
	// one), and listItems agrees on durationMs.
	if err := os.Remove(filepath.Join(e.m.dir, filmHD)); err != nil {
		t.Fatal(err)
	}
	e.scan(lib.Roots[0].ID)
	d = e.detail(grantAll, id)
	if len(d.Versions) != 1 || d.Versions[0].FileID != cut.FileID || !d.Versions[0].Available {
		t.Fatalf("after missing: %+v", d.Versions)
	}
	if d.Item.DurationMs == nil || *d.Item.DurationMs != 6000 || d.Item.Available == nil || !*d.Item.Available {
		t.Fatalf("summary after missing %+v", d.Item)
	}
	listed := e.items(grantAll, ItemQuery{LibraryID: lib.ID}).Items
	if len(listed) != 1 || listed[0].DurationMs == nil || *listed[0].DurationMs != *d.Versions[0].DurationMs {
		t.Fatalf("listItems durationMs %+v", listed)
	}

	// Unprobed: no streams, an empty container, no duration.
	if err := e.s.RetryProbe(context.Background(), cut.FileID); err != nil {
		t.Fatal(err)
	}
	v := e.detail(grantAll, id).Versions[0]
	if v.Container != "" || v.DurationMs != nil || len(v.Video)+len(v.Audio)+len(v.Subtitles) != 0 {
		t.Fatalf("unprobed version %+v", v)
	}
}

func TestItemDetailHiddenAndUnknownAreNotFound(t *testing.T) {
	e := newBrowse(t)
	filmLibrary(e)
	tv := e.library("TV", "tv", []string{"TV"}, "TV/Show (2019)/Season 01/Show - S01E01.mkv")
	ctx := context.Background()
	for name, c := range map[string]struct {
		g  Grant
		id string
	}{
		"unknown":       {grantAll, "item-nope"},
		"no grant":      {grantNone, e.itemID("Film")},
		"other library": {Grant{LibraryIDs: []string{tv.ID}}, e.itemID("Film")},
	} {
		if _, err := e.s.Item(ctx, c.g, c.id); !errorsIs(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if d := e.detail(Grant{LibraryIDs: []string{tv.ID}}, e.itemID("Show")); d.Item.Title != "Show" {
		t.Fatalf("granted show %+v", d.Item)
	}
}

func TestFileNameOnlyForAdmins(t *testing.T) {
	e := newBrowse(t)
	filmLibrary(e)
	id := e.itemID("Film")
	want := []string{"Film (2001).mkv", "Film (2001) - Director's Cut.mkv"}
	for i, v := range e.detail(Grant{All: true, Admin: true}, id).Versions {
		if v.FileName == nil || *v.FileName != want[i] || strings.ContainsAny(*v.FileName, `/\`) {
			t.Errorf("admin version %d fileName %v", i, v.FileName)
		}
	}
	for i, v := range e.detail(Grant{All: true}, id).Versions {
		if v.FileName != nil {
			t.Errorf("viewer version %d fileName %q", i, *v.FileName)
		}
	}
}

func TestShowDetailHasNoVersions(t *testing.T) {
	e := newBrowse(t)
	e.library("TV", "tv", []string{"TV"}, "TV/Show (2019)/Season 01/Show - S01E01.mkv")
	show := e.itemID("Show")
	season := e.itemID("Season 1")
	for name, id := range map[string]string{"show": show, "season": season} {
		d := e.detail(grantAll, id)
		if d.Versions == nil || len(d.Versions) != 0 || d.Item.Available != nil || d.Item.Kind != name {
			t.Errorf("%s: versions %v available %v kind %s", name, d.Versions, d.Item.Available, d.Item.Kind)
		}
	}
	ep := e.detail(Grant{All: true, Admin: true}, e.itemID("Episode 1"))
	if len(ep.Versions) != 1 || !ep.Versions[0].Available || ep.Item.Available == nil || !*ep.Item.Available ||
		ep.Versions[0].FileName == nil || *ep.Versions[0].FileName != "Show - S01E01.mkv" {
		t.Fatalf("episode %+v %+v", ep.Item, ep.Versions)
	}
}

// A corrupt stored probe makes that version bare, not the whole item a 500.
func TestItemDetailCorruptProbeServesBareVersion(t *testing.T) {
	e := newBrowse(t)
	filmLibrary(e)
	id := e.itemID("Film")
	good := e.detail(grantAll, id)
	if len(good.Versions) != 2 {
		t.Fatalf("versions %+v", good.Versions)
	}
	bad := good.Versions[0].FileID
	if _, err := e.s.db.Exec(`UPDATE library_files SET probe_json = '{not json' WHERE id = ?`, bad); err != nil {
		t.Fatal(err)
	}
	d := e.detail(grantAll, id)
	if len(d.Versions) != 2 {
		t.Fatalf("versions %+v", d.Versions)
	}
	for _, v := range d.Versions {
		if v.FileID == bad {
			if v.Container != "" || len(v.Video)+len(v.Audio)+len(v.Subtitles) != 0 {
				t.Errorf("corrupt version not bare: %+v", v)
			}
		} else if v.Container == "" || len(v.Video) == 0 {
			t.Errorf("other version damaged: %+v", v)
		}
	}
}

// LIB-2c review: a retired file's version is gone for viewers. getItem leaves
// it out, and listItems never takes it as the default version, even when the
// remaining one is missing too.
func TestRetiredVersionIsLeftOut(t *testing.T) {
	e := newBrowse(t)
	lib := filmLibrary(e)
	root := lib.Roots[0].ID
	if err := os.Remove(filepath.Join(e.m.dir, filmHD)); err != nil {
		t.Fatal(err)
	}
	e.scan(root)
	if _, err := e.s.db.Exec(`UPDATE library_files SET state = 'retired' WHERE path = ?`, strings.TrimPrefix(filmHD, "Movies/")); err != nil {
		t.Fatal(err)
	}
	check := func(stage string, available bool) {
		t.Helper()
		vs := e.detail(grantAll, e.itemID("Film")).Versions
		if len(vs) != 1 || vs[0].Label == nil || *vs[0].Label != "Director's Cut" {
			t.Fatalf("%s: versions %+v", stage, vs)
		}
		p := e.items(grantAll, ItemQuery{LibraryID: lib.ID})
		if len(p.Items) != 1 || p.Items[0].DurationMs == nil || *p.Items[0].DurationMs != 6000 {
			t.Fatalf("%s: durationMs %+v", stage, p.Items)
		}
		if a := p.Items[0].Available; a == nil || *a != available {
			t.Fatalf("%s: available %v", stage, a)
		}
	}
	check("cut present", true)
	// The cut goes missing: the retired unlabelled version still doesn't count.
	if err := os.Remove(filepath.Join(e.m.dir, filmCut)); err != nil {
		t.Fatal(err)
	}
	e.scan(root)
	check("cut missing", false)
}

// versionsOf and extrasOf run the two reads in one read transaction.
func (s *Store) versionsOf(t testing.TB, g Grant, itemID string) []contracts.MediaVersion {
	t.Helper()
	var out []contracts.MediaVersion
	if err := s.read(context.Background(), func(tx *sql.Tx) (err error) {
		out, err = s.itemVersions(context.Background(), tx, g, itemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func (s *Store) extrasOf(t testing.TB, g Grant, itemID string) []contracts.Extra {
	t.Helper()
	var out []contracts.Extra
	if err := s.read(context.Background(), func(tx *sql.Tx) (err error) {
		out, err = s.itemExtras(context.Background(), tx, g, itemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func labels(vs []contracts.MediaVersion) []string {
	out := []string{}
	for _, v := range vs {
		if v.Label == nil {
			out = append(out, "<nil>")
		} else {
			out = append(out, *v.Label)
		}
	}
	return out
}

const sharedEp = "TV/Show (2019)/Season 01/Show - S01E01-E02.mkv"

// sharedFile is a season with episodes 1 and 2 on one probed file of two
// 25-minute chapters. Episode 1's version is re-inserted so episode 2's row
// comes first: the read must order by episode number, not by row.
func sharedFile(t *testing.T) (*benv, string, string) {
	t.Helper()
	e := newBrowse(t)
	lib := e.library("TV", "tv", []string{"TV"}, sharedEp)
	e.probe(lib.Roots[0].ID, resProber{"Show - S01E01-E02.mkv": {Container: "matroska,webm", DurationMs: 3_000_000,
		Chapters: []probe.Chapter{{StartMs: 0, EndMs: 1_500_000}, {StartMs: 1_500_000, EndMs: 3_000_000}}}})
	ep1, ep2 := e.itemID("Episode 1"), e.itemID("Episode 2")
	if _, err := e.s.db.Exec(`CREATE TEMP TABLE v1 AS SELECT * FROM library_versions WHERE item_id = ?;
		DELETE FROM library_versions WHERE item_id = ?; INSERT INTO library_versions SELECT * FROM v1; DROP TABLE v1`, ep1, ep1); err != nil {
		t.Fatal(err)
	}
	return e, ep1, ep2
}

func TestItemVersionsSharedFile(t *testing.T) {
	e, ep1, ep2 := sharedFile(t)
	want := map[string]contracts.Segment{ep1: {StartMs: 0, EndMs: 1_500_000}, ep2: {StartMs: 1_500_000, EndMs: 3_000_000}}
	for id, w := range want {
		vs := e.s.versionsOf(t, grantAll, id)
		if len(vs) != 1 || vs[0].Segment == nil || *vs[0].Segment != w {
			t.Fatalf("%s: %+v, want segment %v", id, vs, w)
		}
	}
	// Episode 2 moves to another season: the file no longer holds two
	// episodes of one season, so neither gets a range.
	if _, err := e.s.db.Exec(`INSERT INTO library_items(id, library_id, kind, parent_id, scan_key, title, sort_title, idx, created_at)
		SELECT 'sea-2', library_id, 'season', parent_id, scan_key || '-2', 'Season 2', 'Season 2', 2, created_at FROM library_items WHERE title = 'Season 1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.db.Exec(`UPDATE library_items SET parent_id = 'sea-2' WHERE id = ?`, ep2); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ep1, ep2} {
		if vs := e.s.versionsOf(t, grantAll, id); len(vs) != 1 || vs[0].Segment != nil {
			t.Fatalf("%s across seasons: %+v", id, vs)
		}
	}
}

// Decision 6d: a stale probe still serves the container and streams but not
// the segment, which needs the probe to match the file's revision.
func TestItemVersionsStaleProbeHasNoSegment(t *testing.T) {
	e, ep1, _ := sharedFile(t)
	if _, err := e.s.db.Exec(`UPDATE library_files SET probe_size = probe_size + 1`); err != nil {
		t.Fatal(err)
	}
	vs := e.s.versionsOf(t, grantAll, ep1)
	if len(vs) != 1 || vs[0].Segment != nil || vs[0].Container != "matroska,webm" {
		t.Fatalf("stale probe: %+v", vs)
	}
}

func TestItemVersionsMovieHasNoSegment(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, filmHD)
	e.probe(lib.Roots[0].ID, resProber{"Film (2001).mkv": {Container: "matroska,webm", DurationMs: 3_000_000,
		Chapters: []probe.Chapter{{StartMs: 0, EndMs: 1_500_000}, {StartMs: 1_500_000, EndMs: 3_000_000}}}})
	vs := e.s.versionsOf(t, grantAll, e.itemID("Film"))
	if len(vs) != 1 || vs[0].Segment != nil || vs[0].Container == "" {
		t.Fatalf("movie: %+v", vs)
	}
}

// Decision 2: a cut change leaves the old cut's file missing; while the new
// one is present on an available root, only the new one is listed.
func TestItemVersionsHidesMissingWhenAPresentOneExists(t *testing.T) {
	const theatrical = "Movies/Film (2001)/Film (2001) {edition-Theatrical}.mkv"
	const cut = "Movies/Film (2001)/Film (2001) {edition-Director's Cut}.mkv"
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, theatrical)
	root := lib.Roots[0].ID
	e.m.rm(theatrical)
	e.m.write(cut, 10)
	e.scan(root)
	id := e.itemID("Film")
	if got := labels(e.s.versionsOf(t, grantAll, id)); !slices.Equal(got, []string{"Director's Cut"}) {
		t.Fatalf("root available: %v", got)
	}
	if got := labels(e.detail(grantAll, id).Versions); !slices.Equal(got, []string{"Director's Cut"}) {
		t.Fatalf("getItem: %v", got)
	}
	setRoot := func(state string) {
		t.Helper()
		if _, err := e.s.db.Exec(`UPDATE library_roots SET state = ? WHERE id = ?`, state, root); err != nil {
			t.Fatal(err)
		}
	}
	setRoot("offline")
	if vs := e.s.versionsOf(t, grantAll, id); len(vs) != 2 || vs[0].Available || vs[1].Available {
		t.Fatalf("root offline: %+v", vs)
	}
	setRoot("available")
	e.m.rm(cut)
	e.scan(root)
	if got := labels(e.s.versionsOf(t, grantAll, id)); len(got) != 2 {
		t.Fatalf("none present: %v", got)
	}
}

func TestItemVersionsLoneSurvivorShowsPlainLabel(t *testing.T) {
	t.Run("dead sibling", func(t *testing.T) {
		g := newGrp(t)
		v1 := g.add(g.it, "1080p", "")
		g.add(g.it, "1080p", "")
		g.gone(v1)
		if got := labels(g.s.versionsOf(t, grantAll, g.it)); !slices.Equal(got, []string{"1080p"}) {
			t.Fatalf("%v", got)
		}
	})
	t.Run("backfill case", func(t *testing.T) {
		g := newGrp(t)
		v1 := g.add(g.it, "1080p", "")
		v2 := g.add(g.it, "1080p", "")
		v3 := g.add(g.it, "1080p", "")
		g.gone(v1)
		g.gone(v2)
		vs := g.s.versionsOf(t, grantAll, g.it)
		if got := labels(vs); !slices.Equal(got, []string{"1080p"}) || vs[0].ID != v3 {
			t.Fatalf("%v", got)
		}
	})
	t.Run("two present", func(t *testing.T) {
		g := newGrp(t)
		v1 := g.add(g.it, "1080p", "")
		v2 := g.add(g.it, "1080p", "")
		check := func(first, second string) {
			t.Helper()
			vs := g.s.versionsOf(t, grantAll, g.it)
			if got := labels(vs); !slices.Equal(got, []string{"1080p", "1080p (2)"}) || vs[0].ID != first || vs[1].ID != second {
				t.Fatalf("%v %+v", got, vs)
			}
		}
		check(v1, v2)
		// The ordinal ranks before the ID: swap the ordinals, swap the order.
		if _, err := g.s.db.Exec(`UPDATE library_versions SET ordinal = 3 - ordinal WHERE item_id = ?`, g.it); err != nil {
			t.Fatal(err)
		}
		check(v2, v1)
	})
	t.Run("null label", func(t *testing.T) {
		g := newGrp(t)
		v1 := g.add(g.it, "", "")
		g.add(g.it, "", "")
		g.gone(v1)
		if got := labels(g.s.versionsOf(t, grantAll, g.it)); !slices.Equal(got, []string{"<nil>"}) {
			t.Fatalf("%v", got)
		}
	})
	t.Run("ascii fold", func(t *testing.T) {
		g := newGrp(t)
		a := g.add(g.it, "Édition", "")
		b := g.add(g.it, "édition", "")
		for _, v := range []string{a, b} {
			if ord, of := g.at(v); ord != 1 || of != "" {
				t.Fatalf("%s placed with ordinal %d against %q", v, ord, of)
			}
		}
		got := labels(g.s.versionsOf(t, grantAll, g.it))
		slices.Sort(got)
		if !slices.Equal(got, []string{"Édition", "édition"}) {
			t.Fatalf("%v", got)
		}
	})
}

const (
	showEp    = "TV/Show (2019)/Season 01/Show - S01E01.mkv"
	showExtra = "TV/Show (2019)/Featurettes/Making Of.mkv"
)

func TestItemExtrasForShow(t *testing.T) {
	e := newBrowse(t)
	e.library("TV", "tv", []string{"TV"}, showEp, showExtra)
	show := e.itemID("Show")
	xs := e.s.extrasOf(t, Grant{All: true, Admin: true}, show)
	if len(xs) != 1 {
		t.Fatalf("extras %+v", xs)
	}
	x := xs[0]
	if x.ID == "" || x.ID != x.Version.ID || x.Type != contracts.ExtraFeaturette || x.Title != "Making Of" ||
		!x.Version.Available || x.Version.FileID == "" || x.Version.SourceRevision != e.revisionOf(x.Version.FileID) ||
		x.Version.FileName == nil || *x.Version.FileName != "Making Of.mkv" {
		t.Fatalf("extra %+v", x)
	}
	if v := e.s.extrasOf(t, grantAll, show); len(v) != 1 || v[0].Version.FileName != nil {
		t.Fatalf("viewer extra %+v", v)
	}
	for _, title := range []string{"Season 1", "Episode 1"} {
		if xs := e.s.extrasOf(t, grantAll, e.itemID(title)); xs == nil || len(xs) != 0 {
			t.Fatalf("%s extras %+v", title, xs)
		}
	}
}

func TestItemExtrasOfflineRoot(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("TV", "tv", []string{"TV"}, showEp, showExtra)
	if _, err := e.s.db.Exec(`UPDATE library_roots SET state = 'offline' WHERE id = ?`, lib.Roots[0].ID); err != nil {
		t.Fatal(err)
	}
	if xs := e.s.extrasOf(t, grantAll, e.itemID("Show")); len(xs) != 1 || xs[0].Version.Available {
		t.Fatalf("offline extras %+v", xs)
	}
}

func TestItemExtrasSkipMissingAfterRename(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("TV", "tv", []string{"TV"}, showEp, showExtra)
	e.m.mv(showExtra, "TV/Show (2019)/Featurettes/Making-Of.mkv")
	e.scan(lib.Roots[0].ID)
	xs := e.s.extrasOf(t, grantAll, e.itemID("Show"))
	if len(xs) != 1 || xs[0].Title != "Making-Of" || !xs[0].Version.Available {
		t.Fatalf("extras after rename %+v", xs)
	}
	if _, err := e.s.db.Exec(`UPDATE library_files SET state = 'retired' WHERE state = 'missing'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.db.Exec(`UPDATE library_roots SET state = 'offline'`); err != nil {
		t.Fatal(err)
	}
	if xs := e.s.extrasOf(t, grantAll, e.itemID("Show")); len(xs) != 1 {
		t.Fatalf("retired extra listed on an offline root %+v", xs)
	}
}
