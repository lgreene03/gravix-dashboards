package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// dlqEntryForReplay matches the DLQEntry struct in the ingestion service.
type dlqEntryForReplay struct {
	Timestamp time.Time       `json:"timestamp"`
	TenantID  string          `json:"tenant_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	FactType  string          `json:"fact_type"`
	Error     string          `json:"error"`
	RawJSON   json.RawMessage `json:"raw_json"`
}

func runReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Parse and validate DLQ entries without resubmitting")
	since := fs.String("since", "", "Only replay entries after this time (RFC3339, e.g. 2025-01-15T00:00:00Z)")
	endpoint := fs.String("endpoint", "", "Ingestion endpoint (default: GRAVIX_ENDPOINT or http://localhost:8090)")
	file := fs.String("file", "", "Read DLQ entries from file instead of stdin")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: gravix replay [flags]

Reads DLQ (dead-letter queue) entries from stdin or a file, validates them,
and resubmits valid entries to the ingestion API.

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  gravix tail dlq | gravix replay --dry-run
  gravix replay --file dlq-export.jsonl --since 2025-01-15T00:00:00Z
  gravix replay --file dlq-export.jsonl
`)
	}

	fs.Parse(args)

	ep := *endpoint
	if ep == "" {
		ep = getEndpoint()
	}
	apiKey := requireAPIKey()

	var sinceTime time.Time
	if *since != "" {
		var err error
		sinceTime, err = time.Parse(time.RFC3339, *since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid --since value: %v\n", err)
			os.Exit(1)
		}
	}

	var reader io.Reader
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot open file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		reader = f
	} else {
		reader = os.Stdin
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: reading input: %v\n", err)
		os.Exit(1)
	}

	lines := splitLines(data)
	if len(lines) == 0 {
		fmt.Println("No DLQ entries found.")
		return
	}

	var (
		total    int
		skipped  int
		invalid  int
		replayed int
		failed   int
	)

	client := &http.Client{Timeout: 10 * time.Second}

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		total++

		var entry dlqEntryForReplay
		if err := json.Unmarshal(line, &entry); err != nil {
			fmt.Fprintf(os.Stderr, "  [SKIP] line %d: invalid JSON: %v\n", total, err)
			invalid++
			continue
		}

		// Apply --since filter
		if !sinceTime.IsZero() && entry.Timestamp.Before(sinceTime) {
			skipped++
			continue
		}

		// Check if we have raw JSON to replay
		if len(entry.RawJSON) == 0 {
			fmt.Fprintf(os.Stderr, "  [SKIP] line %d: no raw_json to replay\n", total)
			invalid++
			continue
		}

		// Determine endpoint path based on fact type
		path := "/api/v1/facts"
		if entry.FactType == "service_event" {
			path = "/api/v1/events"
		}

		if *dryRun {
			fmt.Printf("  [DRY-RUN] %s %s (original error: %s)\n", entry.FactType, entry.Timestamp.Format(time.RFC3339), entry.Error)
			replayed++
			continue
		}

		// Submit to ingestion
		url := strings.TrimRight(ep, "/") + path
		req, err := http.NewRequest("POST", url, bytes.NewReader(entry.RawJSON))
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [FAIL] line %d: create request: %v\n", total, err)
			failed++
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [FAIL] line %d: http error: %v\n", total, err)
			failed++
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
			replayed++
		} else {
			fmt.Fprintf(os.Stderr, "  [FAIL] line %d: HTTP %d\n", total, resp.StatusCode)
			failed++
		}
	}

	fmt.Printf("\nReplay summary:\n")
	fmt.Printf("  Total entries:   %d\n", total)
	fmt.Printf("  Replayed:        %d\n", replayed)
	fmt.Printf("  Skipped (time):  %d\n", skipped)
	fmt.Printf("  Invalid:         %d\n", invalid)
	fmt.Printf("  Failed:          %d\n", failed)

	if *dryRun {
		fmt.Println("\n  (dry run — no entries were actually submitted)")
	}
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
