package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---- Base URL ----

func TestBaseURLUsesConfiguredValueAndIgnoresForwardedHost(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org/"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "internal:8090"
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "http")

	if got := app.baseURL(req); got != "https://events.example.org" {
		t.Errorf("baseURL = %q, want the configured base without trailing slash", got)
	}
}

func TestBaseURLFallsBackToRequestHostWithoutForwardedHeaders(t *testing.T) {
	app := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:8090"
	req.Header.Set("X-Forwarded-Host", "evil.example")

	if got := app.baseURL(req); got != "http://localhost:8090" {
		t.Errorf("baseURL = %q, want http://localhost:8090", got)
	}
}

func TestSantaLinkEmailIgnoresForwardedHost(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedSantaEvent(t, app.DB)

	req := httptest.NewRequest(http.MethodPost, "/santa/register?lang=fr", strings.NewReader(url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {"alice@test.com"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	w := httptest.NewRecorder()
	newMux(app).ServeHTTP(w, req)

	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 1 {
		t.Fatalf("expected 1 email, got %d", fake.count())
	}
	html := fake.sent[0].HTML
	if !strings.Contains(html, "https://events.example.org/santa/edit?token=") {
		t.Error("link email should point at the configured base URL")
	}
	if strings.Contains(html, "evil.example") {
		t.Error("link email must never carry the forwarded host")
	}
}

func TestSignupCancelURLIgnoresForwardedHost(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Cuisine", int64Ptr(3))

	req := httptest.NewRequest(http.MethodPost, "/signup?lang=fr", strings.NewReader(url.Values{
		"task_id":    {fmt.Sprint(tk.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {"alice@test.com"},
		"phone":      {"0601"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	w := httptest.NewRecorder()
	newMux(app).ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "evil.example") {
		t.Error("signup response must never carry the forwarded host")
	}
	if got := app.baseURL(req); got != "https://events.example.org" {
		t.Errorf("baseURL = %q", got)
	}
}

// ---- Listener and timeouts ----

func TestListenAddressDefaultsToLoopback(t *testing.T) {
	if got := listenAddress("8090", ""); got != "127.0.0.1:8090" {
		t.Errorf("listenAddress = %q, want 127.0.0.1:8090", got)
	}
	if got := listenAddress("8090", "0.0.0.0:9000"); got != "0.0.0.0:9000" {
		t.Errorf("override should win, got %q", got)
	}
}

func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 30*time.Second ||
		srv.WriteTimeout != 120*time.Second || srv.IdleTimeout != 120*time.Second {
		t.Errorf("unexpected timeouts: %v %v %v %v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
}

// ---- Request body limits ----

// secureMux is newMux behind the same middleware chain main.go installs.
func secureMux(app *App) http.Handler {
	return app.secure(newMux(app))
}

func TestOversizedPublicPostIsRejected(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	h := secureMux(app)

	big := url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {strings.Repeat("x", 2<<20)},
		"email":      {"alice@test.com"},
	}
	w := postForm(h, "/santa/register?lang=fr", big)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized public POST: status = %d, want 413", w.Code)
	}
	if app.Email.(*fakeEmailSender).count() != 0 {
		t.Error("an oversized request must not trigger any email")
	}
}

func TestNormalPublicPostPassesBodyLimit(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	h := secureMux(app)

	w := postForm(h, "/santa/register?lang=fr", url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {"alice@test.com"},
	})
	if w.Code != 200 {
		t.Errorf("normal public POST: status = %d, want 200", w.Code)
	}
}

func TestBodyLimitIsHigherForAdminRoutes(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	h := secureMux(app)

	// 2 MB CSV: over the 1 MB public cap, under the 5 MB admin cap.
	csv := "email,prenom,nom\na@b.c,A," + strings.Repeat("x", 2<<20) + "\n"
	w := postMultipart(h, "/admin/santa/import", "p.csv", csv,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))
	if w.Code == http.StatusRequestEntityTooLarge {
		t.Error("a 2 MB admin upload must be accepted")
	}

	huge := strings.Repeat("x", 6<<20)
	w = postMultipart(h, "/admin/santa/import", "p.csv", huge,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a 6 MB admin upload: status = %d, want 413", w.Code)
	}
}
