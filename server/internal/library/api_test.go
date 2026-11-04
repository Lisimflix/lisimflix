package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/obs"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// Test principals: the admin sees everything; the viewer only the TV library.
var (
	adminP  = &contracts.Principal{AccountID: "acct-admin", ProfileID: "prof-admin", AuthSessionID: "as-admin"}
	viewerP = &contracts.Principal{AccountID: "acct-viewer", ProfileID: "prof-viewer", AuthSessionID: "as-viewer"}
)

// aenv is a scanned catalogue served by the API: a movies library (Film with
// two versions) and a TV library.
type aenv struct {
	*benv
	api       *API
	h         http.Handler
	movies    Library
	tv        Library
	logOutput *bytes.Buffer
}

func newAPIEnv(t *testing.T) *aenv {
	t.Helper()
	e := newBrowse(t)
	movies := filmLibrary(e)
	tv := e.library("TV", "tv", []string{"TV"},
		"TV/Show (2019)/Season 01/Show - S01E01.mkv", "TV/Show (2019)/Season 01/Show - S01E02.mkv")
	var buf bytes.Buffer
	api := NewAPI(e.s, func(p contracts.Principal) Grant {
		switch p.AccountID {
		case adminP.AccountID:
			return Grant{All: true, Admin: true}
		case viewerP.AccountID:
			return Grant{LibraryIDs: []string{tv.ID}}
		}
		return Grant{}
	}, slog.New(slog.NewTextHandler(&buf, nil)))
	return &aenv{benv: e, api: api, h: api.Handler(), movies: movies, tv: tv, logOutput: &buf}
}

// get serves one request as app's observe middleware would: a request ID in
// the context and the X-Request-Id header, which WriteError reuses.
func (a *aenv) get(p *contracts.Principal, target string) *httptest.ResponseRecorder {
	a.t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	id := obs.NewRequestID()
	ctx := obs.WithRequestID(r.Context(), id)
	if p != nil {
		ctx = contracts.WithPrincipal(ctx, *p)
	}
	w := httptest.NewRecorder()
	w.Header().Set("X-Request-Id", id)
	a.h.ServeHTTP(w, r.WithContext(ctx))
	return w
}

