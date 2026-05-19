# Secret Santa Invitations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the admin import a participant list from a CSV and send each person a personalized invitation email with a direct link to the Secret Santa wish-list form, keeping public self-registration as a fallback.

**Architecture:** Two new admin POST routes. `/admin/santa/import` parses an uploaded CSV with a pure, header-mapped parser and upserts participants. `/admin/santa/invite` triggers a rate-limited background sender (`sendInviteEmails`) that reuses the existing magic-link email and skips anyone already invited. No data-model change — `santa_participants` and `email_messages` (kind `link`) already cover everything. The public self-registration flow is untouched; only the confirmation-page envelope icon is fixed.

**Tech Stack:** Go 1.25, `net/http`, `encoding/csv`, `html/template`, SQLite (`github.com/mattn/go-sqlite3`). Single flat-file module — all source files at repo root.

**Spec:** `docs/superpowers/specs/2026-05-19-secret-santa-invitations-design.md`

**Build/test notes (from `CLAUDE.md`):**
- NEVER run `go build ./...` — it drops an `event-signup` binary at the repo root. Use `go vet ./...` to check compilation.
- Run tests with `go test .` (single package at repo root) or `go test -run TestName .` for one test.
- Tests use an in-memory SQLite DB; `testApp(t)` wires a `*App` with a `fakeEmailSender` and `AsyncEmail:false` (emails send synchronously, no goroutines).

---

## File Structure

| File | Change | Responsibility |
|------|--------|----------------|
| `handlers.go` | modify | Add `parseSantaCSV` + helpers (pure CSV parsing), `handleAdminSantaImport`, `handleAdminSantaInvite`. |
| `email.go` | modify | Add `dispatchInviteEmails` + `sendInviteEmails` (batch invitation sender). |
| `main.go` | modify | Register the two new admin routes. |
| `i18n.go` | modify | New FR/EN keys; reword `santa_email_link_intro`. |
| `templates/admin_santa.html` | modify | CSV import form + "send invitations" button + invitation counter. |
| `templates/public_santa.html` | modify | Replace the text-glyph envelope with a Font Awesome icon. |
| `static/style.css` | modify | Slightly enlarge `.confirmation-icon`. |
| `handlers_test.go` | modify | Add `postMultipart` helper; register new routes in `newMux`. |
| `santa_test.go` | modify | Tests for `parseSantaCSV` / `normalizeSantaLang`. |
| `santa_handlers_test.go` | modify | Tests for the import handler, invite sender, invite handler, admin UI. |

No new files. No `schema.sql` / `models.go` change.

---

## Task 1: CSV parser (pure function)

A self-contained, DB-free parser. Built and tested first so later tasks can rely on it.

**Files:**
- Modify: `handlers.go` (add types + functions; add `errors` and `io` to the import block)
- Test: `santa_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `santa_test.go`:

```go
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
			in:   "\ufeffemail,Nom,Prénom\nalice@test.com,Dupont,Alice\n",
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
```

Add `"errors"` to the `santa_test.go` import block (it currently imports `database/sql`, `math/rand`, `strings`, `testing`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestParseSantaCSV|TestNormalizeSantaLang' .`
Expected: FAIL — compile error `undefined: parseSantaCSV`, `undefined: normalizeSantaLang`, `undefined: santaCSVRow`, `undefined: errSantaCSVNoEmail`.

- [ ] **Step 3: Implement the parser**

In `handlers.go`, add `"errors"` and `"io"` to the import block (it already imports `"encoding/csv"`, `"fmt"`, `"strings"`).

Add at the end of `handlers.go`:

