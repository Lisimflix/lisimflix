package naming

import (
	"errors"
	"fmt"
	"testing"
)

func abs(n int) *int { return &n }

// got summarises an Entry for comparison; nil maps and slices compare as empty.
func summary(e Entry) string {
	ids := e.IDs
	if len(ids) == 0 {
		ids = nil
	}
	a := "-"
	if e.Absolute != nil {
		a = fmt.Sprint(*e.Absolute)
	}
	return fmt.Sprintf("%s %s %q %d %v v=%q s=%d e=%v d=%q abs=%s t=%q",
		e.Kind, e.Key, e.Title, e.Year, ids, e.Version, e.Season, e.Episodes, e.AirDate, a, e.EpisodeTitle)
}

func check(t *testing.T, libType, rel string, want Entry) {
	t.Helper()
	got, err := Classify(libType, rel, 2<<30)
	if err != nil {
		t.Fatalf("Classify(%q): %v", rel, err)
	}
	if summary(got) != summary(want) {
		t.Fatalf("Classify(%q)\n got %s\nwant %s", rel, summary(got), summary(want))
	}
}

func TestClassifyMovies(t *testing.T) {
	for _, c := range []struct {
		rel  string
		want Entry
	}{
		{"Movie Title (2024)/Movie Title (2024) - 2160p.mkv",
			Entry{Kind: Movie, Key: "movie:movie title|2024", Title: "Movie Title", Year: 2024, Version: "2160p"}},
		{"Movie (2010)/Movie (2010) - Director's Cut.mkv",
			Entry{Kind: Movie, Key: "movie:movie|2010", Title: "Movie", Year: 2010, Version: "Director's Cut"}},
		{"Movie (2010)/Movie (2010) {edition-Extended Edition}.mkv",
			Entry{Kind: Movie, Key: "movie:movie|2010", Title: "Movie", Year: 2010, Version: "Extended Edition"}},
		{"The Matrix (1999) {tmdb-603}/The Matrix (1999) {imdb-tt0133093} - [Bluray-1080p][DTS 5.1][x264]-GRP.mkv",
			Entry{Kind: Movie, Key: "movie:the matrix|1999", Title: "The Matrix", Year: 1999,
				IDs: map[string]string{"tmdb": "603", "imdb": "tt0133093"}, Version: "1080p"}},
		// A Radarr rename keeps the key: same title and year, new tags.
		{"Heat.1995.2160p.UHD.BluRay.x265-GRP/Heat.1995.2160p.UHD.BluRay.x265-GRP.mkv",
			Entry{Kind: Movie, Key: "movie:heat|1995", Title: "Heat", Year: 1995, Version: "2160p"}},
		{"Heat (1995) [tmdbid-949]/Heat (1995).mkv",
			Entry{Kind: Movie, Key: "movie:heat|1995", Title: "Heat", Year: 1995, IDs: map[string]string{"tmdb": "949"}}},
		{"Movies A-Z/Alien (1979)/Alien.mkv", Entry{Kind: Movie, Key: "movie:alien|1979", Title: "Alien", Year: 1979}},
		{"Random Folder/Arrival.2016.1080p.WEB-DL.mkv",
			Entry{Kind: Movie, Key: "movie:arrival|2016", Title: "Arrival", Year: 2016, Version: "1080p"}},
		{"Loose Movie (2001) - 720p.mkv", Entry{Kind: Movie, Key: "movie:loose movie|2001", Title: "Loose Movie", Year: 2001, Version: "720p"}},
		{"1917 (2019)/1917.mkv", Entry{Kind: Movie, Key: "movie:1917|2019", Title: "1917", Year: 2019}},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-GRP.mkv",
			Entry{Kind: Movie, Key: "movie:blade runner 2049|2017", Title: "Blade Runner 2049", Year: 2017, Version: "1080p"}},
		{"Amélie (2001)/Amélie (2001).mkv", Entry{Kind: Movie, Key: "movie:amélie|2001", Title: "Amélie", Year: 2001}},
		{"No Year Movie.mkv", Entry{Kind: Movie, Key: "movie:no year movie|0", Title: "No Year Movie"}},
		{"Bad ID {imdb-123}.mkv", Entry{Kind: Movie, Key: "movie:bad id|0", Title: "Bad ID"}},
	} {
		t.Run(c.rel, func(t *testing.T) { check(t, Movies, c.rel, c.want) })
	}
}

func TestClassifyTV(t *testing.T) {
	ids := map[string]string{"tvdb": "123"}
	show := func(e Entry) Entry {
		e.Kind, e.Key, e.Title, e.Year, e.IDs = Episode, "show:show|2019", "Show", 2019, ids
		return e
	}
	const dir = "Show (2019) {tvdb-123}/"
	for _, c := range []struct {
		rel  string
		want Entry
	}{
		{dir + "Season 01/Show - S01E02 - Title.mkv", show(Entry{Season: 1, Episodes: []int{2}, EpisodeTitle: "Title"})},
		{dir + "Season 01/Show (2019) - S01E02E03 - Two Parter [WEBDL-1080p][EAC3 5.1][h264]-GRP.mkv",
			show(Entry{Season: 1, Episodes: []int{2, 3}, EpisodeTitle: "Two Parter", Version: "1080p"})},
		{dir + "Season 01/Show - S01E04-E06.mkv", show(Entry{Season: 1, Episodes: []int{4, 5, 6}})},
		{dir + "Season 01/Show - S01E01-E99.mkv", show(Entry{Season: 1, Episodes: []int{1}})}, // implausible range: first only
		{dir + "Specials/Show - S00E01 - Christmas Special.mkv", show(Entry{Season: 0, Episodes: []int{1}, EpisodeTitle: "Christmas Special"})},
		{dir + "Season 2/Show.S02E10.1080p.WEB.h264-GRP.mkv", show(Entry{Season: 2, Episodes: []int{10}, Version: "1080p"})},
		{dir + "Season 3/3x07 - Old Style.avi", show(Entry{Season: 3, Episodes: []int{7}, EpisodeTitle: "Old Style"})},
		{"The Daily Show/Season 2024/The Daily Show - 2024-03-01 - Guest Name.mkv",
			Entry{Kind: Episode, Key: "show:the daily show|0", Title: "The Daily Show", Season: 2024, AirDate: "2024-03-01", EpisodeTitle: "Guest Name"}},
		{"Loose.Show.S01E01.720p.HDTV.x264-GRP.mkv",
			Entry{Kind: Episode, Key: "show:loose show|0", Title: "Loose Show", Season: 1, Episodes: []int{1}, Version: "720p"}},
	} {
		t.Run(c.rel, func(t *testing.T) { check(t, TV, c.rel, c.want) })
	}
}

