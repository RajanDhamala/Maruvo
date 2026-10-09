package utils

import (
	"context"
	"testing"
)

func TestMailConfiguration(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	if m, err := NewMailerFromEnv(); m != nil || err != nil {
		t.Fatalf("disabled: %v %v", m, err)
	}
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_FROM", "invalid")
	if _, err := NewMailerFromEnv(); err == nil {
		t.Fatal("invalid sender accepted")
	}
	t.Setenv("SMTP_FROM", "Maruvo <mail@example.com>")
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_PORT", "")
	m, err := NewMailerFromEnv()
	if err != nil || m.Port != "587" {
		t.Fatalf("default config: %v %v", m, err)
	}
	t.Setenv("SMTP_USERNAME", "user")
	if _, err := NewMailerFromEnv(); err == nil {
		t.Fatal("partial credentials accepted")
	}
}

func TestMailRejectsHeaderInjection(t *testing.T) {
	m := &Mailer{From: "mail@example.com"}
	for _, input := range []struct{ to, subject, id string }{
		{"user@example.com\r\nBcc: other@example.com", "Job accepted", "1"},
		{"user@example.com", "Job accepted\r\nBcc: other@example.com", "1"},
		{"user@example.com", "Job accepted", "1\r\nBcc: other@example.com"},
	} {
		if err := m.Send(context.Background(), input.to, input.subject, "body", input.id); err == nil {
			t.Fatal("header injection accepted")
		}
	}
}
