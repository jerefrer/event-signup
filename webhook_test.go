package main

import "testing"

func TestSNSCanonicalStringNotification(t *testing.T) {
	env := snsEnvelope{
		Type:      "Notification",
		MessageId: "m-1",
		TopicArn:  "arn:topic",
		Message:   "hello",
		Timestamp: "2026-05-19T00:00:00.000Z",
	}
	want := "Message\nhello\nMessageId\nm-1\nTimestamp\n2026-05-19T00:00:00.000Z\nTopicArn\narn:topic\nType\nNotification\n"
	if got := snsCanonicalString(env); got != want {
		t.Errorf("canonical string =\n%q\nwant\n%q", got, want)
	}
	env.Subject = "Hi"
	wantWithSubject := "Message\nhello\nMessageId\nm-1\nSubject\nHi\nTimestamp\n2026-05-19T00:00:00.000Z\nTopicArn\narn:topic\nType\nNotification\n"
	if got := snsCanonicalString(env); got != wantWithSubject {
		t.Errorf("canonical string with Subject =\n%q\nwant\n%q", got, wantWithSubject)
	}
}

func TestSNSCanonicalStringSubscriptionConfirmation(t *testing.T) {
	env := snsEnvelope{
		Type:         "SubscriptionConfirmation",
		MessageId:    "m-2",
		TopicArn:     "arn:topic",
		Message:      "confirm me",
		SubscribeURL: "https://sns.example/confirm",
		Timestamp:    "2026-05-19T00:00:00.000Z",
		Token:        "tok",
	}
	want := "Message\nconfirm me\nMessageId\nm-2\nSubscribeURL\nhttps://sns.example/confirm\n" +
		"Timestamp\n2026-05-19T00:00:00.000Z\nToken\ntok\nTopicArn\narn:topic\nType\nSubscriptionConfirmation\n"
	if got := snsCanonicalString(env); got != want {
		t.Errorf("canonical string =\n%q\nwant\n%q", got, want)
	}
}

func TestVerifySNSMessageRejectsBadCertURL(t *testing.T) {
	if err := verifySNSMessage(snsEnvelope{Type: "Notification", SigningCertURL: "https://evil.example.com/cert.pem"}); err == nil {
		t.Error("expected rejection of a non-amazonaws SigningCertURL")
	}
	if err := verifySNSMessage(snsEnvelope{Type: "Notification", SigningCertURL: "http://sns.eu-west-1.amazonaws.com/cert.pem"}); err == nil {
		t.Error("expected rejection of a non-https SigningCertURL")
	}
}
