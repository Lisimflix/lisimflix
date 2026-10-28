package playback

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

func TestRangeReturnsExactSourceBytes(t *testing.T) {
	e := newEnv(t, 1<<20)
	ps := e.mustStart(alice, "k1")
	if ps.Route != "direct" || ps.HeartbeatIntervalSeconds != 10 || ps.IdleTimeoutSeconds != 60 || ps.SourceRevision == "" {
		t.Fatalf("session = %+v", ps)
	}
	resp, b := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=1000-1999"})
	if resp.StatusCode != 206 || !bytes.Equal(b, e.data[1000:2000]) {
		t.Fatalf("range = %d, %d bytes", resp.StatusCode, len(b))
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 1000-1999/"+strconv.Itoa(len(e.data)) {
		t.Fatalf("Content-Range = %q", cr)
	}
	resp, b = e.req(&alice, "GET", ps.ContentURL, "", nil)
	h := resp.Header
	if resp.StatusCode != 200 || !bytes.Equal(b, e.data) {
		t.Fatalf("whole file = %d, %d bytes", resp.StatusCode, len(b))
	}
	etag := h.Get("ETag")
	if h.Get("Accept-Ranges") != "bytes" || h.Get("Content-Length") != strconv.Itoa(len(e.data)) || etag != `"`+ps.SourceRevision+`"` ||
		h.Get("Last-Modified") == "" || h.Get("Content-Type") != "video/mp4" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", h)
	}
	if resp, _ := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"If-None-Match": etag}); resp.StatusCode != 304 {
		t.Fatalf("If-None-Match = %d", resp.StatusCode)
	}
	resp, b = e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=10-19", "If-Range": etag})
	if resp.StatusCode != 206 || !bytes.Equal(b, e.data[10:20]) {
		t.Fatalf("If-Range match = %d", resp.StatusCode)
	}
	if resp, _ := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=10-19", "If-Range": `"stale"`}); resp.StatusCode != 200 {
		t.Fatalf("If-Range mismatch = %d, want the whole file", resp.StatusCode)
	}
}

func TestHeadHasNoBody(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	resp, b := e.req(&alice, "HEAD", ps.ContentURL, "", nil)
	if resp.StatusCode != 200 || len(b) != 0 || resp.Header.Get("Content-Length") != "4096" || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("HEAD = %d, %d bytes, %v", resp.StatusCode, len(b), resp.Header)
	}
	resp, b = e.req(&alice, "HEAD", ps.ContentURL, "", map[string]string{"Range": "bytes=0-99"})
	if resp.StatusCode != 206 || len(b) != 0 || resp.Header.Get("Content-Length") != "100" {
		t.Fatalf("HEAD range = %d, %d bytes", resp.StatusCode, len(b))
	}
}

