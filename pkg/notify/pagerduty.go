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

// PagerDutyNotifier sends alert notifications via PagerDuty Events API v2.
type PagerDutyNotifier struct {
	routingKey string
	client     *http.Client
	apiURL     string
}

// NewPagerDutyNotifier creates a new PagerDuty notifier.
func NewPagerDutyNotifier(routingKey string) *PagerDutyNotifier {
	return &PagerDutyNotifier{
		routingKey: routingKey,
		client:     &http.Client{Timeout: 10 * time.Second},
		apiURL:     "https://events.pagerduty.com/v2/enqueue",
	}
}

// PagerDutyEvent represents a PagerDuty Events API v2 payload.
type PagerDutyEvent struct {
	RoutingKey  string           `json:"routing_key"`
	EventAction string           `json:"event_action"` // trigger, acknowledge, resolve
	DedupKey    string           `json:"dedup_key,omitempty"`
	Payload     PagerDutyPayload `json:"payload"`
}

// PagerDutyPayload contains the event details.
type PagerDutyPayload struct {
	Summary       string                 `json:"summary"`
	Source        string                 `json:"source"`
	Severity      string                 `json:"severity"` // critical, error, warning, info
	Timestamp     string                 `json:"timestamp,omitempty"`
	Component     string                 `json:"component,omitempty"`
	Group         string                 `json:"group,omitempty"`
	CustomDetails map[string]interface{} `json:"custom_details,omitempty"`
}

// Trigger sends a trigger event to PagerDuty.
func (p *PagerDutyNotifier) Trigger(ctx context.Context, dedupKey, summary, source, severity string, details map[string]interface{}) error {
	event := PagerDutyEvent{
		RoutingKey:  p.routingKey,
		EventAction: "trigger",
		DedupKey:    dedupKey,
		Payload: PagerDutyPayload{
			Summary:       summary,
			Source:        source,
			Severity:      severity,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			CustomDetails: details,
		},
	}
	return p.send(ctx, event)
}

// Resolve sends a resolve event to PagerDuty.
func (p *PagerDutyNotifier) Resolve(ctx context.Context, dedupKey string) error {
	event := PagerDutyEvent{
		RoutingKey:  p.routingKey,
		EventAction: "resolve",
		DedupKey:    dedupKey,
		Payload: PagerDutyPayload{
			Summary:  "Alert resolved",
			Source:   "gravix",
			Severity: "info",
		},
	}
	return p.send(ctx, event)
}

func (p *PagerDutyNotifier) send(ctx context.Context, event PagerDutyEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("pagerduty: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pagerduty: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("pagerduty: send: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(io.LimitReader(resp.Body, 1024)) // drain

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pagerduty: status %d", resp.StatusCode)
	}
	return nil
}