```go
// ---- Secret Santa: CSV import parsing ----

// santaCSVRow is one parsed, validated participant from an imported CSV.
type santaCSVRow struct {
	FirstName string
	LastName  string
	Email     string
	Lang      string
}

// errSantaCSVNoEmail is returned by parseSantaCSV when the file has no column
// recognised as the email column (also covers an empty file).
var errSantaCSVNoEmail = errors.New("santa csv: no email column")

// santaCSVField maps a CSV header cell to a canonical field name, or "" when
// the header is not one we use. Matching is case-insensitive and trimmed.
func santaCSVField(header string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "email", "e-mail", "courriel":
		return "email"
	case "prénom", "prenom", "first name", "first_name", "firstname":
		return "first_name"
	case "nom", "last name", "last_name", "lastname":
		return "last_name"
	case "langue", "lang", "language":
		return "lang"
	}
	return ""
}

// normalizeSantaLang reduces a free-form language cell to "fr" or "en".
// Anything that does not clearly start with "en" defaults to French.
func normalizeSantaLang(v string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "en") {
		return "en"
	}
	return "fr"
}

// parseSantaCSV reads a participant CSV. It strips a leading UTF-8 BOM,
// auto-detects a ',' or ';' delimiter, and maps columns by header name. Rows
// whose email is empty or has no '@' are dropped and counted in skipped. It
// returns errSantaCSVNoEmail when no email column is present (or the file is
// empty), or a wrapped error when the input cannot be read or parsed.
func parseSantaCSV(r io.Reader) (rows []santaCSVRow, skipped int, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, fmt.Errorf("read santa csv: %w", err)
	}
	content := strings.TrimPrefix(string(raw), "\ufeff")

	firstLine := content
	if i := strings.IndexAny(content, "\r\n"); i >= 0 {
		firstLine = content[:i]
	}
	comma := ','
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		comma = ';'
	}

	cr := csv.NewReader(strings.NewReader(content))
	cr.Comma = comma
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	records, err := cr.ReadAll()
	if err != nil {
		return nil, 0, fmt.Errorf("parse santa csv: %w", err)
	}
	if len(records) == 0 {
		return nil, 0, errSantaCSVNoEmail
	}

	col := map[string]int{"email": -1, "first_name": -1, "last_name": -1, "lang": -1}
	for i, h := range records[0] {
		if f := santaCSVField(h); f != "" && col[f] == -1 {
			col[f] = i
		}
	}
	if col["email"] == -1 {
		return nil, 0, errSantaCSVNoEmail
	}

	at := func(rec []string, i int) string {
		if i >= 0 && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	for _, rec := range records[1:] {
		email := at(rec, col["email"])
		if email == "" || !strings.Contains(email, "@") {
			skipped++
			continue
		}
		rows = append(rows, santaCSVRow{
			FirstName: at(rec, col["first_name"]),
			LastName:  at(rec, col["last_name"]),
			Email:     email,
			Lang:      normalizeSantaLang(at(rec, col["lang"])),
		})
	}
	return rows, skipped, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -run 'TestParseSantaCSV|TestNormalizeSantaLang' .`
Expected: PASS (`ok` line, all sub-tests pass).

- [ ] **Step 5: Verify the whole package still compiles and passes**

Run: `go vet ./... && go test .`
Expected: no vet output; `ok` — all existing tests still pass.

- [ ] **Step 6: Commit**

```bash
git add handlers.go santa_test.go
git commit -m "feat(santa): add CSV participant-list parser"
```

---

## Task 2: CSV import handler

Wires the parser to an admin route that upserts participants and reports a result.

**Files:**
- Modify: `handlers.go` (add `handleAdminSantaImport` after `handleAdminSantaParticipantDelete`, near line 1297)
- Modify: `main.go` (register route, near line 150)
- Modify: `i18n.go` (import-related keys)
- Modify: `handlers_test.go` (add `postMultipart` helper; register route in `newMux`)
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Add the i18n keys**

In `i18n.go`, in the `// Secret Santa — admin` block (after `santa_admin_resend_done`, around line 198), add:

