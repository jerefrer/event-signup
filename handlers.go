package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type App struct {
	DB            *sql.DB
	AdminPassword string
	BaseURL       string
	AnthropicKey  string
}

type PageData struct {
	Lang      string
	OtherLang string
	LangURL   string
	Data      any
	Error     string
	Success   string
}

func (app *App) newPageData(r *http.Request, data any) PageData {
	lang := LangFromRequest(r)
	other := SwitchLang(lang)
	langURL := r.URL.Path + "?lang=" + other
	if r.URL.RawQuery != "" {
		q := r.URL.Query()
		q.Set("lang", other)
		langURL = r.URL.Path + "?" + q.Encode()
	}
	return PageData{
		Lang:      lang,
		OtherLang: other,
		LangURL:   langURL,
		Data:      data,
	}
}

func (app *App) render(w http.ResponseWriter, r *http.Request, tmpl string, data PageData) {
	lang := data.Lang
	funcs := app.buildFuncs(lang)
	funcs["isAdmin"] = func() bool { return strings.HasPrefix(tmpl, "admin_") }

	t, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/layout.html", "templates/"+tmpl)
	if err != nil {
		http.Error(w, "template error", 500)
		log.Printf("template parse error: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, tmpl, data); err != nil {
		log.Printf("template execute error (%s): %v", tmpl, err)
	}
}

func (app *App) buildFuncs(lang string) template.FuncMap {
	funcs := TemplateFuncs(lang)
	funcs["safeHTML"] = func(s string) template.HTML { return template.HTML(s) }
	funcs["nl2br"] = func(s string) template.HTML {
		return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
	}
	funcs["formatDate"] = func(s string) string {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return s
		}
		if lang == LangFR {
			days := []string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
			months := []string{"", "janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
			return fmt.Sprintf("%s %d %s %d", days[t.Weekday()], t.Day(), months[t.Month()], t.Year())
		}
		return t.Format("Monday, January 2, 2006")
	}
	funcs["formatTime"] = func(s string) string {
		if s == "" {
			return ""
		}
		t, err := time.Parse("15:04", s)
		if err != nil {
			return s
		}
		if lang == LangFR {
			return t.Format("15h04")
		}
		return t.Format("3:04 PM")
	}
	funcs["formatDateTime"] = func(t time.Time) string {
		if lang == LangFR {
			return t.Format("02/01/2006 15:04")
		}
		return t.Format("Jan 2, 2006 3:04 PM")
	}
	funcs["add"] = func(a, b int) int { return a + b }
	funcs["sub"] = func(a, b int) int { return a - b }
	funcs["json"] = func(v any) template.JS {
		b, _ := json.Marshal(v)
		return template.JS(b)
	}
	funcs["dict"] = func(pairs ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(pairs); i += 2 {
			m[fmt.Sprint(pairs[i])] = pairs[i+1]
		}
		return m
	}
	funcs["indent"] = func(depth int) string {
		return strings.Repeat("— ", depth)
	}
	return funcs
}

// ---- Middleware ----

func (app *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err != nil || cookie.Value != app.adminSessionValue() {
			if r.Header.Get("Content-Type") == "application/json" {
				http.Error(w, `{"error":"unauthorized"}`, 401)
				return
			}
			http.Redirect(w, r, "/admin/login?lang="+LangFromRequest(r), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (app *App) adminSessionValue() string {
	return fmt.Sprintf("%x", sha256Sum([]byte(app.AdminPassword)))
}

func (app *App) handleLangSwitch(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = SwitchLang(LangFromRequest(r))
	}
	SetLangCookie(w, lang)
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = "/"
	}
	http.Redirect(w, r, ref, http.StatusSeeOther)
}

// ---- Admin Login ----

func (app *App) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	pd := app.newPageData(r, nil)
	if r.Method == http.MethodPost {
		if r.FormValue("password") == app.AdminPassword {
			http.SetCookie(w, &http.Cookie{
				Name:     "admin_session",
				Value:    app.adminSessionValue(),
				Path:     "/",
				MaxAge:   24 * 60 * 60,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/admin?lang="+pd.Lang, http.StatusSeeOther)
			return
		}
		pd.Error = T("admin_login_error", pd.Lang)
	}
	app.render(w, r, "admin_login.html", pd)
}

func (app *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// ---- Admin Events List ----

func (app *App) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	events, _ := ListEvents(app.DB)
	for i := range events {
		events[i].RegCount = CountRegistrations(app.DB, events[i].ID)
	}
	pd := app.newPageData(r, map[string]any{
		"Events":  events,
		"BaseURL": app.BaseURL,
	})
	app.render(w, r, "admin_events.html", pd)
}

// ---- Admin Event Create ----

func (app *App) handleAdminEventNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		e := &Event{
			TitleFR:       r.FormValue("title_fr"),
			TitleEN:       r.FormValue("title_en"),
			DescriptionFR: r.FormValue("description_fr"),
			DescriptionEN: r.FormValue("description_en"),
			EventDate:     r.FormValue("event_date"),
			EventTime:     r.FormValue("event_time"),
		}
		if e.TitleFR == "" || e.EventDate == "" {
			pd := app.newPageData(r, map[string]any{"Event": e, "IsNew": true})
			pd.Error = T("error_invalid_form", pd.Lang)
			app.render(w, r, "admin_event_edit.html", pd)
			return
		}
		if err := CreateEvent(app.DB, e); err != nil {
			log.Printf("create event error: %v", err)
			pd := app.newPageData(r, map[string]any{"Event": e, "IsNew": true})
			pd.Error = T("error_server", pd.Lang)
			app.render(w, r, "admin_event_edit.html", pd)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s", e.ID, LangFromRequest(r)), http.StatusSeeOther)
		return
	}
	pd := app.newPageData(r, map[string]any{"Event": &Event{}, "IsNew": true})
	app.render(w, r, "admin_event_edit.html", pd)
}

// ---- Admin Event Edit (UNIFIED PAGE) ----

func (app *App) handleAdminEventEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	event, err := GetEvent(app.DB, id)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)

	if r.Method == http.MethodPost {
		event.TitleFR = r.FormValue("title_fr")
		event.TitleEN = r.FormValue("title_en")
		event.DescriptionFR = r.FormValue("description_fr")
		event.DescriptionEN = r.FormValue("description_en")
		event.EventDate = r.FormValue("event_date")
		event.EventTime = r.FormValue("event_time")

		if event.TitleFR == "" || event.EventDate == "" {
			pd := app.newPageData(r, app.eventEditData(event))
			pd.Error = T("error_invalid_form", lang)
			app.render(w, r, "admin_event_edit.html", pd)
			return
		}
		if err := UpdateEvent(app.DB, event); err != nil {
			log.Printf("update event error: %v", err)
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s", event.ID, lang), http.StatusSeeOther)
		return
	}

	pd := app.newPageData(r, app.eventEditData(event))
	app.render(w, r, "admin_event_edit.html", pd)
}

func (app *App) eventEditData(event *Event) map[string]any {
	tree, _ := BuildEventTree(app.DB, event.ID)
	flatGroups, _ := BuildFlatGroupList(app.DB, event.ID)
	loadTreeRegistrations(app.DB, tree)
	allTasks := CollectTaskViews(tree)
	totalRegs := CountRegistrations(app.DB, event.ID)

	return map[string]any{
		"Event":      event,
		"IsNew":      false,
		"Tree":       tree,
		"FlatGroups": flatGroups,
		"AllTasks":   allTasks,
		"TotalRegs":  totalRegs,
		"BaseURL":    app.BaseURL,
		"HasAI":      app.AnthropicKey != "",
	}
}

func loadTreeRegistrations(db *sql.DB, nodes []TreeNode) {
	for i := range nodes {
		if nodes[i].Type == "task" && nodes[i].Task != nil {
			regs, _ := ListRegistrations(db, nodes[i].Task.ID)
			nodes[i].Task.Registrations = regs
		}
		if nodes[i].Type == "group" {
			loadTreeRegistrations(db, nodes[i].Children)
		}
	}
}

func (app *App) handleAdminEventDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	DeleteEvent(app.DB, id)
	http.Redirect(w, r, "/admin?lang="+LangFromRequest(r), http.StatusSeeOther)
}

