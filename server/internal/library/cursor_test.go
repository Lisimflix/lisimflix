package library

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	c := encodeCursor("q1", "the matrix", int64(1999), nil, "item-1")
	got, err := decodeCursor(c, "q1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{"the matrix", int64(1999), nil, "item-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %#v, want %#v", got, want)
	}
	if _, err := decodeCursor(encodeCursor("q"), "q"); err != nil {
		t.Fatalf("no keys: %v", err)
	}
}

func TestCursorRefusals(t *testing.T) {
	good := encodeCursor("q1", "a", "id")
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, c := range map[string]string{
		"other query":   encodeCursor("q2", "a", "id"),
		"other version": enc(`{"v":2,"q":"` + queryHash("q1") + `","k":["a","id"]}`),
		"too long":      encodeCursor("q1", strings.Repeat("x", 600)),
		"not base64":    "!!!",
		"not json":      enc("hello"),
		"no keys":       enc(`{"v":1,"q":"` + queryHash("q1") + `"}`),
		"truncated":     good[:len(good)-3],
	} {
		if _, err := decodeCursor(c, "q1"); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
}

func TestCursorDoesNotEscapeHTML(t *testing.T) {
	c := encodeCursor("q", "<a&b>", "id")
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || !strings.Contains(string(raw), `"<a&b>"`) || strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("raw %q %v", raw, err)
	}
	if got, err := decodeCursor(c, "q"); err != nil || !reflect.DeepEqual(got, []any{"<a&b>", "id"}) {
		t.Fatalf("keys %v %v", got, err)
	}
}
