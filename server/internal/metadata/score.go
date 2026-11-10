package metadata

import (
	"sort"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/textfold"
)

// Auto-match thresholds. A search result is applied without review only when
// the best candidate scores at least AutoScore and leads the next by at least
// AutoMargin. reviewKeep is how many candidates are stored for review.
const (
	AutoScore   = 0.85
	AutoMargin  = 0.10
	reviewKeep  = 5
	scoreSlack  = 1e-9 // float rounding, so a lead of exactly 0.10 counts
	partialMult = 0.8  // a partial title match never reaches AutoScore
)

// Query is what we are looking for: a kind ("movie" or "show"), the title
// parsed from the file or folder name, and the year if there is one (0 if not).
type Query struct {
	Kind, Title string
	Year        int
}

// Candidate is one provider search result.
type Candidate struct {
	Provider, ID, Kind, Title string
	AltTitles                 []string
	Year                      int
}

// Scored is a Candidate with its score against a Query.
type Scored struct {
	Candidate
	Score float64
}

// Normalize reduces a title to a comparable form; see textfold.Title.
func Normalize(title string) string { return textfold.Title(title) }

// similarity is 1 for equal normalised titles, otherwise 0.8 times the share
// of words they have in common.
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	set := map[string]bool{}
	for _, w := range strings.Fields(a) {
		set[w] = true
	}
	union := len(set)
	common := 0
	seen := map[string]bool{}
	for _, w := range strings.Fields(b) {
		if seen[w] {
			continue
		}
		seen[w] = true
		if set[w] {
			common++
		} else {
			union++
		}
	}
	return partialMult * float64(common) / float64(union)
}

// Score rates how well a candidate fits the query, from 0 to 1. The best of
// the candidate's titles counts; the year then adjusts it. A candidate of
// another kind scores 0.
func Score(q Query, c Candidate) float64 {
	if q.Kind != c.Kind {
		return 0
	}
	want := Normalize(q.Title)
	best := similarity(want, Normalize(c.Title))
	for _, t := range c.AltTitles {
		if s := similarity(want, Normalize(t)); s > best {
			best = s
		}
	}
	switch d := q.Year - c.Year; {
	case q.Year == 0 || c.Year == 0:
		best -= 0.05
	case d == 0:
	case d == 1 || d == -1:
		best -= 0.1
	default:
		best -= 0.5
	}
	if best < 0 {
		return 0
	}
	return best
}

// Rank scores every candidate and returns them best first (ties keep the
// provider's order). auto reports whether the best one is confident enough to
// apply without review: it scores at least AutoScore and leads the next by at
// least AutoMargin.
func Rank(q Query, cands []Candidate) (ranked []Scored, auto bool) {
	for _, c := range cands {
		ranked = append(ranked, Scored{Candidate: c, Score: Score(q, c)})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if len(ranked) == 0 || ranked[0].Score < AutoScore-scoreSlack {
		return ranked, false
	}
	if len(ranked) > 1 && ranked[0].Score-ranked[1].Score < AutoMargin-scoreSlack {
		return ranked, false
	}
	return ranked, true
}
