package playback

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

const (
	maxBodyBytes = 1 << 10
	writeStall   = 30 * time.Second // per write, never for the whole movie
)

var (
	idPattern   = regexp.MustCompile(`^ps-[A-Z2-7]{26}$`)
	errRevoked  = errors.New("playback: delivery revoked")
	errNotFound = errors.New("not found")
)

// Handler serves GET /api/v1/movies and every /api/v1/playback/ operation in
// CTR-1, with full paths. Requests must carry a contracts.Principal (set by
// auth.Require); without one they get 401.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/movies", s.withPrincipal(s.movies))
	mux.HandleFunc("POST /api/v1/playback/sessions", s.withPrincipal(s.start))
	mux.HandleFunc("POST /api/v1/playback/sessions/{id}/heartbeat", s.withPrincipal(s.heartbeat))
	mux.HandleFunc("DELETE /api/v1/playback/sessions/{id}", s.withPrincipal(s.stop))
	mux.HandleFunc("GET /api/v1/playback/sessions/{id}/content", s.withPrincipal(s.content))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		contracts.WriteError(w, r, contracts.CodeNotFound, "Not found.")
	})
	return mux
}

func (s *Server) withPrincipal(h func(http.ResponseWriter, *http.Request, contracts.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := contracts.PrincipalFrom(r.Context())
		if !ok {
			contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Sign in to continue.")
			return
		}
		s.mu.Lock()
		closed := s.closed
		s.unlock()
		if closed {
			contracts.WriteError(w, r, contracts.CodeSourceUnavailable, "Playback is shutting down.")
			return
		}
		h(w, r, p)
	}
}

func (s *Server) movies(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	list := contracts.MovieList{}
	if s.authorize(p, s.src.Movie.ID) {
		list.Items = []contracts.Movie{s.src.Movie}
	}
	writeJSON(w, http.StatusOK, list)
}

