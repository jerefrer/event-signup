package main

import (
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSNSCanonicalStringNotification(t *testing.T) {
	env := snsEnvelope{
		Type:      "Notification",
		MessageId: "m-1",
		TopicArn:  "arn:topic",
		Message:   "hello",
		Timestamp: "2026-05-19T00:00:00.000Z",
	}
	want := "Message\nhello\nMessageId\nm-1\nTimestamp\n2026-05-19T00:00:00.000Z\nTopicArn\narn:topic\nType\nNotification\n"
	if got := snsCanonicalString(env); got != want {
		t.Errorf("canonical string =\n%q\nwant\n%q", got, want)
	}
	env.Subject = "Hi"
	wantWithSubject := "Message\nhello\nMessageId\nm-1\nSubject\nHi\nTimestamp\n2026-05-19T00:00:00.000Z\nTopicArn\narn:topic\nType\nNotification\n"
	if got := snsCanonicalString(env); got != wantWithSubject {
		t.Errorf("canonical string with Subject =\n%q\nwant\n%q", got, wantWithSubject)
	}
}

func TestSNSCanonicalStringSubscriptionConfirmation(t *testing.T) {
	env := snsEnvelope{
		Type:         "SubscriptionConfirmation",
		MessageId:    "m-2",
		TopicArn:     "arn:topic",
		Message:      "confirm me",
		SubscribeURL: "https://sns.example/confirm",
		Timestamp:    "2026-05-19T00:00:00.000Z",
		Token:        "tok",
	}
	want := "Message\nconfirm me\nMessageId\nm-2\nSubscribeURL\nhttps://sns.example/confirm\n" +
		"Timestamp\n2026-05-19T00:00:00.000Z\nToken\ntok\nTopicArn\narn:topic\nType\nSubscriptionConfirmation\n"
	if got := snsCanonicalString(env); got != want {
		t.Errorf("canonical string =\n%q\nwant\n%q", got, want)
	}
}

func TestVerifySNSMessageRejectsBadCertURL(t *testing.T) {
	if err := verifySNSMessage(snsEnvelope{Type: "Notification", SigningCertURL: "https://evil.example.com/cert.pem"}); err == nil {
		t.Error("expected rejection of a non-amazonaws SigningCertURL")
	}
	if err := verifySNSMessage(snsEnvelope{Type: "Notification", SigningCertURL: "http://sns.eu-west-1.amazonaws.com/cert.pem"}); err == nil {
		t.Error("expected rejection of a non-https SigningCertURL")
	}
	// a non-SNS amazonaws.com host (e.g. an attacker-controlled S3 bucket) must be rejected
	if err := verifySNSMessage(snsEnvelope{Type: "Notification", SigningCertURL: "https://evil-bucket.s3.amazonaws.com/cert.pem"}); err == nil {
		t.Error("expected rejection of a non-SNS amazonaws.com host")
	}
}

// postRaw POSTs a raw (non-form) body, as SNS does.
func postRaw(mux http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestSESWebhookUpdatesStatus(t *testing.T) {
	app := testApp(t) // testApp sets SNSSkipVerify = true
	e := seedSantaEvent(t, app.DB)
	p := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	if err := RecordEmailSent(app.DB, p.ID, "link", "ses-abc", "alice@test.com"); err != nil {
		t.Fatal(err)
	}
	mux := newMux(app)
	notification := `{"Type":"Notification","Message":"{\"eventType\":\"Bounce\",\"mail\":{\"messageId\":\"ses-abc\"},\"bounce\":{\"bounceType\":\"Permanent\",\"bounceSubType\":\"General\"}}"}`
	w := postRaw(mux, "/webhooks/ses", notification)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	m, err := GetEmailMessageBySESID(app.DB, "ses-abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.Status != "bounced" || m.StatusDetail != "Permanent/General" {
		t.Errorf("after bounce event: status=%q detail=%q", m.Status, m.StatusDetail)
	}
}

func TestSESWebhookUnknownMessageID(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)
	notification := `{"Type":"Notification","Message":"{\"eventType\":\"Delivery\",\"mail\":{\"messageId\":\"never-seen\"}}"}`
	w := postRaw(mux, "/webhooks/ses", notification)
	if w.Code != 200 {
		t.Errorf("an unknown message ID should still return 200, got %d", w.Code)
	}
}

func TestSESWebhookSubscriptionConfirmation(t *testing.T) {
	app := testApp(t)
	confirmed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		confirmed = true
	}))
	defer srv.Close()
	mux := newMux(app)
	body := `{"Type":"SubscriptionConfirmation","SubscribeURL":"` + srv.URL + `"}`
	w := postRaw(mux, "/webhooks/ses", body)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !confirmed {
		t.Error("the webhook should have called SubscribeURL to confirm the subscription")
	}
}

