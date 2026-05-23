package main

import (
	"encoding/json"
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

// mockCubeServer returns an httptest.Server that responds to Cube.js queries
// with the given measures map (measure name -> value).
func mockCubeServer(t *testing.T, measures map[string]float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}

		var req cubeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		if len(req.Query.Measures) == 0 {
			http.Error(w, "no measures", http.StatusBadRequest)
			return
		}

		measure := req.Query.Measures[0]
		val, ok := measures[measure]
		if !ok {
			// Return empty data for unknown measures
			json.NewEncoder(w).Encode(cubeResponse{Data: []map[string]interface{}{}})
			return
		}

		resp := cubeResponse{
			Data: []map[string]interface{}{
				{measure: val},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestHandleBadge_ContentTypeAndCaching(t *testing.T) {
	cube := mockCubeServer(t, map[string]float64{
		"RequestMetricsMinute.p95Latency": 100,
		"RequestMetricsMinute.errorRate":  0.5,
	})
	defer cube.Close()

	bs := &badgeServer{
		cubeURL: cube.URL,
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
	cube := mockCubeServer(t, map[string]float64{
		"RequestMetricsMinute.p95Latency": 42,
	})
	defer cube.Close()

	bs := &badgeServer{
		cubeURL: cube.URL,
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
	if !strings.Contains(body, "42ms") {
		t.Error("expected badge to contain '42ms' value")
	}
	if !strings.Contains(body, colorGreen) {
		t.Error("expected green color for p95 < 200ms")
	}
}

func TestHandleBadge_MetricErrors(t *testing.T) {
	cube := mockCubeServer(t, map[string]float64{
		"RequestMetricsMinute.errorRate": 0.5,
	})
	defer cube.Close()

	bs := &badgeServer{
		cubeURL: cube.URL,
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
	if !strings.Contains(body, "0.5%") {
		t.Error("expected badge to contain '0.5%' value")
	}
	if !strings.Contains(body, colorGreen) {
		t.Error("expected green color for error rate < 1%")
	}
}

func TestHandleBadge_MetricStatus(t *testing.T) {
	cube := mockCubeServer(t, map[string]float64{
		"RequestMetricsMinute.p95Latency": 100,
		"RequestMetricsMinute.errorRate":  0.5,
	})
	defer cube.Close()

	bs := &badgeServer{
		cubeURL: cube.URL,
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
	cube := mockCubeServer(t, map[string]float64{
		"RequestMetricsMinute.p95Latency": 100,
		"RequestMetricsMinute.errorRate":  0.5,
	})
	defer cube.Close()

	bs := &badgeServer{
		cubeURL: cube.URL,
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

func TestFetchMetric_P95Colors(t *testing.T) {
	tests := []struct {
		name     string
		p95      float64
		wantVal  string
		wantColor string
	}{
		{"green", 50, "50ms", colorGreen},
		{"yellow_boundary", 200, "200ms", colorYellow},
		{"yellow", 350, "350ms", colorYellow},
		{"red_boundary", 500, "500ms", colorRed},
		{"red", 1200, "1200ms", colorRed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cube := mockCubeServer(t, map[string]float64{
				"RequestMetricsMinute.p95Latency": tc.p95,
			})
			defer cube.Close()

			bs := &badgeServer{cubeURL: cube.URL, client: &http.Client{Timeout: 5 * time.Second}}
			val, color, err := bs.fetchMetric("svc", "p95")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tc.wantVal {
				t.Errorf("value = %q, want %q", val, tc.wantVal)
			}
			if color != tc.wantColor {
				t.Errorf("color = %q, want %q", color, tc.wantColor)
			}
		})
	}
}

func TestFetchMetric_ErrorRateColors(t *testing.T) {
	tests := []struct {
		name      string
		errRate   float64
		wantVal   string
		wantColor string
	}{
		{"green", 0.3, "0.3%", colorGreen},
		{"yellow_boundary", 1.0, "1.0%", colorYellow},
		{"yellow", 3.5, "3.5%", colorYellow},
		{"red_boundary", 5.0, "5.0%", colorRed},
		{"red", 12.0, "12.0%", colorRed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cube := mockCubeServer(t, map[string]float64{
				"RequestMetricsMinute.errorRate": tc.errRate,
			})
			defer cube.Close()

			bs := &badgeServer{cubeURL: cube.URL, client: &http.Client{Timeout: 5 * time.Second}}
			val, color, err := bs.fetchMetric("svc", "errors")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tc.wantVal {
				t.Errorf("value = %q, want %q", val, tc.wantVal)
			}
			if color != tc.wantColor {
				t.Errorf("color = %q, want %q", color, tc.wantColor)
			}
		})
	}
}

func TestFetchMetric_StatusVariants(t *testing.T) {
	tests := []struct {
		name      string
		p95       float64
		errRate   float64
		wantVal   string
		wantColor string
	}{
		{"operational", 100, 0.5, "operational", colorGreen},
		{"degraded_high_p95", 600, 2.0, "degraded", colorYellow},
		{"degraded_high_errors", 100, 6.0, "degraded", colorYellow},
		{"outage", 100, 25.0, "outage", colorRed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cube := mockCubeServer(t, map[string]float64{
				"RequestMetricsMinute.p95Latency": tc.p95,
				"RequestMetricsMinute.errorRate":  tc.errRate,
			})
			defer cube.Close()

			bs := &badgeServer{cubeURL: cube.URL, client: &http.Client{Timeout: 5 * time.Second}}
			val, color, err := bs.fetchMetric("svc", "status")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tc.wantVal {
				t.Errorf("value = %q, want %q", val, tc.wantVal)
			}
			if color != tc.wantColor {
				t.Errorf("color = %q, want %q", color, tc.wantColor)
			}
		})
	}
}

func TestFetchMetric_CubeUnavailable(t *testing.T) {
	bs := &badgeServer{
		cubeURL: "http://127.0.0.1:1", // unreachable
		client:  &http.Client{Timeout: 1 * time.Second},
	}

	for _, metric := range []string{"p95", "errors", "status"} {
		t.Run(metric, func(t *testing.T) {
			val, color, err := bs.fetchMetric("svc", metric)
			if err == nil {
				t.Fatal("expected error when Cube.js is unavailable")
			}
			if val != "—" {
				t.Errorf("expected fallback value '—', got %q", val)
			}
			if color != colorGray {
				t.Errorf("expected gray fallback color, got %q", color)
			}
		})
	}
}

func TestFetchMetric_CubeReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	bs := &badgeServer{
		cubeURL: srv.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	val, color, err := bs.fetchMetric("svc", "p95")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if val != "—" {
		t.Errorf("expected fallback value, got %q", val)
	}
	if color != colorGray {
		t.Errorf("expected gray color, got %q", color)
	}
}