```go
	"santa_import_btn":          {"fr": "Importer une liste (CSV)", "en": "Import a list (CSV)"},
	"santa_import_hint":         {"fr": "Colonnes attendues : email, Nom, Prénom, Langue. Les autres colonnes sont ignorées.", "en": "Expected columns: email, Nom, Prénom, Langue. Other columns are ignored."},
	"santa_import_done":         {"fr": "%d participant(s) importé(s), %d mis à jour, %d ligne(s) ignorée(s).", "en": "%d participant(s) imported, %d updated, %d row(s) skipped."},
	"santa_import_no_file":      {"fr": "Aucun fichier reçu.", "en": "No file received."},
	"santa_import_bad_file":     {"fr": "Fichier CSV illisible.", "en": "Unreadable CSV file."},
	"santa_import_no_email_col": {"fr": "Le fichier ne contient pas de colonne « email ».", "en": "The file has no \"email\" column."},
	"santa_import_closed":       {"fr": "Import impossible : le tirage a déjà eu lieu.", "en": "Import unavailable: the draw has already happened."},
	"santa_invite_btn":          {"fr": "Envoyer les invitations", "en": "Send invitations"},
	"santa_invite_confirm":      {"fr": "Envoyer un email d'invitation à tous les participants qui n'en ont pas encore reçu ?", "en": "Send an invitation email to every participant who has not received one yet?"},
	"santa_invite_done":         {"fr": "Envoi des invitations en cours.", "en": "Sending invitations."},
	"santa_invite_closed":       {"fr": "Envoi impossible : le tirage a déjà eu lieu.", "en": "Sending unavailable: the draw has already happened."},
	"santa_invite_count":        {"fr": "invitation(s) envoyée(s)", "en": "invitation(s) sent"},
```

(All keys for Tasks 2, 4 and 5 are added together here — they are plain data and adding them once avoids touching `i18n.go` three times.)

- [ ] **Step 2: Add the `postMultipart` test helper and register the route in `newMux`**

In `handlers_test.go`, add `"bytes"` and `"mime/multipart"` to the import block.

After the `postForm` helper (around line 48), add:

```go
// postMultipart sends a POST with one uploaded file plus extra form fields.
func postMultipart(mux http.Handler, path, fileName, fileBody string, fields map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", fileName)
	_, _ = fw.Write([]byte(fileBody))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}
```

In the `newMux` function in `handlers_test.go`, after the line registering `/admin/santa/participant/delete`, add exactly one line (the invite route is added in Task 4 — adding it now would break compilation):

```go
	mux.HandleFunc("/admin/santa/import", app.requireAdmin(app.handleAdminSantaImport))
```

- [ ] **Step 3: Write the failing tests**

Add to `santa_handlers_test.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test -run TestAdminSantaImport .`
Expected: FAIL — compile error `undefined: app.handleAdminSantaImport`.

- [ ] **Step 5: Implement the handler and register the route**

In `handlers.go`, after `handleAdminSantaParticipantDelete` (ends near line 1297), add:

```go
func (app *App) handleAdminSantaImport(w http.ResponseWriter, r *http.Request) {
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
	renderMsg := func(errMsg, okMsg string) {
		pd := app.newPageData(r, app.santaAdminData(event))
		pd.Error = errMsg
		pd.Success = okMsg
		app.render(w, r, "admin_santa.html", pd)
	}
	if event.SantaDrawnAt.Valid {
		renderMsg(T("santa_import_closed", lang), "")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		renderMsg(T("santa_import_no_file", lang), "")
		return
	}
	defer file.Close()

	rows, skipped, err := parseSantaCSV(file)
	if errors.Is(err, errSantaCSVNoEmail) {
		renderMsg(T("santa_import_no_email_col", lang), "")
		return
	}
	if err != nil {
		log.Printf("santa import parse error: %v", err)
		renderMsg(T("santa_import_bad_file", lang), "")
		return
	}

	created, updated := 0, 0
	for _, row := range rows {
		_, existsErr := GetSantaParticipantByEmail(app.DB, event.ID, row.Email)
		if _, err := UpsertSantaParticipant(app.DB, event.ID, row.FirstName, row.LastName, row.Email, row.Lang); err != nil {
			log.Printf("santa import upsert error (%s): %v", row.Email, err)
			continue
		}
		if existsErr == nil {
			updated++
		} else {
			created++
		}
	}
	renderMsg("", fmt.Sprintf(T("santa_import_done", lang), created, updated, skipped))
}
```

In `main.go`, after the line registering `/admin/santa/participant/delete` (near line 150), add:

