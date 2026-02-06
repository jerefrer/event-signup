package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ---- helpers ----

// newMux wires up the routes the same way main.go does, minus static files.
func newMux(app *App) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/login", app.handleAdminLogin)
	mux.HandleFunc("/admin", app.requireAdmin(app.handleAdminEvents))
	mux.HandleFunc("/api/slots", app.handleAPISlots)
	mux.HandleFunc("/e/", app.handlePublicEvent)
	mux.HandleFunc("/signup", app.handlePublicSignup)
	mux.HandleFunc("/cancel/", app.handlePublicCancel)
	return mux
}

func adminCookie(app *App) *http.Cookie {
	return &http.Cookie{Name: "admin_session", Value: app.adminSessionValue()}
}

// postForm sends a POST with form data and returns the response.
func postForm(mux http.Handler, path string, data url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func getRequest(mux http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// ---- Public event page ----

func TestPublicEventPage(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	seedTask(t, app.DB, e.ID, "Cuisine", int64Ptr(3))

	mux := newMux(app)
	w := getRequest(mux, "/e/"+e.Slug+"?lang=fr")

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Cuisine") {
		t.Error("expected task title in page")
	}
	if !strings.Contains(body, `name="task_id"`) {
		t.Error("expected radio inputs in page")
	}
	if !strings.Contains(body, "registered-view") {
		t.Error("expected registered-view div")
	}
}

func TestPublicEventPage404(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)
	w := getRequest(mux, "/e/nonexistent")
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---- Signup flow ----

func TestSignupSuccess(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Cuisine", int64Ptr(5))

	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":    {fmt.Sprint(tk.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":     {"alice@test.com"},
		"phone":     {"0601020304"},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "alice@test.com") {
		t.Error("expected email in confirmation")
	}
	if !strings.Contains(body, "localStorage.setItem") {
		t.Error("expected localStorage store script")
	}

	// Verify registration in DB
	reg, err := GetRegistrationByEmailAndEvent(app.DB, "alice@test.com", e.ID)
	if err != nil {
		t.Fatalf("registration not in DB: %v", err)
	}
	if reg.FirstName != "Alice" {
		t.Errorf("first_name = %q", reg.FirstName)
	}
	if reg.LastName != "Dupont" {
		t.Errorf("last_name = %q", reg.LastName)
	}
}

func TestSignupMissingFields(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Task", int64Ptr(5))

	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":    {fmt.Sprint(tk.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":     {""}, // missing
		"phone":     {"0601"},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "alert-error") {
		t.Error("expected error alert for missing fields")
	}
}

func TestSignupTaskFull(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Limited", int64Ptr(1))

	// Fill the task
	RegisterForTask(app.DB, tk.ID, "First", "Person", "first@t.com", "01")

	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":    {fmt.Sprint(tk.ID)},
		"first_name": {"Second"},
		"last_name":  {"Person"},
		"email":     {"second@t.com"},
		"phone":     {"02"},
	})

	body := w.Body.String()
	if !strings.Contains(body, "alert-error") {
		t.Error("expected full-task error")
	}
}

// ---- Duplicate email (different device) ----

func TestSignupDuplicateEmail(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk1 := seedTask(t, app.DB, e.ID, "Task A", int64Ptr(5))
	tk2 := seedTask(t, app.DB, e.ID, "Task B", int64Ptr(5))

	// First registration
	RegisterForTask(app.DB, tk1.ID, "Alice", "Dupont", "alice@test.com", "0601")

	// Try to register same email for different task (no cancel_token)
	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":    {fmt.Sprint(tk2.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":     {"alice@test.com"},
		"phone":     {"0601"},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "localStorage.setItem") {
		t.Error("expected localStorage store for existing reg")
	}

	// Should still only have 1 registration
	count := CountRegistrations(app.DB, e.ID)
	if count != 1 {
		t.Errorf("expected 1 registration, got %d", count)
	}
}

