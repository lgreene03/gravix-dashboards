package gravix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Default configuration values.
const (
	DefaultBatchSize     = 100
	DefaultFlushInterval = 5 * time.Second
	DefaultHTTPTimeout   = 10 * time.Second
)

// Option configures the Client.
type Option func(*Client)

// WithService sets the default service name for all facts and events.
// If a fact or event already specifies a Service, it takes precedence.
func WithService(name string) Option {
	return func(c *Client) { c.service = name }
}

// WithBatchSize sets the maximum number of facts buffered before an
// automatic flush. Default is 100.
func WithBatchSize(n int) Option {
	return func(c *Client) { c.batchSize = n }
}

// WithFlushInterval sets the maximum time between automatic flushes.
// Default is 5 seconds.
func WithFlushInterval(d time.Duration) Option {
	return func(c *Client) { c.flushInterval = d }
}

// WithHTTPClient overrides the default HTTP client used for API calls.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithAutoSanitize enables or disables automatic path template sanitization.
// When enabled (default), raw UUIDs and numeric IDs in PathTemplate are
// automatically replaced with {id} placeholders.
func WithAutoSanitize(enabled bool) Option {
	return func(c *Client) { c.autoSanitize = enabled }
}

// WithMaxRetries sets the maximum number of retry attempts for failed requests.
// Default is 3. Set to 0 to disable retries.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = n }
}

// WithOnError sets a callback invoked when a background batch flush fails.
// Useful for logging or metrics in production.
func WithOnError(fn func(error)) Option {
	return func(c *Client) { c.onError = fn }
}

// Client sends request facts and service events to the Gravix ingestion API.
type Client struct {
	baseURL       string
	apiKey        string
	service       string
	httpClient    *http.Client
	batcher       *batcher
	batchSize     int
	flushInterval time.Duration
	autoSanitize  bool
	maxRetries    int
	onError       func(error)
}

// New creates a new Gravix client. The baseURL should point to the ingestion
// service (e.g., "http://localhost:8090"). The apiKey is the X-API-Key value.
func New(baseURL, apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		apiKey:        apiKey,
		batchSize:     DefaultBatchSize,
		flushInterval: DefaultFlushInterval,
		autoSanitize:  true,
		maxRetries:    3,
		httpClient: &http.Client{
			Timeout: DefaultHTTPTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	c.batcher = newBatcher(c.batchSize, c.flushInterval, c.sendBatch)
	if c.onError != nil {
		c.batcher.errFn = c.onError
	}

	return c
}

// RecordFact queues a request fact for batched submission to the ingestion API.
// The fact is automatically enriched:
//   - EventID is generated (UUIDv7) if empty.
//   - EventTime is set to now (UTC) if empty.
//   - Service is set from WithService if the fact's Service is empty.
//   - PathTemplate is sanitized if WithAutoSanitize is enabled.
func (c *Client) RecordFact(ctx context.Context, f RequestFact) error {
	c.enrichFact(&f)
	c.batcher.add(f)
	return nil
}

// RecordEvent sends a service event synchronously to the ingestion API.
// Events are not batched because they are typically low-volume and
// time-sensitive (deploys, restarts, etc.).
func (c *Client) RecordEvent(ctx context.Context, e ServiceEvent) error {
	c.enrichEvent(&e)

	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("gravix: marshal event: %w", err)
	}

	return c.doWithRetry(ctx, "POST", "/api/v1/events", body, nil)
}

// SendFact sends a single request fact synchronously (bypassing the batcher).
// Use RecordFact for normal operation; use SendFact when you need
// confirmation that the fact was accepted.
func (c *Client) SendFact(ctx context.Context, f RequestFact) error {
	c.enrichFact(&f)

	body, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("gravix: marshal fact: %w", err)
	}

	return c.doWithRetry(ctx, "POST", "/api/v1/facts", body, nil)
}

// Flush forces an immediate flush of all buffered facts.
func (c *Client) Flush(ctx context.Context) error {
	c.batcher.flush(ctx)
	return nil
}

// Close flushes any remaining buffered facts and shuts down the client.
// Always call Close before your application exits.
func (c *Client) Close() error {
	c.batcher.close()
	return nil
}

// enrichFact fills in auto-generated fields.
func (c *Client) enrichFact(f *RequestFact) {
	if f.EventID == "" {
		if id, err := uuid.NewV7(); err == nil {
			f.EventID = id.String()
		}
	}
	if f.EventTime == "" {
		f.EventTime = formatTime(time.Now())
	}
	if f.Service == "" && c.service != "" {
		f.Service = c.service
	}
	if c.autoSanitize && f.PathTemplate != "" {
		f.PathTemplate = SanitizePath(f.PathTemplate)
	}
}

// enrichEvent fills in auto-generated fields.
func (c *Client) enrichEvent(e *ServiceEvent) {
	if e.EventID == "" {
		if id, err := uuid.NewV7(); err == nil {
			e.EventID = id.String()
		}
	}
	if e.EventTime == "" {
		e.EventTime = formatTime(time.Now())
	}
	if e.Service == "" && c.service != "" {
		e.Service = c.service
	}
}

// sendBatch submits a JSONL payload to the batch endpoint.
func (c *Client) sendBatch(ctx context.Context, body []byte) (*BatchResult, error) {
	var result BatchResult
	err := c.doWithRetry(ctx, "POST", "/api/v1/facts/batch", body, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// doWithRetry executes an HTTP request with retry logic.
func (c *Client) doWithRetry(ctx context.Context, method, path string, body []byte, result interface{}) error {
	url := c.baseURL + path
	cfg := retryConfig{
		maxAttempts: c.maxRetries,
		baseDelay:   500 * time.Millisecond,
		maxDelay:    10 * time.Second,
	}

	var lastErr error
	for attempt := 0; attempt <= cfg.maxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt-1, nil, cfg)
			if err := sleepWithContext(ctx, delay); err != nil {
				return err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("gravix: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gravix: http error: %w", err)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Success cases.
		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
			if result != nil && len(respBody) > 0 {
				if err := json.Unmarshal(respBody, result); err != nil {
					return fmt.Errorf("gravix: unmarshal response: %w", err)
				}
			}
			return nil
		}

		// Parse error response.
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if len(respBody) > 0 {
			_ = json.Unmarshal(respBody, apiErr)
		}
		if apiErr.Message == "" {
			apiErr.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}

		// Retryable?
		if shouldRetry(resp.StatusCode) && attempt < cfg.maxAttempts {
			lastErr = apiErr
			delay := retryDelay(attempt, resp, cfg)
			if err := sleepWithContext(ctx, delay); err != nil {
				return err
			}
			continue
		}

		return apiErr
	}

	return lastErr
}
