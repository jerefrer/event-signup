package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
