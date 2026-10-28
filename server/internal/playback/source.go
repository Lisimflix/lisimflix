// Package playback is M1 direct play: it serves one configured original MP4
// through authorised range requests bound to live playback sessions. It never
// transcodes or remuxes.
package playback

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Source is the one movie this server plays: its public metadata and its path
// inside the media root.
type Source struct {
	Movie        contracts.Movie
	RelativePath string
}

var (
	errSourceChanged     = errors.New("playback: the source file changed since it was configured")
	errSourceUnavailable = errors.New("playback: the source file is not available")
)

// Revision is the source revision of the configured movie's file
// (contracts.SourceRevision with the movie ID as the file ID). init, startup
// and playback all use it.
func Revision(movieID string, info fs.FileInfo) string {
	return contracts.SourceRevision(movieID, info.Size(), info.ModTime().UnixNano())
}

// statSource returns the source's metadata through the root, which refuses
// paths and symlinks that leave it. It never reveals the path in errors.
func (s *Server) statSource() (fs.FileInfo, error) {
	info, err := s.root.Stat(s.src.RelativePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errSourceUnavailable
	}
	if Revision(s.src.Movie.ID, info) != s.src.Movie.SourceRevision {
		return nil, errSourceChanged
	}
	return info, nil
}

// Check reports whether the configured source is present and unchanged. The
// integration's readiness check calls it.
func (s *Server) Check() error {
	_, err := s.statSource()
	return err
}

// open opens the session's file and confirms it is the same file, with the
// same revision, as when the session started. The caller closes it.
func (s *Server) open(sess *session) (*os.File, fs.FileInfo, error) {
	f, err := s.root.Open(s.src.RelativePath)
	if err != nil {
		return nil, nil, errSourceUnavailable
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errSourceUnavailable
	}
	if !os.SameFile(info, sess.info) || Revision(sess.movieID, info) != sess.revision {
		f.Close()
		return nil, nil, errSourceChanged
	}
	return f, info, nil
}

func validRelative(p string) bool {
	return p != "" && filepath.IsLocal(p)
}
