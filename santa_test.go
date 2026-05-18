package main

import (
	"database/sql"
	"testing"
)

func seedSantaEvent(t *testing.T, db *sql.DB) *Event {
	t.Helper()
	e := &Event{TitleFR: "Noël", TitleEN: "Christmas", EventDate: "2026-12-20", EventType: "secret_santa"}
	if err := CreateEvent(db, e); err != nil {
		t.Fatalf("seed santa event: %v", err)
	}
	return e
}

func seedSantaParticipant(t *testing.T, db *sql.DB, eventID int64, firstName, email string, completed bool) *SantaParticipant {
	t.Helper()
	p, err := UpsertSantaParticipant(db, eventID, firstName, "Test", email, "fr")
	if err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	if completed {
		if err := SaveSantaWishes(db, p.Token, "buy-"+firstName, "make-"+firstName, "free-"+firstName); err != nil {
			t.Fatalf("seed wishes: %v", err)
		}
		p, _ = GetSantaParticipantByToken(db, p.Token)
	}
	return p
}

func TestSantaParticipantCRUD(t *testing.T) {
	db := testDB(t)
	e := seedSantaEvent(t, db)

	p, err := UpsertSantaParticipant(db, e.ID, "Alice", "Dupont", "alice@test.com", "fr")
	if err != nil {
		t.Fatalf("upsert create: %v", err)
	}
	if p.ID == 0 || p.Token == "" {
		t.Fatal("expected non-zero id and token")
	}
	if p.CompletedAt.Valid {
		t.Error("new participant should not be completed")
	}

	// Upsert with same email (different case) reuses the row and keeps the token
	p2, err := UpsertSantaParticipant(db, e.ID, "Alice", "Martin", "ALICE@test.com", "en")
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if p2.ID != p.ID {
		t.Error("upsert by email should reuse the existing row")
	}
	if p2.Token != p.Token {
		t.Error("token must be preserved on upsert")
	}
	if p2.LastName != "Martin" || p2.Lang != "en" {
		t.Errorf("update not applied: %+v", p2)
	}

	byTok, err := GetSantaParticipantByToken(db, p.Token)
	if err != nil {
		t.Fatalf("get by token: %v", err)
	}
	if byTok.Email != "alice@test.com" {
		t.Errorf("email = %q", byTok.Email)
	}

	byEmail, err := GetSantaParticipantByEmail(db, e.ID, "Alice@Test.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if byEmail.ID != p.ID {
		t.Error("get by email is not case-insensitive")
	}

	if err := SaveSantaWishes(db, p.Token, "pen", "poem", "surprise"); err != nil {
		t.Fatalf("save wishes: %v", err)
	}
	done, _ := GetSantaParticipantByToken(db, p.Token)
	if !done.CompletedAt.Valid {
		t.Error("completed_at should be set after wishes saved")
	}
	if done.WishBuy != "pen" || done.WishMake != "poem" || done.WishFree != "surprise" {
		t.Errorf("wishes = %+v", done)
	}

	// completed_at must freeze on first completion (COALESCE): re-saving wishes
	// updates the wishes but must NOT move completed_at. Use a sentinel timestamp
	// so the assertion is robust regardless of CURRENT_TIMESTAMP's 1-second resolution.
	if _, err := db.Exec("UPDATE santa_participants SET completed_at='2020-01-01 00:00:00' WHERE id=?", p.ID); err != nil {
		t.Fatalf("set sentinel completed_at: %v", err)
	}
	if err := SaveSantaWishes(db, p.Token, "pen2", "poem2", "surprise2"); err != nil {
		t.Fatalf("re-save wishes: %v", err)
	}
	resaved, err := GetSantaParticipantByToken(db, p.Token)
	if err != nil {
		t.Fatalf("get after re-save: %v", err)
	}
	if resaved.CompletedAt.String != "2020-01-01 00:00:00" {
		t.Errorf("completed_at must be frozen by COALESCE, got %q", resaved.CompletedAt.String)
	}
	if resaved.WishBuy != "pen2" {
		t.Errorf("re-save must still update wishes, wish_buy = %q", resaved.WishBuy)
	}

	// MarkRevealEmailSent records delivery
	if err := MarkRevealEmailSent(db, p.ID); err != nil {
		t.Fatalf("mark reveal email sent: %v", err)
	}
	sent, err := GetSantaParticipant(db, p.ID)
	if err != nil {
		t.Fatalf("get after mark sent: %v", err)
	}
	if !sent.EmailSentAt.Valid {
		t.Error("email_sent_at should be set after MarkRevealEmailSent")
	}

	seedSantaParticipant(t, db, e.ID, "Bob", "bob@test.com", false)
	list, _ := ListSantaParticipants(db, e.ID)
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
	total, completed := CountSantaParticipants(db, e.ID)
	if total != 2 || completed != 1 {
		t.Errorf("count total=%d completed=%d, want 2/1", total, completed)
	}

	if err := DeleteSantaParticipant(db, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetSantaParticipant(db, p.ID); err == nil {
		t.Error("expected error after delete")
	}
}

func TestSantaSchema(t *testing.T) {
	db := testDB(t)
	ev := seedEvent(t, db)
	// santa_participants table must exist and be usable
	_, err := db.Exec(`INSERT INTO santa_participants (event_id, first_name, last_name, email, lang, token)
		VALUES (?, 'A', 'B', 'a@b.com', 'fr', 'tok1')`, ev.ID)
	if err != nil {
		t.Fatalf("insert into santa_participants: %v", err)
	}
	// events must have santa_drawn_at
	var drawn sql.NullString
	if err := db.QueryRow("SELECT santa_drawn_at FROM events LIMIT 1").Scan(&drawn); err != nil && err != sql.ErrNoRows {
		t.Fatalf("select santa_drawn_at: %v", err)
	}
}

func TestSantaDrawnAtMigration(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// events table WITHOUT santa_drawn_at (pre-feature shape)
	if _, err := db.Exec(`CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL UNIQUE,
		title_fr TEXT NOT NULL, title_en TEXT NOT NULL DEFAULT '',
		description_fr TEXT NOT NULL DEFAULT '', description_en TEXT NOT NULL DEFAULT '',
		event_date TEXT NOT NULL, event_time TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL DEFAULT 'tasks',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create events table: %v", err)
	}
	migrateColumn(db, "events", "santa_drawn_at", "ALTER TABLE events ADD COLUMN santa_drawn_at TEXT")
	var v sql.NullString
	if err := db.QueryRow("SELECT santa_drawn_at FROM events LIMIT 1").Scan(&v); err != nil && err != sql.ErrNoRows {
		t.Fatalf("santa_drawn_at missing after migration: %v", err)
	}
}
