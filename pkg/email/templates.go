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

// firstDataTmpl — sent when a tenant's first event is ingested.
var firstDataTmpl = template.Must(template.New("first_data").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #059669;">Your First Event Is In!</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>Great news &mdash; we just received the first event for <strong>{{.TenantName}}</strong>. Your observability pipeline is live.</p>
  <p>Here's what to do next:</p>
  <ol>
    <li>Open your <a href="{{.BaseURL}}/index.html">dashboard</a> to see data flowing in</li>
    <li>Set up <strong>alert rules</strong> so you know when things go wrong</li>
    <li>Invite your team from Settings &gt; Team</li>
  </ol>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/index.html" style="background: #059669; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      View Dashboard
    </a>
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// tryAlertingTmpl — sent 3 days after signup if no alert rules created.
var tryAlertingTmpl = template.Must(template.New("try_alerting").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #7c3aed;">Set Up Your First Alert Rule</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>You've been using Gravix for a few days now. Have you set up alerting yet?</p>
  <p>Alert rules let you know immediately when something goes wrong:</p>
  <ul>
    <li><strong>Error rate spikes</strong> &mdash; get notified when 5xx errors exceed a threshold</li>
    <li><strong>Latency degrades</strong> &mdash; catch slow endpoints before users complain</li>
    <li><strong>Traffic anomalies</strong> &mdash; spot unusual request patterns</li>
  </ul>
  <p>It takes less than a minute to create your first rule.</p>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/index.html#alerts" style="background: #7c3aed; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Create Alert Rule
    </a>
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
  <p style="color: #999; font-size: 12px;">Gravix &mdash; Simple HTTP Observability</p>
</body>
</html>`))

// upgradeNudgeTmpl — sent 7 days after signup if still on free plan.
var upgradeNudgeTmpl = template.Must(template.New("upgrade_nudge").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
  <h2 style="color: #2563eb;">Ready to Unlock More?</h2>
  <p>Hi{{if .UserName}} {{.UserName}}{{end}},</p>
  <p>You've been on the free plan for a week. Here's what you're missing on a paid plan:</p>
  <table style="width: 100%; border-collapse: collapse; margin: 20px 0;">
    <tr><td style="padding: 8px; border-bottom: 1px solid #eee;">Event volume</td><td style="padding: 8px; border-bottom: 1px solid #eee;">Up to <strong>50M events/month</strong> (vs 100K free)</td></tr>
    <tr><td style="padding: 8px; border-bottom: 1px solid #eee;">Data retention</td><td style="padding: 8px; border-bottom: 1px solid #eee;"><strong>90 days</strong> (vs 7 days free)</td></tr>
    <tr><td style="padding: 8px; border-bottom: 1px solid #eee;">Alert rules</td><td style="padding: 8px; border-bottom: 1px solid #eee;"><strong>Unlimited</strong> (vs 3 free)</td></tr>
    <tr><td style="padding: 8px;">Team members</td><td style="padding: 8px;"><strong>Unlimited</strong> (vs 1 free)</td></tr>
  </table>
  <p style="margin: 30px 0;">
    <a href="{{.BaseURL}}/index.html#settings" style="background: #2563eb; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Compare Plans
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

// RenderFirstData renders the "first event received" email.
func RenderFirstData(data OnboardingData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := firstDataTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render first data template: %w", err)
	}
	textBody := fmt.Sprintf("Your first event is in! Your Gravix account %s is now receiving data. View your dashboard: %s/index.html",
		data.TenantName, data.BaseURL)
	return buf.String(), textBody, nil
}

// RenderTryAlerting renders the "try alerting" nudge email.
func RenderTryAlerting(data OnboardingData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := tryAlertingTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render try alerting template: %w", err)
	}
	textBody := fmt.Sprintf("Set up your first alert rule in Gravix. It takes less than a minute: %s/index.html#alerts",
		data.BaseURL)
	return buf.String(), textBody, nil
}

// RenderUpgradeNudge renders the "upgrade from free plan" nudge email.
func RenderUpgradeNudge(data OnboardingData) (html, text string, err error) {
	var buf bytes.Buffer
	if err := upgradeNudgeTmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render upgrade nudge template: %w", err)
	}
	textBody := fmt.Sprintf("You're on the free plan at Gravix. Upgrade for more events, longer retention, unlimited alerts, and team members: %s/index.html#settings",
		data.BaseURL)
	return buf.String(), textBody, nil
}
