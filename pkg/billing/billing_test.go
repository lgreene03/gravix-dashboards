package billing

import (
	"encoding/json"
	"testing"

	"github.com/stripe/stripe-go/v81"
)

func testPlans() []PlanConfig {
	return []PlanConfig{
		{PriceID: "price_free_123", PlanName: "free", EventLimit: 1_000_000},
		{PriceID: "price_starter_456", PlanName: "starter", EventLimit: 10_000_000},
		{PriceID: "price_pro_789", PlanName: "pro", EventLimit: 50_000_000},
	}
}

func TestPlanForPriceID(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	tests := []struct {
		priceID  string
		expected string
	}{
		{"price_free_123", "free"},
		{"price_starter_456", "starter"},
		{"price_pro_789", "pro"},
		{"price_unknown", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		got := svc.PlanForPriceID(tt.priceID)
		if got != tt.expected {
			t.Errorf("PlanForPriceID(%q) = %q, want %q", tt.priceID, got, tt.expected)
		}
	}
}

func TestFreePriceID(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())
	if got := svc.FreePriceID(); got != "price_free_123" {
		t.Errorf("FreePriceID() = %q, want %q", got, "price_free_123")
	}
}

func TestDefaultPlans(t *testing.T) {
	plans := DefaultPlans("pf", "ps", "pp")
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}

	// Verify plan names
	names := map[string]bool{}
	for _, p := range plans {
		names[p.PlanName] = true
	}
	for _, want := range []string{"free", "starter", "pro"} {
		if !names[want] {
			t.Errorf("missing plan %q", want)
		}
	}

	// Verify event limits are ordered
	if plans[0].EventLimit >= plans[1].EventLimit || plans[1].EventLimit >= plans[2].EventLimit {
		t.Error("event limits should increase: free < starter < pro")
	}
}

func TestExtractEvent_SubscriptionCreated(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	// Build a fake Stripe subscription JSON
	subJSON, _ := json.Marshal(map[string]interface{}{
		"id": "sub_abc123",
		"customer": map[string]interface{}{
			"id": "cus_xyz789",
		},
		"status": "active",
		"items": map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"id": "si_item1",
					"price": map[string]interface{}{
						"id": "price_starter_456",
					},
				},
			},
		},
	})

	event := stripe.Event{
		Type: "customer.subscription.created",
		Data: &stripe.EventData{
			Raw: json.RawMessage(subJSON),
		},
	}

	we, err := svc.extractEvent(event)
	if err != nil {
		t.Fatalf("extractEvent error: %v", err)
	}

	if we.Type != "customer.subscription.created" {
		t.Errorf("Type = %q, want %q", we.Type, "customer.subscription.created")
	}
	if we.CustomerID != "cus_xyz789" {
		t.Errorf("CustomerID = %q, want %q", we.CustomerID, "cus_xyz789")
	}
	if we.SubscriptionID != "sub_abc123" {
		t.Errorf("SubscriptionID = %q, want %q", we.SubscriptionID, "sub_abc123")
	}
	if we.PriceID != "price_starter_456" {
		t.Errorf("PriceID = %q, want %q", we.PriceID, "price_starter_456")
	}
	if we.PlanName != "starter" {
		t.Errorf("PlanName = %q, want %q", we.PlanName, "starter")
	}
	if we.Status != "active" {
		t.Errorf("Status = %q, want %q", we.Status, "active")
	}
}

func TestExtractEvent_SubscriptionDeleted(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	subJSON, _ := json.Marshal(map[string]interface{}{
		"id": "sub_del456",
		"customer": map[string]interface{}{
			"id": "cus_del789",
		},
		"status": "canceled",
		"items": map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"id": "si_item2",
					"price": map[string]interface{}{
						"id": "price_pro_789",
					},
				},
			},
		},
	})

	event := stripe.Event{
		Type: "customer.subscription.deleted",
		Data: &stripe.EventData{
			Raw: json.RawMessage(subJSON),
		},
	}

	we, err := svc.extractEvent(event)
	if err != nil {
		t.Fatalf("extractEvent error: %v", err)
	}

	if we.Type != "customer.subscription.deleted" {
		t.Errorf("Type = %q, want %q", we.Type, "customer.subscription.deleted")
	}
	if we.Status != "canceled" {
		t.Errorf("Status = %q, want %q", we.Status, "canceled")
	}
	if we.PlanName != "pro" {
		t.Errorf("PlanName = %q, want %q", we.PlanName, "pro")
	}
}

func TestExtractEvent_InvoicePaymentFailed(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	invJSON, _ := json.Marshal(map[string]interface{}{
		"id": "inv_fail123",
		"customer": map[string]interface{}{
			"id": "cus_fail456",
		},
		"subscription": map[string]interface{}{
			"id": "sub_fail789",
		},
	})

	event := stripe.Event{
		Type: "invoice.payment_failed",
		Data: &stripe.EventData{
			Raw: json.RawMessage(invJSON),
		},
	}

	we, err := svc.extractEvent(event)
	if err != nil {
		t.Fatalf("extractEvent error: %v", err)
	}

	if we.Type != "invoice.payment_failed" {
		t.Errorf("Type = %q, want %q", we.Type, "invoice.payment_failed")
	}
	if we.CustomerID != "cus_fail456" {
		t.Errorf("CustomerID = %q, want %q", we.CustomerID, "cus_fail456")
	}
	if we.SubscriptionID != "sub_fail789" {
		t.Errorf("SubscriptionID = %q, want %q", we.SubscriptionID, "sub_fail789")
	}
	if we.Status != "payment_failed" {
		t.Errorf("Status = %q, want %q", we.Status, "payment_failed")
	}
}

func TestExtractEvent_UnhandledType(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	event := stripe.Event{
		Type: "charge.succeeded",
		Data: &stripe.EventData{
			Raw: json.RawMessage(`{}`),
		},
	}

	we, err := svc.extractEvent(event)
	if err != nil {
		t.Fatalf("extractEvent error: %v", err)
	}

	if we.Type != "charge.succeeded" {
		t.Errorf("Type = %q, want %q", we.Type, "charge.succeeded")
	}
	// Unhandled events should have empty fields
	if we.CustomerID != "" {
		t.Errorf("CustomerID should be empty for unhandled events, got %q", we.CustomerID)
	}
}

func TestExtractEvent_SubscriptionUpdatedPlanChange(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	// Simulate upgrade from starter to pro
	subJSON, _ := json.Marshal(map[string]interface{}{
		"id": "sub_upgrade",
		"customer": map[string]interface{}{
			"id": "cus_upgrade",
		},
		"status": "active",
		"items": map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"id": "si_new",
					"price": map[string]interface{}{
						"id": "price_pro_789",
					},
				},
			},
		},
	})

	event := stripe.Event{
		Type: "customer.subscription.updated",
		Data: &stripe.EventData{
			Raw: json.RawMessage(subJSON),
		},
	}

	we, err := svc.extractEvent(event)
	if err != nil {
		t.Fatalf("extractEvent error: %v", err)
	}

	if we.PlanName != "pro" {
		t.Errorf("PlanName = %q, want %q", we.PlanName, "pro")
	}
}
