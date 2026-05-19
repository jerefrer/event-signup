package main

import "testing"

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
	m2, err := GetEmailMessageBySESID(db, "msg-2")
	if err != nil {
		t.Fatalf("get msg-2: %v", err)
	}
	if m2.Status != "sent" {
		t.Errorf("re-record status = %q, want sent", m2.Status)
	}

	// ApplyEmailEvent advances the status
	if _, err := ApplyEmailEvent(db, "msg-2", "delivered", ""); err != nil {
		t.Fatalf("apply delivered: %v", err)
	}
	m3, err := GetEmailMessageBySESID(db, "msg-2")
	if err != nil {
		t.Fatalf("get msg-2: %v", err)
	}
	if m3.Status != "delivered" {
		t.Errorf("status = %q, want delivered", m3.Status)
	}

	// transition rule: a late 'sent' must NOT overwrite 'delivered'
	if _, err := ApplyEmailEvent(db, "msg-2", "sent", ""); err != nil {
		t.Fatalf("apply late sent: %v", err)
	}
	m4, err := GetEmailMessageBySESID(db, "msg-2")
	if err != nil {
		t.Fatalf("get msg-2: %v", err)
	}
	if m4.Status != "delivered" {
		t.Error("a late 'sent' event must not downgrade 'delivered'")
	}

	// transition rule: 'bounced' overrides, and a later 'delivered' must NOT override 'bounced'
	ApplyEmailEvent(db, "msg-2", "bounced", "Permanent/General")
	ApplyEmailEvent(db, "msg-2", "delivered", "")
	m5, err := GetEmailMessageBySESID(db, "msg-2")
	if err != nil {
		t.Fatalf("get msg-2: %v", err)
	}
	if m5.Status != "bounced" || m5.StatusDetail != "Permanent/General" {
		t.Errorf("bounced must stick: %+v", m5)
	}

	// complaint (highest rank) overrides bounced
	if _, err := ApplyEmailEvent(db, "msg-2", "complaint", "abuse"); err != nil {
		t.Fatalf("apply complaint: %v", err)
	}
	m6, err := GetEmailMessageBySESID(db, "msg-2")
	if err != nil {
		t.Fatalf("get msg-2: %v", err)
	}
	if m6.Status != "complaint" {
		t.Errorf("complaint should override bounced, got %q", m6.Status)
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
