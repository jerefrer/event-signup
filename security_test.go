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

// ---- Security headers ----

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	app := testApp(t)
	h := secureMux(app)

	for _, path := range []string{"/admin/login", "/api/slots", "/nope-404"} {
		w := getRequest(h, path)
		want := map[string]string{
			"X-Frame-Options":         "DENY",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "strict-origin-when-cross-origin",
			"Content-Security-Policy": "frame-ancestors 'none'",
		}
		for k, v := range want {
			if got := w.Header().Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", path, k, got, v)
			}
		}
	}
}

func TestAdminEmailPreviewIframeStillUsesSrcdoc(t *testing.T) {
	// The preview iframes are srcdoc documents (no network response), so the
	// frame-ancestors / X-Frame-Options headers do not apply to them. Guard
	// the markup so nobody swaps srcdoc for a URL that would now be blocked.
	b, err := templatesFS.ReadFile("templates/admin_event_edit.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `<iframe id="email-preview-fr" class="email-preview-iframe" src=`) {
		t.Error("email preview iframe must keep using srcdoc, not src")
	}
	js, _ := staticFS.ReadFile("static/admin.js")
	if !strings.Contains(string(js), "frFrame.srcdoc") {
		t.Error("admin.js must fill the preview iframes through srcdoc")
	}
}

// ---- Client IP ----

func TestClientIPTrustsXRealIPOnlyFromLoopback(t *testing.T) {
	cases := []struct {
		name, remote, realIP, want string
	}{
		{"nginx on loopback", "127.0.0.1:5555", "203.0.113.9", "203.0.113.9"},
		{"nginx on ipv6 loopback", "[::1]:5555", "203.0.113.9", "203.0.113.9"},
		{"loopback without header", "127.0.0.1:5555", "", "127.0.0.1"},
		{"direct peer spoofing the header", "198.51.100.7:5555", "203.0.113.9", "198.51.100.7"},
		{"garbage header from loopback", "127.0.0.1:5555", "not-an-ip", "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = c.remote
			if c.realIP != "" {
				req.Header.Set("X-Real-IP", c.realIP)
			}
			if got := clientIP(req); got != c.want {
				t.Errorf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// ---- Login rate limiter ----

func TestLoginLimiterLocksAfterFiveFailures(t *testing.T) {
	var l loginLimiter
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		l.fail("1.2.3.4", now)
		if l.blocked("1.2.3.4", now) {
			t.Fatalf("blocked after only %d failures", i+1)
		}
	}
	l.fail("1.2.3.4", now)
	if !l.blocked("1.2.3.4", now) {
		t.Fatal("5th failure should lock the IP")
	}
	if l.blocked("5.6.7.8", now) {
		t.Error("another IP must not be affected")
	}
	if !l.blocked("1.2.3.4", now.Add(14*time.Minute)) {
		t.Error("still locked after 14 minutes")
	}
	if l.blocked("1.2.3.4", now.Add(15*time.Minute+time.Second)) {
		t.Error("lock should expire after 15 minutes")
	}
}

func TestLoginLimiterFailuresOutsideWindowDoNotAccumulate(t *testing.T) {
	var l loginLimiter
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		l.fail("1.2.3.4", now)
	}
	later := now.Add(16 * time.Minute)
	l.fail("1.2.3.4", later)
	if l.blocked("1.2.3.4", later) {
		t.Error("an old failure window must not count toward the lockout")
	}
}

func TestLoginLimiterLockoutDoublesAndCaps(t *testing.T) {
	var l loginLimiter
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lock := func() {
		for i := 0; i < 5; i++ {
			l.fail("1.2.3.4", now)
		}
	}
	lock()
	now = now.Add(15*time.Minute + time.Second)
	lock() // second lockout: 30 minutes
	if !l.blocked("1.2.3.4", now.Add(29*time.Minute)) {
		t.Error("second lockout should last 30 minutes")
	}
	if l.blocked("1.2.3.4", now.Add(30*time.Minute+time.Second)) {
		t.Error("second lockout should end after 30 minutes")
	}
	for i := 0; i < 20; i++ {
		now = now.Add(24*time.Hour + time.Second)
		lock()
	}
	if l.blocked("1.2.3.4", now.Add(24*time.Hour+time.Second)) {
		t.Error("lockout must be capped at 24 hours")
	}
}

func TestLoginLimiterSuccessResets(t *testing.T) {
	var l loginLimiter
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		l.fail("1.2.3.4", now)
	}
	l.success("1.2.3.4")
	l.fail("1.2.3.4", now)
	if l.blocked("1.2.3.4", now) {
		t.Error("a successful login must reset the failure count")
	}
}

func TestLoginLimiterCleanupDropsStaleEntries(t *testing.T) {
	var l loginLimiter
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l.fail("1.2.3.4", now)
	l.blocked("9.9.9.9", now.Add(48*time.Hour)) // any call after the interval sweeps
	if n := l.size(); n != 0 {
		t.Errorf("stale entry should have been cleaned up, %d left", n)
	}
}

func TestAdminLoginLocksOutAfterFiveWrongPasswords(t *testing.T) {
	app := testApp(t)
	h := newMux(app)
	for i := 0; i < 5; i++ {
		w := postForm(h, "/admin/login?lang=fr", url.Values{"password": {"wrong"}})
		if w.Code != 200 {
			t.Fatalf("attempt %d: status = %d", i+1, w.Code)
		}
	}
	// Even the right password is refused while locked out.
	w := postForm(h, "/admin/login?lang=fr", url.Values{"password": {"testpass"}})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked out login: status = %d, want 429", w.Code)
	}
	if !strings.Contains(w.Body.String(), T("admin_login_locked", LangFR)) {
		t.Error("expected the translated lockout message")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "admin_session" && c.Value != "" {
			t.Error("no session may be issued while locked out")
		}
	}
}

