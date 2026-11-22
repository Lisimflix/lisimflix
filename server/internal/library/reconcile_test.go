package library

import (
	"context"
	"slices"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// TestVersionCutReconciliation runs one catalogue through whole-root scans of
// a movies root and a TV root and checks versions, flags, extras and segments
// after each.
func TestVersionCutReconciliation(t *testing.T) {
	const (
		dir        = "Movies/Heat (1995)/"
		theatrical = dir + "Heat (1995) {edition-Theatrical}.mkv"
		cut        = dir + "Heat (1995) {edition-Director's Cut}.mkv"
		uhd        = dir + "Heat.1995.2160p.mkv"
	)
	ctx := context.Background()
	e := newBrowse(t)
	movies := e.library("Movies", "movies", []string{"Movies"}, theatrical, cut, uhd)
	root := movies.Roots[0].ID
	heat := e.itemID("Heat")
	byLabel := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, v := range e.s.versionsOf(t, grantAll, heat) {
			out[*v.Label] = v.ID
		}
		return out
	}

	// 1. Three versions from edition tags and a release name, none flagged.
	v := byLabel()
	if len(v) != 3 || v["Theatrical"] == "" || v["Director's Cut"] == "" || v["2160p"] == "" {
		t.Fatalf("step 1 labels %v", v)
	}
	if f := vflags(t, e.s, movies.ID); len(f) != 0 {
		t.Fatalf("step 1 flags %+v", f)
	}

	// 2. A REPACK with the label in another case replaces the Theatrical file:
	// adoption keeps the version ID.
	e.m.rm(theatrical)
	e.m.write(dir+"Heat (1995) {edition-theatrical} REPACK.mkv", 10)
	e.scan(root)
	vs := e.s.versionsOf(t, grantAll, heat)
	if len(vs) != 3 || !slices.ContainsFunc(vs, func(x contracts.MediaVersion) bool { return x.ID == v["Theatrical"] }) {
		t.Fatalf("step 2 versions %+v, want 3 keeping %s", vs, v["Theatrical"])
	}

	// 3. A second 2160p file is numbered and flagged; keeping it clears the flag.
	e.m.write(dir+"Heat (1995) - 4K.mkv", 10)
	e.scan(root)
	v = byLabel()
	if len(v) != 4 || v["2160p"] == "" || v["2160p (2)"] == "" {
		t.Fatalf("step 3 labels %v", v)
	}
	f := vflags(t, e.s, movies.ID)
	if len(f) != 1 || f[0].VersionID != v["2160p (2)"] || f[0].OfID != v["2160p"] {
		t.Fatalf("step 3 flags %+v", f)
	}
	if err := e.s.KeepVersions(ctx, f[0].VersionID); err != nil {
		t.Fatal(err)
	}
	if f := vflags(t, e.s, movies.ID); len(f) != 0 {
		t.Fatalf("step 3 flags after keep %+v", f)
	}

	// 4. A featurette folder and a trailer suffix are extras, not versions.
	e.m.write(dir+"Featurettes/Making Of.mkv", 10)
	e.m.write(dir+"Heat (1995)-trailer.mkv", 10)
	e.scan(root)
	if n := len(e.s.versionsOf(t, grantAll, heat)); n != 4 {
		t.Fatalf("step 4: %d versions, want 4", n)
	}
	xs := e.s.extrasOf(t, grantAll, heat)
	types := []string{}
	for _, x := range xs {
		types = append(types, x.Type)
	}
	slices.Sort(types)
	if want := []string{contracts.ExtraFeaturette, contracts.ExtraTrailer}; !slices.Equal(types, want) {
		t.Fatalf("step 4 extras %+v", xs)
	}

	// 5. A two-episode file with two 25-minute chapters gives each episode its
	// range; re-probed without chapters, neither has one.
	tv := e.library("TV", "tv", []string{"TV"}, sharedEp)
	tvRoot := tv.Roots[0].ID
	result := func(ch []probe.Chapter) resProber {
		return resProber{"Show - S01E01-E02.mkv": {Container: "matroska,webm", DurationMs: 3_000_000,
			Video:    []probe.VideoStream{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, Default: true}},
			Chapters: ch}}
	}
	e.probe(tvRoot, result([]probe.Chapter{{StartMs: 0, EndMs: 1_500_000}, {StartMs: 1_500_000, EndMs: 3_000_000}}))
	ep1, ep2 := e.itemID("Episode 1"), e.itemID("Episode 2")
	for id, w := range map[string]contracts.Segment{ep1: {StartMs: 0, EndMs: 1_500_000}, ep2: {StartMs: 1_500_000, EndMs: 3_000_000}} {
		if vs := e.s.versionsOf(t, grantAll, id); len(vs) != 1 || vs[0].Segment == nil || *vs[0].Segment != w {
			t.Fatalf("step 5 %s: %+v, want segment %v", id, vs, w)
		}
	}
	e.m.write(sharedEp, 20)
	e.scan(tvRoot)
	e.probe(tvRoot, result(nil))
	for _, id := range []string{ep1, ep2} {
		if vs := e.s.versionsOf(t, grantAll, id); len(vs) != 1 || vs[0].Segment != nil || vs[0].Container == "" {
			t.Fatalf("step 5 no chapters %s: %+v", id, vs)
		}
	}
}
