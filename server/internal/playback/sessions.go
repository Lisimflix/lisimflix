package playback

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Contract values (CTR-1 PlaybackSession enums) and M1 bounds.
const (
	heartbeatSeconds = 10
	idleTimeout      = 60 * time.Second
	tombstoneTTL     = 5 * time.Minute
	maxTombstones    = 64
	minGrantKeyBytes = 32
	sweepEvery       = time.Second // how soon an idle session's stalled delivery is cut off
)

// Options configures a Server. Authorize is mandatory: it is asked whether a
// principal may list, start or watch a movie ID, on every such request.
type Options struct {
	Source      Source
	GrantKey    []byte
	MaxSessions int
	Authorize   func(contracts.Principal, string) bool
	Log         *slog.Logger // session events and source errors; nil discards
}

type session struct {
	id, key           string // key: auth session + idempotency key
	owner             contracts.Principal
	movieID, revision string
	info              fs.FileInfo // file identity when the session started
	grant             string
	deadline          time.Time // idle expiry
	lastSeq           int64
	ended             time.Time // zero while active; then a tombstone
	deliveries        map[*delivery]struct{}
}

// delivery is one in-flight content response that revocation can cut off.
type delivery struct {
	cancel context.CancelFunc
	rc     writeDeadliner
}

// writeDeadliner is the part of http.ResponseController a delivery uses.
type writeDeadliner interface{ SetWriteDeadline(time.Time) error }

// Server holds the playback sessions for one source.
type Server struct {
	root      *os.Root
	src       Source
	grantKey  []byte
	max       int
	authorize func(contracts.Principal, string) bool
	now       func() time.Time
	log       *slog.Logger

	inflight atomic.Int64 // content handlers still running, revoked or not
	written  atomic.Int64 // content bytes written, for tests

	mu       sync.Mutex
	sessions map[string]*session // active sessions and tombstones, by ID
	byKey    map[string]*session
	closed   bool

	done      chan struct{}
	closeOnce sync.Once
}

// New returns a Server for opts.Source inside root. The caller owns root and
// closes it after Close. A missing or changed source is reported by Check and
// by requests, not by New.
func New(root *os.Root, opts Options) (*Server, error) {
	switch {
	case root == nil:
		return nil, errors.New("playback: root is required")
	case !validRelative(opts.Source.RelativePath):
		return nil, errors.New("playback: source path must be relative and inside the root")
	case opts.Source.Movie.ID == "" || opts.Source.Movie.SourceRevision == "":
		return nil, errors.New("playback: source movie ID and revision are required")
	case len(opts.GrantKey) < minGrantKeyBytes:
		return nil, errors.New("playback: grant key must be at least 32 bytes")
	case opts.MaxSessions < 1:
		return nil, errors.New("playback: MaxSessions must be at least 1")
	case opts.Authorize == nil:
		return nil, errors.New("playback: Authorize is required")
	}
	s := &Server{
		root: root, src: opts.Source, grantKey: append([]byte(nil), opts.GrantKey...), max: opts.MaxSessions,
		authorize: opts.Authorize, now: time.Now, log: cmp.Or(opts.Log, slog.New(slog.DiscardHandler)),
		sessions: map[string]*session{}, byKey: map[string]*session{}, done: make(chan struct{}),
	}
	go s.sweeper()
	return s, nil
}

// sweeper ends idle sessions even when nothing calls the server, so a
// delivery stuck on a stalled client is cut off within sweepEvery of expiry.
func (s *Server) sweeper() {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			s.sweepLocked(s.now())
			s.mu.Unlock()
		}
	}
}

func (s *Server) sign(sess *session) string {
	m := hmac.New(sha256.New, s.grantKey)
	m.Write([]byte(strings.Join([]string{"lisimflix-grant-v1", sess.id, sess.owner.AuthSessionID,
		sess.owner.AccountID, sess.owner.ProfileID, sess.movieID, sess.revision}, "\x00")))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (sess *session) ownedBy(p contracts.Principal) bool {
	return sess.owner == p
}

func (sess *session) public() contracts.PlaybackSession {
	return contracts.PlaybackSession{
		ID: sess.id, MovieID: sess.movieID, SourceRevision: sess.revision,
		ContentURL: "/api/v1/playback/sessions/" + sess.id + "/content?grant=" + sess.grant,
		Route:      "direct", HeartbeatIntervalSeconds: heartbeatSeconds, IdleTimeoutSeconds: int(idleTimeout / time.Second),
	}
}

// endLocked turns an active session into a tombstone and cuts off its
// deliveries, logging why (stopped, expired, revoked or closed). s.mu must
// be held.
func (s *Server) endLocked(sess *session, now time.Time, reason string) {
	if !sess.ended.IsZero() {
		return
	}
	sess.ended = now
	s.log.Info("playback.end", "session_id", sess.id, "auth_session", sess.owner.AuthSessionID, "reason", reason)
	// Cancel first, then set the deadline: a write that set its own deadline
	// before this one is unblocked by it, and a write that sets its deadline
	// after sees the cancel (deliveryWriter.Write rechecks).
	for d := range sess.deliveries {
		d.cancel()
		_ = d.rc.SetWriteDeadline(time.Now()) // unblock a write stuck on a slow client
	}
	sess.deliveries = nil
	s.boundTombstonesLocked()
}

// boundTombstonesLocked drops the oldest tombstones beyond maxTombstones.
func (s *Server) boundTombstonesLocked() {
	for {
		var oldest *session
		n := 0
		for _, t := range s.sessions {
			if t.ended.IsZero() {
				continue
			}
			n++
			if oldest == nil || t.ended.Before(oldest.ended) {
				oldest = t
			}
		}
		if n <= maxTombstones {
			return
		}
		s.dropLocked(oldest.id, oldest)
	}
}

// liveLocked reports whether sess is active, ending it if its idle deadline
// has passed.
func (s *Server) liveLocked(sess *session, now time.Time) bool {
	if sess.ended.IsZero() && now.After(sess.deadline) {
		s.endLocked(sess, now, "expired")
	}
	return sess.ended.IsZero()
}

// sweepLocked ends idle sessions and drops tombstones older than
// tombstoneTTL. endLocked keeps the count within maxTombstones.
func (s *Server) sweepLocked(now time.Time) {
	for id, sess := range s.sessions {
		if !s.liveLocked(sess, now) && now.Sub(sess.ended) > tombstoneTTL {
			s.dropLocked(id, sess)
		}
	}
}

func (s *Server) dropLocked(id string, sess *session) {
	delete(s.sessions, id)
	if s.byKey[sess.key] == sess {
		delete(s.byKey, sess.key)
	}
}

func (s *Server) activeLocked() int {
	n := 0
	for _, sess := range s.sessions {
		if sess.ended.IsZero() {
			n++
		}
	}
	return n
}

// RevokeAuthSession ends every playback session started under authSessionID
// and cuts off their deliveries. auth.Manager calls it on logout and expiry.
func (s *Server) RevokeAuthSession(authSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, sess := range s.sessions {
		if sess.owner.AuthSessionID == authSessionID {
			s.endLocked(sess, now, "revoked")
		}
	}
}

// Close ends all sessions and deliveries and stops the sweeper. Later
// requests get 503. It is safe to call more than once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	now := s.now()
	for _, sess := range s.sessions {
		s.endLocked(sess, now, "closed")
	}
	return nil
}

func (s *Server) activeDeliveries() int { return int(s.inflight.Load()) }

func (s *Server) tombstoneCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sess := range s.sessions {
		if !sess.ended.IsZero() {
			n++
		}
	}
	return n
}

func newID() string { return "ps-" + rand.Text() }
