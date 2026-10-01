// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/influxdata/tdigest"
	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/schemas"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	services   = []string{"auth-service", "payment-service", "inventory-service", "user-service", "cart-service"}
	methods    = []string{"GET", "POST", "PUT", "DELETE"}
	paths      = []string{"/api/v1/login", "/api/v1/users/:id", "/api/v1/products", "/api/v1/cart/checkout"}
	userAgents = []string{"Chrome", "Firefox", "Safari", "Edge", "Postman", "LoadGenerator"}
	eventTypes = []string{"deploy_started", "deploy_completed", "restart", "scale_up", "scale_down", "health_check_failed"}

	// Throughput counters (atomic for concurrent access)
	successCount   atomic.Int64
	failureCount   atomic.Int64
	totalLatencyNs atomic.Int64

	// latencies holds every request's latency in milliseconds, failures
	// included, in a bounded t-digest: synthetic-traffic runs this binary
	// indefinitely, so a slice of every observation would grow without limit.
	// It exists so the summary can report a real p95 (F-017).
	latencyMu sync.Mutex
	latencies = tdigest.NewWithCompression(100)
)

// recordLatency counts one request's latency, whatever its outcome. A failed
// request took time too, and leaving it out made the average improve as the
// system degraded (F-017).
func recordLatency(d time.Duration) {
	totalLatencyNs.Add(d.Nanoseconds())
	latencyMu.Lock()
	latencies.Add(float64(d.Nanoseconds())/1e6, 1)
	latencyMu.Unlock()
}

// latencyQuantile returns the q-quantile of recorded latencies in
// milliseconds, or 0 when nothing has been recorded.
func latencyQuantile(q float64) float64 {
	latencyMu.Lock()
	defer latencyMu.Unlock()
	if latencies.Count() == 0 {
		return 0
	}
	return latencies.Quantile(q)
}

// resetLatencies empties the counters and the digest, for tests.
func resetLatencies() {
	successCount.Store(0)
	failureCount.Store(0)
	totalLatencyNs.Store(0)
	latencyMu.Lock()
	latencies = tdigest.NewWithCompression(100)
	latencyMu.Unlock()
}

// LoadTestSummary is the structured output for benchmark mode.
type LoadTestSummary struct {
	TotalRequests int64   `json:"total_requests"`
	Successful    int64   `json:"successful"`
	Failed        int64   `json:"failed"`
	ActualQPS     float64 `json:"actual_qps"`
	// Latencies cover every request, failed ones included, measured to the
	// response headers or to the error.
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	P50LatencyMs float64 `json:"p50_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms"`
	ErrorRate    float64 `json:"error_rate"`
	DurationSecs float64 `json:"duration_secs"`
}

func computeSummary(elapsed time.Duration) LoadTestSummary {
	s := successCount.Load()
	f := failureCount.Load()
	total := s + f
	lat := totalLatencyNs.Load()

	var avgLatMs float64
	if total > 0 {
		avgLatMs = float64(lat) / float64(total) / 1e6
	}

	var errRate float64
	if total > 0 {
		errRate = float64(f) / float64(total)
	}

	return LoadTestSummary{
		TotalRequests: total,
		Successful:    s,
		Failed:        f,
		ActualQPS:     float64(total) / elapsed.Seconds(),
		AvgLatencyMs:  avgLatMs,
		P50LatencyMs:  latencyQuantile(0.50),
		P95LatencyMs:  latencyQuantile(0.95),
		P99LatencyMs:  latencyQuantile(0.99),
		ErrorRate:     errRate,
		DurationSecs:  elapsed.Seconds(),
	}
}

func main() {
	var targetURL string
	var eventsURL string
	var apiKey string
	var qps float64
	var concurrency int
	var duration time.Duration
	var verbose bool
	var benchmark bool

	flag.StringVar(&targetURL, "target", "http://localhost:8090/api/v1/facts", "Target Ingestion Service URL for facts")
	flag.StringVar(&eventsURL, "events-target", "", "Target Ingestion Service URL for service events (default: derived from --target)")
	flag.StringVar(&apiKey, "api-key", "", "API Key for Ingestion Service (also reads API_KEY env var)")
	flag.Float64Var(&qps, "qps", 5.0, "Average Queries Per Second (QPS) across all workers")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of concurrent workers")
	flag.DurationVar(&duration, "duration", 0, "Duration to run (0 for infinite)")
	flag.BoolVar(&verbose, "verbose", false, "Verbose logging")
	flag.BoolVar(&benchmark, "benchmark", false, "Benchmark mode: defaults to 30s/100qps/10 workers, prints JSON summary")
	flag.Parse()

	// Benchmark mode overrides
	if benchmark {
		if duration == 0 {
			duration = 30 * time.Second
		}
		if qps == 5.0 {
			qps = 100
		}
		if concurrency == 1 {
			concurrency = 10
		}
	}

	// Fall back to API_KEY env var if --api-key not provided
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}

	// Derive events URL from target if not explicitly set
	if eventsURL == "" {
		eventsURL = strings.Replace(targetURL, "/api/v1/facts", "/api/v1/events", 1)
	}

	logging.Init("load-generator")
	startTime := time.Now()

	slog.Info("starting load generator", "target", targetURL)
	slog.Info("events target", "url", eventsURL)
	slog.Info("configuration", "qps", qps, "concurrency", concurrency, "duration", duration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("received shutdown signal, stopping")
		cancel()
	}()

	if duration > 0 {
		time.AfterFunc(duration, func() {
			slog.Info("duration reached, stopping")
			cancel()
		})
	}

	var wg sync.WaitGroup
	// Calculate target QPS per worker (approximate)
	// Or use a global rate limiter. For simplicity, splitting QPS per worker.
	qpsPerWorker := qps / float64(concurrency)
	if qpsPerWorker <= 0 {
		qpsPerWorker = 0.1
	}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			runWorker(ctx, id, targetURL, apiKey, qpsPerWorker, verbose)
		}(i)
	}

	// Service events worker: emit ~1 event every 30 seconds (deploy/restart/scale events)
	wg.Add(1)
	go func() {
		defer wg.Done()
		runEventsWorker(ctx, eventsURL, apiKey, verbose)
	}()

	wg.Wait()
	elapsed := time.Since(startTime)
	summary := computeSummary(elapsed)

	slog.Info("load test summary",
		"total_requests", summary.TotalRequests,
		"successful", summary.Successful,
		"failed", summary.Failed,
		"actual_qps", fmt.Sprintf("%.1f", summary.ActualQPS),
		"avg_latency_ms", fmt.Sprintf("%.1f", summary.AvgLatencyMs),
		"p95_latency_ms", fmt.Sprintf("%.1f", summary.P95LatencyMs),
		"error_rate", fmt.Sprintf("%.2f%%", summary.ErrorRate*100),
		"duration", elapsed,
	)

	if benchmark {
		out, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(out))
		if summary.ErrorRate > 0.05 {
			slog.Error("benchmark failed: error rate exceeds 5%")
			os.Exit(1)
		}
	}

	slog.Info("load generator stopped")
}

