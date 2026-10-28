package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// fakeProber stands in for ffprobe, which unit tests don't have.
type fakeProber struct{}

func (fakeProber) Probe(context.Context, *os.Root, string) (probe.Result, error) {
	return probe.Result{Container: "mov,mp4,m4a,3gp,3g2,mj2", DurationMs: 12500}, nil
}

func fastLibrary(t *testing.T) {
	t.Helper()
	oldP, oldO := newProber, libraryOptions
	newProber = func() library.Prober { return fakeProber{} }
	libraryOptions = library.SchedulerOptions{Debounce: 20 * time.Millisecond, Recheck: 50 * time.Millisecond}
	t.Cleanup(func() { newProber, libraryOptions = oldP, oldO })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// LIB-2b wiring: a library added with the CLI while serve runs is picked up,
// scanned and probed; serve's shutdown stops the scheduler.
func TestServeScansLibraries(t *testing.T) {
	fastLibrary(t)
	f := newFixture(t)
	media := filepath.Join(f.dir, "media")
	os.MkdirAll(filepath.Join(media, "Films", "Heat (1995)"), 0o755)
	os.WriteFile(filepath.Join(media, "Films", "Heat (1995)", "Heat (1995).mkv"), []byte("x"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(media, "Films", "Heat (1995)", "Heat (1995).mkv"), old, old)

	var out bytes.Buffer
	if err := run([]string{"library", "add", "--config", f.cfgPath, "--name", "Films", "--type", "movies", "--folder", "Films"},
		nil, &out, &out); err != 0 {
		t.Fatalf("library add: %s", out.String())
	}
	waitFor(t, "scan and probe", func() bool {
		st, err := f.s.scans.Status(context.Background())
		return err == nil && len(st) == 1 && st[0].LastRun != nil && st[0].LastRun.Status == library.RunCompleted &&
			st[0].Files.Present == 1 && st[0].Files.ProbesDue == 0
	})

	out.Reset()
	if code := run([]string{"library", "list", "--config", f.cfgPath}, nil, &out, &out); code != 0 ||
		!strings.Contains(out.String(), "Films") || !strings.Contains(out.String(), "available") {
		t.Fatalf("library list (%d):\n%s", code, out.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.ts.Close()
	if err := f.s.shutdown(ctx, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.s.libraryDone:
	default:
		t.Fatal("scheduler still running after shutdown")
	}
}

func TestLibraryAddChecksInput(t *testing.T) {
	f := newFixture(t)
	for name, args := range map[string][]string{
		"missing folder": {"--name", "X", "--type", "movies", "--folder", "Nope"},
		"escape":         {"--name", "X", "--type", "movies", "--folder", "../config"},
		"bad type":       {"--name", "X", "--type", "music", "--folder", "Movies"},
		"no folder":      {"--name", "X", "--type", "movies"},
	} {
		var out bytes.Buffer
		if code := run(append([]string{"library", "add", "--config", f.cfgPath}, args...), nil, &out, &out); code == 0 {
			t.Errorf("%s: accepted\n%s", name, out.String())
		}
	}
}