func TestUnsatisfiableRange(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	resp, b := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=5000-"})
	if resp.StatusCode != 416 || resp.Header.Get("Content-Range") != "bytes */4096" {
		t.Fatalf("416 = %d %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}
	if bytes.Contains(b, []byte(e.dir)) || bytes.Contains(b, []byte("movie.mp4")) {
		t.Fatalf("416 body leaks the path: %s", b)
	}
}

func TestPrivatePathAndSymlinkEscapeDenied(t *testing.T) {
	for _, rel := range []string{"../movie.mp4", "/etc/passwd", "Movies/../../x.mp4", ""} {
		e := newEnv(t, 16)
		if err := Single(contracts.Movie{ID: movieID, SourceRevision: "r"}, rel, nil).Check(e.root); err == nil {
			t.Errorf("Check accepted RelativePath %q", rel)
		}
	}

	secret := []byte("TOP-SECRET-OUTSIDE-THE-ROOT")
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, secret, 0o644); err != nil {
		t.Fatal(err)
	}
	outInfo, _ := os.Stat(outside)
	e := newEnv(t, 16, func(o *Options) {
		single := Single(contracts.Movie{ID: movieID, SourceRevision: Revision(movieID, outInfo)}, "Movies/link.mp4",
			func(p contracts.Principal, id string) bool { return true })
		o.Resolver, o.Movies = single, single.Movies
	})
	if err := os.Symlink(outside, filepath.Join(e.dir, "Movies", "link.mp4")); err != nil {
		t.Fatal(err)
	}
	code, ps, b := e.start(alice, "k1")
	if code == 201 || bytes.Contains(b, secret) {
		t.Fatalf("start through an escaping symlink = %d %s", code, b)
	}
	if ps.ContentURL != "" {
		if _, b := e.req(&alice, "GET", ps.ContentURL, "", nil); bytes.Contains(b, secret) {
			t.Fatal("served bytes from outside the root")
		}
	}
	// A play request carries no path, and unknown fields are rejected.
	resp, b := e.req(&alice, "POST", "/api/v1/playback/sessions", `{"itemId":"mov-1","versionId":"mov-1","path":"/etc/passwd"}`, map[string]string{"Idempotency-Key": "k2"})
	if resp.StatusCode != 400 || errCode(t, b) != contracts.CodeInvalidRequest {
		t.Fatalf("path in request = %d %s", resp.StatusCode, b)
	}
}

func TestSourceReplacementRejected(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	// Same size, new content and mtime: a replaced file.
	repl := filepath.Join(e.dir, "Movies", "new.mp4")
	if err := os.WriteFile(repl, bytes.Repeat([]byte{1}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	os.Chtimes(repl, later, later)
	if err := os.Rename(repl, e.path); err != nil {
		t.Fatal(err)
	}
	resp, b := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=0-9"})
	if resp.StatusCode != 409 || errCode(t, b) != contracts.CodeSourceChanged {
		t.Fatalf("content after replacement = %d %s", resp.StatusCode, b)
	}
	if code, _, b := e.start(alice, "k2"); code != 409 || errCode(t, b) != contracts.CodeSourceChanged {
		t.Fatalf("start after replacement = %d %s", code, b)
	}
	if e.check() == nil {
		t.Fatal("Check() reports a replaced source as ready")
	}
}

func TestSameMetadataDifferentFileRejected(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	info, _ := os.Stat(e.path)
	repl := filepath.Join(e.dir, "Movies", "new.mp4")
	os.WriteFile(repl, bytes.Repeat([]byte{2}, 4096), 0o644)
	os.Chtimes(repl, info.ModTime(), info.ModTime()) // same size and mtime: same revision
	if err := os.Rename(repl, e.path); err != nil {
		t.Fatal(err)
	}
	if resp, b := e.req(&alice, "GET", ps.ContentURL, "", nil); resp.StatusCode != 409 || errCode(t, b) != contracts.CodeSourceChanged {
		t.Fatalf("a different file with identical metadata = %d %s; file identity must be checked", resp.StatusCode, b)
	}
}

func TestMissingSourceIsUnavailable(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	if resp, b := e.req(&alice, "GET", ps.ContentURL, "", nil); resp.StatusCode != 503 || errCode(t, b) != contracts.CodeSourceUnavailable {
		t.Fatalf("content of a missing source = %d %s", resp.StatusCode, b)
	}
	if code, _, b := e.start(alice, "k2"); code != 503 || errCode(t, b) != contracts.CodeSourceUnavailable {
		t.Fatalf("start with a missing source = %d %s", code, b)
	}
	if e.check() == nil {
		t.Fatal("Check() reports a missing source as ready")
	}
}

func TestRevisionIsStable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp4")
	os.WriteFile(p, []byte("abc"), 0o644)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	os.Chtimes(p, ts, ts)
	info, _ := os.Stat(p)
	r := Revision("mov-1", info)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(r) || r != Revision("mov-1", info) {
		t.Fatalf("Revision = %q", r)
	}
	// The agreed layout (#20): sha256 of movieID 0x00 size 0x00 mtimeUnixNano, decimal.
	sum := sha256.Sum256([]byte("mov-1\x003\x00" + strconv.FormatInt(ts.UnixNano(), 10)))
	if want := hex.EncodeToString(sum[:]); r != want {
		t.Fatalf("Revision = %s, want %s", r, want)
	}
	if Revision("mov-2", info) == r {
		t.Fatal("Revision ignores the movie ID")
	}
	os.WriteFile(p, []byte("abcd"), 0o644)
	os.Chtimes(p, ts, ts)
	info2, _ := os.Stat(p)
	if Revision("mov-1", info2) == r {
		t.Fatal("Revision ignores the size")
	}
}