func runWorker(ctx context.Context, id int, url, apiKey string, qps float64, verbose bool) {
	// Simple ticker-based rate limiting per worker
	interval := time.Duration(float64(time.Second) / qps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Add jitter to interval? For now strictly periodic + random latency in request handling
			sendRequest(ctx, client, url, apiKey, verbose)
		}
	}
}

func sendRequest(ctx context.Context, client *http.Client, url, apiKey string, verbose bool) {
	fact := generateRandomFact()

	payload, err := protojson.Marshal(fact)
	if err != nil {
		slog.Error("error marshaling protobuf to JSON", "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		slog.Error("error creating request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		recordLatency(time.Since(start))
		failureCount.Add(1)
		if verbose {
			slog.Warn("request failed", "error", err)
		}
		return
	}
	defer resp.Body.Close()
	dur := time.Since(start)
	recordLatency(dur)

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		successCount.Add(1)
	} else {
		failureCount.Add(1)
	}

	if verbose {
		slog.Info("sent fact", "event_id", fact.EventId, "status", resp.StatusCode, "duration", dur)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		slog.Warn("unexpected status code", "status", resp.StatusCode)
	}
}

func generateRandomFact() *schemas.RequestFact {
	// Simulate Latency Distribution roughly
	latency := rand.Float64()
	var latencyMs int32
	if latency < 0.90 {
		latencyMs = int32(rand.Intn(100) + 10)
	} else if latency < 0.99 {
		latencyMs = int32(rand.Intn(500) + 100)
	} else {
		latencyMs = int32(rand.Intn(2000) + 500)
	}

	// Status Codes
	status := int32(200)
	r := rand.Float64()
	if r > 0.98 {
		status = 500
	} else if r > 0.95 {
		status = 400
	}

	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}

	ua := userAgents[rand.Intn(len(userAgents))]

	return &schemas.RequestFact{
		EventId:         id.String(),
		EventTime:       timestamppb.Now(),
		Service:         services[rand.Intn(len(services))],
		Method:          methods[rand.Intn(len(methods))],
		PathTemplate:    paths[rand.Intn(len(paths))],
		StatusCode:      status,
		LatencyMs:       latencyMs,
		UserAgentFamily: ua,
	}
}

func runEventsWorker(ctx context.Context, url, apiKey string, verbose bool) {
	// Emit a service event every 15-45 seconds (random interval)
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		interval := time.Duration(15+rand.Intn(30)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			sendEvent(ctx, client, url, apiKey, verbose)
		}
	}
}

func sendEvent(ctx context.Context, client *http.Client, url, apiKey string, verbose bool) {
	event := generateRandomEvent()

	payload, err := protojson.Marshal(event)
	if err != nil {
		slog.Error("error marshaling event", "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		slog.Error("error creating event request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		if verbose {
			slog.Warn("event request failed", "error", err)
		}
		return
	}
	defer resp.Body.Close()

	if verbose {
		slog.Info("sent event", "event_id", event.EventId, "service", event.Service, "event_type", event.EventType, "status", resp.StatusCode)
	}
}

func generateRandomEvent() *schemas.ServiceEvent {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}

	service := services[rand.Intn(len(services))]
	eventType := eventTypes[rand.Intn(len(eventTypes))]

	props := map[string]string{
		"version":  fmt.Sprintf("1.%d.%d", rand.Intn(10), rand.Intn(100)),
		"instance": fmt.Sprintf("%s-%d", service, rand.Intn(5)),
	}

	return &schemas.ServiceEvent{
		EventId:    id.String(),
		EventTime:  timestamppb.Now(),
		Service:    service,
		EventType:  eventType,
		Properties: props,
	}
}
