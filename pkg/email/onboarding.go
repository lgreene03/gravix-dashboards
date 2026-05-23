package email

import (
	"context"
	"log/slog"
)

// OnboardingService sends onboarding-related emails using the Sender interface.
type OnboardingService struct {
	sender  Sender
	baseURL string
}

// NewOnboardingService creates a new OnboardingService.
func NewOnboardingService(sender Sender, baseURL string) *OnboardingService {
	return &OnboardingService{sender: sender, baseURL: baseURL}
}

// SendWelcome sends the welcome email after registration.
func (s *OnboardingService) SendWelcome(ctx context.Context, toEmail, userName, tenantName, plan string) {
	data := OnboardingData{
		UserName:   userName,
		TenantName: tenantName,
		Plan:       plan,
		BaseURL:    s.baseURL,
	}
	htmlBody, textBody, err := RenderWelcome(data)
	if err != nil {
		slog.Error("failed to render welcome email", "error", err, "email", toEmail)
		return
	}
	if err := s.sender.Send(ctx, toEmail, "Welcome to Gravix! Here's how to get started", htmlBody, textBody); err != nil {
		slog.Error("failed to send welcome email", "error", err, "email", toEmail)
	}
}

// SendFirstData sends the "first event received" email.
func (s *OnboardingService) SendFirstData(ctx context.Context, toEmail, userName, tenantName string) {
	data := OnboardingData{
		UserName:   userName,
		TenantName: tenantName,
		BaseURL:    s.baseURL,
	}
	htmlBody, textBody, err := RenderFirstData(data)
	if err != nil {
		slog.Error("failed to render first data email", "error", err, "email", toEmail)
		return
	}
	if err := s.sender.Send(ctx, toEmail, "Your first event is in! Here's what to do next", htmlBody, textBody); err != nil {
		slog.Error("failed to send first data email", "error", err, "email", toEmail)
	}
}

// SendTryAlerting sends the "try alerting" nudge email.
func (s *OnboardingService) SendTryAlerting(ctx context.Context, toEmail, userName, tenantName string) {
	data := OnboardingData{
		UserName:   userName,
		TenantName: tenantName,
		BaseURL:    s.baseURL,
	}
	htmlBody, textBody, err := RenderTryAlerting(data)
	if err != nil {
		slog.Error("failed to render try alerting email", "error", err, "email", toEmail)
		return
	}
	if err := s.sender.Send(ctx, toEmail, "Set up your first alert rule", htmlBody, textBody); err != nil {
		slog.Error("failed to send try alerting email", "error", err, "email", toEmail)
	}
}

// SendUpgradeNudge sends the "upgrade from free plan" nudge email.
func (s *OnboardingService) SendUpgradeNudge(ctx context.Context, toEmail, userName, tenantName string) {
	data := OnboardingData{
		UserName:   userName,
		TenantName: tenantName,
		BaseURL:    s.baseURL,
	}
	htmlBody, textBody, err := RenderUpgradeNudge(data)
	if err != nil {
		slog.Error("failed to render upgrade nudge email", "error", err, "email", toEmail)
		return
	}
	if err := s.sender.Send(ctx, toEmail, "You're on the free plan — here's what you're missing", htmlBody, textBody); err != nil {
		slog.Error("failed to send upgrade nudge email", "error", err, "email", toEmail)
	}
}
