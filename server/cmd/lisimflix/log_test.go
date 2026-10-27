package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) events() []map[string]any {
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// #37: one sign-in → play → logout run leaves a server-side story with
// correlation IDs, and none of its secrets.
func TestLogsTellTheStoryWithoutSecrets(t *testing.T) {
	var buf syncBuffer
	f := newFixtureWith(t, 2<<20, slog.New(slog.NewJSONHandler(&buf, nil)))
	b := f.browser()
	_, out := b.do("GET", "/api/v1/auth/session", "", nil)
	b.token = jsonField(out, "csrfToken")
	failed, _ := b.do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"wrong password!"}`, nil)
	b.signIn()
	ps := b.start("k1")
	if resp, _ := b.do("GET", ps.ContentURL, "", map[string]string{"Range": "bytes=0-99"}); resp.StatusCode != 206 {
		t.Fatalf("content = %d", resp.StatusCode)
	}
	b.do("GET", ps.ContentURL[:len(ps.ContentURL)-2]+"AA", "", nil) // tampered grant: 404
	cookie := ""
	for _, c := range b.c.Jar.Cookies(mustParse(f.ts.URL).URL) {
		if c.Name == "lisimflix_session" {
			cookie = c.Value
		}
	}
	b.do("POST", "/api/v1/auth/logout", "", nil)
	afterLogout, _ := b.do("GET", ps.ContentURL, "", nil)

	ev := buf.events()
	has := func(msg string, kv ...any) map[string]any {
		for _, e := range ev {
			if e["msg"] != msg {
				continue
			}
			ok := true
			for i := 0; i+1 < len(kv); i += 2 {
				if e[kv[i].(string)] != kv[i+1] {
					ok = false
				}
			}
			if ok {
				return e
			}
		}
		t.Errorf("no %s %v in:\n%s", msg, kv, buf.String())
		return nil
	}
	if e := has("auth.login", "result", "fail", "reason", "bad_credentials"); e != nil && e["request_id"] != failed.Header.Get("X-Request-Id") {
		t.Errorf("failed login request_id %v, response header %q", e["request_id"], failed.Header.Get("X-Request-Id"))
	}
	has("auth.login", "result", "ok", "username", "viewer")
	has("playback.start", "session_id", ps.ID)
	has("http.request", "status", float64(404), "path", "/api/v1/playback/sessions/"+ps.ID+"/content")
	has("auth.session_end", "reason", "logout")
	has("playback.end", "session_id", ps.ID, "reason", "revoked")
	if e := has("http.request", "status", float64(401), "path", "/api/v1/playback/sessions/"+ps.ID+"/content"); e != nil && e["request_id"] != afterLogout.Header.Get("X-Request-Id") {
		t.Errorf("401 request_id %v, response header %q", e["request_id"], afterLogout.Header.Get("X-Request-Id"))
	}

	grant := ps.ContentURL[strings.Index(ps.ContentURL, "grant=")+len("grant="):]
	for name, secret := range map[string]string{"password": password, "session cookie": cookie, "CSRF token": b.token, "grant": grant, "query": "grant="} {
		if secret != "" && strings.Contains(buf.String(), secret) {
			t.Errorf("the %s appears in the log", name)
		}
	}
}

func jsonField(b []byte, k string) string {
	var m map[string]any
	json.Unmarshal(b, &m)
	s, _ := m[k].(string)
	return s
}

func mustParse(s string) *http.Request {
	r, err := http.NewRequest("GET", s, nil)
	if err != nil {
		panic(err)
	}
	return r
}
