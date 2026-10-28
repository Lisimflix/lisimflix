package naming

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Library types, spelled as contracts' Library.type values.
const (
	Movies = "movies"
	TV     = "tv"
	Anime  = "anime"
)

type Kind string

const (
	Movie   Kind = "movie"
	Episode Kind = "episode"
)

var (
	ErrUnrecognised = errors.New("naming: no movie or episode in the file name")
	ErrExtra        = errors.New("naming: extra or trailer (LIB-3)")
	ErrSample       = errors.New("naming: sample file")
)

// Entry is where one video file belongs. Key names the movie or show:
// normalised title plus year, so cosmetic renames ("Heat.1995" → "Heat (1995)
// {tmdb-949}") give the same key. Folder is the folder the name came from;
// the store uses it to tell a rename from two folders that clash (spec §3.6).
type Entry struct {
	Kind    Kind
	Key     string            // "movie:heat|1995" or "show:show|2019"
	Folder  string            // the movie's folder or the show's top folder, inside the root; "" for loose files
	Title   string            // movie or show title
	Year    int               // 0 when unknown
	IDs     map[string]string // provider ID hints from the names, e.g. tmdb → 949
	Version string            // label among several files of one item: "2160p", "Director's Cut"

	// Episodes only.
	Season       int
	Episodes     []int  // one, or each episode of a multi-episode file
	AirDate      string // daily shows (YYYY-MM-DD), with no episode numbers
	Absolute     *int   // anime absolute number, kept for META-2's mapping
	EpisodeTitle string
}

// sampleMaxBytes: a file named "sample" is skipped only below this size, so a
// real film called "The Sample" is still catalogued.
const sampleMaxBytes = 300 << 20

// Classify places rel (a slash-separated path inside a root of a libType
// library) of the given size. Errors wrap ErrUnrecognised, ErrExtra or
// ErrSample; the scan lists such files instead of guessing (spec §3.1).
func Classify(libType, rel string, size int64) (Entry, error) {
	parts := strings.Split(rel, "/")
	dirs, file := parts[:len(parts)-1], parts[len(parts)-1]
	stem := strings.TrimSuffix(file, path.Ext(file))
	if size < sampleMaxBytes && sampleRe.MatchString(stem) {
		return Entry{}, ErrSample
	}
	for _, d := range dirs {
		if extraFolders[strings.ToLower(d)] {
			return Entry{}, ErrExtra
		}
	}
	if isExtraName(stem) && len(dirs) > 0 {
		return Entry{}, ErrExtra
	}
	switch libType {
	case Movies:
		return classifyMovie(dirs, stem), nil
	case TV, Anime:
		e, err := classifyEpisode(libType == Anime, dirs, stem)
		// Anime libraries also hold films: a file outside season folders with
		// no episode number is a movie.
		if errors.Is(err, ErrUnrecognised) && libType == Anime && !hasSeasonDir(dirs) {
			return classifyMovie(dirs, stem), nil
		}
		return e, err
	}
	return Entry{}, fmt.Errorf("naming: unknown library type %q", libType)
}

// extraFolders are Plex/Jellyfin extras folders.
var extraFolders = map[string]bool{
	"behind the scenes": true, "deleted scenes": true, "featurettes": true, "interviews": true,
	"scenes": true, "shorts": true, "trailers": true, "extras": true, "other": true, "others": true,
}

func isExtraName(stem string) bool {
	lower := strings.ToLower(stem)
	if lower == "trailer" {
		return true
	}
	for _, s := range []string{"-behindthescenes", "-deleted", "-featurette", "-interview", "-scene", "-short", "-trailer", "-other"} {
		if strings.HasSuffix(lower, s) && len(lower) > len(s) {
			return true
		}
	}
	return false
}

