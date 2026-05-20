package main

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

// EmailSender delivers a single transactional HTML email and returns the
// provider's message ID (used to correlate later delivery events).
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) (messageID string, err error)
}

// LogSender writes emails to the log instead of sending them. Used when SES is
// not configured (development, manual testing).
type LogSender struct{}

func (LogSender) Send(ctx context.Context, to, subject, htmlBody string) (string, error) {
	log.Printf("[email] to=%s subject=%q\n%s", to, subject, htmlBody)
	return "", nil
}

type santaLinkEmailData struct {
	Title, Greeting, Intro, ButtonText, EditURL, EventTitle, Disclaimer string
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
		Disclaimer: T("santa_disclaimer", lang),
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

// SESSender sends email through AWS SES (SESv2 API). Credentials and region are
// read from the standard AWS environment (AWS_REGION, AWS_ACCESS_KEY_ID, ...).
type SESSender struct {
	client    *sesv2.Client
	from      string
	configSet string // SES configuration set; enables delivery-event publishing
}

func NewSESSender(ctx context.Context, from, configSet string) (*SESSender, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESSender{client: sesv2.NewFromConfig(cfg), from: from, configSet: configSet}, nil
}

func (s *SESSender) Send(ctx context.Context, to, subject, htmlBody string) (string, error) {
	in := &sesv2.SendEmailInput{
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
	}
	if s.configSet != "" {
		in.ConfigurationSetName = aws.String(s.configSet)
	}
	out, err := s.client.SendEmail(ctx, in)
	if err != nil {
		return "", fmt.Errorf("ses send email: %w", err)
	}
	if out.MessageId == nil {
		return "", nil
	}
	return *out.MessageId, nil
}

// dispatchRevealEmails starts sending reveal emails. In production (AsyncEmail)
// it runs in a goroutine so the HTTP request returns immediately; in tests it
// runs synchronously. baseURL is captured from the triggering request so links
// in the email point back to the same host the admin is using.
func (app *App) dispatchRevealEmails(eventID int64, baseURL string) {
	if app.AsyncEmail {
		go app.sendRevealEmails(eventID, baseURL)
	} else {
		app.sendRevealEmails(eventID, baseURL)
	}
}

// sendRevealEmails sends the reveal email to every completed, assigned
// participant of the event who has not been emailed yet. It is rate-limited and
// guarded so only one send runs per event at a time.
func (app *App) sendRevealEmails(eventID int64, baseURL string) {
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
		subject, htmlBody := renderSantaRevealEmail(p.Lang, p, receiver, *event, baseURL)
		if htmlBody == "" {
			log.Printf("sendRevealEmails: empty rendered email body for participant %d, skipping", p.ID)
			continue
		}
		messageID, err := app.sendWithRetry(p.Email, subject, htmlBody)
		if err != nil {
			log.Printf("sendRevealEmails: send to %s failed: %v", p.Email, err)
			continue
		}
		if err := MarkRevealEmailSent(app.DB, p.ID); err != nil {
			log.Printf("sendRevealEmails: mark sent %d: %v", p.ID, err)
		}
		if err := RecordEmailSent(app.DB, p.ID, "reveal", messageID, p.Email); err != nil {
			log.Printf("sendRevealEmails: record %d: %v", p.ID, err)
		}
	}
}

// dispatchInviteEmails starts sending invitation emails. In production
// (AsyncEmail) it runs in a goroutine so the HTTP request returns immediately;
// in tests it runs synchronously. baseURL is captured from the triggering
// request so the magic links point back to the same host the admin is using.
func (app *App) dispatchInviteEmails(eventID int64, baseURL string) {
	if app.AsyncEmail {
		go app.sendInviteEmails(eventID, baseURL)
	} else {
		app.sendInviteEmails(eventID, baseURL)
	}
}

// sendInviteEmails sends the magic-link email to every participant of the event
// who has not been sent a "link" email yet. It is rate-limited and guarded so
// only one send runs per event at a time.
func (app *App) sendInviteEmails(eventID int64, baseURL string) {
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
		editURL := fmt.Sprintf("%s/santa/edit?token=%s&lang=%s", baseURL, p.Token, p.Lang)
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

// sendWithRetry retries a transient send failure up to 3 attempts.
func (app *App) sendWithRetry(to, subject, htmlBody string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			// EmailSendDelay doubles as the retry backoff — adequate at this
			// app's scale; a dedicated retry-delay field would be over-engineering.
			time.Sleep(app.EmailSendDelay)
		}
		messageID, err := app.Email.Send(context.Background(), to, subject, htmlBody)
		if err == nil {
			return messageID, nil
		}
		lastErr = err
	}
	return "", lastErr
}