func TestSignupDuplicateEmailCaseInsensitive(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Task", int64Ptr(5))

	RegisterForTask(app.DB, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")

	mux := newMux(app)
	w := postForm(mux, "/signup?lang=en", url.Values{
		"task_id":    {fmt.Sprint(tk.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":     {"ALICE@TEST.COM"},
		"phone":     {"0601"},
	})

	body := w.Body.String()
	if !strings.Contains(body, "localStorage.setItem") {
		t.Error("case-insensitive duplicate detection should show existing registration")
	}
}

// ---- Change registration (cancel_token flow) ----

func TestSignupChangeTask(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk1 := seedTask(t, app.DB, e.ID, "Task A", int64Ptr(5))
	tk2 := seedTask(t, app.DB, e.ID, "Task B", int64Ptr(5))

	// Initial registration
	reg, _ := RegisterForTask(app.DB, tk1.ID, "Alice", "Dupont", "alice@test.com", "0601")

	// Change to task B by providing cancel_token
	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":      {fmt.Sprint(tk2.ID)},
		"first_name":   {"Alice"},
		"last_name":    {"Dupont"},
		"email":        {"alice@test.com"},
		"phone":        {"0601"},
		"cancel_token": {reg.Token},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "localStorage.setItem") {
		t.Error("expected confirmation with localStorage store")
	}

	// Old registration should be gone, new one exists
	_, err := GetRegistrationByToken(app.DB, reg.Token)
	if err == nil {
		t.Error("old registration should be deleted")
	}

	newReg, err := GetRegistrationByEmailAndEvent(app.DB, "alice@test.com", e.ID)
	if err != nil {
		t.Fatal("new registration should exist")
	}
	if newReg.TaskID != tk2.ID {
		t.Errorf("new reg task = %d, want %d", newReg.TaskID, tk2.ID)
	}
}

func TestSignupChangeSameTask(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Task A", int64Ptr(5))

	reg, _ := RegisterForTask(app.DB, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")

	// "Change" to same task — should just show confirmation, not create a new registration
	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":      {fmt.Sprint(tk.ID)},
		"first_name":   {"Alice"},
		"last_name":    {"Dupont"},
		"email":        {"alice@test.com"},
		"phone":        {"0601"},
		"cancel_token": {reg.Token},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	// Same registration should still exist with same token
	got, err := GetRegistrationByToken(app.DB, reg.Token)
	if err != nil {
		t.Fatal("original registration should still exist")
	}
	if got.ID != reg.ID {
		t.Error("should be the same registration")
	}
}

func TestSignupChangeInvalidToken(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Task", int64Ptr(5))

	// Submit with a bogus cancel_token — should proceed as a normal registration
	mux := newMux(app)
	w := postForm(mux, "/signup?lang=fr", url.Values{
		"task_id":      {fmt.Sprint(tk.ID)},
		"first_name":   {"Bob"},
		"last_name":    {"Martin"},
		"email":        {"bob@test.com"},
		"phone":        {"0602"},
		"cancel_token": {"bogus-token-that-does-not-exist"},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "localStorage.setItem") {
		t.Error("expected successful registration confirmation")
	}

	reg, err := GetRegistrationByEmailAndEvent(app.DB, "bob@test.com", e.ID)
	if err != nil {
		t.Fatal("registration should have been created")
	}
	if reg.FirstName != "Bob" {
		t.Errorf("first_name = %q", reg.FirstName)
	}
}

// ---- Cancel flow ----

func TestCancelFlow(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk := seedTask(t, app.DB, e.ID, "Task", int64Ptr(5))
	reg, _ := RegisterForTask(app.DB, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")

	mux := newMux(app)

	// GET cancel page — should show confirmation prompt
	w := getRequest(mux, "/cancel/"+reg.Token+"?lang=fr")
	if w.Code != 200 {
		t.Fatalf("GET cancel status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Alice") {
		t.Error("expected registrant name on cancel page")
	}

	// POST cancel — should delete
	w2 := postForm(mux, "/cancel/"+reg.Token+"?lang=fr", url.Values{})
	if w2.Code != 200 {
		t.Fatalf("POST cancel status = %d", w2.Code)
	}
	body2 := w2.Body.String()
	if !strings.Contains(body2, "localStorage.removeItem") {
		t.Error("expected localStorage clear script")
	}

	// Registration should be gone
	_, err := GetRegistrationByToken(app.DB, reg.Token)
	if err == nil {
		t.Error("registration should be deleted after cancel")
	}
}

func TestCancelInvalidToken(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)

	w := getRequest(mux, "/cancel/nonexistent-token?lang=fr")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "introuvable") {
		t.Error("expected 'not found' message")
	}
}

// ---- Slots API ----

func TestAPISlotsEndpoint(t *testing.T) {
	app := testApp(t)
	e := seedEvent(t, app.DB)
	tk1 := seedTask(t, app.DB, e.ID, "Limited", int64Ptr(3))
	tk2 := seedTask(t, app.DB, e.ID, "Unlimited", nil)

	RegisterForTask(app.DB, tk1.ID, "A", "A", "a@t.com", "01")

	mux := newMux(app)
	w := getRequest(mux, fmt.Sprintf("/api/slots?event_id=%d", e.ID))

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}

	var slots []struct {
		ID        int64 `json:"id"`
		SlotsLeft int   `json:"slots_left"`
		IsFull    bool  `json:"is_full"`
	}
	if err := json.NewDecoder(w.Body).Decode(&slots); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(slots))
	}

	slotMap := map[int64]struct {
		SlotsLeft int
		IsFull    bool
	}{}
	for _, s := range slots {
		slotMap[s.ID] = struct {
			SlotsLeft int
			IsFull    bool
		}{s.SlotsLeft, s.IsFull}
	}

	if s := slotMap[tk1.ID]; s.SlotsLeft != 2 || s.IsFull {
		t.Errorf("limited task: slots=%d full=%v", s.SlotsLeft, s.IsFull)
	}
	if s := slotMap[tk2.ID]; s.SlotsLeft != -1 || s.IsFull {
		t.Errorf("unlimited task: slots=%d full=%v", s.SlotsLeft, s.IsFull)
	}
}

func TestAPISlotsNoEventID(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)
	w := getRequest(mux, "/api/slots")
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ---- Admin auth ----

func TestAdminRequiresAuth(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)

	w := getRequest(mux, "/admin?lang=fr")
	if w.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated admin: status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/admin/login") {
		t.Errorf("redirect to %q, expected /admin/login", loc)
	}
}

func TestAdminLoginSuccess(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)

	w := postForm(mux, "/admin/login?lang=fr", url.Values{
		"password": {"testpass"},
	})

	if w.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	// Check that session cookie is set
	var found bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "admin_session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected admin_session cookie")
	}
}

func TestAdminLoginWrongPassword(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)

	w := postForm(mux, "/admin/login?lang=fr", url.Values{
		"password": {"wrong"},
	})

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "alert-error") {
		t.Error("expected login error")
	}
}

func TestAdminWithAuth(t *testing.T) {
	app := testApp(t)
	seedEvent(t, app.DB)
	mux := newMux(app)

	w := getRequest(mux, "/admin?lang=fr", adminCookie(app))
	if w.Code != 200 {
		t.Errorf("authenticated admin: status = %d, want 200", w.Code)
	}
}

// ---- Signup GET redirects ----

func TestSignupGetRedirects(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)

	w := getRequest(mux, "/signup")
	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /signup status = %d, want %d", w.Code, http.StatusSeeOther)
	}
}
