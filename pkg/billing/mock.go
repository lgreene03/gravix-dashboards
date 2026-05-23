package billing

import (
	"context"
	"time"
)

// MockService implements billing.Service with configurable return values.
// Use this in gateway tests to exercise billing code paths without Stripe API.
type MockService struct {
	// Configurable return values.
	CustomerID     string
	CustomerErr    error
	SubscriptionID string
	SubscriptionErr error
	UsageErr       error
	PortalURL      string
	PortalErr      error
	WebhookEvent   *WebhookEvent
	WebhookErr     error
	PlanName       string
	FreePriceIDVal string

	// Call tracking.
	CreateCustomerCalls     int
	CreateSubscriptionCalls int
	ReportUsageCalls        int
	CreatePortalCalls       int
	ParseWebhookCalls       int
}

func (m *MockService) CreateCustomer(_ context.Context, name, email, tenantID string) (string, error) {
	m.CreateCustomerCalls++
	return m.CustomerID, m.CustomerErr
}

func (m *MockService) CreateSubscription(_ context.Context, customerID, priceID string) (string, error) {
	m.CreateSubscriptionCalls++
	return m.SubscriptionID, m.SubscriptionErr
}

func (m *MockService) ReportUsage(_ context.Context, subscriptionID string, quantity int64, ts time.Time) error {
	m.ReportUsageCalls++
	return m.UsageErr
}

func (m *MockService) CreatePortalSession(_ context.Context, customerID, returnURL string) (string, error) {
	m.CreatePortalCalls++
	return m.PortalURL, m.PortalErr
}

func (m *MockService) ParseWebhook(payload []byte, sigHeader string) (*WebhookEvent, error) {
	m.ParseWebhookCalls++
	return m.WebhookEvent, m.WebhookErr
}

func (m *MockService) PlanForPriceID(priceID string) string {
	if m.PlanName != "" {
		return m.PlanName
	}
	return "unknown"
}

func (m *MockService) FreePriceID() string {
	return m.FreePriceIDVal
}

func (m *MockService) ListInvoices(_ context.Context, customerID string) ([]Invoice, error) {
	return []Invoice{}, nil
}
