package main

import (
	"fmt"
	"net/http"
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

	app.sendRevealEmails(e.ID, "http://localhost:8090")

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

	app.sendRevealEmails(e.ID, "http://localhost:8090")
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

	app.sendRevealEmails(e.ID, "http://localhost:8090")

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

	app.sendRevealEmails(e.ID, "http://localhost:8090")
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 2 {
		t.Fatalf("first pass: got %d emails, want 2", fake.count())
	}
	app.sendRevealEmails(e.ID, "http://localhost:8090") // both already sent
	if fake.count() != 2 {
		t.Errorf("resend should skip already-sent participants, got %d total", fake.count())
	}
}

func TestAdminSantaPage(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	mux := newMux(app)
	w := getRequest(mux, fmt.Sprintf("/admin/event/santa?id=%d&lang=fr", e.ID), adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), T("santa_admin_draw_btn", LangFR)) {
		t.Error("expected the draw button on the admin santa page")
	}
}

func TestAdminSantaDraw(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	mux := newMux(app)

	w := postForm(mux, "/admin/santa/draw?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	ev, _ := GetEvent(app.DB, e.ID)
	if !ev.SantaDrawnAt.Valid {
		t.Error("event should be marked drawn")
	}
	g1, _ := GetSantaParticipant(app.DB, p1.ID)
	if !g1.AssignedToID.Valid {
		t.Error("p1 should have an assignment")
	}
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 2 {
		t.Errorf("expected 2 reveal emails, got %d", fake.count())
	}
	g2, _ := GetSantaParticipant(app.DB, p2.ID)
	if !g2.AssignedToID.Valid {
		t.Error("p2 should have an assignment")
	}

	postForm(mux, "/admin/santa/draw?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if fake.count() != 2 {
		t.Errorf("second draw should not send more emails, got %d", fake.count())
	}
}

func TestAdminSantaDrawTooFew(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true) // only 1 completed
	mux := newMux(app)
	w := postForm(mux, "/admin/santa/draw?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	ev, _ := GetEvent(app.DB, e.ID)
	if ev.SantaDrawnAt.Valid {
		t.Error("draw must not run with fewer than 2 completed lists")
	}
}

func TestAdminSantaResend(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})
	MarkRevealEmailSent(app.DB, p1.ID) // p1 already emailed
	mux := newMux(app)
	w := postForm(mux, "/admin/santa/resend?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 1 {
		t.Errorf("resend should email only the 1 unsent participant, got %d", fake.count())
	}
}

func TestAdminSantaParticipantDelete(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	mux := newMux(app)
	w := postForm(mux, "/admin/santa/participant/delete?lang=fr", url.Values{
		"id":       {fmt.Sprint(p.ID)},
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if _, err := GetSantaParticipant(app.DB, p.ID); err == nil {
		t.Error("participant should be deleted before the draw")
	}
}

func TestAdminSantaParticipantDeleteAfterDrawRefused(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})
	mux := newMux(app)
	postForm(mux, "/admin/santa/participant/delete?lang=fr", url.Values{
		"id":       {fmt.Sprint(p1.ID)},
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if _, err := GetSantaParticipant(app.DB, p1.ID); err != nil {
		t.Error("participant must NOT be deleted after the draw")
	}
}

func TestAdminSantaResendBeforeDraw(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	mux := newMux(app)
	w := postForm(mux, "/admin/santa/resend?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 0 {
		t.Errorf("resend before the draw must send nothing, got %d", fake.count())
	}
	if strings.Contains(w.Body.String(), T("santa_admin_resend_done", LangFR)) {
		t.Error("resend before the draw must not show the 'resend in progress' message")
	}
}

func TestSantaRegisterRecordsLinkEmail(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	mux := newMux(app)
	postForm(mux, "/santa/register?lang=fr", url.Values{
		"event_id":   {fmt.Sprint(e.ID)},
		"first_name": {"Alice"},
		"last_name":  {"Dupont"},
		"email":      {"alice@test.com"},
	})
	p, err := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com")
	if err != nil {
		t.Fatalf("participant not created: %v", err)
	}
	fake := app.Email.(*fakeEmailSender)
	if len(fake.sent) != 1 {
		t.Fatalf("expected 1 email, got %d", len(fake.sent))
	}
	m, err := GetEmailMessageBySESID(app.DB, fake.sent[0].MessageID)
	if err != nil {
		t.Fatalf("email_messages row not recorded: %v", err)
	}
	if m.Kind != "link" || m.ParticipantID != p.ID || m.Status != "sent" {
		t.Errorf("unexpected email_messages row: %+v", m)
	}
}

func TestSendRevealEmailsRecordsRevealEmails(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})

	app.sendRevealEmails(e.ID, "http://localhost:8090")

	msgs, err := ListEmailMessages(app.DB, e.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 reveal email_messages rows, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.Kind != "reveal" {
			t.Errorf("kind = %q, want reveal", m.Kind)
		}
		if m.SESMessageID == "" {
			t.Error("reveal email_messages row has no SES message ID")
		}
	}
}