func validKey(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func (s *Server) start(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	key := r.Header.Get("Idempotency-Key")
	var req contracts.PlaybackSessionRequest
	if !validKey(key) || !decode(w, r, &req) || req.MovieID == "" {
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send a movieId and an Idempotency-Key.")
		return
	}
	// A known key is answered first: a changed payload is 409 whatever it names.
	scoped := p.AuthSessionID + "\x00" + key
	if s.replay(w, r, scoped, req.MovieID) {
		return
	}
	if req.MovieID != s.src.Movie.ID || !s.authorize(p, req.MovieID) {
		contracts.WriteError(w, r, contracts.CodeNotFound, "Movie not found.")
		return
	}
	info, err := s.statSource()
	if err != nil {
		s.sourceError(w, r, err, "")
		return
	}
	s.mu.Lock()
	now := s.now()
	s.sweepLocked(now)
	if prev, ok := s.byKey[scoped]; ok { // a concurrent start with the same key won
		s.unlock()
		s.replayed(w, r, prev, req.MovieID)
		return
	}
	if s.activeLocked() >= s.max {
		s.unlock()
		contracts.WriteError(w, r, contracts.CodeCapacityUnavailable, "Too many active playback sessions. Stop one and try again.")
		return
	}
	sess := &session{
		id: newID(), key: scoped, owner: p, movieID: req.MovieID, revision: s.src.Movie.SourceRevision,
		info: info, deadline: now.Add(idleTimeout), lastSeq: -1, deliveries: map[*delivery]struct{}{},
	}
	sess.grant = s.sign(sess)
	s.sessions[sess.id] = sess
	s.byKey[scoped] = sess
	out := sess.public()
	s.unlock()
	obs.Log(r.Context(), s.log).Info("playback.start", "session_id", sess.id, "auth_session", p.AuthSessionID, "movie_id", req.MovieID)
	writeJSON(w, http.StatusCreated, out)
}

// replay answers a start whose idempotency key is already known, and
// reports whether it did.
func (s *Server) replay(w http.ResponseWriter, r *http.Request, scoped, movieID string) bool {
	s.mu.Lock()
	s.sweepLocked(s.now())
	prev, ok := s.byKey[scoped]
	s.unlock()
	if ok {
		s.replayed(w, r, prev, movieID)
	}
	return ok
}

// replayed: the same payload for a live session returns it (200). A changed
// payload, or a session that has ended, is 409 (#20 point 1).
func (s *Server) replayed(w http.ResponseWriter, r *http.Request, prev *session, movieID string) {
	s.mu.Lock()
	live := s.liveLocked(prev, s.now())
	out := prev.public()
	s.unlock()
	if prev.movieID != movieID {
		contracts.WriteError(w, r, contracts.CodeIdempotencyConflict, "This Idempotency-Key was already used for another request.")
		return
	}
	if !live { // UI-1 shows this text and starts again with a fresh key
		contracts.WriteError(w, r, contracts.CodeIdempotencyConflict, "Playback ended. Press Play to start again.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// owned returns the caller's session with the path's ID, or errNotFound for
// unknown IDs and sessions owned by anyone else.
func (s *Server) owned(r *http.Request, p contracts.Principal) (*session, error) {
	id := r.PathValue("id")
	if !idPattern.MatchString(id) {
		return nil, errNotFound
	}
	sess, ok := s.sessions[id]
	if !ok || !sess.ownedBy(p) {
		return nil, errNotFound
	}
	return sess, nil
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	var req contracts.HeartbeatRequest
	if !decode(w, r, &req) || req.Sequence < 0 {
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send a nonnegative integer sequence.")
		return
	}
	s.mu.Lock()
	defer s.unlock()
	now := s.now()
	sess, err := s.owned(r, p)
	switch {
	case err != nil:
		contracts.WriteError(w, r, contracts.CodeNotFound, "Playback session not found.")
	case !s.liveLocked(sess, now):
		contracts.WriteError(w, r, contracts.CodePlaybackExpired, "Playback session ended. Start playback again.")
	case req.Sequence < sess.lastSeq:
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Heartbeat sequence went backwards.")
	case req.Sequence == sess.lastSeq: // a retried heartbeat: no-op (#20 point 2)
		w.WriteHeader(http.StatusNoContent)
	default:
		sess.lastSeq = req.Sequence
		sess.deadline = now.Add(idleTimeout)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	s.mu.Lock()
	defer s.unlock()
	sess, err := s.owned(r, p)
	if err != nil {
		contracts.WriteError(w, r, contracts.CodeNotFound, "Playback session not found.")
		return
	}
	s.endLocked(sess, s.now(), "stopped")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) content(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	s.inflight.Add(1)
	defer s.inflight.Add(-1)
	s.mu.Lock()
	sess, err := s.owned(r, p)
	if err == nil && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("grant")), []byte(sess.grant)) != 1 {
		err = errNotFound
	}
	if err != nil {
		s.unlock()
		contracts.WriteError(w, r, contracts.CodeNotFound, "Playback session not found.")
		return
	}
	live := s.liveLocked(sess, s.now())
	s.unlock()
	if !live {
		contracts.WriteError(w, r, contracts.CodePlaybackExpired, "Playback session ended. Start playback again.")
		return
	}
	if !s.authorize(p, sess.movieID) {
		contracts.WriteError(w, r, contracts.CodeNotFound, "Playback session not found.")
		return
	}
	f, info, err := s.open(sess)
	if err != nil {
		s.sourceError(w, r, err, sess.id)
		return
	}
	defer f.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	d := &delivery{cancel: cancel, rc: http.NewResponseController(w)}
	s.mu.Lock()
	if !s.liveLocked(sess, s.now()) {
		s.unlock()
		contracts.WriteError(w, r, contracts.CodePlaybackExpired, "Playback session ended. Start playback again.")
		return
	}
	sess.deliveries[d] = struct{}{}
	s.unlock()
	defer func() {
		s.mu.Lock()
		delete(sess.deliveries, d)
		s.unlock()
		_ = d.rc.SetWriteDeadline(time.Time{}) // don't leave a deadline on a kept-alive connection
	}()

	h := w.Header()
	h.Set("Content-Type", "video/mp4")
	h.Set("ETag", `"`+sess.revision+`"`)
	h.Set("Cache-Control", "no-store") // #20 point 4
	h.Set("X-Content-Type-Options", "nosniff")
	dw := &deliveryWriter{ResponseWriter: w, ctx: ctx, rc: d.rc, written: &s.written, alive: func() bool {
		s.mu.Lock()
		defer s.unlock()
		return s.liveLocked(sess, s.now())
	}}
	http.ServeContent(dw, r.WithContext(ctx), "", info.ModTime(), f)
}

// testHookAfterAlive, when set by a test, runs between a write's liveness
// check and its deadline, the window where a revoke can race the write.
var testHookAfterAlive func()

// deliveryWriter stops a response as soon as its session ends, and gives each
// write its own deadline so a stalled client can't hold a revoked stream open.
// It deliberately doesn't implement io.ReaderFrom, so every chunk goes through
// Write.
type deliveryWriter struct {
	http.ResponseWriter
	ctx     context.Context
	rc      writeDeadliner
	alive   func() bool
	written *atomic.Int64
}

func (d *deliveryWriter) Write(p []byte) (int, error) {
	if d.ctx.Err() != nil || !d.alive() {
		return 0, errRevoked
	}
	if testHookAfterAlive != nil {
		testHookAfterAlive()
	}
	_ = d.rc.SetWriteDeadline(time.Now().Add(writeStall))
	if d.ctx.Err() != nil { // a revoke landed after the check above
		return 0, errRevoked
	}
	n, err := d.ResponseWriter.Write(p)
	d.written.Add(int64(n))
	return n, err
}

// sourceError answers and logs a missing or changed source. sessionID is ""
// for a start.
func (s *Server) sourceError(w http.ResponseWriter, r *http.Request, err error, sessionID string) {
	code, msg := contracts.CodeSourceUnavailable, "The movie file is not available."
	if errors.Is(err, errSourceChanged) {
		code, msg = contracts.CodeSourceChanged, "The movie file changed. Start playback again."
	}
	obs.Log(r.Context(), s.log).Warn("playback.source_error", "code", code, "session_id", sessionID, "movie_id", s.src.Movie.ID)
	contracts.WriteError(w, r, code, msg)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil && !dec.More()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic("playback: encode response: " + err.Error()) // fixed types; cannot fail
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}
