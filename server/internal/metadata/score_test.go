package metadata

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"The Lord of the Rings: The Return of the King": "lord of the rings the return of the king",
		"Lord of the Rings - The Return Of The King":    "lord of the rings the return of the king",
		"Amélie":                 "amelie",
		"Léon: The Professional": "leon the professional",
		"Fast & Furious":         "fast and furious",
		"A Quiet Place":          "quiet place",
		"The":                    "the",
		"WALL·E":                 "wall e",
		"Straße":                 "strasse",
		"進撃の巨人":                  "進撃の巨人",  // other scripts compare as written
		"Amélie":                "amelie", // a decomposed Latin accent (macOS NFD names)
		// #121 review: marks in other scripts are part of the word, not
		// accents to drop: Devanagari virama U+094D and vowel sign U+093F,
		// Thai U+0E34 and U+0E4C, Hebrew U+05B8, Japanese voiced mark U+3099.
		"हिन्दी":     "हिन्दी",
		"ภาพยนตร์":   "ภาพยนตร์",
		"שָׁלוֹם":    "שָׁלוֹם",
		"ガ":         "ガ", // ガ as written by macOS (NFD)
		"かぐや様は告らせたい": "かぐや様は告らせたい",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScore(t *testing.T) {
	q := Query{Kind: "movie", Title: "Heat", Year: 1995}
	for _, c := range []struct {
		name string
		c    Candidate
		want float64
	}{
		{"exact", Candidate{Kind: "movie", Title: "Heat", Year: 1995}, 1},
		{"one year off", Candidate{Kind: "movie", Title: "Heat", Year: 1996}, 0.9},
		{"remake", Candidate{Kind: "movie", Title: "Heat", Year: 1986}, 0.5},
		{"unknown year", Candidate{Kind: "movie", Title: "Heat"}, 0.95},
		{"alternate title", Candidate{Kind: "movie", Title: "Hitze", AltTitles: []string{"Heat"}, Year: 1995}, 1},
		{"other kind", Candidate{Kind: "show", Title: "Heat", Year: 1995}, 0},
		{"partial", Candidate{Kind: "movie", Title: "Heat Wave", Year: 1995}, 0.4},
	} {
		if got := Score(q, c.c); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s: Score = %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

// Low-confidence results never auto-match: a weak best, or two candidates
// too close to call (a remake with no year), go to review.
func TestRankAutoMatchesOnlyWhenConfident(t *testing.T) {
	heat := Candidate{ID: "949", Kind: "movie", Title: "Heat", Year: 1995}
	for _, c := range []struct {
		name  string
		q     Query
		cands []Candidate
		auto  bool
		best  string
	}{
		{"clear winner", Query{"movie", "Heat", 1995}, []Candidate{
			{ID: "1", Kind: "movie", Title: "Heat Wave", Year: 1995}, heat}, true, "949"},
		{"remakes without a year", Query{"movie", "Dune", 0}, []Candidate{
			{ID: "438631", Kind: "movie", Title: "Dune", Year: 2021}, {ID: "841", Kind: "movie", Title: "Dune", Year: 1984}}, false, "438631"},
		{"remakes, year decides", Query{"movie", "Dune", 1984}, []Candidate{
			{ID: "438631", Kind: "movie", Title: "Dune", Year: 2021}, {ID: "841", Kind: "movie", Title: "Dune", Year: 1984}}, true, "841"},
		{"only a partial match", Query{"movie", "Heat", 1995}, []Candidate{{ID: "1", Kind: "movie", Title: "Heat Wave", Year: 1995}}, false, "1"},
		{"nothing found", Query{"movie", "Heat", 1995}, nil, false, ""},
	} {
		ranked, auto := Rank(c.q, c.cands)
		best := ""
		if len(ranked) > 0 {
			best = ranked[0].ID
		}
		if auto != c.auto || best != c.best {
			t.Errorf("%s: auto=%v best=%q, want auto=%v best=%q", c.name, auto, best, c.auto, c.best)
		}
	}
}
