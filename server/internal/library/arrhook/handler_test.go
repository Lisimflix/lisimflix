package arrhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

const sonarrUA = "Sonarr/4.0.20.3014 (ubuntu 22.04)"

// hints records Hint calls; err makes them fail.
type hints struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (h *hints) Hint(rootID, scope, cause string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return h.err
	}
	h.calls = append(h.calls, rootID+" "+scope+" "+cause)
	return nil
}

func (h *hints) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

type hookFixture struct {
	st            *Store
	h             *Handler
	hints         *hints
	src           Source
	token, tvRoot string
	clock         time.Time
}

func newHook(t *testing.T) *hookFixture {
	t.Helper()
	st, tvRoot, _ := newStore(t)
	src, token, err := st.Add(context.Background(), "sonarr", Sonarr, []string{"TV"}, []Mapping{{"/tv", "TV"}})
	if err != nil {
		t.Fatal(err)
	}
	f := &hookFixture{st: st, hints: &hints{}, src: src, token: token, tvRoot: tvRoot, clock: time.Now()}
	f.h = NewHandler(st, f.hints, nil)
	f.h.now = func() time.Time { return f.clock }
	return f
}

// send posts body as the source would, with the given user agent and
// credentials, from peer 192.0.2.1.
func (f *hookFixture) send(method, ua, user, pass string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, Path, bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.1:5000"
	req.Header.Set("Content-Type", "application/json")
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f *hookFixture) ok(body []byte) *httptest.ResponseRecorder {
	return f.send(http.MethodPost, sonarrUA, f.src.ID, f.token, body)
}

func (f *hookFixture) lastReceipt(t *testing.T) Receipt {
	t.Helper()
	rs, err := f.st.Receipts(context.Background(), f.src.ID, 1)
	if err != nil || len(rs) != 1 {
		t.Fatalf("receipts %+v %v", rs, err)
	}
	return rs[0]
}

