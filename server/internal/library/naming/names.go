// Package naming turns a video file's path inside a library root into the
// catalogue item it belongs to, from Plex/Radarr/Sonarr and anime naming
// conventions (LIB-2, spec §3.1). It only reads names, never files.
package naming

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	idHintRe    = regexp.MustCompile(`(?i)[\[{]\s*(tmdb|tvdb|imdb|anidb|anilist|mal)(?:id)?\s*[-=:]\s*([a-z0-9]+)\s*[\]}]`)
	editionRe   = regexp.MustCompile(`(?i)\{edition-([^}]+)\}`)
	parenYearRe = regexp.MustCompile(`\((\d{4})\)`)
	bracketRe   = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
	qualityRe   = regexp.MustCompile(`(?i)(?:^|[\s(\[_-])(2160p|1080p|1080i|720p|576p|480p|4k|uhd|bluray|blu-ray|bdrip|brrip|bdremux|web-?dl|webrip|hdtv|dvdrip|remux|hdr10|x264|x265|h\.?264|h\.?265|hevc|10bit|8bit)(?:$|[\s)\]_-])`)
	resRe       = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(2160p|1080p|1080i|720p|576p|480p|4k|uhd)(?:$|[^a-z0-9])`)
	spacesRe    = regexp.MustCompile(`\s+`)
	imdbRe      = regexp.MustCompile(`^tt\d+$`)
	// releaseGroupRe matches a trailing "-GROUP" left after brackets are removed.
	releaseGroupRe = regexp.MustCompile(`(?:^|\s)-[^\s-][^\s]*$`)
	sampleRe       = regexp.MustCompile(`(?i)(?:^|[-._ \[(])sample(?:$|[-._ \])])`)
)

// nameInfo is what a folder or file name says about a movie or show.
type nameInfo struct {
	Title string
	Year  int
	IDs   map[string]string
}

// parseName reads "Title (Year) {tmdb-1}", Radarr/Sonarr names and scene
// names like "Title.Year.1080p.BluRay.x264-GRP".
func parseName(name string) nameInfo {
	ni := nameInfo{IDs: idHints(name)}
	s := bracketRe.ReplaceAllString(name, " ")
	if ms := parenYearRe.FindAllStringSubmatchIndex(s, -1); len(ms) > 0 {
		m := ms[len(ms)-1]
		if y, _ := strconv.Atoi(s[m[2]:m[3]]); validYear(y) && strings.TrimSpace(s[:m[0]]) != "" {
			ni.Year = y
			ni.Title = cleanTitle(dotsToSpaces(s[:m[0]]))
			return ni
		}
	}
	s = dotsToSpaces(s)
	if loc := qualityRe.FindStringSubmatchIndex(s); loc != nil && strings.TrimSpace(s[:loc[2]]) != "" {
		s = s[:loc[2]]
	}
	// The last year-like word that isn't the first word is the year:
	// "Blade Runner 2049 2017" → 2017, but "1917" alone stays a title.
	words := strings.Fields(s)
	for i := len(words) - 1; i > 0; i-- {
		w := strings.Trim(words[i], "()")
		if y, err := strconv.Atoi(w); err == nil && len(w) == 4 && validYear(y) {
			ni.Year = y
			s = strings.Join(words[:i], " ")
			break
		}
	}
	ni.Title = cleanTitle(s)
	return ni
}

// idHints extracts {tmdb-123}, [tmdbid-123], {imdb-tt123}, [anidb-123] ... hints.
func idHints(name string) map[string]string {
	ids := map[string]string{}
	for _, m := range idHintRe.FindAllStringSubmatch(name, -1) {
		if k, v, ok := normalizeID(m[1], m[2]); ok {
			ids[k] = v
		}
	}
	return ids
}

// normalizeID checks a hint's value; the namespace is already one of the
// regexp's names, lower-cased.
func normalizeID(provider, value string) (string, string, bool) {
	provider, value = strings.ToLower(provider), strings.ToLower(value)
	if provider == "imdb" {
		return provider, value, imdbRe.MatchString(value)
	}
	if n, err := strconv.ParseInt(value, 10, 64); err != nil || n <= 0 {
		return "", "", false
	}
	return provider, value, true
}

func validYear(y int) bool { return y >= 1880 && y <= 2100 }

// dotsToSpaces turns scene-style "The.Movie_Name" into "The Movie Name" when
// the name has no real spaces.
func dotsToSpaces(s string) string {
	if strings.Count(s, " ") >= strings.Count(s, ".")+strings.Count(s, "_") {
		return s
	}
	return strings.NewReplacer(".", " ", "_", " ").Replace(s)
}

func cleanTitle(s string) string {
	s = spacesRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Trim(s, " -_.(["))
}

// versionLabel names one of several files of a movie: an {edition-...} tag,
// else the text after " - " following the year ("Director's Cut", "1080p"),
// else the resolution anywhere in the name, else "".
func versionLabel(stem string) string {
	if m := editionRe.FindStringSubmatch(stem); m != nil {
		return strings.TrimSpace(m[1])
	}
	rest := ""
	if ms := parenYearRe.FindAllStringIndex(stem, -1); len(ms) > 0 {
		rest = stem[ms[len(ms)-1][1]:]
	} else if i := strings.Index(stem, " - "); i >= 0 {
		rest = stem[i:]
	}
	rest = strings.TrimSpace(idHintRe.ReplaceAllString(rest, ""))
	if strings.HasPrefix(rest, "- ") || rest == "-" {
		label := strings.TrimSpace(bracketRe.ReplaceAllString(strings.TrimSpace(rest[1:]), " "))
		if label = cleanTitle(releaseGroupRe.ReplaceAllString(label, "")); label != "" {
			return label
		}
	}
	return resolution(stem)
}

func resolution(stem string) string {
	m := resRe.FindStringSubmatch(stem)
	if m == nil {
		return ""
	}
	if r := strings.ToLower(m[1]); r != "4k" && r != "uhd" {
		return r
	}
	return "2160p"
}

// key normalises a title for identity: lower case, letters and digits only,
// single spaces. "The.Matrix" and "The Matrix" share a key.
func key(title string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

// SortTitle drops a leading English article: "The Matrix" sorts as "Matrix".
func SortTitle(title string) string {
	for _, a := range []string{"the ", "a ", "an "} {
		if len(title) > len(a) && strings.EqualFold(title[:len(a)], a) {
			return title[len(a):]
		}
	}
	return title
}
