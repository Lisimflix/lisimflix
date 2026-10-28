//go:build integration

package library

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// Run on request: go -C server test -tags integration ./internal/library/
// Needs ffmpeg and ffprobe on PATH.
func TestDiscoverAndProbeRealFilesReadOnly(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("ffprobe is not on PATH")
	}
	dir := t.TempDir()
	for _, name := range []string{"Movies/Clip (2026)/Clip (2026).mp4", "Movies/Late (2026)/Late (2026).mkv"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		gen := exec.Command("ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2",
			"-metadata:s:a:0", "language=eng", p)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v\n%s", name, err, out)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "Movies/Clip (2026)/Clip (2026).mp4"), old, old); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)

	root := openRoot(t, dir)
	got := map[string]Outcome{}
	done := make(chan Outcome)
	q := NewQueue(root, probe.Runner{Path: ffprobe}, DefaultQuiet, func(o Outcome) { done <- o })
	err = Discover(context.Background(), root, func(f File) error { q.Add(f.Path); return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx, DefaultWorkers)
	for range 2 {
		select {
		case o := <-done:
			got[o.Path] = o
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for probes")
		}
	}

	clip := got["Movies/Clip (2026)/Clip (2026).mp4"]
	if clip.Err != nil {
		t.Fatal(clip.Err)
	}
	v, a := clip.Probe.Video, clip.Probe.Audio
	if len(v) != 1 || v[0].Codec != "h264" || v[0].Width != 320 || v[0].Height != 240 || v[0].BitDepth != 8 || v[0].FrameRate != 25 {
		t.Fatalf("video: %+v", v)
	}
	if len(a) != 1 || a[0].Codec != "aac" || a[0].Channels != 2 || a[0].Language != "eng" {
		t.Fatalf("audio: %+v", a)
	}
	if d := clip.Probe.DurationMs; d < 1900 || d > 2200 {
		t.Fatalf("duration %d ms", d)
	}
	if late := got["Movies/Late (2026)/Late (2026).mkv"]; late.Err != ErrNotStable {
		t.Fatalf("a just-written file must wait for the quiet period: %+v", late)
	}
	// SAFE-01: discovery and probing leave every source byte and timestamp alone.
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("source tree changed:\nbefore %v\n after %v", before, after)
	}
}

// snapshot records each file's size, mtime, mode and SHA-256 under dir.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%v %v %d %x", info.Mode(), info.ModTime(), info.Size(), sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
