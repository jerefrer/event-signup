package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// snsEnvelope is the outer JSON message Amazon SNS POSTs to the webhook.
type snsEnvelope struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	SubscribeURL     string `json:"SubscribeURL"`
	Token            string `json:"Token"`
}

// snsCanonicalString builds the exact string SNS signs, per the documented
// field order (different for confirmation messages and notifications).
func snsCanonicalString(env snsEnvelope) string {
	var b strings.Builder
	add := func(k, v string) {
		b.WriteString(k)
		b.WriteByte('\n')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	switch env.Type {
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		add("Message", env.Message)
		add("MessageId", env.MessageId)
		add("SubscribeURL", env.SubscribeURL)
		add("Timestamp", env.Timestamp)
		add("Token", env.Token)
		add("TopicArn", env.TopicArn)
		add("Type", env.Type)
	default: // Notification
		add("Message", env.Message)
		add("MessageId", env.MessageId)
		if env.Subject != "" {
			add("Subject", env.Subject)
		}
		add("Timestamp", env.Timestamp)
		add("TopicArn", env.TopicArn)
		add("Type", env.Type)
	}
	return b.String()
}

var snsCertCache sync.Map // SigningCertURL -> *rsa.PublicKey

var snsCertHTTPClient = &http.Client{Timeout: 5 * time.Second}

var snsSubscribeHTTPClient = &http.Client{Timeout: 10 * time.Second}

func snsSigningKey(certURL string) (*rsa.PublicKey, error) {
	if v, ok := snsCertCache.Load(certURL); ok {
		return v.(*rsa.PublicKey), nil
	}
	resp, err := snsCertHTTPClient.Get(certURL)
	if err != nil {
		return nil, fmt.Errorf("fetch SNS cert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch SNS cert: HTTP %d", resp.StatusCode)
	}
	pemBytes, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read SNS cert: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("SNS cert: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse SNS cert: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("SNS cert: not an RSA public key")
	}
	snsCertCache.Store(certURL, pub)
	return pub, nil
}

// verifySNSMessage validates the cryptographic signature of an SNS message. The
// SigningCertURL must be an https URL on an SNS-controlled amazonaws.com host.
func verifySNSMessage(env snsEnvelope) error {
	u, err := url.Parse(env.SigningCertURL)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("invalid SigningCertURL %q", env.SigningCertURL)
	}
	// The cert URL must be an AWS SNS host. A bare ".amazonaws.com" suffix is
	// NOT sufficient: an attacker can host a forged certificate on their own S3
	// bucket (e.g. bucket.s3.amazonaws.com) and sign messages with its key.
	// Only sns.<region>.amazonaws.com hosts are controlled by AWS SNS.
	host := strings.ToLower(u.Hostname())
	if !strings.HasPrefix(host, "sns.") || !strings.HasSuffix(host, ".amazonaws.com") {
		return fmt.Errorf("invalid SigningCertURL host %q", host)
	}
	pub, err := snsSigningKey(env.SigningCertURL)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("decode SNS signature: %w", err)
	}
	canonical := []byte(snsCanonicalString(env))
	if env.SignatureVersion == "2" {
		h := sha256.Sum256(canonical)
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
	}
	h := sha1.Sum(canonical)
	return rsa.VerifyPKCS1v15(pub, crypto.SHA1, h[:], sig)
}

// sesEvent is the SES event JSON carried inside an SNS Notification's Message.
type sesEvent struct {
	EventType        string `json:"eventType"`
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID string `json:"messageId"`
	} `json:"mail"`
	Bounce *struct {
		BounceType    string `json:"bounceType"`
		BounceSubType string `json:"bounceSubType"`
	} `json:"bounce"`
	Complaint *struct {
		ComplaintFeedbackType string `json:"complaintFeedbackType"`
	} `json:"complaint"`
	Reject *struct {
		Reason string `json:"reason"`
	} `json:"reject"`
}

// handleSESWebhook receives Amazon SNS deliveries of SES events. It is public
// (SNS cannot authenticate) but every message's signature is verified.
func (app *App) handleSESWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !app.SNSSkipVerify {
		if err := verifySNSMessage(env); err != nil {
			log.Printf("SES webhook: signature verification failed: %v", err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	switch env.Type {
	case "SubscriptionConfirmation":
		if env.SubscribeURL != "" {
			resp, err := snsSubscribeHTTPClient.Get(env.SubscribeURL)
			if err != nil {
				log.Printf("SES webhook: subscription confirmation failed: %v", err)
			} else {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				log.Printf("SES webhook: SNS subscription confirmed")
			}
		}
	case "Notification":
		app.handleSESEvent(env.Message)
	}
	w.WriteHeader(http.StatusOK)
}

// handleSESEvent parses one SES event and updates the matching email_messages row.
func (app *App) handleSESEvent(message string) {
	var ev sesEvent
	if err := json.Unmarshal([]byte(message), &ev); err != nil {
		log.Printf("SES webhook: cannot parse event: %v", err)
		return
	}
	eventType := ev.EventType
	if eventType == "" {
		eventType = ev.NotificationType
	}
	var status, detail string
	switch eventType {
	case "Send":
		status = "sent"
	case "Delivery":
		status = "delivered"
	case "Bounce":
		status = "bounced"
		if ev.Bounce != nil {
			detail = ev.Bounce.BounceType + "/" + ev.Bounce.BounceSubType
		}
	case "Complaint":
		status = "complaint"
		if ev.Complaint != nil {
			detail = ev.Complaint.ComplaintFeedbackType
		}
	case "Reject":
		status = "rejected"
		if ev.Reject != nil {
			detail = ev.Reject.Reason
		}
	default:
		return // unknown / uninteresting event type
	}
	if ev.Mail.MessageID == "" {
		return
	}
	if _, err := ApplyEmailEvent(app.DB, ev.Mail.MessageID, status, detail); err != nil {
		log.Printf("SES webhook: apply event %s/%s: %v", ev.Mail.MessageID, status, err)
	}
}
