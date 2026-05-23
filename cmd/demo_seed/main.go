// Command demo_seed generates 30 days of realistic request data across
// 5 services for demo/evaluation purposes.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"time"
)

type requestFact struct {
	EventID         string `json:"event_id"`
	EventTime       string `json:"event_time"`
	Service         string `json:"service"`
	Method          string `json:"method"`
	PathTemplate    string `json:"path_template"`
	StatusCode      int    `json:"status_code"`
	LatencyMs       int    `json:"latency_ms"`
	UserAgentFamily string `json:"user_agent_family"`
}

var services = []struct {
	name   string
	routes []struct{ method, path string }
}{
	{"api-gateway", []struct{ method, path string }{
		{"GET", "/api/v1/users/{id}"}, {"POST", "/api/v1/users"}, {"GET", "/api/v1/health"},
		{"GET", "/api/v1/products"}, {"GET", "/api/v1/products/{id}"}, {"PUT", "/api/v1/products/{id}"},
	}},
	{"auth-service", []struct{ method, path string }{
		{"POST", "/auth/login"}, {"POST", "/auth/register"}, {"POST", "/auth/refresh"},
		{"POST", "/auth/logout"}, {"GET", "/auth/verify"},
	}},
	{"payment-service", []struct{ method, path string }{
		{"POST", "/payments/charge"}, {"GET", "/payments/{id}"}, {"POST", "/payments/refund"},
		{"GET", "/payments/history"}, {"POST", "/webhooks/stripe"},
	}},
	{"notification-service", []struct{ method, path string }{
		{"POST", "/notifications/send"}, {"GET", "/notifications/{id}"}, {"POST", "/notifications/bulk"},
		{"GET", "/notifications/status"},
	}},
	{"search-service", []struct{ method, path string }{
		{"GET", "/search"}, {"GET", "/search/suggest"}, {"POST", "/search/index"},
		{"GET", "/search/health"},
	}},
}

var userAgents = []string{"Chrome", "Firefox", "Safari", "Edge", "Mobile-Safari", "okhttp", "python-requests", "curl"}

func main() {
	ingestionURL := flag.String("url", envOr("INGESTION_URL", "http://localhost:8090/api/v1/facts"), "Ingestion API URL")
	apiKey := flag.String("api-key", envOr("API_KEY", ""), "API key for ingestion")
	days := flag.Int("days", 30, "Number of days of historical data to generate")
	eventsPerDay := flag.Int("events-per-day", 50000, "Average events per day")
	flag.Parse()

	if *apiKey == "" {
		slog.Error("API key required (--api-key or API_KEY env var)")
		os.Exit(1)
	}

	slog.Info("demo_seed starting",
		"url", *ingestionURL,
		"days", *days,
		"events_per_day", *eventsPerDay,
	)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	now := time.Now().UTC()
	totalSent := 0

	for day := *days; day > 0; day-- {
		date := now.AddDate(0, 0, -day)
		dayEvents := *eventsPerDay + rng.Intn(*eventsPerDay/5) - *eventsPerDay/10 // +/- 10%

		// Batch events
		batch := make([]requestFact, 0, 500)
		for i := 0; i < dayEvents; i++ {
			fact := generateFact(rng, date)
			batch = append(batch, fact)

			if len(batch) >= 500 {
				if err := sendBatch(*ingestionURL, *apiKey, batch); err != nil {
					slog.Error("send batch failed", "error", err, "day", date.Format("2006-01-02"))
				}
				totalSent += len(batch)
				batch = batch[:0]
			}
		}
		if len(batch) > 0 {
			if err := sendBatch(*ingestionURL, *apiKey, batch); err != nil {
				slog.Error("send batch failed", "error", err, "day", date.Format("2006-01-02"))
			}
			totalSent += len(batch)
		}

		slog.Info("day seeded", "date", date.Format("2006-01-02"), "events", dayEvents, "total", totalSent)
	}

	slog.Info("demo_seed complete", "total_events", totalSent)
}

func generateFact(rng *rand.Rand, date time.Time) requestFact {
	svc := services[rng.Intn(len(services))]
	route := svc.routes[rng.Intn(len(route(svc)))]

	// Random time within the day with realistic traffic pattern (peak during business hours)
	hour := weightedHour(rng)
	minute := rng.Intn(60)
	second := rng.Intn(60)
	ms := rng.Intn(1000)
	eventTime := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, second, ms*1e6, time.UTC)

	// Status code distribution: 95% success, 3% client error, 2% server error
	statusCode := generateStatus(rng)
	latency := generateLatency(rng, svc.name, statusCode)

	return requestFact{
		EventID:         fmt.Sprintf("%08x-%04x-7%03x-%04x-%012x", rng.Uint32(), rng.Uint32()&0xffff, rng.Uint32()&0xfff, 0x8000|(rng.Uint32()&0x3fff), rng.Int63()),
		EventTime:       eventTime.Format(time.RFC3339Nano),
		Service:         svc.name,
		Method:          route.method,
		PathTemplate:    route.path,
		StatusCode:      statusCode,
		LatencyMs:       latency,
		UserAgentFamily: userAgents[rng.Intn(len(userAgents))],
	}
}

func route(svc struct {
	name   string
	routes []struct{ method, path string }
}) []struct{ method, path string } {
	return svc.routes
}

func weightedHour(rng *rand.Rand) int {
	// More traffic during 9-17 UTC
	weights := []int{1, 1, 1, 1, 1, 2, 3, 5, 8, 10, 10, 9, 8, 10, 10, 9, 8, 7, 5, 4, 3, 2, 2, 1}
	total := 0
	for _, w := range weights {
		total += w
	}
	r := rng.Intn(total)
	cumulative := 0
	for hour, w := range weights {
		cumulative += w
		if r < cumulative {
			return hour
		}
	}
	return 12
}

func generateStatus(rng *rand.Rand) int {
	r := rng.Float64()
	if r < 0.70 {
		return 200
	} else if r < 0.85 {
		return 201
	} else if r < 0.90 {
		return 204
	} else if r < 0.92 {
		return 301
	} else if r < 0.95 {
		return 304
	} else if r < 0.965 {
		return 400
	} else if r < 0.975 {
		return 401
	} else if r < 0.98 {
		return 404
	} else if r < 0.985 {
		return 429
	} else if r < 0.99 {
		return 500
	} else if r < 0.995 {
		return 502
	}
	return 503
}

func generateLatency(rng *rand.Rand, service string, status int) int {
	// Base latency varies by service
	var base float64
	switch service {
	case "auth-service":
		base = 80
	case "payment-service":
		base = 200
	case "search-service":
		base = 50
	default:
		base = 30
	}

	// Server errors tend to be slower
	if status >= 500 {
		base *= 3
	}

	// Log-normal distribution for realistic latency
	latency := base * (1 + rng.ExpFloat64()*0.5)
	if latency < 1 {
		latency = 1
	}
	if latency > 30000 {
		latency = 30000
	}
	return int(latency)
}

func sendBatch(url, apiKey string, facts []requestFact) error {
	data, err := json.Marshal(facts)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
