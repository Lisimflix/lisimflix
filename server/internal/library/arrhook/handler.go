package arrhook

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// Path is where Sonarr and Radarr send notifications, outside /api/ so the
// browser-only Origin and CSRF checks don't apply; sources authenticate
// with their own tokens instead.
const Path = "/hooks/v1/arr"

// Hinter queues a scoped scan; *library.Scheduler implements it.
type Hinter interface {
	Hint(rootID, scope, cause string) error
}

// CauseImport is the scan cause for notifications, as library.CauseImport.
const CauseImport = "import"

// Limits (spec §3.6: bound payload and event rates).
const (
	maxBodyBytes   = 1 << 20 // an import-complete payload for a season pack is far smaller
	perSource      = 60      // notifications per source per window
	failuresPerIP  = 20      // failed sign-ins per peer per window
	window         = time.Minute
	maxPeerEntries = 4096

	maxApp    = 64  // bytes of the User-Agent product kept; it is sender-controlled
	maxReason = 512 // bytes of a receipt's reason kept (it can echo a sender's path)
)

// Handler receives notifications.
type Handler struct {
	store *Store
	hint  Hinter
	log   *slog.Logger
	now   func() time.Time

	mu       sync.Mutex
	sources  map[string]*counter // by source ID
	failures map[string]*counter // by peer
}

type counter struct {
	start time.Time
	n     int
}

func NewHandler(st *Store, h Hinter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{store: st, hint: h, log: log, now: time.Now,
		sources: map[string]*counter{}, failures: map[string]*counter{}}
}

// count adds one to key's counter in m and reports whether it is now over
// limit. Counters reset each window; a full map drops expired entries first.
func (h *Handler) count(m map[string]*counter, key string, limit int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	c := m[key]
	if c == nil || now.Sub(c.start) >= window {
		if len(m) >= maxPeerEntries {
			for k, v := range m {
				if now.Sub(v.start) >= window {
					delete(m, k)
				}
			}
			if len(m) >= maxPeerEntries {
				return true // still full of live entries: refuse rather than grow
			}
		}
		c = &counter{start: now}
		m[key] = c
	}
	c.n++
	return c.n > limit
}