type answer struct {
	Result    string  `json:"result"`
	ReceiptID string  `json:"receiptId"`
	Scopes    []Scope `json:"scopes"`
	Code      string  `json:"code"` // error responses (contracts.APIError)
	Message   string  `json:"message"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) answer {
	t.Helper()
	var a answer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return a
}

// SCAN-02: an import queues a scoped scan of the file and of the file it
// replaced; the answer is a receipt, not a promise that it's scanned.
func TestImportQueuesScopedScans(t *testing.T) {
	f := newHook(t)
	rec := f.ok(fixture(t, "sonarr_download.json"))
	a := decode(t, rec)
	if rec.Code != http.StatusAccepted || a.Result != ResultQueued || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %+v", rec.Code, a)
	}
	want := []string{f.tvRoot + " Show (2019)/Season 01 import"} // new and replaced file share a folder
	if got := f.hints.list(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("hints %q, want %q", got, want)
	}
	r := f.lastReceipt(t)
	if r.ID != a.ReceiptID || r.Event != "Download" || r.App != "Sonarr/4.0.20.3014" || r.Result != ResultQueued ||
		len(r.Scopes) != 1 || r.Scopes[0].Path != "Show (2019)/Season 01" {
		t.Fatalf("receipt %+v", r)
	}
}

// A season pack is one scan of the season folder, and a repeated
// notification is coalesced by the scheduler's queue (one pending scope).
func TestSeasonPackIsOneScan(t *testing.T) {
	f := newHook(t)
	f.ok(fixture(t, "sonarr_import_complete.json"))
	if got := f.hints.list(); len(got) != 1 || got[0] != f.tvRoot+" Show (2019)/Season 02 import" {
		t.Fatalf("hints %q", got)
	}
}

// The connection test checks credentials and reachability and queues nothing.
func TestConnectionTestQueuesNothing(t *testing.T) {
	f := newHook(t)
	rec := f.ok(fixture(t, "sonarr_test.json"))
	if a := decode(t, rec); rec.Code != http.StatusOK || a.Result != ResultTest || len(f.hints.list()) != 0 {
		t.Fatalf("%d %+v %q", rec.Code, a, f.hints.list())
	}
	if r := f.lastReceipt(t); r.Result != ResultTest {
		t.Fatalf("receipt %+v", r)
	}
	rec = f.ok(fixture(t, "sonarr_grab.json"))
	if a := decode(t, rec); rec.Code != http.StatusOK || a.Result != ResultIgnored || len(f.hints.list()) != 0 {
		t.Fatalf("grab: %d %+v", rec.Code, a)
	}
}

// Paths that can't be placed inside the source's folders reject the whole
// notification: nothing is queued, and Sonarr shows the failure.
func TestPathsOutsideTheSourceAreRejected(t *testing.T) {
	f := newHook(t)
	for name, body := range map[string]string{
		"unmapped":       `{"eventType":"Download","series":{"path":"/anime/x"},"episodeFile":{"path":"/anime/x/a.mkv"}}`,
		"escape":         `{"eventType":"Download","series":{"path":"/tv/x"},"episodeFile":{"path":"/tv/../../etc/passwd"}}`,
		"one bad of two": `{"eventType":"Download","series":{"path":"/tv/x"},"episodeFile":{"path":"/tv/x/a.mkv"},"deletedFiles":[{"path":"/elsewhere/a.mkv"}]}`,
		"relative":       `{"eventType":"EpisodeFileDelete","series":{"path":"/tv/x"},"episodeFile":{"path":"x/a.mkv"}}`,
	} {
		rec := f.ok([]byte(body))
		if a := decode(t, rec); rec.Code != http.StatusBadRequest || a.Code != "invalid_request" {
			t.Errorf("%s: %d %+v", name, rec.Code, a)
		}
		if r := f.lastReceipt(t); r.Result != ResultRejected || r.Reason == "" {
			t.Errorf("%s: receipt %+v", name, r)
		}
	}
	if got := f.hints.list(); len(got) != 0 {
		t.Fatalf("queued %q", got)
	}
	// The source's whole folder is a valid scope (a series deleted at the root is unusual but harmless).
	if rec := f.ok([]byte(`{"eventType":"SeriesDelete","series":{"path":"/tv"}}`)); rec.Code != http.StatusAccepted {
		t.Fatalf("root scope: %d", rec.Code)
	}
}

func TestAuthenticationAndLimits(t *testing.T) {
	f := newHook(t)
	body := fixture(t, "sonarr_download.json")
	rec := f.send(http.MethodPost, sonarrUA, "", "", body)
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("no credentials: %d %v", rec.Code, rec.Header())
	}
	if rec := f.send(http.MethodPost, sonarrUA, f.src.ID, "lfh_wrong", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := f.send(http.MethodGet, sonarrUA, f.src.ID, f.token, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := f.send(http.MethodPut, sonarrUA, f.src.ID, f.token, body); rec.Code != http.StatusAccepted {
		t.Fatalf("PUT: %d", rec.Code) // Sonarr can be set to PUT
	}

	// Wrong app or an unsupported version: rejected with the reason.
	for _, ua := range []string{"Radarr/6.4.4.10685 (alpine 3.22.2)", "Sonarr/3.0.10.1567 (ubuntu 20.04)", ""} {
		rec := f.send(http.MethodPost, ua, f.src.ID, f.token, body)
		if a := decode(t, rec); rec.Code != http.StatusBadRequest || !strings.Contains(a.Message, "version") && !strings.Contains(a.Message, "came from") {
			t.Errorf("UA %q: %d %+v", ua, rec.Code, a)
		}
	}

	// Too large: a valid notification padded past the limit is refused.
	valid := fixture(t, "sonarr_download.json")
	big := append([]byte(`{"pad":"`+strings.Repeat("x", maxBodyBytes)+`",`), valid[1:]...)
	if rec := f.ok(big); rec.Code != http.StatusBadRequest {
		t.Fatalf("large body: %d", rec.Code)
	}

	// Per-source rate: beyond perSource in a window → 429; the next window is fine.
	f.clock = f.clock.Add(time.Hour)
	for i := range perSource {
		if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusOK {
			t.Fatalf("notification %d: %d", i, rec.Code)
		}
	}
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", rec.Code)
	}
	f.clock = f.clock.Add(window)
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusOK {
		t.Fatalf("next window: %d", rec.Code)
	}

	// Repeated failed sign-ins from one peer lock it out, even with the right token.
	for range failuresPerIP {
		f.send(http.MethodPost, sonarrUA, f.src.ID, "lfh_wrong", body)
	}
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after failures: %d", rec.Code)
	}

	// Revoked: refused at once.
	f.clock = f.clock.Add(window)
	f.st.Revoke(context.Background(), "sonarr")
	if rec := f.ok(fixture(t, "sonarr_test.json")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", rec.Code)
	}
}

// If the scheduler can't take the hint (a library added moments ago), the
// source is told to retry later; the next reconciliation scan covers it.
func TestSchedulerNotReady(t *testing.T) {
	f := newHook(t)
	f.hints.err = errors.New("library: not found")
	rec := f.ok(fixture(t, "sonarr_download.json"))
	if a := decode(t, rec); rec.Code != http.StatusServiceUnavailable || a.Code != "source_unavailable" {
		t.Fatalf("%d %+v", rec.Code, a)
	}
	if r := f.lastReceipt(t); r.Result != ResultRejected || !strings.Contains(r.Reason, "not ready") {
		t.Fatalf("receipt %+v", r)
	}
}

// Sender-controlled text is bounded before it reaches the receipt row or the log.
func TestUntrustedTextIsCapped(t *testing.T) {
	f := newHook(t)
	body := fixture(t, "sonarr_test.json")
	// A multi-byte rune straddling the cap must not be split.
	ua := "Sonarr" + strings.Repeat("é", 100) + "/4.0.20.3014"
	f.send(http.MethodPost, ua, f.src.ID, f.token, body)
	r := f.lastReceipt(t)
	if len(r.App) > maxApp || !utf8.ValidString(r.App) || !strings.HasPrefix(r.App, "Sonarr") {
		t.Fatalf("app %d bytes valid=%v", len(r.App), utf8.ValidString(r.App))
	}

	long := strings.Repeat("é", 1000)
	rec := f.ok([]byte(`{"eventType":"Download","series":{"path":"/tv/x"},"episodeFile":{"path":"/` + long + `/a.mkv"}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%d", rec.Code)
	}
	r = f.lastReceipt(t)
	if r.Result != ResultRejected || len(r.Reason) == 0 || len(r.Reason) > maxReason || !utf8.ValidString(r.Reason) {
		t.Fatalf("reason %d bytes valid=%v", len(r.Reason), utf8.ValidString(r.Reason))
	}
}
