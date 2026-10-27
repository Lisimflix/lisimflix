//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// realMedia returns the owner-supplied movie and probe record. This test is
// only run on request (-tags=integration), so missing files fail it.
func realMedia(t *testing.T) (movie, probe string) {
	t.Helper()
	movie, probe = os.Getenv("LISIMFLIX_M1_MOVIE"), os.Getenv("LISIMFLIX_M1_PROBE")
	if movie == "" || probe == "" {
		t.Fatal("set LISIMFLIX_M1_MOVIE and LISIMFLIX_M1_PROBE to a local MP4 and its ffprobe JSON (testdata/media/README.md)")
	}
	return movie, probe
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestProbeOfTheRealM1Source(t *testing.T) {
	_, probe := realMedia(t)
	b, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseProbe(b); err != nil {
		t.Fatalf("real probe: %v", err)
	}
}

// Real login → CSRF and session start → original byte ranges → heartbeat →
// logout → revoked content, against the real modules and a real movie.
func TestM1RealHTTPRangeAndRevocation(t *testing.T) {
	movie, probe := realMedia(t)
	before := fileSHA256(t, movie)
	cfgPath := filepath.Join(t.TempDir(), "m1.json")
	ts := httptest.NewUnstartedServer(nil)
	origin := "http://" + ts.Listener.Addr().String()
	if err := cmdInit([]string{"--config", cfgPath, "--origin", origin, "--media-root", filepath.Dir(movie),
		"--movie", filepath.Base(movie), "--probe-json", probe, "--username", "viewer"},
		strings.NewReader(password+"\n"+password+"\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	s, err := compose(context.Background(), cfgPath, "", "", testWeb, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.handler
	ts.Start()
	defer func() { ts.Close(); s.shutdown(context.Background(), nil) }()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	token := ""
	do := func(method, path, body string, hdr map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Origin", ts.URL)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}
	var st contracts.AuthState
	_, b := do("GET", "/api/v1/auth/session", "", nil)
	json.Unmarshal(b, &st)
	token = st.CSRFToken
	if resp, _ := do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"wrong password!"}`, nil); resp.StatusCode != 401 {
		t.Fatalf("wrong password = %d", resp.StatusCode)
	}
	resp, b := do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"`+password+`"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, b)
	}
	json.Unmarshal(b, &st)
	token = st.CSRFToken
	_, b = do("GET", "/api/v1/movies", "", nil)
	var list contracts.MovieList
	json.Unmarshal(b, &list)
	if len(list.Items) != 1 {
		t.Fatalf("movies = %s", b)
	}
	resp, b = do("POST", "/api/v1/playback/sessions", `{"movieId":"`+list.Items[0].ID+`"}`, map[string]string{"Idempotency-Key": "itest-1"})
	if resp.StatusCode != 201 {
		t.Fatalf("start = %d %s", resp.StatusCode, b)
	}
	var ps contracts.PlaybackSession
	json.Unmarshal(b, &ps)

	src, err := os.Open(movie)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	info, _ := src.Stat()
	size := info.Size()
	for _, r := range [][2]int64{{0, 1023}, {size / 2, size/2 + 65535}, {size - 545, size - 1}} {
		resp, got := do("GET", ps.ContentURL, "", map[string]string{"Range": "bytes=" + itoa(r[0]) + "-" + itoa(r[1])})
		want := make([]byte, r[1]-r[0]+1)
		src.ReadAt(want, r[0])
		if resp.StatusCode != 206 || !bytes.Equal(got, want) {
			t.Fatalf("range %d-%d = %d, %d bytes; the original bytes must come back", r[0], r[1], resp.StatusCode, len(got))
		}
	}
	if resp, _ := do("POST", "/api/v1/playback/sessions/"+ps.ID+"/heartbeat", `{"sequence":1}`, nil); resp.StatusCode != 204 {
		t.Fatalf("heartbeat = %d", resp.StatusCode)
	}
	if resp, _ := do("POST", "/api/v1/auth/logout", "", nil); resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	if resp, _ := do("GET", ps.ContentURL, "", map[string]string{"Range": "bytes=0-0"}); resp.StatusCode != 401 {
		t.Fatalf("content after logout = %d", resp.StatusCode)
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("process: %d MiB obtained from the OS, %d MiB heap in use (ranges stream; the movie is never loaded whole)", ms.Sys>>20, ms.HeapInuse>>20)
	if after := fileSHA256(t, movie); after != before {
		t.Fatalf("source changed: %s → %s", before, after)
	}
	t.Logf("source sha256 unchanged: %s", before)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