func (h *Handler) over(m map[string]*counter, key string, limit int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := m[key]
	return c != nil && h.now().Sub(c.start) < window && c.n >= limit
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.Header().Set("Allow", "POST, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	log := obs.Log(ctx, h.log)
	peer := peerKey(r.RemoteAddr)
	if h.over(h.failures, peer, failuresPerIP) {
		log.Warn("library.hook", "result", "rate_limited", "peer", peer)
		contracts.WriteError(w, r, contracts.CodeRateLimited, "Too many failed attempts. Try again later.")
		return
	}
	id, token, ok := r.BasicAuth()
	src, err := h.store.Authenticate(ctx, id, token)
	if !ok || err != nil {
		if ok && !errors.Is(err, ErrUnauthorized) {
			log.Error("library.hook", "result", "error", "err", err.Error())
			contracts.WriteError(w, r, contracts.CodeInternalError, "Internal error.")
			return
		}
		if h.count(h.failures, peer, failuresPerIP) {
			// Only when the map is full of live peers: refuse the new one
			// rather than fail open without recording its failure.
			log.Warn("library.hook", "result", "rate_limited", "peer", peer)
			contracts.WriteError(w, r, contracts.CodeRateLimited, "Too many failed attempts. Try again later.")
			return
		}
		log.Warn("library.hook", "result", "unauthenticated", "peer", peer)
		w.Header().Set("WWW-Authenticate", `Basic realm="lisimflix-hooks", charset="UTF-8"`)
		contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Use the source ID as the username and its token as the password.")
		return
	}
	if h.count(h.sources, src.ID, perSource) {
		log.Warn("library.hook", "source_id", src.ID, "source", src.Name, "result", "rate_limited")
		contracts.WriteError(w, r, contracts.CodeRateLimited, "Too many notifications. Missed changes are found by the next scan.")
		return
	}

	rec := Receipt{ID: "rcpt-" + rand.Text(), SourceID: src.ID, At: h.now(), RequestID: obs.RequestID(ctx)}
	reject := func(reason string) {
		rec.Result, rec.Reason = ResultRejected, reason
		h.finish(w, r, log, src, rec, http.StatusBadRequest)
	}
	app, verr := checkAgent(src.Kind, r.UserAgent())
	rec.App = truncate(app, maxApp)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		rec.Event = "?"
		reject("body too large or unreadable")
		return
	}
	ev, perr := parse(src.Kind, body)
	rec.Event = truncate(ev.Type, maxApp)
	switch {
	case verr != nil:
		reject(verr.Error())
		return
	case perr != nil:
		rec.Event = cmpEvent(rec.Event)
		reject(perr.Error())
		return
	case ev.Kind == kindTest:
		rec.Result = ResultTest
		h.finish(w, r, log, src, rec, http.StatusOK)
		return
	case ev.Kind == kindIgnore:
		rec.Result, rec.Reason = ResultIgnored, "event doesn't change files"
		h.finish(w, r, log, src, rec, http.StatusOK)
		return
	}
	scopes := make([]Scope, 0, len(ev.Paths))
	for _, p := range ev.Paths {
		s, err := src.resolve(p)
		if err != nil {
			// All or nothing: a path we can't place is a setup mistake the
			// admin should see in Sonarr/Radarr, not a partial scan.
			reject("path " + p + ": " + err.Error())
			return
		}
		scopes = append(scopes, s)
	}
	rec.Scopes = group(scopes)
	for _, s := range rec.Scopes {
		if err := h.hint.Hint(s.RootID, s.Path, CauseImport); err != nil {
			if errors.Is(err, library.ErrInvalid) {
				// A scope the library won't scan (e.g. through a symlinked
				// folder) is the path's problem, like an unmappable path.
				reject(err.Error())
				return
			}
			// The scheduler doesn't know the root yet (just added) or isn't
			// running; the next reconciliation scan covers the change.
			rec.Result, rec.Reason = ResultRejected, "library not ready: "+err.Error()
			h.finish(w, r, log, src, rec, http.StatusServiceUnavailable)
			return
		}
	}
	rec.Result = ResultQueued
	h.finish(w, r, log, src, rec, http.StatusAccepted)
}

func cmpEvent(e string) string {
	if e == "" {
		return "?"
	}
	return e
}

// finish records the receipt, logs it and answers. A 202 means queued: the
// scans themselves run later, as runs with cause "import".
func (h *Handler) finish(w http.ResponseWriter, r *http.Request, log *slog.Logger, src Source, rec Receipt, status int) {
	rec.Reason = truncate(rec.Reason, maxReason)
	if err := h.store.record(r.Context(), rec); err != nil {
		log.Error("library.hook", "source_id", src.ID, "err", err.Error())
	}
	level := slog.LevelInfo
	if rec.Result == ResultRejected {
		level = slog.LevelWarn
	}
	log.Log(r.Context(), level, "library.hook", "source_id", src.ID, "source", src.Name, "event", rec.Event,
		"app", rec.App, "result", rec.Result, "reason", rec.Reason, "scopes", len(rec.Scopes))
	switch status {
	case http.StatusBadRequest:
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Notification rejected: "+rec.Reason)
		return
	case http.StatusServiceUnavailable:
		contracts.WriteError(w, r, contracts.CodeSourceUnavailable, "The library isn't ready yet; the next scan will catch up.")
		return
	}
	scopes := rec.Scopes
	if scopes == nil {
		scopes = []Scope{} // "scopes": [], never null
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(struct {
		Result    string  `json:"result"`
		ReceiptID string  `json:"receiptId"`
		Scopes    []Scope `json:"scopes"`
	}{rec.Result, rec.ID, scopes})
}

// peerKey is the rate-limit key auth uses: the IPv4 address, or the /64.
func peerKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	ip = ip.Unmap().WithZone("")
	if ip.Is4() {
		return ip.String()
	}
	return netip.PrefixFrom(ip, 64).Masked().String()
}

// truncate cuts s to at most n bytes on a UTF-8 rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
