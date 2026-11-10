package library

import (
	"context"
	"os"
	"path/filepath"
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

	// The 1080p file goes missing: the cut is now the default, the missing
	// version stays listed as unavailable, and listItems agrees on durationMs.
	if err := os.Remove(filepath.Join(e.m.dir, filmHD)); err != nil {
		t.Fatal(err)
	}
	e.scan(lib.Roots[0].ID)
	d = e.detail(grantAll, id)
	if len(d.Versions) != 2 || d.Versions[0].FileID != cut.FileID || !d.Versions[0].Available || d.Versions[1].Available {
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
