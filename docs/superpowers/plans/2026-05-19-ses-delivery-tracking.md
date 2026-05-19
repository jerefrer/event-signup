# SES Delivery Tracking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track the real delivery outcome (sent / delivered / bounced / complaint / rejected) of the app's two Secret Santa emails by receiving AWS SES events via an SNS webhook, and show the status per participant in the admin.

**Architecture:** SES publishes events to an SNS topic via a configuration set; SNS calls a new public `/webhooks/ses` endpoint (SNS-signature-verified). Each email actually sent is recorded as one `email_messages` row, keyed by the SES message ID; the webhook updates that row's status. The admin santa page shows the status of the link email and the reveal email per participant.

**Tech Stack:** Go 1.25, `html/template`, SQLite (`github.com/mattn/go-sqlite3`, CGo), `aws-sdk-go-v2` (`sesv2`), Go stdlib `crypto` for SNS signature verification.

**Spec:** `docs/superpowers/specs/2026-05-19-ses-delivery-tracking-design.md`

**Conventions:**
- Every commit message uses Conventional Commits and ends with the trailer:
  `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- Never run `go build ./...` (it leaves a binary). Use `go vet ./...` to check compilation.
- Run tests with `go test`. Use `-race` where noted.

---

### Task 1: `email_messages` table

**Files:**
- Modify: `schema.sql`
- Test: `email_messages_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `email_messages_test.go`:

```go
package main

import "testing"

func TestEmailMessagesSchema(t *testing.T) {
	db := testDB(t)
	e := seedSantaEvent(t, db)
	p := seedSantaParticipant(t, db, e.ID, "Alice", "alice@test.com", false)
	_, err := db.Exec(`INSERT INTO email_messages (participant_id, kind, ses_message_id, to_email)
		VALUES (?, 'link', 'msg-1', 'alice@test.com')`, p.ID)
	if err != nil {
		t.Fatalf("insert into email_messages: %v", err)
	}
	// the (participant_id, kind) pair is unique
	_, err = db.Exec(`INSERT INTO email_messages (participant_id, kind, ses_message_id, to_email)
		VALUES (?, 'link', 'msg-2', 'alice@test.com')`, p.ID)
	if err == nil {
		t.Error("expected UNIQUE(participant_id, kind) to reject a duplicate")
	}
}
```

