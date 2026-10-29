//go:build unix

package metadata

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO named like an NFO is skipped without blocking (opening one for
// reading would wait for a writer forever); the real NFO beside it is read.
func TestReadNFOsSkipsFIFOs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Heat (1995)"), 0o755)
	if err := syscall.Mkfifo(filepath.Join(dir, "Heat (1995)", "movie.nfo"), 0o644); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	os.WriteFile(filepath.Join(dir, "Heat (1995)", "Heat (1995).nfo"), []byte(`<movie><tmdbid>949</tmdbid></movie>`), 0o644)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan map[string]string, 1)
	go func() {
		ids, _ := readNFOs(root, []string{"Heat (1995)/movie.nfo", "Heat (1995)/Heat (1995).nfo"})
		done <- ids
	}()
	select {
	case ids := <-done:
		if ids["tmdb"] != "949" {
			t.Fatalf("ids %v: the FIFO hid the real NFO", ids)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO named movie.nfo blocked")
	}
}