// roundTrip decodes body strictly into v and checks that encoding v again
// gives the same bytes (the contracts package's round-trip rule).
func roundTrip(t *testing.T, body []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if dec.More() {
		t.Fatalf("trailing data: %s", body)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out, bytes.TrimSuffix(body, []byte("\n")); !bytes.Equal(got, want) {
		t.Fatalf("round trip changed the body\n got: %s\nwant: %s", got, want)
	}
}

func TestAPIShapesMatchContract(t *testing.T) {
	a := newAPIEnv(t)
	film, show := a.itemID("Film"), a.itemID("Show")
	season := a.itemID("Season 1")
	ok := func(p *contracts.Principal, target string, v any) {
		t.Helper()
		w := a.get(p, target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", target, w.Code, w.Body)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("GET %s: Content-Type %q", target, ct)
		}
		roundTrip(t, w.Body.Bytes(), v)
	}

	var libs contracts.LibraryList
	ok(adminP, "/api/v1/libraries", &libs)
	if len(libs.Items) != 2 || libs.Items[0].Name != "Movies" || libs.Items[1].Name != "TV" {
		t.Fatalf("libraries %+v", libs)
	}
	ok(viewerP, "/api/v1/libraries", &libs)
	if len(libs.Items) != 1 || libs.Items[0].ID != a.tv.ID {
		t.Fatalf("viewer libraries %+v", libs)
	}
	var p1, p2 contracts.LibraryList
	ok(adminP, "/api/v1/libraries?limit=1", &p1)
	if p1.NextCursor == nil {
		t.Fatalf("libraries page 1 %+v", p1)
	}
	ok(adminP, "/api/v1/libraries?limit=1&cursor="+url.QueryEscape(*p1.NextCursor), &p2)
	if len(p2.Items) != 1 || p2.Items[0].Name != "TV" || p2.NextCursor != nil {
		t.Fatalf("libraries page 2 %+v", p2)
	}

	var facets contracts.LibraryFacets
	ok(adminP, "/api/v1/libraries/"+a.movies.ID+"/facets", &facets)
	if !reflect.DeepEqual(facets.Resolutions, []string{"4k", "1080p"}) || !facets.HDR || !facets.SDR {
		t.Fatalf("facets %+v", facets)
	}

	var page contracts.ItemPage
	ok(adminP, "/api/v1/items?libraryId="+a.movies.ID, &page)
	if len(page.Items) != 1 || page.Items[0].ID != film || page.Total == nil || *page.Total != 1 {
		t.Fatalf("movies %+v", page)
	}
	// Every listItems parameter reaches the store.
	q := url.Values{"libraryId": {a.movies.ID}, "sort": {"title"}, "from": {"F"}, "yearFrom": {"2000"}, "yearTo": {"2002"},
		"resolution": {"4k"}, "hdr": {"true"}, "audioLanguage": {"eng"}, "limit": {"200"}}
	ok(adminP, "/api/v1/items?"+q.Encode(), &page)
	if len(page.Items) != 1 || page.Items[0].ID != film {
		t.Fatalf("filtered %+v", page)
	}
	for _, miss := range []url.Values{{"from": {"G"}}, {"yearFrom": {"2002"}}, {"resolution": {"720p"}}, {"audioLanguage": {"jpn"}}, {"genre": {"Drama"}}} {
		mq := url.Values{"libraryId": {a.movies.ID}}
		for k, v := range miss {
			mq[k] = v
		}
		ok(adminP, "/api/v1/items?"+mq.Encode(), &page)
		if len(page.Items) != 0 || page.Total == nil || *page.Total != 0 {
			t.Fatalf("%v: %+v", miss, page)
		}
	}
	ok(adminP, "/api/v1/items?libraryId="+a.movies.ID+"&sort=year", &page)
	ok(adminP, "/api/v1/items?libraryId="+a.movies.ID+"&sort=added&hdr=false", &page)
	if len(page.Items) != 1 {
		t.Fatalf("sdr %+v", page)
	}
	ok(viewerP, "/api/v1/items?parentId="+show, &page)
	if len(page.Items) != 1 || page.Items[0].ID != season {
		t.Fatalf("seasons %+v", page)
	}
	ok(viewerP, "/api/v1/items?parentId="+season+"&limit=1", &page)
	if len(page.Items) != 1 || page.NextCursor == nil || page.Total == nil || *page.Total != 2 {
		t.Fatalf("episodes page 1 %+v", page)
	}
	ok(viewerP, "/api/v1/items?parentId="+season+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor), &page)
	if len(page.Items) != 1 || page.Items[0].Title != "Episode 2" || page.NextCursor != nil || page.Total != nil {
		t.Fatalf("episodes page 2 %+v", page)
	}

	var d contracts.ItemDetail
	ok(adminP, "/api/v1/items/"+film, &d)
	if len(d.Versions) != 2 || d.Versions[0].FileName == nil {
		t.Fatalf("admin film %+v", d)
	}
	ok(viewerP, "/api/v1/items/"+show, &d)
	if len(d.Versions) != 0 || d.Item.ID != show {
		t.Fatalf("show %+v", d)
	}
	ok(viewerP, "/api/v1/items/"+a.itemID("Episode 1"), &d)
	if len(d.Versions) != 1 || d.Versions[0].FileName != nil {
		t.Fatalf("viewer episode %+v", d)
	}
}

// apiError decodes an error body strictly.
func apiError(t *testing.T, w *httptest.ResponseRecorder) contracts.APIError {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q: %s", ct, w.Body)
	}
	var e contracts.APIError
	roundTrip(t, w.Body.Bytes(), &e)
	if e.RequestID == "" || e.RequestID != w.Header().Get("X-Request-Id") {
		t.Fatalf("requestId %q, header %q", e.RequestID, w.Header().Get("X-Request-Id"))
	}
	return e
}

