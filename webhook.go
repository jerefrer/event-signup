package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
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
// SigningCertURL must be an https URL on an amazonaws.com host.
func verifySNSMessage(env snsEnvelope) error {
	u, err := url.Parse(env.SigningCertURL)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".amazonaws.com") {
		return fmt.Errorf("invalid SigningCertURL %q", env.SigningCertURL)
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