// ---- Admin Group CRUD (form-based, redirects back to event edit) ----

func (app *App) handleAdminGroupSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)

	g := &TaskGroup{ID: id, EventID: eventID, TitleFR: r.FormValue("title_fr"), TitleEN: r.FormValue("title_en")}
	if pid := r.FormValue("parent_group_id"); pid != "" && pid != "0" {
		v, _ := strconv.ParseInt(pid, 10, 64)
		g.ParentGroupID = sql.NullInt64{Int64: v, Valid: true}
	}
	if id > 0 {
		UpdateTaskGroup(app.DB, g)
	} else {
		CreateTaskGroup(app.DB, g)
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s#groups-tasks", eventID, lang), http.StatusSeeOther)
}

func (app *App) handleAdminGroupDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	DeleteTaskGroup(app.DB, id)
	http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s#groups-tasks", eventID, lang), http.StatusSeeOther)
}

// ---- Admin Task CRUD ----

func (app *App) handleAdminTaskSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)

	t := &Task{
		ID: id, EventID: eventID,
		TitleFR: r.FormValue("title_fr"), TitleEN: r.FormValue("title_en"),
		DescriptionFR: r.FormValue("description_fr"), DescriptionEN: r.FormValue("description_en"),
	}

	if ms := r.FormValue("max_slots"); ms != "" {
		v, _ := strconv.ParseInt(ms, 10, 64)
		if v > 0 {
			t.MaxSlots = sql.NullInt64{Int64: v, Valid: true}
		}
	}

	if id > 0 {
		// group_id is managed by drag-and-drop reorder, not inline edits
		UpdateTask(app.DB, t)
	} else {
		// Only set group_id when creating new tasks
		if gid := r.FormValue("group_id"); gid != "" && gid != "0" {
			v, _ := strconv.ParseInt(gid, 10, 64)
			t.GroupID = sql.NullInt64{Int64: v, Valid: true}
		}
		CreateTask(app.DB, t)
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s#groups-tasks", eventID, lang), http.StatusSeeOther)
}