func TestAPIErrors(t *testing.T) {
	a := newAPIEnv(t)
	film, show := a.itemID("Film"), a.itemID("Show")
	lib := "/api/v1/items?libraryId=" + a.movies.ID

	for _, target := range []string{"/api/v1/libraries", "/api/v1/libraries/" + a.movies.ID + "/facets", lib, "/api/v1/items/" + film} {
		w := a.get(nil, target)
		if e := apiError(t, w); w.Code != http.StatusUnauthorized || e.Code != contracts.CodeUnauthenticated {
			t.Errorf("no principal %s: %d %+v", target, w.Code, e)
		}
	}

	for _, target := range []string{
		"/api/v1/libraries?limit=0", "/api/v1/libraries?limit=201", "/api/v1/libraries?limit=abc", "/api/v1/libraries?limit=",
		"/api/v1/libraries?limit=1&limit=2", "/api/v1/libraries?limit=1.5", "/api/v1/libraries?cursor=junk", "/api/v1/libraries?cursor=",
		lib + "&limit=0", lib + "&limit=-1", lib + "&limit=99999999999999999999", lib + "&yearFrom=abc", lib + "&yearTo=",
		lib + "&from=AB", lib + "&from=a", lib + "&from=", lib + "&resolution=8k", lib + "&hdr=yes", lib + "&hdr=1",
		lib + "&watched=true", lib + "&sort=bogus", lib + "&sort=", lib + "&audioLanguage=german", lib + "&genre=",
		lib + "&yearFrom=2005&yearTo=2001", lib + "&cursor=junk", lib + "&sort=added&from=A",
		"/api/v1/items", "/api/v1/items?libraryId=", "/api/v1/items?parentId=" + show + "&libraryId=" + a.tv.ID,
		"/api/v1/items?parentId=" + show + "&sort=title", "/api/v1/items?parentId=" + show + "&genre=Drama",
		"/api/v1/items?parentId=" + film,
	} {
		w := a.get(adminP, target)
		if e := apiError(t, w); w.Code != http.StatusBadRequest || e.Code != contracts.CodeInvalidRequest || e.Message == "" ||
			strings.Contains(e.Message, "library:") {
			t.Errorf("%s: %d %+v", target, w.Code, e)
		}
	}

	// Unknown and hidden are the same 404 apart from requestId.
	body := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		e := apiError(t, w)
		if w.Code != http.StatusNotFound || e.Code != contracts.CodeNotFound {
			t.Fatalf("%d %+v", w.Code, e)
		}
		return strings.Replace(w.Body.String(), e.RequestID, "REQ", 1)
	}
	for name, pair := range map[string][2]string{
		"item":     {"/api/v1/items/item-nope", "/api/v1/items/" + film},
		"facets":   {"/api/v1/libraries/lib-nope/facets", "/api/v1/libraries/" + a.movies.ID + "/facets"},
		"library":  {"/api/v1/items?libraryId=lib-nope", lib},
		"parentId": {"/api/v1/items?parentId=item-nope", "/api/v1/items?parentId=" + film},
	} {
		unknown, hidden := body(a.get(viewerP, pair[0])), body(a.get(viewerP, pair[1]))
		if unknown != hidden {
			t.Errorf("%s: unknown %s, hidden %s", name, unknown, hidden)
		}
	}
	for _, target := range []string{"/api/v1/items/" + film + "/extra", "/api/v1/libraries/" + a.movies.ID, "/api/v1/nope"} {
		body(a.get(adminP, target))
	}

	// Anything else is a 500 that names no detail and is logged with its request ID.
	if err := a.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	w := a.get(adminP, "/api/v1/items/"+film)
	e := apiError(t, w)
	if w.Code != http.StatusInternalServerError || e.Code != contracts.CodeInternalError || e.Message != "Internal error." {
		t.Fatalf("closed db: %d %+v", w.Code, e)
	}
	if logged := a.logOutput.String(); !strings.Contains(logged, "request_id="+e.RequestID) || !strings.Contains(logged, "library.api_error") {
		t.Fatalf("log %q", logged)
	}
}

// benchProber gives each benchmark file streams by its name's number.
type benchProber struct{}

func (benchProber) Probe(_ context.Context, _ *os.Root, rel string) (probe.Result, error) {
	n := len(rel)
	w, h, hdr := 1920, 1080, probe.SDR
	switch n % 3 {
	case 0:
		w, h, hdr = 3840, 2160, probe.HDR10
	case 1:
		w, h = 1280, 720
	}
	lang := []string{"eng", "jpn", "fre"}[n%3]
	return probe.Result{Container: "matroska,webm", DurationMs: int64(1000 + n),
		Video:     []probe.VideoStream{{Index: 0, Codec: "hevc", Width: w, Height: h, HDR: hdr, Default: true}},
		Audio:     []probe.AudioStream{{Index: 1, Codec: "aac", Channels: 2, Language: lang, Default: true}},
		Subtitles: []probe.SubtitleStream{{Index: 2, Codec: "subrip", Language: "eng"}}}, nil
}

