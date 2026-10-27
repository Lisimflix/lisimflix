// Package library finds video files under a media root and probes them
// read-only (LIB-1, spec §3.6). Library definitions and the catalogue (LIB-2)
// build on it.
package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Revision identifies one version of a source file. A size or mtime change
// makes a new revision and invalidates any probe of the old one.
type Revision struct {
	Size    int64
	ModTime int64 // Unix nanoseconds
}

func RevisionOf(info fs.FileInfo) Revision {
	return Revision{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

// File is one discovered video file: its slash-separated path inside the root
// and its revision when it was seen.
type File struct {
	Path string
	Rev  Revision
}

// Discover walks root read-only, in lexical order, and calls found for each
// video file. Symlinked files are followed only while they stay inside root;
// symlinked folders are not walked. Any unreadable folder fails the whole
// walk, so a caller never mistakes a partial walk for a complete one (spec
// §3.6: files go missing only after a successful enumeration).
// Planned: LIB-2 (the catalogue scan enumerates each library root with it).
func Discover(ctx context.Context, root *os.Root, found func(File) error) error {
	fsys := root.FS()
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !isVideo(d.Name()) {
			return nil
		}
		info, err := fs.Stat(fsys, p) // follows symlinks; root refuses ones that leave it
		if err != nil {
			if d.Type()&fs.ModeSymlink != 0 || errors.Is(err, fs.ErrNotExist) {
				return nil // escaping or dangling link, or deleted since the folder was read
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return found(File{Path: p, Rev: RevisionOf(info)})
	})
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".mts": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".flv": true, ".ogv": true, ".3gp": true, ".divx": true, ".asf": true, ".rmvb": true,
	".f4v": true, ".vob": true,
}

// isVideo also excludes hidden files and in-progress downloads such as
// "x.mkv.part" (their extension isn't a video one).
func isVideo(name string) bool {
	return !strings.HasPrefix(name, ".") && videoExts[strings.ToLower(path.Ext(name))]
}

func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") {
		return true // .grab, .Trash-1000, @eaDir (Synology) ...
	}
	switch strings.ToLower(name) {
	case "#recycle", "$recycle.bin", "lost+found", "system volume information", "sample", "samples":
		return true
	}
	return false
}
