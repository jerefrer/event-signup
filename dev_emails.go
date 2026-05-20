package main

// Dev-only email previews — admin-gated routes that render the same HTML the
// app would send to participants. Lets us iterate on the email design without
// triggering any send and without having to read the body out of the log.

import (
	"database/sql"
	"fmt"
	"net/http"
)

// handleDevEmailIndex lists the available email previews.
func (app *App) handleDevEmailIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Email previews</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 640px; margin: 2rem auto; padding: 0 1rem; color: #222; }
h1 { font-size: 1.5rem; margin-bottom: 0.5rem; }
p { color: #555; margin-bottom: 1.5rem; }
ul { list-style: none; padding: 0; margin: 0; }
li { margin: 0.75rem 0; display: flex; justify-content: space-between; align-items: center; gap: 0.75rem; padding: 0.75rem 1rem; background: #f6f6f6; border-radius: 6px; }
.links a { color: #0366d6; text-decoration: none; margin-left: 0.75rem; padding: 0.25rem 0.5rem; border: 1px solid transparent; border-radius: 4px; }
.links a:hover { background: #fff; border-color: #0366d6; }
</style></head><body>
<h1>Email previews</h1>
<p>Each link renders the exact HTML the app would email to participants, using real data from the latest Secret Santa event. Hit Cmd-R after a template change to re-render.</p>
<ul>
  <li><strong>Lien magique / Invitation</strong><span class="links"><a href="/dev/emails/santa-link?lang=fr">FR</a><a href="/dev/emails/santa-link?lang=en">EN</a></span></li>
  <li><strong>Tirage / Reveal</strong><span class="links"><a href="/dev/emails/santa-reveal?lang=fr">FR</a><a href="/dev/emails/santa-reveal?lang=en">EN</a></span></li>
</ul>
</body></html>`)
}

// handleDevEmailSantaLink renders the magic-link email for the most recent
// santa participant in the database.
func (app *App) handleDevEmailSantaLink(w http.ResponseWriter, r *http.Request) {
	lang := LangFromRequest(r)
	p, event, err := devLatestSantaParticipant(app.DB)
	if err != nil {
		http.Error(w, "preview unavailable: "+err.Error(), http.StatusNotFound)
		return
	}
	editURL := fmt.Sprintf("%s/santa/edit?token=%s&lang=%s", baseURLFor(r), p.Token, lang)
	_, html := renderSantaLinkEmail(lang, *p, *event, editURL)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// handleDevEmailSantaReveal renders the reveal email using the first two
// completed participants of the latest eligible Secret Santa event as a fake
// giver / receiver pair.
func (app *App) handleDevEmailSantaReveal(w http.ResponseWriter, r *http.Request) {
	lang := LangFromRequest(r)
	giver, receiver, event, err := devSantaRevealPair(app.DB)
	if err != nil {
		http.Error(w, "preview unavailable: "+err.Error(), http.StatusNotFound)
		return
	}
	_, html := renderSantaRevealEmail(lang, *giver, *receiver, *event, baseURLFor(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// devLatestSantaParticipant returns the most recent participant of any Secret
// Santa event, along with its event row. Used to populate the link-email preview.
func devLatestSantaParticipant(db *sql.DB) (*SantaParticipant, *Event, error) {
	var pid int64
	err := db.QueryRow(`SELECT sp.id FROM santa_participants sp
		JOIN events e ON e.id = sp.event_id
		WHERE e.event_type = 'secret_santa'
		ORDER BY sp.id DESC LIMIT 1`).Scan(&pid)
	if err != nil {
		return nil, nil, fmt.Errorf("no Secret Santa participant in any event yet")
	}
	p, err := GetSantaParticipant(db, pid)
	if err != nil {
		return nil, nil, err
	}
	e, err := GetEvent(db, p.EventID)
	if err != nil {
		return nil, nil, err
	}
	return p, e, nil
}

// devSantaRevealPair picks two completed participants from the latest eligible
// Secret Santa event and returns them as a giver / receiver pair plus the event.
func devSantaRevealPair(db *sql.DB) (*SantaParticipant, *SantaParticipant, *Event, error) {
	var eid int64
	err := db.QueryRow(`SELECT e.id FROM events e
		JOIN santa_participants sp ON sp.event_id = e.id
		WHERE e.event_type = 'secret_santa' AND sp.completed_at IS NOT NULL
		GROUP BY e.id HAVING COUNT(*) >= 2
		ORDER BY e.id DESC LIMIT 1`).Scan(&eid)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("need at least 2 completed participants in a Secret Santa event")
	}
	rows, err := db.Query("SELECT id FROM santa_participants WHERE event_id = ? AND completed_at IS NOT NULL ORDER BY id LIMIT 2", eid)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, nil, nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		return nil, nil, nil, fmt.Errorf("not enough completed participants")
	}
	giver, err := GetSantaParticipant(db, ids[0])
	if err != nil {
		return nil, nil, nil, err
	}
	receiver, err := GetSantaParticipant(db, ids[1])
	if err != nil {
		return nil, nil, nil, err
	}
	event, err := GetEvent(db, eid)
	if err != nil {
		return nil, nil, nil, err
	}
	return giver, receiver, event, nil
}