(`seedSantaEvent` and `seedSantaParticipant` already exist in `santa_test.go`, same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestEmailMessagesSchema -v`
Expected: FAIL — `no such table: email_messages`.

- [ ] **Step 3: Append the table to `schema.sql`**

At the end of `schema.sql`, append:

```sql

CREATE TABLE IF NOT EXISTS email_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    participant_id INTEGER NOT NULL REFERENCES santa_participants(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    ses_message_id TEXT NOT NULL DEFAULT '',
    to_email TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'sent',
    status_detail TEXT NOT NULL DEFAULT '',
    sent_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(participant_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_email_messages_ses_id ON email_messages(ses_message_id);
```

A whole new table needs no `migrateColumn` — `CREATE TABLE IF NOT EXISTS` is a no-op on databases that already have it and creates it on fresh ones.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestEmailMessagesSchema -v`
Expected: PASS. Then `go test ./...` — all existing tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add schema.sql email_messages_test.go
git commit -m "feat(email-tracking): add email_messages table"
```

---

### Task 2: `EmailMessage` model & CRUD

**Files:**
- Modify: `models.go`
- Test: `email_messages_test.go`

- [ ] **Step 1: Write the failing test**

Add to `email_messages_test.go`:

```go
func TestEmailMessageModel(t *testing.T) {
	db := testDB(t)
	e := seedSantaEvent(t, db)
	p := seedSantaParticipant(t, db, e.ID, "Alice", "alice@test.com", false)

	// RecordEmailSent creates a row
	if err := RecordEmailSent(db, p.ID, "link", "msg-1", "alice@test.com"); err != nil {
		t.Fatalf("record: %v", err)
	}
	m, err := GetEmailMessageBySESID(db, "msg-1")
	if err != nil {
		t.Fatalf("get by ses id: %v", err)
	}
	if m.Status != "sent" || m.Kind != "link" || m.ParticipantID != p.ID {
		t.Errorf("unexpected row: %+v", m)
	}

	// RecordEmailSent again for the same (participant, kind) upserts: new message ID, status reset
	if err := RecordEmailSent(db, p.ID, "link", "msg-2", "alice@test.com"); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if _, err := GetEmailMessageBySESID(db, "msg-1"); err == nil {
		t.Error("old message ID should no longer be findable after re-record")
	}
	m2, _ := GetEmailMessageBySESID(db, "msg-2")
	if m2.Status != "sent" {
		t.Errorf("re-record status = %q, want sent", m2.Status)
	}

	// ApplyEmailEvent advances the status
	if _, err := ApplyEmailEvent(db, "msg-2", "delivered", ""); err != nil {
		t.Fatalf("apply delivered: %v", err)
	}
	m3, _ := GetEmailMessageBySESID(db, "msg-2")
	if m3.Status != "delivered" {
		t.Errorf("status = %q, want delivered", m3.Status)
	}

	// transition rule: a late 'sent' must NOT overwrite 'delivered'
	if _, err := ApplyEmailEvent(db, "msg-2", "sent", ""); err != nil {
		t.Fatalf("apply late sent: %v", err)
	}
	m4, _ := GetEmailMessageBySESID(db, "msg-2")
	if m4.Status != "delivered" {
		t.Error("a late 'sent' event must not downgrade 'delivered'")
	}

	// transition rule: 'bounced' overrides, and a later 'delivered' must NOT override 'bounced'
	ApplyEmailEvent(db, "msg-2", "bounced", "Permanent/General")
	ApplyEmailEvent(db, "msg-2", "delivered", "")
	m5, _ := GetEmailMessageBySESID(db, "msg-2")
	if m5.Status != "bounced" || m5.StatusDetail != "Permanent/General" {
		t.Errorf("bounced must stick: %+v", m5)
	}

	// unknown message ID is a no-op (no error, no row updated)
	updated, err := ApplyEmailEvent(db, "does-not-exist", "delivered", "")
	if err != nil {
		t.Fatalf("apply unknown: %v", err)
	}
	if updated {
		t.Error("ApplyEmailEvent on an unknown message ID should report no update")
	}

	// ListEmailMessages returns the event's messages
	msgs, err := ListEmailMessages(db, e.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("ListEmailMessages = %d, want 1", len(msgs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestEmailMessageModel -v`
Expected: FAIL — build error, `undefined: RecordEmailSent`, etc.

- [ ] **Step 3: Implement the model in `models.go`**

Add at the end of `models.go`:

```go
// ---- Email delivery tracking ----

type EmailMessage struct {
	ID            int64
	ParticipantID int64
	Kind          string // "link" or "reveal"
	SESMessageID  string
	ToEmail       string
	Status        string // sent | delivered | bounced | complaint | rejected
	StatusDetail  string
	SentAt        time.Time
	UpdatedAt     time.Time
}

const emailMessageCols = "id, participant_id, kind, ses_message_id, to_email, status, status_detail, sent_at, updated_at"

func scanEmailMessage(row interface{ Scan(...any) error }) (*EmailMessage, error) {
	m := &EmailMessage{}
	err := row.Scan(&m.ID, &m.ParticipantID, &m.Kind, &m.SESMessageID, &m.ToEmail,
		&m.Status, &m.StatusDetail, &m.SentAt, &m.UpdatedAt)
	return m, err
}

// emailStatusRank orders statuses so a later, less-significant event cannot
// overwrite a more-significant one (e.g. a late "delivered" cannot erase a
// "bounced"). A higher rank wins.
func emailStatusRank(status string) int {
	switch status {
	case "delivered":
		return 1
	case "rejected":
		return 2
	case "bounced":
		return 3
	case "complaint":
		return 4
	default: // "sent" and anything unknown
		return 0
	}
}

// RecordEmailSent inserts (or, for an existing (participant, kind) pair, resets)
// the email_messages row for an email just handed to the sender.
func RecordEmailSent(db *sql.DB, participantID int64, kind, sesMessageID, toEmail string) error {
	_, err := db.Exec(`INSERT INTO email_messages (participant_id, kind, ses_message_id, to_email, status, status_detail, sent_at, updated_at)
		VALUES (?, ?, ?, ?, 'sent', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(participant_id, kind) DO UPDATE SET
			ses_message_id=excluded.ses_message_id,
			to_email=excluded.to_email,
			status='sent', status_detail='',
			sent_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP`,
		participantID, kind, sesMessageID, toEmail)
	return err
}

func GetEmailMessageBySESID(db *sql.DB, sesMessageID string) (*EmailMessage, error) {
	return scanEmailMessage(db.QueryRow("SELECT "+emailMessageCols+" FROM email_messages WHERE ses_message_id=?", sesMessageID))
}

// ApplyEmailEvent updates the status of the email_messages row matching the SES
// message ID, honouring the transition rule (a lower-rank status never
// overwrites a higher-rank one). Returns whether a row was updated. An unknown
// message ID is a silent no-op.
func ApplyEmailEvent(db *sql.DB, sesMessageID, status, detail string) (bool, error) {
	var current string
	err := db.QueryRow("SELECT status FROM email_messages WHERE ses_message_id=?", sesMessageID).Scan(&current)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if emailStatusRank(status) < emailStatusRank(current) {
		return false, nil
	}
	if _, err := db.Exec("UPDATE email_messages SET status=?, status_detail=?, updated_at=CURRENT_TIMESTAMP WHERE ses_message_id=?",
		status, detail, sesMessageID); err != nil {
		return false, err
	}
	return true, nil
}

// ListEmailMessages returns every email_messages row for an event's participants.
func ListEmailMessages(db *sql.DB, eventID int64) ([]EmailMessage, error) {
	rows, err := db.Query(`SELECT `+emailMessageCols+`
		FROM email_messages em JOIN santa_participants sp ON em.participant_id = sp.id
		WHERE sp.event_id = ?`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []EmailMessage
	for rows.Next() {
		m, err := scanEmailMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, *m)
	}
	return msgs, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestEmailMessageModel -v` — expect PASS. Then `go test ./...` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add models.go email_messages_test.go
git commit -m "feat(email-tracking): add EmailMessage model and CRUD"
```

---

### Task 3: `EmailSender.Send` returns the SES message ID

This is a signature refactor: `Send` gains a `messageID` return value and `SESSender` gains a configuration set. No new behaviour yet (callers ignore the ID — Task 4 uses it). Verification is the existing suite staying green.

**Files:**
- Modify: `email.go`, `main.go`, `testutil_test.go`

- [ ] **Step 1: Change the `EmailSender` interface and `LogSender` in `email.go`**

Change the interface:

```go
// EmailSender delivers a single transactional HTML email and returns the
// provider's message ID (used to correlate later delivery events).
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) (messageID string, err error)
}
```

Change `LogSender.Send`:

```go
func (LogSender) Send(ctx context.Context, to, subject, htmlBody string) (string, error) {
	log.Printf("[email] to=%s subject=%q\n%s", to, subject, htmlBody)
	return "", nil
}
```

- [ ] **Step 2: Change `SESSender` in `email.go` (config set + message ID)**

Replace the `SESSender` struct, `NewSESSender`, and `SESSender.Send` with:

```go
// SESSender sends email through AWS SES (SESv2 API). Credentials and region are
// read from the standard AWS environment (AWS_REGION, AWS_ACCESS_KEY_ID, ...).
type SESSender struct {
	client    *sesv2.Client
	from      string
	configSet string // SES configuration set; enables delivery-event publishing
}

func NewSESSender(ctx context.Context, from, configSet string) (*SESSender, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESSender{client: sesv2.NewFromConfig(cfg), from: from, configSet: configSet}, nil
}

func (s *SESSender) Send(ctx context.Context, to, subject, htmlBody string) (string, error) {
	in := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination:      &sesv2types.Destination{ToAddresses: []string{to}},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{Data: aws.String(subject), Charset: aws.String("UTF-8")},
				Body: &sesv2types.Body{
					Html: &sesv2types.Content{Data: aws.String(htmlBody), Charset: aws.String("UTF-8")},
				},
			},
		},
	}
	if s.configSet != "" {
		in.ConfigurationSetName = aws.String(s.configSet)
	}
	out, err := s.client.SendEmail(ctx, in)
	if err != nil {
		return "", fmt.Errorf("ses send email: %w", err)
	}
	if out.MessageId == nil {
		return "", nil
	}
	return *out.MessageId, nil
}
```

- [ ] **Step 3: Update the callers in `email.go`**

`sendWithRetry` — change its signature to return the message ID:

```go
// sendWithRetry retries a transient send failure up to 3 attempts.
func (app *App) sendWithRetry(to, subject, htmlBody string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			// EmailSendDelay doubles as the retry backoff — adequate at this
			// app's scale; a dedicated retry-delay field would be over-engineering.
			time.Sleep(app.EmailSendDelay)
		}
		messageID, err := app.Email.Send(context.Background(), to, subject, htmlBody)
		if err == nil {
			return messageID, nil
		}
		lastErr = err
	}
	return "", lastErr
}
```

In `sendRevealEmails`, the call site currently is `if err := app.sendWithRetry(p.Email, subject, htmlBody); err != nil {`. Change it to ignore the ID for now:

```go
		if _, err := app.sendWithRetry(p.Email, subject, htmlBody); err != nil {
			log.Printf("sendRevealEmails: send to %s failed: %v", p.Email, err)
			continue
		}
```

- [ ] **Step 4: Update `handleSantaRegister` in `handlers.go`**

The call site currently is `if err := app.Email.Send(r.Context(), p.Email, subject, htmlBody); err != nil {`. Change it to ignore the ID for now:

```go
	if _, err := app.Email.Send(r.Context(), p.Email, subject, htmlBody); err != nil {
		log.Printf("santa link email error: %v", err)
		pd := app.newPageData(r, map[string]any{"Event": event})
		pd.Error = T("santa_email_error", lang)
		app.render(w, r, "public_santa.html", pd)
		return
	}
```

- [ ] **Step 5: Update `fakeEmailSender` in `testutil_test.go`**

Add a `MessageID` field to `sentEmail`:

```go
// sentEmail records one email handed to fakeEmailSender.
type sentEmail struct {
	To        string
	Subject   string
	HTML      string
	MessageID string
}
```

Change `fakeEmailSender.Send` to return a unique synthetic message ID:

```go
func (f *fakeEmailSender) Send(ctx context.Context, to, subject, htmlBody string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failUntil > 0 {
		f.failUntil--
		return "", fmt.Errorf("fake email failure")
	}
	id := fmt.Sprintf("fake-msg-%d", len(f.sent)+1)
	f.sent = append(f.sent, sentEmail{To: to, Subject: subject, HTML: htmlBody, MessageID: id})
	return id, nil
}
```

- [ ] **Step 6: Update `main.go`**

Add the configuration-set env var. After the `emailFrom := os.Getenv("EVENT_SIGNUP_EMAIL_FROM")` line, add a read of the config set, and pass it to `NewSESSender`. The email-sender block becomes:

```go
	emailFrom := os.Getenv("EVENT_SIGNUP_EMAIL_FROM")
	emailConfigSet := os.Getenv("EVENT_SIGNUP_SES_CONFIGURATION_SET")
	var emailSender EmailSender
	if emailFrom != "" {
		s, err := NewSESSender(context.Background(), emailFrom, emailConfigSet)
		if err != nil {
			log.Fatalf("Failed to initialize SES: %v", err)
		}
		emailSender = s
		log.Printf("Email: AWS SES (from %s)", emailFrom)
	} else {
		emailSender = LogSender{}
		log.Println("Email: EVENT_SIGNUP_EMAIL_FROM not set — emails will be logged, not sent")
	}
```

- [ ] **Step 7: Verify**

Run: `go vet ./...` — expect clean.
Run: `go test ./...` — expect all tests still PASS, the same count as after Task 2 (this signature refactor adds no test of its own).

- [ ] **Step 8: Commit**

```bash
git add email.go main.go testutil_test.go handlers.go
git commit -m "refactor(email): EmailSender.Send returns the SES message ID"
```

---

### Task 4: Record sends into `email_messages`

**Files:**
- Modify: `handlers.go` (`handleSantaRegister`), `email.go` (`sendRevealEmails`)
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `santa_handlers_test.go`:

```go
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

	app.sendRevealEmails(e.ID)

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestSantaRegisterRecordsLinkEmail|TestSendRevealEmailsRecordsRevealEmails' -v`
Expected: FAIL — the `email_messages` rows are not created (no recording yet).

- [ ] **Step 3: Record the link email in `handleSantaRegister`**

In `handlers.go`, in `handleSantaRegister`, the send block currently is:

```go
	if _, err := app.Email.Send(r.Context(), p.Email, subject, htmlBody); err != nil {
		log.Printf("santa link email error: %v", err)
		pd := app.newPageData(r, map[string]any{"Event": event})
		pd.Error = T("santa_email_error", lang)
		app.render(w, r, "public_santa.html", pd)
		return
	}
	pd := app.newPageData(r, map[string]any{"Event": event, "LinkSent": true})
	app.render(w, r, "public_santa.html", pd)
```

Replace it with:

```go
	messageID, err := app.Email.Send(r.Context(), p.Email, subject, htmlBody)
	if err != nil {
		log.Printf("santa link email error: %v", err)
		pd := app.newPageData(r, map[string]any{"Event": event})
		pd.Error = T("santa_email_error", lang)
		app.render(w, r, "public_santa.html", pd)
		return
	}
	if err := RecordEmailSent(app.DB, p.ID, "link", messageID, p.Email); err != nil {
		log.Printf("santa link email record error: %v", err)
	}
	pd := app.newPageData(r, map[string]any{"Event": event, "LinkSent": true})
	app.render(w, r, "public_santa.html", pd)
```

(`err` is already declared earlier in the function; `messageID, err :=` is valid because `messageID` is new.)

- [ ] **Step 4: Record the reveal email in `sendRevealEmails`**

In `email.go`, in `sendRevealEmails`, the send block currently is:

```go
		if _, err := app.sendWithRetry(p.Email, subject, htmlBody); err != nil {
			log.Printf("sendRevealEmails: send to %s failed: %v", p.Email, err)
			continue
		}
		if err := MarkRevealEmailSent(app.DB, p.ID); err != nil {
			log.Printf("sendRevealEmails: mark sent %d: %v", p.ID, err)
		}
```

Replace it with:

```go
		messageID, err := app.sendWithRetry(p.Email, subject, htmlBody)
		if err != nil {
			log.Printf("sendRevealEmails: send to %s failed: %v", p.Email, err)
			continue
		}
		if err := MarkRevealEmailSent(app.DB, p.ID); err != nil {
			log.Printf("sendRevealEmails: mark sent %d: %v", p.ID, err)
		}
		if err := RecordEmailSent(app.DB, p.ID, "reveal", messageID, p.Email); err != nil {
			log.Printf("sendRevealEmails: record %d: %v", p.ID, err)
		}
```

(`err` is already declared earlier in `sendRevealEmails`; `messageID, err :=` is valid because `messageID` is new. `MarkRevealEmailSent` is kept — `santa_participants.email_sent_at` remains the "already sent, skip on resend" flag; `email_messages` is the additional delivery-status record.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -run 'TestSantaRegisterRecordsLinkEmail|TestSendRevealEmailsRecordsRevealEmails' -v` — expect PASS.
Then `go test ./...` — all PASS. Then `go vet ./...` — clean.

- [ ] **Step 6: Commit**

```bash
git add handlers.go email.go santa_handlers_test.go
git commit -m "feat(email-tracking): record sent emails in email_messages"
```

---

### Task 5: SNS signature verification

**Files:**
- Create: `webhook.go`, `webhook_test.go`

- [ ] **Step 1: Write the failing tests — create `webhook_test.go`**

```go
package main

import (
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
	if got := snsCanonicalString(env); !strings.Contains(got, "Subject\nHi\n") {
		t.Error("Subject should be included when present")
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
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestSNS|TestVerifySNS' -v`
Expected: FAIL — build error, `undefined: snsEnvelope`, etc.

- [ ] **Step 3: Create `webhook.go`**

```go
package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// snsEnvelope is the outer JSON message Amazon SNS POSTs to the webhook.
type snsEnvelope struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	SubscribeURL     string `json:"SubscribeURL"`
	Token            string `json:"Token"`
}

// snsCanonicalString builds the exact string SNS signs, per the documented
// field order (different for confirmation messages and notifications).
func snsCanonicalString(env snsEnvelope) string {
	var b strings.Builder
	add := func(k, v string) {
		b.WriteString(k)
		b.WriteByte('\n')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	switch env.Type {
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		add("Message", env.Message)
		add("MessageId", env.MessageId)
		add("SubscribeURL", env.SubscribeURL)
		add("Timestamp", env.Timestamp)
		add("Token", env.Token)
		add("TopicArn", env.TopicArn)
		add("Type", env.Type)
	default: // Notification
		add("Message", env.Message)
		add("MessageId", env.MessageId)
		if env.Subject != "" {
			add("Subject", env.Subject)
		}
		add("Timestamp", env.Timestamp)
		add("TopicArn", env.TopicArn)
		add("Type", env.Type)
	}
	return b.String()
}

var snsCertCache sync.Map // SigningCertURL -> *rsa.PublicKey

func snsSigningKey(certURL string) (*rsa.PublicKey, error) {
	if v, ok := snsCertCache.Load(certURL); ok {
		return v.(*rsa.PublicKey), nil
	}
	resp, err := http.Get(certURL)
	if err != nil {
		return nil, fmt.Errorf("fetch SNS cert: %w", err)
	}
	defer resp.Body.Close()
	pemBytes, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read SNS cert: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("SNS cert: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse SNS cert: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("SNS cert: not an RSA public key")
	}
	snsCertCache.Store(certURL, pub)
	return pub, nil
}

// verifySNSMessage validates the cryptographic signature of an SNS message. The
// SigningCertURL must be an https URL on an amazonaws.com host.
func verifySNSMessage(env snsEnvelope) error {
	u, err := url.Parse(env.SigningCertURL)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".amazonaws.com") {
		return fmt.Errorf("invalid SigningCertURL %q", env.SigningCertURL)
	}
	pub, err := snsSigningKey(env.SigningCertURL)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("decode SNS signature: %w", err)
	}
	canonical := []byte(snsCanonicalString(env))
	if env.SignatureVersion == "2" {
		h := sha256.Sum256(canonical)
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
	}
	h := sha1.Sum(canonical)
	return rsa.VerifyPKCS1v15(pub, crypto.SHA1, h[:], sig)
}
```

Note: `verifySNSMessage`'s RSA path is thin glue over the Go standard library. The unit tests cover the app's own logic — the canonical-string construction and the cert-URL validation. The end-to-end RSA verification is exercised by real SNS traffic in production (the webhook handler tests in Task 6 bypass it via a flag).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestSNS|TestVerifySNS' -v` — expect PASS. Then `go test ./...` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add webhook.go webhook_test.go
git commit -m "feat(email-tracking): add SNS message signature verification"
```

---

### Task 6: SES webhook handler

**Files:**
- Modify: `webhook.go` (handler), `handlers.go` (`App` struct), `main.go` (route), `handlers_test.go` (`newMux`), `testutil_test.go` (`testApp`)
- Test: `webhook_test.go`

- [ ] **Step 1: Write the failing tests**

Change the `webhook_test.go` import block to:

```go
import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)
```

Append to `webhook_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestSESWebhook -v`
Expected: FAIL — build error, `undefined: app.SNSSkipVerify` / `app.handleSESWebhook`.

- [ ] **Step 3: Add the `SNSSkipVerify` field to the `App` struct in `handlers.go`**

The `App` struct currently ends with the `sending sync.Map` field. Add one field:

```go
type App struct {
	DB            *sql.DB
	AdminPassword string
	BaseURL       string
	AnthropicKey  string

	Email          EmailSender
	EmailSendDelay time.Duration // pause between reveal emails (rate limiting)
	AsyncEmail     bool          // true in production: reveal emails sent in a goroutine
	sending        sync.Map      // event ID -> bool, guards concurrent reveal sends
	SNSSkipVerify  bool          // true in tests: skip SNS signature verification
}
```

- [ ] **Step 4: Add the handler to `webhook.go`**

Change the `webhook.go` import block to add `encoding/json` and `log`:

```go
import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)
```

Append to `webhook.go`:

```go
// sesEvent is the SES event JSON carried inside an SNS Notification's Message.
type sesEvent struct {
	EventType        string `json:"eventType"`
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID string `json:"messageId"`
	} `json:"mail"`
	Bounce *struct {
		BounceType    string `json:"bounceType"`
		BounceSubType string `json:"bounceSubType"`
	} `json:"bounce"`
	Complaint *struct {
		ComplaintFeedbackType string `json:"complaintFeedbackType"`
	} `json:"complaint"`
	Reject *struct {
		Reason string `json:"reason"`
	} `json:"reject"`
}

// handleSESWebhook receives Amazon SNS deliveries of SES events. It is public
// (SNS cannot authenticate) but every message's signature is verified.
func (app *App) handleSESWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !app.SNSSkipVerify {
		if err := verifySNSMessage(env); err != nil {
			log.Printf("SES webhook: signature verification failed: %v", err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	switch env.Type {
	case "SubscriptionConfirmation":
		if env.SubscribeURL != "" {
			if _, err := http.Get(env.SubscribeURL); err != nil {
				log.Printf("SES webhook: subscription confirmation failed: %v", err)
			} else {
				log.Printf("SES webhook: SNS subscription confirmed")
			}
		}
	case "Notification":
		app.handleSESEvent(env.Message)
	}
	w.WriteHeader(http.StatusOK)
}

// handleSESEvent parses one SES event and updates the matching email_messages row.
func (app *App) handleSESEvent(message string) {
	var ev sesEvent
	if err := json.Unmarshal([]byte(message), &ev); err != nil {
		log.Printf("SES webhook: cannot parse event: %v", err)
		return
	}
	eventType := ev.EventType
	if eventType == "" {
		eventType = ev.NotificationType
	}
	var status, detail string
	switch eventType {
	case "Send":
		status = "sent"
	case "Delivery":
		status = "delivered"
	case "Bounce":
		status = "bounced"
		if ev.Bounce != nil {
			detail = ev.Bounce.BounceType + "/" + ev.Bounce.BounceSubType
		}
	case "Complaint":
		status = "complaint"
		if ev.Complaint != nil {
			detail = ev.Complaint.ComplaintFeedbackType
		}
	case "Reject":
		status = "rejected"
		if ev.Reject != nil {
			detail = ev.Reject.Reason
		}
	default:
		return // unknown / uninteresting event type
	}
	if ev.Mail.MessageID == "" {
		return
	}
	if _, err := ApplyEmailEvent(app.DB, ev.Mail.MessageID, status, detail); err != nil {
		log.Printf("SES webhook: apply event %s/%s: %v", ev.Mail.MessageID, status, err)
	}
}
```

- [ ] **Step 5: Register the route**

In `main.go`, in the `// Public routes` section, add (it must NOT be wrapped in `requireAdmin`):

```go
	mux.HandleFunc("/webhooks/ses", app.handleSESWebhook)
```

In `handlers_test.go`, in `newMux`, add the same line before `return mux`:

```go
	mux.HandleFunc("/webhooks/ses", app.handleSESWebhook)
	return mux
```

- [ ] **Step 6: Make tests skip signature verification**

In `testutil_test.go`, in `testApp`, add `SNSSkipVerify: true`:

```go
	return &App{
		DB:            db,
		AdminPassword: "testpass",
		BaseURL:       "http://localhost:8090",
		Email:         &fakeEmailSender{},
		SNSSkipVerify: true,
		// EmailSendDelay: 0 and AsyncEmail: false (zero values) — reveal emails
		// send synchronously in tests, so no goroutine races with t.Cleanup.
	}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test -run TestSESWebhook -v` — expect PASS. Then `go test ./...` — all PASS. Then `go vet ./...` — clean.

- [ ] **Step 8: Commit**

```bash
git add webhook.go handlers.go main.go handlers_test.go testutil_test.go webhook_test.go
git commit -m "feat(email-tracking): add SES delivery-event webhook"
```

---

### Task 7: Admin display of delivery status

**Files:**
- Modify: `handlers.go` (`santaAdminData`), `i18n.go`, `templates/admin_santa.html`
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing test**

Append to `santa_handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestAdminSantaShowsEmailStatus -v`
Expected: FAIL — the page does not contain the bounced label (no status column yet).

- [ ] **Step 3: Add i18n keys to `i18n.go`**

In the `translations` map, after the existing `santa_admin_*` block (the line `"santa_admin_view": …`), add:

```go
	"santa_admin_link_email_col":   {"fr": "Email lien", "en": "Link email"},
	"santa_admin_reveal_email_col": {"fr": "Email tirage", "en": "Draw email"},
	"santa_admin_email_problems":   {"fr": "problème(s) d'envoi", "en": "delivery problem(s)"},
	"email_status_sent":            {"fr": "Envoyé", "en": "Sent"},
	"email_status_delivered":       {"fr": "Remis", "en": "Delivered"},
	"email_status_bounced":         {"fr": "Rebond", "en": "Bounced"},
	"email_status_complaint":       {"fr": "Plainte", "en": "Complaint"},
	"email_status_rejected":        {"fr": "Rejeté", "en": "Rejected"},
```

- [ ] **Step 4: Update `santaAdminData` in `handlers.go`**

Replace the whole `santaAdminData` function with:

```go
// santaAdminData builds the data map for the admin santa page.
func (app *App) santaAdminData(event *Event) map[string]any {
	participants, _ := ListSantaParticipants(app.DB, event.ID)
	total, completed := CountSantaParticipants(app.DB, event.ID)
	byID := make(map[int64]SantaParticipant, len(participants))
	sentCount := 0
	for _, p := range participants {
		byID[p.ID] = p
		if p.EmailSentAt.Valid {
			sentCount++
		}
	}
	linkStatus := map[int64]EmailMessage{}
	revealStatus := map[int64]EmailMessage{}
	revealProblems := 0
	msgs, _ := ListEmailMessages(app.DB, event.ID)
	for _, m := range msgs {
		switch m.Kind {
		case "link":
			linkStatus[m.ParticipantID] = m
		case "reveal":
			revealStatus[m.ParticipantID] = m
			if m.Status == "bounced" || m.Status == "complaint" || m.Status == "rejected" {
				revealProblems++
			}
		}
	}
	return map[string]any{
		"Event":          event,
		"Participants":   participants,
		"ByID":           byID,
		"Total":          total,
		"Completed":      completed,
		"Pending":        total - completed,
		"Drawn":          event.SantaDrawnAt.Valid,
		"SentCount":      sentCount,
		"LinkStatus":     linkStatus,
		"RevealStatus":   revealStatus,
		"RevealProblems": revealProblems,
	}
}
```

- [ ] **Step 5: Update `templates/admin_santa.html`**

**5a.** At the very top of the file, before `{{define "content"}}`, add the status-badge sub-template:

```html
{{define "santa-email-badge"}}
{{- if eq .Status "delivered"}}<span class="badge badge-success">{{t "email_status_delivered"}}</span>
{{- else if eq .Status "bounced"}}<span class="badge badge-danger" title="{{.StatusDetail}}">{{t "email_status_bounced"}}</span>
{{- else if eq .Status "complaint"}}<span class="badge badge-danger" title="{{.StatusDetail}}">{{t "email_status_complaint"}}</span>
{{- else if eq .Status "rejected"}}<span class="badge badge-danger" title="{{.StatusDetail}}">{{t "email_status_rejected"}}</span>
{{- else if eq .Status "sent"}}<span class="badge badge-info">{{t "email_status_sent"}}</span>
{{- else}}<span style="color:#999;">&mdash;</span>
{{- end}}
{{- end}}
```

**5b.** In the variable block at the top of `{{define "content"}}` (the `{{$… := index $data "…"}}` lines), add after `{{$sentCount := index $data "SentCount"}}`:

```html
{{$linkStatus := index $data "LinkStatus"}}
{{$revealStatus := index $data "RevealStatus"}}
{{$revealProblems := index $data "RevealProblems"}}
```

**5c.** Replace the reveal-email count line. Currently:

```html
        <p><strong>{{$sentCount}}</strong> / {{$completed}} {{t "santa_admin_emails_sent"}}</p>
```

with:

```html
        <p><strong>{{$sentCount}}</strong> / {{$completed}} {{t "santa_admin_emails_sent"}}{{if gt $revealProblems 0}} · <span class="badge badge-danger">{{$revealProblems}} {{t "santa_admin_email_problems"}}</span>{{end}}</p>
```

**5d.** Replace the table header row. Currently:

```html
                    <tr>
                        <th>{{t "registration_last_name"}}</th>
                        <th>{{t "registration_first_name"}}</th>
                        <th>{{t "registration_email"}}</th>
                        <th>{{t "santa_wish_buy"}}</th>
                        <th>{{t "santa_wish_make"}}</th>
                        <th>{{t "santa_wish_free"}}</th>
                        <th>{{t "santa_admin_completed_col"}}</th>
                        {{if $drawn}}<th>{{t "santa_admin_assigned_to"}}</th><th>{{t "santa_admin_email_sent_col"}}</th>{{else}}<th></th>{{end}}
                    </tr>
```

with:

```html
                    <tr>
                        <th>{{t "registration_last_name"}}</th>
                        <th>{{t "registration_first_name"}}</th>
                        <th>{{t "registration_email"}}</th>
                        <th>{{t "santa_wish_buy"}}</th>
                        <th>{{t "santa_wish_make"}}</th>
                        <th>{{t "santa_wish_free"}}</th>
                        <th>{{t "santa_admin_completed_col"}}</th>
                        <th>{{t "santa_admin_link_email_col"}}</th>
                        {{if $drawn}}<th>{{t "santa_admin_assigned_to"}}</th><th>{{t "santa_admin_reveal_email_col"}}</th>{{else}}<th></th>{{end}}
                    </tr>
```

**5e.** Replace the table body row. Currently:

```html
                    {{range $participants}}
                    <tr>
                        <td>{{.LastName}}</td>
                        <td>{{.FirstName}}</td>
                        <td>{{.Email}}</td>
                        <td>{{.WishBuy}}</td>
                        <td>{{.WishMake}}</td>
                        <td>{{.WishFree}}</td>
                        <td>{{if .CompletedAt.Valid}}<span class="badge badge-success">{{t "santa_admin_yes"}}</span>{{else}}<span class="badge badge-danger">{{t "santa_admin_no"}}</span>{{end}}</td>
                        {{if $drawn}}
                        <td>{{if .AssignedToID.Valid}}{{$r := index $byID .AssignedToID.Int64}}{{$r.FirstName}} {{$r.LastName}}{{end}}</td>
                        <td>{{if .EmailSentAt.Valid}}<span class="badge badge-success">{{t "santa_admin_yes"}}</span>{{else}}<span class="badge badge-danger">{{t "santa_admin_no"}}</span>{{end}}</td>
                        {{else}}
                        <td>
                            <form method="POST" action="/admin/santa/participant/delete?lang={{lang}}" class="inline-form" onsubmit="return confirm('{{t "santa_admin_delete_confirm"}}')">
                                <input type="hidden" name="id" value="{{.ID}}">
                                <input type="hidden" name="event_id" value="{{$event.ID}}">
                                <button type="submit" class="btn btn-sm btn-danger"><i class="fa-solid fa-trash"></i></button>
                            </form>
                        </td>
                        {{end}}
                    </tr>
                    {{end}}
```

with:

```html
                    {{range $participants}}
                    <tr>
                        <td>{{.LastName}}</td>
                        <td>{{.FirstName}}</td>
                        <td>{{.Email}}</td>
                        <td>{{.WishBuy}}</td>
                        <td>{{.WishMake}}</td>
                        <td>{{.WishFree}}</td>
                        <td>{{if .CompletedAt.Valid}}<span class="badge badge-success">{{t "santa_admin_yes"}}</span>{{else}}<span class="badge badge-danger">{{t "santa_admin_no"}}</span>{{end}}</td>
                        <td>{{template "santa-email-badge" (index $linkStatus .ID)}}</td>
                        {{if $drawn}}
                        <td>{{if .AssignedToID.Valid}}{{$r := index $byID .AssignedToID.Int64}}{{$r.FirstName}} {{$r.LastName}}{{end}}</td>
                        <td>{{template "santa-email-badge" (index $revealStatus .ID)}}</td>
                        {{else}}
                        <td>
                            <form method="POST" action="/admin/santa/participant/delete?lang={{lang}}" class="inline-form" onsubmit="return confirm('{{t "santa_admin_delete_confirm"}}')">
                                <input type="hidden" name="id" value="{{.ID}}">
                                <input type="hidden" name="event_id" value="{{$event.ID}}">
                                <button type="submit" class="btn btn-sm btn-danger"><i class="fa-solid fa-trash"></i></button>
                            </form>
                        </td>
                        {{end}}
                    </tr>
                    {{end}}
```

(`index $linkStatus .ID` returns the zero `EmailMessage` when the participant has no row for that kind — the badge sub-template renders a muted "—" for an empty status.)

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -run TestAdminSantaShowsEmailStatus -v` — expect PASS. Then `go test ./...` — all PASS. Then `go vet ./...` — clean.

- [ ] **Step 7: Commit**

```bash
git add handlers.go i18n.go templates/admin_santa.html santa_handlers_test.go
git commit -m "feat(email-tracking): show per-participant delivery status in admin"
```

---

### Task 8: Final verification & AWS deployment checklist

**Files:** none modified (verification only)

- [ ] **Step 1: Tidy, vet, full race test run**

Run: `go mod tidy` — expect no changes (this feature adds only stdlib + already-present `aws-sdk-go-v2`).
Run: `go vet ./...` — expect no output.
Run: `go test -race ./...` — expect `ok  event-signup`, all tests PASS, no race warnings.

- [ ] **Step 2: Smoke test**

Run the app: `EVENT_SIGNUP_ADMIN_PASSWORD=test EVENT_SIGNUP_DATABASE_PATH=/tmp/ses-smoke.db EVENT_SIGNUP_PORT=8099 go run .` (no SES env — `LogSender`, `SNSSkipVerify` is false).

Verify:
1. `POST /webhooks/ses` with a body and no valid SNS signature → **403** (route is wired and signature verification is active). A 404 would mean the route is missing.
2. `POST /webhooks/ses` is reachable (not behind admin auth) — it returns 403, not a redirect to `/admin/login`.
3. Admin: create a Secret Santa event, register a participant — the admin santa page (`/admin/event/santa`) shows a "Link email" column with the email status (`Envoyé` with `LogSender`, since no SES events arrive).

Then stop the app and remove `/tmp/ses-smoke.db*`.

- [ ] **Step 3: Commit any tidy changes**

```bash
git add go.mod go.sum
git commit -m "chore(email-tracking): tidy module dependencies"
```

(Skip this commit if `go mod tidy` produced no changes — it should produce none.)

---

## AWS Deployment Checklist (run at deploy time — NOT a dev-session step)

Run **after** the new code is deployed and `https://evenements.chanteloube.fr/webhooks/ses` is live. All commands use the `chanteloube` AWS profile (see the spec §12) and `<region>` = the region where `chanteloube.fr` is verified in SES. The controller may run these once the profile exists; otherwise the user runs them.

1. **Create the SNS topic:**
   ```
   aws sns create-topic --name event-signup-ses-events --profile chanteloube --region <region>
   ```
   Note the returned `TopicArn`.

2. **Allow SES to publish to the topic** — set a topic policy (`<topic-arn>` and `<account-id>` from step 1 / `aws sts get-caller-identity`):
   ```
   aws sns set-topic-attributes --topic-arn <topic-arn> --attribute-name Policy --profile chanteloube --region <region> --attribute-value '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ses.amazonaws.com"},"Action":"SNS:Publish","Resource":"<topic-arn>","Condition":{"StringEquals":{"AWS:SourceAccount":"<account-id>"}}}]}'
   ```

3. **Create the SES configuration set:**
   ```
   aws sesv2 create-configuration-set --configuration-set-name event-signup --profile chanteloube --region <region>
   ```

4. **Create the event destination** (config set → SNS topic):
   ```
   aws sesv2 create-configuration-set-event-destination --configuration-set-name event-signup --event-destination-name sns-events --profile chanteloube --region <region> --event-destination '{"Enabled":true,"SnsDestination":{"TopicArn":"<topic-arn>"},"MatchingEventTypes":["SEND","DELIVERY","BOUNCE","COMPLAINT","REJECT"]}'
   ```

5. **Subscribe the webhook** (the deployed app auto-confirms the subscription):
   ```
   aws sns subscribe --topic-arn <topic-arn> --protocol https --notification-endpoint https://evenements.chanteloube.fr/webhooks/ses --profile chanteloube --region <region>
   ```

6. **Enable the config set in the app**: set `EVENT_SIGNUP_SES_CONFIGURATION_SET=event-signup` in the app's environment and restart. (`EVENT_SIGNUP_EMAIL_FROM` and the AWS credentials must also be the `chanteloube` account's.)

All of these create new, distinctly-named resources — nothing existing in the account is modified. If any command fails with `AccessDenied`, the IAM user lacks SES/SNS admin rights and the policy must be widened or the command run from the console.

---

## Self-Review Notes

**Spec coverage** — every spec section maps to a task: `email_messages` table (T1), model + transition rule (T2), `EmailSender.Send` message-ID + config set (T3), recording sends (T4), SNS signature verification (T5), webhook handler + event parsing (T6), admin display + i18n (T7), AWS config (T8 checklist). Edge cases: unknown message ID (T2/T6 tests), out-of-order events (T2 test), upsert on re-send (T2 test), LogSender empty ID (T3 — returns "", harmless), participant delete cascade (schema FK, T1).

**Deviation from spec** — none material. `LogSender.Send` returns `""` as the message ID (dev mode has no SES events to correlate); `RecordEmailSent` still creates the row at status `sent`, as the spec describes.

**Type consistency** — `EmailMessage` fields, `RecordEmailSent(db, participantID, kind, sesMessageID, toEmail)`, `ApplyEmailEvent(db, sesMessageID, status, detail) (bool, error)`, `GetEmailMessageBySESID`, `ListEmailMessages`, `EmailSender.Send(...) (string, error)`, `snsEnvelope`, `snsCanonicalString`, `verifySNSMessage`, `App.SNSSkipVerify` are used identically across all tasks. Statuses `sent`/`delivered`/`bounced`/`complaint`/`rejected` and kinds `link`/`reveal` are consistent throughout.


