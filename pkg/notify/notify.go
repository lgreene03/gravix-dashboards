// Package notify provides alert notification dispatchers for Gravix.
//
// It supports Slack incoming webhooks and generic HTTP webhooks.
// Channel configuration is stored as a JSON blob in the database
// and parsed at dispatch time.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// AlertPayload contains the information about a triggered alert.
type AlertPayload struct {
	RuleName       string
	Metric         string
	Operator       string
	Threshold      float64
	ActualValue    float64
	WindowMinutes  int
	Service        string
	PathTemplate   string
	FiredAt        time.Time
	// Anomaly-specific fields (only set when Operator == "anomaly")
	Mean           float64
	Stddev         float64
	DeviationSigma float64
}

// ChannelConfig holds the parsed notification channel configuration.
type ChannelConfig struct {
	WebhookURL string `json:"webhook_url"`
	AuthHeader string `json:"auth_header,omitempty"`
}

// ParseChannelConfig parses a JSON config string into a ChannelConfig.
func ParseChannelConfig(configJSON string) (ChannelConfig, error) {
	var c ChannelConfig
	if err := json.Unmarshal([]byte(configJSON), &c); err != nil {
		return c, fmt.Errorf("parse channel config: %w", err)
	}
	if c.WebhookURL == "" {
		return c, fmt.Errorf("webhook_url is required")
	}
	return c, nil
}

// Dispatcher sends alert notifications.
type Dispatcher struct {
	client *http.Client
}

// NewDispatcher creates a new notification dispatcher with sensible defaults.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send dispatches an alert notification to the given channel.
func (d *Dispatcher) Send(ctx context.Context, channelType string, config ChannelConfig, alert AlertPayload) error {
	switch channelType {
	case "slack":
		return d.sendSlack(ctx, config, alert)
	case "webhook":
		return d.sendWebhook(ctx, config, alert)
	default:
		return fmt.Errorf("unsupported channel type: %s", channelType)
	}
}

// SendTest sends a test notification to verify channel configuration.
func (d *Dispatcher) SendTest(ctx context.Context, channelType string, config ChannelConfig) error {
	testPayload := AlertPayload{
		RuleName:      "Test Alert",
		Metric:        "error_rate",
		Operator:      "gt",
		Threshold:     0.05,
		ActualValue:   0.00,
		WindowMinutes: 5,
		FiredAt:       time.Now().UTC(),
	}

	switch channelType {
	case "slack":
		return d.sendSlackTest(ctx, config)
	case "webhook":
		return d.sendWebhook(ctx, config, testPayload)
	default:
		return fmt.Errorf("unsupported channel type: %s", channelType)
	}
}

func (d *Dispatcher) sendSlack(ctx context.Context, config ChannelConfig, alert AlertPayload) error {
	// Build scope text
	scope := "All services"
	if alert.Service != "" {
		scope = alert.Service
		if alert.PathTemplate != "" {
			scope += " " + alert.PathTemplate
		}
	}

	var fields []map[string]string
	var headerEmoji string

	if alert.Operator == "anomaly" {
		headerEmoji = "📊"
		fields = []map[string]string{
			{"type": "mrkdwn", "text": fmt.Sprintf("*Metric:*\n%s", alert.Metric)},
			{"type": "mrkdwn", "text": fmt.Sprintf("*Current Value:*\n%.4f", alert.ActualValue)},
			{"type": "mrkdwn", "text": fmt.Sprintf("*Baseline Mean:*\n%.4f (σ=%.4f)", alert.Mean, alert.Stddev)},
			{"type": "mrkdwn", "text": fmt.Sprintf("*Deviation:*\n%.1fσ (threshold: %.1fσ)", alert.DeviationSigma, alert.Threshold)},
		}
	} else {
		headerEmoji = "🚨"
		operatorText := ">"
		if alert.Operator == "lt" {
			operatorText = "<"
		}
		fields = []map[string]string{
			{"type": "mrkdwn", "text": fmt.Sprintf("*Metric:*\n%s", alert.Metric)},
			{"type": "mrkdwn", "text": fmt.Sprintf("*Current Value:*\n%.4f", alert.ActualValue)},
			{"type": "mrkdwn", "text": fmt.Sprintf("*Threshold:*\n%s %.4f", operatorText, alert.Threshold)},
			{"type": "mrkdwn", "text": fmt.Sprintf("*Window:*\n%d min", alert.WindowMinutes)},
		}
	}

	payload := map[string]interface{}{
		"text": fmt.Sprintf("%s Gravix Alert: %s", headerEmoji, alert.RuleName),
		"blocks": []map[string]interface{}{
			{
				"type": "header",
				"text": map[string]string{
					"type": "plain_text",
					"text": headerEmoji + " Gravix Alert: " + alert.RuleName,
				},
			},
			{
				"type":   "section",
				"fields": fields,
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*Scope:* %s\n*Fired at:* %s", scope, alert.FiredAt.Format("2006-01-02 15:04:05 UTC")),
				},
			},
		},
	}

	return d.postJSON(ctx, config.WebhookURL, "", payload)
}

func (d *Dispatcher) sendSlackTest(ctx context.Context, config ChannelConfig) error {
	payload := map[string]interface{}{
		"text": "✅ Gravix test notification — your Slack channel is configured correctly!",
	}
	return d.postJSON(ctx, config.WebhookURL, "", payload)
}

func (d *Dispatcher) sendWebhook(ctx context.Context, config ChannelConfig, alert AlertPayload) error {
	payload := map[string]interface{}{
		"alert_name":     alert.RuleName,
		"metric":         alert.Metric,
		"operator":       alert.Operator,
		"threshold":      alert.Threshold,
		"actual_value":   alert.ActualValue,
		"window_minutes": alert.WindowMinutes,
		"service":        alert.Service,
		"path_template":  alert.PathTemplate,
		"fired_at":       alert.FiredAt.Format(time.RFC3339),
	}
	return d.postJSON(ctx, config.WebhookURL, config.AuthHeader, payload)
}

func (d *Dispatcher) postJSON(ctx context.Context, url, authHeader string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification endpoint returned %d", resp.StatusCode)
	}
	return nil
}
