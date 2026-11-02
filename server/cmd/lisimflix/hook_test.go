package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/library"
)

// LIB-4 wiring: a Radarr source added with the CLI notifies serve, without
// browser headers, and the imported file is scanned with cause "import".
func TestServeImportNotification(t *testing.T) {
	fastLibrary(t)
	libraryOptions.NoWatch = true // only the notification can find the new film
	f := newFixture(t)
	films := filepath.Join(f.dir, "media", "Films")
	os.MkdirAll(filepath.Join(films, "Alien (1979)"), 0o755)
	os.WriteFile(filepath.Join(films, "Alien (1979)", "Alien (1979).mkv"), []byte("x"), 0o644)
	run1 := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if code := run(append(args, "--config", f.cfgPath), nil, &out, &out); code != 0 {
			t.Fatalf("%v: %s", args, out.String())
		}
		return out.String()
	}
	run1("library", "add", "--name", "Films", "--type", "movies", "--folder", "Films")
	out := run1("hook", "add", "--name", "radarr", "--kind", "radarr", "--folder", "Films", "--map", "/movies=Films")
	user := regexp.MustCompile(`Username:\s+(\S+)`).FindStringSubmatch(out)
	pass := regexp.MustCompile(`Password:\s+(\S+)`).FindStringSubmatch(out)
	if user == nil || pass == nil || !strings.Contains(out, "/hooks/v1/arr") {
		t.Fatalf("hook add output:\n%s", out)
	}
	if !strings.Contains(out, "On Movie File Delete For Upgrade") {
		t.Fatalf("hook add output lacks the upgrade-delete trigger:\n%s", out)
	}
	status := func() library.RootStatus {
		st, _ := f.s.scans.Status(context.Background())
		for _, s := range st {
			if s.Path == "Films" {
				return s
			}
		}
		return library.RootStatus{}
	}
	waitFor(t, "library picked up", func() bool { s := status(); return s.LastRun != nil && s.LastRun.Status == library.RunCompleted })

	os.MkdirAll(filepath.Join(films, "Heat (1995)"), 0o755)
	os.WriteFile(filepath.Join(films, "Heat (1995)", "Heat (1995).mkv"), []byte("x"), 0o644)
	notify := func(pw string) int {
		req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/hooks/v1/arr", strings.NewReader(
			`{"eventType":"Download","movie":{"title":"Heat","year":1995,"folderPath":"/movies/Heat (1995)"},`+
				`"movieFile":{"relativePath":"Heat (1995).mkv","path":"/movies/Heat (1995)/Heat (1995).mkv"},"isUpgrade":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Radarr/6.4.4.10685 (alpine 3.22.2)")
		req.SetBasicAuth(user[1], pw)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := notify(pass[1]); code != http.StatusAccepted {
		t.Fatalf("notification: %d", code)
	}
	waitFor(t, "import scan", func() bool {
		s := status()
		return s.LastRun != nil && s.LastRun.Cause == library.CauseImport && s.LastRun.Status == library.RunCompleted && s.Files.Present == 2
	})
	if out := run1("hook", "receipts", "--name", "radarr"); !strings.Contains(out, "queued") || !strings.Contains(out, "Heat (1995)") {
		t.Fatalf("receipts:\n%s", out)
	}
	run1("hook", "revoke", "--name", "radarr")
	if code := notify(pass[1]); code != http.StatusUnauthorized {
		t.Fatalf("after revoke: %d", code)
	}
	if out := run1("hook", "list"); !strings.Contains(out, "revoked") {
		t.Fatalf("list:\n%s", out)
	}
}

func TestPrintable(t *testing.T) {
	if got := printable("a\x1b[31mred\t\nb\u200bc é"); got != "a?[31mred??b?c é" {
		t.Fatalf("%q", got)
	}
}
