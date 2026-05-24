package mailer

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"pastebox/internal/config"
)

func TestNewSenderBuildsLogAndSMTPSenders(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	logSender, err := NewSender(config.Config{MailerProvider: "log"}, logger)
	if err != nil {
		t.Fatalf("new log sender: %v", err)
	}
	if _, ok := logSender.(LogSender); !ok {
		t.Fatalf("expected LogSender, got %T", logSender)
	}

	smtpSender, err := NewSender(config.Config{
		MailerProvider: "smtp",
		SMTP: config.SMTPConfig{
			Host:      "smtp.example.com",
			Port:      587,
			FromEmail: "noreply@pastebox.example.com",
			TLSMode:   "starttls",
		},
	}, logger)
	if err != nil {
		t.Fatalf("new smtp sender: %v", err)
	}
	if _, ok := smtpSender.(*SMTPSender); !ok {
		t.Fatalf("expected SMTPSender, got %T", smtpSender)
	}

	if _, err := NewSender(config.Config{MailerProvider: "unknown"}, logger); err == nil {
		t.Fatal("expected unknown provider error")
	}
}

func TestLogSenderHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (LogSender{}).Send(ctx, "user@example.com", "Subject", "Body"); err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestNewSMTPSenderValidatesConfig(t *testing.T) {
	valid := config.SMTPConfig{
		Host:      "smtp.example.com",
		Port:      587,
		FromEmail: "noreply@pastebox.example.com",
		TLSMode:   "starttls",
	}
	if _, err := NewSMTPSender(valid); err != nil {
		t.Fatalf("valid smtp config should pass: %v", err)
	}

	invalid := valid
	invalid.Port = 0
	if _, err := NewSMTPSender(invalid); err == nil {
		t.Fatal("expected invalid port error")
	}

	invalid = valid
	invalid.FromEmail = "not an email"
	if _, err := NewSMTPSender(invalid); err == nil {
		t.Fatal("expected invalid from email error")
	}

	invalid = valid
	invalid.TLSMode = "weird"
	if _, err := NewSMTPSender(invalid); err == nil {
		t.Fatal("expected invalid tls mode error")
	}
}

func TestBuildMessageFormatsHeadersAndBody(t *testing.T) {
	message, from, to, err := buildMessage(config.SMTPConfig{
		FromEmail: "noreply@pastebox.example.com",
		FromName:  "PasteBox",
	}, "User <user@example.com>", "Verify PasteBox", "Line one\nLine two")
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	if from.Address != "noreply@pastebox.example.com" || from.Name != "PasteBox" {
		t.Fatalf("unexpected from address: %#v", from)
	}
	if to.Address != "user@example.com" {
		t.Fatalf("unexpected recipient: %#v", to)
	}
	raw := string(message)
	for _, want := range []string{
		"From: \"PasteBox\" <noreply@pastebox.example.com>\r\n",
		"To: \"User\" <user@example.com>\r\n",
		"Subject: Verify PasteBox\r\n",
		"Content-Type: text/plain; charset=\"utf-8\"\r\n",
		"\r\nLine one\r\nLine two\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("expected message to contain %q, got %q", want, raw)
		}
	}
}

func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	_, _, _, err := buildMessage(config.SMTPConfig{
		FromEmail: "noreply@pastebox.example.com",
	}, "user@example.com", "Verify\r\nBcc: attacker@example.com", "Body")
	if err == nil {
		t.Fatal("expected header injection error")
	}
}