```go
	mux.HandleFunc("/admin/santa/import", app.requireAdmin(app.handleAdminSantaImport))
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -run TestAdminSantaImport .`
Expected: PASS — all three `TestAdminSantaImport*` tests pass.

- [ ] **Step 7: Verify the whole package**

Run: `go vet ./... && go test .`
Expected: no vet output; `ok` — all tests pass.

- [ ] **Step 8: Commit**

```bash
git add handlers.go main.go i18n.go handlers_test.go santa_handlers_test.go
git commit -m "feat(santa): import participant list from CSV"
```

---

## Task 3: Invitation batch sender

A rate-limited background sender that emails the magic link to every participant who has not been invited yet.

**Files:**
- Modify: `email.go` (add `dispatchInviteEmails` + `sendInviteEmails`)
- Modify: `i18n.go` (reword `santa_email_link_intro`)
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing test**

Add to `santa_handlers_test.go`:

```go
func TestSantaSendInviteEmails(t *testing.T) {
	app := testApp(t)
	e := seedSantaEvent(t, app.DB)
	p1 := seedSantaParticipant(t, app.DB, e.ID, "Alice", "alice@test.com", false)
	seedSantaParticipant(t, app.DB, e.ID, "Bob", "bob@test.com", false)

	app.sendInviteEmails(e.ID)

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
	app.sendInviteEmails(e.ID)
	if fake.count() != 2 {
		t.Errorf("re-invite must skip already-invited participants, got %d", fake.count())
	}

	// A newly added participant IS picked up by the next run.
	seedSantaParticipant(t, app.DB, e.ID, "Carol", "carol@test.com", false)
	app.sendInviteEmails(e.ID)
	if fake.count() != 3 {
		t.Errorf("a newly added participant should be invited, got %d", fake.count())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestSantaSendInviteEmails .`
Expected: FAIL — compile error `undefined: app.sendInviteEmails`.

- [ ] **Step 3: Implement the sender**

In `email.go`, after `sendRevealEmails` (ends near line 202, before `sendWithRetry`), add:

```go
// dispatchInviteEmails starts sending invitation emails. In production
// (AsyncEmail) it runs in a goroutine so the HTTP request returns immediately;
// in tests it runs synchronously.
func (app *App) dispatchInviteEmails(eventID int64) {
	if app.AsyncEmail {
		go app.sendInviteEmails(eventID)
	} else {
		app.sendInviteEmails(eventID)
	}
}

// sendInviteEmails sends the magic-link email to every participant of the event
// who has not been sent a "link" email yet. It is rate-limited and guarded so
// only one send runs per event at a time.
func (app *App) sendInviteEmails(eventID int64) {
	if _, busy := app.sending.LoadOrStore(eventID, true); busy {
		return
	}
	defer app.sending.Delete(eventID)

	event, err := GetEvent(app.DB, eventID)
	if err != nil {
		log.Printf("sendInviteEmails: event %d: %v", eventID, err)
		return
	}
	participants, err := ListSantaParticipants(app.DB, eventID)
	if err != nil {
		log.Printf("sendInviteEmails: list participants: %v", err)
		return
	}
	msgs, err := ListEmailMessages(app.DB, eventID)
	if err != nil {
		log.Printf("sendInviteEmails: list email messages: %v", err)
		return
	}
	invited := make(map[int64]bool)
	for _, m := range msgs {
		if m.Kind == "link" {
			invited[m.ParticipantID] = true
		}
	}
	first := true
	for _, p := range participants {
		if invited[p.ID] {
			continue
		}
		if !first {
			time.Sleep(app.EmailSendDelay)
		}
		first = false
		editURL := fmt.Sprintf("%s/santa/edit?token=%s&lang=%s", app.BaseURL, p.Token, p.Lang)
		subject, htmlBody := renderSantaLinkEmail(p.Lang, p, *event, editURL)
		if htmlBody == "" {
			log.Printf("sendInviteEmails: empty rendered email body for participant %d, skipping", p.ID)
			continue
		}
		messageID, err := app.sendWithRetry(p.Email, subject, htmlBody)
		if err != nil {
			log.Printf("sendInviteEmails: send to %s failed: %v", p.Email, err)
			continue
		}
		if err := RecordEmailSent(app.DB, p.ID, "link", messageID, p.Email); err != nil {
			log.Printf("sendInviteEmails: record %d: %v", p.ID, err)
		}
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run TestSantaSendInviteEmails .`
Expected: PASS.

