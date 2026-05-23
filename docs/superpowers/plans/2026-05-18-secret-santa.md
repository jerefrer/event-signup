# Secret Santa Event Type — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `secret_santa` event type where participants self-register, fill a 3-wish list via a magic-link, and an admin triggers a draw that emails each person their assigned recipient.

**Architecture:** Follows the existing flat-file Go module. New `email.go` holds the `EmailSender` interface (AWS SES + log implementations) and email rendering. Santa data and the draw algorithm go in `models.go`; handlers in `handlers.go`; new templates in `templates/`. SQLite schema gains one table and one column.

**Tech Stack:** Go 1.25, `html/template`, SQLite (`github.com/mattn/go-sqlite3`, CGo), `aws-sdk-go-v2` (`config` + `service/sesv2`).

**Spec:** `docs/superpowers/specs/2026-05-18-secret-santa-design.md`

**Conventions:**

- Every commit message uses Conventional Commits and ends with the trailer:
  `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- Never run `go build ./...` (it leaves a binary). Use `go vet ./...` to check compilation.
- Run tests with `go test`. Use `-race` where noted.

---

### Task 1: Database schema & migration

**Files:**

- Modify: `schema.sql`
- Modify: `models.go` (Event struct, `eventCols`, `scanEvent`, `InitDB`)
- Test: `santa_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `santa_test.go`:

