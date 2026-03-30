// Package email provides transactional email sending for Gravix.
//
// It defines a Sender interface with SMTP and no-op implementations.
// Templates are embedded and rendered with html/template.
package email

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/smtp"
	"os"
	"strings"
)

// Sender sends transactional emails.
type Sender interface {
	Send(ctx context.Context, to, subject, htmlBody, textBody string) error
}

// NewSenderFromEnv creates a Sender based on environment variables.
//
// EMAIL_PROVIDER: "smtp" (default), "noop" (logs only, no sends)
// SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS, EMAIL_FROM_ADDRESS, EMAIL_FROM_NAME
func NewSenderFromEnv() Sender {
	provider := os.Getenv("EMAIL_PROVIDER")
	switch provider {
	case "smtp":
		return &SMTPSender{
			Host:        os.Getenv("SMTP_HOST"),
			Port:        os.Getenv("SMTP_PORT"),
			User:        os.Getenv("SMTP_USER"),
			Pass:        os.Getenv("SMTP_PASS"),
			FromAddress: os.Getenv("EMAIL_FROM_ADDRESS"),
			FromName:    os.Getenv("EMAIL_FROM_NAME"),
		}
	case "noop", "":
		return &NoopSender{}
	default:
		slog.Warn("unknown EMAIL_PROVIDER, using noop", "provider", provider)
		return &NoopSender{}
	}
}

// SMTPSender sends emails via SMTP.
type SMTPSender struct {
	Host        string
	Port        string
	User        string
	Pass        string
	FromAddress string
	FromName    string
}

// Send sends an email via SMTP.
func (s *SMTPSender) Send(_ context.Context, to, subject, htmlBody, textBody string) error {
	if s.Host == "" || s.Port == "" {
		return fmt.Errorf("SMTP not configured: host=%q port=%q", s.Host, s.Port)
	}

	from := s.FromAddress
	if s.FromName != "" {
		from = fmt.Sprintf("%s <%s>", s.FromName, s.FromAddress)
	}

	// Build MIME message
	boundary := "gravix-boundary-2024"
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary))

	// Text part
	if textBody != "" {
		msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString(textBody)
		msg.WriteString("\r\n")
	}

	// HTML part
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(htmlBody)
	msg.WriteString("\r\n")
	msg.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
	}

	addr := fmt.Sprintf("%s:%s", s.Host, s.Port)
	return smtp.SendMail(addr, auth, s.FromAddress, []string{to}, msg.Bytes())
}

// NoopSender logs emails without sending. Used in development.
type NoopSender struct {
	// Sent records emails for testing.
	Sent []SentEmail
}

// SentEmail records a sent email for testing.
type SentEmail struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Send logs the email and records it.
func (s *NoopSender) Send(_ context.Context, to, subject, htmlBody, textBody string) error {
	slog.Info("email sent (noop)", "to", to, "subject", subject)
	s.Sent = append(s.Sent, SentEmail{To: to, Subject: subject, HTML: htmlBody, Text: textBody})
	return nil
}

// TemplateData holds variables passed to email templates.
type TemplateData struct {
	UserName string
	Token    string
	BaseURL  string
	Email    string
}

// passwordResetTmpl is the HTML template for password reset emails.
var passwordResetTmpl = template.Must(template.New("password_reset").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #333;">Reset Your Password</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>We received a request to reset your Gravix password. Click the link below to set a new password:</p>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/reset-password.html?token={{.Token}}"
       style="background: #4f46e5; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Reset Password
    </a>
  </p>
  <p style="color: #666; font-size: 14px;">This link expires in 1 hour. If you didn't request this, you can safely ignore this email.</p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// emailVerificationTmpl is the HTML template for email verification.
var emailVerificationTmpl = template.Must(template.New("email_verify").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #333;">Verify Your Email</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>Welcome to Gravix! Please verify your email address to get started:</p>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/verify-email.html?token={{.Token}}"
       style="background: #4f46e5; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Verify Email
    </a>
  </p>
  <p style="color: #666; font-size: 14px;">This link expires in 24 hours.</p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// RenderPasswordReset renders the password reset email template.
func RenderPasswordReset(data TemplateData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := passwordResetTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render password reset template: %w", err)
	}
	textBody := fmt.Sprintf(
		"Reset your Gravix password: %s/reset-password.html?token=%s\n\nThis link expires in 1 hour.",
		data.BaseURL, data.Token,
	)
	return buf.String(), textBody, nil
}

// RenderEmailVerification renders the email verification template.
func RenderEmailVerification(data TemplateData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := emailVerificationTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render email verification template: %w", err)
	}
	textBody := fmt.Sprintf(
		"Verify your Gravix email: %s/verify-email.html?token=%s\n\nThis link expires in 24 hours.",
		data.BaseURL, data.Token,
	)
	return buf.String(), textBody, nil
}

// BaseURLFromEnv returns the BASE_URL env var or a default.
func BaseURLFromEnv() string {
	base := os.Getenv("BASE_URL")
	if base == "" {
		return "http://localhost:8000"
	}
	return strings.TrimRight(base, "/")
}
