// Package billing provides Stripe integration for Gravix multi-tenant billing.
//
// The Service interface abstracts billing operations for testability.
// StripeService implements it using the Stripe Go SDK.
//
// Required environment variables (when Stripe is enabled):
//
//	STRIPE_SECRET_KEY      — Stripe API secret key
//	STRIPE_WEBHOOK_SECRET  — Stripe webhook signing secret
//	STRIPE_PRICE_FREE      — Price ID for Free plan ($0/mo, 1M events)
//	STRIPE_PRICE_STARTER   — Price ID for Starter plan ($29/mo, 10M events)
//	STRIPE_PRICE_PRO       — Price ID for Pro plan ($99/mo, 50M events)
package billing

import (
	"context"
	"time"
)

// WebhookEvent represents a parsed Stripe webhook event relevant to billing.
type WebhookEvent struct {
	Type           string // e.g. "customer.subscription.updated"
	CustomerID     string
	SubscriptionID string
	PriceID        string
	PlanName       string // mapped from price ID (free, starter, pro)
	Status         string // subscription status: active, past_due, canceled, unpaid, payment_failed
}

// PlanConfig maps a Stripe price ID to a Gravix plan.
type PlanConfig struct {
	PriceID    string
	PlanName   string // free, starter, pro
	EventLimit int64  // monthly event limit
}

// Service defines billing operations required by Gravix.
type Service interface {
	// CreateCustomer creates a Stripe customer linked to a Gravix tenant.
	CreateCustomer(ctx context.Context, name, email, tenantID string) (customerID string, err error)

	// CreateSubscription starts a subscription for a customer on the given price.
	CreateSubscription(ctx context.Context, customerID, priceID string) (subscriptionID string, err error)

	// ReportUsage reports metered event usage for a subscription.
	ReportUsage(ctx context.Context, subscriptionID string, quantity int64, ts time.Time) error

	// CreatePortalSession returns a Stripe Customer Portal URL for self-service billing.
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (url string, err error)

	// ParseWebhook verifies signature and parses a Stripe webhook payload.
	ParseWebhook(payload []byte, sigHeader string) (*WebhookEvent, error)

	// PlanForPriceID maps a Stripe price ID to a Gravix plan name.
	PlanForPriceID(priceID string) string

	// FreePriceID returns the price ID for the free plan.
	FreePriceID() string

	// ListInvoices returns recent invoices for a Stripe customer.
	ListInvoices(ctx context.Context, customerID string) ([]Invoice, error)
}

// Invoice represents a billing invoice summary.
type Invoice struct {
	ID        string `json:"id"`
	Date      string `json:"date"`       // YYYY-MM-DD
	Amount    int64  `json:"amount"`      // cents
	Currency  string `json:"currency"`
	Status    string `json:"status"`      // paid, open, void, draft
	PDFUrl    string `json:"pdf_url"`
	HostedUrl string `json:"hosted_url"`
}

// DefaultPlans returns the standard plan configuration.
// Price IDs must be set from environment variables.
func DefaultPlans(freePriceID, starterPriceID, proPriceID string) []PlanConfig {
	return []PlanConfig{
		{PriceID: freePriceID, PlanName: "free", EventLimit: 1_000_000},
		{PriceID: starterPriceID, PlanName: "starter", EventLimit: 10_000_000},
		{PriceID: proPriceID, PlanName: "pro", EventLimit: 50_000_000},
	}
}
