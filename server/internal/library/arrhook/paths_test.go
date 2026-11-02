package arrhook

import (
	"errors"
	"slices"
	"testing"
)

func TestResolve(t *testing.T) {
	src := Source{
		Roots: []Root{{"root-tv", "TV"}, {"root-anime", "Media/Anime"}},
		Mappings: []Mapping{
			{"/tv", "TV"},
			{"/data", "Media"},
			{"/data/series", "TV"}, // longer than /data: wins for paths under it
			{"C:/Media/TV", "TV"},
		},
	}
	for _, c := range []struct {
		remote string
		want   Scope
		err    error
	}{
		{"/tv/Show (2019)/Season 01/x.mkv", Scope{"root-tv", "Show (2019)/Season 01/x.mkv"}, nil},
		{"/tv", Scope{"root-tv", "."}, nil},
		{"/tv/", Scope{"root-tv", "."}, nil},
		{"/data/series/Show/x.mkv", Scope{"root-tv", "Show/x.mkv"}, nil},
		{"/data/Anime/Frieren/x.mkv", Scope{"root-anime", "Frieren/x.mkv"}, nil},
		{`C:\Media\TV\Show\x.mkv`, Scope{"root-tv", "Show/x.mkv"}, nil},
		{"/tvshows/x.mkv", Scope{}, errUnmapped},                        // a prefix, not a folder
		{"/tv/../etc/passwd", Scope{}, errUnmapped},                     // cleaned first
		{"/data/Movies/x.mkv", Scope{}, errOutside},                     // mapped, but no root of this source
		{"/data/series/../../tv/x.mkv", Scope{"root-tv", "x.mkv"}, nil}, // = /tv/x.mkv
		{"Show/x.mkv", Scope{}, errNotAbsolute},
		{"", Scope{}, errNotAbsolute},
	} {
		got, err := src.resolve(c.remote)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("resolve(%q) = %+v, %v; want %+v, %v", c.remote, got, err, c.want, c.err)
		}
	}
	// A mapping onto the media root itself works too.
	whole := Source{Roots: []Root{{"root-tv", "TV"}}, Mappings: []Mapping{{"/media", "."}}}
	if got, err := whole.resolve("/media/TV/x.mkv"); err != nil || got != (Scope{"root-tv", "x.mkv"}) {
		t.Errorf("mapping to the media root: %+v %v", got, err)
	}
}

func TestGroup(t *testing.T) {
	tv := func(p string) Scope { return Scope{"root-tv", p} }
	for _, c := range []struct {
		name     string
		in, want []Scope
	}{
		{"one file", []Scope{tv("Show/Season 01/a.mkv")}, []Scope{tv("Show/Season 01/a.mkv")}},
		{"season pack", []Scope{tv("Show/Season 02/a.mkv"), tv("Show/Season 02/b.mkv"), tv("Show/Season 02/c.mkv")},
			[]Scope{tv("Show/Season 02")}},
		{"upgrade: new and replaced file", []Scope{tv("Show/S1/new.mkv"), tv("Show/S1/old.mkv")}, []Scope{tv("Show/S1")}},
		{"duplicates", []Scope{tv("Show/a.mkv"), tv("Show/a.mkv")}, []Scope{tv("Show/a.mkv")}},
		{"inside another", []Scope{tv("Show"), tv("Show/S1/a.mkv")}, []Scope{tv("Show")}},
		{"different roots stay apart", []Scope{tv("a.mkv"), {"root-anime", "a.mkv"}},
			[]Scope{{"root-anime", "a.mkv"}, tv("a.mkv")}},
		{"whole root", []Scope{tv("."), tv("x/a.mkv")}, []Scope{tv(".")}},
	} {
		if got := group(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
