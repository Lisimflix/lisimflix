package userdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

var (
	alice = contracts.Principal{AccountID: "acct-a", ProfileID: "prof-a", AuthSessionID: "as-a"}
	bob   = contracts.Principal{AccountID: "acct-b", ProfileID: "prof-b", AuthSessionID: "as-b"}
)

// fakeSessions answers for sessions owned by one principal; err, when set, wins.
type fakeSessions struct {
	owner contracts.Principal
	refs  map[string]SessionRef
	err   error
}

func (f *fakeSessions) Session(p contracts.Principal, id string) (SessionRef, error) {
	if f.err != nil {
		return SessionRef{}, f.err
	}
	ref, ok := f.refs[id]
	if !ok || p != f.owner {
		return SessionRef{}, ErrSessionNotFound
	}
	return ref, nil
}

type apiEnv struct {
	*env
	sessions *fakeSessions
	known    map[string]bool // catalogue items ItemFacts knows
	asked    []string        // "item|version|revision" per ItemFacts call
	h        http.Handler
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	a := &apiEnv{env: newEnv(t), known: map[string]bool{"item": true, "other": true}}
	a.sessions = &fakeSessions{owner: alice, refs: map[string]SessionRef{
		"ps-1": {ID: "ps-1", ItemID: "item", VersionID: "v1", SourceRevision: r1},
		"ps-2": {ID: "ps-2", ItemID: "item", VersionID: "v1", SourceRevision: r1},
		"ps-x": {ID: "ps-x", ItemID: "extra", VersionID: "vx", SourceRevision: r1},
	}}
	facts := func(_ context.Context, _ contracts.Principal, itemID, versionID, rev string) (int64, bool, bool, error) {
		a.asked = append(a.asked, itemID+"|"+versionID+"|"+rev)
		return 100_000, false, a.known[itemID], nil
	}
	a.h = NewAPI(a.st, a.sessions, facts, nil).Handler()
	return a
}

