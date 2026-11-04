// Package textfold folds titles into one comparable, sortable form, shared by
// metadata matching and the library catalogue.
package textfold

import (
	"strings"
	"unicode"
)

// latinFold maps lower-case Latin-1 and Latin Extended-A letters to plain
// ASCII. Only Latin is folded: marks in other scripts are part of the word.
var latinFold = func() map[rune]string {
	m := map[rune]string{}
	add := func(to string, from string) {
		for _, r := range from {
			m[r] = to
		}
	}
	add("a", "àáâãäåāăą")
	add("ae", "æ")
	add("c", "çćĉċč")
	add("d", "ðďđ")
	add("e", "èéêëēĕėęě")
	add("g", "ĝğġģ")
	add("h", "ĥħ")
	add("i", "ìíîïĩīĭįı")
	add("ij", "ĳ")
	add("j", "ĵ")
	add("k", "ķĸ")
	add("l", "ĺļľŀł")
	add("n", "ñńņňŉŋ")
	add("o", "òóôõöøōŏő")
	add("oe", "œ")
	add("r", "ŕŗř")
	add("s", "śŝşš")
	add("ss", "ß")
	add("t", "ţťŧ")
	add("th", "þ")
	add("u", "ùúûüũūŭůűų")
	add("w", "ŵ")
	add("y", "ýÿŷ")
	add("z", "źżž")
	return m
}()

// Title reduces a title to a comparable form: lower case, Latin accents
// folded, "&" written as "and", apostrophes dropped ("Ocean's" is "oceans"),
// other punctuation turned into spaces, and a leading "the", "a" or "an"
// dropped when more words follow. Titles in other scripts compare as written.
func Title(title string) string {
	var b strings.Builder
	lastLatin := false // was the last letter written Latin?
	for _, r := range strings.ToLower(title) {
		if f, ok := latinFold[r]; ok {
			b.WriteString(f)
			lastLatin = true
			continue
		}
		switch {
		case r == '\'' || r == '\u2019' || r == '\u02BC': // ' ’ ʼ join the word
		case r == '&':
			b.WriteString(" and ")
		case unicode.IsLetter(r):
			b.WriteRune(r)
			lastLatin = unicode.Is(unicode.Latin, r)
		case unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsMark(r):
			// A decomposed accent after a Latin letter is dropped; in any
			// other script the mark belongs to the word.
			if !lastLatin {
				b.WriteRune(r)
			}
		default:
			b.WriteByte(' ')
		}
	}
	words := strings.Fields(b.String())
	if len(words) > 1 {
		switch words[0] {
		case "the", "a", "an":
			words = words[1:]
		}
	}
	return strings.Join(words, " ")
}

// Group returns the catalogue letter group of an already folded title: 1 when
// it starts with an ASCII letter a-z, 0 otherwise (the "#" group, sorted first).
func Group(folded string) int {
	if folded != "" && folded[0] >= 'a' && folded[0] <= 'z' {
		return 1
	}
	return 0
}
