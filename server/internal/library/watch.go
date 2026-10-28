package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watch states of a root, for the status view.
const (
	WatchOff     = "off"     // not watched (yet): periodic scans only
	WatchOn      = "on"      // every folder is watched
	WatchLimited = "limited" // the inotify watch limit was reached part-way
)

// watchHooks receive what the watcher learns. They are called from the
// watcher's goroutine and must not block for long.
type watchHooks struct {
	hint     func(rootID, scope string)     // something changed at scope, inside the root
	lost     func(rootID string)            // the root folder itself was removed or moved
	limited  func(rootID string, err error) // a new folder couldn't be watched
	overflow func()                         // the kernel dropped events: rescan everything
	failed   func(error)                    // any other watcher error
}

// watcher turns inotify events under library roots into scan hints. Events
// are hints only (spec §3.6): every read still goes through os.Root, and
// periodic and recheck scans cover anything it misses.
type watcher struct {
	fw    *fsnotify.Watcher
	hooks watchHooks

	mu    sync.Mutex
	roots map[string]string // root ID → absolute folder
}

func newWatcher(h watchHooks) (*watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &watcher{fw: fw, hooks: h, roots: map[string]string{}}, nil
}

// watch adds the root's folder and every folder Discover would walk. It
// returns WatchLimited (with the error) when a folder can't be watched,
// usually because fs.inotify.max_user_watches is too low; folders already
// added stay watched.
func (w *watcher) watch(rootID, abs string) (string, error) {
	w.unwatch(rootID)
	w.mu.Lock()
	w.roots[rootID] = abs
	w.mu.Unlock()
	if err := w.addTree(abs); err != nil {
		return WatchLimited, err
	}
	return WatchOn, nil
}

// addTree watches dir and the folders under it, skipping the ones Discover
// skips. Symlinked folders aren't followed (WalkDir doesn't).
func (w *watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil // vanished or unreadable meanwhile: the scan reports it
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		return w.fw.Add(p)
	})
}

// unwatch stops watching a root's folders.
func (w *watcher) unwatch(rootID string) {
	w.mu.Lock()
	abs, ok := w.roots[rootID]
	delete(w.roots, rootID)
	w.mu.Unlock()
	if !ok {
		return
	}
	for _, p := range w.fw.WatchList() {
		if p == abs || strings.HasPrefix(p, abs+string(filepath.Separator)) {
			w.fw.Remove(p)
		}
	}
}

// watching reports which roots still have a watch on their own folder. An
// unmount drops watches without any event, so the scheduler checks this.
func (w *watcher) watching() map[string]bool {
	w.mu.Lock()
	byPath := make(map[string]string, len(w.roots))
	for id, abs := range w.roots {
		byPath[abs] = id
	}
	w.mu.Unlock()
	out := map[string]bool{}
	for _, p := range w.fw.WatchList() {
		if id, ok := byPath[p]; ok {
			out[id] = true
		}
	}
	return out
}

// locate maps an event path to its root and a slash path inside it.
func (w *watcher) locate(name string) (rootID, rel string, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, abs := range w.roots {
		r, err := filepath.Rel(abs, name)
		if err != nil {
			continue
		}
		r = filepath.ToSlash(r)
		if fs.ValidPath(r) {
			return id, r, true
		}
	}
	return "", "", false
}

// run handles events until ctx ends, then closes the watcher.
func (w *watcher) run(ctx context.Context) {
	defer w.fw.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.event(ev)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				w.hooks.overflow()
			} else {
				w.hooks.failed(err)
			}
		}
	}
}

func (w *watcher) event(ev fsnotify.Event) {
	rootID, rel, ok := w.locate(ev.Name)
	if !ok {
		return
	}
	gone := ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)
	if rel == "." {
		if gone {
			w.hooks.lost(rootID)
		}
		return
	}
	if skippedPath(rel) {
		return
	}
	if ev.Has(fsnotify.Create) {
		if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
			// A new or moved-in folder: watch it, and scan it, since files
			// may have landed before its watch was added.
			if err := w.addTree(ev.Name); err != nil {
				w.hooks.limited(rootID, err)
			}
			w.hooks.hint(rootID, rel)
			return
		}
	}
	// A removed or moved-away name may have been a folder, whatever its
	// extension; anything else matters only if it's a video file.
	if gone || (ev.Has(fsnotify.Create) || ev.Has(fsnotify.Write)) && isVideo(path.Base(rel)) {
		w.hooks.hint(rootID, rel)
	}
}