func (a *apiEnv) do(p *contracts.Principal, method, path, body string) (int, []byte) {
	a.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		r = r.WithContext(contracts.WithPrincipal(r.Context(), *p))
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func (a *apiEnv) progress(p contracts.Principal, session, body string) (int, []byte) {
	return a.do(&p, "POST", "/api/v1/playback/sessions/"+session+"/progress", body)
}

func tickBody(seq, pos int64) string {
	b, _ := json.Marshal(map[string]any{"sequence": seq, "kind": "tick", "positionMs": pos, "fromPositionMs": nil})
	return string(b)
}

func decodeInto[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

func errorCode(t *testing.T, b []byte) string {
	t.Helper()
	return decodeInto[struct{ Code string }](t, b).Code
}

func TestReportProgressAcceptsThenDuplicates(t *testing.T) {
	a := newAPIEnv(t)
	code, b := a.progress(alice, "ps-1", tickBody(1, 10_000))
	first := decodeInto[contracts.ProgressResult](t, b)
	if code != 200 || !first.Accepted || first.State.Resume == nil || first.State.Resume.PositionMs != 10_000 {
		t.Fatalf("first = %d %s", code, b)
	}
	code, b = a.progress(alice, "ps-1", tickBody(1, 10_000))
	dup := decodeInto[contracts.ProgressResult](t, b)
	if code != 200 || dup.Accepted || dup.Reason == nil || *dup.Reason != "duplicate" || dup.State.Resume.PositionMs != 10_000 {
		t.Fatalf("duplicate = %d %s", code, b)
	}
}

func TestReportProgressStale(t *testing.T) {
	a := newAPIEnv(t)
	a.progress(alice, "ps-1", tickBody(5, 50_000))
	code, b := a.progress(alice, "ps-1", tickBody(3, 30_000))
	r := decodeInto[contracts.ProgressResult](t, b)
	if code != 200 || r.Accepted || *r.Reason != "stale" || r.State.Resume.PositionMs != 50_000 {
		t.Fatalf("stale = %d %s", code, b)
	}
}

func TestReportProgressBodyRules(t *testing.T) {
	a := newAPIEnv(t)
	for name, body := range map[string]string{
		"missing sequence":       `{"kind":"tick","positionMs":1,"fromPositionMs":null}`,
		"missing kind":           `{"sequence":1,"positionMs":1,"fromPositionMs":null}`,
		"missing positionMs":     `{"sequence":1,"kind":"tick","fromPositionMs":null}`,
		"missing fromPositionMs": `{"sequence":1,"kind":"tick","positionMs":1}`,
		"seek without from":      `{"sequence":1,"kind":"seek","positionMs":1,"fromPositionMs":null}`,
		"tick with from":         `{"sequence":1,"kind":"tick","positionMs":1,"fromPositionMs":0}`,
		"negative sequence":      `{"sequence":-1,"kind":"tick","positionMs":1,"fromPositionMs":null}`,
		"unknown field":          `{"sequence":1,"kind":"tick","positionMs":1,"fromPositionMs":null,"extra":1}`,
		"unknown kind":           `{"sequence":1,"kind":"rewind","positionMs":1,"fromPositionMs":null}`,
		"not json":               `tick`,
		"over 4 KiB":             `{"sequence":1,"kind":"tick","positionMs":1,"fromPositionMs":null` + strings.Repeat(" ", 4096) + `}`,
		"null sequence":          `{"sequence":null,"kind":"tick","positionMs":1,"fromPositionMs":null}`,
		"null positionMs":        `{"sequence":1,"kind":"tick","positionMs":null,"fromPositionMs":null}`,
		"null kind":              `{"sequence":1,"kind":null,"positionMs":1,"fromPositionMs":null}`,
		"case-variant key":       `{"sequence":1,"kind":"tick","positionMs":5,"fromPositionMs":null,"PositionMs":0}`,
		"trailing data":          `{"sequence":1,"kind":"tick","positionMs":1,"fromPositionMs":null} {}`,
	} {
		if code, b := a.progress(alice, "ps-1", body); code != 400 || errorCode(t, b) != contracts.CodeInvalidRequest {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM userdata_events`); n != 0 {
		t.Fatalf("%d events written", n)
	}
}

func TestReportProgressSessionErrors(t *testing.T) {
	a := newAPIEnv(t)
	if code, b := a.progress(bob, "ps-1", tickBody(1, 10_000)); code != 404 || errorCode(t, b) != contracts.CodeNotFound {
		t.Errorf("other principal: %d %s", code, b)
	}
	for err, want := range map[error]struct {
		status int
		code   string
	}{
		ErrSessionEnded:  {410, contracts.CodePlaybackExpired},
		ErrSourceChanged: {409, contracts.CodeSourceChanged},
	} {
		a.sessions.err = err
		if code, b := a.progress(alice, "ps-1", tickBody(1, 10_000)); code != want.status || errorCode(t, b) != want.code {
			t.Errorf("%v: %d %s", err, code, b)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM userdata_events`); n != 0 {
		t.Fatalf("%d events written", n)
	}
}

func TestReportProgressNotACatalogueItem(t *testing.T) {
	a := newAPIEnv(t)
	if code, b := a.progress(alice, "ps-x", tickBody(1, 10_000)); code != 404 || errorCode(t, b) != contracts.CodeNotFound {
		t.Fatalf("extra: %d %s", code, b)
	}
	if n := a.count(`SELECT (SELECT COUNT(*) FROM userdata_events) + (SELECT COUNT(*) FROM userdata_state)`); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

func TestSetWatchedRevisionConflict(t *testing.T) {
	a := newAPIEnv(t)
	a.progress(alice, "ps-1", tickBody(1, 40_000))
	code, b := a.do(&alice, "PUT", "/api/v1/items/item/watched", `{"watched":true,"expectedRevision":0}`)
	st := decodeInto[contracts.UserItemState](t, b)
	if code != 200 || st.Revision != 1 || st.Watched == nil || st.Watched.Origin != contracts.WatchedManual {
		t.Fatalf("mark = %d %s", code, b)
	}
	if code, b := a.do(&alice, "PUT", "/api/v1/items/item/watched", `{"watched":false,"expectedRevision":0}`); code != 409 || errorCode(t, b) != contracts.CodeRevisionConflict {
		t.Fatalf("stale revision: %d %s", code, b)
	}
	code, b = a.do(&alice, "PUT", "/api/v1/items/item/watched", `{"watched":false,"expectedRevision":1}`)
	st = decodeInto[contracts.UserItemState](t, b)
	if code != 200 || st.Revision != 2 || st.Watched != nil || st.Resume != nil {
		t.Fatalf("unwatch = %d %s", code, b)
	}
	for name, body := range map[string]string{
		"missing revision":  `{"watched":true}`,
		"missing watched":   `{"expectedRevision":2}`,
		"negative revision": `{"watched":true,"expectedRevision":-1}`,
		"unknown field":     `{"watched":true,"expectedRevision":2,"x":1}`,
		"null watched":      `{"watched":null,"expectedRevision":2}`,
		"null revision":     `{"watched":false,"expectedRevision":null}`,
		"case-variant key":  `{"watched":true,"expectedRevision":2,"WATCHED":false}`,
	} {
		if code, b := a.do(&alice, "PUT", "/api/v1/items/item/watched", body); code != 400 || errorCode(t, b) != contracts.CodeInvalidRequest {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
	if st, _ := getState(a.st, context.Background(), alice.ProfileID, "item"); st.Revision != 2 {
		t.Fatalf("a rejected body changed the item: %+v", st)
	}
	if code, b := a.do(&alice, "PUT", "/api/v1/items/item/continue-watching-hidden", `{"expectedRevision":null}`); code != 400 {
		t.Errorf("hide null revision: %d %s", code, b)
	}
}

func TestSetWatchedAndHideUnknownItem(t *testing.T) {
	a := newAPIEnv(t)
	if code, b := a.do(&alice, "PUT", "/api/v1/items/nope/continue-watching-hidden", `{"expectedRevision":0}`); code != 404 || errorCode(t, b) != contracts.CodeNotFound {
		t.Errorf("hide: %d %s", code, b)
	}
	if code, b := a.do(&alice, "PUT", "/api/v1/items/nope/watched", `{"watched":true,"expectedRevision":0}`); code != 404 || errorCode(t, b) != contracts.CodeNotFound {
		t.Errorf("watched: %d %s", code, b)
	}
	if n := a.count(`SELECT COUNT(*) FROM userdata_state`); n != 0 {
		t.Fatalf("%d state rows", n)
	}
}

func TestHideAndPlayAgainRestores(t *testing.T) {
	a := newAPIEnv(t)
	a.progress(alice, "ps-1", tickBody(1, 40_000))
	code, b := a.do(&alice, "PUT", "/api/v1/items/item/continue-watching-hidden", `{"expectedRevision":0}`)
	if st := decodeInto[contracts.UserItemState](t, b); code != 200 || !st.HiddenFromContinueWatching || st.Revision != 1 {
		t.Fatalf("hide = %d %s", code, b)
	}
	_, b = a.progress(alice, "ps-1", tickBody(2, 50_000))
	if r := decodeInto[contracts.ProgressResult](t, b); !r.Accepted || !r.State.HiddenFromContinueWatching {
		t.Fatalf("open session unhid it: %s", b)
	}
	_, b = a.progress(alice, "ps-2", tickBody(1, 60_000))
	if r := decodeInto[contracts.ProgressResult](t, b); !r.Accepted || r.State.HiddenFromContinueWatching {
		t.Fatalf("new session left it hidden: %s", b)
	}
}

func TestStoreErrorsNeverLeak(t *testing.T) {
	a := newAPIEnv(t)
	a.db.Close()
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/playback/sessions/ps-1/progress", tickBody(1, 10_000)},
		{"PUT", "/api/v1/items/item/watched", `{"watched":true,"expectedRevision":0}`},
		{"PUT", "/api/v1/items/item/continue-watching-hidden", `{"expectedRevision":0}`},
	} {
		code, b := a.do(&alice, c.method, c.path, c.body)
		msg := decodeInto[struct{ Code, Message string }](t, b)
		if code != 500 || msg.Code != contracts.CodeInternalError || msg.Message != "Internal error." {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, b)
		}
	}
}

func TestProfilesAreIsolated(t *testing.T) {
	a := newAPIEnv(t)
	a.progress(alice, "ps-1", tickBody(1, 40_000))
	code, b := a.do(&bob, "PUT", "/api/v1/items/item/watched", `{"watched":true,"expectedRevision":0}`)
	if st := decodeInto[contracts.UserItemState](t, b); code != 200 || st.Resume != nil || st.Revision != 1 {
		t.Fatalf("bob = %d %s", code, b)
	}
	got, err := getState(a.st, context.Background(), alice.ProfileID, "item")
	if err != nil || got.Watched != nil || got.Revision != 0 || got.Resume == nil {
		t.Fatalf("alice changed: %+v %v", got, err)
	}
}

func TestRoutesNeedAPrincipal(t *testing.T) {
	a := newAPIEnv(t)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/playback/sessions/ps-1/progress", tickBody(1, 10_000)},
		{"PUT", "/api/v1/items/item/watched", `{"watched":true,"expectedRevision":0}`},
		{"PUT", "/api/v1/items/item/continue-watching-hidden", `{"expectedRevision":0}`},
	} {
		if code, b := a.do(nil, c.method, c.path, c.body); code != 401 {
			t.Errorf("no principal %s: %d %s", c.path, code, b)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM userdata_state`); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

// Progress asks ItemFacts about the session's version and file revision; marks ask about the item alone (#177 note 1).
func TestItemFactsArguments(t *testing.T) {
	a := newAPIEnv(t)
	a.progress(alice, "ps-1", tickBody(1, 10_000))
	a.do(&alice, "PUT", "/api/v1/items/item/watched", `{"watched":true,"expectedRevision":0}`)
	a.do(&alice, "PUT", "/api/v1/items/item/continue-watching-hidden", `{"expectedRevision":1}`)
	want := []string{"item|v1|" + r1, "item||", "item||"}
	if strings.Join(a.asked, ",") != strings.Join(want, ",") {
		t.Fatalf("ItemFacts asked %v, want %v", a.asked, want)
	}
}

func TestHideTouchesOnlyTheCallersRow(t *testing.T) {
	a := newAPIEnv(t)
	a.progress(alice, "ps-1", tickBody(1, 40_000))
	if code, b := a.do(&bob, "PUT", "/api/v1/items/item/continue-watching-hidden", `{"expectedRevision":0}`); code != 200 {
		t.Fatalf("bob hide = %d %s", code, b)
	}
	if got, _ := getState(a.st, context.Background(), alice.ProfileID, "item"); got.HiddenFromContinueWatching || got.Revision != 0 {
		t.Fatalf("alice changed: %+v", got)
	}
}
