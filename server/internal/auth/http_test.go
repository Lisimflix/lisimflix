package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
)

const testPassword = "correct horse battery"

// viewerHash is computed once with the real argon2, before any test can stub it.
var viewerHash string

func TestMain(m *testing.M) {
	h, err := HashPassword([]byte(testPassword))
	if err != nil {
		panic(err)
	}
	viewerHash = h
	os.Exit(m.Run())
}

func testViewer(t *testing.T) m1config.Viewer {
	t.Helper()
	return m1config.Viewer{AccountID: "acct-1", ProfileID: "prof-1", Username: "viewer", PasswordHash: viewerHash}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type harness struct {
	t       *testing.T
	m       *Manager
	srv     *httptest.Server
	clock   *clock
	mu      sync.Mutex
	revoked []string
}

// newHarness serves the auth routes plus a protected /api/v1/movies that
// echoes the principal, behind the same CSRF middleware the app uses.
func newHarness(t *testing.T, tls bool) *harness { return newHarnessWith(t, tls, nil, nil) }

// newHarnessWith also passes revocations to onRevoke and lets routes mount
// more handlers before the CSRF wrapper.
func newHarnessWith(t *testing.T, tls bool, onRevoke func(string), routes func(*http.ServeMux, *Manager)) *harness {
	t.Helper()
	h := &harness{t: t, clock: &clock{t: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}}
	m, err := NewManager(testViewer(t), tls, func(id string) {
		h.mu.Lock()
		h.revoked = append(h.revoked, id)
		h.mu.Unlock()
		if onRevoke != nil {
			onRevoke(id)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	m.now = h.clock.Now
	h.m = m
	mux := http.NewServeMux()
	m.Register(mux)
	mux.Handle("GET /api/v1/movies", m.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := contracts.PrincipalFrom(r.Context())
		if !ok {
			t.Error("Require passed a request without a principal")
		}
		json.NewEncoder(w).Encode(map[string]string{"account": p.AccountID, "profile": p.ProfileID, "session": p.AuthSessionID})
	})))
	if routes != nil {
		routes(mux, m)
	}
	handler := CSRF(bytes.Repeat([]byte{9}, 32), tls)(mux)
	if tls {
		h.srv = httptest.NewTLSServer(handler)
	} else {
		h.srv = httptest.NewServer(handler)
	}
	t.Cleanup(h.srv.Close)
	return h
}

type client struct {
	h     *harness
	c     *http.Client
	token string
}

func (h *harness) client() *client {
	jar, _ := cookiejar.New(nil)
	c := h.srv.Client()
	c.Jar = jar
	return &client{h: h, c: c}
}

func (c *client) do(method, path, body string, hdr map[string]string) (*http.Response, []byte) {
	c.h.t.Helper()
	req, _ := http.NewRequest(method, c.h.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Origin", c.h.srv.URL)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("X-CSRF-Token", c.token)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func (c *client) session() contracts.AuthState {
	c.h.t.Helper()
	resp, b := c.do("GET", "/api/v1/auth/session", "", nil)
	if resp.StatusCode != 200 {
		c.h.t.Fatalf("session = %d %s", resp.StatusCode, b)
	}
	var s contracts.AuthState
	if err := json.Unmarshal(b, &s); err != nil {
		c.h.t.Fatal(err)
	}
	c.token = s.CSRFToken
	return s
}

func (c *client) login(user, pass string) (*http.Response, []byte) {
	c.h.t.Helper()
	body, _ := json.Marshal(contracts.LoginRequest{Username: user, Password: pass})
	resp, b := c.do("POST", "/api/v1/auth/login", string(body), nil)
	if resp.StatusCode == 200 {
		var s contracts.AuthState
		json.Unmarshal(b, &s)
		c.token = s.CSRFToken
	}
	return resp, b
}

func (c *client) principal() (int, map[string]string) {
	c.h.t.Helper()
	resp, b := c.do("GET", "/api/v1/movies", "", nil)
	var p map[string]string
	json.Unmarshal(b, &p)
	return resp.StatusCode, p
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var e contracts.APIError
	if err := json.Unmarshal(b, &e); err != nil || e.RequestID == "" {
		t.Fatalf("not an APIError: %s", b)
	}
	return e.Code
}

func (h *harness) revocations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.revoked...)
}

