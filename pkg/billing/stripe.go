package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stripe/stripe-go/v81"
	portalsession "github.com/stripe/stripe-go/v81/billingportal/session"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/subscription"
	"github.com/stripe/stripe-go/v81/usagerecord"
	"github.com/stripe/stripe-go/v81/webhook"
)

// StripeService implements Service using the Stripe Go SDK.
type StripeService struct {
	webhookSecret string
	plans         map[string]PlanConfig // priceID → plan config
	freePriceID   string
}

// NewStripeService creates a StripeService. The secretKey configures the
// Stripe SDK globally. Plans map Stripe price IDs to Gravix plan names.
func NewStripeService(secretKey, webhookSecret string, plans []PlanConfig) *StripeService {
	stripe.Key = secretKey

	planMap := make(map[string]PlanConfig)
	var freeID string
	for _, p := range plans {
		planMap[p.PriceID] = p
		if p.PlanName == "free" {
			freeID = p.PriceID
		}
	}

	return &StripeService{
		webhookSecret: webhookSecret,
		plans:         planMap,
		freePriceID:   freeID,
	}
}

func (s *StripeService) CreateCustomer(ctx context.Context, name, email, tenantID string) (string, error) {
	params := &stripe.CustomerParams{
		Name:  stripe.String(name),
		Email: stripe.String(email),
	}
	params.AddMetadata("gravix_tenant_id", tenantID)

	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("create stripe customer: %w", err)
	}
	return c.ID, nil
}

func (s *StripeService) CreateSubscription(ctx context.Context, customerID, priceID string) (string, error) {
	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{Price: stripe.String(priceID)},
		},
	}

	sub, err := subscription.New(params)
	if err != nil {
		return "", fmt.Errorf("create stripe subscription: %w", err)
	}
	return sub.ID, nil
}

func (s *StripeService) ReportUsage(ctx context.Context, subscriptionID string, quantity int64, ts time.Time) error {
	// Get the subscription to find the subscription item ID
	sub, err := subscription.Get(subscriptionID, nil)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}
	if len(sub.Items.Data) == 0 {
		return fmt.Errorf("subscription %s has no items", subscriptionID)
	}

	itemID := sub.Items.Data[0].ID
	params := &stripe.UsageRecordParams{
		SubscriptionItem: stripe.String(itemID),
		Quantity:         stripe.Int64(quantity),
		Timestamp:        stripe.Int64(ts.Unix()),
		Action:           stripe.String(UsageRecordActionSet),
	}

	_, err = usagerecord.New(params)
	if err != nil {
		return fmt.Errorf("report usage: %w", err)
	}
	return nil
}

// UsageRecordActionSet overwrites usage at timestamp (vs increment).
const UsageRecordActionSet = "set"

func (s *StripeService) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}

	sess, err := portalsession.New(params)
	if err != nil {
		return "", fmt.Errorf("create portal session: %w", err)
	}
	return sess.URL, nil
}

func (s *StripeService) ParseWebhook(payload []byte, sigHeader string) (*WebhookEvent, error) {
	event, err := webhook.ConstructEvent(payload, sigHeader, s.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("verify webhook signature: %w", err)
	}
	return s.extractEvent(event)
}

// extractEvent maps a raw Stripe event to a billing WebhookEvent.
// Exported via ParseWebhook; separated for testability.
func (s *StripeService) extractEvent(event stripe.Event) (*WebhookEvent, error) {
	we := &WebhookEvent{Type: string(event.Type)}

	switch event.Type {
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		var sub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			return nil, fmt.Errorf("unmarshal subscription: %w", err)
		}
		we.CustomerID = sub.Customer.ID
		we.SubscriptionID = sub.ID
		we.Status = string(sub.Status)
		if len(sub.Items.Data) > 0 {
			we.PriceID = sub.Items.Data[0].Price.ID
			we.PlanName = s.PlanForPriceID(we.PriceID)
		}

	case "invoice.payment_failed":
		var inv stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
			return nil, fmt.Errorf("unmarshal invoice: %w", err)
		}
		we.CustomerID = inv.Customer.ID
		if inv.Subscription != nil {
			we.SubscriptionID = inv.Subscription.ID
		}
		we.Status = "payment_failed"
	}

	return we, nil
}

func (s *StripeService) PlanForPriceID(priceID string) string {
	if p, ok := s.plans[priceID]; ok {
		return p.PlanName
	}
	return "unknown"
}

func (s *StripeService) FreePriceID() string {
	return s.freePriceID
}