func TestAdminSantaShowsEmailStatus(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	if err := RecordEmailSent(app.DB, p.ID, "link", "ses-x", "alice@test.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyEmailEvent(app.DB, "ses-x", "bounced", "Permanent/General"); err != nil {
		t.Fatal(err)
	}
	mux := newMux(app)
	w := getRequest(mux, fmt.Sprintf("/admin/event/santa?id=%d&lang=fr", e.ID), adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), T("email_status_bounced", LangFR)) {
		t.Error("admin santa page should show the bounced link-email status")
	}
}

func TestAdminSantaRevealProblems(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})
	if err := RecordEmailSent(app.DB, p1.ID, "reveal", "ses-r1", "alice@test.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyEmailEvent(app.DB, "ses-r1", "bounced", "Permanent/General"); err != nil {
		t.Fatal(err)
	}
	mux := newMux(app)
	w := getRequest(mux, fmt.Sprintf("/admin/event/santa?id=%d&lang=fr", e.ID), adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	// The template HTML-escapes apostrophes (d'envoi → d&#39;envoi), so check
	// using the escaped form rather than the raw translated string.
	if !strings.Contains(body, "problème(s) d&#39;envoi") {
		t.Error("admin santa page should show the reveal-email problems count after a bounce")
	}
	if !strings.Contains(body, T("email_status_bounced", LangFR)) {
		t.Error("admin santa page should show the bounced reveal-email status")
	}
}

func TestAdminSantaImport(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	mux := newMux(app)

	csv := "email,Nom,Prénom,Langue\nalice@test.com,Dupont,Alice,fr\nbob@test.com,Martin,Bob,en\n,NoEmail,X,fr\n"
	w := postMultipart(mux, "/admin/santa/import?lang=fr", "list.csv", csv,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	ps, _ := ListSantaParticipants(app.DB, e.ID)
	if len(ps) != 2 {
		t.Fatalf("expected 2 imported participants, got %d", len(ps))
	}
	alice, err := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com")
	if err != nil {
		t.Fatalf("alice not imported: %v", err)
	}
	if alice.FirstName != "Alice" || alice.LastName != "Dupont" || alice.Lang != "fr" {
		t.Errorf("alice imported wrong: %+v", alice)
	}
	bob, _ := GetSantaParticipantByEmail(app.DB, e.ID, "bob@test.com")
	if bob.Lang != "en" {
		t.Errorf("bob lang = %q, want en", bob.Lang)
	}

	wantMsg := fmt.Sprintf(T("santa_import_done", LangFR), 2, 0, 1)
	if !strings.Contains(w.Body.String(), wantMsg) {
		t.Errorf("expected import result message %q in the rendered page", wantMsg)
	}
}

func TestAdminSantaImportIdempotent(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	mux := newMux(app)
	csv := "email,Nom,Prénom\nalice@test.com,Dupont,Alice\n"

	postMultipart(mux, "/admin/santa/import?lang=fr", "list.csv", csv,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))
	alice, _ := GetSantaParticipantByEmail(app.DB, e.ID, "alice@test.com")
	if err := SaveSantaWishes(app.DB, alice.Token, "a", "b", "c"); err != nil {
		t.Fatalf("save wishes: %v", err)
	}

	// Re-import the same file.
	postMultipart(mux, "/admin/santa/import?lang=fr", "list.csv", csv,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))

	ps, _ := ListSantaParticipants(app.DB, e.ID)
	if len(ps) != 1 {
		t.Fatalf("re-import created a duplicate: %d participants", len(ps))
	}
	again, _ := GetSantaParticipantByToken(app.DB, alice.Token)
	if !again.CompletedAt.Valid || again.WishBuy != "a" {
		t.Error("re-import must preserve the participant's wishes")
	}
}

