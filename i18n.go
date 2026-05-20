package main

import (
	"html/template"
	"net/http"
)

const (
	LangFR = "fr"
	LangEN = "en"

	DefaultLang = LangFR
)

var SupportedLangs = []string{LangFR, LangEN}

type Translations map[string]string

var translations = map[string]Translations{
	// General
	"app_title":   {"fr": "Évènements Chanteloube", "en": "Évènements Chanteloube"},
	"lang_switch": {"fr": "English", "en": "Français"},
	"save":        {"fr": "Enregistrer", "en": "Save"},
	"cancel":      {"fr": "Annuler", "en": "Cancel"},
	"delete":      {"fr": "Supprimer", "en": "Delete"},
	"edit":        {"fr": "Modifier", "en": "Edit"},
	"create":      {"fr": "Créer", "en": "Create"},
	"add":         {"fr": "Ajouter", "en": "Add"},
	"back":        {"fr": "Retour", "en": "Back"},
	"actions":     {"fr": "Actions", "en": "Actions"},
	"confirm":     {"fr": "Confirmer", "en": "Confirm"},
	"or":          {"fr": "ou", "en": "or"},

	// Admin login
	"admin_login":       {"fr": "Administration", "en": "Administration"},
	"admin_password":    {"fr": "Mot de passe", "en": "Password"},
	"admin_login_btn":   {"fr": "Se connecter", "en": "Log in"},
	"admin_login_error": {"fr": "Mot de passe incorrect", "en": "Incorrect password"},
	"admin_logout":      {"fr": "Déconnexion", "en": "Log out"},

	// Events list
	"events":           {"fr": "Événements", "en": "Events"},
	"event_new":        {"fr": "Nouvel événement", "en": "New Event"},
	"event_no_events":  {"fr": "Aucun événement créé.", "en": "No events yet."},
	"event_create_first": {"fr": "Créez votre premier événement", "en": "Create your first event"},
	"event_delete_confirm": {"fr": "Supprimer cet événement et toutes ses données ?", "en": "Delete this event and all its data?"},

	// Event edit
	"event_edit":       {"fr": "Modifier l'événement", "en": "Edit Event"},
	"event_details":    {"fr": "Détails de l'événement", "en": "Event Details"},
	"event_title_fr":   {"fr": "Titre (français)", "en": "Title (French)"},
	"event_title_en":   {"fr": "Titre (anglais)", "en": "Title (English)"},
	"event_desc_fr":    {"fr": "Description (français)", "en": "Description (French)"},
	"event_desc_en":    {"fr": "Description (anglais)", "en": "Description (English)"},
	"event_date":       {"fr": "Date", "en": "Date"},
	"event_time":       {"fr": "Heure", "en": "Time"},
	"event_public_link": {"fr": "Lien public", "en": "Public link"},
	"event_copy_link":  {"fr": "Copier", "en": "Copy"},
	"event_copied":     {"fr": "Copié !", "en": "Copied!"},

	// Sections
	"section_groups_tasks": {"fr": "Groupes et tâches", "en": "Groups & Tasks"},
	"section_registrations": {"fr": "Inscriptions", "en": "Registrations"},

	// Groups
	"group_new":        {"fr": "Nouveau groupe", "en": "New Group"},
	"group_title_fr":   {"fr": "Nom du groupe (FR)", "en": "Group name (FR)"},
	"group_title_en":   {"fr": "Nom du groupe (EN)", "en": "Group name (EN)"},
	"group_ungrouped":  {"fr": "Sans groupe", "en": "Ungrouped"},
	"group_drop_here":  {"fr": "Déposer ici", "en": "Drop here"},
	"group_clear_all":  {"fr": "Tout supprimer", "en": "Clear all"},
	"group_clear_confirm": {"fr": "Supprimer tous les groupes et tâches de cet événement ?", "en": "Delete all groups and tasks for this event?"},
	"group_parent":     {"fr": "Groupe parent", "en": "Parent group"},
	"group_root_level": {"fr": "— Racine —", "en": "— Root level —"},

	// Tasks
	"task_new":           {"fr": "Nouvelle tâche", "en": "New Task"},
	"task_title_fr":      {"fr": "Titre (FR)", "en": "Title (FR)"},
	"task_title_en":      {"fr": "Titre (EN)", "en": "Title (EN)"},
	"task_desc_fr":       {"fr": "Description (FR)", "en": "Description (FR)"},
	"task_desc_en":       {"fr": "Description (EN)", "en": "Description (EN)"},
	"task_max_slots":     {"fr": "Places max (vide = illimité)", "en": "Max slots (empty = unlimited)"},
	"task_no_tasks":      {"fr": "Aucune tâche. Ajoutez-en une ci-dessous.", "en": "No tasks yet. Add one below."},
	"task_unlimited":     {"fr": "illimité", "en": "unlimited"},
	"task_slots_remaining": {"fr": "places restantes", "en": "spots remaining"},
	"task_slots_taken":     {"fr": "inscriptions", "en": "registrations"},
	"task_full":            {"fr": "Complet", "en": "Full"},
	"task_max_people":        {"fr": "pers. max", "en": "max people"},
	"task_drag_hint":       {"fr": "Glisser pour réorganiser", "en": "Drag to reorder"},
	"task_add_description": {"fr": "ajouter une description", "en": "add description"},
	"task_hide_description": {"fr": "masquer la description", "en": "hide description"},

	// Registrations
	"registrations":            {"fr": "Inscriptions", "en": "Registrations"},
	"registration_first_name":  {"fr": "Prénom", "en": "First name"},
	"registration_last_name":   {"fr": "Nom", "en": "Last name"},
	"registration_email":       {"fr": "Email", "en": "Email"},
	"registration_phone":       {"fr": "Téléphone", "en": "Phone"},
	"registration_date":        {"fr": "Date", "en": "Date"},
	"registration_signup":      {"fr": "S'inscrire", "en": "Sign up"},
	"registration_no_regs":     {"fr": "Aucune inscription.", "en": "No registrations."},
	"registration_export_csv":  {"fr": "Exporter CSV", "en": "Export CSV"},
	"registration_delete_confirm": {"fr": "Supprimer cette inscription ?", "en": "Delete this registration?"},
	"registration_total":       {"fr": "Total inscriptions", "en": "Total registrations"},
	"registration_view_all":    {"fr": "Voir les inscriptions", "en": "View registrations"},
	"registration_group":       {"fr": "Groupe", "en": "Group"},
	"registration_search":      {"fr": "Rechercher…", "en": "Search…"},

	// Public
	"public_event_on":        {"fr": "Le", "en": "On"},
	"public_event_at":        {"fr": "à", "en": "at"},
	"public_signup_title":    {"fr": "Inscription", "en": "Sign Up"},
	"public_choose_task":     {"fr": "Choisissez une tâche", "en": "Choose a task"},
	"public_required_fields": {"fr": "Tous les champs sont obligatoires.", "en": "All fields are required."},
	"public_signup_btn":      {"fr": "Confirmer l'inscription", "en": "Confirm Registration"},
	"public_back_to_event":   {"fr": "Retour à l'événement", "en": "Back to event"},

	// Confirmation
	"confirmation_title":       {"fr": "Inscription confirmée", "en": "Registration Confirmed"},
	"confirmation_message":     {"fr": "Vous êtes inscrit(e) à :", "en": "You are registered for:"},
	"confirmation_task":        {"fr": "Tâche", "en": "Task"},
	"confirmation_first_name":  {"fr": "Prénom", "en": "First name"},
	"confirmation_last_name":   {"fr": "Nom", "en": "Last name"},
	"confirmation_email":       {"fr": "Email", "en": "Email"},
	"confirmation_phone":       {"fr": "Téléphone", "en": "Phone"},
	"confirmation_cancel_link": {"fr": "Lien de désinscription", "en": "Cancellation link"},
	"confirmation_cancel_info": {"fr": "Conservez ce lien pour vous désinscrire si nécessaire.", "en": "Keep this link to unregister if needed."},
	"confirmation_back":        {"fr": "Retour à l'événement", "en": "Back to event"},

	// Cancellation
	"cancel_title":       {"fr": "Désinscription", "en": "Cancellation"},
	"cancel_confirm_msg": {"fr": "Voulez-vous annuler votre inscription à cette tâche ?", "en": "Do you want to cancel your registration for this task?"},
	"cancel_success":     {"fr": "Votre inscription a été annulée.", "en": "Your registration has been cancelled."},
	"cancel_not_found":   {"fr": "Inscription introuvable.", "en": "Registration not found."},
	"cancel_btn":         {"fr": "Confirmer la désinscription", "en": "Confirm Cancellation"},

	// AI Import
	"ai_section":     {"fr": "Structurer avec l'IA", "en": "Structure with AI"},
	"ai_subtitle":    {"fr": "Décrivez les tâches et groupes en texte libre. L'IA créera ou mettra à jour la structure automatiquement.", "en": "Describe tasks and groups in free text. AI will create or update the structure automatically."},
	"ai_placeholder": {"fr": "Collez ici le texte décrivant les tâches et groupes…", "en": "Paste text describing tasks and groups…"},
	"ai_submit":      {"fr": "Appliquer", "en": "Apply"},
	"ai_loading":     {"fr": "Traitement en cours…", "en": "Processing…"},
	"ai_error":       {"fr": "Erreur IA", "en": "AI Error"},
	"ai_success":     {"fr": "Structure mise à jour avec succès !", "en": "Structure updated successfully!"},
	"ai_no_key":      {"fr": "Clé API Anthropic non configurée (ANTHROPIC_API_KEY)", "en": "Anthropic API key not configured (ANTHROPIC_API_KEY)"},
	"ai_default_one": {"fr": "Considérer que les tâches sans indications sont pour une personne maximum", "en": "Assume tasks without indication are for one person maximum"},

	// Registered (returning visitor)
	"already_registered":    {"fr": "Vous êtes déjà inscrit(e) pour cet événement.", "en": "You are already registered for this event."},
	"registered_title":      {"fr": "Vous êtes inscrit(e)", "en": "You are registered"},
	"registered_for_task":   {"fr": "Votre choix :", "en": "Your choice:"},
	"registered_change":     {"fr": "Modifier mon choix", "en": "Change my choice"},
	"registered_cancel":         {"fr": "Annuler mon inscription", "en": "Cancel my registration"},
	"cancel_confirm_dialog":     {"fr": "Êtes-vous sûr(e) de vouloir annuler votre inscription ?", "en": "Are you sure you want to cancel your registration?"},
	"cancel_success_inline":     {"fr": "Votre inscription a été annulée.", "en": "Your registration has been cancelled."},

	// Event type
	"event_type":            {"fr": "Type d'événement", "en": "Event type"},
	"event_type_tasks":      {"fr": "Inscription aux tâches", "en": "Task signup"},
	"event_type_attendance": {"fr": "Présence (RSVP)", "en": "Attendance (RSVP)"},
	"event_type_santa":      {"fr": "Secret Santa", "en": "Secret Santa"},

	// Secret Santa — public
	"santa_register_title":  {"fr": "Inscription au Secret Santa", "en": "Secret Santa Registration"},
	"santa_register_intro":  {"fr": "Inscrivez-vous pour recevoir par email votre lien personnel.", "en": "Sign up to receive your personal link by email."},
	"santa_disclaimer":      {"fr": "Nous ne pouvons pas garantir que vous recevrez quelque chose en retour. Nous espérons que ceux qui souhaitent participer le feront pleinement, mais souvenez-vous : c'est un exercice de DON, pas de réception — et en donnant, on reçoit naturellement !", "en": "We cannot guarantee you will receive anything in return. We hope those who wish to participate will do so fully, but please remember this is an exercise in GIVING, not receiving — and in giving, we will naturally receive!"},
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
	"santa_wish_make_hint":  {"fr": "Pour ceux qui n'ont pas d'argent — une plante, un plat, un poème, une prière…", "en": "For those short on money — a plant, a dish, a poem, a prayer…"},
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
	"santa_admin_emails_sent":     {"fr": "emails envoyés", "en": "emails sent"},
	"santa_admin_assigned_to":     {"fr": "Offre à", "en": "Gives to"},
	"santa_admin_completed_col":   {"fr": "Liste complétée", "en": "List completed"},
	"santa_admin_delete_confirm":  {"fr": "Supprimer ce participant ?", "en": "Delete this participant?"},
	"santa_admin_no_participants": {"fr": "Aucun inscrit.", "en": "No participants yet."},
	"santa_admin_yes":             {"fr": "Oui", "en": "Yes"},
	"santa_admin_no":              {"fr": "Non", "en": "No"},
	"santa_admin_view":            {"fr": "Gérer le Secret Santa", "en": "Manage Secret Santa"},
	"santa_admin_link_email_col":   {"fr": "Email lien", "en": "Link email"},
	"santa_admin_reveal_email_col": {"fr": "Email tirage", "en": "Draw email"},
	"santa_admin_email_problems":   {"fr": "problème(s) d'envoi", "en": "delivery problem(s)"},
	"email_status_sent":            {"fr": "Envoyé", "en": "Sent"},
	"email_status_delivered":       {"fr": "Remis", "en": "Delivered"},
	"email_status_bounced":         {"fr": "Rebond", "en": "Bounced"},
	"email_status_complaint":       {"fr": "Plainte", "en": "Complaint"},
	"email_status_rejected":        {"fr": "Rejeté", "en": "Rejected"},

	// Secret Santa — emails
	"santa_email_greeting":       {"fr": "Bonjour %s,", "en": "Hello %s,"},
	"santa_email_link_subject":   {"fr": "Votre lien pour", "en": "Your link for"},
	"santa_email_link_title":     {"fr": "Votre Secret Santa", "en": "Your Secret Santa"},
	"santa_email_link_intro":     {"fr": "Voici votre lien personnel pour composer votre liste de souhaits. Cliquez sur le bouton ci-dessous.", "en": "Here is your personal link to put together your wish list. Click the button below."},
	"santa_email_link_button":    {"fr": "Remplir ma liste", "en": "Fill in my list"},
	"santa_email_reveal_subject": {"fr": "Votre tirage Secret Santa pour", "en": "Your Secret Santa draw for"},
	"santa_email_reveal_title":   {"fr": "Le tirage est fait !", "en": "The draw is done!"},
	"santa_email_reveal_intro":   {"fr": "Vous offrez un cadeau à :", "en": "You are giving a gift to:"},
	"santa_email_reveal_wishes":  {"fr": "Voici ses souhaits :", "en": "Here are their wishes:"},
	"santa_email_reveal_link":    {"fr": "Voir l'événement", "en": "View the event"},

	// Public RSVP
	"rsvp_title":              {"fr": "Confirmer votre présence", "en": "Confirm Your Attendance"},
	"rsvp_attending_label":    {"fr": "Serez-vous présent(e) ?", "en": "Will you attend?"},
	"rsvp_attending_yes":      {"fr": "Oui", "en": "Yes"},
	"rsvp_attending_no":       {"fr": "Non", "en": "No"},
	"rsvp_message":            {"fr": "Message (optionnel)", "en": "Message (optional)"},
	"rsvp_message_placeholder": {"fr": "Un commentaire, une question…", "en": "A comment, a question…"},
	"rsvp_submit":             {"fr": "Envoyer ma réponse", "en": "Submit my response"},
	"rsvp_confirmed_yes":      {"fr": "Vous avez confirmé votre présence !", "en": "You confirmed your attendance!"},
	"rsvp_confirmed_no":       {"fr": "Merci de nous avoir prévenu(e).", "en": "Thank you for letting us know."},
	"rsvp_updated":            {"fr": "Votre réponse a été mise à jour.", "en": "Your response has been updated."},
	"rsvp_change":             {"fr": "Modifier ma réponse", "en": "Change my response"},

	// Admin attendance
	"section_attendances":        {"fr": "Présences", "en": "Attendances"},
	"attendance_total":           {"fr": "au total", "en": "total"},
	"attendance_attending":       {"fr": "Présent(e)", "en": "Attending"},
	"attendance_not_attending":   {"fr": "Absent(e)", "en": "Not attending"},
	"attendance_yes":             {"fr": "Oui", "en": "Yes"},
	"attendance_no":              {"fr": "Non", "en": "No"},
	"attendance_message":         {"fr": "Message", "en": "Message"},
	"attendance_delete_confirm":  {"fr": "Supprimer cette réponse ?", "en": "Delete this response?"},
	"attendance_no_responses":    {"fr": "Aucune réponse.", "en": "No responses yet."},
	"attendance_summary":         {"fr": "présent(e)s", "en": "attending"},

	// Errors
	"error_title":        {"fr": "Erreur", "en": "Error"},
	"error_not_found":    {"fr": "Page introuvable.", "en": "Page not found."},
	"error_full":         {"fr": "Cette tâche est complète, il n'y a plus de places disponibles.", "en": "This task is full, no spots available."},
	"error_invalid_form": {"fr": "Veuillez remplir tous les champs obligatoires.", "en": "Please fill in all required fields."},
	"error_server":       {"fr": "Erreur interne du serveur.", "en": "Internal server error."},
}

