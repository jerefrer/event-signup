package main

import (
	"encoding/csv"
	"net/http"
	"net/url"
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

// securityHeaders sets the baseline hardening headers on every response.
// The CSP is deliberately limited to frame-ancestors: the pages use inline
// scripts, so a script-src policy would break them. The admin email preview is
// unaffected — its iframes are srcdoc documents, which have no response of
// their own for these headers to apply to.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// allowedOrigins lists the origins a same-site browser request may carry: the
// configured base URL's origin, or — with no base URL (development) — the
// request's own Host over either scheme.
func (app *App) allowedOrigins(r *http.Request) []string {
	if base := strings.TrimRight(app.BaseURL, "/"); base != "" {
		if u, err := url.Parse(base); err == nil && u.Scheme != "" && u.Host != "" {
			return []string{strings.ToLower(u.Scheme + "://" + u.Host)}
		}
	}
	return []string{"http://" + strings.ToLower(r.Host), "https://" + strings.ToLower(r.Host)}
}

// checkOrigin is the CSRF defence for state-changing requests. A browser sends
// Origin (or at least Referer) on cross-site POSTs; if present it must match
// our own origin. Admin routes additionally refuse requests carrying neither
// header, while public routes tolerate that (old clients and privacy tools
// that strip both). The SES webhook is exempt: SNS is not a browser.
// Admin JSON APIs must also be called with Content-Type: application/json, which
// a cross-site HTML form cannot produce.
func (app *App) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/webhooks/ses" {
			next.ServeHTTP(w, r)
			return
		}
		origins := app.allowedOrigins(r)
		origin, referer := r.Header.Get("Origin"), r.Header.Get("Referer")
		switch {
		case origin != "":
			if !containsFold(origins, origin) {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		case referer != "":
			ok := false
			for _, o := range origins {
				if strings.HasPrefix(strings.ToLower(referer), o+"/") {
					ok = true
				}
			}
			if !ok {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		case isAdminPath(r.URL.Path):
			http.Error(w, "forbidden: missing Origin", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/admin/api/") && !isJSONContentType(r.Header.Get("Content-Type")) {
			http.Error(w, `{"error":"content type must be application/json"}`, http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// secure wraps the whole mux in the app's security middleware chain.
func (app *App) secure(next http.Handler) http.Handler {
	return securityHeaders(limitBody(app.checkOrigin(next)))
}

// csvSafe neutralizes spreadsheet formula injection: a cell that starts with
// =, +, -, @, tab or carriage return would be evaluated as a formula by Excel
// or LibreOffice when an admin opens the export, so it gets a leading
// apostrophe (shown by the spreadsheet as plain text).
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// writeCSVRow writes one record with every cell passed through csvSafe.
func writeCSVRow(cw *csv.Writer, cells []string) {
	safe := make([]string, len(cells))
	for i, c := range cells {
		safe[i] = csvSafe(c)
	}
	cw.Write(safe)
}
