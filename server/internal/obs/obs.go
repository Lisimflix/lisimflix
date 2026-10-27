// Package obs carries the per-request ID and builds loggers that include it.
// Logs are structured (log/slog) and must never contain passwords, cookies,
// CSRF tokens, content grants or query strings (spec §6, OPS-5).
package obs

import (
	"context"
	"crypto/rand"
	"log/slog"
)

type requestIDKey struct{}

// NewRequestID returns a fresh, non-secret correlation ID.
func NewRequestID() string { return "req-" + rand.Text() }

// WithRequestID attaches id to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request's ID, or "" outside a request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Log returns base with the request's ID attached. A nil base discards.
func Log(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		return slog.New(slog.DiscardHandler)
	}
	if id := RequestID(ctx); id != "" {
		return base.With("request_id", id)
	}
	return base
}
