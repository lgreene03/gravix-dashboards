package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/schemas"
)

var (
	services   = []string{"auth-service", "payment-service", "inventory-service", "user-service", "cart-service"}
	methods    = []string{"GET", "POST", "PUT", "DELETE"}
	paths      = []string{"/api/v1/login", "/api/v1/users/:id", "/api/v1/products", "/api/v1/cart/checkout"}
	userAgents = []string{"Chrome", "Firefox", "Safari", "Edge", "Postman", "LoadGenerator"}
)

func main() {
	var targetURL string
	var apiKey string
	var qps float64
	var concurrency int
	var duration time.Duration
	var verbose bool

	flag.StringVar(&targetURL, "target", "http://localhost:8090/api/v1/facts", "Target Ingestion Service URL")
	flag.StringVar(&apiKey, "api-key", "", "API Key for Ingestion Service")
	flag.Float64Var(&qps, "qps", 5.0, "Average Queries Per Second (QPS) across all workers")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of concurrent workers")
	flag.DurationVar(&duration, "duration", 0, "Duration to run (0 for infinite)")
	flag.BoolVar(&verbose, "verbose", false, "Verbose logging")
	flag.Parse()

	log.Printf("Starting Load Generator for %s", targetURL)
	log.Printf("Configuration: QPS=%.2f, Concurrency=%d, Duration=%v", qps, concurrency, duration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, stopping...")
		cancel()
	}()

	if duration > 0 {
		time.AfterFunc(duration, func() {
			log.Println("Duration reached, stopping...")
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

	wg.Wait()
	log.Println("Load Generator stopped.")
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

	payload, err := json.Marshal(fact)
	if err != nil {
		log.Printf("Error marshaling JSON: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if verbose {
			log.Printf("Request failed: %v", err)
		}
		return
	}
	defer resp.Body.Close()
	duration := time.Since(start)

	if verbose {
		log.Printf("Sent event %s: Status %d (%v)", fact.EventID, resp.StatusCode, duration)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code: %d", resp.StatusCode)
	}
}

func generateRandomFact() schemas.RequestFact {
	// Simulate Latency Distribution roughly
	// P50 ~ 50ms, P95 ~ 300ms, Occasional outliers > 1s
	latency := rand.Float64()
	var latencyMs int
	if latency < 0.90 {
		latencyMs = int(rand.Intn(100) + 10) // 10-110ms
	} else if latency < 0.99 {
		latencyMs = int(rand.Intn(500) + 100) // 100-600ms
	} else {
		latencyMs = int(rand.Intn(2000) + 500) // 500ms-2500ms
	}

	// Status Codes
	status := 200
	r := rand.Float64()
	if r > 0.98 {
		status = 500 // 2% 5xx errors
	} else if r > 0.95 {
		status = 400 // 3% 4xx errors
	}

	// Generate UUIDv7
	id, err := uuid.NewV7()
	if err != nil {
		// Fallback if needed, though NewV7 rarely fails practically
		id = uuid.New()
	}

	ua := userAgents[rand.Intn(len(userAgents))]

	return schemas.RequestFact{
		EventID:         id.String(),
		EventTime:       time.Now().UTC(),
		Service:         services[rand.Intn(len(services))],
		Method:          methods[rand.Intn(len(methods))],
		PathTemplate:    paths[rand.Intn(len(paths))],
		StatusCode:      status,
		LatencyMs:       latencyMs,
		UserAgentFamily: &ua,
	}
}
