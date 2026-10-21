package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedAttendanceEvent(t *testing.T, db *sql.DB) *Event {
	t.Helper()
	e := &Event{TitleFR: "Fête", TitleEN: "Party", EventDate: "2026-07-01", EventType: "attendance"}
	if err := CreateEvent(db, e); err != nil {
		t.Fatalf("seed attendance event: %v", err)
	}
	return e
}

func rsvpForm(eventID int64, email, attending, message string, extra ...string) url.Values {
	v := url.Values{
		"event_id":   {fmt.Sprint(eventID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {email},
		"phone":      {"0601020304"},
		"attending":  {attending},
		"message":    {message},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return v
}

func lookup(h http.Handler, body string) *httptest.ResponseRecorder {
	return send(h, http.MethodPost, "/rsvp/lookup", "application/json", body, nil)
}

func TestAttendanceGetsTokenOnCreate(t *testing.T) {
	db := testDB(t)
	e := seedAttendanceEvent(t, db)
	a, err := UpsertAttendance(db, e.ID, "A", "B", "a@b.co", "", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Token) != 32 {
		t.Errorf("token = %q, want 128-bit hex", a.Token)
	}
	b, _ := UpsertAttendance(db, e.ID, "A", "B2", "a@b.co", "", false, "")
	if b.Token != a.Token {
		t.Error("updating an attendance must keep its token")
	}
}

func TestMigrationBackfillsAttendanceTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL UNIQUE, title_fr TEXT NOT NULL, title_en TEXT NOT NULL DEFAULT '', description_fr TEXT NOT NULL DEFAULT '', description_en TEXT NOT NULL DEFAULT '', event_date TEXT NOT NULL, event_time TEXT NOT NULL DEFAULT '', event_type TEXT NOT NULL DEFAULT 'tasks', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE attendances (id INTEGER PRIMARY KEY AUTOINCREMENT, event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE, first_name TEXT NOT NULL DEFAULT '', last_name TEXT NOT NULL DEFAULT '', email TEXT NOT NULL, phone TEXT NOT NULL DEFAULT '', attending INTEGER NOT NULL DEFAULT 1, message TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO events (slug, title_fr, event_date, event_type) VALUES ('f', 'F', '2026-01-01', 'attendance');
INSERT INTO attendances (event_id, email) VALUES (1, 'a@b.co'), (1, 'c@d.co');`)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()

	db, err := InitDB(path)
	if err != nil {
		t.Fatalf("InitDB on an old database: %v", err)
	}
	defer db.Close()
	list, _ := ListAttendances(db, 1)
	if len(list) != 2 {
		t.Fatalf("got %d attendances", len(list))
	}
	if len(list[0].Token) != 32 || len(list[1].Token) != 32 || list[0].Token == list[1].Token {
		t.Errorf("rows must be backfilled with distinct tokens: %q %q", list[0].Token, list[1].Token)
	}
	// A second start must not rotate them.
	db.Close()
	db2, _ := InitDB(path)
	defer db2.Close()
	again, _ := ListAttendances(db2, 1)
	if again[0].Token != list[0].Token {
		t.Error("backfill must be idempotent")
	}
}

func TestRSVPLookupByEmailReturnsNothing(t *testing.T) {
	app := testApp(t)
	e := seedAttendanceEvent(t, app.DB)
	UpsertAttendance(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "0601020304", true, "secret note")
	h := newMux(app)

	w := lookup(h, fmt.Sprintf(`{"event_id":%d,"email":"alice@test.com"}`, e.ID))
	if strings.TrimSpace(w.Body.String()) != `{"found":false}` {
		t.Errorf("email-only lookup must find nothing, got %s", w.Body.String())
	}
}

func TestRSVPLookupByToken(t *testing.T) {
	app := testApp(t)
	e := seedAttendanceEvent(t, app.DB)
	other := seedAttendanceEvent(t, app.DB)
	a, _ := UpsertAttendance(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "0601020304", true, "hello")
	h := newMux(app)

	w := lookup(h, fmt.Sprintf(`{"event_id":%d,"token":%q}`, e.ID, a.Token))
	if !strings.Contains(w.Body.String(), `"found":true`) || !strings.Contains(w.Body.String(), "hello") {
		t.Errorf("matching token should return the data, got %s", w.Body.String())
	}
	for name, body := range map[string]string{
		"wrong token":    fmt.Sprintf(`{"event_id":%d,"token":"%s"}`, e.ID, strings.Repeat("0", 32)),
		"empty token":    fmt.Sprintf(`{"event_id":%d,"token":""}`, e.ID),
		"other event":    fmt.Sprintf(`{"event_id":%d,"token":%q}`, other.ID, a.Token),
		"no token field": fmt.Sprintf(`{"event_id":%d}`, e.ID),
	} {
		if got := strings.TrimSpace(lookup(h, body).Body.String()); got != `{"found":false}` {
			t.Errorf("%s: got %s", name, got)
		}
	}
}

func TestRSVPCreateStoresTokenInConfirmation(t *testing.T) {
	app := testApp(t)
	e := seedAttendanceEvent(t, app.DB)
	w := postForm(newMux(app), "/rsvp?lang=fr", rsvpForm(e.ID, "alice@test.com", "yes", "hi"))
	a, err := GetAttendanceByEmail(app.DB, "alice@test.com", e.ID)
	if err != nil {
		t.Fatalf("attendance not created: %v", err)
	}
	if !strings.Contains(w.Body.String(), `token: "`+a.Token+`"`) {
		t.Error("the confirmation must store the token in localStorage")
	}
}

func TestRSVPUpdateWithoutTokenDoesNotOverwrite(t *testing.T) {
	app := testApp(t)
	app.BaseURL = "https://events.example.org"
	e := seedAttendanceEvent(t, app.DB)
	a, _ := UpsertAttendance(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "0601020304", true, "original")
	h := newMux(app)
	fake := app.Email.(*fakeEmailSender)

	for name, extra := range map[string][]string{"missing token": nil, "wrong token": {"token", strings.Repeat("f", 32)}} {
		w := postForm(h, "/rsvp?lang=fr", rsvpForm(e.ID, "alice@test.com", "no", "hijacked", extra...))
		got, _ := GetAttendance(app.DB, a.ID)
		if got.Message != "original" || !got.Attending {
			t.Errorf("%s: existing attendance was overwritten: %+v", name, got)
		}
		if strings.Contains(w.Body.String(), a.Token) || strings.Contains(w.Body.String(), "0601020304") {
			t.Errorf("%s: response leaks stored data", name)
		}
		if !strings.Contains(w.Body.String(), T("rsvp_already_registered_link_sent", LangFR)) {
			t.Errorf("%s: expected the 'link sent' message", name)
		}
	}
	if fake.count() != 1 {
		t.Fatalf("expected exactly 1 email (second attempt throttled), got %d", fake.count())
	}
	if !strings.Contains(fake.sent[0].HTML, "https://events.example.org/e/"+e.Slug+"?rsvp="+a.Token) {
		t.Error("email must carry the /e/<slug>?rsvp=<token> link")
	}
}

func TestRSVPUpdateWithRightTokenWorks(t *testing.T) {
	app := testApp(t)
	e := seedAttendanceEvent(t, app.DB)
	a, _ := UpsertAttendance(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "0601020304", true, "original")

	postForm(newMux(app), "/rsvp?lang=fr", rsvpForm(e.ID, "alice@test.com", "no", "changed", "token", a.Token))
	got, _ := GetAttendance(app.DB, a.ID)
	if got.Message != "changed" || got.Attending {
		t.Errorf("update with the right token not applied: %+v", got)
	}
	if app.Email.(*fakeEmailSender).count() != 0 {
		t.Error("no email expected when the token matches")
	}
}

func TestRSVPPrefillFromEmailLink(t *testing.T) {
	app := testApp(t)
	e := seedAttendanceEvent(t, app.DB)
	a, _ := UpsertAttendance(app.DB, e.ID, "Alice", "Dupont", "alice@test.com", "0601020304", false, "my note")
	h := newMux(app)

	w := getRequest(h, "/e/"+e.Slug+"?lang=fr&rsvp="+a.Token)
	body := w.Body.String()
	for _, want := range []string{`value="Alice"`, `value="alice@test.com"`, "my note", `name="token" id="rsvp-token" value="` + a.Token + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("prefilled page is missing %q", want)
		}
	}
	if strings.Contains(body, `id="rsvp-form" method="POST" action="/rsvp?lang=fr" class="signup-unified" style="display:none"`) {
		t.Error("the form must be visible so the person can change the answer")
	}

	w = getRequest(h, "/e/"+e.Slug+"?lang=fr&rsvp="+strings.Repeat("0", 32))
	if strings.Contains(w.Body.String(), "alice@test.com") {
		t.Error("an unknown rsvp token must not prefill anything")
	}
}

func TestRSVPThrottleKeyedPerEmail(t *testing.T) {
	app := testApp(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.Clock = func() time.Time { return now }
	e := seedAttendanceEvent(t, app.DB)
	UpsertAttendance(app.DB, e.ID, "A", "B", "a@b.co", "", true, "")
	UpsertAttendance(app.DB, e.ID, "C", "D", "c@d.co", "", true, "")
	h := newMux(app)
	postForm(h, "/rsvp?lang=en", rsvpForm(e.ID, "a@b.co", "yes", ""))
	postForm(h, "/rsvp?lang=en", rsvpForm(e.ID, "c@d.co", "yes", ""))
	if n := app.Email.(*fakeEmailSender).count(); n != 2 {
		t.Errorf("each address gets its own link: got %d emails", n)
	}
	now = now.Add(11 * time.Minute)
	postForm(h, "/rsvp?lang=en", rsvpForm(e.ID, "a@b.co", "yes", ""))
	if n := app.Email.(*fakeEmailSender).count(); n != 3 {
		t.Errorf("allowed again after 10 minutes: got %d emails", n)
	}
}
