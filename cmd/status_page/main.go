// Command status_page polls health endpoints and serves a public status page
// showing service availability and 90-day uptime history.
//
// Environment variables:
//
//	STATUS_ENDPOINTS — comma-separated list of name=url pairs to monitor
//	STATUS_PORT — HTTP port (default 8095)
//	STATUS_POLL_INTERVAL — polling interval (default 30s)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ServiceStatus represents the current and historical status of a service.
type ServiceStatus struct {
	Name      string        `json:"name"`
	URL       string        `json:"url"`
	Status    string        `json:"status"` // operational, degraded, down
	LastCheck time.Time     `json:"last_check"`
	Latency   time.Duration `json:"latency_ms"`
	UptimePct float64       `json:"uptime_pct"`
	History   []DayStatus   `json:"history"` // last 90 days
}

// DayStatus represents the status for a single day.
type DayStatus struct {
	Date      string  `json:"date"` // YYYY-MM-DD
	UptimePct float64 `json:"uptime_pct"`
	Checks    int     `json:"checks"`
	Failures  int     `json:"failures"`
}

// StatusPage holds all service statuses.
type StatusPage struct {
	mu       sync.RWMutex
	services map[string]*serviceTracker
	client   *http.Client
}

type serviceTracker struct {
	name      string
	url       string
	status    string
	lastCheck time.Time
	latency   time.Duration
	// Daily tracking
	dailyChecks   map[string]int // date → total checks
	dailyFailures map[string]int // date → failed checks
}

func newStatusPage() *StatusPage {
	return &StatusPage{
		services: make(map[string]*serviceTracker),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (sp *StatusPage) addService(name, url string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.services[name] = &serviceTracker{
		name:          name,
		url:           url,
		status:        "unknown",
		dailyChecks:   make(map[string]int),
		dailyFailures: make(map[string]int),
	}
}

func (sp *StatusPage) poll(ctx context.Context) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")

	for _, svc := range sp.services {
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.url, nil)
		if err != nil {
			svc.status = "down"
			svc.dailyChecks[today]++
			svc.dailyFailures[today]++
			continue
		}

		resp, err := sp.client.Do(req)
		elapsed := time.Since(start)
		svc.lastCheck = time.Now()
		svc.latency = elapsed
		svc.dailyChecks[today]++

		if err != nil || resp.StatusCode >= 500 {
			svc.status = "down"
			svc.dailyFailures[today]++
		} else if resp.StatusCode >= 400 {
			svc.status = "degraded"
		} else {
			svc.status = "operational"
		}
		if resp != nil {
			resp.Body.Close()
		}

		// Prune data older than 90 days
		cutoff := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
		for date := range svc.dailyChecks {
			if date < cutoff {
				delete(svc.dailyChecks, date)
				delete(svc.dailyFailures, date)
			}
		}
	}
}

func (sp *StatusPage) getStatuses() []ServiceStatus {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	var result []ServiceStatus
	for _, svc := range sp.services {
		ss := ServiceStatus{
			Name:      svc.name,
			URL:       svc.url,
			Status:    svc.status,
			LastCheck: svc.lastCheck,
			Latency:   svc.latency / time.Millisecond,
		}

		// Build 90-day history
		totalChecks := 0
		totalFailures := 0
		for i := 89; i >= 0; i-- {
			date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
			checks := svc.dailyChecks[date]
			failures := svc.dailyFailures[date]
			totalChecks += checks
			totalFailures += failures

			uptime := 100.0
			if checks > 0 {
				uptime = float64(checks-failures) / float64(checks) * 100
			}
			ss.History = append(ss.History, DayStatus{
				Date:      date,
				UptimePct: uptime,
				Checks:    checks,
				Failures:  failures,
			})
		}

		if totalChecks > 0 {
			ss.UptimePct = float64(totalChecks-totalFailures) / float64(totalChecks) * 100
		} else {
			ss.UptimePct = 100.0
		}

		result = append(result, ss)
	}
	return result
}

func (sp *StatusPage) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statuses := sp.getStatuses()

	// Check Accept header
	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"services":   statuses,
			"updated_at": time.Now().UTC(),
		})
		return
	}

	// Serve HTML status page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(renderStatusHTML(statuses)))
}

