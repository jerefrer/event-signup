package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// EmailSender delivers a single transactional HTML email and returns the
// provider's message ID (used to correlate later delivery events). Optional
// attachments are delivered alongside the HTML body.
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string, attachments ...emailAttachment) (messageID string, err error)
}

// emailAttachment is a file delivered alongside the HTML body. SES carries
// attachments only via a raw MIME message, so any send with attachments
// switches from the Simple content type to Raw (see buildRawMIME).
type emailAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// soleilDeLaConfiancePDF is the "Soleil de la confiance" teaching, attached to
// every invitation/reminder email. Embedded so it ships inside the binary —
// consistent with how templates, static assets, and the schema are bundled.
//
//go:embed "Le soleil de la confiance.pdf"
var soleilDeLaConfiancePDF []byte

// invitationAttachments returns the files attached to every invitation/reminder
// email. A fresh slice per call keeps callers from sharing mutable state; the
// underlying Data is read-only.
func invitationAttachments() []emailAttachment {
	return []emailAttachment{{
		Filename:    "Le soleil de la confiance.pdf",
		ContentType: "application/pdf",
		Data:        soleilDeLaConfiancePDF,
	}}
}

// LogSender writes emails to the log instead of sending them. Used when SES is
// not configured (development, manual testing).
type LogSender struct{}

func (LogSender) Send(ctx context.Context, to, subject, htmlBody string, attachments ...emailAttachment) (string, error) {
	if len(attachments) > 0 {
		names := make([]string, len(attachments))
		for i, a := range attachments {
			names[i] = a.Filename
		}
		log.Printf("[email] to=%s subject=%q attachments=%v\n%s", to, subject, names, htmlBody)
		return "", nil
	}
	log.Printf("[email] to=%s subject=%q\n%s", to, subject, htmlBody)
	return "", nil
}

// Fields shared by every email — used by email_layout.html to render the
// outer shell (HTML lang attribute, logo, title bar). LogoURL is an absolute
// URL the email client can fetch directly.
type emailCommon struct {
	Lang, Title, LogoURL string
}

type santaLinkEmailData struct {
	emailCommon
	Greeting, Hook                            string
	HowItWorksTitle, Step1, Step2, Step3      string
	ButtonText, EditURL                       string
	EventDescription                          template.HTML
	Disclaimer                                string
}

type santaRevealEmailData struct {
	emailCommon
	Greeting, Intro, ReceiverName, WishesIntro string
	WishBuyLabel, WishMakeLabel, WishFreeLabel string
	WishBuy, WishMake, WishFree                string
}

// logoURLFromBase returns the absolute URL of the email logo (the red
// Dharmachakra) for a given base URL. The web UI uses a different logo at
// /static/logo.png — the email-specific one lives at /static/logo-email.png.
func logoURLFromBase(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	return baseURL + "/static/logo-email.png"
}

// emailLabel formats a wish-list label for the reveal email — appends a
// colon in English ("Label:") and a non-breaking-space + colon in French
// ("Label :") so French typography is respected and the colon never wraps
// onto its own line.
func emailLabel(label, lang string) string {
	if lang == LangFR {
		return label + "\u00a0:"
	}
	return label + ":"
}

// baseFromURL extracts scheme://host from any absolute URL — used to derive
// the logo URL when only an editURL is available.
func baseFromURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// renderSantaLinkEmail builds the magic-link email in the given language.
func renderSantaLinkEmail(lang string, p SantaParticipant, event Event, editURL string) (subject, html string) {
	eventTitle := Localized(event.TitleFR, event.TitleEN, lang)
	descRaw := Localized(event.DescriptionFR, event.DescriptionEN, lang)
	desc := template.HTML(strings.ReplaceAll(template.HTMLEscapeString(descRaw), "\n", "<br>"))
	data := santaLinkEmailData{
		emailCommon: emailCommon{
			Lang:    lang,
			Title:   eventTitle,
			LogoURL: logoURLFromBase(baseFromURL(editURL)),
		},
		Greeting:         fmt.Sprintf(T("santa_email_greeting", lang), p.FirstName),
		Hook:             T("santa_email_link_hook", lang),
		HowItWorksTitle:  T("santa_email_how_title", lang),
		Step1:            T("santa_email_how_step1", lang),
		Step2:            T("santa_email_how_step2", lang),
		Step3:            T("santa_email_how_step3", lang),
		ButtonText:       T("santa_email_link_button", lang),
		EditURL:          editURL,
		EventDescription: desc,
		Disclaimer:       T("santa_disclaimer", lang),
	}
	return T("santa_email_link_subject", lang) + " " + eventTitle, renderEmailTemplate("email_santa_link.html", data)
}

