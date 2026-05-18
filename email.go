package main

import (
	"context"
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