func TestAdminLoginSuccessResetsFailures(t *testing.T) {
	app := testApp(t)
	h := newMux(app)
	for i := 0; i < 4; i++ {
		postForm(h, "/admin/login?lang=fr", url.Values{"password": {"wrong"}})
	}
	if w := postForm(h, "/admin/login?lang=fr", url.Values{"password": {"testpass"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d", w.Code)
	}
	for i := 0; i < 4; i++ {
		postForm(h, "/admin/login?lang=fr", url.Values{"password": {"wrong"}})
	}
	if w := postForm(h, "/admin/login?lang=fr", url.Values{"password": {"testpass"}}); w.Code != http.StatusSeeOther {
		t.Errorf("failures before a success must not count afterwards, status = %d", w.Code)
	}
}

func TestAdminLoginLockoutIsPerClientIP(t *testing.T) {
	app := testApp(t)
	h := newMux(app)
	post := func(ip, pw string) int {
		req := httptest.NewRequest(http.MethodPost, "/admin/login?lang=fr", strings.NewReader(url.Values{"password": {pw}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "127.0.0.1:4000" // nginx
		req.Header.Set("X-Real-IP", ip)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		post("203.0.113.1", "wrong")
	}
	if code := post("203.0.113.1", "testpass"); code != http.StatusTooManyRequests {
		t.Errorf("attacker IP: status = %d, want 429", code)
	}
	if code := post("203.0.113.2", "testpass"); code != http.StatusSeeOther {
		t.Errorf("other IP must still log in, status = %d", code)
	}
}

// ---- Server-side admin sessions ----

func TestAdminSessionCookieAttributes(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	w := postForm(newMux(app), "/admin/login?lang=fr", url.Values{"password": {"testpass"}})
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "admin_session" {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("no admin_session cookie")
	}
	if len(c.Value) != 64 {
		t.Errorf("token should be 32 random bytes in hex, got %q", c.Value)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 24*60*60 {
		t.Errorf("bad cookie attributes: %+v", c)
	}
}

func TestAdminSessionCookieNotSecureOverPlainHTTPBase(t *testing.T) {
	app := testApp(t) // no base URL: local development over http
	w := postForm(newMux(app), "/admin/login?lang=fr", url.Values{"password": {"testpass"}})
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "admin_session" && ck.Secure {
			t.Error("Secure must only be set when the base URL is https")
		}
	}
}

func TestOldStyleSessionCookieIsRejected(t *testing.T) {
	app := testApp(t)
	old := fmt.Sprintf("%x", sha256Sum([]byte(app.AdminPassword)))
	w := getRequest(newMux(app), "/admin?lang=fr", &http.Cookie{Name: "admin_session", Value: old})
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "/admin/login") {
		t.Errorf("sha256(password) cookie must no longer authenticate: status = %d", w.Code)
	}
}

func TestUnknownSessionTokenIsRejected(t *testing.T) {
	app := testApp(t)
	w := getRequest(newMux(app), "/admin?lang=fr", &http.Cookie{Name: "admin_session", Value: strings.Repeat("ab", 32)})
	if w.Code != http.StatusSeeOther {
		t.Errorf("unknown token: status = %d, want redirect", w.Code)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	app := testApp(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.Clock = func() time.Time { return now }
	tok := app.adminSessions.create(now)
	now = now.Add(24*time.Hour + time.Second)
	w := getRequest(newMux(app), "/admin?lang=fr", &http.Cookie{Name: "admin_session", Value: tok})
	if w.Code != http.StatusSeeOther {
		t.Errorf("expired session: status = %d, want redirect", w.Code)
	}
}

func TestLogoutInvalidatesTokenServerSide(t *testing.T) {
	app := testApp(t)
	h := newMux(app)
	login := postForm(h, "/admin/login?lang=fr", url.Values{"password": {"testpass"}})
	var tok *http.Cookie
	for _, ck := range login.Result().Cookies() {
		if ck.Name == "admin_session" {
			tok = ck
		}
	}
	if tok == nil {
		t.Fatal("no session cookie")
	}
	if w := getRequest(h, "/admin?lang=fr", tok); w.Code != 200 {
		t.Fatalf("fresh session: status = %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/logout", nil)
	req.AddCookie(tok)
	h2 := http.NewServeMux()
	h2.HandleFunc("/admin/logout", app.handleAdminLogout)
	h2.ServeHTTP(httptest.NewRecorder(), req)

	// The very same cookie value, replayed, must be dead now.
	if w := getRequest(h, "/admin?lang=fr", tok); w.Code != http.StatusSeeOther {
		t.Errorf("replayed token after logout: status = %d, want redirect", w.Code)
	}
}

// ---- Origin / CSRF check ----

// send issues a request through the secure chain with extra headers.
func send(h http.Handler, method, path, contentType, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

const formCT = "application/x-www-form-urlencoded"

func TestCrossOriginPostToAdminIsRejected(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedEvent(t, app.DB)
	h := secureMux(app)
	body := url.Values{"id": {fmt.Sprint(e.ID)}}.Encode()
	c := adminCookie(app)

	w := send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, body,
		map[string]string{"Origin": "https://evil.example"}, c)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin admin POST: status = %d, want 403", w.Code)
	}
	w = send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, body,
		map[string]string{"Origin": "null"}, c)
	if w.Code != http.StatusForbidden {
		t.Errorf("Origin: null admin POST: status = %d, want 403", w.Code)
	}
	w = send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, body,
		map[string]string{"Referer": "https://evil.example/page"}, c)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin Referer: status = %d, want 403", w.Code)
	}
	w = send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, body, nil, c)
	if w.Code != http.StatusForbidden {
		t.Errorf("admin POST without Origin or Referer: status = %d, want 403", w.Code)
	}
}

func TestSameOriginPostToAdminIsAccepted(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedEvent(t, app.DB)
	h := secureMux(app)
	body := url.Values{"event_id": {fmt.Sprint(e.ID)}, "id": {"1"}}.Encode()
	c := adminCookie(app)

	w := send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, body,
		map[string]string{"Origin": "https://events.example.org"}, c)
	if w.Code == http.StatusForbidden {
		t.Error("same-origin admin POST (Origin) must pass")
	}
	w = send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, body,
		map[string]string{"Referer": "https://events.example.org/admin/event/edit?id=1"}, c)
	if w.Code == http.StatusForbidden {
		t.Error("same-origin admin POST (Referer) must pass")
	}
}