func TestAnonymousSessionHasTokenAndNullViewer(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	resp, b := c.do("GET", "/api/v1/auth/session", "", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("session = %d, Cache-Control %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if !bytes.Contains(b, []byte(`"viewer":null`)) || !bytes.Contains(b, []byte(`"authenticated":false`)) {
		t.Fatalf("anonymous body = %s", b)
	}
	if s := c.session(); s.CSRFToken == "" {
		t.Fatal("no CSRF token for an anonymous caller")
	}
}

func TestLoginRequiresValidPasswordAndCSRF(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	if resp, b := c.login("viewer", testPassword); resp.StatusCode != 403 || errCode(t, b) != contracts.CodeCSRFFailed {
		t.Fatalf("login without CSRF token = %d %s", resp.StatusCode, b)
	}
	c.session()
	good := c.token
	c.token = "AAAA" + good[4:]
	if resp, b := c.login("viewer", testPassword); resp.StatusCode != 403 || errCode(t, b) != contracts.CodeCSRFFailed {
		t.Fatalf("login with a bad CSRF token = %d %s", resp.StatusCode, b)
	}
	c.token = good
	resp, wrongPass := c.login("viewer", "wrong password here")
	if resp.StatusCode != 401 || errCode(t, wrongPass) != contracts.CodeUnauthenticated {
		t.Fatalf("wrong password = %d %s", resp.StatusCode, wrongPass)
	}
	_, wrongUser := c.login("nobody", testPassword)
	var a, b contracts.APIError
	json.Unmarshal(wrongPass, &a)
	json.Unmarshal(wrongUser, &b)
	if a.Code != b.Code || a.Message != b.Message {
		t.Fatalf("unknown user and wrong password differ: %+v vs %+v", a, b)
	}
	resp, body := c.login("viewer", testPassword)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var s contracts.AuthState
	json.Unmarshal(body, &s)
	if !s.Authenticated || s.Viewer == nil || *s.Viewer != (contracts.Viewer{AccountID: "acct-1", ProfileID: "prof-1", Username: "viewer"}) || s.CSRFToken == "" {
		t.Fatalf("login state = %+v", s)
	}
	if bytes.Contains(body, []byte("argon2")) {
		t.Fatal("login response leaks the password hash")
	}
	code, p := c.principal()
	if code != 200 || p["account"] != "acct-1" || p["profile"] != "prof-1" || p["session"] == "" {
		t.Fatalf("principal = %d %v", code, p)
	}
}

func TestLoginRejectsMalformedBodies(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	c.session()
	for name, tc := range map[string]struct{ body, ctype string }{
		"unknown field":  {`{"username":"viewer","password":"x","admin":true}`, "application/json"},
		"not json":       {`username=viewer`, "application/x-www-form-urlencoded"},
		"two objects":    {`{"username":"viewer","password":"x"}{}`, "application/json"},
		"oversized":      {`{"username":"viewer","password":"` + strings.Repeat("x", 17<<10) + `"}`, "application/json"},
		"long password":  {`{"username":"viewer","password":"` + strings.Repeat("x", 1025) + `"}`, "application/json"},
		"empty username": {`{"username":"","password":"x"}`, "application/json"},
	} {
		resp, b := c.do("POST", "/api/v1/auth/login", tc.body, map[string]string{"Content-Type": tc.ctype})
		if resp.StatusCode != 400 || errCode(t, b) != contracts.CodeInvalidRequest {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
}

func TestLoginRotatesSession(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	c.session()
	c.login("viewer", testPassword)
	_, first := c.principal()
	old := sessionCookie(t, c)
	c.login("viewer", testPassword)
	_, second := c.principal()
	if first["session"] == second["session"] || sessionCookie(t, c) == old {
		t.Fatal("second login kept the same session")
	}
	if r := h.revocations(); len(r) != 1 || r[0] != first["session"] {
		t.Fatalf("revoked = %v, want the first session", r)
	}
	stale := h.client()
	stale.c.Jar.SetCookies(mustURL(h.srv.URL), []*http.Cookie{{Name: CookieName, Value: old}})
	if code, _ := stale.principal(); code != 401 {
		t.Fatalf("old session cookie still works: %d", code)
	}
}

func TestHTTPAndHTTPSCookieAttributes(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", tls), func(t *testing.T) {
			h := newHarness(t, tls)
			c := h.client()
			c.session()
			// A spoofed proxy header must not change the mode.
			body, _ := json.Marshal(contracts.LoginRequest{Username: "viewer", Password: testPassword})
			resp, b := c.do("POST", "/api/v1/auth/login", string(body), map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "evil.example"})
			if resp.StatusCode != 200 {
				t.Fatalf("login = %d %s", resp.StatusCode, b)
			}
			var ck *http.Cookie
			for _, k := range resp.Cookies() {
				if k.Name == CookieName {
					ck = k
				}
			}
			if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Domain != "" || ck.Secure != tls {
				t.Fatalf("session cookie = %+v", ck)
			}
		})
	}
}

