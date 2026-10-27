package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	id := NewRequestID()
	if !regexp.MustCompile(`^req-[A-Z2-7]{26}$`).MatchString(id) || NewRequestID() == id {
		t.Fatalf("NewRequestID = %q", id)
	}
	ctx := WithRequestID(context.Background(), id)
	if RequestID(ctx) != id || RequestID(context.Background()) != "" {
		t.Fatal("RequestID did not round-trip")
	}
}

func TestLogAddsTheRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	Log(WithRequestID(context.Background(), "req-X"), base).Info("auth.login")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil || line["request_id"] != "req-X" {
		t.Fatalf("line = %s", buf.Bytes())
	}
	buf.Reset()
	Log(context.Background(), base).Info("background")
	if bytes.Contains(buf.Bytes(), []byte("request_id")) {
		t.Fatalf("a request ID was added outside a request: %s", buf.Bytes())
	}
	Log(context.Background(), nil).Info("discarded") // a nil base must not panic
}
