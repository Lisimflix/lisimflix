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
		// #141: leading zeros are dropped, as in name hints.
		{"zero-padded IDs", `<movie><tmdbid>0949</tmdbid><uniqueid type="tvdb">0081189</uniqueid><uniqueid type="anidb">000</uniqueid></movie>`,
			map[string]string{"tmdb": "949", "tvdb": "81189"}},
		// #136: URL fallback IDs are validated too, and other encodings parse.
		{"over-long IMDb URL", "https://www.imdb.com/title/tt12345678901/", map[string]string{}},
		{"UTF-8 BOM", "\uFEFF<movie><tmdbid>949</tmdbid></movie>", map[string]string{"tmdb": "949"}},
		{"ISO-8859-1", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<movie><title>Am\xe9lie</title><tmdbid>194</tmdbid></movie>",
			map[string]string{"tmdb": "194"}},
		{"unsupported encoding", `<?xml version="1.0" encoding="Shift_JIS"?><movie><tmdbid>194</tmdbid></movie>`, map[string]string{}},
	} {
		if got := parseNFO([]byte(c.doc)); !maps.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadNFOs(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Heat (1995)/Heat (1995).nfo", `<movie><uniqueid type="tmdb">949</uniqueid></movie>`)
	write("Heat (1995)/movie.nfo", `<movie><uniqueid type="tmdb">950</uniqueid><uniqueid type="imdb">tt0113277</uniqueid></movie>`)
	write("Big/movie.nfo", `<movie><tmdbid>1</tmdbid>`+strings.Repeat(" ", maxNFOBytes)+`<imdbid>tt0000001</imdbid></movie>`)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ids, conflicts := readNFOs(root, []string{"Heat (1995)/Heat (1995).nfo", "Heat (1995)/movie.nfo", "Heat (1995)/missing.nfo"})
	if ids["tmdb"] != "949" || ids["imdb"] != "tt0113277" || len(conflicts) != 1 || conflicts[0] != "tmdb" {
		t.Fatalf("ids %v conflicts %v", ids, conflicts)
	}
	// Only the first MiB is read: a truncated file gives no XML IDs.
	if ids, _ := readNFOs(root, []string{"Big/movie.nfo"}); len(ids) != 0 {
		t.Fatalf("oversized NFO: %v", ids)
	}
	// Paths can't leave the root: a real NFO above it is skipped.
	inner, err := root.OpenRoot("Big")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	if ids, _ := readNFOs(inner, []string{"../Heat (1995)/movie.nfo"}); len(ids) != 0 {
		t.Fatalf("read outside the root: %v", ids)
	}
	// Nor can a symlink inside it (#136).
	if err := os.Symlink(filepath.Join("..", "Heat (1995)", "movie.nfo"), filepath.Join(dir, "Big", "linked.nfo")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if ids, _ := readNFOs(root, []string{"Big/linked.nfo"}); ids["tmdb"] != "950" {
		t.Fatalf("a symlink inside the root wasn't followed: %v", ids)
	}
	if ids, _ := readNFOs(inner, []string{"linked.nfo"}); len(ids) != 0 {
		t.Fatalf("a symlink read outside the root: %v", ids)
	}
}

// A directory named like an NFO is skipped; the real NFOs beside it are read.
func TestReadNFOsSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Heat (1995)", "movie.nfo"), 0o755)
	os.WriteFile(filepath.Join(dir, "Heat (1995)", "Heat (1995).nfo"), []byte(`<movie><tmdbid>949</tmdbid></movie>`), 0o644)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if ids, _ := readNFOs(root, []string{"Heat (1995)/movie.nfo", "Heat (1995)/Heat (1995).nfo"}); ids["tmdb"] != "949" {
		t.Fatalf("ids %v: the directory hid the real NFO", ids)
	}
}
