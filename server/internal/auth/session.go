package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/m1config"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// M1 guardrails for a one-viewer proof, not measured deployment capacity.
const (
	idleTimeout     = 7 * 24 * time.Hour
	absoluteTimeout = 30 * 24 * time.Hour
	maxSessions     = 64
	loginLimit      = 5
	loginWindow     = 5 * time.Minute
	maxLimiterKeys  = 256
	maxVerifies     = 2 // concurrent argon2 runs (one shared, one viewer-only), so logins can't exhaust memory
	tokenBytes      = 32
)

// verifyWait is how long a login waits for a free argon2 slot; tests shorten it.
var verifyWait = 5 * time.Second

type session struct {
	id          string
	created, at time.Time // at is the last use
}

type attempts struct {
	n     int
	reset time.Time
}

// Manager holds the M1 viewer's in-memory sessions. Tokens are stored only as
// SHA-256 hashes. A restart signs everyone out, which is acceptable for M1.
type Manager struct {
	viewer   m1config.Viewer
	hash     params
	dummy    params // same cost as hash; verified for unknown usernames
	secure   bool
	onRevoke func(authSessionID string)
	now      func() time.Time
	log      *slog.Logger // events (obs); discards until SetLogger
	// Two argon2 slots: anySlot serves every login; viewerSlot serves only the
	// configured username, so a spray of fake usernames can't starve the
	// viewer (#24). checking holds peers with a check in progress.
	anySlot, viewerSlot chan struct{}
	checking            map[string]bool

	mu       sync.Mutex
	sessions map[[sha256.Size]byte]*session
	limits   map[string]*attempts
}

// NewManager checks viewer.PasswordHash and returns a Manager for it.
// secure sets the cookie's Secure flag; pass true only for an https public
// origin. onRevoke is called with the auth-session ID after logout, rotation,
// expiry or eviction, outside any lock.
func NewManager(viewer m1config.Viewer, secure bool, onRevoke func(string)) (*Manager, error) {
	if onRevoke == nil {
		return nil, errors.New("auth: onRevoke is required")
	}
	p, err := parseHash(viewer.PasswordHash)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, len(p.salt))
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	dummy := params{memory: p.memory, time: p.time, threads: p.threads, salt: salt, key: make([]byte, len(p.key))}
	return &Manager{
		viewer: viewer, hash: p, dummy: dummy, secure: secure, onRevoke: onRevoke, now: time.Now, log: slog.New(slog.DiscardHandler),
		anySlot: make(chan struct{}, 1), viewerSlot: make(chan struct{}, 1), checking: map[string]bool{},
		sessions: map[[sha256.Size]byte]*session{}, limits: map[string]*attempts{},
	}, nil
}

// Secure reports whether session cookies carry the Secure flag.
func (m *Manager) Secure() bool { return m.secure }

var errBusy = errors.New("auth: no free password-check slot")

// checkPassword always runs one argon2 verification, against the dummy hash
// when the username is wrong, so timing doesn't reveal valid usernames. A
// peer gets one check at a time (a second one is errBusy at once). The
// viewer's username may use either slot, any other only the shared one; a
// login waits at most verifyWait, then returns errBusy.
func (m *Manager) checkPassword(ctx context.Context, peer, username string, password []byte) (bool, error) {
	m.mu.Lock()
	if m.checking[peer] {
		m.mu.Unlock()
		return false, errBusy
	}
	m.checking[peer] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.checking, peer)
		m.mu.Unlock()
	}()

	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(m.viewer.Username)) == 1
	viewerSlot := m.viewerSlot
	if !userOK {
		viewerSlot = nil // a nil channel never receives, so fakes can only take anySlot
	}
	t := time.NewTimer(verifyWait)
	defer t.Stop()
	var held chan struct{}
	select {
	case m.anySlot <- struct{}{}:
		held = m.anySlot
	case viewerSlot <- struct{}{}:
		held = viewerSlot
	case <-t.C:
		return false, errBusy
	case <-ctx.Done():
		return false, errBusy
	}
	defer func() { <-held }()
	p := m.dummy
	if userOK {
		p = m.hash
	}
	return verify(p, password) && userOK, nil
}

// limited reports whether key may not attempt a login now, and for how long.
func (m *Manager) limited(key string) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if a, ok := m.limits[key]; ok {
		if now.Before(a.reset) {
			if a.n >= loginLimit {
				return true, a.reset.Sub(now)
			}
			return false, 0
		}
		delete(m.limits, key)
	}
	return false, 0
}

