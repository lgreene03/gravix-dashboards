package email

import (
	"bytes"
	"fmt"
	"html/template"
)

// OnboardingData holds variables for onboarding email templates.
type OnboardingData struct {
	UserName   string
	TenantName string
	Plan       string
	BaseURL    string
	APIKey     string // masked: grvx_abc1...
}

// BillingAlertData holds variables for billing alert emails.
type BillingAlertData struct {
	UserName    string
	TenantName  string
	Plan        string
	EventCount  int64
	EventLimit  int64
	UsagePercent int
	BaseURL     string
}

// welcomeTmpl — sent immediately after registration.
var welcomeTmpl = template.Must(template.New("welcome").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #2563eb;">Welcome to Gravix!</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>Your account <strong>{{.TenantName}}</strong> is ready. You're on the <strong>{{.Plan}}</strong> plan.</p>
  <p>Here's how to start sending data in under 5 minutes:</p>
  <ol>
    <li>Install an SDK: <code>go get github.com/lgreene/gravix-dashboards/sdk/go</code></li>
    <li>Add middleware to your HTTP server</li>
    <li>Watch data flow into your <a href="{{.BaseURL}}/index.html">dashboard</a></li>
  </ol>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/index.html" style="background: #2563eb; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Open Dashboard
    </a>
  </p>
  <p style="color: #666; font-size: 14px;">Need help? Reply to this email or check our <a href="https://docs.gravix.io">docs</a>.</p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// setupGuideTmpl — sent day 2 after registration.
var setupGuideTmpl = template.Must(template.New("setup_guide").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #333;">Quick Setup Guide</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>Here's a quick guide to get the most out of Gravix:</p>
  <h3>1. Send your first events</h3>
  <pre style="background: #f1f5f9; padding: 16px; border-radius: 6px; overflow-x: auto; font-size: 13px;">curl -X POST {{.BaseURL}}/api/v1/facts \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '[{"event_id":"...","event_time":"...","service":"my-api","method":"GET","path_template":"/api/users/{id}","status_code":200,"latency_ms":42}]'</pre>
  <h3>2. Set up alerting</h3>
  <p>Create alert rules in your <a href="{{.BaseURL}}/index.html">dashboard</a> to get notified when error rates spike or latency degrades.</p>
  <h3>3. Invite your team</h3>
  <p>Add team members from Settings &gt; Team to collaborate on monitoring.</p>
  <p style="margin: 30px 0;">
    <a href="https://docs.gravix.io/getting-started" style="background: #2563eb; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Read Full Guide
    </a>
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// quotaWarningTmpl — sent when usage reaches 80%.
var quotaWarningTmpl = template.Must(template.New("quota_warning").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #f59e0b;">Usage Alert: {{.UsagePercent}}% of Event Quota Used</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>Your organization <strong>{{.TenantName}}</strong> has used <strong>{{.UsagePercent}}%</strong> of your monthly event quota.</p>
  <table style="width: 100%; border-collapse: collapse; margin: 20px 0;">
    <tr><td style="padding: 8px; border-bottom: 1px solid #eee;">Plan</td><td style="padding: 8px; border-bottom: 1px solid #eee;"><strong>{{.Plan}}</strong></td></tr>
    <tr><td style="padding: 8px; border-bottom: 1px solid #eee;">Events used</td><td style="padding: 8px; border-bottom: 1px solid #eee;"><strong>{{.EventCount}}</strong></td></tr>
    <tr><td style="padding: 8px;">Event limit</td><td style="padding: 8px;"><strong>{{.EventLimit}}</strong></td></tr>
  </table>
  <p>To avoid service interruptions, consider upgrading your plan:</p>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/index.html#settings" style="background: #f59e0b; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Upgrade Plan
    </a>
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// RenderWelcome renders the welcome email.
func RenderWelcome(data OnboardingData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := welcomeTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render welcome template: %w", err)
	}
	textBody := fmt.Sprintf("Welcome to Gravix, %s! Your account %s is ready on the %s plan. Open your dashboard: %s/index.html",
		data.UserName, data.TenantName, data.Plan, data.BaseURL)
	return buf.String(), textBody, nil
}

// RenderSetupGuide renders the setup guide email.
func RenderSetupGuide(data OnboardingData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := setupGuideTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render setup guide template: %w", err)
	}
	textBody := fmt.Sprintf("Quick Setup Guide for Gravix. Read the full guide: https://docs.gravix.io/getting-started")
	return buf.String(), textBody, nil
}

// RenderQuotaWarning renders the quota warning email.
func RenderQuotaWarning(data BillingAlertData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := quotaWarningTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render quota warning template: %w", err)
	}
	textBody := fmt.Sprintf("Usage Alert: Your organization %s has used %d%% of your %s plan's monthly event quota (%d/%d events). Upgrade: %s/index.html#settings",
		data.TenantName, data.UsagePercent, data.Plan, data.EventCount, data.EventLimit, data.BaseURL)
	return buf.String(), textBody, nil
}