func TestSantaSendInviteEmails(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", false)

	app.sendInviteEmails(e.ID, "http://localhost:8090")

	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 2 {
		t.Fatalf("expected 2 invitation emails, got %d", fake.count())
	}
	// Every "link" email carries that participant's edit token.
	found := false
	for _, m := range fake.sent {
		if m.To == "alice@test.com" && strings.Contains(m.HTML, p1.Token) {
			found = true
		}
	}
	if !found {
		t.Error("alice's invitation should contain her edit token")
	}
	// A link email_messages row was recorded for each participant.
	msgs, _ := ListEmailMessages(app.DB, e.ID)
	links := 0
	for _, m := range msgs {
		if m.Kind == "link" {
			links++
		}
	}
	if links != 2 {
		t.Errorf("expected 2 link email_messages rows, got %d", links)
	}

	// A second run sends nothing — everyone already has a link email.
	app.sendInviteEmails(e.ID, "http://localhost:8090")
	if fake.count() != 2 {
		t.Errorf("re-invite must skip already-invited participants, got %d", fake.count())
	}

	// A newly added participant IS picked up by the next run.
	seedSantaParticipant(t, app.DB, e.ID, "Carol", "carol@test.com", false)
	app.sendInviteEmails(e.ID, "http://localhost:8090")
	if fake.count() != 3 {
		t.Errorf("a newly added participant should be invited, got %d", fake.count())
	}
}

func TestAdminSantaImportRejectedAfterDraw(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})
	mux := newMux(app)

	csv := "email,Prénom\ncarol@test.com,Carol\n"
	w := postMultipart(mux, "/admin/santa/import?lang=fr", "list.csv", csv,
		map[string]string{"event_id": fmt.Sprint(e.ID)}, adminCookie(app))
	if !strings.Contains(w.Body.String(), T("santa_import_closed", LangFR)) {
		t.Error("import should be refused once the draw has happened")
	}
	if _, err := GetSantaParticipantByEmail(app.DB, e.ID, "carol@test.com"); err == nil {
		t.Error("no participant should have been imported after the draw")
	}
}

func TestAdminSantaInvite(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", false)
	mux := newMux(app)

	w := postForm(mux, "/admin/santa/invite?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 2 {
		t.Fatalf("expected 2 invitation emails, got %d", fake.count())
	}
	if !strings.Contains(w.Body.String(), T("santa_invite_done", LangFR)) {
		t.Error("expected the invitation-sent confirmation message")
	}
}

func TestAdminSantaPageShowsImportAndInvite(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	mux := newMux(app)

	w := getRequest(mux, fmt.Sprintf("/admin/event/santa?id=%d&lang=fr", e.ID), adminCookie(app))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, T("santa_import_drop_hint", LangFR)) {
		t.Error("admin santa page should show the CSV drop zone before the draw")
	}
	if !strings.Contains(body, T("santa_invite_btn", LangFR)) {
		t.Error("admin santa page should show the send-invitations button before the draw")
	}
	if !strings.Contains(body, `action="/admin/santa/import`) {
		t.Error("admin santa page should contain the import form")
	}
	if !strings.Contains(body, `action="/admin/santa/invite`) {
		t.Error("admin santa page should contain the invite form")
	}
}

func TestAdminSantaInviteRejectedAfterDraw(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", true)
	p2 := seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", true)
	SaveSantaDraw(app.DB, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID})
	fake := app.Email.(*fakeEmailSender)
	before := fake.count()
	mux := newMux(app)

	w := postForm(mux, "/admin/santa/invite?lang=fr", url.Values{
		"event_id": {fmt.Sprint(e.ID)},
	}, adminCookie(app))
	if !strings.Contains(w.Body.String(), T("santa_invite_closed", LangFR)) {
		t.Error("invitations should be refused once the draw has happened")
	}
	if fake.count() != before {
		t.Error("no invitation email should be sent after the draw")
	}
}

func TestSantaDisclaimerVisibleEverywhere(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	mux := newMux(app)
	// Unique apostrophe-free fragment of santa_disclaimer (FR); the full string
	// contains apostrophes that html/template escapes to &#39; in the page body,
	// so a raw-string Contains on the full T() value would never match.
	disc := "exercice de DON, pas de réception"

	// 1) Public registration page.
	w := getRequest(mux, "/e/"+e.Slug+"?lang=fr")
	if !strings.Contains(w.Body.String(), disc) {
		t.Error("disclaimer should appear on the public registration page")
	}

	// 2) Wishes-editing page (the universal touchpoint).
	w = getRequest(mux, "/santa/edit?token="+p.Token+"&lang=fr")
	if !strings.Contains(w.Body.String(), disc) {
		t.Error("disclaimer should appear on the wishes-editing page")
	}

	// 3) Magic-link / invitation email body.
	app.sendInviteEmails(e.ID, "http://localhost:8090")
	fake := app.Email.(*fakeEmailSender)
	if fake.count() == 0 {
		t.Fatal("expected the invitation email to be sent")
	}
	if !strings.Contains(fake.sent[0].HTML, disc) {
		t.Error("disclaimer should appear in the invitation email body")
	}
}