func TestUnauthenticatedAndExpiredSession(t *testing.T) {
	h := newHarness(t, false)
	anon := h.client()
	if resp, b := anon.do("GET", "/api/v1/movies", "", nil); resp.StatusCode != 401 || errCode(t, b) != contracts.CodeUnauthenticated {
		t.Fatalf("anonymous = %d %s", resp.StatusCode, b)
	}
	anon.c.Jar.SetCookies(mustURL(h.srv.URL), []*http.Cookie{{Name: CookieName, Value: strings.Repeat("A", 43)}})
	if code, _ := anon.principal(); code != 401 {
		t.Fatalf("forged cookie = %d", code)
	}

	idle := h.client()
	idle.session()
	idle.login("viewer", testPassword)
	_, p := idle.principal()
	h.clock.Advance(7*24*time.Hour + time.Second)
	if code, _ := idle.principal(); code != 401 {
		t.Fatalf("after 7 days idle = %d", code)
	}
	if r := h.revocations(); len(r) != 1 || r[0] != p["session"] {
		t.Fatalf("idle expiry revoked %v", r)
	}

	active := h.client()
	active.session()
	active.login("viewer", testPassword)
	for i := 0; i < 4; i++ { // 24 days, never idle for 7
		h.clock.Advance(6 * 24 * time.Hour)
		if code, _ := active.principal(); code != 200 {
			t.Fatalf("active session rejected on day %d", (i+1)*6)
		}
	}
	h.clock.Advance(6*24*time.Hour + time.Second) // past 30 days absolute
	if code, _ := active.principal(); code != 401 {
		t.Fatalf("after 30 days absolute = %d", code)
	}
}

