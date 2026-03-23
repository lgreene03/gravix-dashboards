package email

import (
	"strings"
	"testing"
)

func TestRenderWelcome(t *testing.T) {
	html, text, err := RenderWelcome(OnboardingData{
		UserName:   "Alice",
		TenantName: "Acme Corp",
		Plan:       "team",
		BaseURL:    "https://app.gravix.io",
	})
	if err != nil {
		t.Fatalf("RenderWelcome: %v", err)
	}
	if !strings.Contains(html, "Alice") {
		t.Error("HTML should contain user name")
	}
	if !strings.Contains(html, "Acme Corp") {
		t.Error("HTML should contain tenant name")
	}
	if !strings.Contains(html, "team") {
		t.Error("HTML should contain plan name")
	}
	if !strings.Contains(text, "Alice") {
		t.Error("text should contain user name")
	}
}

func TestRenderSetupGuide(t *testing.T) {
	html, text, err := RenderSetupGuide(OnboardingData{
		UserName: "Bob",
		BaseURL:  "https://app.gravix.io",
	})
	if err != nil {
		t.Fatalf("RenderSetupGuide: %v", err)
	}
	if !strings.Contains(html, "Bob") {
		t.Error("HTML should contain user name")
	}
	if !strings.Contains(html, "curl") {
		t.Error("HTML should contain curl example")
	}
	if text == "" {
		t.Error("text body should not be empty")
	}
}

func TestRenderQuotaWarning(t *testing.T) {
	html, text, err := RenderQuotaWarning(BillingAlertData{
		UserName:     "Charlie",
		TenantName:   "Startup Inc",
		Plan:         "business",
		EventCount:   45_000_000,
		EventLimit:   50_000_000,
		UsagePercent: 90,
		BaseURL:      "https://app.gravix.io",
	})
	if err != nil {
		t.Fatalf("RenderQuotaWarning: %v", err)
	}
	if !strings.Contains(html, "90%") {
		t.Error("HTML should contain usage percentage")
	}
	if !strings.Contains(html, "Startup Inc") {
		t.Error("HTML should contain tenant name")
	}
	if !strings.Contains(text, "90%") {
		t.Error("text should contain usage percentage")
	}
}
