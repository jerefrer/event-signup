package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

//go:embed schema.sql
var schemaSQL string

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func main() {
	adminPassword := os.Getenv("EVENT_SIGNUP_ADMIN_PASSWORD")
	if adminPassword == "" {
		log.Fatal("EVENT_SIGNUP_ADMIN_PASSWORD environment variable is required")
	}

	dbPath := os.Getenv("EVENT_SIGNUP_DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data.db"
	}

	port := os.Getenv("EVENT_SIGNUP_PORT")
	if port == "" {
		port = "8090"
	}

	baseURL := os.Getenv("EVENT_SIGNUP_BASE_URL")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%s", port)
	}

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	db, err := InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	app := &App{
		DB:            db,
		AdminPassword: adminPassword,
		BaseURL:       baseURL,
		AnthropicKey:  anthropicKey,
	}

	mux := http.NewServeMux()

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Language switch
	mux.HandleFunc("/lang", app.handleLangSwitch)

	// Admin routes
	mux.HandleFunc("/admin/login", app.handleAdminLogin)
	mux.HandleFunc("/admin/logout", app.handleAdminLogout)
	mux.HandleFunc("/admin", app.requireAdmin(app.handleAdminEvents))
	mux.HandleFunc("/admin/event/new", app.requireAdmin(app.handleAdminEventNew))
	mux.HandleFunc("/admin/event/edit", app.requireAdmin(app.handleAdminEventEdit))
	mux.HandleFunc("/admin/event/delete", app.requireAdmin(app.handleAdminEventDelete))
	mux.HandleFunc("/admin/groups/save", app.requireAdmin(app.handleAdminGroupSave))
	mux.HandleFunc("/admin/groups/delete", app.requireAdmin(app.handleAdminGroupDelete))
	mux.HandleFunc("/admin/tasks/save", app.requireAdmin(app.handleAdminTaskSave))
	mux.HandleFunc("/admin/tasks/delete", app.requireAdmin(app.handleAdminTaskDelete))
	mux.HandleFunc("/admin/registrations/delete", app.requireAdmin(app.handleAdminRegistrationDelete))
	mux.HandleFunc("/admin/export", app.requireAdmin(app.handleAdminExportCSV))

	mux.HandleFunc("/admin/clear-all", app.requireAdmin(app.handleAdminClearAll))

	// Registrations page
	mux.HandleFunc("/admin/event/registrations", app.requireAdmin(app.handleAdminRegistrations))
	mux.HandleFunc("/admin/event/attendances", app.requireAdmin(app.handleAdminAttendances))
	mux.HandleFunc("/admin/attendances/delete", app.requireAdmin(app.handleAdminAttendanceDelete))

	// JSON APIs
	mux.HandleFunc("/admin/api/reorder", app.requireAdmin(app.handleAPIReorder))
	mux.HandleFunc("/admin/api/max-slots", app.requireAdmin(app.handleAPIUpdateMaxSlots))
	mux.HandleFunc("/admin/api/ai-parse", app.requireAdmin(app.handleAdminAIParse))
	mux.HandleFunc("/admin/api/event/save", app.requireAdmin(app.handleAPIEventSave))
	mux.HandleFunc("/admin/api/group/create", app.requireAdmin(app.handleAPIGroupCreate))
	mux.HandleFunc("/admin/api/group/save", app.requireAdmin(app.handleAPIGroupSave))
	mux.HandleFunc("/admin/api/group/delete", app.requireAdmin(app.handleAPIGroupDelete))
	mux.HandleFunc("/admin/api/task/create", app.requireAdmin(app.handleAPITaskCreate))
	mux.HandleFunc("/admin/api/task/save", app.requireAdmin(app.handleAPITaskSave))
	mux.HandleFunc("/admin/api/task/delete", app.requireAdmin(app.handleAPITaskDelete))

	// Public API
	mux.HandleFunc("/api/slots", app.handleAPISlots)

	// Public routes
	mux.HandleFunc("/e/", app.handlePublicEvent)
	mux.HandleFunc("/signup", app.handlePublicSignup)
	mux.HandleFunc("/rsvp", app.handlePublicRSVP)
mux.HandleFunc("/cancel/", app.handlePublicCancel)

	// Root redirect
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
	})

	addr := ":" + port
	log.Printf("Starting server on %s", addr)
	log.Printf("Admin: http://localhost:%s/admin", port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
