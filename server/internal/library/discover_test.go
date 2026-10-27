package library

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeTree creates files (slash paths) under a new temp dir, with mtimes an
// hour old so they count as settled.
func writeTree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func discoverAll(t *testing.T, root *os.Root) ([]File, error) {
	t.Helper()
	var got []File
	err := Discover(context.Background(), root, func(f File) error {
		got = append(got, f)
		return nil
	})
	return got, err
}

func paths(files []File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestDiscoverFindsOnlyVideoFiles(t *testing.T) {
	dir := writeTree(t,
		"Movies/Alien (1979)/Alien (1979).mkv",
		"Movies/Alien (1979)/Alien (1979).nfo",
		"Movies/Alien (1979)/.Alien (1979).mkv.swp",
		"Movies/Heat (1995)/Heat.MP4",
		"Movies/Heat (1995)/Sample/sample.mkv",
		"TV/Show/Season 01/S01E01.mkv.part",
		"TV/Show/Season 01/S01E02.m2ts",
		".grab/x.mkv",
		"@eaDir/thumb.mkv",
		"#recycle/old.mkv",
	)
	outside := writeTree(t, "secret.mkv")
	if err := os.Symlink("../Heat (1995)/Heat.MP4", filepath.Join(dir, "Movies/Alien (1979)/linked.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.mkv"), filepath.Join(dir, "escape.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../..", filepath.Join(dir, "TV/loop")); err != nil {
		t.Fatal(err)
	}

	got, err := discoverAll(t, openRoot(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Movies/Alien (1979)/Alien (1979).mkv",
		"Movies/Alien (1979)/linked.mp4",
		"Movies/Heat (1995)/Heat.MP4",
		"TV/Show/Season 01/S01E02.m2ts",
	}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("paths:\n got %q\nwant %q", paths(got), want)
	}
	info, err := os.Stat(filepath.Join(dir, "Movies/Heat (1995)/Heat.MP4"))
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Rev != RevisionOf(info) || got[2].Rev.Size != int64(len("Movies/Heat (1995)/Heat.MP4")) {
		t.Fatalf("revision %+v", got[2].Rev)
	}
}

func TestDiscoverFailsOnUnreadableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any folder")
	}
	dir := writeTree(t, "a/movie.mkv", "b/movie.mkv")
	if err := os.Chmod(filepath.Join(dir, "b"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "b"), 0o755) })
	if _, err := discoverAll(t, openRoot(t, dir)); err == nil {
		t.Fatal("a partial walk must fail")
	}
}

func TestDiscoverStopsWhenCancelled(t *testing.T) {
	dir := writeTree(t, "a.mkv", "b.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err := Discover(ctx, openRoot(t, dir), func(File) error {
		n++
		cancel()
		return nil
	})
	if err != context.Canceled || n != 1 {
		t.Fatalf("err %v after %d files", err, n)
	}
}
