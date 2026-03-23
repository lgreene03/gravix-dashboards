package email

import (
	"context"
	"strings"
	"testing"
)

func TestNoopSender(t *testing.T) {
	s := &NoopSender{}
	err := s.Send(context.Background(), "test@example.com", "Test Subject", "<h1>Hi</h1>", "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Sent) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(s.Sent))
	}
	if s.Sent[0].To != "test@example.com" {
		t.Errorf("expected to=test@example.com, got %s", s.Sent[0].To)
	}
	if s.Sent[0].Subject != "Test Subject" {
		t.Errorf("expected subject=Test Subject, got %s", s.Sent[0].Subject)
	}
}

func TestRenderPasswordReset(t *testing.T) {
	data := TemplateData{
		UserName: "Alice",
		Token:    "abc123token",
		BaseURL:  "https://app.gravix.io",
	}
	html, text, err := RenderPasswordReset(data)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(html, "abc123token") {
		t.Error("HTML should contain token")
	}
	if !strings.Contains(html, "Alice") {
		t.Error("HTML should contain user name")
	}
	if !strings.Contains(html, "https://app.gravix.io/reset-password.html") {
		t.Error("HTML should contain reset URL")
	}
	if !strings.Contains(text, "abc123token") {
		t.Error("text should contain token")
	}
	if !strings.Contains(text, "1 hour") {
		t.Error("text should mention expiry")
	}
}

func TestRenderEmailVerification(t *testing.T) {
	data := TemplateData{
		UserName: "Bob",
		Token:    "verify456",
		BaseURL:  "https://app.gravix.io",
	}
	html, text, err := RenderEmailVerification(data)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(html, "verify456") {
		t.Error("HTML should contain token")
	}
	if !strings.Contains(html, "Bob") {
		t.Error("HTML should contain user name")
	}
	if !strings.Contains(html, "Verify Email") {
		t.Error("HTML should contain verify button text")
	}
	if !strings.Contains(text, "24 hours") {
		t.Error("text should mention expiry")
	}
}

func TestNewSenderFromEnv_Default(t *testing.T) {
	s := NewSenderFromEnv()
	if _, ok := s.(*NoopSender); !ok {
		t.Error("default sender should be NoopSender")
	}
}

func TestSMTPSender_NotConfigured(t *testing.T) {
	s := &SMTPSender{}
	err := s.Send(context.Background(), "to@x.com", "sub", "<p>hi</p>", "hi")
	if err == nil {
		t.Fatal("expected error for unconfigured SMTP")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}
