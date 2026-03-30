package billing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v81"
)

func testPlans() []PlanConfig {
	return []PlanConfig{
		{PriceID: "price_free_123", PlanName: "free", EventLimit: 500_000, SeatLimit: 1, ServiceLimit: 2, RetentionDays: 7},
		{PriceID: "price_team_456", PlanName: "team", EventLimit: 10_000_000, SeatLimit: 5, ServiceLimit: 5, RetentionDays: 30},
		{PriceID: "price_business_789", PlanName: "business", EventLimit: 50_000_000, SeatLimit: 20, ServiceLimit: 20, RetentionDays: 90},
	}
}

func TestPlanForPriceID(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	tests := []struct {
		priceID  string
		expected string
	}{
		{"price_free_123", "free"},
		{"price_team_456", "team"},
		{"price_business_789", "business"},
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
	plans := DefaultPlans("pf", "pt", "pb", "ps", "pe")
	if len(plans) != 5 {
		t.Fatalf("expected 5 plans, got %d", len(plans))
	}

	// Verify plan names
	names := map[string]bool{}
	for _, p := range plans {
		names[p.PlanName] = true
	}
	for _, want := range []string{"free", "team", "business", "scale", "enterprise"} {
		if !names[want] {
			t.Errorf("missing plan %q", want)
		}
	}

	// Verify event limits are ordered (enterprise is 0 = unlimited)
	for i := 0; i < 3; i++ {
		if plans[i].EventLimit >= plans[i+1].EventLimit {
			t.Errorf("event limits should increase: %s (%d) should be < %s (%d)",
				plans[i].PlanName, plans[i].EventLimit, plans[i+1].PlanName, plans[i+1].EventLimit)
		}
	}

	// Verify seat limits
	if plans[0].SeatLimit != 1 {
		t.Errorf("free seat limit = %d, want 1", plans[0].SeatLimit)
	}
	if plans[4].SeatLimit != 0 {
		t.Errorf("enterprise seat limit = %d, want 0 (unlimited)", plans[4].SeatLimit)
	}

	// Verify retention days
	if plans[0].RetentionDays != 7 {
		t.Errorf("free retention = %d, want 7", plans[0].RetentionDays)
	}
}

func TestPlanSeatLimit(t *testing.T) {
	tests := []struct{ plan string; want int }{
		{"free", 1}, {"team", 5}, {"business", 20}, {"scale", 0}, {"enterprise", 0},
		{"starter", 5}, {"pro", 20}, // legacy
	}
	for _, tt := range tests {
		if got := PlanSeatLimit(tt.plan); got != tt.want {
			t.Errorf("PlanSeatLimit(%q) = %d, want %d", tt.plan, got, tt.want)
		}
	}
}

func TestPlanEventLimit(t *testing.T) {
	if got := PlanEventLimit("free"); got != 500_000 {
		t.Errorf("free = %d, want 500000", got)
	}
	if got := PlanEventLimit("scale"); got != 200_000_000 {
		t.Errorf("scale = %d, want 200000000", got)
	}
	if got := PlanEventLimit("enterprise"); got != 0 {
		t.Errorf("enterprise = %d, want 0 (unlimited)", got)
	}
}

func TestPlanRetentionDays(t *testing.T) {
	tests := []struct{ plan string; want int }{
		{"free", 7}, {"team", 30}, {"business", 90}, {"scale", 365}, {"enterprise", 365},
		{"starter", 30}, {"pro", 90}, // legacy
	}
	for _, tt := range tests {
		if got := PlanRetentionDays(tt.plan); got != tt.want {
			t.Errorf("PlanRetentionDays(%q) = %d, want %d", tt.plan, got, tt.want)
		}
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
						"id": "price_team_456",
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
	if we.PriceID != "price_team_456" {
		t.Errorf("PriceID = %q, want %q", we.PriceID, "price_team_456")
	}
	if we.PlanName != "team" {
		t.Errorf("PlanName = %q, want %q", we.PlanName, "team")
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
						"id": "price_business_789",
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
	if we.PlanName != "business" {
		t.Errorf("PlanName = %q, want %q", we.PlanName, "business")
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

// ─── Edge Case Tests ───

func TestExtractEvent_EmptySubscriptionItems(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	subJSON, _ := json.Marshal(map[string]interface{}{
		"id": "sub_empty",
		"customer": map[string]interface{}{
			"id": "cus_empty",
		},
		"status": "active",
		"items": map[string]interface{}{
			"data": []map[string]interface{}{},
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

	if we.PriceID != "" {
		t.Errorf("PriceID should be empty for subscription with no items, got %q", we.PriceID)
	}
	if we.PlanName != "" {
		t.Errorf("PlanName should be empty for subscription with no items, got %q", we.PlanName)
	}
}

func TestExtractEvent_MalformedSubscriptionJSON(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	event := stripe.Event{
		Type: "customer.subscription.created",
		Data: &stripe.EventData{
			Raw: json.RawMessage(`{invalid json`),
		},
	}

	_, err := svc.extractEvent(event)
	if err == nil {
		t.Fatal("expected error for malformed subscription JSON")
	}
}

func TestExtractEvent_MalformedInvoiceJSON(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	event := stripe.Event{
		Type: "invoice.payment_failed",
		Data: &stripe.EventData{
			Raw: json.RawMessage(`{not valid json`),
		},
	}

	_, err := svc.extractEvent(event)
	if err == nil {
		t.Fatal("expected error for malformed invoice JSON")
	}
}

func TestExtractEvent_InvoiceWithoutSubscription(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	invJSON, _ := json.Marshal(map[string]interface{}{
		"id": "inv_oneoff",
		"customer": map[string]interface{}{
			"id": "cus_oneoff",
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

	if we.SubscriptionID != "" {
		t.Errorf("SubscriptionID should be empty for invoice without subscription, got %q", we.SubscriptionID)
	}
	if we.Status != "payment_failed" {
		t.Errorf("Status = %q, want payment_failed", we.Status)
	}
}

func TestNewStripeService_NoFreePlan(t *testing.T) {
	plans := []PlanConfig{
		{PriceID: "price_team", PlanName: "team", EventLimit: 10_000_000},
		{PriceID: "price_business", PlanName: "business", EventLimit: 50_000_000},
	}
	svc := NewStripeService("sk_test_fake", "whsec_fake", plans)

	if svc.FreePriceID() != "" {
		t.Errorf("FreePriceID() should be empty when no free plan, got %q", svc.FreePriceID())
	}
}

// ─── MockService Tests ───

func TestMockService_ImplementsInterface(t *testing.T) {
	var _ Service = &MockService{}
}

func TestMockService_CallTracking(t *testing.T) {
	m := &MockService{
		CustomerID:     "cus_mock",
		SubscriptionID: "sub_mock",
		PortalURL:      "https://billing.example.com",
		FreePriceIDVal: "price_free",
		PlanName:       "team",
	}

	ctx := context.Background()
	if id, err := m.CreateCustomer(ctx, "Test", "test@example.com", "t1"); err != nil || id != "cus_mock" {
		t.Errorf("CreateCustomer = (%q, %v), want (cus_mock, nil)", id, err)
	}
	if id, err := m.CreateSubscription(ctx, "cus_mock", "price_1"); err != nil || id != "sub_mock" {
		t.Errorf("CreateSubscription = (%q, %v), want (sub_mock, nil)", id, err)
	}
	if err := m.ReportUsage(ctx, "sub_mock", 100, time.Now()); err != nil {
		t.Errorf("ReportUsage = %v, want nil", err)
	}
	if url, err := m.CreatePortalSession(ctx, "cus_mock", "https://return.example.com"); err != nil || url != "https://billing.example.com" {
		t.Errorf("CreatePortalSession = (%q, %v)", url, err)
	}

	if m.CreateCustomerCalls != 1 {
		t.Errorf("CreateCustomerCalls = %d, want 1", m.CreateCustomerCalls)
	}
	if m.CreateSubscriptionCalls != 1 {
		t.Errorf("CreateSubscriptionCalls = %d, want 1", m.CreateSubscriptionCalls)
	}
	if m.ReportUsageCalls != 1 {
		t.Errorf("ReportUsageCalls = %d, want 1", m.ReportUsageCalls)
	}
	if m.CreatePortalCalls != 1 {
		t.Errorf("CreatePortalCalls = %d, want 1", m.CreatePortalCalls)
	}

	if m.PlanForPriceID("any") != "team" {
		t.Errorf("PlanForPriceID = %q, want team", m.PlanForPriceID("any"))
	}
	if m.FreePriceID() != "price_free" {
		t.Errorf("FreePriceID = %q, want price_free", m.FreePriceID())
	}
}

func TestMockService_DefaultPlanName(t *testing.T) {
	m := &MockService{}
	if got := m.PlanForPriceID("any"); got != "unknown" {
		t.Errorf("PlanForPriceID with empty PlanName = %q, want unknown", got)
	}
}

func TestExtractEvent_SubscriptionUpdatedPlanChange(t *testing.T) {
	svc := NewStripeService("sk_test_fake", "whsec_fake", testPlans())

	// Simulate upgrade from team to business
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
						"id": "price_business_789",
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

	if we.PlanName != "business" {
		t.Errorf("PlanName = %q, want %q", we.PlanName, "business")
	}
}