func TestClassifyAnime(t *testing.T) {
	ids := map[string]string{"anidb": "17617"}
	fr := func(e Entry) Entry {
		e.Kind, e.Key, e.Title, e.Year, e.IDs = Episode, "show:frieren|2023", "Frieren", 2023, ids
		return e
	}
	const dir = "Frieren (2023) [anidb-17617]/"
	for _, c := range []struct {
		rel  string
		want Entry
	}{
		{dir + "Season 01/Frieren - S01E05 - 005 - Phantoms [Bluray-1080p][10bit]-Grp.mkv",
			fr(Entry{Season: 1, Episodes: []int{5}, Absolute: abs(5), EpisodeTitle: "Phantoms", Version: "1080p"})},
		{dir + "[SubsPlease] Frieren - 12 (1080p) [ABCD1234].mkv", fr(Entry{Season: 1, Episodes: []int{12}, Absolute: abs(12), Version: "1080p"})},
		{dir + "Season 02/Frieren - 30v2.mkv", fr(Entry{Season: 2, Episodes: []int{30}, Absolute: abs(30)})},
		{dir + "Specials/Frieren - OVA 2.mkv", fr(Entry{Season: 0, Episodes: []int{2}})},
		{dir + "Frieren - 13 (Part 2) - The Hero (1080p).mkv", fr(Entry{Season: 1, Episodes: []int{13}, Absolute: abs(13), EpisodeTitle: "The Hero", Version: "1080p"})},
		{dir + "Frieren - Episode 7 - Title.mkv", fr(Entry{Season: 1, Episodes: []int{7}, Absolute: abs(7), EpisodeTitle: "Title"})},
		// No episode number and no season folder: an anime film.
		{"Your Name (2016)/Your Name (2016) - 1080p.mkv",
			Entry{Kind: Movie, Key: "movie:your name|2016", Title: "Your Name", Year: 2016, Version: "1080p"}},
	} {
		t.Run(c.rel, func(t *testing.T) { check(t, Anime, c.rel, c.want) })
	}
}

func TestClassifyRefusesWhatItCannotPlace(t *testing.T) {
	for _, c := range []struct {
		lib, rel string
		size     int64
		want     error
	}{
		{Movies, "Movie (2010)/Featurettes/Making Of.mkv", 2 << 30, ErrExtra},
		{Movies, "Movie (2010)/Movie (2010)-trailer.mkv", 2 << 30, ErrExtra},
		{Movies, "Movie (2010)/trailer.mp4", 2 << 30, ErrExtra},
		{TV, "Show/Extras/Gag Reel.mkv", 2 << 30, ErrExtra},
		{Movies, "Movie (2010)/movie-sample.mkv", 50 << 20, ErrSample},
		{TV, "Show/Season 01/Some Random Clip.mkv", 2 << 30, ErrUnrecognised},
		{Anime, "Show/Season 01/Not An Episode.mkv", 2 << 30, ErrUnrecognised},
	} {
		if _, err := Classify(c.lib, c.rel, c.size); !errors.Is(err, c.want) {
			t.Errorf("Classify(%s, %q) = %v, want %v", c.lib, c.rel, err, c.want)
		}
	}
	// A real film whose title contains "Sample" is not a sample file.
	if e, err := Classify(Movies, "The Sample (2020)/The Sample (2020).mkv", 2<<30); err != nil || e.Title != "The Sample" {
		t.Fatalf("The Sample: %+v %v", e, err)
	}
	if _, err := Classify("music", "a.mkv", 1); err == nil {
		t.Fatal("unknown library type accepted")
	}
}

func TestSortTitle(t *testing.T) {
	for in, want := range map[string]string{"The Matrix": "Matrix", "A Quiet Place": "Quiet Place", "An Education": "Education",
		"Theory": "Theory", "The": "The", "Alien": "Alien"} {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyFolder(t *testing.T) {
	for _, c := range []struct{ lib, rel, want string }{
		{Movies, "Heat (1995)/Heat (1995).mkv", "Heat (1995)"},
		{Movies, "Movies A-Z/Alien (1979)/Alien.mkv", "Movies A-Z/Alien (1979)"},
		{Movies, "Loose Movie (2001).mkv", ""},
		{TV, "Show (2019)/Season 01/Show - S01E01.mkv", "Show (2019)"},
		{TV, "Loose.Show.S01E01.mkv", ""},
	} {
		if e, err := Classify(c.lib, c.rel, 2<<30); err != nil || e.Folder != c.want {
			t.Errorf("Classify(%q).Folder = %q, %v; want %q", c.rel, e.Folder, err, c.want)
		}
	}
}
