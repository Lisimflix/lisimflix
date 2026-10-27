// Package app composes the M1 HTTP application: auth routes, the protected
// playback API, health endpoints and the embedded web app. The executable,
// server timeouts and shutdown belong to the integration plan.
package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/auth"
	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
)

const (
	maxAPIBodyBytes = 16 << 10
	readyTimeout    = 2 * time.Second
	csp             = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self'; " +
		"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// New returns the application handler. playback owns GET /api/v1/movies and
// everything under /api/v1/playback/, and only ever sees requests with a live
// session. web must contain index.html. ready reports whether initialisation
// succeeded; /health/ready returns 503 while it returns an error.
func New(cfg m1config.Config, am *auth.Manager, playback http.Handler, web fs.FS, ready func(context.Context) error, log *slog.Logger) (http.Handler, error) {
	if am == nil || playback == nil || web == nil || ready == nil {
		return nil, errors.New("app: auth manager, playback handler, web assets and ready check are required")
	}
	origin, err := url.Parse(cfg.PublicOrigin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return nil, errors.New("app: invalid public origin")
	}
	secure := origin.Scheme == "https"
	if am.Secure() != secure {
		return nil, errors.New("app: the auth manager's cookie mode doesn't match the public origin's scheme")
	}
	if st, err := fs.Stat(web, "index.html"); err != nil || st.IsDir() {
		return nil, errors.New("app: web assets have no index.html")
	}
	public := normalize(origin)
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	readiness := &readiness{log: log}

	api := http.NewServeMux()
	am.Register(api)
	api.Handle("/api/v1/movies", am.Require(playback))
	api.Handle("/api/v1/playback/", am.Require(playback))
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		contracts.WriteError(w, r, contracts.CodeNotFound, "Not found.")
	})
	apiHandler := apiHeaders(sameOrigin(public, auth.CSRF(cfg.CSRFKey, secure)(api)))

	root := http.NewServeMux()
	root.Handle("/api/", apiHandler)
	root.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		health(w, http.StatusOK, "ok")
	})
	root.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		if err := ready(ctx); readiness.note(err) != nil {
			health(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
		health(w, http.StatusOK, "ready")
	})
	root.HandleFunc("/health/", func(w http.ResponseWriter, r *http.Request) {
		health(w, http.StatusNotFound, "not_found")
	})
	root.Handle("/", spa(web))
	return observe(log, securityHeaders(root)), nil
}

// sameOrigin rejects unsafe requests unless both the Host and the Origin (or,
// without Origin, the Referer) match the configured public origin. It never
// reads X-Forwarded-* headers. gorilla/csrf compares Origin only with the
// request's own Host, which the client controls.
func sameOrigin(public string, next http.Handler) http.Handler {
	pub, _ := url.Parse(public)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}
		src := r.Header.Get("Origin")
		if src == "" {
			src = r.Referer()
		}
		u, err := url.Parse(src)
		if err != nil || src == "" || normalize(u) != public || !strings.EqualFold(hostPort(pub.Scheme, r.Host), pub.Host) {
			contracts.WriteError(w, r, contracts.CodeCSRFFailed, "Request blocked. Open Lisimflix at its configured address.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// normalize returns scheme://host[:port] in lower case, without default ports.
func normalize(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(hostPort(u.Scheme, u.Host))
}

func hostPort(scheme, host string) string {
	switch {
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		return strings.TrimSuffix(host, ":80")
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		return strings.TrimSuffix(host, ":443")
	}
	return host
}

// apiHeaders marks every API response private and caps request bodies.
// observe has already set the request ID that contracts.WriteError reuses.
func apiHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func health(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	io.WriteString(w, `{"status":"`+s+`"}`+"\n")
}

// spa serves files from web, and index.html for any other GET/HEAD path so
// the web app's client-side routes work. /api/ and /health/ never reach it.
func spa(web fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(web, name); err == nil && !st.IsDir() {
				w.Header().Set("Cache-Control", "public, max-age=3600")
				http.ServeFileFS(w, r, web, name)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		f, err := web.Open("index.html")
		if err != nil {
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.Copy(w, f)
	})
}

// readiness logs health.ready when the readiness state changes, with the
// reason when it fails, so a healthcheck polling every 30 s logs one line per
// change rather than one per poll (#75).
type readiness struct {
	log   *slog.Logger
	mu    sync.Mutex
	known bool
	ok    bool
}

func (r *readiness) note(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.known && r.ok == (err == nil) {
		return err
	}
	r.known, r.ok = true, err == nil
	if err != nil {
		r.log.Error("health.ready", "ready", false, "reason", err.Error())
	} else {
		r.log.Info("health.ready", "ready", true)
	}
	return err
}