func TestLogoutInvokesRevocation(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	c.session()
	c.login("viewer", testPassword)
	_, p := c.principal()
	resp, b := c.do("POST", "/api/v1/auth/logout", "", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("logout = %d %s", resp.StatusCode, b)
	}
	if r := h.revocations(); len(r) != 1 || r[0] != p["session"] {
		t.Fatalf("revoked %v, want %s", r, p["session"])
	}
	if code, _ := c.principal(); code != 401 {
		t.Fatalf("after logout = %d", code)
	}
	if resp, _ := c.do("POST", "/api/v1/auth/logout", "", nil); resp.StatusCode != 401 {
		t.Fatalf("second logout = %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/api/v1/auth/logout", "", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET logout = %d; state-changing GETs must not exist", resp.StatusCode)
	}
}

func TestLoginRateLimitIsBounded(t *testing.T) {
	var calls int
	defer stubArgon(t, &calls)()
	h := newHarness(t, false)
	c := h.client()
	c.session()
	for i := 0; i < 5; i++ {
		if resp, _ := c.login("viewer", "wrong password here"); resp.StatusCode != 401 {
			t.Fatalf("attempt %d = %d", i+1, resp.StatusCode)
		}
	}
	before := calls
	resp, b := c.login("viewer", testPassword)
	if resp.StatusCode != 429 || errCode(t, b) != contracts.CodeRateLimited || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("6th attempt = %d %s Retry-After=%q", resp.StatusCode, b, resp.Header.Get("Retry-After"))
	}
	if calls != before {
		t.Fatal("a rate-limited attempt still ran argon2")
	}
	// Unknown usernames count too, and the key is case-insensitive.
	for i := 0; i < 5; i++ {
		c.login("Nobody", "wrong password here")
	}
	if resp, _ := c.login("nobody", "x"); resp.StatusCode != 429 {
		t.Fatalf("unknown user not limited: %d", resp.StatusCode)
	}
	h.clock.Advance(5*time.Minute + time.Second)
	if resp, _ := c.login("viewer", "wrong password here"); resp.StatusCode != 401 {
		t.Fatalf("after the window = %d", resp.StatusCode)
	}
	// The limiter never holds more than 256 keys.
	for i := 0; i < 400; i++ {
		c.login(fmt.Sprintf("user%d", i), "wrong password here")
	}
	if n := h.m.limiterSize(); n > maxLimiterKeys {
		t.Fatalf("limiter holds %d keys", n)
	}
}

func TestUnknownUsernameDoesSameWork(t *testing.T) {
	var calls int
	defer stubArgon(t, &calls)()
	h := newHarness(t, false)
	c := h.client()
	c.session()
	c.login("viewer", "wrong password here")
	if calls != 1 {
		t.Fatalf("wrong password ran argon2 %d times", calls)
	}
	c.login("nobody", "wrong password here")
	if calls != 2 {
		t.Fatalf("unknown username ran argon2 %d times, want the same work as a wrong password", calls-1)
	}
}

func TestSessionStoreIsBounded(t *testing.T) {
	h := newHarness(t, false)
	var first string
	for i := 0; i < maxSessions+6; i++ {
		c := h.client()
		c.session()
		if resp, b := c.login("viewer", testPassword); resp.StatusCode != 200 {
			t.Fatalf("login %d = %d %s", i, resp.StatusCode, b)
		}
		if i == 0 {
			_, p := c.principal()
			first = p["session"]
		}
		h.clock.Advance(time.Second)
	}
	if n := h.m.sessionCount(); n != maxSessions {
		t.Fatalf("store holds %d sessions, want %d", n, maxSessions)
	}
	if r := h.revocations(); len(r) != 6 || r[0] != first {
		t.Fatalf("evictions revoked %v; the oldest must go first and be revoked", r)
	}
}

