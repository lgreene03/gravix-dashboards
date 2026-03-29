// Command badge_server serves status badges for services.
//
// GET /badge/:tenant/:service.svg returns an SVG badge showing the service's
// current status (p95 latency, error rate, or uptime).
//
// Environment:
//
//	BADGE_PORT — HTTP port (default 8096)
//	CUBE_API_URL — Cube.js API URL
//	CUBE_API_SECRET — Cube.js API secret for auth
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	port := os.Getenv("BADGE_PORT")
	if port == "" {
		port = "8096"
	}

	cubeURL := os.Getenv("CUBE_API_URL")
	if cubeURL == "" {
		cubeURL = "http://localhost:4000/cubejs-api/v1/load"
	}

	mux := http.NewServeMux()
	bs := &badgeServer{
		cubeURL: cubeURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}

	mux.HandleFunc("/badge/", bs.handleBadge)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	slog.Info("badge server listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

type badgeServer struct {
	cubeURL string
	client  *http.Client
}

func (bs *badgeServer) handleBadge(w http.ResponseWriter, r *http.Request) {
	// Parse: /badge/{service}.svg or /badge/{service}
	path := strings.TrimPrefix(r.URL.Path, "/badge/")
	path = strings.TrimSuffix(path, ".svg")

	if path == "" {
		http.Error(w, "missing service name", http.StatusBadRequest)
		return
	}

	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "status"
	}

	var label, value, color string

	switch metric {
	case "p95":
		label = "p95 latency"
		value, color, _ = bs.fetchMetric(path, "p95")
	case "errors":
		label = "error rate"
		value, color, _ = bs.fetchMetric(path, "errors")
	default:
		label = path
		value, color, _ = bs.fetchMetric(path, "status")
	}

	// Generate SVG badge
	svg := renderBadge(label, value, color)

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "max-age=300, s-maxage=300")
	w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, svg)
}

// renderBadge generates a shields.io-style SVG badge.
func renderBadge(label, value, color string) string {
	labelWidth := float64(len(label))*6.5 + 10
	valueWidth := float64(len(value))*6.5 + 10
	totalWidth := labelWidth + valueWidth

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="20" role="img" aria-label="%s: %s">
  <title>%s: %s</title>
  <linearGradient id="s" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r"><rect width="%.0f" height="20" rx="3" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="%.0f" height="20" fill="#555"/>
    <rect x="%.0f" width="%.0f" height="20" fill="%s"/>
    <rect width="%.0f" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="11">
    <text aria-hidden="true" x="%.1f" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%.1f" y="14">%s</text>
    <text aria-hidden="true" x="%.1f" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%.1f" y="14">%s</text>
  </g>
</svg>`,
		totalWidth, label, value,
		label, value,
		totalWidth,
		labelWidth,
		labelWidth, valueWidth, color,
		totalWidth,
		float64(labelWidth)/2, label,
		float64(labelWidth)/2, label,
		float64(labelWidth)+float64(valueWidth)/2, value,
		float64(labelWidth)+float64(valueWidth)/2, value,
	)
}

// cubeQuery represents a Cube.js query payload.
type cubeQuery struct {
	Measures       []string          `json:"measures"`
	Filters        []cubeFilter      `json:"filters,omitempty"`
	TimeDimensions []cubeTimeDim     `json:"timeDimensions,omitempty"`
}

type cubeFilter struct {
	Member   string   `json:"member"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type cubeTimeDim struct {
	Dimension string `json:"dimension"`
	DateRange string `json:"dateRange"`
}

type cubeRequest struct {
	Query cubeQuery `json:"query"`
}

// cubeResponse represents the Cube.js load API response.
type cubeResponse struct {
	Data []map[string]interface{} `json:"data"`
}

const (
	colorGreen  = "#4c1"
	colorYellow = "#dfb317"
	colorRed    = "#e05d44"
	colorGray   = "#9f9f9f"
)

// queryCube sends a query to the Cube.js API and returns the first row's value
// for the given measure. Returns 0 and an error if the query fails.
func (bs *badgeServer) queryCube(service, measure string) (float64, error) {
	req := cubeRequest{
		Query: cubeQuery{
			Measures: []string{measure},
			Filters: []cubeFilter{
				{
					Member:   "RequestMetricsMinute.service",
					Operator: "equals",
					Values:   []string{service},
				},
			},
			TimeDimensions: []cubeTimeDim{
				{
					Dimension: "RequestMetricsMinute.minuteTs",
					DateRange: "last 1 hour",
				},
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal cube query: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, bs.cubeURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create cube request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := bs.client.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("cube request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("cube returned status %d", resp.StatusCode)
	}

	var cubeResp cubeResponse
	if err := json.NewDecoder(resp.Body).Decode(&cubeResp); err != nil {
		return 0, fmt.Errorf("decode cube response: %w", err)
	}

	if len(cubeResp.Data) == 0 {
		return 0, fmt.Errorf("cube returned no data")
	}

	val, ok := cubeResp.Data[0][measure]
	if !ok {
		return 0, fmt.Errorf("measure %s not found in response", measure)
	}

	switch v := val.(type) {
	case float64:
		return v, nil
	case json.Number:
		return v.Float64()
	default:
		return 0, fmt.Errorf("unexpected type for measure value: %T", val)
	}
}

// fetchMetric queries Cube.js for the requested metric and returns a display
// value, badge color, and any error. On error it falls back to "—" with gray.
func (bs *badgeServer) fetchMetric(service, metric string) (string, string, error) {
	switch metric {
	case "p95":
		p95, err := bs.queryCube(service, "RequestMetricsMinute.p95Latency")
		if err != nil {
			slog.Warn("failed to fetch p95", "service", service, "error", err)
			return "—", colorGray, err
		}
		value := fmt.Sprintf("%.0fms", p95)
		color := colorGreen
		if p95 >= 500 {
			color = colorRed
		} else if p95 >= 200 {
			color = colorYellow
		}
		return value, color, nil

	case "errors":
		errRate, err := bs.queryCube(service, "RequestMetricsMinute.errorRate")
		if err != nil {
			slog.Warn("failed to fetch error rate", "service", service, "error", err)
			return "—", colorGray, err
		}
		value := fmt.Sprintf("%.1f%%", errRate)
		color := colorGreen
		if errRate >= 5 {
			color = colorRed
		} else if errRate >= 1 {
			color = colorYellow
		}
		return value, color, nil

	default: // "status"
		p95, p95Err := bs.queryCube(service, "RequestMetricsMinute.p95Latency")
		errRate, errErr := bs.queryCube(service, "RequestMetricsMinute.errorRate")
		if p95Err != nil || errErr != nil {
			slog.Warn("failed to fetch status metrics", "service", service,
				"p95_error", p95Err, "error_rate_error", errErr)
			return "—", colorGray, fmt.Errorf("cube unavailable")
		}
		if errRate > 20 {
			return "outage", colorRed, nil
		}
		if p95 >= 500 || errRate >= 5 {
			return "degraded", colorYellow, nil
		}
		return "operational", colorGreen, nil
	}
}