func TestSESWebhookRejectsGET(t *testing.T) {
	app := testApp(t)
	mux := newMux(app)
	req := httptest.NewRequest(http.MethodGet, "/webhooks/ses", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /webhooks/ses = %d, want 405", w.Code)
	}
}

func TestSESWebhookRejectsBadSignature(t *testing.T) {
	app := testApp(t)
	app.SNSSkipVerify = false // exercise real signature verification
	mux := newMux(app)
	// valid JSON envelope, but no SigningCertURL -> verification must fail
	w := postRaw(mux, "/webhooks/ses", `{"Type":"Notification","Message":"{}"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("unsigned message = %d, want 403", w.Code)
	}
}

func TestSNSCertHostMustBeExactlySNSRegion(t *testing.T) {
	for host, want := range map[string]bool{
		"sns.eu-west-1.amazonaws.com":          true,
		"sns.us-east-1.amazonaws.com":          true,
		"sns.eu-west-1.amazonaws.com.evil.com": false,
		"sns.evil.s3.amazonaws.com":            false,
		"sns.a.b.amazonaws.com":                false,
		"sns..amazonaws.com":                   false,
		"sns.EU_west.amazonaws.com":            false,
		"xsns.eu-west-1.amazonaws.com":         false,
		"evil.com/sns.eu-west-1.amazonaws.com": false,
		"sns.eu-west-1.amazonaws.com.":         false,
	} {
		if got := snsHostPattern.MatchString(host); got != want {
			t.Errorf("snsHostPattern(%q) = %v, want %v", host, got, want)
		}
	}
	err := verifySNSMessage(snsEnvelope{Type: "Notification", SigningCertURL: "https://sns.evil.s3.amazonaws.com/cert.pem"})
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Errorf("nested sns.* host must be rejected on the host check, got %v", err)
	}
}

func TestSESWebhookRejectsForeignTopic(t *testing.T) {
	app := testApp(t)
	app.SNSTopicARN = "arn:aws:sns:eu-west-1:123:ours"
	e := seedSantaEvent(t, app.DB)
	p := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	RecordEmailSent(app.DB, p.ID, "link", "ses-abc", "alice@test.com")
	mux := newMux(app)

	foreign := `{"Type":"Notification","TopicArn":"arn:aws:sns:eu-west-1:999:theirs","Message":"{\"eventType\":\"Bounce\",\"mail\":{\"messageId\":\"ses-abc\"}}"}`
	if w := postRaw(mux, "/webhooks/ses", foreign); w.Code != http.StatusForbidden {
		t.Errorf("foreign topic: status = %d, want 403", w.Code)
	}
	if m, _ := GetEmailMessageBySESID(app.DB, "ses-abc"); m.Status != "sent" {
		t.Errorf("a foreign-topic message must not update status, got %q", m.Status)
	}

	own := strings.Replace(foreign, "999:theirs", "123:ours", 1)
	if w := postRaw(mux, "/webhooks/ses", own); w.Code != 200 {
		t.Errorf("own topic: status = %d, want 200", w.Code)
	}
	if m, _ := GetEmailMessageBySESID(app.DB, "ses-abc"); m.Status != "bounced" {
		t.Errorf("own-topic message should apply, got %q", m.Status)
	}
}

func TestSESWebhookDoesNotConfirmForeignSubscription(t *testing.T) {
	app := testApp(t)
	app.SNSTopicARN = "arn:aws:sns:eu-west-1:123:ours"
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()

	body := `{"Type":"SubscriptionConfirmation","TopicArn":"arn:aws:sns:eu-west-1:999:theirs","SubscribeURL":"` + srv.URL + `"}`
	if w := postRaw(newMux(app), "/webhooks/ses", body); w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if called {
		t.Error("a foreign topic's SubscribeURL must never be fetched")
	}
}

func TestSNSCertCacheIsBounded(t *testing.T) {
	var c snsCertCache
	for i := 0; i < 50; i++ {
		c.put(fmt.Sprintf("https://sns.eu-west-1.amazonaws.com/%d.pem", i), &rsa.PublicKey{})
	}
	if n := c.size(); n > snsCertCacheMax {
		t.Errorf("cache holds %d entries, cap is %d", n, snsCertCacheMax)
	}
	c.put("https://sns.eu-west-1.amazonaws.com/new.pem", &rsa.PublicKey{})
	if _, ok := c.get("https://sns.eu-west-1.amazonaws.com/new.pem"); !ok {
		t.Error("the newest entry must be cached")
	}
}