func TestNewManagerRejectsBadHash(t *testing.T) {
	v := testViewer(t)
	v.PasswordHash = "$argon2id$v=19$m=999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	if _, err := NewManager(v, false, func(string) {}); err == nil {
		t.Fatal("NewManager accepted an out-of-bounds hash")
	}
	if _, err := NewManager(testViewer(t), false, nil); err == nil {
		t.Fatal("NewManager accepted a nil revocation hook")
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func sessionCookie(t *testing.T, c *client) string {
	t.Helper()
	for _, k := range c.c.Jar.Cookies(mustURL(c.h.srv.URL)) {
		if k.Name == CookieName {
			return k.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// #18 (Pete, point 1): a table full of fake usernames must not lock out the
// configured viewer, and spraying fakes must not reset the viewer's counter.
func TestFullLimiterNeverLocksOutTheViewer(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	c.session()
	var calls int
	restore := stubArgon(t, &calls)
	for i := 0; i < 2*maxLimiterKeys; i++ {
		c.login(fmt.Sprintf("fake%d", i), "wrong password here")
	}
	restore()
	if n := h.m.limiterSize(); n > maxLimiterKeys {
		t.Fatalf("limiter holds %d keys", n)
	}
	if resp, b := c.login("viewer", testPassword); resp.StatusCode != 200 {
		t.Fatalf("viewer login with a full table = %d %s", resp.StatusCode, b)
	}
	for i := 0; i < loginLimit; i++ {
		c.login("viewer", "wrong password here")
	}
	if resp, _ := c.login("viewer", testPassword); resp.StatusCode != 429 {
		t.Fatalf("viewer not limited after %d failures: %d", loginLimit, resp.StatusCode)
	}
	restore = stubArgon(t, &calls)
	for i := 0; i < 2*maxLimiterKeys; i++ {
		c.login(fmt.Sprintf("spray%d", i), "wrong password here")
	}
	restore()
	if resp, _ := c.login("viewer", testPassword); resp.StatusCode != 429 {
		t.Fatalf("spraying fake usernames reset the viewer's counter: %d", resp.StatusCode)
	}
}

// #18 (Pete's question): when every argon2 slot is busy, a login waits at
// most verifyWait and then gets 429 with Retry-After.
func TestBusyVerifierGetsRateLimited(t *testing.T) {
	h := newHarness(t, false)
	old := verifyWait
	verifyWait = 50 * time.Millisecond
	defer func() { verifyWait = old }()
	c := h.client()
	c.session()
	h.m.anySlot <- struct{}{}
	h.m.viewerSlot <- struct{}{}
	start := time.Now()
	resp, b := c.login("viewer", testPassword)
	if resp.StatusCode != 429 || errCode(t, b) != contracts.CodeRateLimited || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("busy verifier = %d %s", resp.StatusCode, b)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("login waited far past verifyWait")
	}
	<-h.m.anySlot
	<-h.m.viewerSlot
	if resp, _ := c.login("viewer", testPassword); resp.StatusCode != 200 {
		t.Fatalf("login after the slots freed = %d", resp.StatusCode)
	}
}

// loginFrom calls the login route directly with a chosen peer address.
// CSRF is the app's job and is tested elsewhere; this isolates the slots.
func loginFrom(m *Manager, peerIP, user, pass string) int {
	mux := http.NewServeMux()
	m.Register(mux)
	body, _ := json.Marshal(contracts.LoginRequest{Username: user, Password: pass})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = net.JoinHostPort(peerIP, "40000")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// slowArgon makes every argon2 run take at least d, so a spray keeps slots busy.
func slowArgon(t *testing.T, d time.Duration) func() {
	t.Helper()
	old := argonKey
	argonKey = func(pw, salt []byte, time_, memory uint32, threads uint8, keyLen uint32) []byte {
		time.Sleep(d)
		return old(pw, salt, time_, memory, threads, keyLen)
	}
	return func() { argonKey = old }
}

// #24 (Pete): fresh fake usernames from many peers must not starve the
// viewer's login of argon2 slots.
func TestUsernameSprayCannotStarveViewer(t *testing.T) {
	h := newHarness(t, false)
	defer slowArgon(t, 100*time.Millisecond)()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for p := 0; p < 10; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				loginFrom(h.m, fmt.Sprintf("10.0.0.%d", p+10), fmt.Sprintf("fake-%d-%d", p, i), "wrong password here")
			}
		}(p)
	}
	time.Sleep(300 * time.Millisecond) // the spray is now holding the shared slot
	start := time.Now()
	code := loginFrom(h.m, "10.0.0.200", "viewer", testPassword)
	took := time.Since(start)
	close(stop)
	wg.Wait()
	// Under the old shared slots the viewer got 429 after verifyWait (5 s);
	// the limit stays below that with room for slow CI machines.
	if code != 200 || took > 4*time.Second {
		t.Fatalf("viewer login during a spray = %d after %v", code, took)
	}
}

// #24 (Pete): one argon2 run per peer at a time; extra requests from that
// peer get 429 at once instead of queuing.
func TestOnePasswordCheckPerPeer(t *testing.T) {
	h := newHarness(t, false)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	old := argonKey
	argonKey = func(pw, salt []byte, time_, memory uint32, threads uint8, keyLen uint32) []byte {
		started <- struct{}{}
		<-release
		return make([]byte, keyLen)
	}
	defer func() { argonKey = old }()
	first := make(chan int, 1)
	go func() { first <- loginFrom(h.m, "10.0.0.5", "fake-a", "wrong password here") }()
	<-started
	start := time.Now()
	if code := loginFrom(h.m, "10.0.0.5", "fake-b", "wrong password here"); code != 429 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("second check from the same peer = %d after %v, want an immediate 429", code, time.Since(start))
	}
	close(release)
	if code := <-first; code != 401 {
		t.Fatalf("first check = %d", code)
	}
}

