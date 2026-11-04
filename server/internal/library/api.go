package library

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// API serves the catalogue operations of CTR-2 (listLibraries,
// getLibraryFacets, listItems, getItem) to an authenticated principal, with
// what it may see decided by access.
type API struct {
	st     *Store
	access Access
	log    *slog.Logger
}

// NewAPI serves st's catalogue, filtered by access. Unexpected errors are
// logged to log; a nil log discards.
func NewAPI(st *Store, access Access, log *slog.Logger) *API {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	st.log = log
	return &API{st: st, access: access, log: log}
}

// Handler serves the catalogue routes with full paths. Requests must carry a
// contracts.Principal (set by auth.Require); without one they get 401. Its
// catch-all answers every other path, for any method, with a JSON 404, so mount
// it only under /api/v1/ (the catalogue's /api/v1/libraries and /api/v1/items).
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/libraries", a.withGrant(a.libraries))
	mux.HandleFunc("GET /api/v1/libraries/{libraryId}/facets", a.withGrant(a.facets))
	mux.HandleFunc("GET /api/v1/items", a.withGrant(a.items))
	mux.HandleFunc("GET /api/v1/items/{itemId}", a.withGrant(a.item))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		contracts.WriteError(w, r, contracts.CodeNotFound, "Not found.")
	})
	return mux
}

func (a *API) withGrant(h func(http.ResponseWriter, *http.Request, Grant)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := contracts.PrincipalFrom(r.Context())
		if !ok {
			contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Sign in to continue.")
			return
		}
		h(w, r, a.access(p))
	}
}

func (a *API) libraries(w http.ResponseWriter, r *http.Request, g Grant) {
	q := newParams(r.URL.Query())
	cursor, limit := q.cursor(), q.limit()
	if err := q.done(); err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := a.st.Libraries(r.Context(), g, cursor, limit)
	a.reply(w, r, out, err)
}

func (a *API) facets(w http.ResponseWriter, r *http.Request, g Grant) {
	out, err := a.st.Facets(r.Context(), g, r.PathValue("libraryId"))
	a.reply(w, r, out, err)
}

func (a *API) items(w http.ResponseWriter, r *http.Request, g Grant) {
	q := newParams(r.URL.Query())
	var iq ItemQuery
	iq.LibraryID = q.text("libraryId")
	iq.ParentID = q.text("parentId")
	iq.Sort, iq.SortGiven = q.text("sort"), q.has("sort")
	iq.From = q.text("from")
	iq.Genre = q.text("genre")
	iq.Resolution = q.text("resolution")
	iq.AudioLanguage = q.text("audioLanguage")
	iq.YearFrom, iq.YearTo = q.integer("yearFrom"), q.integer("yearTo")
	iq.HDR, iq.Watched = q.boolean("hdr"), q.boolean("watched")
	iq.Cursor, iq.Limit = q.cursor(), q.limit()
	if err := q.done(); err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := a.st.Items(r.Context(), g, iq)
	a.reply(w, r, out, err)
}

func (a *API) item(w http.ResponseWriter, r *http.Request, g Grant) {
	out, err := a.st.Item(r.Context(), g, r.PathValue("itemId"))
	a.reply(w, r, out, err)
}

// reply writes v as the 200 response, or err as an APIError.
func (a *API) reply(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(b, '\n'))
}

// fail maps an error to its APIError. ErrBadRequest messages are fixed text
// (never input or paths), so they are sent; anything unexpected is a 500 with
// no detail, logged with the request ID.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrBadRequest):
		contracts.WriteError(w, r, contracts.CodeInvalidRequest, badRequestMessage(err))
	case errors.Is(err, ErrNotFound):
		contracts.WriteError(w, r, contracts.CodeNotFound, "Not found.")
	default:
		contracts.WriteError(w, r, contracts.CodeInternalError, "Internal error.")
		obs.Log(r.Context(), a.log).Error("library.api_error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	}
}

// badRequestMessage turns "library: bad request: limit must be 1 to 200" into
// "Limit must be 1 to 200."
func badRequestMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), ErrBadRequest.Error())
	msg = strings.TrimSpace(strings.TrimPrefix(msg, ":"))
	if msg == "" {
		return "The request isn't valid."
	}
	first, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(first)) + msg[n:] + "."
}

// params reads query parameters as the contract's schemas type them. The
// first malformed one is kept and reported by done; values absent from the
// query are the zero value or nil.
type params struct {
	v   url.Values
	err error
}

func newParams(v url.Values) *params { return &params{v: v} }

func (p *params) bad(msg string) {
	if p.err == nil {
		p.err = badRequest(msg)
	}
}

func (p *params) has(name string) bool { _, ok := p.v[name]; return ok }

// text returns a string parameter: absent is "", but a sent one must be one
// non-empty value (every string parameter has minLength 1 or a pattern or
// enum that excludes "").
func (p *params) text(name string) string {
	vs, ok := p.v[name]
	switch {
	case !ok:
		return ""
	case len(vs) != 1:
		p.bad(name + " is sent more than once")
	case vs[0] == "":
		p.bad(name + " is empty")
	default:
		return vs[0]
	}
	return ""
}

func (p *params) integer(name string) *int {
	if !p.has(name) {
		return nil
	}
	s := p.text(name)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || strings.HasPrefix(s, "+") {
		p.bad(name + " must be an integer")
		return nil
	}
	return &n
}

func (p *params) boolean(name string) *bool {
	if !p.has(name) {
		return nil
	}
	switch p.text(name) {
	case "true":
		return ptrTo(true)
	case "false":
		return ptrTo(false)
	case "":
		return nil
	}
	p.bad(name + " must be true or false")
	return nil
}

func (p *params) cursor() string {
	c := p.text("cursor")
	if len(c) > maxCursor {
		p.bad("cursor is too long")
	}
	return c
}

// limit is 0 when absent (the store's default page size); a sent limit must
// be 1 to 200, so an explicit limit=0 is refused.
func (p *params) limit() int {
	n := p.integer("limit")
	if n == nil {
		return 0
	}
	if *n < 1 || *n > maxLimit {
		p.bad("limit must be 1 to 200")
		return 0
	}
	return *n
}

// done reports the first malformed parameter.
func (p *params) done() error { return p.err }
