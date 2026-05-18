package main

import (
	"database/sql"
	"testing"
)

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