func T(key, lang string) string {
	if t, ok := translations[key]; ok {
		if s, ok := t[lang]; ok && s != "" {
			return s
		}
		if s, ok := t[DefaultLang]; ok {
			return s
		}
	}
	return key
}

func LangFromRequest(r *http.Request) string {
	if q := r.URL.Query().Get("lang"); q != "" {
		for _, l := range SupportedLangs {
			if q == l {
				return l
			}
		}
	}
	if c, err := r.Cookie("lang"); err == nil {
		for _, l := range SupportedLangs {
			if c.Value == l {
				return l
			}
		}
	}
	return DefaultLang
}

func SetLangCookie(w http.ResponseWriter, lang string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "lang",
		Value:    lang,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func SwitchLang(current string) string {
	if current == LangFR {
		return LangEN
	}
	return LangFR
}

func Localized(fr, en, lang string) string {
	if lang == LangEN && en != "" {
		return en
	}
	return fr
}

func TemplateFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(key string) string {
			return T(key, lang)
		},
		"loc": func(fr, en string) string {
			return Localized(fr, en, lang)
		},
		"switchLang": func() string {
			return SwitchLang(lang)
		},
		"lang": func() string {
			return lang
		},
		"otherLang": func() string {
			return SwitchLang(lang)
		},
	}
}
