package textfold

import "testing"

func TestTitle(t *testing.T) {
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
		if got := Title(in); got != want {
			t.Errorf("Title(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGroup(t *testing.T) {
	for in, want := range map[string]int{"matrix": 1, "elite": 1, "2001 a space odyssey": 0, "": 0, "ガ": 0, "#hashtag": 0} {
		if got := Group(in); got != want {
			t.Errorf("Group(%q) = %d, want %d", in, got, want)
		}
	}
}
