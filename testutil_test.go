package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

// testDB creates an in-memory SQLite database with the schema applied.
// It returns the db and a cleanup function.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testApp creates an App backed by an in-memory DB for handler tests.
func testApp(t *testing.T) *App {
	t.Helper()
	db := testDB(t)
	return &App{
		DB:            db,
		AdminPassword: "testpass",
		Email:         &fakeEmailSender{},
		SNSSkipVerify: true,
		// EmailSendDelay: 0 and AsyncEmail: false (zero values) — reveal emails
		// send synchronously in tests, so no goroutine races with t.Cleanup.
	}
}

// seedEvent creates an event and returns it.
func seedEvent(t *testing.T, db *sql.DB) *Event {
	t.Helper()
	e := &Event{TitleFR: "Test Event", TitleEN: "Test Event", EventDate: "2026-06-15"}
	if err := CreateEvent(db, e); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return e
}

// seedTask creates a task under the given event, optionally with max_slots.
func seedTask(t *testing.T, db *sql.DB, eventID int64, titleFR string, maxSlots *int64) *Task {
	t.Helper()
	tk := &Task{EventID: eventID, TitleFR: titleFR, TitleEN: titleFR}
	if maxSlots != nil {
		tk.MaxSlots = sql.NullInt64{Int64: *maxSlots, Valid: true}
	}
	if err := CreateTask(db, tk); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return tk
}

func int64Ptr(v int64) *int64 { return &v }

// sentEmail records one email handed to fakeEmailSender.
type sentEmail struct {
	To          string
	Subject     string
	HTML        string
	MessageID   string
	Attachments []string // filenames of any attachments
}

// fakeEmailSender records emails for assertions. failUntil makes the next N
// Send calls fail (used to exercise retry logic).
type fakeEmailSender struct {
	mu        sync.Mutex
	sent      []sentEmail
	failUntil int
}

func (f *fakeEmailSender) Send(ctx context.Context, to, subject, htmlBody string, attachments ...emailAttachment) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failUntil > 0 {
		f.failUntil--
		return "", fmt.Errorf("fake email failure")
	}
	id := fmt.Sprintf("fake-msg-%d", len(f.sent)+1)
	var names []string
	for _, a := range attachments {
		names = append(names, a.Filename)
	}
	f.sent = append(f.sent, sentEmail{To: to, Subject: subject, HTML: htmlBody, MessageID: id, Attachments: names})
	return id, nil
}

func (f *fakeEmailSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// oldSchemaSQL is the original schema before first_name/last_name migration.
// It has `name TEXT NOT NULL` instead of first_name/last_name.
const oldSchemaSQL = `
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE,
    title_fr TEXT NOT NULL,
    title_en TEXT NOT NULL DEFAULT '',
    description_fr TEXT NOT NULL DEFAULT '',
    description_en TEXT NOT NULL DEFAULT '',
    event_date TEXT NOT NULL,
    event_time TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS task_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    parent_group_id INTEGER REFERENCES task_groups(id) ON DELETE SET NULL,
    title_fr TEXT NOT NULL,
    title_en TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    group_id INTEGER REFERENCES task_groups(id) ON DELETE SET NULL,
    title_fr TEXT NOT NULL,
    title_en TEXT NOT NULL DEFAULT '',
    description_fr TEXT NOT NULL DEFAULT '',
    description_en TEXT NOT NULL DEFAULT '',
    max_slots INTEGER,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS registrations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    email TEXT NOT NULL,
    phone TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_groups_event ON task_groups(event_id);
CREATE INDEX IF NOT EXISTS idx_task_groups_parent ON task_groups(parent_group_id);
CREATE INDEX IF NOT EXISTS idx_tasks_event ON tasks(event_id);
CREATE INDEX IF NOT EXISTS idx_tasks_group ON tasks(group_id);
CREATE INDEX IF NOT EXISTS idx_registrations_task ON registrations(task_id);
CREATE INDEX IF NOT EXISTS idx_registrations_token ON registrations(token);
`

// testOldDB creates an in-memory DB with the old schema (has name column).
func testOldDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(oldSchemaSQL); err != nil {
		t.Fatalf("apply old schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
