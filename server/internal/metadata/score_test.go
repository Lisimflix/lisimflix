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
		"Ame\u0301lie":           "amelie", // a decomposed Latin accent (macOS NFD names)
		// #121 review: marks in other scripts are part of the word, not
		// accents to drop: Devanagari virama U+094D and vowel sign U+093F,
		// Thai U+0E34 and U+0E4C, Hebrew U+05B8, Japanese voiced mark U+3099.
		"हिन्दी":       "हिन्दी",
		"ภาพยนตร์":     "ภาพยนตร์",
		"שָׁלוֹם":      "שָׁלוֹם",
		"\u30AB\u3099": "\u30AB\u3099", // katakana ga as written by macOS (NFD)
		"かぐや様は告らせたい":   "かぐや様は告らせたい",
		// Apostrophes join the word (Decision 5: punctuation is dropped).
		"Ocean's Eleven":   "oceans eleven",
		"Schindler’s List": "schindlers list",
		"Hawaiʼi Five-O":   "hawaii five o",
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
	// A title with and without its apostrophe is an exact match.
	for _, p := range [][2]string{{"Oceans Eleven", "Ocean's Eleven"}, {"Schindlers List", "Schindler’s List"}} {
		if got := Score(Query{Kind: "movie", Title: p[0], Year: 2001}, Candidate{Kind: "movie", Title: p[1], Year: 2001}); got != 1 {
			t.Errorf("%q vs %q: Score = %.3f, want 1", p[0], p[1], got)
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

// #136: Rank's edge cases.
func TestRankEdgeCases(t *testing.T) {
	q := Query{"movie", "Heat", 1995}
	// A lead of exactly AutoMargin (1 vs 0.9) is enough, despite float rounding.
	if _, auto := Rank(q, []Candidate{{ID: "a", Kind: "movie", Title: "Heat", Year: 1995}, {ID: "b", Kind: "movie", Title: "Heat", Year: 1996}}); !auto {
		t.Error("a lead of exactly 0.10 didn't auto-match")
	}
	// An alternative title counts inside Rank too.
	if ranked, auto := Rank(q, []Candidate{{ID: "a", Kind: "movie", Title: "Heat Wave", Year: 1995},
		{ID: "b", Kind: "movie", Title: "Hitze", AltTitles: []string{"Heat"}, Year: 1995}}); !auto || ranked[0].ID != "b" {
		t.Errorf("alternative title: auto=%v ranked %+v", auto, ranked)
	}
	// Empty or punctuation-only titles never match, not even each other.
	for _, p := range [][2]string{{"", ""}, {"...", "!!!"}, {"Heat", "?"}, {"-", "Heat"}} {
		if ranked, auto := Rank(Query{"movie", p[0], 1995}, []Candidate{{Kind: "movie", Title: p[1], Year: 1995}}); auto || ranked[0].Score != 0 {
			t.Errorf("%q vs %q: score %.3f auto=%v", p[0], p[1], ranked[0].Score, auto)
		}
	}
	// Ties keep the provider's order.
	var cands []Candidate
	for _, id := range []string{"5", "3", "9", "1", "7", "2", "8", "4", "6"} {
		cands = append(cands, Candidate{ID: id, Kind: "movie", Title: "Heat", Year: 1995})
	}
	ranked, _ := Rank(q, cands)
	for i, c := range ranked {
		if c.ID != cands[i].ID {
			t.Fatalf("tie order %v, want the provider's", ranked)
		}
	}
}
