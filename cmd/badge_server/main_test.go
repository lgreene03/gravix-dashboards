package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenderBadge_ValidSVG(t *testing.T) {
	svg := renderBadge("my-service", "operational", "#4c1")

	if !strings.Contains(svg, `<svg xmlns="http://www.w3.org/2000/svg"`) {
		t.Fatal("expected valid SVG opening tag")
	}
	if !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Fatal("expected SVG closing tag")
	}
	if !strings.Contains(svg, `role="img"`) {
		t.Error("expected role=img for accessibility")
	}
}

func TestRenderBadge_WidthCalculation(t *testing.T) {
	tests := []struct {
		label string
		value string
	}{
		{"svc", "ok"},
		{"my-long-service-name", "operational"},
		{"p95 latency", "42ms"},
	}

	for _, tc := range tests {
		t.Run(tc.label+"_"+tc.value, func(t *testing.T) {
			svg := renderBadge(tc.label, tc.value, "#4c1")

			labelWidth := float64(len(tc.label))*6.5 + 10
			valueWidth := float64(len(tc.value))*6.5 + 10
			totalWidth := labelWidth + valueWidth

			widthStr := fmt.Sprintf(`width="%.0f"`, totalWidth)
			if !strings.Contains(svg, widthStr) {
				t.Errorf("expected SVG to contain %s", widthStr)
			}
		})
	}
}

func TestRenderBadge_ContainsLabelAndValue(t *testing.T) {
	svg := renderBadge("error rate", "0.5%", "#e05d44")

	if !strings.Contains(svg, "error rate") {
		t.Error("SVG should contain the label text")
	}
	if !strings.Contains(svg, "0.5%") {
		t.Error("SVG should contain the value text")
	}
	if !strings.Contains(svg, "#e05d44") {
		t.Error("SVG should contain the color")
	}
	if !strings.Contains(svg, `aria-label="error rate: 0.5%"`) {
		t.Error("SVG should contain aria-label with label and value")
	}
	if !strings.Contains(svg, `<title>error rate: 0.5%</title>`) {
		t.Error("SVG should contain title element with label and value")
	}
}

func TestHandleBadge_ContentTypeAndCaching(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	req := httptest.NewRequest(http.MethodGet, "/badge/my-service.svg", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "image/svg+xml" {
		t.Errorf("expected Content-Type image/svg+xml, got %q", ct)
	}

	cc := resp.Header.Get("Cache-Control")
	if !strings.Contains(cc, "max-age=300") {
		t.Errorf("expected Cache-Control to contain max-age=300, got %q", cc)
	}
	if !strings.Contains(cc, "s-maxage=300") {
		t.Errorf("expected Cache-Control to contain s-maxage=300, got %q", cc)
	}

	expires := resp.Header.Get("Expires")
	if expires == "" {
		t.Error("expected Expires header to be set")
	}

	body := w.Body.String()
	if !strings.Contains(body, "<svg") {
		t.Error("expected response body to contain SVG")
	}
}

func TestHandleBadge_MissingServiceName(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	// /badge/ with no service name
	req := httptest.NewRequest(http.MethodGet, "/badge/", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing service, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "missing service name") {
		t.Errorf("expected error message about missing service name, got %q", body)
	}
}

func TestHandleBadge_MissingServiceNameSVGSuffix(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	// /badge/.svg — service name is empty after trimming .svg
	req := httptest.NewRequest(http.MethodGet, "/badge/.svg", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty service after .svg trim, got %d", resp.StatusCode)
	}
}

func TestHandleBadge_MetricP95(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	req := httptest.NewRequest(http.MethodGet, "/badge/api-gateway.svg?metric=p95", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "p95 latency") {
		t.Error("expected badge to contain 'p95 latency' label for metric=p95")
	}
}

func TestHandleBadge_MetricErrors(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	req := httptest.NewRequest(http.MethodGet, "/badge/api-gateway.svg?metric=errors", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "error rate") {
		t.Error("expected badge to contain 'error rate' label for metric=errors")
	}
}

func TestHandleBadge_MetricStatus(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	req := httptest.NewRequest(http.MethodGet, "/badge/my-service.svg?metric=status", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "my-service") {
		t.Error("expected badge to contain service name as label for metric=status")
	}
	if !strings.Contains(body, "operational") {
		t.Error("expected badge to contain 'operational' value for metric=status")
	}
}

func TestHandleBadge_DefaultMetricIsStatus(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://localhost:4000/cubejs-api/v1/load",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	// No metric query param — should default to status
	req := httptest.NewRequest(http.MethodGet, "/badge/checkout.svg", nil)
	w := httptest.NewRecorder()

	bs.handleBadge(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "checkout") {
		t.Error("expected badge to use service name as label when no metric specified")
	}
	if !strings.Contains(body, "operational") {
		t.Error("expected badge to show 'operational' when no metric specified")
	}
}
