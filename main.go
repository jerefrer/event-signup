package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
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

// staticBuildID is a short hash of every file in the embedded staticFS,
// used as a ?v= query string on <script>/<link> tags so browsers refetch
// JS/CSS after a deploy that actually changes them — and reuse their cache
// when it doesn't.
var staticBuildID = computeStaticBuildID()

func computeStaticBuildID() string {
	h := sha256.New()
	err := fs.WalkDir(staticFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := staticFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(data)
		return nil
	})
	if err != nil {
		return "dev"
	}
	return hex.EncodeToString(h.Sum(nil)[:4])
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

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	emailFrom := os.Getenv("EVENT_SIGNUP_EMAIL_FROM")
	emailFromName := os.Getenv("EVENT_SIGNUP_EMAIL_FROM_NAME")
	emailConfigSet := os.Getenv("EVENT_SIGNUP_SES_CONFIGURATION_SET")
	var emailSender EmailSender
	if emailFrom != "" {
		s, err := NewSESSender(context.Background(), emailFrom, emailFromName, emailConfigSet)
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
		} else {
			log.Printf("WARNING: invalid EVENT_SIGNUP_EMAIL_RATE %q, using default %d/s", v, emailRate)
		}
	}

	// LogSender writes to stdout — no remote rate limit to respect, so fire
	// instantly. Speeds up local dev when sending invites to a long list.
	var emailDelay time.Duration
	if emailFrom != "" {
		emailDelay = time.Second / time.Duration(emailRate)
	}

	db, err := InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	app := &App{
		DB:             db,
		AdminPassword:  adminPassword,
		AnthropicKey:   anthropicKey,
		Email:          emailSender,
		EmailSendDelay: emailDelay,
		AsyncEmail:     true,
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
	mux.HandleFunc("/webhooks/ses", app.handleSESWebhook)
	mux.HandleFunc("/e/", app.handlePublicEvent)
	mux.HandleFunc("/signup", app.handlePublicSignup)
	mux.HandleFunc("/rsvp", app.handlePublicRSVP)
	mux.HandleFunc("/rsvp/lookup", app.handlePublicRSVPLookup)
	mux.HandleFunc("/cancel/", app.handlePublicCancel)
	mux.HandleFunc("/santa/register", app.handleSantaRegister)
	mux.HandleFunc("/santa/edit", app.handleSantaEdit)
	mux.HandleFunc("/admin/santa/draw", app.requireAdmin(app.handleAdminSantaDraw))
	mux.HandleFunc("/admin/santa/resend", app.requireAdmin(app.handleAdminSantaResend))
	mux.HandleFunc("/admin/santa/participant/delete", app.requireAdmin(app.handleAdminSantaParticipantDelete))
	mux.HandleFunc("/admin/santa/import", app.requireAdmin(app.handleAdminSantaImport))
	mux.HandleFunc("/admin/santa/invite", app.requireAdmin(app.handleAdminSantaInvite))

	// Dev email previews — admin-gated, render the same HTML the app would send.
	mux.HandleFunc("/dev/emails", app.requireAdmin(app.handleDevEmailIndex))
	mux.HandleFunc("/dev/emails/santa-link", app.requireAdmin(app.handleDevEmailSantaLink))
	mux.HandleFunc("/dev/emails/santa-reveal", app.requireAdmin(app.handleDevEmailSantaReveal))

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
