package userdata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// Session lookup errors a SessionLookup returns; serve translates playback's.
var (
	ErrSessionNotFound = errors.New("userdata: playback session not found")
	ErrSessionEnded    = errors.New("userdata: playback session ended")
	ErrSourceChanged   = errors.New("userdata: playback source changed")
)

// SessionRef is what progress needs from a live playback session.
type SessionRef struct{ ID, ItemID, VersionID, SourceRevision string }

// SessionLookup finds the caller's live playback session.
type SessionLookup interface {
	Session(p contracts.Principal, id string) (SessionRef, error)
}

// ItemFacts reports what the playback session cannot know. versionID and
// sourceRevision "" ask about the item alone (setWatched, hide); progress
// passes the session's, so a probe duration is used only for that file
// revision. ok=false means "not a catalogue movie or episode" (404).
type ItemFacts func(ctx context.Context, p contracts.Principal, itemID, versionID, sourceRevision string) (durationMs int64, noAutoWatched, ok bool, err error)

// maxBody bounds every request body.
const maxBody = 4 << 10

// API serves the progress, watched and hide operations (CTR-2, DATA-1b).
type API struct {
	store    *Store
	sessions SessionLookup
	facts    ItemFacts
	log      *slog.Logger
}

// NewAPI returns the user-data API. Store errors are logged to log; nil discards.
func NewAPI(store *Store, sessions SessionLookup, facts ItemFacts, log *slog.Logger) *API {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &API{store: store, sessions: sessions, facts: facts, log: log}
}

// Handler serves the routes by full path; requests carry a principal.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/playback/sessions/{sessionId}/progress", a.withPrincipal(a.progress))
	mux.HandleFunc("PUT /api/v1/items/{itemId}/watched", a.withPrincipal(a.watched))
	mux.HandleFunc("PUT /api/v1/items/{itemId}/continue-watching-hidden", a.withPrincipal(a.hide))
	return mux
}

func (a *API) withPrincipal(h func(http.ResponseWriter, *http.Request, contracts.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := contracts.PrincipalFrom(r.Context())
		if !ok {
			contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Sign in to continue.")
			return
		}
		h(w, r, p)
	}
}

func (a *API) progress(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	var e contracts.ProgressEvent
	if !decodeStrict(w, r, &e, "fromPositionMs", "sequence", "kind", "positionMs", "fromPositionMs") {
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send sequence, kind, positionMs and fromPositionMs.")
		return
	}
	ref, err := a.sessions.Session(p, r.PathValue("sessionId"))
	switch {
	case errors.Is(err, ErrSessionNotFound):
		contracts.WriteError(w, r, contracts.CodeNotFound, "Playback session not found.")
		return
	case errors.Is(err, ErrSessionEnded):
		contracts.WriteError(w, r, contracts.CodePlaybackExpired, "Playback session ended. Start playback again.")
		return
	case errors.Is(err, ErrSourceChanged):
		contracts.WriteError(w, r, contracts.CodeSourceChanged, "The file changed since playback started. Start playback again.")
		return
	case err != nil:
		a.fail(w, r, err)
		return
	}
	dur, noAuto, ok, err := a.facts(r.Context(), p, ref.ItemID, ref.VersionID, ref.SourceRevision)
	if err == nil && dur < 0 {
		err = errors.New("userdata: item facts gave a negative duration")
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !ok { // an extra or an unknown item: nothing is stored (DATA-1b Decision 3)
		contracts.WriteError(w, r, contracts.CodeNotFound, "Item not found.")
		return
	}
	res, err := a.store.Apply(r.Context(), p.ProfileID, Session{ID: ref.ID, ItemID: ref.ItemID, VersionID: ref.VersionID,
		SourceRevision: ref.SourceRevision, DurationMs: dur, NoAutoWatched: noAuto}, e)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, res)
}

func (a *API) watched(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	var req contracts.WatchedRequest
	if !decodeStrict(w, r, &req, "", "watched", "expectedRevision") || req.ExpectedRevision < 0 {
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send watched and a nonnegative expectedRevision.")
		return
	}
	a.mark(w, r, p, func(itemID string) (contracts.UserItemState, error) {
		return a.store.SetWatched(r.Context(), p.ProfileID, itemID, req.Watched, req.ExpectedRevision)
	})
}

func (a *API) hide(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	var req contracts.HideRequest
	if !decodeStrict(w, r, &req, "", "expectedRevision") || req.ExpectedRevision < 0 {
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send a nonnegative expectedRevision.")
		return
	}
	a.mark(w, r, p, func(itemID string) (contracts.UserItemState, error) {
		return a.store.Hide(r.Context(), p.ProfileID, itemID, req.ExpectedRevision)
	})
}

// mark checks the item is a catalogue movie or episode, then runs write.
func (a *API) mark(w http.ResponseWriter, r *http.Request, p contracts.Principal, write func(itemID string) (contracts.UserItemState, error)) {
	itemID := r.PathValue("itemId")
	_, _, ok, err := a.facts(r.Context(), p, itemID, "", "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !ok { // checked first, so an unknown item never gets a state row
		contracts.WriteError(w, r, contracts.CodeNotFound, "Item not found.")
		return
	}
	st, err := write(itemID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, st)
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Invalid request.")
	case errors.Is(err, ErrRevisionConflict):
		contracts.WriteError(w, r, contracts.CodeRevisionConflict, "This item changed. Reload and try again.")
	default:
		contracts.WriteError(w, r, contracts.CodeInternalError, "Internal error.")
		obs.Log(r.Context(), a.log).Error("userdata.error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	}
}

// decodeStrict reads into v a body of at most maxBody with exactly the
// required keys (every schema property is required, so any other key, even a
// case variant Go would match, is refused) and no null except in nullable.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any, nullable string, required ...string) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return false
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(body, &keys) != nil {
		return false
	}
	if len(keys) != len(required) {
		return false
	}
	for _, k := range required {
		raw, ok := keys[k]
		if !ok || (k != nullable && string(bytes.TrimSpace(raw)) == "null") {
			return false
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