// renderSantaRevealEmail builds the draw-reveal email in the given language.
func renderSantaRevealEmail(lang string, giver, receiver SantaParticipant, event Event, baseURL string) (subject, html string) {
	eventTitle := Localized(event.TitleFR, event.TitleEN, lang)
	data := santaRevealEmailData{
		emailCommon: emailCommon{
			Lang:    lang,
			Title:   eventTitle,
			LogoURL: logoURLFromBase(baseURL),
		},
		Greeting:      fmt.Sprintf(T("santa_email_greeting", lang), giver.FirstName),
		Intro:         T("santa_email_reveal_intro", lang),
		ReceiverName:  receiver.FirstName + " " + receiver.LastName,
		WishesIntro:   T("santa_email_reveal_wishes", lang),
		WishBuyLabel:  emailLabel(T("santa_wish_buy", lang), lang),
		WishMakeLabel: emailLabel(T("santa_wish_make", lang), lang),
		WishFreeLabel: emailLabel(T("santa_wish_free", lang), lang),
		WishBuy:       receiver.WishBuy,
		WishMake:      receiver.WishMake,
		WishFree:      receiver.WishFree,
	}
	return T("santa_email_reveal_subject", lang) + " " + eventTitle, renderEmailTemplate("email_santa_reveal.html", data)
}

// renderEmailTemplate executes an embedded email template wrapped in the
// shared email_layout (logo + title bar + body shell). The template name must
// equal the file's base name as registered by ParseFS. Returns "" on failure
// (logged); callers that send email MUST treat an empty result as a hard
// error and abort the send rather than deliver a blank body.
func renderEmailTemplate(name string, data any) string {
	tmpl, err := template.ParseFS(templatesFS, "templates/email_layout.html", "templates/"+name)
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
	from      string // RFC 5322 From; may carry a display name ("Name <addr>")
	configSet string // SES configuration set; enables delivery-event publishing
}

func NewSESSender(ctx context.Context, from, fromName, configSet string) (*SESSender, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESSender{client: sesv2.NewFromConfig(cfg), from: formatFrom(from, fromName), configSet: configSet}, nil
}

// formatFrom builds the From header. With a display name it returns
// "Name <addr>" (mail.Address handles RFC 2047 encoding for non-ASCII names);
// without one it returns the bare address.
func formatFrom(addr, name string) string {
	if name == "" {
		return addr
	}
	return (&mail.Address{Name: name, Address: addr}).String()
}

func (s *SESSender) Send(ctx context.Context, to, subject, htmlBody string, attachments ...emailAttachment) (string, error) {
	in := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination:      &sesv2types.Destination{ToAddresses: []string{to}},
	}
	if len(attachments) > 0 {
		// Simple content can't carry attachments — assemble a raw MIME message.
		raw, err := buildRawMIME(s.from, to, subject, htmlBody, attachments)
		if err != nil {
			return "", fmt.Errorf("build raw mime: %w", err)
		}
		in.Content = &sesv2types.EmailContent{Raw: &sesv2types.RawMessage{Data: raw}}
	} else {
		in.Content = &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{Data: aws.String(subject), Charset: aws.String("UTF-8")},
				Body: &sesv2types.Body{
					Html: &sesv2types.Content{Data: aws.String(htmlBody), Charset: aws.String("UTF-8")},
				},
			},
		}
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

// buildRawMIME assembles a multipart/mixed RFC 5322 message — the HTML body
// plus each attachment — for SES's Raw content type. The Subject is RFC
// 2047-encoded so non-ASCII (French) survives; the HTML body is quoted-printable
// and attachments are base64 with the line wrapping RFC 2045 requires.
func buildRawMIME(from, to, subject, htmlBody string, attachments []emailAttachment) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=" + w.Boundary(),
	}
	buf.WriteString(strings.Join(headers, "\r\n") + "\r\n\r\n")

	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := w.CreatePart(htmlHeader)
	if err != nil {
		return nil, fmt.Errorf("create html part: %w", err)
	}
	qp := quotedprintable.NewWriter(htmlPart)
	if _, err := qp.Write([]byte(htmlBody)); err != nil {
		return nil, fmt.Errorf("write html body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("close html body: %w", err)
	}

	for _, a := range attachments {
		ah := textproto.MIMEHeader{}
		ah.Set("Content-Type", a.ContentType)
		ah.Set("Content-Transfer-Encoding", "base64")
		ah.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.Filename))
		part, err := w.CreatePart(ah)
		if err != nil {
			return nil, fmt.Errorf("create attachment part %q: %w", a.Filename, err)
		}
		if err := writeBase64Wrapped(part, a.Data); err != nil {
			return nil, fmt.Errorf("encode attachment %q: %w", a.Filename, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close mime writer: %w", err)
	}
	return buf.Bytes(), nil
}

// writeBase64Wrapped writes data as base64 with a CRLF every 76 characters, the
// line length RFC 2045 mandates for the base64 transfer encoding.
func writeBase64Wrapped(w io.Writer, data []byte) error {
	const lineLen = 76
	encoded := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(encoded); i += lineLen {
		end := i + lineLen
		if end > len(encoded) {
			end = len(encoded)
		}
		if _, err := io.WriteString(w, encoded[i:end]+"\r\n"); err != nil {
			return err
		}
	}
	return nil
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
		messageID, err := app.sendWithRetry(p.Email, subject, htmlBody, invitationAttachments()...)
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
func (app *App) sendWithRetry(to, subject, htmlBody string, attachments ...emailAttachment) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			// EmailSendDelay doubles as the retry backoff — adequate at this
			// app's scale; a dedicated retry-delay field would be over-engineering.
			time.Sleep(app.EmailSendDelay)
		}
		messageID, err := app.Email.Send(context.Background(), to, subject, htmlBody, attachments...)
		if err == nil {
			return messageID, nil
		}
		lastErr = err
	}
	return "", lastErr
}
