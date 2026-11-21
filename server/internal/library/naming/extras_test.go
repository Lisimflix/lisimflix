package naming

import (
	"fmt"
	"strings"
	"testing"
)

func TestExtra(t *testing.T) {
	for _, c := range []struct {
		rel  string
		want ExtraEntry
		ok   bool
	}{
		{"Heat (1995)/Featurettes/Making Of.mkv", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "featurette", Title: "Making Of"}, true},
		{"Heat (1995)/behind the scenes/Stunts.mkv", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "behindTheScenes", Title: "Stunts"}, true},
		{"Show (2019)/Season 01/Deleted Scenes/Cut.mkv",
			ExtraEntry{Folder: "Show (2019)/Season 01", Folders: []string{"Show (2019)/Season 01", "Show (2019)"}, Type: "deletedScene", Title: "Cut"}, true},
		{"Show (2019)/Specials/Extras/x.mkv",
			ExtraEntry{Folder: "Show (2019)/Specials", Folders: []string{"Show (2019)/Specials", "Show (2019)"}, Type: "other", Title: "x"}, true},
		{"Marvel/The Avengers (2012)/Behind The Scenes/x.mkv",
			ExtraEntry{Folder: "Marvel/The Avengers (2012)", Folders: []string{"Marvel/The Avengers (2012)"}, Type: "behindTheScenes", Title: "x"}, true},
		{"Heat (1995)/Extras/x.mkv", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "other", Title: "x"}, true},
		{"Heat (1995)/Shorts/x.mkv", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "short", Title: "x"}, true},
		// The first extras folder wins; the extras folder between it and the file still lets placement climb.
		{"Heat (1995)/Featurettes/Extras/x.mkv", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "featurette", Title: "x"}, true},
		// Directly in a top-level extras folder: an extra with nowhere to go.
		{"Extras/x.mkv", ExtraEntry{Type: "other", Title: "x"}, true},
		{"Others/x.mkv", ExtraEntry{Type: "other", Title: "x"}, true},
		// Decision 6b: a top-level extras folder holding further folders is a collection.
		{"Shorts/Paperman (2012)/Paperman (2012).mkv", ExtraEntry{}, false},
		// Ruling F9: an ordinary folder between the extras folder and the file leaves nothing to try.
		{"Collections/Shorts/Film (2010)/Film.mkv", ExtraEntry{Folder: "Collections", Type: "short", Title: "Film"}, true},

		{"Heat (1995)/Heat (1995)-trailer.mkv",
			ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Key: "heat|1995", Type: "trailer", Title: "Trailer"}, true},
		{"Heat (1995)/Teaser-trailer.mkv", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "trailer", Title: "Teaser"}, true},
		{"Heat (1995)/trailer.mp4", ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Type: "trailer", Title: "Trailer"}, true},
		{"Marvel/Thor (2011)-trailer.mkv", ExtraEntry{Folder: "Marvel", Folders: []string{"Marvel"}, Key: "thor|2011", Type: "trailer", Title: "Thor (2011)"}, true},
		{"Show (2019)/Season 01/Making Of-featurette.mkv",
			ExtraEntry{Folder: "Show (2019)/Season 01", Folders: []string{"Show (2019)/Season 01", "Show (2019)"}, Type: "featurette", Title: "Making Of"}, true},
		{"Show (2019)/Show (2019)-trailer.mkv",
			ExtraEntry{Folder: "Show (2019)", Folders: []string{"Show (2019)"}, Key: "show|2019", Type: "trailer", Title: "Trailer"}, true},
		{"Heat (1995)/Heat (1995)-scene.mkv",
			ExtraEntry{Folder: "Heat (1995)", Folders: []string{"Heat (1995)"}, Key: "heat|1995", Type: "scene", Title: "Scene"}, true},
		// Decision 6c: a file named after its folder whose title ends in the suffix word is the movie.
		{"The Crime-Scene (2019)/The Crime-Scene.mkv", ExtraEntry{}, false},
		{"The Crime Scene (2019)/The Crime-Scene.mkv", ExtraEntry{}, false}, // the folder spells it without the dash
		// A year-named extra of a movie whose title ends in the suffix word is still the extra.
		{"The Interview (2014)/The Interview (2014)-interview.mkv",
			ExtraEntry{Folder: "The Interview (2014)", Folders: []string{"The Interview (2014)"}, Key: "the interview|2014", Type: "interview", Title: "Interview"}, true},
		{"The Crime-Scene (2019)/The Crime-Scene (2019)-scene.mkv",
			ExtraEntry{Folder: "The Crime-Scene (2019)", Folders: []string{"The Crime-Scene (2019)"}, Key: "the crime scene|2019", Type: "scene", Title: "Scene"}, true},
		{"The Crime-Scene (2019)/The Crime-Scene (2019)-trailer.mkv",
			ExtraEntry{Folder: "The Crime-Scene (2019)", Folders: []string{"The Crime-Scene (2019)"}, Key: "the crime scene|2019", Type: "trailer", Title: "Trailer"}, true},
		{"The Crime-Scene (2019)/Making-scene.mkv",
			ExtraEntry{Folder: "The Crime-Scene (2019)", Folders: []string{"The Crime-Scene (2019)"}, Type: "scene", Title: "Making"}, true},

		{"Trailer Park (2006)/Trailer Park (2006).mkv", ExtraEntry{}, false},
		{"Heat (1995)/Heat (1995).mkv", ExtraEntry{}, false},
		{"Loose-trailer.mkv", ExtraEntry{}, false}, // suffix names need a folder, as before
	} {
		got, ok := Extra(c.rel)
		if ok != c.ok || fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", c.want) {
			t.Errorf("Extra(%q)\n got %#v %v\nwant %#v %v", c.rel, got, ok, c.want, c.ok)
		}
	}
	// Decision 6b: the collection's film is placed as the movie.
	check(t, Movies, "Shorts/Paperman (2012)/Paperman (2012).mkv", Entry{Kind: Movie, Key: "movie:paperman|2012", Title: "Paperman", Year: 2012})
}

