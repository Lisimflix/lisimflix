package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
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

// Review (LIB-2b): a library folder may not be a symlink. It would be
// watched as nothing, and could add the same files twice under two paths.
func TestLibraryAddRefusesSymlinkedFolder(t *testing.T) {
	f := newFixture(t)
	media := filepath.Join(f.dir, "media")
	if err := os.Symlink("Movies", filepath.Join(media, "Films")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run([]string{"library", "add", "--config", f.cfgPath, "--name", "Films", "--type", "movies", "--folder", "Films"}, nil, &out, &out); code == 0 || !strings.Contains(out.String(), "symlink") {
		t.Fatalf("symlinked folder accepted (%d): %s", code, out.String())
	}
}

// LIB-2c: on a database upgraded from LIB-2b, items have no sort keys until
// the backfill. Serve fills them before it serves the catalogue, so the first
// listItems already pages in title order with working cursors.
func TestServeFillsSortKeysBeforeServing(t *testing.T) {
	fastLibrary(t)
	f := newFixture(t)
	media := filepath.Join(f.dir, "media")
	for _, title := range []string{"Cobra (1986)", "Alien (1979)", "Brazil (1985)"} {
		os.MkdirAll(filepath.Join(media, "Films", title), 0o755)
		os.WriteFile(filepath.Join(media, "Films", title, title+".mkv"), []byte("x"), 0o644)
	}
	var out bytes.Buffer
	if code := run([]string{"library", "add", "--config", f.cfgPath, "--name", "Films", "--type", "movies", "--folder", "Films"},
		nil, &out, &out); code != 0 {
		t.Fatalf("library add: %s", out.String())
	}
	waitFor(t, "the scan", func() bool {
		st, err := f.s.scans.Status(context.Background())
		return err == nil && len(st) == 1 && st[0].LastRun != nil && st[0].LastRun.Status == library.RunCompleted && st[0].Files.Present == 3
	})

	// Stop serve and clear the keys, as a LIB-2b database has them.
	addr := f.ts.Listener.Addr().String()
	f.ts.Close()
	if err := f.s.shutdown(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(context.Background(), filepath.Join(filepath.Dir(f.cfgPath), "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE library_items SET sort_key = NULL, sort_group = NULL`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown(context.Background(), nil)
	var unkeyed int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE sort_key IS NULL OR sort_group IS NULL`).Scan(&unkeyed); err != nil || unkeyed != 0 {
		t.Fatalf("%d items without sort keys once compose returned (%v)", unkeyed, err)
	}

	// Serve again on the configured origin and page one item at a time.
	ts := httptest.NewUnstartedServer(s.handler)
	ts.Listener.Close()
	if ts.Listener, err = net.Listen("tcp", addr); err != nil {
		t.Fatal(err)
	}
	ts.Start()
	f.ts = ts // the fixture's cleanup closes it
	b := f.browser()
	b.signIn()
	get := func(path string, v any) {
		t.Helper()
		resp, body := b.do("GET", path, "", nil)
		if resp.StatusCode != 200 || json.Unmarshal(body, v) != nil {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, body)
		}
	}
	var libs contracts.LibraryList
	get("/api/v1/libraries", &libs)
	var titles []string
	cursor := ""
	for range 5 {
		var page contracts.ItemPage
		q := url.Values{"libraryId": {libs.Items[0].ID}, "limit": {"1"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		get("/api/v1/items?"+q.Encode(), &page)
		for _, it := range page.Items {
			titles = append(titles, it.Title)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !reflect.DeepEqual(titles, []string{"Alien", "Brazil", "Cobra"}) {
		t.Fatalf("titles %v", titles)
	}
}
