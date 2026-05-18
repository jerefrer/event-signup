package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
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

// SESSender sends email through AWS SES (SESv2 API). Credentials and region are
// read from the standard AWS environment (AWS_REGION, AWS_ACCESS_KEY_ID, ...).
type SESSender struct {
	client *sesv2.Client
	from   string
}

func NewSESSender(ctx context.Context, from string) (*SESSender, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESSender{client: sesv2.NewFromConfig(cfg), from: from}, nil
}

func (s *SESSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
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
	})
	if err != nil {
		return fmt.Errorf("ses send email: %w", err)
	}
	return nil
}
