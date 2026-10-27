package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "contracts", "m1", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// decodeStrict rejects fields the Go type does not declare.
func decodeStrict(t *testing.T, b []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatal(err)
	}
}

func canonical(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func TestPublicFixturesRoundTrip(t *testing.T) {
	cases := map[string]func() any{
		"anonymous.json":     func() any { return &AuthState{} },
		"authenticated.json": func() any { return &AuthState{} },
		"movies.json":        func() any { return &MovieList{} },
		"playback.json":      func() any { return &PlaybackSession{} },
		"error.json":         func() any { return &APIError{} },
	}
	for name, newValue := range cases {
		t.Run(name, func(t *testing.T) {
			raw := loadFixture(t, name)
			v := newValue()
			decodeStrict(t, raw, v)
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := canonical(t, out), canonical(t, raw); got != want {
				t.Fatalf("round trip changed fixture\n got: %s\nwant: %s", got, want)
			}
		})
	}

	var anon AuthState
	decodeStrict(t, loadFixture(t, "anonymous.json"), &anon)
	if anon.Authenticated || anon.Viewer != nil {
		t.Fatalf("anonymous fixture decoded as %+v", anon)
	}

	var fractional MovieList
	err := json.Unmarshal([]byte(`{"items":[{"durationMs":1.5}],"nextCursor":null}`), &fractional)
	if err == nil {
		t.Fatal("fractional durationMs was accepted")
	}
}

func TestPrincipalAbsentIsUnauthenticated(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("empty context reported a principal")
	}
	want := Principal{AccountID: "acct-1", ProfileID: "prof-1", AuthSessionID: "auth-1"}
	got, ok := PrincipalFrom(WithPrincipal(context.Background(), want))
	if !ok || got != want {
		t.Fatalf("got %+v, %v", got, ok)
	}
	b, _ := json.Marshal(want)
	if string(b) != "{}" {
		t.Fatalf("principal serialised as %s", b)
	}
}

func TestErrorResponseDoesNotExposeSourcePath(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/p/content?grant=secret-grant&path=/media/movies/secret.mp4", nil)
	w := httptest.NewRecorder()
	WriteError(w, r, CodeNotFound, "Movie not found.")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache control %q", cc)
	}
	body := w.Body.String()
	for _, leak := range []string{"/media", "secret.mp4", "secret-grant"} {
		if strings.Contains(body, leak) {
			t.Fatalf("body leaks %q: %s", leak, body)
		}
	}
	var e APIError
	decodeStrict(t, w.Body.Bytes(), &e)
	if e.Code != CodeNotFound || e.Message != "Movie not found." || e.RequestID == "" {
		t.Fatalf("error body %+v", e)
	}
	if w.Header().Get("X-Request-Id") != e.RequestID {
		t.Fatal("request ID header and body differ")
	}
}

func TestWriteErrorKeepsMiddlewareRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("X-Request-Id", "req-from-middleware")
	WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), CodeInvalidRequest, "Bad input.")
	var e APIError
	decodeStrict(t, w.Body.Bytes(), &e)
	if e.RequestID != "req-from-middleware" {
		t.Fatalf("request ID %q", e.RequestID)
	}
}

func TestWriteErrorRejectsUnknownCodeAndLongMessage(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), "open /media/x: permission denied", strings.Repeat("x", 1000))
	var e APIError
	decodeStrict(t, w.Body.Bytes(), &e)
	if e.Code != CodeInternalError || w.Code != http.StatusInternalServerError {
		t.Fatalf("unknown code produced %d %q", w.Code, e.Code)
	}
	if len(e.Message) > maxMessageBytes {
		t.Fatalf("message length %d", len(e.Message))
	}
}

