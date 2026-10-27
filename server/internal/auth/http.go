package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/csrf"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// CookieName is the session cookie from the CTR-1 contract.
const CookieName = "lisimflix_session"

const maxBodyBytes = 16 << 10

// CSRF wraps h in gorilla/csrf with the CTR-1 header (X-CSRF-Token) and the
// shared 403 csrf_failed error. secure must match the configured public
// origin's scheme. When it is false, requests are marked as plain HTTP so the
// TLS-only Referer check doesn't run; the app still checks Origin itself.
func CSRF(key []byte, secure bool) func(http.Handler) http.Handler {
	protect := csrf.Protect(key,
		csrf.Secure(secure),
		csrf.HttpOnly(true),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
		csrf.CookieName("lisimflix_csrf"),
		csrf.RequestHeader("X-CSRF-Token"),
		csrf.MaxAge(int(absoluteTimeout/time.Second)),
		csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			contracts.WriteError(w, r, contracts.CodeCSRFFailed, "Request blocked. Reload the page and try again.")
		})),
	)
	return func(h http.Handler) http.Handler {
		p := protect(h)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !secure {
				r = csrf.PlaintextHTTPRequest(r)
			}
			p.ServeHTTP(w, r)
		})
	}
}

// Register adds the three CTR-1 auth routes. They must be served behind CSRF.
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/session", m.handleSession)
	mux.HandleFunc("POST /api/v1/auth/login", m.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", m.handleLogout)
}

// Require serves next only for a live session, with its contracts.Principal
// in the request context. Anything else gets 401 unauthenticated.
func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := m.fromRequest(r)
		if !ok {
			contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Sign in to continue.")
			return
		}
		ctx := contracts.WithPrincipal(r.Context(), contracts.Principal{
			AccountID: m.viewer.AccountID, ProfileID: m.viewer.ProfileID, AuthSessionID: s.id,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Manager) fromRequest(r *http.Request) (*session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, false
	}
	return m.lookup(r.Context(), c.Value)
}

func (m *Manager) state(r *http.Request, signedIn bool) contracts.AuthState {
	st := contracts.AuthState{CSRFToken: csrf.Token(r)}
	if signedIn {
		st.Authenticated = true
		st.Viewer = &contracts.Viewer{AccountID: m.viewer.AccountID, ProfileID: m.viewer.ProfileID, Username: m.viewer.Username}
	}
	return st
}

func (m *Manager) handleSession(w http.ResponseWriter, r *http.Request) {
	_, ok := m.fromRequest(r)
	writeJSON(w, http.StatusOK, m.state(r, ok))
}

func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
	log := obs.Log(r.Context(), m.log)
	peer := peerKey(r.RemoteAddr)
	// Failures never log the username: a mistyped password often lands there.
	fail := func(reason string) { log.Info("auth.login", "result", "fail", "reason", reason, "peer", peer) }
	req, ok := decodeLogin(w, r)
	if !ok {
		fail("invalid_request")
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send a JSON username and password.")
		return
	}
	key := strings.ToLower(req.Username) + "|" + peer
	if limited, wait := m.limited(key); limited {
		fail("rate_limited")
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		contracts.WriteError(w, r, contracts.CodeRateLimited, "Too many sign-in attempts. Try again later.")
		return
	}
	good, err := m.checkPassword(r.Context(), peer, req.Username, []byte(req.Password))
	if err != nil {
		fail("busy")
		w.Header().Set("Retry-After", "1")
		contracts.WriteError(w, r, contracts.CodeRateLimited, "Sign-in is busy. Try again in a moment.")
		return
	}
	if !good {
		fail("bad_credentials")
		m.failed(key)
		contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Invalid username or password.")
		return
	}
	m.succeeded(key)
	if c, err := r.Cookie(CookieName); err == nil {
		m.remove(r.Context(), c.Value, "rotated") // the old session ends
	}
	token, s, err := m.create(r.Context())
	if err != nil {
		contracts.WriteError(w, r, contracts.CodeInternalError, "Internal error.")
		return
	}
	log.Info("auth.login", "result", "ok", "username", req.Username, "session_id", s.id, "peer", peer)
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: s.created.Add(absoluteTimeout),
		HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, m.state(r, true))
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookieName)
	if err != nil || !m.remove(r.Context(), c.Value, "logout") {
		contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Sign in to continue.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func decodeLogin(w http.ResponseWriter, r *http.Request) (contracts.LoginRequest, bool) {
	var req contracts.LoginRequest
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return req, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return req, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		return req, false
	}
	if req.Username == "" || len(req.Username) > 64 || req.Password == "" || len(req.Password) > MaxPasswordBytes {
		return req, false
	}
	return req, true
}

// peerKey identifies the TCP peer for rate limits: the IPv4 address, or the
// /64 prefix for IPv6, since one machine can rotate through many addresses
// in its /64 (#24). Forwarded headers are never trusted here.
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(errors.New("auth: encode response: " + err.Error())) // fixed types; cannot fail
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}