- [ ] **Step 5: Reword `santa_email_link_intro`**

The magic-link email is reused for invitations. Reword its intro so it reads naturally both for an invited person (who did not self-register) and a self-registrant.

In `i18n.go`, replace the `santa_email_link_intro` line (around line 220):

```go
	"santa_email_link_intro":     {"fr": "Voici votre lien personnel pour composer votre liste de souhaits. Cliquez sur le bouton ci-dessous.", "en": "Here is your personal link to put together your wish list. Click the button below."},
```

- [ ] **Step 6: Verify the whole package**

Run: `go vet ./... && go test .`
Expected: no vet output; `ok` — all tests pass (the existing `TestSantaRegister` still passes; it asserts the token is in the email body, not the intro wording).

- [ ] **Step 7: Commit**

```bash
git add email.go i18n.go santa_handlers_test.go
git commit -m "feat(santa): add invitation batch sender"
```

---

## Task 4: Invitation handler

The admin route that triggers `dispatchInviteEmails`.

**Files:**
- Modify: `handlers.go` (add `handleAdminSantaInvite` after `handleAdminSantaImport`)
- Modify: `main.go` (register route)
- Modify: `handlers_test.go` (register route in `newMux`)
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Register the route in `newMux`**

In `handlers_test.go`, in `newMux`, after the `/admin/santa/import` line added in Task 2, add:

```go
	mux.HandleFunc("/admin/santa/invite", app.requireAdmin(app.handleAdminSantaInvite))
```

- [ ] **Step 2: Write the failing tests**

