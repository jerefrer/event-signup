package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
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

type santaLinkEmailData struct {
	Title, Greeting, Intro, ButtonText, EditURL, EventTitle string
}

type santaRevealEmailData struct {
	Title, Greeting, Intro, ReceiverName, WishesIntro string
	WishBuyLabel, WishMakeLabel, WishFreeLabel        string
	WishBuy, WishMake, WishFree                       string
	EventURL, EventLinkText                           string
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

// renderEmailTemplate executes an embedded email template (no layout). The
// template name must equal the file's base name as registered by ParseFS.
// Returns "" on failure (logged); callers that send email MUST treat an empty
// result as a hard error and abort the send rather than deliver a blank body.
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
