// Package playback is M1 direct play: it serves original MP4 files resolved
// from an item and version through authorised range requests bound to live
// playback sessions. It never transcodes or remuxes.
package playback

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Source is one playable file: what a session plays and where it lives under
// the media root.
type Source struct {
	ItemID, VersionID, FileID string
	RelativePath              string
	SourceRevision            string
}

// Resolver maps a request to a Source the principal may play. ErrNotFound
// for an unknown or forbidden item/version (same 404 either way).
type Resolver interface {
	Resolve(p contracts.Principal, itemID, versionID string) (Source, error)
}

// ErrNotFound is the Resolver's answer for an unknown or forbidden item/version.
var ErrNotFound = errors.New("playback: no such item or version")

// M1 is M1's resolver: one configured movie, itemID == versionID == movie.ID.
type M1 struct {
	movie     contracts.Movie
	rel       string
	authorize func(contracts.Principal, string) bool
}

// Single is M1's resolver: one movie, itemID == versionID == movie.ID and
// fileID == movie.ID, so source revisions are the ones init wrote.
// LIB-2c replaces Single with a catalogue Resolver.
func Single(movie contracts.Movie, relativePath string, authorize func(contracts.Principal, string) bool) *M1 {
	return &M1{movie: movie, rel: relativePath, authorize: authorize}
}

func (m *M1) source() Source {
	return Source{ItemID: m.movie.ID, VersionID: m.movie.ID, FileID: m.movie.ID, RelativePath: m.rel, SourceRevision: m.movie.SourceRevision}
}

// Resolve implements Resolver.
func (m *M1) Resolve(p contracts.Principal, itemID, versionID string) (Source, error) {
	if itemID != m.movie.ID || versionID != m.movie.ID || !m.authorize(p, m.movie.ID) {
		return Source{}, ErrNotFound
	}
	return m.source(), nil
}

// Movies is the GET /api/v1/movies list: the movie if p may play it.
func (m *M1) Movies(p contracts.Principal) []contracts.Movie {
	if m.authorize(p, m.movie.ID) {
		return []contracts.Movie{m.movie}
	}
	return nil
}

// Check reports whether the configured file is present and unchanged. The
// integration's readiness check calls it.
func (m *M1) Check(root *os.Root) error {
	_, err := statSource(root, m.source())
	return err
}

var (
	errSourceChanged     = errors.New("playback: the source file changed since it was configured")
	errSourceUnavailable = errors.New("playback: the source file is not available")
)

// Revision is the source revision of a file (contracts.SourceRevision with
// the file ID). init, startup and playback all use it.
func Revision(fileID string, info fs.FileInfo) string {
	return contracts.SourceRevision(fileID, info.Size(), info.ModTime().UnixNano())
}

// statSource returns the source's metadata through the root, which refuses
// paths and symlinks that leave it. It never reveals the path in errors.
func statSource(root *os.Root, src Source) (fs.FileInfo, error) {
	if !validRelative(src.RelativePath) {
		return nil, errSourceUnavailable
	}
	info, err := root.Stat(src.RelativePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errSourceUnavailable
	}
	if Revision(src.FileID, info) != src.SourceRevision {
		return nil, errSourceChanged
	}
	return info, nil
}

// open opens the session's file and confirms it is the same file, with the
// same revision, as when the session started. The caller closes it.
func (s *Server) open(sess *session) (*os.File, fs.FileInfo, error) {
	f, err := s.root.Open(sess.src.RelativePath)
	if err != nil {
		return nil, nil, errSourceUnavailable
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errSourceUnavailable
	}
	if !os.SameFile(info, sess.info) || Revision(sess.src.FileID, info) != sess.src.SourceRevision {
		f.Close()
		return nil, nil, errSourceChanged
	}
	return f, info, nil
}

func validRelative(p string) bool {
	return p != "" && filepath.IsLocal(p)
}