Add to `santa_handlers_test.go`:

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run TestAdminSantaInvite .`
Expected: FAIL — compile error `undefined: app.handleAdminSantaInvite`.

- [ ] **Step 4: Implement the handler and register the route**

In `handlers.go`, after `handleAdminSantaImport`, add:

```go
func (app *App) handleAdminSantaInvite(w http.ResponseWriter, r *http.Request) {
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
		pd.Error = T("santa_invite_closed", lang)
		app.render(w, r, "admin_santa.html", pd)
		return
	}
	app.dispatchInviteEmails(event.ID)
	pd := app.newPageData(r, app.santaAdminData(event))
	pd.Success = T("santa_invite_done", lang)
	app.render(w, r, "admin_santa.html", pd)
}
```

(`santaAdminData` is built *after* `dispatchInviteEmails` so that in tests — where `AsyncEmail` is false and the send runs synchronously — the rendered page reflects the just-recorded `link` rows.)

In `main.go`, after the `/admin/santa/import` line, add:

```go
	mux.HandleFunc("/admin/santa/invite", app.requireAdmin(app.handleAdminSantaInvite))
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -run TestAdminSantaInvite .`
Expected: PASS — both `TestAdminSantaInvite*` tests pass.

- [ ] **Step 6: Verify the whole package**

Run: `go vet ./... && go test .`
Expected: no vet output; `ok` — all tests pass.

- [ ] **Step 7: Commit**

```bash
git add handlers.go main.go handlers_test.go santa_handlers_test.go
git commit -m "feat(santa): add invitation send admin route"
```

---

## Task 5: Admin UI — import form and invite button

Surface the two new actions on the admin Secret Santa page.

**Files:**
- Modify: `templates/admin_santa.html`
- Test: `santa_handlers_test.go`

- [ ] **Step 1: Write the failing test**

Add to `santa_handlers_test.go`:

```go
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
	if !strings.Contains(body, T("santa_import_btn", LangFR)) {
		t.Error("admin santa page should show the CSV import button before the draw")
	}
	if !strings.Contains(body, T("santa_invite_btn", LangFR)) {
		t.Error("admin santa page should show the send-invitations button before the draw")
	}
	if !strings.Contains(body, `action="/admin/santa/import`) {
		t.Error("admin santa page should contain the import form")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestAdminSantaPageShowsImportAndInvite .`
Expected: FAIL — body does not contain the import/invite button text.

- [ ] **Step 3: Add the import form and invite button to the template**

In `templates/admin_santa.html`, in the pre-draw branch, replace the existing `{{else}}` block (currently the draw form + too-few hint, lines 48–54):

```html
        {{else}}
        <form method="POST" action="/admin/santa/draw?lang={{lang}}" class="inline-form" style="margin-top:0.75rem;" onsubmit="return confirm('{{t "santa_admin_draw_confirm"}}')">
            <input type="hidden" name="event_id" value="{{$event.ID}}">
            <button type="submit" class="btn btn-primary" {{if lt $completed 2}}disabled{{end}}><i class="fa-solid fa-shuffle"></i> {{t "santa_admin_draw_btn"}}</button>
        </form>
        {{if lt $completed 2}}<p class="form-hint" style="margin-top:0.5rem;">{{t "santa_admin_too_few"}}</p>{{end}}
        {{end}}
```

with:

```html
        {{else}}
        <form method="POST" action="/admin/santa/import?lang={{lang}}" enctype="multipart/form-data" class="inline-form" style="margin-top:0.75rem;gap:0.5rem;flex-wrap:wrap;">
            <input type="hidden" name="event_id" value="{{$event.ID}}">
            <input type="file" name="file" accept=".csv,text/csv" required>
            <button type="submit" class="btn btn-secondary"><i class="fa-solid fa-file-import"></i> {{t "santa_import_btn"}}</button>
        </form>
        <p class="form-hint" style="margin-top:0.25rem;">{{t "santa_import_hint"}}</p>

        {{if $participants}}
        <form method="POST" action="/admin/santa/invite?lang={{lang}}" class="inline-form" style="margin-top:0.75rem;" onsubmit="return confirm('{{t "santa_invite_confirm"}}')">
            <input type="hidden" name="event_id" value="{{$event.ID}}">
            <button type="submit" class="btn btn-secondary"><i class="fa-solid fa-paper-plane"></i> {{t "santa_invite_btn"}}</button>
        </form>
        <p class="form-hint" style="margin-top:0.25rem;"><strong>{{len $linkStatus}}</strong> {{t "santa_invite_count"}}</p>
        {{end}}

        <form method="POST" action="/admin/santa/draw?lang={{lang}}" class="inline-form" style="margin-top:0.75rem;" onsubmit="return confirm('{{t "santa_admin_draw_confirm"}}')">
            <input type="hidden" name="event_id" value="{{$event.ID}}">
            <button type="submit" class="btn btn-primary" {{if lt $completed 2}}disabled{{end}}><i class="fa-solid fa-shuffle"></i> {{t "santa_admin_draw_btn"}}</button>
        </form>
        {{if lt $completed 2}}<p class="form-hint" style="margin-top:0.5rem;">{{t "santa_admin_too_few"}}</p>{{end}}
        {{end}}
```

The `santa_invite_count` line uses `{{len $linkStatus}}` — `$linkStatus` is the `map[int64]EmailMessage` already provided by `santaAdminData`; `len` on a map works in `html/template`. No Go change is needed.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run TestAdminSantaPageShowsImportAndInvite .`
Expected: PASS.

- [ ] **Step 5: Verify the whole package**

Run: `go vet ./... && go test .`
Expected: no vet output; `ok` — all tests pass.

- [ ] **Step 6: Commit**

```bash
git add templates/admin_santa.html santa_handlers_test.go
git commit -m "feat(santa): show CSV import and invite controls in admin"
```

---

## Task 6: Fix the confirmation-page envelope icon

The public self-registration confirmation card shows a thin text-glyph envelope (`&#x2709;`) that renders too small. Replace it with a Font Awesome icon (already loaded on that page) and enlarge `.confirmation-icon` slightly.

**Files:**
- Modify: `templates/public_santa.html`
- Modify: `static/style.css`

No automated test — this is a visual change. Verified manually.

- [ ] **Step 1: Confirm which pages use `.confirmation-icon`**

Run: `grep -rn "confirmation-icon" templates/`
Expected: a small list of confirmation cards. The CSS change in Step 3 enlarges the icon uniformly on all of them, which is a harmless, consistent improvement — note the affected templates so the manual check in Step 4 can glance at them.

- [ ] **Step 2: Swap the glyph for a Font Awesome icon**

In `templates/public_santa.html`, replace the confirmation icon line (line 21):

```html
    <div class="confirmation-icon" aria-hidden="true">&#x2709;</div>
```

with:

```html
    <div class="confirmation-icon" aria-hidden="true"><i class="fa-solid fa-envelope"></i></div>
```

- [ ] **Step 3: Enlarge `.confirmation-icon`**

In `static/style.css`, on the `.confirmation-icon` rule (line 343), change `font-size: 1.5rem;` to `font-size: 1.75rem;`:

```css
.confirmation-icon { width: 56px; height: 56px; border-radius: 50%; background: var(--color-success-bg); color: var(--color-success); display: flex; align-items: center; justify-content: center; font-size: 1.75rem; font-weight: bold; margin: 0 auto 1rem; }
```

- [ ] **Step 4: Verify manually**

Run: `go run .` (with `EVENT_SIGNUP_ADMIN_PASSWORD` set; `EVENT_SIGNUP_EMAIL_FROM` may stay unset — emails are logged, not sent).
Create or open a `secret_santa` event, self-register on its public page, and confirm the confirmation card now shows a clearly visible, properly proportioned envelope inside its circle. Glance at the other confirmation pages found in Step 1 to confirm the icon enlargement looks fine there too. Stop the server when done.

- [ ] **Step 5: Commit**

```bash
git add templates/public_santa.html static/style.css
git commit -m "fix(santa): enlarge the registration confirmation icon"
```

---

## Final verification

- [ ] **Run the full suite**

Run: `go vet ./... && go test .`
Expected: no vet output; `ok` — all tests pass.

- [ ] **Confirm no stray binary**

Run: `git status`
Expected: clean tree (no untracked `event-signup` binary — if one appears, `go build` was run by mistake; delete it).

---

## Self-Review (done while writing this plan)

**Spec coverage:**
- §7 CSV import → Tasks 1 (parser) + 2 (handler). BOM, delimiter detection, header mapping, lang normalization, skipped rows, post-draw refusal, idempotency, created/updated/skipped report — all covered by Task 1's table tests and Task 2's three tests.
- §8 send invitations → Tasks 3 (sender) + 4 (handler). "Skip already invited", "catch up new participants", concurrency guard reuse, post-draw refusal — covered.
- §8.3 reuse magic-link email + reword intro → Task 3 Step 5.
- §9 admin UI (import form, invite button, counter) → Task 5.
- §10 confirmation icon → Task 6.
- §12 i18n keys → Task 2 Step 1 (all keys) + Task 3 Step 5 (intro reword).
- §6 no data-model change → confirmed; no task touches `schema.sql` or `models.go`.
- §13 edge cases → no email column / empty file (Task 1), invalid-email rows skipped (Task 1), `;` + BOM (Task 1), import & invite after draw (Tasks 2 & 4), re-import idempotency (Task 2), re-invite no double-send (Task 3).

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command has an expected result.

**Type consistency:** `santaCSVRow{FirstName,LastName,Email,Lang}`, `parseSantaCSV(io.Reader) ([]santaCSVRow, int, error)`, `errSantaCSVNoEmail`, `normalizeSantaLang(string) string`, `santaCSVField(string) string` — used identically in Tasks 1 and 2. `dispatchInviteEmails(int64)` / `sendInviteEmails(int64)` — defined Task 3, called Task 4. `handleAdminSantaImport` / `handleAdminSantaInvite` — registered in `main.go` and `newMux`, defined Tasks 2 and 4. Existing functions used (`GetEvent`, `GetSantaParticipantByEmail`, `UpsertSantaParticipant`, `ListSantaParticipants`, `ListEmailMessages`, `RecordEmailSent`, `renderSantaLinkEmail`, `sendWithRetry`, `santaAdminData`, `newPageData`, `render`, `T`, `LangFromRequest`) verified against the current source.