func TestFoldLabel(t *testing.T) {
	for in, want := range map[string]string{"Director's Cut": "director's cut", "Édition": "Édition", "": ""} {
		if got := FoldLabel(in); got != want {
			t.Errorf("FoldLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsResolutionLabel(t *testing.T) {
	for _, l := range []string{"", "480p", "576p", "720p", "1080i", "1080p", "1080P", "2160p"} {
		if !IsResolutionLabel(l) {
			t.Errorf("IsResolutionLabel(%q) = false", l)
		}
	}
	for _, l := range []string{"Director's Cut", "1080p HDR", "Theatrical", "4K"} {
		if IsResolutionLabel(l) {
			t.Errorf("IsResolutionLabel(%q) = true", l)
		}
	}
}

func TestVersionLabelsAreCanonical(t *testing.T) {
	for stem, want := range map[string]string{
		"Heat (1995) - 4K":                                          "2160p",
		"Heat (1995) - UHD":                                         "2160p",
		"Heat (1995) - 1080P":                                       "1080p",
		"Heat (1995) {edition-Director's Cut}":                      "Director's Cut",
		"Heat (1995) - Theatrical":                                  "Theatrical",
		"Heat.1995.2160p.UHD.BluRay.x265-GRP":                       "2160p",
		"Heat (1995) - 1080p REPACK":                                "1080p",
		"Heat (1995) - Bluray-1080p Proper":                         "1080p",
		"Heat (1995) - WEBDL-1080p":                                 "1080p",
		"Heat (1995) - Remux-2160p":                                 "2160p",
		"Heat (1995) - HDTV-720p":                                   "720p",
		"Heat (1995) - DVD":                                         "",
		"Heat (1995) - DVD-R":                                       "",
		"Heat (1995) - 4K Remux":                                    "2160p",
		"Heat (1995) - 2160p UHD BluRay":                            "2160p",
		"Heat (1995) {edition-Remux}":                               "Remux",
		"Heat (1995) {edition-4K}":                                  "4K",
		"Heat (1995) - SDTV":                                        "",
		"Heat (1995) - x265":                                        "",
		"Heat (1995) - 1080p x265":                                  "1080p",
		"Heat (1995) - 1080p REAL":                                  "1080p",
		"Heat (1995) - 1080p HDR":                                   "1080p HDR",
		"Heat (1995) - Director's Cut REPACK":                       "Director's Cut",
		"Heat (1995) - Director's Cut - REPACK":                     "Director's Cut",
		"Heat (1995) {edition-" + strings.Repeat("a", 88) + " - b}": strings.Repeat("a", 88),
		"Heat (1995) {edition-" + strings.Repeat("a", 89) + " b}":   strings.Repeat("a", 89),
		"Heat (1995) - Proper":                                      "",
		"Heat (1995) - 720p 1080p":                                  "720p 1080p", // two resolutions: not one value, kept
		"Heat (1995) {edition-" + strings.Repeat("é", 100) + "}":    strings.Repeat("é", 90),
	} {
		if got := versionLabel(stem); got != want {
			t.Errorf("versionLabel(%q) = %q, want %q", stem, got, want)
		}
	}
}
