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
