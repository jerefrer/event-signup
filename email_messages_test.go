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