```go
package main

import (
	"database/sql"
	"math/rand"
	"strings"
	"testing"
)

func TestSantaSchema(t *testing.T) {
	db := testDB(t)
	// santa_participants table must exist and be usable
	_, err := db.Exec(`INSERT INTO santa_participants (event_id, first_name, last_name, email, lang, token)
		VALUES (1, 'A', 'B', 'a@b.com', 'fr', 'tok1')`)
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
	db.Exec(`CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL UNIQUE,
		title_fr TEXT NOT NULL, title_en TEXT NOT NULL DEFAULT '',
		description_fr TEXT NOT NULL DEFAULT '', description_en TEXT NOT NULL DEFAULT '',
		event_date TEXT NOT NULL, event_time TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL DEFAULT 'tasks',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	migrateColumn(db, "events", "santa_drawn_at", "ALTER TABLE events ADD COLUMN santa_drawn_at TEXT")
	var v sql.NullString
	if err := db.QueryRow("SELECT santa_drawn_at FROM events LIMIT 1").Scan(&v); err != nil && err != sql.ErrNoRows {
		t.Fatalf("santa_drawn_at missing after migration: %v", err)
	}
}
```

(The `rand` and `strings` imports are unused now but consumed by Tasks 2-3; leaving them triggers a compile error, so omit them until Task 2. For this task the import block is just `database/sql` and `testing`.)

Use this exact import block for Step 1:

```go
import (
	"database/sql"
	"testing"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestSantaSchema|TestSantaDrawnAtMigration' -v`
Expected: FAIL — `no such table: santa_participants` / `no such column: santa_drawn_at`.

- [ ] **Step 3: Update `schema.sql`**

In the `CREATE TABLE IF NOT EXISTS events` block, add `santa_drawn_at` just before `created_at`:

```sql
    event_type TEXT NOT NULL DEFAULT 'tasks',
    santa_drawn_at TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

At the end of `schema.sql`, append the new table and indexes:

```sql

CREATE TABLE IF NOT EXISTS santa_participants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    first_name TEXT NOT NULL DEFAULT '',
    last_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL,
    lang TEXT NOT NULL DEFAULT 'fr',
    token TEXT NOT NULL UNIQUE,
    wish_buy TEXT NOT NULL DEFAULT '',
    wish_make TEXT NOT NULL DEFAULT '',
    wish_free TEXT NOT NULL DEFAULT '',
    completed_at TEXT,
    assigned_to_id INTEGER REFERENCES santa_participants(id) ON DELETE SET NULL,
    email_sent_at TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_santa_participants_event ON santa_participants(event_id);
CREATE INDEX IF NOT EXISTS idx_santa_participants_token ON santa_participants(token);
```

- [ ] **Step 4: Update `models.go` — Event struct, columns, migration**

In the `Event` struct, add a field just before `CreatedAt`:

```go
	EventType     string // "tasks", "attendance" or "secret_santa"
	SantaDrawnAt  sql.NullString
	CreatedAt     time.Time
```

Change `eventCols` to include `santa_drawn_at` before `created_at`:

```go
const eventCols = "id, slug, title_fr, title_en, description_fr, description_en, event_date, event_time, event_type, santa_drawn_at, created_at"
```

Update `scanEvent` to scan it (between `EventType` and `CreatedAt`):

```go
func scanEvent(row interface{ Scan(...any) error }) (*Event, error) {
	e := &Event{}
	err := row.Scan(&e.ID, &e.Slug, &e.TitleFR, &e.TitleEN, &e.DescriptionFR, &e.DescriptionEN, &e.EventDate, &e.EventTime, &e.EventType, &e.SantaDrawnAt, &e.CreatedAt)
	return e, err
}
```

In `InitDB`, add the migration next to the other `events` migrations (after the `event_type` migration line):

```go
	migrateColumn(db, "events", "event_type", "ALTER TABLE events ADD COLUMN event_type TEXT NOT NULL DEFAULT 'tasks'")
	migrateColumn(db, "events", "santa_drawn_at", "ALTER TABLE events ADD COLUMN santa_drawn_at TEXT")
```

`CreateEvent` and `UpdateEvent` do NOT need changes — `santa_drawn_at` defaults to NULL and is set only by the draw.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -run 'TestSantaSchema|TestSantaDrawnAtMigration' -v`
Expected: PASS. Then `go test ./...` — all existing tests still PASS (the `eventCols`/`scanEvent` change is consistent).

- [ ] **Step 6: Commit**

```bash
git add schema.sql models.go santa_test.go
git commit -m "feat(santa): add santa_participants table and santa_drawn_at column"
```

---

### Task 2: SantaParticipant model & CRUD

**Files:**

- Modify: `models.go` (struct + CRUD functions)
- Test: `santa_test.go`

- [ ] **Step 1: Write the failing test**

Keep the `santa_test.go` import block from Task 1 (`database/sql`, `testing`) unchanged. Add to `santa_test.go` — test helpers (used by this and later tasks) and the CRUD test:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSantaParticipantCRUD -v`
Expected: FAIL — build error, `undefined: SantaParticipant`, `undefined: UpsertSantaParticipant`, etc.

- [ ] **Step 3: Implement the model in `models.go`**

Add at the end of `models.go`:

```go
// ---- Secret Santa ----

type SantaParticipant struct {
	ID           int64
	EventID      int64
	FirstName    string
	LastName     string
	Email        string
	Lang         string
	Token        string
	WishBuy      string
	WishMake     string
	WishFree     string
	CompletedAt  sql.NullString
	AssignedToID sql.NullInt64
	EmailSentAt  sql.NullString
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const santaCols = "id, event_id, first_name, last_name, email, lang, token, wish_buy, wish_make, wish_free, completed_at, assigned_to_id, email_sent_at, created_at, updated_at"

func scanSantaParticipant(row interface{ Scan(...any) error }) (*SantaParticipant, error) {
	p := &SantaParticipant{}
	err := row.Scan(&p.ID, &p.EventID, &p.FirstName, &p.LastName, &p.Email, &p.Lang, &p.Token,
		&p.WishBuy, &p.WishMake, &p.WishFree, &p.CompletedAt, &p.AssignedToID, &p.EmailSentAt,
		&p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// UpsertSantaParticipant creates a participant or, if one already exists for the
// (event, email) pair, updates its name and language while preserving its token,
// wishes and completion state.
func UpsertSantaParticipant(db *sql.DB, eventID int64, firstName, lastName, email, lang string) (*SantaParticipant, error) {
	var id int64
	err := db.QueryRow("SELECT id FROM santa_participants WHERE event_id=? AND LOWER(email)=LOWER(?)", eventID, email).Scan(&id)
	if err == nil {
		if _, err := db.Exec("UPDATE santa_participants SET first_name=?, last_name=?, lang=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
			firstName, lastName, lang, id); err != nil {
			return nil, err
		}
		return GetSantaParticipant(db, id)
	}
	token := GenerateToken()
	res, err := db.Exec("INSERT INTO santa_participants (event_id, first_name, last_name, email, lang, token) VALUES (?, ?, ?, ?, ?, ?)",
		eventID, firstName, lastName, email, lang, token)
	if err != nil {
		return nil, err
	}
	newID, _ := res.LastInsertId()
	return GetSantaParticipant(db, newID)
}

func GetSantaParticipant(db *sql.DB, id int64) (*SantaParticipant, error) {
	return scanSantaParticipant(db.QueryRow("SELECT "+santaCols+" FROM santa_participants WHERE id=?", id))
}

func GetSantaParticipantByToken(db *sql.DB, token string) (*SantaParticipant, error) {
	return scanSantaParticipant(db.QueryRow("SELECT "+santaCols+" FROM santa_participants WHERE token=?", token))
}

func GetSantaParticipantByEmail(db *sql.DB, eventID int64, email string) (*SantaParticipant, error) {
	return scanSantaParticipant(db.QueryRow("SELECT "+santaCols+" FROM santa_participants WHERE event_id=? AND LOWER(email)=LOWER(?)", eventID, email))
}

func ListSantaParticipants(db *sql.DB, eventID int64) ([]SantaParticipant, error) {
	rows, err := db.Query("SELECT "+santaCols+" FROM santa_participants WHERE event_id=? ORDER BY last_name, first_name", eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []SantaParticipant
	for rows.Next() {
		p, err := scanSantaParticipant(rows)
		if err != nil {
			return nil, err
		}
		ps = append(ps, *p)
	}
	return ps, rows.Err()
}

func CountSantaParticipants(db *sql.DB, eventID int64) (total, completed int) {
	db.QueryRow("SELECT COUNT(*) FROM santa_participants WHERE event_id=?", eventID).Scan(&total)
	db.QueryRow("SELECT COUNT(*) FROM santa_participants WHERE event_id=? AND completed_at IS NOT NULL", eventID).Scan(&completed)
	return
}

// SaveSantaWishes stores the three wishes and sets completed_at on first completion.
func SaveSantaWishes(db *sql.DB, token, wishBuy, wishMake, wishFree string) error {
	_, err := db.Exec(`UPDATE santa_participants
		SET wish_buy=?, wish_make=?, wish_free=?,
		    completed_at=COALESCE(completed_at, CURRENT_TIMESTAMP),
		    updated_at=CURRENT_TIMESTAMP
		WHERE token=?`, wishBuy, wishMake, wishFree, token)
	return err
}

func DeleteSantaParticipant(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM santa_participants WHERE id=?", id)
	return err
}

// MarkRevealEmailSent records that the reveal email has been delivered.
func MarkRevealEmailSent(db *sql.DB, id int64) error {
	_, err := db.Exec("UPDATE santa_participants SET email_sent_at=CURRENT_TIMESTAMP WHERE id=?", id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestSantaParticipantCRUD -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add models.go santa_test.go
git commit -m "feat(santa): add SantaParticipant model and CRUD"
```

---

### Task 3: Draw algorithm

**Files:**

- Modify: `models.go` (`DrawSecretSanta` + `math/rand` import)
- Test: `santa_test.go`

- [ ] **Step 1: Write the failing test**

Add `"math/rand"` to the `santa_test.go` import block — it becomes `database/sql`, `math/rand`, `testing`. Then add to `santa_test.go`:

```go
func TestDrawSecretSanta(t *testing.T) {
	ids := []int64{10, 20, 30, 40, 50}
	assignments, err := DrawSecretSanta(ids, rand.New(rand.NewSource(42)))
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if len(assignments) != len(ids) {
		t.Fatalf("got %d assignments, want %d", len(assignments), len(ids))
	}
	for giver, receiver := range assignments {
		if giver == receiver {
			t.Errorf("participant %d assigned to self", giver)
		}
	}
	seen := map[int64]bool{}
	for _, receiver := range assignments {
		if seen[receiver] {
			t.Errorf("receiver %d assigned twice", receiver)
		}
		seen[receiver] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("id %d is never a receiver", id)
		}
		if _, ok := assignments[id]; !ok {
			t.Errorf("id %d is never a giver", id)
		}
	}
	// deterministic with the same seed
	again, _ := DrawSecretSanta(ids, rand.New(rand.NewSource(42)))
	for g, r := range assignments {
		if again[g] != r {
			t.Errorf("not deterministic: giver %d -> %d vs %d", g, r, again[g])
		}
	}
	// N=2 must swap
	two, err := DrawSecretSanta([]int64{1, 2}, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("N=2: %v", err)
	}
	if two[1] != 2 || two[2] != 1 {
		t.Errorf("N=2 should swap, got %v", two)
	}
	// errors for fewer than 2
	if _, err := DrawSecretSanta([]int64{1}, rand.New(rand.NewSource(1))); err == nil {
		t.Error("expected error for N=1")
	}
	if _, err := DrawSecretSanta(nil, rand.New(rand.NewSource(1))); err == nil {
		t.Error("expected error for N=0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDrawSecretSanta -v`
Expected: FAIL — `undefined: DrawSecretSanta`.

- [ ] **Step 3: Implement `DrawSecretSanta` in `models.go`**

Add `"math/rand"` to the `models.go` import block (alphabetically, after `"fmt"`... place near `"sort"`):

```go
import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)
```

Note: `models.go` already imports `crypto/rand` as `rand`. To avoid a name clash, import `math/rand` as `mrand` and use `*mrand.Rand` in the signature.

Add at the end of `models.go`:

```go
// DrawSecretSanta returns a derangement (giver ID -> receiver ID) using
// Sattolo's algorithm, which produces a single random cycle: no participant is
// ever assigned to themselves. Requires at least 2 participants.
func DrawSecretSanta(ids []int64, rng *mrand.Rand) (map[int64]int64, error) {
	if len(ids) < 2 {
		return nil, fmt.Errorf("secret santa draw needs at least 2 participants, got %d", len(ids))
	}
	perm := make([]int64, len(ids))
	copy(perm, ids)
	for i := len(perm) - 1; i > 0; i-- {
		j := rng.Intn(i) // 0 <= j < i
		perm[i], perm[j] = perm[j], perm[i]
	}
	assignments := make(map[int64]int64, len(perm))
	for i := range perm {
		assignments[perm[i]] = perm[(i+1)%len(perm)]
	}
	return assignments, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestDrawSecretSanta -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add models.go santa_test.go
git commit -m "feat(santa): add Sattolo draw algorithm"
```

---

### Task 4: Persist the draw

**Files:**

- Modify: `models.go` (`SaveSantaDraw`)
- Test: `santa_test.go`

- [ ] **Step 1: Write the failing test**

Add to `santa_test.go`:

```go
func TestSaveSantaDraw(t *testing.T) {
	db := testDB(t)
	e := seedSantaEvent(t, db)
	p1 := seedSantaParticipant(t, db, e.ID, "Alice", "alice@t.com", true)
	p2 := seedSantaParticipant(t, db, e.ID, "Bob", "bob@t.com", true)

	if err := SaveSantaDraw(db, e.ID, map[int64]int64{p1.ID: p2.ID, p2.ID: p1.ID}); err != nil {
		t.Fatalf("save draw: %v", err)
	}
	got1, _ := GetSantaParticipant(db, p1.ID)
	if !got1.AssignedToID.Valid || got1.AssignedToID.Int64 != p2.ID {
		t.Errorf("p1 assigned_to = %v, want %d", got1.AssignedToID, p2.ID)
	}
	ev, _ := GetEvent(db, e.ID)
	if !ev.SantaDrawnAt.Valid {
		t.Error("event santa_drawn_at should be set after the draw")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSaveSantaDraw -v`
Expected: FAIL — `undefined: SaveSantaDraw`.

- [ ] **Step 3: Implement `SaveSantaDraw` in `models.go`**

Add at the end of `models.go`:

```go
// SaveSantaDraw persists the assignments and marks the event as drawn, in a
// single transaction.
func SaveSantaDraw(db *sql.DB, eventID int64, assignments map[int64]int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for giver, receiver := range assignments {
		if _, err := tx.Exec("UPDATE santa_participants SET assigned_to_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=? AND event_id=?",
			receiver, giver, eventID); err != nil {
			return fmt.Errorf("save assignment %d: %w", giver, err)
		}
	}
	if _, err := tx.Exec("UPDATE events SET santa_drawn_at=CURRENT_TIMESTAMP WHERE id=?", eventID); err != nil {
		return fmt.Errorf("mark event drawn: %w", err)
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestSaveSantaDraw -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add models.go santa_test.go
git commit -m "feat(santa): persist draw assignments transactionally"
```

---

### Task 5: i18n strings

**Files:**

- Modify: `i18n.go`

This task adds translation keys only — no test (data, verified by later rendering tests).

- [ ] **Step 1: Add keys to `i18n.go`**

In the `translations` map in `i18n.go`, add the `secret_santa` value to the existing event-type group and append a new Secret Santa block. Insert after the `"event_type_attendance"` line:

```go
	"event_type":            {"fr": "Type d'événement", "en": "Event type"},
	"event_type_tasks":      {"fr": "Inscription aux tâches", "en": "Task signup"},
	"event_type_attendance": {"fr": "Présence (RSVP)", "en": "Attendance (RSVP)"},
	"event_type_santa":      {"fr": "Secret Santa", "en": "Secret Santa"},

	// Secret Santa — public
	"santa_register_title":  {"fr": "Inscription au Secret Santa", "en": "Secret Santa Registration"},
	"santa_register_intro":  {"fr": "Inscrivez-vous pour recevoir par email votre lien personnel.", "en": "Sign up to receive your personal link by email."},
	"santa_register_btn":    {"fr": "Recevoir mon lien", "en": "Send me my link"},
	"santa_link_sent":       {"fr": "Un email contenant votre lien personnel vous a été envoyé. Cliquez dessus pour remplir vos souhaits.", "en": "An email with your personal link has been sent. Click it to fill in your wishes."},
	"santa_continue_btn":    {"fr": "Continuer ma liste", "en": "Continue my list"},
	"santa_closed":          {"fr": "Les inscriptions sont closes : le tirage a été effectué.", "en": "Registration is closed: the draw has been done."},
	"santa_email_error":     {"fr": "Impossible d'envoyer l'email. Réessayez ou contactez l'organisateur.", "en": "Could not send the email. Please retry or contact the organizer."},
	"santa_invalid_link":    {"fr": "Ce lien est invalide ou a expiré.", "en": "This link is invalid or has expired."},

	// Secret Santa — wishes form
	"santa_wishes_title":    {"fr": "Ma liste de souhaits", "en": "My wish list"},
	"santa_wish_buy":        {"fr": "Quelque chose qui peut être acheté (moins de 10 €)", "en": "Something that can be bought (under €10)"},
	"santa_wish_buy_hint":   {"fr": "Pour ceux qui n'ont pas le temps — un stylo, des chaussettes, du chocolat…", "en": "For those short on time — a pen, socks, chocolate…"},
	"santa_wish_make":       {"fr": "Quelque chose qui peut être fabriqué ou trouvé", "en": "Something that can be made or found"},
	"santa_wish_make_hint":  {"fr": "Pour ceux qui n'ont pas d'argent — une plante, un plat, un poème, une prière…", "en": "For those who have time — a plant, a dish, a poem, a prayer…"},
	"santa_wish_free":       {"fr": "Quelque chose au choix", "en": "Anything you like"},
	"santa_wish_free_hint":  {"fr": "Ce que vous voulez.", "en": "Whatever you want."},
	"santa_wishes_required": {"fr": "Les trois souhaits sont obligatoires.", "en": "All three wishes are required."},
	"santa_wishes_save":     {"fr": "Enregistrer ma liste", "en": "Save my list"},
	"santa_wishes_saved":    {"fr": "Votre liste a été enregistrée. Revenez la modifier avec votre lien personnel.", "en": "Your list has been saved. Come back to edit it with your personal link."},

	// Secret Santa — admin
	"santa_admin_title":           {"fr": "Secret Santa", "en": "Secret Santa"},
	"santa_admin_participants":    {"fr": "inscrits", "en": "registered"},
	"santa_admin_completed":       {"fr": "ont complété leur liste", "en": "completed their list"},
	"santa_admin_draw_btn":        {"fr": "Mélanger et envoyer", "en": "Shuffle and send"},
	"santa_admin_draw_confirm":    {"fr": "Lancer le tirage et envoyer les emails ? Cette action est irréversible.", "en": "Run the draw and send the emails? This cannot be undone."},
	"santa_admin_reveal_btn":      {"fr": "Révéler la liste", "en": "Reveal the list"},
	"santa_admin_hide_btn":        {"fr": "Masquer la liste", "en": "Hide the list"},
	"santa_admin_resend_btn":      {"fr": "Renvoyer les emails", "en": "Resend the emails"},
	"santa_admin_drawn":           {"fr": "Tirage effectué le", "en": "Draw completed on"},
	"santa_admin_too_few":         {"fr": "Il faut au moins 2 listes complétées pour lancer le tirage.", "en": "At least 2 completed lists are required to run the draw."},
	"santa_admin_pending_warning": {"fr": "liste(s) non complétée(s) — ces personnes seront exclues du tirage.", "en": "incomplete list(s) — these people will be excluded from the draw."},
	"santa_admin_draw_done":       {"fr": "Tirage effectué, envoi des emails en cours.", "en": "Draw completed, sending emails."},
	"santa_admin_resend_done":     {"fr": "Renvoi des emails en cours.", "en": "Resending emails."},
	"santa_admin_emails_sent":     {"fr": "emails envoyés", "en": "emails sent"},
	"santa_admin_assigned_to":     {"fr": "Offre à", "en": "Gives to"},
	"santa_admin_completed_col":   {"fr": "Liste complétée", "en": "List completed"},
	"santa_admin_email_sent_col":  {"fr": "Email envoyé", "en": "Email sent"},
	"santa_admin_delete_confirm":  {"fr": "Supprimer ce participant ?", "en": "Delete this participant?"},
	"santa_admin_no_participants": {"fr": "Aucun inscrit.", "en": "No participants yet."},
	"santa_admin_yes":             {"fr": "Oui", "en": "Yes"},
	"santa_admin_no":              {"fr": "Non", "en": "No"},
	"santa_admin_view":            {"fr": "Gérer le Secret Santa", "en": "Manage Secret Santa"},

	// Secret Santa — emails
	"santa_email_greeting":       {"fr": "Bonjour %s,", "en": "Hello %s,"},
	"santa_email_link_subject":   {"fr": "Votre lien pour", "en": "Your link for"},
	"santa_email_link_title":     {"fr": "Votre Secret Santa", "en": "Your Secret Santa"},
	"santa_email_link_intro":     {"fr": "Cliquez sur le bouton ci-dessous pour remplir votre liste de souhaits.", "en": "Click the button below to fill in your wish list."},
	"santa_email_link_button":    {"fr": "Remplir ma liste", "en": "Fill in my list"},
	"santa_email_reveal_subject": {"fr": "Votre tirage Secret Santa pour", "en": "Your Secret Santa draw for"},
	"santa_email_reveal_title":   {"fr": "Le tirage est fait !", "en": "The draw is done!"},
	"santa_email_reveal_intro":   {"fr": "Vous offrez un cadeau à :", "en": "You are giving a gift to:"},
	"santa_email_reveal_wishes":  {"fr": "Voici ses souhaits :", "en": "Here are their wishes:"},
	"santa_email_reveal_link":    {"fr": "Voir l'événement", "en": "View the event"},
```

- [ ] **Step 2: Verify it compiles**

Run: `go vet ./...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add i18n.go
git commit -m "feat(santa): add FR/EN translations"
```

---

### Task 6: EmailSender interface, LogSender & test fake

**Files:**

- Create: `email.go`
- Modify: `testutil_test.go` (add `fakeEmailSender`, inject into `testApp`)

This task is scaffolding — the interface and `LogSender` have no branching logic, so they are verified by downstream handler tests rather than a dedicated unit test.

- [ ] **Step 1: Create `email.go`**

```go
package main

import (
	"context"
	"log"
)

// EmailSender delivers a single transactional HTML email.
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// LogSender writes emails to the log instead of sending them. Used when SES is
// not configured (development, manual testing).
type LogSender struct{}

func (LogSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	log.Printf("[email] to=%s subject=%q\n%s", to, subject, htmlBody)
	return nil
}
```

- [ ] **Step 2: Add the test fake to `testutil_test.go`**

Add to the import block of `testutil_test.go`:

```go
import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
)
```

Add at the end of `testutil_test.go`:

```go
// sentEmail records one email handed to fakeEmailSender.
type sentEmail struct {
	To      string
	Subject string
	HTML    string
}

// fakeEmailSender records emails for assertions. failUntil makes the next N
// Send calls fail (used to exercise retry logic).
type fakeEmailSender struct {
	mu        sync.Mutex
	sent      []sentEmail
	failUntil int
}

func (f *fakeEmailSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failUntil > 0 {
		f.failUntil--
		return fmt.Errorf("fake email failure")
	}
	f.sent = append(f.sent, sentEmail{To: to, Subject: subject, HTML: htmlBody})
	return nil
}

func (f *fakeEmailSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}
```

`fakeEmailSender` is an unused type after this task (Go allows that — only unused imports/locals are errors). Task 9 wires it into `testApp`. The package still compiles after this task.

- [ ] **Step 3: Commit**

```bash
git add email.go testutil_test.go
git commit -m "feat(santa): add EmailSender interface, LogSender and test fake"
```

---

### Task 7: Email rendering

**Files:**

- Create: `templates/email_santa_link.html`, `templates/email_santa_reveal.html`
- Modify: `email.go` (rendering functions)
- Test: `santa_test.go`

- [ ] **Step 1: Write the failing test**

Add `"strings"` to the `santa_test.go` import block:

```go
import (
	"database/sql"
	"math/rand"
	"strings"
	"testing"
)
```

Add to `santa_test.go`:

```go
func TestRenderSantaEmails(t *testing.T) {
	e := Event{TitleFR: "Noël", TitleEN: "Christmas", Slug: "noel"}
	giver := SantaParticipant{FirstName: "Alice", LastName: "Dupont", Email: "alice@t.com"}
	receiver := SantaParticipant{FirstName: "Bob", LastName: "Martin",
		WishBuy: "un stylo", WishMake: "un poeme", WishFree: "une surprise"}

	subj, html := renderSantaLinkEmail("fr", giver, e, "http://x/santa/edit?token=abc")
	if subj == "" {
		t.Error("link email subject is empty")
	}
	if !strings.Contains(html, "http://x/santa/edit?token=abc") {
		t.Error("link email is missing the edit URL")
	}
	if !strings.Contains(html, "Alice") {
		t.Error("link email is missing the greeting name")
	}

	subj2, html2 := renderSantaRevealEmail("fr", giver, receiver, e, "http://x")
	if subj2 == "" {
		t.Error("reveal email subject is empty")
	}
	for _, want := range []string{"Bob", "Martin", "un stylo", "un poeme", "une surprise", "http://x/e/noel"} {
		if !strings.Contains(html2, want) {
			t.Errorf("reveal email is missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRenderSantaEmails -v`
Expected: FAIL — `undefined: renderSantaLinkEmail`.

- [ ] **Step 3: Create `templates/email_santa_link.html`**

```html
<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8" />
  </head>
  <body
    style="font-family:sans-serif;color:#333;max-width:480px;margin:0 auto;padding:1rem;"
  >
    <h2>{{.Title}}</h2>
    <p>{{.Greeting}}</p>
    <p>{{.Intro}}</p>
    <p style="text-align:center;margin:1.5rem 0;">
      <a
        href="{{.EditURL}}"
        style="background:#c0392b;color:#fff;padding:0.75rem 1.5rem;text-decoration:none;border-radius:4px;"
        >{{.ButtonText}}</a
      >
    </p>
    <p style="color:#888;font-size:0.85rem;">{{.EventTitle}}</p>
  </body>
</html>
```

- [ ] **Step 4: Create `templates/email_santa_reveal.html`**

```html
<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8" />
  </head>
  <body
    style="font-family:sans-serif;color:#333;max-width:480px;margin:0 auto;padding:1rem;"
  >
    <h2>{{.Title}}</h2>
    <p>{{.Greeting}}</p>
    <p>{{.Intro}}</p>
    <p
      style="font-size:1.3rem;font-weight:bold;text-align:center;margin:1rem 0;"
    >
      {{.ReceiverName}}
    </p>
    <p>{{.WishesIntro}}</p>
    <ul>
      <li><strong>{{.WishBuyLabel}}:</strong> {{.WishBuy}}</li>
      <li><strong>{{.WishMakeLabel}}:</strong> {{.WishMake}}</li>
      <li><strong>{{.WishFreeLabel}}:</strong> {{.WishFree}}</li>
    </ul>
    <p style="margin-top:1.5rem;">
      <a href="{{.EventURL}}">{{.EventLinkText}}</a>
    </p>
  </body>
</html>
```

- [ ] **Step 5: Add rendering functions to `email.go`**

Change the `email.go` import block to:

```go
import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
)
```

Add to `email.go`:

```go
type santaLinkEmailData struct {
	Title, Greeting, Intro, ButtonText, EditURL, EventTitle string
}

type santaRevealEmailData struct {
	Title, Greeting, Intro, ReceiverName, WishesIntro          string
	WishBuyLabel, WishMakeLabel, WishFreeLabel                 string
	WishBuy, WishMake, WishFree                                string
	EventURL, EventLinkText                                    string
}

// renderSantaLinkEmail builds the magic-link email in the given language.
func renderSantaLinkEmail(lang string, p SantaParticipant, event Event, editURL string) (subject, html string) {
	eventTitle := Localized(event.TitleFR, event.TitleEN, lang)
	data := santaLinkEmailData{
		Title:      T("santa_email_link_title", lang),
		Greeting:   fmt.Sprintf(T("santa_email_greeting", lang), p.FirstName),
		Intro:      T("santa_email_link_intro", lang),
		ButtonText: T("santa_email_link_button", lang),
		EditURL:    editURL,
		EventTitle: eventTitle,
	}
	return T("santa_email_link_subject", lang) + " " + eventTitle, renderEmailTemplate("email_santa_link.html", data)
}

// renderSantaRevealEmail builds the draw-reveal email in the given language.
func renderSantaRevealEmail(lang string, giver, receiver SantaParticipant, event Event, baseURL string) (subject, html string) {
	eventTitle := Localized(event.TitleFR, event.TitleEN, lang)
	data := santaRevealEmailData{
		Title:         T("santa_email_reveal_title", lang),
		Greeting:      fmt.Sprintf(T("santa_email_greeting", lang), giver.FirstName),
		Intro:         T("santa_email_reveal_intro", lang),
		ReceiverName:  receiver.FirstName + " " + receiver.LastName,
		WishesIntro:   T("santa_email_reveal_wishes", lang),
		WishBuyLabel:  T("santa_wish_buy", lang),
		WishMakeLabel: T("santa_wish_make", lang),
		WishFreeLabel: T("santa_wish_free", lang),
		WishBuy:       receiver.WishBuy,
		WishMake:      receiver.WishMake,
		WishFree:      receiver.WishFree,
		EventURL:      baseURL + "/e/" + event.Slug,
		EventLinkText: T("santa_email_reveal_link", lang),
	}
	return T("santa_email_reveal_subject", lang) + " " + eventTitle, renderEmailTemplate("email_santa_reveal.html", data)
}

// renderEmailTemplate executes an embedded email template (no layout).
func renderEmailTemplate(name string, data any) string {
	tmpl, err := template.ParseFS(templatesFS, "templates/"+name)
	if err != nil {
		log.Printf("email template parse error (%s): %v", name, err)
		return ""
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("email template execute error (%s): %v", name, err)
		return ""
	}
	return buf.String()
}
```

(`templatesFS` is the `//go:embed templates/*.html` variable in `main.go`; the new `email_santa_*.html` files match the pattern and are embedded automatically.)

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -run TestRenderSantaEmails -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add email.go templates/email_santa_link.html templates/email_santa_reveal.html santa_test.go
git commit -m "feat(santa): render magic-link and reveal emails"
```

---

### Task 8: AWS SES sender

**Files:**

- Modify: `email.go` (`SESSender`)
- Modify: `go.mod`, `go.sum` (dependency)

`SESSender` is thin glue over the AWS SDK; it is verified by `go vet` and manual integration testing against SES, not by a unit test (per the testing rules: do not unit-test third-party library behavior).

- [ ] **Step 1: Add the AWS SDK dependency**

Run: `go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/sesv2`
Expected: `go.mod` and `go.sum` updated with `aws-sdk-go-v2` modules. This needs network access.

- [ ] **Step 2: Implement `SESSender` in `email.go`**

Change the `email.go` import block to:

```go
import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)
```

Add to `email.go`:

```go
// SESSender sends email through AWS SES (SESv2 API). Credentials and region are
// read from the standard AWS environment (AWS_REGION, AWS_ACCESS_KEY_ID, ...).
type SESSender struct {
	client *sesv2.Client
	from   string
}

func NewSESSender(ctx context.Context, from string) (*SESSender, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESSender{client: sesv2.NewFromConfig(cfg), from: from}, nil
}

func (s *SESSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
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
	})
	if err != nil {
		return fmt.Errorf("ses send email: %w", err)
	}
	return nil
}
```

If the `sesv2` type names differ in the installed SDK version, verify them against the current `aws-sdk-go-v2/service/sesv2` docs (via context7) — the shape above matches SESv2 `SendEmail`.

- [ ] **Step 3: Verify it compiles**

Run: `go vet ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add email.go go.mod go.sum
git commit -m "feat(santa): add AWS SES email sender"
```

---

### Task 9: App struct & main.go wiring

**Files:**

- Modify: `handlers.go` (`App` struct + imports)
- Modify: `main.go` (EmailSender construction, App fields, imports)

After this task the whole package compiles again (Task 6's `testutil_test.go` referenced fields added here).

- [ ] **Step 1: Extend the `App` struct in `handlers.go`**

Add `"sync"` to the `handlers.go` import block. Then change the `App` struct:

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
}
```

(`time` is already imported in `handlers.go`.)

- [ ] **Step 2: Inject the fake sender into `testApp`**

In `testutil_test.go`, update `testApp` to set the `Email` field:

```go
func testApp(t *testing.T) *App {
	t.Helper()
	db := testDB(t)
	return &App{
		DB:            db,
		AdminPassword: "testpass",
		BaseURL:       "http://localhost:8090",
		Email:         &fakeEmailSender{},
		// EmailSendDelay: 0 and AsyncEmail: false (zero values) — reveal emails
		// send synchronously in tests, so no goroutine races with t.Cleanup.
	}
}
```

- [ ] **Step 3: Wire the EmailSender in `main.go`**

Change the `main.go` import block to:

```go
import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)
```

In `main()`, after the `anthropicKey := os.Getenv("ANTHROPIC_API_KEY")` line, add:

```go
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	emailFrom := os.Getenv("EVENT_SIGNUP_EMAIL_FROM")
	var emailSender EmailSender
	if emailFrom != "" {
		s, err := NewSESSender(context.Background(), emailFrom)
		if err != nil {
			log.Fatalf("Failed to initialize SES: %v", err)
		}
		emailSender = s
		log.Printf("Email: AWS SES (from %s)", emailFrom)
	} else {
		emailSender = LogSender{}
		log.Println("Email: EVENT_SIGNUP_EMAIL_FROM not set — emails will be logged, not sent")
	}

	emailRate := 2
	if v := os.Getenv("EVENT_SIGNUP_EMAIL_RATE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			emailRate = n
		}
	}
```

Change the `app := &App{...}` literal to:

```go
	app := &App{
		DB:             db,
		AdminPassword:  adminPassword,
		BaseURL:        baseURL,
		AnthropicKey:   anthropicKey,
		Email:          emailSender,
		EmailSendDelay: time.Second / time.Duration(emailRate),
		AsyncEmail:     true,
	}
```

- [ ] **Step 4: Verify it compiles and all tests pass**

Run: `go vet ./...` then `go test ./...`
Expected: no vet errors; all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add handlers.go main.go testutil_test.go
git commit -m "feat(santa): wire EmailSender into App and main"
```

---

### Task 10: Public templates

**Files:**

- Create: `templates/public_santa.html`, `templates/santa_edit.html`

Asset-only task — no test (verified by Task 11's handler tests).

- [ ] **Step 1: Create `templates/public_santa.html`**

```html
{{define "content"}} {{$data := .Data}} {{$event := index $data "Event"}}
{{$closed := index $data "Closed"}} {{$linkSent := index $data "LinkSent"}}

<div class="event-header">
  <h1>{{loc $event.TitleFR $event.TitleEN}}</h1>
  <div class="event-meta">
    <span class="event-meta-item"
      >&#x1F4C5; {{formatDate $event.EventDate}}</span
    >
    {{if $event.EventTime}}<span class="event-meta-item"
      >&#x1F552; {{formatTime $event.EventTime}}</span
    >{{end}}
  </div>
  {{$desc := loc $event.DescriptionFR $event.DescriptionEN}} {{if $desc}}
  <div class="event-description">{{nl2br $desc}}</div>
  {{end}}
</div>

{{if $closed}}
<div class="card">
  <div class="card-body"><p>{{t "santa_closed"}}</p></div>
</div>
{{else if $linkSent}}
<div class="registered-card card">
  <div class="confirmation-icon" aria-hidden="true">&#x2709;</div>
  <h2>{{t "santa_link_sent"}}</h2>
</div>
{{else}}
<form
  id="santa-register-form"
  method="POST"
  action="/santa/register?lang={{lang}}"
  class="signup-unified"
>
  <input type="hidden" name="event_id" value="{{$event.ID}}" />
  <section class="panel">
    <h2 class="panel-title">{{t "santa_register_title"}}</h2>
    <div class="panel-body">
      <p class="form-hint" style="margin-bottom:1rem;">
        {{t "santa_register_intro"}}
      </p>
      <div class="form-row">
        <div class="form-group">
          <label for="first_name">{{t "registration_first_name"}} *</label>
          <input
            type="text"
            id="first_name"
            name="first_name"
            required
            class="form-input"
            autocomplete="given-name"
          />
        </div>
        <div class="form-group">
          <label for="last_name">{{t "registration_last_name"}} *</label>
          <input
            type="text"
            id="last_name"
            name="last_name"
            required
            class="form-input"
            autocomplete="family-name"
          />
        </div>
      </div>
      <div class="form-group">
        <label for="email">{{t "registration_email"}} *</label>
        <input
          type="email"
          id="email"
          name="email"
          required
          class="form-input"
          autocomplete="email"
        />
      </div>
    </div>
  </section>
  <button type="submit" class="btn btn-primary btn-block">
    <i class="fa-solid fa-envelope"></i> {{t "santa_register_btn"}}
  </button>
</form>
<div
  id="santa-continue"
  style="display:none;text-align:center;margin-top:1rem;"
>
  <a id="santa-continue-link" class="btn btn-secondary" href="#"
    ><i class="fa-solid fa-pencil"></i> {{t "santa_continue_btn"}}</a
  >
</div>
<script>
  (function() {
      try {
          var tok = localStorage.getItem('santa_' + {{json $event.Slug}});
          if (tok) {
              var link = document.getElementById('santa-continue-link');
              link.href = '/santa/edit?token=' + encodeURIComponent(tok) + '&lang={{lang}}';
              document.getElementById('santa-continue').style.display = '';
          }
      } catch(e) {}
  })();
</script>
{{end}} {{end}} {{template "layout" .}}
```

- [ ] **Step 2: Create `templates/santa_edit.html`**

```html
{{define "content"}} {{$data := .Data}} {{$event := index $data "Event"}} {{$p
:= index $data "Participant"}} {{$closed := index $data "Closed"}} {{if not
$event}}
<p><a href="/">{{t "back"}}</a></p>
{{else}}
<div class="event-header">
  <h1>{{loc $event.TitleFR $event.TitleEN}}</h1>
  <div class="event-meta">
    <span class="event-meta-item"
      >&#x1F4C5; {{formatDate $event.EventDate}}</span
    >
    {{if $event.EventTime}}<span class="event-meta-item"
      >&#x1F552; {{formatTime $event.EventTime}}</span
    >{{end}}
  </div>
</div>
{{if $closed}}
<div class="card">
  <div class="card-body"><p>{{t "santa_closed"}}</p></div>
</div>
{{else}}
<form method="POST" action="/santa/edit?lang={{lang}}" class="signup-unified">
  <input type="hidden" name="token" value="{{$p.Token}}" />
  <section class="panel">
    <h2 class="panel-title">{{t "santa_wishes_title"}}</h2>
    <div class="panel-body">
      <p style="margin-bottom:1rem;">
        <strong>{{$p.FirstName}} {{$p.LastName}}</strong>
      </p>
      <div class="form-group">
        <label for="wish_buy">{{t "santa_wish_buy"}} *</label>
        <p class="form-hint">{{t "santa_wish_buy_hint"}}</p>
        <input
          type="text"
          id="wish_buy"
          name="wish_buy"
          required
          class="form-input"
          value="{{$p.WishBuy}}"
        />
      </div>
      <div class="form-group" style="margin-top:1rem;">
        <label for="wish_make">{{t "santa_wish_make"}} *</label>
        <p class="form-hint">{{t "santa_wish_make_hint"}}</p>
        <input
          type="text"
          id="wish_make"
          name="wish_make"
          required
          class="form-input"
          value="{{$p.WishMake}}"
        />
      </div>
      <div class="form-group" style="margin-top:1rem;">
        <label for="wish_free">{{t "santa_wish_free"}} *</label>
        <p class="form-hint">{{t "santa_wish_free_hint"}}</p>
        <input
          type="text"
          id="wish_free"
          name="wish_free"
          required
          class="form-input"
          value="{{$p.WishFree}}"
        />
      </div>
    </div>
  </section>
  <button type="submit" class="btn btn-primary btn-block">
    <i class="fa-solid fa-check"></i> {{t "santa_wishes_save"}}
  </button>
</form>
<script>
  (function() {
      try { localStorage.setItem('santa_' + {{json $event.Slug}}, {{json $p.Token}}); } catch(e) {}
  })();
</script>
{{end}} {{end}} {{end}} {{template "layout" .}}
```

- [ ] **Step 3: Commit**

```bash
git add templates/public_santa.html templates/santa_edit.html
git commit -m "feat(santa): add public registration and wishes templates"
```

---

### Task 11: Public handlers

**Files:**

- Modify: `handlers.go` (`handlePublicEvent` santa branch; new `handleSantaRegister`, `handleSantaEdit`)
- Modify: `main.go` (routes)
- Modify: `handlers_test.go` (`newMux` routes)
- Create: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

Create `santa_handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestSanta(PublicPage|Register|EditFlow|ClosedAfterDraw)' -v`
Expected: FAIL — build error, `undefined: handleSantaRegister`, etc.

- [ ] **Step 3: Add the santa branch to `handlePublicEvent`**

In `handlers.go`, in `handlePublicEvent`, after the `attendance` branch (the block ending `app.render(w, r, "public_attendance.html", pd)` / `return`) and before `tree, _ := BuildEventTree(...)`, insert:

```go
	if event.EventType == "secret_santa" {
		pd := app.newPageData(r, map[string]any{"Event": event})
		app.render(w, r, "public_santa.html", pd)
		return
	}
```

- [ ] **Step 4: Add `handleSantaRegister` and `handleSantaEdit` to `handlers.go`**

Add at the end of `handlers.go`:

```go
// ---- Secret Santa: public ----

func (app *App) handleSantaRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	event, err := GetEvent(app.DB, eventID)
	if err != nil || event.EventType != "secret_santa" {
		http.NotFound(w, r)
		return
	}
	if event.SantaDrawnAt.Valid {
		pd := app.newPageData(r, map[string]any{"Event": event, "Closed": true})
		app.render(w, r, "public_santa.html", pd)
		return
	}
	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	if firstName == "" || lastName == "" || email == "" {
		pd := app.newPageData(r, map[string]any{"Event": event})
		pd.Error = T("error_invalid_form", lang)
		app.render(w, r, "public_santa.html", pd)
		return
	}
	p, err := UpsertSantaParticipant(app.DB, event.ID, firstName, lastName, email, lang)
	if err != nil {
		log.Printf("santa register error: %v", err)
		pd := app.newPageData(r, map[string]any{"Event": event})
		pd.Error = T("error_server", lang)
		app.render(w, r, "public_santa.html", pd)
		return
	}
	editURL := fmt.Sprintf("%s/santa/edit?token=%s&lang=%s", app.BaseURL, p.Token, lang)
	subject, htmlBody := renderSantaLinkEmail(lang, *p, *event, editURL)
	if err := app.Email.Send(r.Context(), p.Email, subject, htmlBody); err != nil {
		log.Printf("santa link email error: %v", err)
		pd := app.newPageData(r, map[string]any{"Event": event})
		pd.Error = T("santa_email_error", lang)
		app.render(w, r, "public_santa.html", pd)
		return
	}
	pd := app.newPageData(r, map[string]any{"Event": event, "LinkSent": true})
	app.render(w, r, "public_santa.html", pd)
}

func (app *App) handleSantaEdit(w http.ResponseWriter, r *http.Request) {
	lang := LangFromRequest(r)
	token := strings.TrimSpace(r.FormValue("token"))
	p, err := GetSantaParticipantByToken(app.DB, token)
	if err != nil {
		pd := app.newPageData(r, map[string]any{})
		pd.Error = T("santa_invalid_link", lang)
		app.render(w, r, "santa_edit.html", pd)
		return
	}
	event, err := GetEvent(app.DB, p.EventID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if event.SantaDrawnAt.Valid {
		pd := app.newPageData(r, map[string]any{"Event": event, "Closed": true})
		app.render(w, r, "santa_edit.html", pd)
		return
	}
	if r.Method == http.MethodPost {
		wishBuy := strings.TrimSpace(r.FormValue("wish_buy"))
		wishMake := strings.TrimSpace(r.FormValue("wish_make"))
		wishFree := strings.TrimSpace(r.FormValue("wish_free"))
		if wishBuy == "" || wishMake == "" || wishFree == "" {
			pd := app.newPageData(r, map[string]any{"Event": event, "Participant": p})
			pd.Error = T("santa_wishes_required", lang)
			app.render(w, r, "santa_edit.html", pd)
			return
		}
		if err := SaveSantaWishes(app.DB, p.Token, wishBuy, wishMake, wishFree); err != nil {
			log.Printf("santa wishes save error: %v", err)
			pd := app.newPageData(r, map[string]any{"Event": event, "Participant": p})
			pd.Error = T("error_server", lang)
			app.render(w, r, "santa_edit.html", pd)
			return
		}
		p, _ = GetSantaParticipantByToken(app.DB, p.Token)
		pd := app.newPageData(r, map[string]any{"Event": event, "Participant": p})
		pd.Success = T("santa_wishes_saved", lang)
		app.render(w, r, "santa_edit.html", pd)
		return
	}
	pd := app.newPageData(r, map[string]any{"Event": event, "Participant": p})
	app.render(w, r, "santa_edit.html", pd)
}
```

- [ ] **Step 5: Register routes**

In `main.go`, in the `// Public routes` section, add:

```go
	mux.HandleFunc("/santa/register", app.handleSantaRegister)
	mux.HandleFunc("/santa/edit", app.handleSantaEdit)
```

In `handlers_test.go`, in `newMux`, add the same two lines before `return mux`:

```go
	mux.HandleFunc("/santa/register", app.handleSantaRegister)
	mux.HandleFunc("/santa/edit", app.handleSantaEdit)
	return mux
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -run 'TestSanta(PublicPage|Register|RegisterMissingFields|EditFlow|ClosedAfterDraw)' -v`
Expected: PASS. Then `go test ./...` — everything PASS.

- [ ] **Step 7: Commit**

```bash
git add handlers.go main.go handlers_test.go santa_handlers_test.go
git commit -m "feat(santa): add public registration and wishes handlers"
```

---

### Task 12: Reveal-email sending

**Files:**

- Modify: `email.go` (`dispatchRevealEmails`, `sendRevealEmails`, `sendWithRetry`)
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `santa_handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -run 'TestSendRevealEmails|TestSendRevealEmailsRetry|TestResendSkipsAlreadySent' -v`
Expected: FAIL — `app.sendRevealEmails undefined`.

- [ ] **Step 3: Implement sending in `email.go`**

Add `"time"` to the `email.go` import block:

```go
import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)
```

Add to `email.go`:

```go
// dispatchRevealEmails starts sending reveal emails. In production (AsyncEmail)
// it runs in a goroutine so the HTTP request returns immediately; in tests it
// runs synchronously.
func (app *App) dispatchRevealEmails(eventID int64) {
	if app.AsyncEmail {
		go app.sendRevealEmails(eventID)
	} else {
		app.sendRevealEmails(eventID)
	}
}

// sendRevealEmails sends the reveal email to every completed, assigned
// participant of the event who has not been emailed yet. It is rate-limited and
// guarded so only one send runs per event at a time.
func (app *App) sendRevealEmails(eventID int64) {
	if _, busy := app.sending.LoadOrStore(eventID, true); busy {
		return
	}
	defer app.sending.Delete(eventID)

	event, err := GetEvent(app.DB, eventID)
	if err != nil {
		log.Printf("sendRevealEmails: event %d: %v", eventID, err)
		return
	}
	participants, err := ListSantaParticipants(app.DB, eventID)
	if err != nil {
		log.Printf("sendRevealEmails: list participants: %v", err)
		return
	}
	byID := make(map[int64]SantaParticipant, len(participants))
	for _, p := range participants {
		byID[p.ID] = p
	}
	first := true
	for _, p := range participants {
		if !p.AssignedToID.Valid || p.EmailSentAt.Valid {
			continue
		}
		receiver, ok := byID[p.AssignedToID.Int64]
		if !ok {
			continue
		}
		if !first {
			time.Sleep(app.EmailSendDelay)
		}
		first = false
		subject, htmlBody := renderSantaRevealEmail(p.Lang, p, receiver, *event, app.BaseURL)
		if err := app.sendWithRetry(p.Email, subject, htmlBody); err != nil {
			log.Printf("sendRevealEmails: send to %s failed: %v", p.Email, err)
			continue
		}
		if err := MarkRevealEmailSent(app.DB, p.ID); err != nil {
			log.Printf("sendRevealEmails: mark sent %d: %v", p.ID, err)
		}
	}
}

// sendWithRetry retries a transient send failure up to 3 attempts.
func (app *App) sendWithRetry(to, subject, htmlBody string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(app.EmailSendDelay)
		}
		err = app.Email.Send(context.Background(), to, subject, htmlBody)
		if err == nil {
			return nil
		}
	}
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race -run 'TestSendRevealEmails|TestSendRevealEmailsRetry|TestResendSkipsAlreadySent' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add email.go santa_handlers_test.go
git commit -m "feat(santa): send reveal emails with rate limiting and retry"
```

---

### Task 13: Admin template & event-edit/list integration

**Files:**

- Create: `templates/admin_santa.html`
- Modify: `templates/admin_event_edit.html`, `templates/admin_events.html`

Asset-only task — verified by Task 14's handler tests.

- [ ] **Step 1: Create `templates/admin_santa.html`**

```html
{{define "content"}}
{{$data := .Data}}
{{$event := index $data "Event"}}
{{$participants := index $data "Participants"}}
{{$byID := index $data "ByID"}}
{{$total := index $data "Total"}}
{{$completed := index $data "Completed"}}
{{$pending := index $data "Pending"}}
{{$drawn := index $data "Drawn"}}
{{$sentCount := index $data "SentCount"}}

<div class="admin-header">
    <div class="header-left">
        <a href="/admin?lang={{lang}}" class="btn-back" title="{{t "back"}}"><i class="fa-solid fa-arrow-left"></i></a>
        <h1>{{t "santa_admin_title"}} — {{loc $event.TitleFR $event.TitleEN}}</h1>
    </div>
</div>

<section class="panel">
    <div class="panel-body">
        <p style="font-size:1.1rem;">
            <strong>{{$total}}</strong> {{t "santa_admin_participants"}} ·
            <strong>{{$completed}}</strong> {{t "santa_admin_completed"}}
        </p>
        {{if and (not $drawn) (gt $pending 0)}}
        <div class="alert alert-error" style="margin-top:0.5rem;">{{$pending}} {{t "santa_admin_pending_warning"}}</div>
        {{end}}

        {{if $drawn}}
        <p style="margin-top:0.75rem;">{{t "santa_admin_drawn"}} {{$event.SantaDrawnAt.String}}</p>
        <p><strong>{{$sentCount}}</strong> / {{$completed}} {{t "santa_admin_emails_sent"}}</p>
        <form method="POST" action="/admin/santa/resend?lang={{lang}}" class="inline-form" style="margin-top:0.5rem;">
            <input type="hidden" name="event_id" value="{{$event.ID}}">
            <button type="submit" class="btn btn-secondary"><i class="fa-solid fa-paper-plane"></i> {{t "santa_admin_resend_btn"}}</button>
        </form>
        {{else}}
        <form method="POST" action="/admin/santa/draw?lang={{lang}}" class="inline-form" style="margin-top:0.75rem;" onsubmit="return confirm('{{t "santa_admin_draw_confirm"}}')">
            <input type="hidden" name="event_id" value="{{$event.ID}}">
            <button type="submit" class="btn btn-primary" {{if lt $completed 2}}disabled{{end}}><i class="fa-solid fa-shuffle"></i> {{t "santa_admin_draw_btn"}}</button>
        </form>
        {{if lt $completed 2}}<p class="form-hint" style="margin-top:0.5rem;">{{t "santa_admin_too_few"}}</p>{{end}}
        {{end}}

        {{if $participants}}
        <button type="button" class="btn btn-secondary" id="reveal-btn" style="margin-top:1rem;"><i class="fa-solid fa-eye"></i> {{t "santa_admin_reveal_btn"}}</button>
        {{else}}
        <p class="empty-state-sm" style="margin-top:1rem;">{{t "santa_admin_no_participants"}}</p>
        {{end}}
    </div>
</section>

{{if $participants}}
<section class="panel" id="participants-panel" style="display:none;">
    <div class="panel-body">
        <div class="table-responsive">
            <table class="data-table">
                <thead>
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
                </thead>
                <tbody>
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
                </tbody>
            </table>
        </div>
    </div>
</section>
<script>
(function() {
    var btn = document.getElementById('reveal-btn');
    var panel = document.getElementById('participants-panel');
    var revealTxt = {{json (t "santa_admin_reveal_btn")}};
    var hideTxt = {{json (t "santa_admin_hide_btn")}};
    var shown = false;
    btn.addEventListener('click', function() {
        shown = !shown;
        panel.style.display = shown ? '' : 'none';
        btn.innerHTML = shown
            ? '<i class="fa-solid fa-eye-slash"></i> ' + hideTxt
            : '<i class="fa-solid fa-eye"></i> ' + revealTxt;
    });
})();
</script>
{{end}}
{{end}}
{{template "layout" .}}
```

- [ ] **Step 2: Add the `secret_santa` option to `templates/admin_event_edit.html`**

In the new-event form's event-type radio group, add a third radio after the `attendance` one:

```html
<label style="display:flex;align-items:center;gap:0.4rem;cursor:pointer;">
  <input type="radio" name="event_type" value="attendance" />
  {{t "event_type_attendance"}}
</label>
<label style="display:flex;align-items:center;gap:0.4rem;cursor:pointer;">
  <input type="radio" name="event_type" value="secret_santa" />
  {{t "event_type_santa"}}
</label>
```

Then change the type-dispatch block. Replace the line `{{if eq $event.EventType "attendance"}}` (the one starting the attendance summary section) so it reads, and insert a santa branch before the final `{{else}}`:

Find:

```html
{{if eq $event.EventType "attendance"}}
<!-- Attendance Summary -->
```

Leave that as-is. Then find the matching `{{else}}` that begins the tasks section:

```html
{{else}} {{$tree := index $data "Tree"}}
```

and replace it with:

```html
{{else if eq $event.EventType "secret_santa"}} {{$santaTotal := index $data
"SantaTotal"}} {{$santaCompleted := index $data "SantaCompleted"}}
<section class="panel">
  <div class="panel-header">
    <h2 class="panel-title">{{t "santa_admin_title"}}</h2>
  </div>
  <div class="panel-body">
    <p style="font-size:1.1rem;">
      <strong>{{$santaTotal}}</strong> {{t "santa_admin_participants"}} ·
      <strong>{{$santaCompleted}}</strong> {{t "santa_admin_completed"}}
    </p>
    <a
      href="/admin/event/santa?id={{$event.ID}}&lang={{lang}}"
      class="btn btn-secondary"
      style="margin-top:0.5rem;"
      ><i class="fa-solid fa-gift"></i> {{t "santa_admin_view"}}</a
    >
  </div>
</section>
{{else}} {{$tree := index $data "Tree"}}
```

- [ ] **Step 3: Add santa branches to `templates/admin_events.html`**

In the `card-meta` paragraph, extend the type badge:

```html
{{if eq .EventType "attendance"}} ·
<span class="badge badge-info">{{t "event_type_attendance"}}</span>{{end}} {{if
eq .EventType "secret_santa"}} ·
<span class="badge badge-info">{{t "event_type_santa"}}</span>{{end}}
```

In `card-actions`, change the `{{if eq .EventType "attendance"}} ... {{else}} ... {{end}}` to add a santa branch:

```html
{{if eq .EventType "attendance"}}
<a
  href="/admin/event/attendances?id={{.ID}}&lang={{lang}}"
  class="btn btn-sm btn-secondary"
  >{{t "section_attendances"}}{{if .RegCount}}
  <span class="count-badge count-yes">&#x2713; {{.AttendanceYes}}</span>
  <span class="count-badge count-no">&#x2717; {{.AttendanceNo}}</span>{{end}}</a
>
{{else if eq .EventType "secret_santa"}}
<a
  href="/admin/event/santa?id={{.ID}}&lang={{lang}}"
  class="btn btn-sm btn-secondary"
  ><i class="fa-solid fa-gift"></i> {{t "santa_admin_title"}}{{if .RegCount}}
  <span class="count-badge">{{.RegCount}}</span>{{end}}</a
>
{{else}}
<a
  href="/admin/event/registrations?id={{.ID}}&lang={{lang}}"
  class="btn btn-sm btn-secondary"
  >{{t "section_registrations"}}{{if .RegCount}}
  <span class="count-badge">{{.RegCount}}</span>{{end}}</a
>
{{end}}
```

- [ ] **Step 4: Commit**

```bash
git add templates/admin_santa.html templates/admin_event_edit.html templates/admin_events.html
git commit -m "feat(santa): add admin santa template and event-list integration"
```

---

### Task 14: Admin handlers

**Files:**

- Modify: `handlers.go` (`handleAdminEventNew`, `handleAdminEvents`, `eventEditData`; new santa admin handlers)
- Modify: `main.go` (routes)
- Modify: `handlers_test.go` (`newMux` routes)
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `santa_handlers_test.go`:

```go
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
	// testApp has AsyncEmail=false, so reveal emails sent synchronously
	fake := app.Email.(*fakeEmailSender)
	if fake.count() != 2 {
		t.Errorf("expected 2 reveal emails, got %d", fake.count())
	}
	_ = p2

	// a second draw must be refused (no additional emails)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -run 'TestAdminSanta' -v`
Expected: FAIL — `undefined: handleAdminSanta`, etc.

- [ ] **Step 3: Allow `secret_santa` in `handleAdminEventNew`**

In `handlers.go`, in `handleAdminEventNew`, change:

```go
		eventType := r.FormValue("event_type")
		if eventType != "attendance" {
			eventType = "tasks"
		}
```

to:

```go
		eventType := r.FormValue("event_type")
		if eventType != "attendance" && eventType != "secret_santa" {
			eventType = "tasks"
		}
```

- [ ] **Step 4: Add the santa branch to `eventEditData`**

In `handlers.go`, in `eventEditData`, change the `if event.EventType == "attendance" { ... } else { ... }` to insert a santa branch:

```go
	if event.EventType == "attendance" {
		yesCount, totalCount := CountAttendances(app.DB, event.ID)
		data["AttendanceYes"] = yesCount
		data["AttendanceTotal"] = totalCount
	} else if event.EventType == "secret_santa" {
		total, completed := CountSantaParticipants(app.DB, event.ID)
		data["SantaTotal"] = total
		data["SantaCompleted"] = completed
	} else {
		tree, _ := BuildEventTree(app.DB, event.ID)
		flatGroups, _ := BuildFlatGroupList(app.DB, event.ID)
		loadTreeRegistrations(app.DB, tree)
		allTasks := CollectTaskViews(tree)
		totalRegs := CountRegistrations(app.DB, event.ID)
		data["Tree"] = tree
		data["FlatGroups"] = flatGroups
		data["AllTasks"] = allTasks
		data["TotalRegs"] = totalRegs
		data["HasAI"] = app.AnthropicKey != ""
	}
```

- [ ] **Step 5: Show participant counts for santa events in `handleAdminEvents`**

In `handlers.go`, in `handleAdminEvents`, change the per-event loop:

```go
	for i := range events {
		if events[i].EventType == "attendance" {
			yesCount, totalCount := CountAttendances(app.DB, events[i].ID)
			events[i].RegCount = totalCount
			events[i].AttendanceYes = yesCount
			events[i].AttendanceNo = totalCount - yesCount
		} else if events[i].EventType == "secret_santa" {
			total, _ := CountSantaParticipants(app.DB, events[i].ID)
			events[i].RegCount = total
		} else {
			events[i].RegCount = CountRegistrations(app.DB, events[i].ID)
		}
	}
```

- [ ] **Step 6: Add the santa admin handlers to `handlers.go`**

Add `"math/rand"` to the `handlers.go` import block. Then add at the end of `handlers.go`:

```go
// ---- Secret Santa: admin ----

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
	return map[string]any{
		"Event":        event,
		"Participants": participants,
		"ByID":         byID,
		"Total":        total,
		"Completed":    completed,
		"Pending":      total - completed,
		"Drawn":        event.SantaDrawnAt.Valid,
		"SentCount":    sentCount,
	}
}

func (app *App) handleAdminSanta(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	event, err := GetEvent(app.DB, id)
	if err != nil || event.EventType != "secret_santa" {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	pd := app.newPageData(r, app.santaAdminData(event))
	app.render(w, r, "admin_santa.html", pd)
}

func (app *App) handleAdminSantaDraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	event, err := GetEvent(app.DB, eventID)
	if err != nil || event.EventType != "secret_santa" {
		http.NotFound(w, r)
		return
	}
	if event.SantaDrawnAt.Valid {
		pd := app.newPageData(r, app.santaAdminData(event))
		app.render(w, r, "admin_santa.html", pd)
		return
	}
	participants, _ := ListSantaParticipants(app.DB, event.ID)
	var ids []int64
	for _, p := range participants {
		if p.CompletedAt.Valid {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) < 2 {
		pd := app.newPageData(r, app.santaAdminData(event))
		pd.Error = T("santa_admin_too_few", lang)
		app.render(w, r, "admin_santa.html", pd)
		return
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	assignments, err := DrawSecretSanta(ids, rng)
	if err != nil {
		pd := app.newPageData(r, app.santaAdminData(event))
		pd.Error = T("santa_admin_too_few", lang)
		app.render(w, r, "admin_santa.html", pd)
		return
	}
	if err := SaveSantaDraw(app.DB, event.ID, assignments); err != nil {
		log.Printf("santa draw save error: %v", err)
		pd := app.newPageData(r, app.santaAdminData(event))
		pd.Error = T("error_server", lang)
		app.render(w, r, "admin_santa.html", pd)
		return
	}
	app.dispatchRevealEmails(event.ID)
	event, _ = GetEvent(app.DB, event.ID)
	pd := app.newPageData(r, app.santaAdminData(event))
	pd.Success = T("santa_admin_draw_done", lang)
	app.render(w, r, "admin_santa.html", pd)
}

func (app *App) handleAdminSantaResend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	event, err := GetEvent(app.DB, eventID)
	if err != nil || event.EventType != "secret_santa" {
		http.NotFound(w, r)
		return
	}
	if event.SantaDrawnAt.Valid {
		app.dispatchRevealEmails(event.ID)
		event, _ = GetEvent(app.DB, event.ID)
	}
	pd := app.newPageData(r, app.santaAdminData(event))
	pd.Success = T("santa_admin_resend_done", lang)
	app.render(w, r, "admin_santa.html", pd)
}

func (app *App) handleAdminSantaParticipantDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	event, err := GetEvent(app.DB, eventID)
	if err == nil && event.EventType == "secret_santa" && !event.SantaDrawnAt.Valid {
		DeleteSantaParticipant(app.DB, id)
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/event/santa?id=%d&lang=%s", eventID, lang), http.StatusSeeOther)
}
```

- [ ] **Step 7: Register routes**

In `main.go`, in the `// Admin routes` section, add:

```go
	mux.HandleFunc("/admin/event/santa", app.requireAdmin(app.handleAdminSanta))
	mux.HandleFunc("/admin/santa/draw", app.requireAdmin(app.handleAdminSantaDraw))
	mux.HandleFunc("/admin/santa/resend", app.requireAdmin(app.handleAdminSantaResend))
	mux.HandleFunc("/admin/santa/participant/delete", app.requireAdmin(app.handleAdminSantaParticipantDelete))
```

In `handlers_test.go`, in `newMux`, add the same four lines before `return mux`:

```go
	mux.HandleFunc("/admin/event/santa", app.requireAdmin(app.handleAdminSanta))
	mux.HandleFunc("/admin/santa/draw", app.requireAdmin(app.handleAdminSantaDraw))
	mux.HandleFunc("/admin/santa/resend", app.requireAdmin(app.handleAdminSantaResend))
	mux.HandleFunc("/admin/santa/participant/delete", app.requireAdmin(app.handleAdminSantaParticipantDelete))
	mux.HandleFunc("/santa/register", app.handleSantaRegister)
	mux.HandleFunc("/santa/edit", app.handleSantaEdit)
	return mux
```

(The `/santa/register` and `/santa/edit` lines were added in Task 11 — keep them; do not duplicate.)

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test -race -run 'TestAdminSanta' -v`
Expected: PASS. Then `go test -race ./...` — everything PASS.

- [ ] **Step 9: Commit**

```bash
git add handlers.go main.go handlers_test.go santa_handlers_test.go
git commit -m "feat(santa): add admin draw, resend and participant management"
```

---

### Task 15: Final verification

**Files:** none modified (verification + tidy only)

- [ ] **Step 1: Tidy modules**

Run: `go mod tidy`
Expected: `go.mod`/`go.sum` cleaned; no unused or missing entries.

- [ ] **Step 2: Vet and full test run**

Run: `go vet ./...`
Expected: no output.

Run: `go test -race ./...`
Expected: `ok  event-signup` — all tests PASS, no race warnings.

- [ ] **Step 3: Manual smoke test**

Run the app: `EVENT_SIGNUP_ADMIN_PASSWORD=test go run .` (no `EVENT_SIGNUP_EMAIL_FROM`, so emails are logged).

Verify in a browser:

1. `/admin` → log in → create a new event, type **Secret Santa**.
2. Open the public link `/e/<slug>` → register with a name + email → confirm the "check your email" message appears and the magic link is printed in the server log.
3. Open the logged `/santa/edit?token=…` URL → fill the 3 wishes → save → confirm the success message.
4. Register and complete a second participant.
5. In admin, open the event → "Manage Secret Santa" → "Reveal the list" shows both participants; "Shuffle and send" (after the confirm dialog) runs the draw; the server log shows 2 reveal emails; the page shows "2 / 2 emails sent".
6. Reload the public page → it shows "registration is closed".

- [ ] **Step 4: Commit any tidy changes**

```bash
git add go.mod go.sum
git commit -m "chore(santa): tidy module dependencies"
```

(Skip this commit if `go mod tidy` produced no changes.)

---

## Self-Review Notes

**Spec coverage** — every spec section maps to a task: data model (T1-2), draw algorithm (T3), persistence (T4), email module/SES/rendering (T6-8), background sending with rate limit + retry + guard (T12), public two-step flow (T10-11), admin page/draw/resend/delete (T13-14), i18n (T5), SES prerequisites (documented in spec §13; `LogSender` fallback in T9), edge cases (closed-after-draw T11, too-few T14, delete-pre-draw-only T14), tests (T1-4, 7, 11, 12, 14).

**Deviation from spec** — the `EmailSender.Send` signature omits the `textBody` parameter from spec §7.1: emails are HTML-only (valid for SES `SendEmail`; a text part can be added later without an interface change). This keeps the email templates to one file each.

**Type consistency** — `SantaParticipant`, `DrawSecretSanta(ids []int64, rng *mrand.Rand)`, `SaveSantaDraw`, `UpsertSantaParticipant`, `SaveSantaWishes`, `MarkRevealEmailSent`, `EmailSender.Send(ctx, to, subject, htmlBody)`, `App.{Email,EmailSendDelay,AsyncEmail,sending}` are used identically across all tasks.
