package arrhook

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	const show = "/tv/Show (2019)/"
	for _, c := range []struct {
		kind, file, typ, want string
		paths                 []string
	}{
		{Sonarr, "sonarr_download.json", "Download", kindScan, []string{
			show + "Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv",
			show + "Season 01/Show - S01E02 - Two [HDTV-720p].mkv"}}, // the upgrade's replaced file
		{Sonarr, "sonarr_import_complete.json", "Download", kindScan, []string{
			show + "Season 02/Show - S02E01.mkv", show + "Season 02/Show - S02E02.mkv", show + "Season 02/Show - S02E03.mkv"}},
		{Sonarr, "sonarr_rename.json", "Rename", kindScan, []string{
			show + "Season 01/Show (2019) - S01E02 - Two.mkv", show + "Season 01/Show - S01E02 - Two [WEBDL-1080p].mkv"}},
		{Sonarr, "sonarr_episode_delete.json", "EpisodeFileDelete", kindScan, []string{show + "Season 01/Show - S01E02.mkv"}},
		{Sonarr, "sonarr_series_delete.json", "SeriesDelete", kindScan, []string{"/tv/Show (2019)"}},
		{Sonarr, "sonarr_test.json", "Test", kindTest, nil},
		{Sonarr, "sonarr_grab.json", "Grab", kindIgnore, nil},
		{Radarr, "radarr_download.json", "Download", kindScan, []string{"/movies/Heat (1995)/Heat (1995) [Bluray-2160p].mkv"}},
		{Radarr, "radarr_download_relative.json", "Download", kindScan, []string{"/movies/Heat (1995)/Heat (1995).mkv"}},
		{Radarr, "radarr_rename.json", "Rename", kindScan, []string{
			"/movies/Heat (1995)/Heat (1995) {tmdb-949}.mkv", "/movies/Heat (1995)/Heat (1995).mkv"}},
		{Radarr, "radarr_movie_file_delete.json", "MovieFileDelete", kindScan, []string{"/movies/Heat (1995)/Heat (1995).mkv"}},
		{Radarr, "radarr_movie_delete.json", "MovieDelete", kindScan, []string{"/movies/Heat (1995)"}},
	} {
		e, err := parse(c.kind, fixture(t, c.file))
		if err != nil || e.Type != c.typ || e.Kind != c.want || !slices.Equal(e.Paths, c.paths) {
			t.Errorf("%s: %+v %v\nwant %s %s %q", c.file, e, err, c.typ, c.want, c.paths)
		}
	}
}

func TestParseRefusesBrokenPayloads(t *testing.T) {
	// typ is the event type the error keeps, so the receipt and the log can
	// name it: empty only when the payload has none to give.
	many := `{"eventType":"Download","series":{"path":"/tv/x"},"episodeFiles":[` +
		strings.Repeat(`{"path":"/tv/x/a.mkv"},`, maxPaths) + `{"path":"/tv/x/b.mkv"}]}`
	for name, c := range map[string]struct{ kind, body, typ string }{
		"not JSON":                               {Sonarr, `eventType=Download`, ""},
		"no eventType":                           {Sonarr, `{"series":{"path":"/tv/x"}}`, ""},
		"download without file":                  {Sonarr, `{"eventType":"Download","series":{"path":"/tv/x"},"episodes":[]}`, "Download"},
		"radarr payload sent to a sonarr source": {Sonarr, string(fixture(t, "radarr_download.json")), "Download"},
		"delete without file":                    {Radarr, `{"eventType":"MovieFileDelete","movie":{"folderPath":"/movies/x"}}`, "MovieFileDelete"},
		"bad deletedFiles":                       {Sonarr, `{"eventType":"Download","series":{"path":"/tv/x"},"episodeFile":{"path":"/tv/x/a.mkv"},"deletedFiles":[1]}`, "Download"},
		"too many paths":                         {Sonarr, many, "Download"},
	} {
		e, err := parse(c.kind, []byte(c.body))
		if !errors.Is(err, errSchema) {
			t.Errorf("%s: %v, want a schema error", name, err)
		}
		if e.Type != c.typ || len(e.Paths) != 0 {
			t.Errorf("%s: event %+v, want type %q and no paths", name, e, c.typ)
		}
	}
	// A new event type in a later release is acknowledged, not an error.
	if e, err := parse(Sonarr, []byte(`{"eventType":"SomethingNew"}`)); err != nil || e.Kind != kindIgnore {
		t.Errorf("unknown event: %+v %v", e, err)
	}
	// eventType in another case still matches.
	if e, err := parse(Radarr, []byte(`{"eventType":"test"}`)); err != nil || e.Kind != kindTest {
		t.Errorf("lower-case test: %+v %v", e, err)
	}
}

func TestCheckAgent(t *testing.T) {
	for _, c := range []struct {
		kind, ua, app string
		ok            bool
	}{
		{Sonarr, "Sonarr/4.0.20.3014 (ubuntu 22.04)", "Sonarr/4.0.20.3014", true},
		{Radarr, "Radarr/6.4.4.10685 (alpine 3.22.2)", "Radarr/6.4.4.10685", true},
		{Radarr, "Radarr/5.26.2.10099", "Radarr/5.26.2.10099", true},
		{Sonarr, "Sonarr/3.0.10.1567 (ubuntu 20.04)", "Sonarr/3.0.10.1567", false},  // v3 is end of life
		{Sonarr, "Sonarr/5.0.0.1 (ubuntu 24.04)", "Sonarr/5.0.0.1", false},          // not known yet
		{Sonarr, "Radarr/6.4.4.10685 (alpine 3.22.2)", "Radarr/6.4.4.10685", false}, // wrong app for the token
		{Sonarr, "Radarr/4.0.0.1 (ubuntu 22.04)", "Radarr/4.0.0.1", false},          // wrong app, though 4 is a Sonarr version
		{Radarr, "curl/8.5.0", "curl/8.5.0", false},
		{Radarr, "", "", false},
	} {
		app, err := checkAgent(c.kind, c.ua)
		if app != c.app || (err == nil) != c.ok {
			t.Errorf("checkAgent(%s, %q) = %q, %v", c.kind, c.ua, app, err)
		}
	}
}
