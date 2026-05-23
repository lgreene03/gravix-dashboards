// Package billing provides Stripe integration for Gravix multi-tenant billing.
//
// The Service interface abstracts billing operations for testability.
// StripeService implements it using the Stripe Go SDK.
//
// Required environment variables (when Stripe is enabled):
//
//	STRIPE_SECRET_KEY      — Stripe API secret key
//	STRIPE_WEBHOOK_SECRET  — Stripe webhook signing secret
//	STRIPE_PRICE_FREE       — Price ID for Free plan ($0/mo, 500K events)
//	STRIPE_PRICE_TEAM       — Price ID for Team plan ($29/mo, 10M events)
//	STRIPE_PRICE_BUSINESS   — Price ID for Business plan ($99/mo, 50M events)
//	STRIPE_PRICE_SCALE      — Price ID for Scale plan ($299/mo, 200M events)
//	STRIPE_PRICE_ENTERPRISE — Price ID for Enterprise plan (custom pricing)
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
	PriceID        string
	AnnualPriceID  string // annual billing price ID (empty if N/A)
	OveragePriceID string // metered overage price ID (empty if N/A)
	PlanName       string // free, team, business, scale, enterprise
	EventLimit     int64  // monthly event limit (0 = unlimited)
	SeatLimit      int    // max users (0 = unlimited)
	ServiceLimit   int    // max monitored services (0 = unlimited)
	RetentionDays  int    // data retention in days
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

// AnnualPriceIDs holds annual billing Stripe price IDs keyed by plan name.
type AnnualPriceIDs struct {
	Team       string
	Business   string
	Scale      string
	Enterprise string
}

// DefaultPlans returns the standard 5-tier plan configuration.
// Price IDs must be set from environment variables.
func DefaultPlans(freePriceID, teamPriceID, businessPriceID, scalePriceID, enterprisePriceID string) []PlanConfig {
	return DefaultPlansWithAnnual(freePriceID, teamPriceID, businessPriceID, scalePriceID, enterprisePriceID, AnnualPriceIDs{})
}

// DefaultPlansWithAnnual returns the standard 5-tier plan configuration with optional annual price IDs.
func DefaultPlansWithAnnual(freePriceID, teamPriceID, businessPriceID, scalePriceID, enterprisePriceID string, annual AnnualPriceIDs) []PlanConfig {
	return []PlanConfig{
		{PriceID: freePriceID, PlanName: "free", EventLimit: 500_000, SeatLimit: 1, ServiceLimit: 2, RetentionDays: 7},
		{PriceID: teamPriceID, AnnualPriceID: annual.Team, PlanName: "team", EventLimit: 10_000_000, SeatLimit: 5, ServiceLimit: 5, RetentionDays: 30},
		{PriceID: businessPriceID, AnnualPriceID: annual.Business, PlanName: "business", EventLimit: 50_000_000, SeatLimit: 20, ServiceLimit: 20, RetentionDays: 90},
		{PriceID: scalePriceID, AnnualPriceID: annual.Scale, PlanName: "scale", EventLimit: 200_000_000, SeatLimit: 0, ServiceLimit: 50, RetentionDays: 365},
		{PriceID: enterprisePriceID, AnnualPriceID: annual.Enterprise, PlanName: "enterprise", EventLimit: 0, SeatLimit: 0, ServiceLimit: 0, RetentionDays: 365},
	}
}

// DefaultPlansLegacy returns plans using the legacy 3-tier names for backward compatibility.
// Deprecated: Use DefaultPlans with 5-tier configuration.
func DefaultPlansLegacy(freePriceID, starterPriceID, proPriceID string) []PlanConfig {
	return []PlanConfig{
		{PriceID: freePriceID, PlanName: "free", EventLimit: 500_000, SeatLimit: 1, ServiceLimit: 2, RetentionDays: 7},
		{PriceID: starterPriceID, PlanName: "team", EventLimit: 10_000_000, SeatLimit: 5, ServiceLimit: 5, RetentionDays: 30},
		{PriceID: proPriceID, PlanName: "business", EventLimit: 50_000_000, SeatLimit: 20, ServiceLimit: 20, RetentionDays: 90},
	}
}

// PlanSeatLimit returns the seat limit for a plan name.
func PlanSeatLimit(plan string) int {
	switch plan {
	case "free":
		return 1
	case "team":
		return 5
	case "business":
		return 20
	case "scale", "enterprise":
		return 0 // unlimited
	default:
		// Legacy plan names
		switch plan {
		case "starter":
			return 5
		case "pro":
			return 20
		}
		return 2 // safe default
	}
}

// PlanEventLimit returns the monthly event limit for a plan name.
func PlanEventLimit(plan string) int64 {
	switch plan {
	case "free":
		return 500_000
	case "team", "starter":
		return 10_000_000
	case "business", "pro":
		return 50_000_000
	case "scale":
		return 200_000_000
	case "enterprise":
		return 0 // unlimited
	default:
		return 500_000
	}
}

// PlanRetentionDays returns the data retention period for a plan name.
func PlanRetentionDays(plan string) int {
	switch plan {
	case "free":
		return 7
	case "team", "starter":
		return 30
	case "business", "pro":
		return 90
	case "scale", "enterprise":
		return 365
	default:
		return 30
	}
}
