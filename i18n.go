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