func (app *App) handleAdminTaskDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	DeleteTask(app.DB, id)
	http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s#groups-tasks", eventID, lang), http.StatusSeeOther)
}

// ---- Admin Registration Delete ----

func (app *App) handleAdminRegistrationDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	DeleteRegistration(app.DB, id)
	http.Redirect(w, r, fmt.Sprintf("/admin/event/registrations?id=%d&lang=%s", eventID, lang), http.StatusSeeOther)
}

func (app *App) handleAdminClearAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	eventID, _ := strconv.ParseInt(r.FormValue("event_id"), 10, 64)
	app.DB.Exec("DELETE FROM tasks WHERE event_id=?", eventID)
	app.DB.Exec("DELETE FROM task_groups WHERE event_id=?", eventID)
	http.Redirect(w, r, fmt.Sprintf("/admin/event/edit?id=%d&lang=%s#groups-tasks", eventID, lang), http.StatusSeeOther)
}

func (app *App) handleAPIUpdateMaxSlots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		TaskID   int64  `json:"task_id"`
		MaxSlots *int64 `json:"max_slots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	var ms sql.NullInt64
	if req.MaxSlots != nil && *req.MaxSlots > 0 {
		ms = sql.NullInt64{Int64: *req.MaxSlots, Valid: true}
	}
	app.DB.Exec("UPDATE tasks SET max_slots=? WHERE id=?", ms, req.TaskID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ---- Admin CSV Export ----

func (app *App) handleAdminExportCSV(w http.ResponseWriter, r *http.Request) {
	eventID, _ := strconv.ParseInt(r.URL.Query().Get("event_id"), 10, 64)
	event, err := GetEvent(app.DB, eventID)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	regs, _ := ListAllRegistrations(app.DB, eventID)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-inscriptions.csv"`, event.Slug))
	w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	cw.Write([]string{"Groupe", "Tâche", "Prénom", "Nom", "Email", "Téléphone", "Date inscription"})
	for _, reg := range regs {
		cw.Write([]string{reg.GroupTitle, reg.TaskTitle, reg.FirstName, reg.LastName, reg.Email, reg.Phone, reg.CreatedAt.Format("2006-01-02 15:04")})
	}
	cw.Flush()
}

// ---- JSON API for drag-and-drop (unified tree reorder) ----