func TestOriginCheckFallsBackToRequestHostWithoutBaseURL(t *testing.T) {
	app := testApp(t)
	h := secureMux(app)
	// httptest requests carry Host: example.com
	w := send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, "id=1",
		map[string]string{"Origin": "http://example.com"}, adminCookie(app))
	if w.Code == http.StatusForbidden {
		t.Error("Origin matching r.Host must pass when no base URL is configured")
	}
	w = send(h, http.MethodPost, "/admin/santa/participant/delete", formCT, "id=1",
		map[string]string{"Origin": "http://evil.example"}, adminCookie(app))
	if w.Code != http.StatusForbidden {
		t.Errorf("foreign Origin: status = %d, want 403", w.Code)
	}
}

func TestPublicPostWithoutOriginOrRefererIsAllowed(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedSantaEvent(t, app.DB)
	w := send(secureMux(app), http.MethodPost, "/santa/register?lang=fr", formCT, url.Values{
		"event_id": {fmt.Sprint(e.ID)}, "first_name": {"A"}, "last_name": {"B"}, "email": {"a@b.co"},
	}.Encode(), nil)
	if w.Code != 200 {
		t.Errorf("public POST with no Origin/Referer: status = %d, want 200 (old clients)", w.Code)
	}
}

func TestCrossOriginPublicPostIsRejected(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedSantaEvent(t, app.DB)
	w := send(secureMux(app), http.MethodPost, "/santa/register?lang=fr", formCT, url.Values{
		"event_id": {fmt.Sprint(e.ID)}, "first_name": {"A"}, "last_name": {"B"}, "email": {"a@b.co"},
	}.Encode(), map[string]string{"Origin": "https://evil.example"})
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin public POST: status = %d, want 403", w.Code)
	}
	if app.Email.(*fakeEmailSender).count() != 0 {
		t.Error("a rejected request must not send email")
	}
}

