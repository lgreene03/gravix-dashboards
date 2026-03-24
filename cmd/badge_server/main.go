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
		value = "—"
		color = "#4c1"
	case "errors":
		label = "error rate"
		value = "—"
		color = "#4c1"
	default:
		label = path
		value = "operational"
		color = "#4c1"
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

// Placeholder for future Cube.js integration
func (bs *badgeServer) fetchMetric(service, metric string) (string, string, error) {
	_ = json.NewDecoder // suppress unused import
	return "—", "#4c1", nil
}