func (app *App) handleAPIReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var nodes []ReorderNode
	if err := json.NewDecoder(r.Body).Decode(&nodes); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if err := ApplyReorder(app.DB, nodes, sql.NullInt64{}); err != nil {
		log.Printf("reorder error: %v", err)
		http.Error(w, `{"error":"server error"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ---- JSON APIs for inline editing ----

func (app *App) handleAPIEventSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		EventID       int64  `json:"event_id"`
		TitleFR       string `json:"title_fr"`
		TitleEN       string `json:"title_en"`
		DescriptionFR string `json:"description_fr"`
		DescriptionEN string `json:"description_en"`
		EventDate     string `json:"event_date"`
		EventTime     string `json:"event_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	e := &Event{
		ID: req.EventID, TitleFR: req.TitleFR, TitleEN: req.TitleEN,
		DescriptionFR: req.DescriptionFR, DescriptionEN: req.DescriptionEN,
		EventDate: req.EventDate, EventTime: req.EventTime,
	}
	if err := UpdateEvent(app.DB, e); err != nil {
		http.Error(w, `{"error":"save failed"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (app *App) handleAPIGroupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		EventID int64 `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	g := &TaskGroup{EventID: req.EventID, TitleFR: "", TitleEN: ""}
	if err := CreateTaskGroup(app.DB, g); err != nil {
		http.Error(w, `{"error":"create failed"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"id": g.ID})
}

func (app *App) handleAPIGroupSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		ID      int64  `json:"id"`
		TitleFR string `json:"title_fr"`
		TitleEN string `json:"title_en"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	g := &TaskGroup{ID: req.ID, TitleFR: req.TitleFR, TitleEN: req.TitleEN}
	if err := UpdateTaskGroup(app.DB, g); err != nil {
		http.Error(w, `{"error":"save failed"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (app *App) handleAPIGroupDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	DeleteTaskGroup(app.DB, req.ID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (app *App) handleAPITaskCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		EventID int64 `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	t := &Task{EventID: req.EventID, TitleFR: "", TitleEN: ""}
	if err := CreateTask(app.DB, t); err != nil {
		http.Error(w, `{"error":"create failed"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"id": t.ID})
}

func (app *App) handleAPITaskSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		ID            int64  `json:"id"`
		TitleFR       string `json:"title_fr"`
		TitleEN       string `json:"title_en"`
		DescriptionFR string `json:"description_fr"`
		DescriptionEN string `json:"description_en"`
		MaxSlots      *int64 `json:"max_slots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	t := &Task{
		ID: req.ID, TitleFR: req.TitleFR, TitleEN: req.TitleEN,
		DescriptionFR: req.DescriptionFR, DescriptionEN: req.DescriptionEN,
	}
	if req.MaxSlots != nil && *req.MaxSlots > 0 {
		t.MaxSlots = sql.NullInt64{Int64: *req.MaxSlots, Valid: true}
	}
	if err := UpdateTask(app.DB, t); err != nil {
		http.Error(w, `{"error":"save failed"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (app *App) handleAPITaskDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	DeleteTask(app.DB, req.ID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ---- Admin Registrations Page ----

func (app *App) handleAdminRegistrations(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	event, err := GetEvent(app.DB, id)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	allRegs, _ := ListAllRegistrations(app.DB, event.ID)
	totalRegs := CountRegistrations(app.DB, event.ID)

	pd := app.newPageData(r, map[string]any{
		"Event":     event,
		"AllRegs":   allRegs,
		"TotalRegs": totalRegs,
	})
	app.render(w, r, "admin_registrations.html", pd)
}

// ---- Public API: slot availability ----

func (app *App) handleAPISlots(w http.ResponseWriter, r *http.Request) {
	eventID, _ := strconv.ParseInt(r.URL.Query().Get("event_id"), 10, 64)
	if eventID == 0 {
		http.Error(w, `{"error":"missing event_id"}`, 400)
		return
	}
	views, err := GetTaskViews(app.DB, eventID)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	type slotInfo struct {
		ID        int64 `json:"id"`
		SlotsLeft int   `json:"slots_left"`
		IsFull    bool  `json:"is_full"`
	}
	result := make([]slotInfo, len(views))
	for i, v := range views {
		result[i] = slotInfo{ID: v.ID, SlotsLeft: v.SlotsLeft, IsFull: v.IsFull}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ---- Public ----

func (app *App) handlePublicEvent(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/e/")
	slug = strings.TrimSuffix(slug, "/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	event, err := GetEventBySlug(app.DB, slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tree, _ := BuildEventTree(app.DB, event.ID)
	pd := app.newPageData(r, map[string]any{"Event": event, "Tree": tree})
	app.render(w, r, "public_event.html", pd)
}

func (app *App) handlePublicSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	lang := LangFromRequest(r)
	taskID, _ := strconv.ParseInt(r.FormValue("task_id"), 10, 64)
	task, err := GetTask(app.DB, taskID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	event, err := GetEvent(app.DB, task.EventID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	phone := strings.TrimSpace(r.FormValue("phone"))

	if firstName == "" || lastName == "" || email == "" || phone == "" {
		tree, _ := BuildEventTree(app.DB, event.ID)
		pd := app.newPageData(r, map[string]any{"Event": event, "Tree": tree})
		pd.Error = T("error_invalid_form", lang)
		app.render(w, r, "public_event.html", pd)
		return
	}

	// Check if this is a "change" request (has cancel_token from localStorage)
	cancelToken := strings.TrimSpace(r.FormValue("cancel_token"))
	if cancelToken != "" {
		existingReg, _ := GetRegistrationByToken(app.DB, cancelToken)
		if existingReg != nil {
			if existingReg.TaskID == taskID {
				// Same task selected — just show confirmation again
				pd := app.newPageData(r, map[string]any{
					"Event": event, "Task": task, "Reg": existingReg,
					"CancelURL": fmt.Sprintf("%s/cancel/%s", app.BaseURL, existingReg.Token),
				})
				app.render(w, r, "confirmation.html", pd)
				return
			}
			// Delete old registration before creating new one
			DeleteRegistrationByToken(app.DB, cancelToken)
		}
	} else {
		// No cancel_token — check for duplicate email (different device case)
		existingReg, _ := GetRegistrationByEmailAndEvent(app.DB, email, event.ID)
		if existingReg != nil {
			existingTask, _ := GetTask(app.DB, existingReg.TaskID)
			pd := app.newPageData(r, map[string]any{
				"Event": event, "Task": existingTask, "Reg": existingReg,
				"CancelURL": fmt.Sprintf("%s/cancel/%s", app.BaseURL, existingReg.Token),
			})
			pd.Success = T("already_registered", lang)
			app.render(w, r, "confirmation.html", pd)
			return
		}
	}

	reg, err := RegisterForTask(app.DB, taskID, firstName, lastName, email, phone)
	if err != nil {
		if strings.Contains(err.Error(), "task_full") {
			tree, _ := BuildEventTree(app.DB, event.ID)
			pd := app.newPageData(r, map[string]any{"Event": event, "Tree": tree})
			pd.Error = T("error_full", lang)
			app.render(w, r, "public_event.html", pd)
			return
		}
		log.Printf("registration error: %v", err)
		http.Error(w, T("error_server", lang), 500)
		return
	}

	pd := app.newPageData(r, map[string]any{
		"Event": event, "Task": task, "Reg": reg,
		"CancelURL": fmt.Sprintf("%s/cancel/%s", app.BaseURL, reg.Token),
	})
	app.render(w, r, "confirmation.html", pd)
}

func (app *App) handlePublicCancel(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/cancel/")
	token = strings.TrimSuffix(token, "/")
	lang := LangFromRequest(r)

	reg, err := GetRegistrationByToken(app.DB, token)
	if err != nil {
		pd := app.newPageData(r, nil)
		pd.Error = T("cancel_not_found", lang)
		app.render(w, r, "cancel.html", pd)
		return
	}
	task, _ := GetTask(app.DB, reg.TaskID)
	event, _ := GetEvent(app.DB, task.EventID)

	if r.Method == http.MethodPost {
		DeleteRegistrationByToken(app.DB, token)
		pd := app.newPageData(r, map[string]any{"Event": event, "Task": task, "Success": true})
		pd.Success = T("cancel_success", lang)
		app.render(w, r, "cancel.html", pd)
		return
	}

	pd := app.newPageData(r, map[string]any{"Event": event, "Task": task, "Reg": reg, "Token": token})
	app.render(w, r, "cancel.html", pd)
}