func TestWebhookIsExemptFromOriginCheck(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	w := send(secureMux(app), http.MethodPost, "/webhooks/ses", "text/plain", `{"Type":"Notification","Message":"{}"}`,
		map[string]string{"Origin": "https://sns.eu-west-1.amazonaws.com"})
	if w.Code != 200 {
		t.Errorf("webhook: status = %d, want 200", w.Code)
	}
}

func TestSafeMethodsSkipOriginCheck(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	w := send(secureMux(app), http.MethodGet, "/admin/login", "", "",
		map[string]string{"Origin": "https://evil.example"})
	if w.Code != 200 {
		t.Errorf("GET: status = %d, want 200", w.Code)
	}
}

func TestAdminAPIRequiresJSONContentType(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/api/group/delete", app.requireAdmin(app.handleAPIGroupDelete))
	h := app.secure(mux)
	hdr := map[string]string{"Origin": "https://events.example.org"}
	c := adminCookie(app)

	w := send(h, http.MethodPost, "/admin/api/group/delete", "text/plain", `{"id":1}`, hdr, c)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: status = %d, want 415", w.Code)
	}
	w = send(h, http.MethodPost, "/admin/api/group/delete", "application/json; charset=utf-8", `{"id":1}`, hdr, c)
	if w.Code != 200 {
		t.Errorf("application/json: status = %d, want 200", w.Code)
	}
}

// ---- Task signup with an already-registered email ----

