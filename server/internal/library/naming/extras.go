package naming

import (
	"path"
	"strconv"
	"strings"
)

// ExtraType is a Plex/Jellyfin extra kind: "trailer", "featurette",
// "behindTheScenes", "deletedScene", "interview", "scene", "short" or "other".
type ExtraType string

// ExtraEntry is what a path says about an extra (LIB-3).
type ExtraEntry struct {
	Folder  string   // the folder the extra belongs to, inside the root; "" when there is none
	Folders []string // folders to try, in order: Folder, then up through season and Specials folders; empty when nothing can be tried
	Key     string   // "heat|1995" for a suffix name whose stem has a year; "" otherwise
	Type    ExtraType
	Title   string
}

// extraFolders are Plex/Jellyfin extras folders.
var extraFolders = map[string]ExtraType{
	"behind the scenes": "behindTheScenes", "deleted scenes": "deletedScene", "featurettes": "featurette",
	"interviews": "interview", "scenes": "scene", "shorts": "short", "trailers": "trailer",
	"extras": "other", "other": "other", "others": "other",
}

// extraSuffixes are Plex's "Name-trailer.mkv" names; name titles an extra
// whose stem is just its folder's name.
var extraSuffixes = []struct {
	suffix string
	typ    ExtraType
	name   string
}{
	{"-behindthescenes", "behindTheScenes", "Behind the Scenes"}, {"-deleted", "deletedScene", "Deleted Scene"},
	{"-featurette", "featurette", "Featurette"}, {"-interview", "interview", "Interview"}, {"-scene", "scene", "Scene"},
	{"-short", "short", "Short"}, {"-trailer", "trailer", "Trailer"}, {"-other", "other", "Other"},
}

// Extra reports whether rel (a slash-separated path inside a root) is an
// extra, and where it belongs. It is type-blind; Classify adds the episode
// rule for TV and anime libraries.
func Extra(rel string) (ExtraEntry, bool) {
	parts := strings.Split(rel, "/")
	dirs, file := parts[:len(parts)-1], parts[len(parts)-1]
	x, _, ok := extra(dirs, strings.TrimSuffix(file, path.Ext(file)))
	return x, ok
}

// extra also says whether a suffix name, not a folder, made the file an
// extra: Classify tries an episode first for those (decision 6c).
func extra(dirs []string, stem string) (x ExtraEntry, bySuffix, ok bool) {
	for i, d := range dirs {
		t, isExtra := extraFolders[strings.ToLower(d)]
		// A top-level extras folder holding further folders is a collection (decision 6b).
		if !isExtra || i == 0 && len(dirs) > 1 {
			continue
		}
		x = ExtraEntry{Folder: strings.Join(dirs[:i], "/"), Type: t, Title: stem}
		// An ordinary folder between the extras folder and the file can't be told
		// from a collection, so placement gets nothing to try (ruling F9).
		if allExtraFolders(dirs[i+1:]) {
			x.Folders = climb(dirs[:i])
		}
		return x, false, true
	}
	if len(dirs) == 0 {
		return x, false, false // a loose "Name-trailer.mkv" has nothing to belong to
	}
	own := dirs[len(dirs)-1]
	x = ExtraEntry{Folder: strings.Join(dirs, "/"), Folders: climb(dirs)}
	if strings.EqualFold(stem, "trailer") {
		x.Type, x.Title = "trailer", "Trailer"
		return x, true, true
	}
	for _, s := range extraSuffixes {
		if len(stem) <= len(s.suffix) || !strings.EqualFold(stem[len(stem)-len(s.suffix):], s.suffix) {
			continue
		}
		folder := parseName(own)
		// "The Crime-Scene (2019)/The Crime-Scene.mkv" is the movie (decision 6c).
		// The raw stem, suffix word included, must key like the folder's title; a
		// year-named extra ("The Interview (2014)-interview") keys differently.
		if key(stem) == key(folder.Title) {
			return ExtraEntry{}, false, false
		}
		pre := strings.TrimSpace(stem[:len(stem)-len(s.suffix)])
		ni := parseName(pre)
		x.Type, x.Title = s.typ, pre
		if ni.Year != 0 {
			x.Key = key(ni.Title) + "|" + strconv.Itoa(ni.Year)
		}
		if key(ni.Title) == key(folder.Title) && ni.Year == folder.Year {
			x.Title = s.name
		}
		return x, true, true
	}
	return ExtraEntry{}, false, false
}

func allExtraFolders(dirs []string) bool {
	for _, d := range dirs {
		if _, ok := extraFolders[strings.ToLower(d)]; !ok {
			return false
		}
	}
	return true
}

// climb lists the folder dirs names, then its parents while the folder is a
// season or Specials folder: season extras attach to the show (decision 3).
func climb(dirs []string) []string {
	var fs []string
	for n := len(dirs); n > 0; n-- {
		fs = append(fs, strings.Join(dirs[:n], "/"))
		if _, season := seasonFromDir(dirs[n-1]); !season {
			break
		}
	}
	return fs
}

// IsResolutionLabel reports whether label names only a resolution, the
// adoption class of decision 2.
func IsResolutionLabel(label string) bool {
	l := strings.ToLower(label)
	return l == "" || resolution(l) == l
}

// FoldLabel lower-cases ASCII letters only, matching SQLite lower(), so Go and
// SQL group labels the same way (decision 6e).
func FoldLabel(label string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, label)
}

// NamesFolder reports whether folder's own name names the movie or show with
// scan key scanKey ("movie:heat|1995"): same title, and the same year unless
// the folder has none. A movie loose in a collection folder ("Marvel/Iron Man
// (2008).mkv") doesn't, so an extra without a year can't attach to it there.
func NamesFolder(folder, scanKey string) bool {
	_, k, _ := strings.Cut(scanKey, ":")
	title, year, _ := strings.Cut(k, "|")
	ni := parseName(path.Base(folder))
	return key(ni.Title) == title && (ni.Year == 0 || strconv.Itoa(ni.Year) == year)
}
