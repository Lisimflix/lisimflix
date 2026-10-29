package library

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

type watchLog struct {
	hints chan [2]string
	lost  chan string
}

func startWatcher(t *testing.T) (*watcher, *watchLog) {
	t.Helper()
	l := &watchLog{hints: make(chan [2]string, 100), lost: make(chan string, 10)}
	w, err := newWatcher(watchHooks{
		hint:     func(root, scope string) { l.hints <- [2]string{root, scope} },
		lost:     func(root string) { l.lost <- root },
		limited:  func(root string, err error) { t.Errorf("limited %s: %v", root, err) },
		overflow: func() { t.Error("overflow") },
		failed:   func(err error) { t.Error(err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return w, l
}

// expect collects hint scopes for root until want are all seen, failing on
// any scope outside allowed (want plus extra).
func (l *watchLog) expect(t *testing.T, root string, want []string, extra ...string) {
	t.Helper()
	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < len(want) {
		select {
		case h := <-l.hints:
			if h[0] != root || !slices.Contains(want, h[1]) && !slices.Contains(extra, h[1]) {
				t.Fatalf("unexpected hint %v", h)
			}
			if slices.Contains(want, h[1]) {
				seen[h[1]] = true
			}
		case <-deadline:
			t.Fatalf("hints seen %v, want %v", seen, want)
		}
	}
}

func (l *watchLog) quiet(t *testing.T) {
	t.Helper()
	select {
	case h := <-l.hints:
		t.Fatalf("unexpected hint %v", h)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatcherHints(t *testing.T) {
	m := newMedia(t)
	m.write("TV/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	os.MkdirAll(filepath.Join(m.dir, "TV/.grab"), 0o755)
	w, l := startWatcher(t)
	abs := filepath.Join(m.dir, "TV")
	if st, err := w.watch("root-1", abs); st != WatchOn || err != nil {
		t.Fatalf("watch = %s, %v", st, err)
	}
	if !w.watching()["root-1"] {
		t.Fatal("root not watched")
	}

	// A new episode: hinted by its own path.
	m.write("TV/Show (2019)/Season 01/Show - S01E02.mkv", 10)
	l.expect(t, "root-1", []string{"Show (2019)/Season 01/Show - S01E02.mkv"})
	l.drain()

	// Files that don't matter: no hints.
	m.write("TV/Show (2019)/Season 01/Show - S01E02.nfo", 10)
	m.write("TV/.grab/Other - S01E01.mkv", 10)
	m.write("TV/Show (2019)/Season 01/Show - S01E03.mkv.part", 10)
	os.Mkdir(filepath.Join(abs, ".incoming"), 0o755) // a new hidden folder isn't watched or hinted
	l.quiet(t)
	m.write("TV/.incoming/Show - S01E04.mkv", 10)
	l.quiet(t)

	// A folder moved in, already full: the folder is hinted, and later
	// changes inside it are watched.
	stage := filepath.Join(t.TempDir(), "New (2020)")
	os.MkdirAll(filepath.Join(stage, "Season 01"), 0o755)
	os.WriteFile(filepath.Join(stage, "Season 01/New - S01E01.mkv"), nil, 0o644)
	if err := os.Rename(stage, filepath.Join(abs, "New (2020)")); err != nil {
		t.Skip("staging folder is on another filesystem:", err)
	}
	l.expect(t, "root-1", []string{"New (2020)"})
	m.write("TV/New (2020)/Season 01/New - S01E02.mkv", 10)
	l.expect(t, "root-1", []string{"New (2020)/Season 01/New - S01E02.mkv"})
	l.drain()

	// A season deleted: hinted whatever the names.
	os.RemoveAll(filepath.Join(abs, "Show (2019)/Season 01"))
	l.expect(t, "root-1", []string{"Show (2019)/Season 01"}, "Show (2019)/Season 01/Show - S01E01.mkv",
		"Show (2019)/Season 01/Show - S01E02.mkv", "Show (2019)/Season 01/Show - S01E02.nfo",
		"Show (2019)/Season 01/Show - S01E03.mkv.part")
	l.drain()

	// The root itself moved away: lost, and no longer watched.
	os.Rename(abs, filepath.Join(m.dir, "away"))
	select {
	case id := <-l.lost:
		if id != "root-1" {
			t.Fatalf("lost %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("root move not reported")
	}

	// unwatch stops hints.
	os.Rename(filepath.Join(m.dir, "away"), abs)
	w.unwatch("root-1")
	m.write("TV/New (2020)/Season 01/New - S01E03.mkv", 10)
	l.quiet(t)
}

func (l *watchLog) drain() {
	for {
		select {
		case <-l.hints:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