func dupSignup(h http.Handler, taskID int64, email string, extra ...string) *httptest.ResponseRecorder {
	v := url.Values{
		"task_id":    {fmt.Sprint(taskID)},
		"first_name": {"Mallory"},
		"last_name":  {"Intruder"},
		"email":      {email},
		"phone":      {"0699999999"},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return postForm(h, "/signup?lang=fr", v)
}

func TestSignupDuplicateEmailDoesNotLeakRegistration(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedEvent(t, app.DB)
	tk1 := seedTask(t, app.DB, e.ID, "Cuisine", int64Ptr(5))
	tk2 := seedTask(t, app.DB, e.ID, "Vaisselle", int64Ptr(5))
	reg, _ := RegisterForTask(app.DB, tk1.ID, "Alice", "Dupont", "alice@test.com", "0601020304")

	w := dupSignup(newMux(app), tk2.ID, "alice@test.com")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, secret := range []string{reg.Token, "0601020304", "Dupont"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaks %q of the existing registration", secret)
		}
	}
	if !strings.Contains(body, T("already_registered_link_sent", LangFR)) {
		t.Error("expected the translated 'link sent by email' message")
	}
	if CountRegistrations(app.DB, e.ID) != 1 {
		t.Error("no second registration may be created")
	}
}

func TestSignupDuplicateEmailSendsManageLinkOnce(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.Clock = func() time.Time { return now }
	e := seedEvent(t, app.DB)
	tk1 := seedTask(t, app.DB, e.ID, "Cuisine", int64Ptr(5))
	tk2 := seedTask(t, app.DB, e.ID, "Vaisselle", int64Ptr(5))
	reg, _ := RegisterForTask(app.DB, tk1.ID, "Alice", "Dupont", "alice@test.com", "0601")
	h := newMux(app)
	fake := app.Email.(*fakeEmailSender)

	dupSignup(h, tk2.ID, "ALICE@test.com")
	if fake.count() != 1 {
		t.Fatalf("expected 1 email, got %d", fake.count())
	}
	m := fake.sent[0]
	if m.To != "ALICE@test.com" && m.To != "alice@test.com" {
		t.Errorf("email sent to %q", m.To)
	}
	if !strings.Contains(m.HTML, "https://events.example.org/cancel/"+reg.Token) {
		t.Error("email must carry the manage/cancel link")
	}
	if !strings.Contains(m.HTML, "Cuisine") {
		t.Error("email must name the registered task")
	}

	// Same (event, email) within 10 minutes: same page, no second email.
	now = now.Add(9 * time.Minute)
	w := dupSignup(h, tk2.ID, "alice@test.com")
	if fake.count() != 1 {
		t.Errorf("throttled attempt sent an email (count = %d)", fake.count())
	}
	if !strings.Contains(w.Body.String(), T("already_registered_link_sent", LangFR)) {
		t.Error("throttled attempt must show the same message")
	}

	// After the window it works again.
	now = now.Add(2 * time.Minute)
	dupSignup(h, tk2.ID, "alice@test.com")
	if fake.count() != 2 {
		t.Errorf("expected a new email after 10 minutes, count = %d", fake.count())
	}
}

