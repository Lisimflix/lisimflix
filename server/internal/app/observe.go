package app

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// observe gives every request an ID (context and X-Request-Id, which
// contracts.WriteError reuses) and logs requests that end in an error:
// 4xx at INFO, 5xx at ERROR. It logs the path without the query string, so
// content grants never reach the log. Health checks are left out: readiness
// logs its own changes (#75).
func observe(log *slog.Logger, next http.Handler) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := obs.NewRequestID()
		w.Header().Set("X-Request-Id", id)
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(obs.WithRequestID(r.Context(), id)))
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if status < 400 || strings.HasPrefix(r.URL.Path, "/health/") {
			return
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		log.Log(r.Context(), level, "http.request", "request_id", id, "method", r.Method, "path", r.URL.Path,
			"status", status, "bytes", rec.bytes, "duration_ms", time.Since(start).Milliseconds())
	})
}

// recorder notes the status and body size. Unwrap lets
// http.ResponseController reach the connection, so playback's write
// deadlines still work; it deliberately adds no io.ReaderFrom.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
