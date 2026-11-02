package arrhook

import (
	"errors"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	errUnmapped    = errors.New("arrhook: path is not under any mapping")
	errOutside     = errors.New("arrhook: path is outside the source's library folders")
	errNotAbsolute = errors.New("arrhook: path must be absolute")
)

var driveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// normRemote cleans a path as a source reports it. Windows drive and UNC paths
// use backslashes, which become slashes; anything else must start with "/".
func normRemote(p string) (string, bool) {
	if driveRe.MatchString(p) || strings.HasPrefix(p, `\\`) {
		p = strings.ReplaceAll(p, `\`, "/")
	} else if !strings.HasPrefix(p, "/") {
		return "", false
	}
	return path.Clean(p), true
}

// within reports whether p is dir or inside it. Whole folders only: "/tv" does
// not contain "/tvshows". "/" and "." contain everything.
func within(p, dir string) bool {
	return dir == "/" || dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// resolve maps a remote path to a scope in one of the source's roots, using
// the longest mapping that covers it.
func (src Source) resolve(remote string) (Scope, error) {
	p, ok := normRemote(remote)
	if !ok {
		return Scope{}, errNotAbsolute
	}
	best := -1
	for i, m := range src.Mappings {
		if within(p, m.Remote) && (best < 0 || len(m.Remote) > len(src.Mappings[best].Remote)) {
			best = i
		}
	}
	if best < 0 {
		return Scope{}, errUnmapped
	}
	m := src.Mappings[best]
	rest := strings.TrimPrefix(strings.TrimPrefix(p, m.Remote), "/")
	local := path.Join(m.Local, rest)
	for _, r := range src.Roots {
		if !within(local, r.Path) {
			continue
		}
		rel := local
		if r.Path != "." {
			rel = strings.TrimPrefix(strings.TrimPrefix(local, r.Path), "/")
		}
		if rel == "" {
			rel = "."
		}
		if !fs.ValidPath(rel) {
			return Scope{}, errOutside
		}
		return Scope{RootID: r.ID, Path: rel}, nil
	}
	return Scope{}, errOutside
}

func cmpScope(a, b Scope) int {
	if c := strings.Compare(a.RootID, b.RootID); c != 0 {
		return c
	}
	return strings.Compare(a.Path, b.Path)
}

// group merges queued scopes: two or more files in one folder become that
// folder (a season pack, or an upgrade replacing files), and a scope inside
// another scope of the same root is dropped. The result is sorted by root,
// then path.
func group(in []Scope) []Scope {
	s := slices.Clone(in)
	slices.SortFunc(s, cmpScope)
	s = slices.Compact(s)

	files := map[Scope]int{} // distinct scopes per folder
	for _, sc := range s {
		if d := path.Dir(sc.Path); d != "." {
			files[Scope{sc.RootID, d}]++
		}
	}
	out := make([]Scope, 0, len(s))
	for _, sc := range s {
		if d := path.Dir(sc.Path); d != "." && files[Scope{sc.RootID, d}] >= 2 {
			sc = Scope{sc.RootID, d}
		}
		out = append(out, sc)
	}
	slices.SortFunc(out, cmpScope)
	out = slices.Compact(out)

	kept := make([]Scope, 0, len(out))
	for _, sc := range out {
		if !containedInOther(sc, out) {
			kept = append(kept, sc)
		}
	}
	return kept
}

func containedInOther(sc Scope, all []Scope) bool {
	for _, t := range all {
		if t.RootID == sc.RootID && t.Path != sc.Path && within(sc.Path, t.Path) {
			return true
		}
	}
	return false
}
