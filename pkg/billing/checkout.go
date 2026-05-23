package billing

import (
	"context"
	"fmt"

	"github.com/stripe/stripe-go/v81"
	checkoutsession "github.com/stripe/stripe-go/v81/checkout/session"
)

// CheckoutParams configures a Stripe Checkout Session.
type CheckoutParams struct {
	CustomerID string
	PriceID    string
	SuccessURL string
	CancelURL  string
	TrialDays  int64 // 0 = no trial
	Quantity   int64 // 0 or 1 = single subscription
}

// CreateCheckoutSession creates a Stripe Checkout Session for plan subscription.
func (s *StripeService) CreateCheckoutSession(ctx context.Context, params CheckoutParams) (string, error) {
	qty := params.Quantity
	if qty <= 0 {
		qty = 1
	}

	lineItem := &stripe.CheckoutSessionLineItemParams{
		Price:    stripe.String(params.PriceID),
		Quantity: stripe.Int64(qty),
	}

	sessParams := &stripe.CheckoutSessionParams{
		Customer:   stripe.String(params.CustomerID),
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(params.SuccessURL),
		CancelURL:  stripe.String(params.CancelURL),
		LineItems:  []*stripe.CheckoutSessionLineItemParams{lineItem},
	}

	if params.TrialDays > 0 {
		sessParams.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{
			TrialPeriodDays: stripe.Int64(params.TrialDays),
		}
	}

	sess, err := checkoutsession.New(sessParams)
	if err != nil {
		return "", fmt.Errorf("create checkout session: %w", err)
	}
	return sess.URL, nil
}

// DefaultTrialDays returns the trial period for a plan (0 if none).
func DefaultTrialDays(plan string) int64 {
	switch plan {
	case "team":
		return 14
	case "business":
		return 14
	default:
		return 0
	}
}
