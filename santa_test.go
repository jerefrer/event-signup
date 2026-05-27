package main

import (
	"database/sql"
	"errors"
	"math/rand"
	"strings"
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

	// single cycle: following the chain from any id must visit every id and return to start
	cur := ids[0]
	for range ids {
		cur = assignments[cur]
	}
	if cur != ids[0] {
		t.Error("assignments do not form a single cycle")
	}

	// deterministic with the same seed
	again, _ := DrawSecretSanta(ids, rand.New(rand.NewSource(42))) // same inputs as above, error already checked
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
	got2, _ := GetSantaParticipant(db, p2.ID)
	if !got2.AssignedToID.Valid || got2.AssignedToID.Int64 != p1.ID {
		t.Errorf("p2 assigned_to = %v, want %d", got2.AssignedToID, p1.ID)
	}
	ev, _ := GetEvent(db, e.ID)
	if !ev.SantaDrawnAt.Valid {
		t.Error("event santa_drawn_at should be set after the draw")
	}
}

func TestRenderSantaEmails(t *testing.T) {
	e := Event{
		TitleFR:       "Noël",
		TitleEN:       "Christmas",
		DescriptionFR: "<p>Rendez-vous à 11h <strong>sous le grand chêne</strong>.</p>",
		DescriptionEN: "<p>Meet at 11am <strong>under the big oak</strong>.</p>",
		Slug:          "noel",
	}
	giver := SantaParticipant{FirstName: "Alice", LastName: "Dupont", Email: "alice@t.com"}
	receiver := SantaParticipant{FirstName: "Bob", LastName: "Martin",
		WishBuy: "un stylo", WishMake: "un poeme", WishFree: "une surprise"}

	subj, html := renderSantaLinkEmail("fr", giver, e, "http://x/santa/edit?token=abc")
	if subj == "" {
		t.Error("link email subject is empty")
	}
	for _, want := range []string{
		"http://x/santa/edit?token=abc",
		"Alice",
		"invité",                                                         // from santa_email_link_hook (FR, default)
		"Comment ça marche",                                              // from santa_email_how_title (FR, default)
		"3 souhaits",                                                     // from santa_email_how_step1 (FR, default)
		"<p>Rendez-vous à 11h <strong>sous le grand chêne</strong>.</p>", // description rendered as-is
	} {
		if !strings.Contains(html, want) {
			t.Errorf("link email is missing %q (default rendering)", want)
		}
	}

	// Per-event overrides win over the i18n defaults.
	eCustom := e
	eCustom.EmailHookFR = "Salut ! Nous organisons un super tirage cette année."
	eCustom.EmailHowTitleFR = "Le déroulé"
	eCustom.EmailHowStep1FR = "Étape personnalisée 1."
	eCustom.EmailButtonFR = "👉 Ma liste perso"
	eCustom.EmailDisclaimerFR = "Petit rappel personnalisé."
	_, htmlCustom := renderSantaLinkEmail("fr", giver, eCustom, "http://x/santa/edit?token=abc")
	for _, want := range []string{
		"super tirage cette année",
		"Le déroulé",
		"Étape personnalisée 1.",
		"Ma liste perso",
		"Petit rappel personnalisé.",
	} {
		if !strings.Contains(htmlCustom, want) {
			t.Errorf("link email did not pick up the per-event override %q", want)
		}
	}
	// The defaults that were overridden must not leak through anymore.
	if strings.Contains(htmlCustom, "invité(e) à participer à notre Secret Santa") {
		t.Error("link email used the default hook even though an override was set")
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

func TestNormalizeSantaLang(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"french code", "fr", "fr"},
		{"french word", "Français", "fr"},
		{"english code", "en", "en"},
		{"english word", "English", "en"},
		{"empty defaults to french", "", "fr"},
		{"unknown defaults to french", "espagnol", "fr"},
		{"english with spaces", "  EN  ", "en"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeSantaLang(c.in); got != c.want {
				t.Errorf("normalizeSantaLang(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseSantaCSV(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantRows    []santaCSVRow
		wantSkipped int
		wantErr     error
	}{
		{
			name: "comma delimited, headers in order",
			in:   "email,Nom,Prénom,Langue\nalice@test.com,Dupont,Alice,fr\nbob@test.com,Martin,Bob,en\n",
			wantRows: []santaCSVRow{
				{FirstName: "Alice", LastName: "Dupont", Email: "alice@test.com", Lang: "fr"},
				{FirstName: "Bob", LastName: "Martin", Email: "bob@test.com", Lang: "en"},
			},
		},
		{
			name: "headers reordered with extra ignored columns",
			in:   "Prénom,Ville,email,Nom\nAlice,Paris,alice@test.com,Dupont\n",
			wantRows: []santaCSVRow{
				{FirstName: "Alice", LastName: "Dupont", Email: "alice@test.com", Lang: "fr"},
			},
		},
		{
			name: "semicolon delimiter is detected",
			in:   "email;Nom;Prénom;Langue\nalice@test.com;Dupont;Alice;en\n",
			wantRows: []santaCSVRow{
				{FirstName: "Alice", LastName: "Dupont", Email: "alice@test.com", Lang: "en"},
			},
		},
		{
			name: "leading UTF-8 BOM is stripped",
			in:   "\xef\xbb\xbfemail,Nom,Prénom\nalice@test.com,Dupont,Alice\n",
			wantRows: []santaCSVRow{
				{FirstName: "Alice", LastName: "Dupont", Email: "alice@test.com", Lang: "fr"},
			},
		},
		{
			name: "header case and surrounding spaces tolerated",
			in:   " EMAIL , nom , PRENOM \nalice@test.com,Dupont,Alice\n",
			wantRows: []santaCSVRow{
				{FirstName: "Alice", LastName: "Dupont", Email: "alice@test.com", Lang: "fr"},
			},
		},
		{
			name:        "rows with invalid email are skipped",
			in:          "email,Prénom\nalice@test.com,Alice\n,Bob\nnotanemail,Carol\n",
			wantRows:    []santaCSVRow{{FirstName: "Alice", Email: "alice@test.com", Lang: "fr"}},
			wantSkipped: 2,
		},
		{
			name:    "missing email column is an error",
			in:      "Nom,Prénom\nDupont,Alice\n",
			wantErr: errSantaCSVNoEmail,
		},
		{
			name:    "empty input is an error",
			in:      "",
			wantErr: errSantaCSVNoEmail,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, skipped, err := parseSantaCSV(strings.NewReader(c.in))
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if skipped != c.wantSkipped {
				t.Errorf("skipped = %d, want %d", skipped, c.wantSkipped)
			}
			if len(rows) != len(c.wantRows) {
				t.Fatalf("got %d rows, want %d (%+v)", len(rows), len(c.wantRows), rows)
			}
			for i, want := range c.wantRows {
				if rows[i] != want {
					t.Errorf("row %d = %+v, want %+v", i, rows[i], want)
				}
			}
		})
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