// With the shared slot held, the viewer still verifies through the reserved
// slot, and any other username gets 429 after verifyWait.
func TestViewerSlotIsReserved(t *testing.T) {
	h := newHarness(t, false)
	old := verifyWait
	verifyWait = 100 * time.Millisecond
	defer func() { verifyWait = old }()
	h.m.anySlot <- struct{}{}
	defer func() { <-h.m.anySlot }()
	if code := loginFrom(h.m, "10.0.0.7", "someone", "wrong password here"); code != 429 {
		t.Fatalf("other username with the shared slot busy = %d, want 429", code)
	}
	if code := loginFrom(h.m, "10.0.0.8", "viewer", testPassword); code != 200 {
		t.Fatalf("viewer with the shared slot busy = %d, want 200 through the reserved slot", code)
	}
}

// #24 (Pete, note 1): one machine can rotate through many IPv6 addresses, so
// IPv6 peers are keyed by their /64 for both the limiter and the per-peer slot.
func TestIPv6PeersShareTheirPrefix(t *testing.T) {
	h := newHarness(t, false)
	var calls int
	defer stubArgon(t, &calls)()
	for i := 1; i <= loginLimit; i++ {
		if code := loginFrom(h.m, fmt.Sprintf("2001:db8::%x", i), "viewer", "wrong password here"); code != 401 {
			t.Fatalf("attempt %d = %d", i, code)
		}
	}
	if code := loginFrom(h.m, "2001:db8::beef", "viewer", "wrong password here"); code != 429 {
		t.Fatalf("a new address in the same /64 = %d, want 429", code)
	}
	if code := loginFrom(h.m, "2001:db8:0:1::1", "viewer", "wrong password here"); code != 401 {
		t.Fatalf("a different /64 = %d, want its own limit (401)", code)
	}
	if code := loginFrom(h.m, "192.0.2.1", "viewer", "wrong password here"); code != 401 {
		t.Fatalf("IPv4 peer = %d", code)
	}
	if got := peerKey("[::ffff:192.0.2.1]:1"); got != "192.0.2.1" {
		t.Fatalf("IPv4-mapped address keyed as %q", got)
	}
}

func TestIPv6PeerSlotIsPerPrefix(t *testing.T) {
	h := newHarness(t, false)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	old := argonKey
	argonKey = func(pw, salt []byte, time_, memory uint32, threads uint8, keyLen uint32) []byte {
		started <- struct{}{}
		<-release
		return make([]byte, keyLen)
	}
	defer func() { argonKey = old }()
	first := make(chan int, 1)
	go func() { first <- loginFrom(h.m, "2001:db8::1", "fake-a", "wrong password here") }()
	<-started
	start := time.Now()
	if code := loginFrom(h.m, "2001:db8::2", "fake-b", "wrong password here"); code != 429 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("second check from the same /64 = %d after %v, want an immediate 429", code, time.Since(start))
	}
	close(release)
	<-first
}
