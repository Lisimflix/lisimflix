package metadata

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

// maxNFOBytes bounds how much of an untrusted NFO file is read.
const maxNFOBytes = 1 << 20

var idPatterns = map[string]*regexp.Regexp{
	"tmdb":  regexp.MustCompile(`^[1-9][0-9]{0,9}$`),
	"tvdb":  regexp.MustCompile(`^[1-9][0-9]{0,9}$`),
	"anidb": regexp.MustCompile(`^[1-9][0-9]{0,9}$`),
	"imdb":  regexp.MustCompile(`^tt[0-9]{7,10}$`),
}

var nfoURLPatterns = []struct {
	ns string
	re *regexp.Regexp
}{
	{"imdb", regexp.MustCompile(`imdb\.com/title/(tt[0-9]+)`)},
	{"tmdb", regexp.MustCompile(`themoviedb\.org/(?:movie|tv)/([0-9]+)`)},
	{"tvdb", regexp.MustCompile(`thetvdb\.com/[^\s]*?[?&]id=([0-9]+)`)},
}

type nfoDoc struct {
	UniqueIDs []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	TMDBID string `xml:"tmdbid"`
	IMDBID string `xml:"imdbid"`
	TVDBID string `xml:"tvdbid"`
	ID     string `xml:"id"`
}

// parseNFO extracts provider IDs from a Kodi NFO: XML elements first, then a
// bare provider URL. Values that don't match their namespace's pattern are
// dropped; the first value for a namespace wins.
func parseNFO(b []byte) map[string]string {
	ids := map[string]string{}
	add := func(ns, v string) {
		v = strings.TrimSpace(v)
		if strings.Trim(v, "0123456789") == "" { // 0949 is 949, as in name hints
			v = strings.TrimLeft(v, "0")
		}
		re, ok := idPatterns[ns]
		if _, have := ids[ns]; ok && !have && re.MatchString(v) {
			ids[ns] = v
		}
	}
	if trimmed := bytes.TrimSpace(bytes.TrimPrefix(b, []byte("\uFEFF"))); bytes.HasPrefix(trimmed, []byte("<")) {
		var doc nfoDoc
		dec := xml.NewDecoder(bytes.NewReader(trimmed))
		dec.Strict = false // Entity stays nil, so custom entities are never expanded.
		dec.CharsetReader = latin1Reader
		if err := dec.Decode(&doc); err == nil {
			for _, u := range doc.UniqueIDs {
				add(strings.ToLower(strings.TrimSpace(u.Type)), u.Value)
			}
			add("tmdb", doc.TMDBID)
			add("imdb", doc.IMDBID)
			add("tvdb", doc.TVDBID)
			add("imdb", doc.ID)
		}
	}
	if len(ids) == 0 {
		for _, p := range nfoURLPatterns {
			if m := p.re.FindSubmatch(b); m != nil {
				add(p.ns, string(m[1]))
			}
		}
	}
	return ids
}

// latin1Reader decodes an NFO declared as ISO-8859-1 (or US-ASCII, a subset).
// Other declared encodings fail, so the file gives no XML IDs.
func latin1Reader(label string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "latin1", "us-ascii":
	default:
		return nil, fmt.Errorf("metadata: NFO encoding %q isn't supported", label)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c) // Latin-1 bytes are the first 256 code points
	}
	return strings.NewReader(string(runes)), nil
}

// readNFOs reads the NFO files at paths (relative to root) and merges their
// IDs. A file that is missing, unreadable or not a regular file (a directory,
// a FIFO) is skipped. A namespace given different values by two files is
// listed in conflicts and keeps its first value.
func readNFOs(root *os.Root, paths []string) (ids map[string]string, conflicts []string) {
	ids = map[string]string{}
	for _, p := range paths {
		b, ok := readNFO(root, p)
		if !ok {
			continue
		}
		for ns, v := range parseNFO(b) {
			switch have, ok := ids[ns]; {
			case !ok:
				ids[ns] = v
			case have != v && !slices.Contains(conflicts, ns):
				conflicts = append(conflicts, ns)
			}
		}
	}
	return ids, conflicts
}

// readNFO reads at most maxNFOBytes of a regular file. The open doesn't
// block, so a FIFO named like an NFO can't hang matching.
func readNFO(root *os.Root, p string) ([]byte, bool) {
	f, err := root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxNFOBytes))
	return b, err == nil
}