func TestSignupBogusCancelTokenStillHitsDuplicateCheck(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Cuisine", int64Ptr(5))
	reg, _ := RegisterForTask(app.DB, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")

	w := dupSignup(newMux(app), tk.ID, "alice@test.com", "cancel_token", "bogus")
	if strings.Contains(w.Body.String(), reg.Token) {
		t.Error("a bogus cancel_token must not unlock the existing registration")
	}
	if CountRegistrations(app.DB, e.ID) != 1 {
		t.Error("a bogus cancel_token must not create a duplicate registration")
	}
}

func TestMailThrottle(t *testing.T) {
	var th mailThrottle
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !th.allow("k", now) {
		t.Fatal("first send must be allowed")
	}
	if th.allow("k", now.Add(9*time.Minute)) {
		t.Error("second send within 10 minutes must be refused")
	}
	if !th.allow("other", now.Add(time.Minute)) {
		t.Error("another key is independent")
	}
	if !th.allow("k", now.Add(10*time.Minute+time.Second)) {
		t.Error("allowed again after the window")
	}
}

// ---- Secret Santa registration abuse ----

func santaRegister(h http.Handler, eventID int64, email, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/santa/register?lang=fr", strings.NewReader(url.Values{
		"event_id": {fmt.Sprint(eventID)}, "first_name": {"Eve"}, "last_name": {"Attacker"}, "email": {email},
	}.Encode()))
	req.Header.Set("Content-Type", formCT)
	req.RemoteAddr = "127.0.0.1:4000"
	req.Header.Set("X-Real-IP", ip)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestSantaRegisterIsRateLimitedPerIP(t *testing.T) {
	app := testApp(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.Clock = func() time.Time { return now }
	e := seedSantaEvent(t, app.DB)
	h := newMux(app)

	for i := 0; i < 10; i++ {
		if w := santaRegister(h, e.ID, fmt.Sprintf("p%d@test.com", i), "203.0.113.5"); w.Code != 200 {
			t.Fatalf("registration %d: status = %d", i+1, w.Code)
		}
	}
	w := santaRegister(h, e.ID, "p11@test.com", "203.0.113.5")
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("11th registration in an hour: status = %d, want 429", w.Code)
	}
	if !strings.Contains(w.Body.String(), T("rate_limited", LangFR)) {
		t.Error("expected the translated rate-limit message")
	}
	if _, err := GetSantaParticipantByEmail(app.DB, e.ID, "p11@test.com"); err == nil {
		t.Error("a rate-limited request must not create a participant")
	}
	if w := santaRegister(h, e.ID, "other@test.com", "203.0.113.6"); w.Code != 200 {
		t.Errorf("another IP is unaffected, status = %d", w.Code)
	}
	now = now.Add(61 * time.Minute)
	if w := santaRegister(h, e.ID, "p12@test.com", "203.0.113.5"); w.Code != 200 {
		t.Errorf("the limit lifts after an hour, status = %d", w.Code)
	}
}

func TestSantaRegisterSameEmailThrottled(t *testing.T) {
	app := testApp(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.Clock = func() time.Time { return now }
	e := seedSantaEvent(t, app.DB)
	h := newMux(app)
	fake := app.Email.(*fakeEmailSender)

	santaRegister(h, e.ID, "victim@test.com", "203.0.113.5")
	now = now.Add(5 * time.Minute)
	w := santaRegister(h, e.ID, "VICTIM@test.com", "203.0.113.6")
	if fake.count() != 1 {
		t.Errorf("second link email within 10 minutes was sent (count = %d)", fake.count())
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), T("santa_link_sent", LangFR)) {
		t.Error("throttled attempt must show the normal 'link sent' message")
	}
	now = now.Add(6 * time.Minute)
	santaRegister(h, e.ID, "victim@test.com", "203.0.113.7")
	if fake.count() != 2 {
		t.Errorf("a new link email is allowed after 10 minutes (count = %d)", fake.count())
	}
}

func TestSantaRegisterDoesNotRenameExistingParticipant(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	UpsertSantaParticipant(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "fr")
	santaRegister(newMux(app), e.ID, "alice@test.com", "203.0.113.5")
	p, _ := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com")
	if p.FirstName != "Alice" || p.LastName != "Dupont" {
		t.Errorf("public registration renamed an existing participant: %+v", p)
	}
}

func TestAdminImportStillUpdatesNames(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	UpsertSantaParticipant(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "fr")
	mux := newMux(app)
	csv := "email,Nom,Prénom\nalice@test.com,Martin,Alicia\n"
	postMultipart(mux, "/admin/santa/import?lang=fr", "list.csv", csv,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))
	p, _ := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com")
	if p.FirstName != "Alicia" || p.LastName != "Martin" {
		t.Errorf("the admin CSV import is the trusted way to correct names: %+v", p)
	}
}

func TestHitLimiter(t *testing.T) {
	var l hitLimiter
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if !l.allow("ip", now.Add(time.Duration(i)*time.Minute), 3, time.Hour) {
			t.Fatalf("hit %d should pass", i+1)
		}
	}
	if l.allow("ip", now.Add(10*time.Minute), 3, time.Hour) {
		t.Error("4th hit within the window must be refused")
	}
	if !l.allow("ip", now.Add(61*time.Minute), 3, time.Hour) {
		t.Error("oldest hit leaves the window after an hour")
	}
}