// makeRoomLocked frees a slot for a new key in a full table: expired entries
// first, then the oldest entry for any username other than the viewer's, so
// fake usernames can neither lock the viewer out nor reset the viewer's
// counter (spec §6.4). Only a table of nothing but viewer keys loses one.
func (m *Manager) makeRoomLocked(now time.Time) {
	if len(m.limits) < maxLimiterKeys {
		return
	}
	viewerPrefix := strings.ToLower(m.viewer.Username) + "|"
	var oldest, oldestAny string
	for k, a := range m.limits {
		if !now.Before(a.reset) {
			delete(m.limits, k)
			continue
		}
		if oldestAny == "" || a.reset.Before(m.limits[oldestAny].reset) {
			oldestAny = k
		}
		if !strings.HasPrefix(k, viewerPrefix) && (oldest == "" || a.reset.Before(m.limits[oldest].reset)) {
			oldest = k
		}
	}
	if len(m.limits) < maxLimiterKeys {
		return
	}
	if oldest == "" {
		oldest = oldestAny
	}
	delete(m.limits, oldest)
}

func (m *Manager) failed(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	a, ok := m.limits[key]
	if !ok || !now.Before(a.reset) {
		if !ok {
			m.makeRoomLocked(now)
		}
		a = &attempts{reset: now.Add(loginWindow)}
		m.limits[key] = a
	}
	a.n++
}

func (m *Manager) succeeded(key string) {
	m.mu.Lock()
	delete(m.limits, key)
	m.mu.Unlock()
}

// create stores a new session and returns its cookie token. If the store is
// full, the least recently used session is evicted and revoked.
// SetLogger sets where auth events go (login, session end). Call it before
// serving. Events never include passwords, tokens or cookies.
func (m *Manager) SetLogger(l *slog.Logger) {
	if l != nil {
		m.log = l
	}
}

func (m *Manager) create(ctx context.Context) (token string, s *session, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	now := m.now()
	s = &session{id: "as-" + rand.Text(), created: now, at: now}
	var evicted []string
	m.mu.Lock()
	expiredIDs := m.sweepLocked(now)
	for len(m.sessions) >= maxSessions {
		var oldest [sha256.Size]byte
		var o *session
		for k, v := range m.sessions {
			if o == nil || v.at.Before(o.at) {
				oldest, o = k, v
			}
		}
		delete(m.sessions, oldest)
		evicted = append(evicted, o.id)
	}
	m.sessions[sha256.Sum256([]byte(token))] = s
	m.mu.Unlock()
	// Expired sessions are only swept here; they aren't this request's doing,
	// so they're logged without its request ID. The eviction is (#86).
	m.revoke(context.Background(), "expired", expiredIDs...)
	m.revoke(ctx, "evicted", evicted...)
	return token, s, nil
}

// lookup returns the live session for token and marks it used. An expired
// session is removed and revoked.
func (m *Manager) lookup(ctx context.Context, token string) (*session, bool) {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return nil, false
	}
	key := sha256.Sum256([]byte(token))
	now := m.now()
	m.mu.Lock()
	s, ok := m.sessions[key]
	if ok && expired(s, now) {
		delete(m.sessions, key)
		m.mu.Unlock()
		m.revoke(ctx, "expired", s.id)
		return nil, false
	}
	if ok {
		s.at = now
	}
	m.mu.Unlock()
	return s, ok
}

// remove deletes the session for token, if any, and revokes it.
// remove ends the session for token, if any, with reason "logout" or "rotated".
func (m *Manager) remove(ctx context.Context, token, reason string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	key := sha256.Sum256([]byte(token))
	m.mu.Lock()
	s, ok := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if ok {
		m.revoke(ctx, reason, s.id)
	}
	return ok
}

func (m *Manager) sweepLocked(now time.Time) []string {
	var ids []string
	for k, s := range m.sessions {
		if expired(s, now) {
			delete(m.sessions, k)
			ids = append(ids, s.id)
		}
	}
	return ids
}

func expired(s *session, now time.Time) bool {
	return now.Sub(s.at) > idleTimeout || now.Sub(s.created) > absoluteTimeout
}

// Sweep ends every expired session and calls onRevoke for each, so expiry
// cuts a session's streams without waiting for its next request (#30).
func (m *Manager) Sweep() {
	m.mu.Lock()
	ids := m.sweepLocked(m.now())
	m.mu.Unlock()
	m.revoke(context.Background(), "expired", ids...)
}

// Run calls Sweep every interval until ctx ends.
func (m *Manager) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Sweep()
		}
	}
}

// revoke logs each ended session with its reason (logout, rotated, expired
// or evicted) and calls onRevoke.
func (m *Manager) revoke(ctx context.Context, reason string, ids ...string) {
	for _, id := range ids {
		obs.Log(ctx, m.log).Info("auth.session_end", "session_id", id, "reason", reason)
		m.onRevoke(id)
	}
}

func (m *Manager) sessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) limiterSize() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.limits)
}