// TestOpenAPIMatchesGoTypes keeps the Go types and the OpenAPI contract (and so
// the generated TypeScript types) on the same field names and required set. It
// does not compare field types or nullability.
func TestOpenAPIMatchesGoTypes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties           map[string]json.RawMessage `json:"properties"`
				Required             []string                   `json:"required"`
				AdditionalProperties *bool                      `json:"additionalProperties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	types := map[string]any{
		"Viewer": Viewer{}, "AuthState": AuthState{}, "Movie": Movie{}, "MovieList": MovieList{},
		"PlaybackSession": PlaybackSession{}, "APIError": APIError{},
		"LoginRequest": LoginRequest{}, "PlaybackSessionRequest": PlaybackSessionRequest{}, "HeartbeatRequest": HeartbeatRequest{},
		// CTR-2
		"UserItemState": UserItemState{}, "Resume": Resume{}, "Watched": Watched{},
		"ProgressEvent": ProgressEvent{}, "ProgressResult": ProgressResult{}, "WatchedRequest": WatchedRequest{},
	}
	for name := range doc.Components.Schemas {
		if _, ok := types[name]; !ok {
			t.Errorf("schema %s has no Go type in this test", name)
		}
	}
	for name, v := range types {
		s, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("schema %s missing", name)
			continue
		}
		if s.AdditionalProperties == nil || *s.AdditionalProperties {
			t.Errorf("schema %s must set additionalProperties: false", name)
		}
		var fields []string
		rt := reflect.TypeOf(v)
		for i := range rt.NumField() {
			fields = append(fields, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
		}
		var props []string
		for p := range s.Properties {
			props = append(props, p)
		}
		slices.Sort(fields)
		slices.Sort(props)
		req := slices.Sorted(slices.Values(s.Required))
		if !slices.Equal(fields, props) || !slices.Equal(fields, req) {
			t.Errorf("schema %s: go fields %v, properties %v, required %v", name, fields, props, req)
		}
	}
}

func TestEmptyMovieListEncodesItemsAsArray(t *testing.T) {
	for _, l := range []MovieList{{}, {Items: []Movie{}}} {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"items":[],"nextCursor":null}` {
			t.Fatalf("empty list encoded as %s", b)
		}
	}
}

func TestPrincipalWithEmptyIDIsUnauthenticated(t *testing.T) {
	full := Principal{AccountID: "acct-1", ProfileID: "prof-1", AuthSessionID: "auth-1"}
	cases := map[string]Principal{"zero": {}}
	for _, field := range []string{"AccountID", "ProfileID", "AuthSessionID"} {
		p := full
		reflect.ValueOf(&p).Elem().FieldByName(field).SetString("")
		cases["empty "+field] = p
	}
	for name, p := range cases {
		if got, ok := PrincipalFrom(WithPrincipal(context.Background(), p)); ok {
			t.Errorf("%s: reported principal %+v", name, got)
		}
	}
}

func TestErrorCodesMatchOpenAPI(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas struct {
				APIError struct {
					Properties struct {
						Code struct {
							Enum []string `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"APIError"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	got := slices.Sorted(maps.Keys(codeStatus))
	want := slices.Sorted(slices.Values(doc.Components.Schemas.APIError.Properties.Code.Enum))
	if !slices.Equal(got, want) {
		t.Fatalf("go codes %v, openapi enum %v", got, want)
	}
}

func TestWriteErrorSendsCodeStatus(t *testing.T) {
	for code, status := range codeStatus {
		w := httptest.NewRecorder()
		WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), code, "Safe message.")
		var e APIError
		decodeStrict(t, w.Body.Bytes(), &e)
		if w.Code != status || e.Code != code {
			t.Errorf("%s: got %d %q, want %d", code, w.Code, e.Code, status)
		}
	}
}

// CTR-2: a write whose expectedRevision is stale is a 409 the client resolves
// by reloading.
func TestRevisionConflictIsAConflict(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest(http.MethodPut, "/", nil), CodeRevisionConflict, "Reload and try again.")
	var e APIError
	decodeStrict(t, w.Body.Bytes(), &e)
	if w.Code != http.StatusConflict || e.Code != "revision_conflict" {
		t.Fatalf("got %d %q", w.Code, e.Code)
	}
}
