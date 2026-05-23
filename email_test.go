package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestFormatFrom(t *testing.T) {
	tests := []struct {
		name string
		addr string
		from string
		want string
	}{
		{"no name", "no-reply@chanteloube.fr", "", "no-reply@chanteloube.fr"},
		{"ascii name", "no-reply@chanteloube.fr", "Chanteloube", `"Chanteloube" <no-reply@chanteloube.fr>`},
		{"non-ascii name encoded", "no-reply@chanteloube.fr", "Évènements", "=?utf-8?q?=C3=89v=C3=A8nements?= <no-reply@chanteloube.fr>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatFrom(tt.addr, tt.from); got != tt.want {
				t.Errorf("formatFrom(%q, %q) = %q, want %q", tt.addr, tt.from, got, tt.want)
			}
		})
	}
}

// TestBuildRawMIME verifies the raw message is well-formed: the subject survives
// RFC 2047 round-tripping, the HTML body decodes back unchanged, and the
// attachment's filename, type, and bytes are intact after base64 round-tripping.
func TestBuildRawMIME(t *testing.T) {
	htmlBody := "<html><body>Bonjour — café ☕</body></html>"
	pdf := []byte("%PDF-1.4 fake \x00\x01\x02 binary bytes")
	att := []emailAttachment{{
		Filename:    "Le soleil de la confiance.pdf",
		ContentType: "application/pdf",
		Data:        pdf,
	}}
	from := formatFrom("no-reply@chanteloube.fr", "Évènements")

	raw, err := buildRawMIME(from, "alice@example.com", "Rappel — Saga Dawa", htmlBody, att)
	if err != nil {
		t.Fatalf("buildRawMIME: %v", err)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}

	dec := new(mime.WordDecoder)
	gotSubj, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if want := "Rappel — Saga Dawa"; gotSubj != want {
		t.Errorf("subject = %q, want %q", gotSubj, want)
	}

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("media type = %q, want multipart/mixed", mediaType)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])

	// Part 1: HTML body — Part auto-decodes quoted-printable on Read.
	p1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("html part: %v", err)
	}
	if ct := p1.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("html part content-type = %q, want text/html prefix", ct)
	}
	body1, err := io.ReadAll(p1)
	if err != nil {
		t.Fatalf("read html part: %v", err)
	}
	if string(body1) != htmlBody {
		t.Errorf("html body = %q, want %q", body1, htmlBody)
	}

	// Part 2: PDF attachment — base64, decoded here.
	p2, err := mr.NextPart()
	if err != nil {
		t.Fatalf("attachment part: %v", err)
	}
	if got := p2.FileName(); got != "Le soleil de la confiance.pdf" {
		t.Errorf("attachment filename = %q", got)
	}
	if ct := p2.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("attachment content-type = %q", ct)
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p2))
	if err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	if !bytes.Equal(decoded, pdf) {
		t.Errorf("attachment data = %q, want %q", decoded, pdf)
	}

	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("expected EOF after 2 parts, got %v", err)
	}
}