func renderStatusHTML(statuses []ServiceStatus) string {
	allOperational := true
	for _, s := range statuses {
		if s.Status != "operational" {
			allOperational = false
			break
		}
	}

	overallBanner := `<div class="banner operational">All Systems Operational</div>`
	if !allOperational {
		overallBanner = `<div class="banner degraded">Some Systems Experiencing Issues</div>`
	}

	var serviceHTML string
	for _, s := range statuses {
		statusClass := s.Status
		statusLabel := strings.Title(s.Status)
		serviceHTML += fmt.Sprintf(`
		<div class="service">
			<div class="service-header">
				<span class="service-name">%s</span>
				<span class="status-badge %s">%s</span>
			</div>
			<div class="uptime-bar">`, s.Name, statusClass, statusLabel)

		for _, day := range s.History {
			barClass := "up"
			if day.Checks == 0 {
				barClass = "no-data"
			} else if day.UptimePct < 99.0 {
				barClass = "degraded"
			}
			if day.UptimePct < 95.0 {
				barClass = "down"
			}
			serviceHTML += fmt.Sprintf(`<div class="bar %s" title="%s: %.1f%% uptime"></div>`, barClass, day.Date, day.UptimePct)
		}

		serviceHTML += fmt.Sprintf(`
			</div>
			<div class="uptime-label">%.2f%% uptime</div>
		</div>`, s.UptimePct)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Gravix Status</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,system-ui,sans-serif;background:#f8f9fa;color:#333;padding:2rem}
.container{max-width:800px;margin:0 auto}
h1{font-size:1.5rem;margin-bottom:1rem}
.banner{padding:1rem;border-radius:8px;text-align:center;font-weight:600;margin-bottom:2rem}
.banner.operational{background:#d4edda;color:#155724}
.banner.degraded{background:#fff3cd;color:#856404}
.service{background:#fff;border-radius:8px;padding:1.5rem;margin-bottom:1rem;box-shadow:0 1px 3px rgba(0,0,0,0.1)}
.service-header{display:flex;justify-content:space-between;align-items:center;margin-bottom:0.75rem}
.service-name{font-weight:600}
.status-badge{padding:0.25rem 0.75rem;border-radius:12px;font-size:0.85rem;font-weight:500}
.status-badge.operational{background:#d4edda;color:#155724}
.status-badge.degraded{background:#fff3cd;color:#856404}
.status-badge.down{background:#f8d7da;color:#721c24}
.status-badge.unknown{background:#e2e3e5;color:#383d41}
.uptime-bar{display:flex;gap:1px;height:32px;margin-bottom:0.5rem}
.bar{flex:1;border-radius:2px;min-width:2px}
.bar.up{background:#28a745}
.bar.degraded{background:#ffc107}
.bar.down{background:#dc3545}
.bar.no-data{background:#e9ecef}
.uptime-label{font-size:0.85rem;color:#6c757d;text-align:right}
.footer{text-align:center;margin-top:2rem;color:#6c757d;font-size:0.85rem}
</style>
</head>
<body>
<div class="container">
<h1>Gravix Status</h1>
%s
%s
<div class="footer">Updated %s</div>
</div>
</body>
</html>`, overallBanner, serviceHTML, time.Now().UTC().Format(time.RFC3339))
}

func main() {
	port := os.Getenv("STATUS_PORT")
	if port == "" {
		port = "8095"
	}

	pollInterval := 30 * time.Second
	if pi := os.Getenv("STATUS_POLL_INTERVAL"); pi != "" {
		if d, err := time.ParseDuration(pi); err == nil {
			pollInterval = d
		}
	}

	sp := newStatusPage()

	// Parse endpoints from env
	endpoints := os.Getenv("STATUS_ENDPOINTS")
	if endpoints == "" {
		endpoints = "Ingestion=http://localhost:8090/live,Gateway=http://localhost:8091/live,Cube=http://localhost:4000/readyz"
	}
	for _, pair := range strings.Split(endpoints, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) == 2 {
			sp.addService(parts[0], parts[1])
			slog.Info("monitoring service", "name", parts[0], "url", parts[1])
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initial poll
	sp.poll(ctx)

	// Background polling
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sp.poll(ctx)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", sp.handleStatus)
	mux.HandleFunc("/api/status", sp.handleStatus)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down status page")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("status page listening", "port", port, "poll_interval", pollInterval)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
