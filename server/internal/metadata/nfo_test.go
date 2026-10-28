package metadata

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNFO(t *testing.T) {
	for _, c := range []struct {
		name, doc string
		want      map[string]string
	}{
		{"uniqueid (Kodi v18+)", `<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>
<movie><title>The Matrix</title><uniqueid type="imdb" default="true">tt0133093</uniqueid><uniqueid type="tmdb">603</uniqueid></movie>`,
			map[string]string{"imdb": "tt0133093", "tmdb": "603"}},
		{"older elements", `<movie><id>tt0113277</id><tmdbid>949</tmdbid></movie>`,
			map[string]string{"imdb": "tt0113277", "tmdb": "949"}},
		{"tv show", `<tvshow><title>Show</title><uniqueid type="tvdb" default="true">81189</uniqueid></tvshow>`,
			map[string]string{"tvdb": "81189"}},
		{"just a URL", "https://www.imdb.com/title/tt0133093/\n", map[string]string{"imdb": "tt0133093"}},
		{"TMDB URL", "https://www.themoviedb.org/movie/603-the-matrix", map[string]string{"tmdb": "603"}},
		{"invalid IDs ignored", `<movie><uniqueid type="tmdb">abc</uniqueid><uniqueid type="imdb">0133093</uniqueid><uniqueid type="evil">1</uniqueid></movie>`,
			map[string]string{}},
		{"not an NFO", "garbage \x00\x01", map[string]string{}},
		{"entities aren't expanded", `<!DOCTYPE m [<!ENTITY x "603">]><movie><tmdbid>&x;</tmdbid></movie>`, map[string]string{}},
	} {
		if got := parseNFO([]byte(c.doc)); !maps.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadNFOs(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("Heat (1995)/Heat (1995).nfo", `<movie><uniqueid type="tmdb">949</uniqueid></movie>`)
	write("Heat (1995)/movie.nfo", `<movie><uniqueid type="tmdb">950</uniqueid><uniqueid type="imdb">tt0113277</uniqueid></movie>`)
	write("Big/movie.nfo", `<movie><tmdbid>1</tmdbid>`+strings.Repeat(" ", maxNFOBytes)+`<imdbid>tt0000001</imdbid></movie>`)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ids, conflicts, err := readNFOs(root, []string{"Heat (1995)/Heat (1995).nfo", "Heat (1995)/movie.nfo", "Heat (1995)/missing.nfo"})
	if err != nil || ids["tmdb"] != "949" || ids["imdb"] != "tt0113277" || len(conflicts) != 1 || conflicts[0] != "tmdb" {
		t.Fatalf("ids %v conflicts %v err %v", ids, conflicts, err)
	}
	// Only the first MiB is read: a truncated file gives no XML IDs.
	if ids, _, err := readNFOs(root, []string{"Big/movie.nfo"}); err != nil || len(ids) != 0 {
		t.Fatalf("oversized NFO: %v %v", ids, err)
	}
	// Paths can't leave the root.
	if _, _, err := readNFOs(root, []string{"../outside.nfo"}); err == nil {
		t.Fatal("read outside the root")
	}
}
