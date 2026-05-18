package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSantaPublicPage(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	mux := newMux(app)
	w := getRequest(mux, "/e/"+e.Slug+"?lang=fr")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `name="email"`) {
		t.Error("expected registration form on santa public page")
	}
}

func TestSantaRegister(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	mux := newMux(app)
	w := postForm(mux, "/santa/register?lang=fr", url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {"alice@test.com"},
	})
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), T("santa_link_sent", LangFR)) {
		t.Error("expected 'link sent' confirmation")
	}
	p, err := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com")
	if err != nil {
		t.Fatalf("participant not created: %v", err)
	}
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 1 {
		t.Fatalf("expected 1 link email, got %d", fake.count())
	}
	if fake.sent[0].To != "alice@test.com" {
		t.Errorf("email sent to %q", fake.sent[0].To)
	}
	if !strings.Contains(fake.sent[0].HTML, p.Token) {
		t.Error("link email should contain the participant token")
	}
}

func TestSantaRegisterMissingFields(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	mux := newMux(app)
	w := postForm(mux, "/santa/register?lang=fr", url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {""},
		"email":      {"alice@test.com"},
	})
	if !strings.Contains(w.Body.String(), "alert-error") {
		t.Error("expected validation error for missing field")
	}
}

func TestSantaEditFlow(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	mux := newMux(app)

	w := getRequest(mux, "/santa/edit?token="+p.Token+"&lang=fr")
	if w.Code != 200 {
		t.Fatalf("GET status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `name="wish_buy"`) {
		t.Error("expected wishes form")
	}

	w2 := getRequest(mux, "/santa/edit?token=bogus&lang=fr")
	if !strings.Contains(w2.Body.String(), "alert-error") {
		t.Error("expected error for invalid token")
	}

	w3 := postForm(mux, "/santa/edit?lang=fr", url.Values{
		"token":     {p.Token},
		"wish_buy":  {"un stylo"},
		"wish_make": {"un poeme"},
		"wish_free": {"une surprise"},
	})
	if w3.Code != 200 {
		t.Fatalf("POST status = %d", w3.Code)
	}
	done, _ := GetSantaParticipantByToken(app.DB, p.Token)
	if !done.CompletedAt.Valid || done.WishBuy != "un stylo" {
		t.Errorf("wishes not saved: %+v", done)
	}

	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", false)
	w4 := postForm(mux, "/santa/edit?lang=fr", url.Values{
		"token":     {p2.Token},
		"wish_buy":  {"x"},
		"wish_make": {""},
		"wish_free": {"z"},
	})
	if !strings.Contains(w4.Body.String(), "alert-error") {
		t.Error("expected validation error for a missing wish")
	}
	notDone, _ := GetSantaParticipantByToken(app.DB, p2.Token)
	if notDone.CompletedAt.Valid {
		t.Error("an incomplete submission must not mark the list completed")
	}
}

func TestSantaClosedAfterDraw(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})
	mux := newMux(app)

	w := postForm(mux, "/santa/register?lang=fr", url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Carol"},
		"last_name":  {"X"},
		"email":      {"carol@test.com"},
	})
	if !strings.Contains(w.Body.String(), T("santa_closed", LangFR)) {
		t.Error("registration should be closed after the draw")
	}
	w2 := getRequest(mux, "/santa/edit?token="+p1.Token+"&lang=fr")
	if !strings.Contains(w2.Body.String(), T("santa_closed", LangFR)) {
		t.Error("editing should be closed after the draw")
	}
}

func TestSantaRegisterEmailFailure(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	app.Email.(*fakeEmailSender).failUntil = 1
	mux := newMux(app)
	w := postForm(mux, "/santa/register?lang=fr", url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {"alice@test.com"},
	})
	if !strings.Contains(w.Body.String(), "alert-error") {
		t.Error("expected an error when the link email fails to send")
	}
	// The participant row is committed before the email send, so it persists.
	if _, err := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com"); err != nil {
		t.Error("participant row should exist even when the email fails")
	}
}

func TestSendRevealEmails(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	p3 := seedSantaParticipant(t, app.DB, e.ID, "Carol", "carol@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p3.ID, p3.ID: p1.ID})

	app.sendRevealEmails(e.ID)

	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 3 {
		t.Fatalf("expected 3 reveal emails, got %d", fake.count())
	}
	var aliceMail *sentEmail
	for i := range fake.sent {
		if fake.sent[i].To == "alice@test.com" {
			aliceMail = &fake.sent[i]
		}
	}
	if aliceMail == nil {
		t.Fatal("Alice received no email")
	}
	if !strings.Contains(aliceMail.HTML, "Bob") || !strings.Contains(aliceMail.HTML, p2.WishBuy) {
		t.Error("Alice's email should reveal Bob and Bob's wishes")
	}
	for _, id := range []int64{p1.ID, p2.ID, p3.ID} {
		got, _ := GetSantaParticipant(app.DB, id)
		if !got.EmailSentAt.Valid {
			t.Errorf("participant %d not marked email_sent", id)
		}
	}
}

func TestSendRevealEmailsRetry(t *testing.T) {
	app := testApp(t)
	fake := app.Email.(*fakeEmailSender)
	fake.failUntil = 2 // first 2 Send calls fail, then succeed
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})

	app.sendRevealEmails(e.ID)
	if fake.count() != 2 {
		t.Fatalf("expected both emails delivered after retry, got %d", fake.count())
	}
	for _, id := range []int64{p1.ID, p2.ID} {
		got, _ := GetSantaParticipant(app.DB, id)
		if !got.EmailSentAt.Valid {
			t.Errorf("participant %d not marked sent after retry", id)
		}
	}
}

func TestSendRevealEmailsPermanentFailure(t *testing.T) {
	app := testApp(t)
	fake := app.Email.(*fakeEmailSender)
	fake.failUntil = 3 // exhausts all 3 retry attempts for the first participant
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})

	app.sendRevealEmails(e.ID)

	// Alice's 3 attempts all fail -> not marked sent; Bob (processed next) still succeeds.
	got1, _ := GetSantaParticipant(app.DB, p1.ID)
	if got1.EmailSentAt.Valid {
		t.Error("p1 should not be marked sent after exhausting all retries")
	}
	got2, _ := GetSantaParticipant(app.DB, p2.ID)
	if !got2.EmailSentAt.Valid {
		t.Error("p2 should still be emailed even though p1 permanently failed")
	}
	if fake.count() != 1 {
		t.Errorf("expected exactly 1 successful email (Bob), got %d", fake.count())
	}
}

func TestResendSkipsAlreadySent(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})

	app.sendRevealEmails(e.ID)
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 2 {
		t.Fatalf("first pass: got %d emails, want 2", fake.count())
	}
	app.sendRevealEmails(e.ID) // both already sent
	if fake.count() != 2 {
		t.Errorf("resend should skip already-sent participants, got %d total", fake.count())
	}
}