// BenchmarkAPIRoutes measures each catalogue route over 5,000 movies (2,000
// with two files) and 200 shows × 3 seasons × 10 episodes. It is a benchmark,
// not a timing test: Task 8 measures the PERF-05 target.
func BenchmarkAPIRoutes(b *testing.B) {
	e := newBrowse(b)
	var files []string
	for i := range 5000 {
		dir := fmt.Sprintf("Movies/%c Movie %04d (%d)", 'A'+i%26, i, 1950+i%70)
		base := filepath.Base(dir)
		files = append(files, dir+"/"+base+".mkv")
		if i < 2000 {
			files = append(files, dir+"/"+base+" - Director's Cut.mkv")
		}
	}
	for s := range 200 {
		for se := 1; se <= 3; se++ {
			for ep := 1; ep <= 10; ep++ {
				files = append(files, fmt.Sprintf("TV/%c Show %03d (2010)/Season %02d/%c Show %03d - S%02dE%02d.mkv", 'A'+s%26, s, se, 'A'+s%26, s, se, ep))
			}
		}
	}
	for _, f := range files {
		e.m.write(f, 10)
	}
	ctx := context.Background()
	movies, err := e.s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	if err != nil {
		b.Fatal(err)
	}
	tv, err := e.s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	if err != nil {
		b.Fatal(err)
	}
	for _, r := range []string{movies.Roots[0].ID, tv.Roots[0].ID} {
		e.scan(r)
		e.probe(r, benchProber{})
	}
	var some []string
	rows, err := e.s.db.Query(`SELECT id FROM library_items WHERE library_id = ? ORDER BY id LIMIT 500`, movies.ID)
	if err != nil {
		b.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			b.Fatal(err)
		}
		some = append(some, id)
	}
	rows.Close()
	for _, id := range some {
		if err := e.s.SetDisplay(ctx, id, Display{Genres: []string{"Drama"}}); err != nil {
			b.Fatal(err)
		}
	}
	api := NewAPI(e.s, func(contracts.Principal) Grant { return Grant{All: true, Admin: true} }, nil)
	h := api.Handler()
	get := func(tb testing.TB, target string) []byte {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r = r.WithContext(contracts.WithPrincipal(r.Context(), *adminP))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			tb.Fatalf("GET %s: %d %s", target, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	// The last page by title, reached by paging the whole library.
	lastPage := "/api/v1/items?libraryId=" + movies.ID + "&limit=200"
	for {
		var p contracts.ItemPage
		if err := json.Unmarshal(get(b, lastPage), &p); err != nil {
			b.Fatal(err)
		}
		if p.NextCursor == nil {
			break
		}
		lastPage = "/api/v1/items?libraryId=" + movies.ID + "&limit=200&cursor=" + url.QueryEscape(*p.NextCursor)
	}
	var twoFiles, aShow, aSeason string
	if err := e.s.db.QueryRow(`SELECT item_id FROM library_versions GROUP BY item_id HAVING COUNT(*) = 2 LIMIT 1`).Scan(&twoFiles); err != nil {
		b.Fatal(err)
	}
	if err := e.s.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'show' LIMIT 1`).Scan(&aShow); err != nil {
		b.Fatal(err)
	}
	if err := e.s.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'season' LIMIT 1`).Scan(&aSeason); err != nil {
		b.Fatal(err)
	}
	m, tvItems := "/api/v1/items?libraryId="+movies.ID, "/api/v1/items?libraryId="+tv.ID
	for _, c := range []struct{ name, target string }{
		{"libraries", "/api/v1/libraries"},
		{"facets/movies", "/api/v1/libraries/" + movies.ID + "/facets"},
		{"facets/tv", "/api/v1/libraries/" + tv.ID + "/facets"},
		{"items/title", m},
		{"items/from=M", m + "&from=M"},
		{"items/last", lastPage},
		{"items/added", m + "&sort=added"},
		{"items/year", m + "&sort=year"},
		{"items/genre", m + "&genre=Drama"},
		{"items/years", m + "&yearFrom=1980&yearTo=1990"},
		{"items/resolution", m + "&resolution=4k"},
		{"items/hdr", m + "&hdr=true"},
		{"items/sdr", m + "&hdr=false"},
		{"items/audio", m + "&audioLanguage=jpn"},
		{"items/tv-title", tvItems},
		{"items/tv-resolution", tvItems + "&resolution=720p"},
		{"items/seasons", "/api/v1/items?parentId=" + aShow},
		{"items/episodes", "/api/v1/items?parentId=" + aSeason},
		{"getItem/movie", "/api/v1/items/" + twoFiles},
		{"getItem/show", "/api/v1/items/" + aShow},
	} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				get(b, c.target)
			}
		})
	}
}
