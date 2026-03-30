package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpsGenieNotifier sends alert notifications via OpsGenie Alert API v2.
type OpsGenieNotifier struct {
	apiKey string
	client *http.Client
	apiURL string
}

// NewOpsGenieNotifier creates a new OpsGenie notifier.
func NewOpsGenieNotifier(apiKey string) *OpsGenieNotifier {
	return &OpsGenieNotifier{
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
		apiURL: "https://api.opsgenie.com/v2/alerts",
	}
}

// OpsGenieAlert represents an OpsGenie alert payload.
type OpsGenieAlert struct {
	Message     string            `json:"message"`
	Alias       string            `json:"alias,omitempty"` // dedup key
	Description string            `json:"description,omitempty"`
	Priority    string            `json:"priority,omitempty"` // P1-P5
	Source      string            `json:"source,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

// Create sends a new alert to OpsGenie.
func (o *OpsGenieNotifier) Create(ctx context.Context, alias, message, description, priority string, details map[string]string) error {
	alert := OpsGenieAlert{
		Message:     message,
		Alias:       alias,
		Description: description,
		Priority:    priority,
		Source:      "Gravix",
		Tags:        []string{"gravix", "alert"},
		Details:     details,
	}

	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("opsgenie: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("opsgenie: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("opsgenie: send: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("opsgenie: status %d", resp.StatusCode)
	}
	return nil
}

// Close resolves an existing alert by alias.
func (o *OpsGenieNotifier) Close(ctx context.Context, alias string) error {
	url := fmt.Sprintf("%s/%s/close?identifierType=alias", o.apiURL, alias)

	body, _ := json.Marshal(map[string]string{
		"source": "Gravix",
		"note":   "Alert resolved by Gravix",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("opsgenie: create close request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("opsgenie: close: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("opsgenie: close status %d", resp.StatusCode)
	}
	return nil
}
