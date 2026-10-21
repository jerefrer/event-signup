package main

import (
	"net/http"
	"strings"
	"time"
)

// baseURL returns the public base URL (scheme://host) used to build every
// absolute link the app emails or renders. It comes from configuration
// (EVENT_SIGNUP_BASE_URL), never from request headers: a forged Host or
// X-Forwarded-Host would otherwise poison magic links sent to participants.
// Only when no base URL is configured (local development) does it fall back to
// the scheme of the connection and r.Host.
func (app *App) baseURL(r *http.Request) string {
	if base := strings.TrimRight(app.BaseURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// listenAddress returns the TCP address to bind. It defaults to the loopback
// interface so the app is only reachable through the nginx reverse proxy;
// EVENT_SIGNUP_LISTEN_ADDR overrides the full address (e.g. for development).
func listenAddress(port, override string) string {
	if override != "" {
		return override
	}
	return "127.0.0.1:" + port
}

// newHTTPServer builds the http.Server with explicit timeouts so a slow or
// stalled client cannot hold a connection (and its goroutine) open forever.
// WriteTimeout is generous because the AI task import can take a while.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

const (
	maxBodyBytes      = 1 << 20 // 1 MB: every public request
	maxAdminBodyBytes = 5 << 20 // 5 MB: /admin routes (CSV import, AI text, event editor)
)

// isAdminPath reports whether the request path belongs to the admin area.
func isAdminPath(p string) bool {
	return p == "/admin" || strings.HasPrefix(p, "/admin/")
}

// limitBody caps every request body. A declared Content-Length over the cap is
// refused up front with 413; chunked or lying bodies are cut off by
// http.MaxBytesReader as the handler reads them. The SES webhook keeps its own
// tighter 256 KB cap.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxBodyBytes)
		if isAdminPath(r.URL.Path) {
			limit = maxAdminBodyBytes
		}
		if r.ContentLength > limit {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// secure wraps the whole mux in the app's security middleware chain.
func (app *App) secure(next http.Handler) http.Handler {
	return limitBody(next)
}
