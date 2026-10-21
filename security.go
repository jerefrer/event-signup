package main

import (
	"net/http"
	"strings"
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