func classifyMovie(dirs []string, stem string) Entry {
	file := parseName(stem)
	ni := file
	if len(dirs) > 0 {
		// The innermost folder names the movie unless it has no year and the
		// file does ("Random Folder/Arrival.2016.1080p.mkv").
		if folder := parseName(dirs[len(dirs)-1]); folder.Year != 0 || file.Year == 0 {
			ni = folder
		}
		for k, v := range file.IDs {
			if _, ok := ni.IDs[k]; !ok {
				ni.IDs[k] = v
			}
		}
	}
	return Entry{Kind: Movie, Key: "movie:" + key(ni.Title) + "|" + strconv.Itoa(ni.Year), Folder: strings.Join(dirs, "/"),
		Title: ni.Title, Year: ni.Year, IDs: ni.IDs, Version: versionLabel(stem)}
}

var (
	seasonDirRe    = regexp.MustCompile(`(?i)^(?:season|series|staffel|saison|s)[\s._-]*(\d{1,4})$`)
	specialsRe     = regexp.MustCompile(`(?i)^specials?$`)
	sxeRe          = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,4})[\s._-]?e(\d{1,4})`)
	moreEpRe       = regexp.MustCompile(`(?i)^(?:-?e|-)(\d{1,4})(?:[^0-9]|$)`)
	nxnRe          = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^0-9]|$)`)
	dateRe         = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})[-. ](\d{2})[-. ](\d{2})(?:[^0-9]|$)`)
	absAfterRe     = regexp.MustCompile(`^\s*-\s*(\d{1,4})(?:v\d+)?(?:\s|$)`)
	absOnlyRe      = regexp.MustCompile(`(?i)^(.*?)\s+-\s+(?:(?:e|ep|episode)\s?)?(\d{1,4})(?:v\d+)?(?:\s+\([^)]*\))?(?:\s+-\s+(.*))?$`)
	epWordRe       = regexp.MustCompile(`(?i)^(.*?)\s+(?:e|ep|episode)\s?(\d{1,4})(?:v\d+)?(?:\s+-\s+(.*))?$`)
	specialRe      = regexp.MustCompile(`(?i)^(.*?)\s+(?:-\s+)?(?:ova|oad|sp|special)\s?-?\s?(\d{1,3})(?:v\d+)?(?:\s+-\s+(.*))?$`)
	parenQualityRe = regexp.MustCompile(`(?i)\([^)]*\b(?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|x264|x265|hevc|10bit|bluray|web-?dl|webrip)\b[^)]*\)`)
	junkRe         = regexp.MustCompile(`(?i)\s+-?(?:proper|repack)\b.*$`)
)

func seasonFromDir(name string) (int, bool) {
	if specialsRe.MatchString(name) {
		return 0, true
	}
	if m := seasonDirRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

func hasSeasonDir(dirs []string) bool {
	for _, d := range dirs {
		if _, ok := seasonFromDir(d); ok {
			return true
		}
	}
	return false
}

// maxEpisodesPerFile bounds a range like S01E01-E99 from a mis-tagged name.
const maxEpisodesPerFile = 12

// classifyEpisode handles TV and anime names:
//
//	Show (2019) {tvdb-123}/Season 01/Show - S01E02 - Title.mkv   (also S01E02E03, S01E02-E04, 1x02)
//	Show/Show - 2024-03-01 - Title.mkv                            (daily: season = year)
//	Anime/[Group] Show - 1094 [1080p].mkv, Show - OVA 2           (absolute numbers, specials)
func classifyEpisode(anime bool, dirs []string, stem string) (Entry, error) {
	e := Entry{Kind: Episode, Version: resolution(stem)}
	folderSeason, haveFolderSeason := 0, false
	for _, d := range dirs[min(1, len(dirs)):] {
		if n, ok := seasonFromDir(d); ok {
			folderSeason, haveFolderSeason = n, true
		}
	}
	haveSeason := false
	s := dotsToSpaces(bracketRe.ReplaceAllString(stem, " "))
	s = parenQualityRe.ReplaceAllString(parenYearRe.ReplaceAllString(s, " "), " ") // "(1080p)" as in SubsPlease names
	s = spacesRe.ReplaceAllString(s, " ")
	showPart, rest := "", ""

	if m := sxeRe.FindStringSubmatchIndex(s); m != nil {
		e.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		first, _ := strconv.Atoi(s[m[4]:m[5]])
		haveSeason = true
		last := first
		showPart, rest = s[:m[0]], s[m[1]:]
		for mm := moreEpRe.FindStringSubmatchIndex(rest); mm != nil; mm = moreEpRe.FindStringSubmatchIndex(rest) {
			if n, _ := strconv.Atoi(rest[mm[2]:mm[3]]); n > last {
				last = n
			}
			rest = rest[mm[3]:]
		}
		if last-first >= maxEpisodesPerFile {
			last = first
		}
		for n := first; n <= last; n++ {
			e.Episodes = append(e.Episodes, n)
		}
		if anime {
			if mm := absAfterRe.FindStringSubmatchIndex(rest); mm != nil {
				n, _ := strconv.Atoi(rest[mm[2]:mm[3]])
				e.Absolute, rest = &n, rest[mm[3]:]
			}
		}
	} else if m := nxnRe.FindStringSubmatchIndex(s); m != nil {
		e.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		ep, _ := strconv.Atoi(s[m[4]:m[5]])
		haveSeason, e.Episodes = true, []int{ep}
		showPart, rest = s[:m[2]], s[m[5]:]
	} else if m := dateRe.FindStringSubmatchIndex(s); m != nil {
		e.AirDate = s[m[2]:m[3]] + "-" + s[m[4]:m[5]] + "-" + s[m[6]:m[7]]
		e.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		haveSeason = true
		showPart, rest = s[:m[2]], s[m[7]:]
	} else if m := specialRe.FindStringSubmatch(strings.TrimSpace(s)); anime && m != nil {
		n, _ := strconv.Atoi(m[2])
		e.Season, e.Episodes, haveSeason = 0, []int{n}, true
		showPart, rest = m[1], m[3]
	} else if m := absOnlyRe.FindStringSubmatch(strings.TrimSpace(s)); anime && m != nil {
		n, _ := strconv.Atoi(m[2])
		e.Absolute, showPart, rest = &n, m[1], m[3]
	} else if m := epWordRe.FindStringSubmatch(strings.TrimSpace(s)); anime && m != nil {
		n, _ := strconv.Atoi(m[2])
		e.Absolute, showPart, rest = &n, m[1], m[3]
	} else {
		return Entry{}, ErrUnrecognised
	}

	if !haveSeason && e.Episodes == nil && e.Absolute != nil {
		// Absolute-only anime: the folder's season (specials stay 0), else 1,
		// numbered by the absolute number until META-2 maps it.
		e.Season = 1
		if haveFolderSeason {
			e.Season = folderSeason
		}
		e.Episodes = []int{*e.Absolute}
	} else if !haveSeason && haveFolderSeason {
		e.Season = folderSeason
	}
	if haveFolderSeason && folderSeason == 0 {
		e.Season = 0 // anything in a Specials folder is a special
	}
	e.EpisodeTitle = episodeTitle(rest)

	var ni nameInfo
	if len(dirs) > 0 {
		ni = parseName(dirs[0])
		e.Folder = dirs[0]
	} else {
		ni = parseName(showPart)
	}
	for k, v := range idHints(stem) {
		if _, ok := ni.IDs[k]; !ok {
			ni.IDs[k] = v
		}
	}
	if ni.Title == "" {
		return Entry{}, ErrUnrecognised
	}
	e.Key, e.Title, e.Year, e.IDs = "show:"+key(ni.Title)+"|"+strconv.Itoa(ni.Year), ni.Title, ni.Year, ni.IDs
	return e, nil
}

// episodeTitle extracts "Title" from " - Title -GROUP" (brackets already removed).
func episodeTitle(rest string) string {
	t := strings.TrimLeft(strings.TrimSpace(rest), "-. ")
	if loc := qualityRe.FindStringSubmatchIndex(" " + t); loc != nil {
		t = t[:max(0, loc[2]-1)]
	}
	t = strings.TrimSpace(releaseGroupRe.ReplaceAllString(strings.TrimSpace(t), ""))
	return cleanTitle(junkRe.ReplaceAllString(t, ""))
}
