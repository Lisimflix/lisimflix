package contracts

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	CodeInvalidRequest      = "invalid_request"
	CodeUnauthenticated     = "unauthenticated"
	CodeCSRFFailed          = "csrf_failed"
	CodeNotFound            = "not_found"
	CodeSourceChanged       = "source_changed"
	CodePlaybackExpired     = "playback_expired"
	CodeCapacityUnavailable = "capacity_unavailable"
	CodeRateLimited         = "rate_limited"
	CodeSourceUnavailable   = "source_unavailable"
	CodeInternalError       = "internal_error"
)

var knownCodes = map[string]bool{
	CodeInvalidRequest: true, CodeUnauthenticated: true, CodeCSRFFailed: true, CodeNotFound: true,
	CodeSourceChanged: true, CodePlaybackExpired: true, CodeCapacityUnavailable: true,
	CodeRateLimited: true, CodeSourceUnavailable: true, CodeInternalError: true,
}

const maxMessageBytes = 256

// WriteError writes one APIError response. Pass a fixed, user-safe message,
// never err.Error(): it may contain paths. An unknown code becomes a 500
// internal_error so a stray error string cannot reach the client. The request
// ID comes from an X-Request-Id response header set by middleware, or is
// generated here.
func WriteError(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
	if !knownCodes[code] {
		status, code, message = http.StatusInternalServerError, CodeInternalError, "Internal error."
	}
	if len(message) > maxMessageBytes {
		message = message[:maxMessageBytes]
	}
	message = strings.ToValidUTF8(message, "")
	id := w.Header().Get("X-Request-Id")
	if id == "" {
		id = "req-" + rand.Text()
		w.Header().Set("X-Request-Id", id)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIError{Code: code, Message: message, RequestID: id})
}
