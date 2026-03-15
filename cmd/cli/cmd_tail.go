package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type dlqEntryResponse struct {
	Entries []struct {
		Timestamp string          `json:"timestamp"`
		TenantID  string          `json:"tenant_id"`
		FactType  string          `json:"fact_type"`
		Error     string          `json:"error"`
		RawJSON   json.RawMessage `json:"raw_json"`
	} `json:"entries"`
	Total int `json:"total"`
}

func runTailDLQ(args []string) {
	fs := flag.NewFlagSet("gravix tail dlq", flag.ExitOnError)
	follow := fs.Bool("follow", false, "Follow new entries (poll every 5s)")
	fs.BoolVar(follow, "f", false, "Follow new entries (short flag)")
	limit := fs.Int("limit", 20, "Number of entries to display")
	endpoint := fs.String("endpoint", "", "Gateway endpoint (env: GRAVIX_GATEWAY_URL)")
	fs.Parse(args)

	gwURL := resolveStr(*endpoint, "GRAVIX_GATEWAY_URL")
	if gwURL == "" {
		gwURL = "http://localhost:8091"
	}
	gwURL = strings.TrimRight(gwURL, "/")

	apiKey := getAPIKey()
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: GRAVIX_API_KEY environment variable is required.\n")
		os.Exit(1)
	}

	seenTimestamps := make(map[string]bool)

	for {
		entries, err := fetchDLQEntries(gwURL, apiKey, *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			if !*follow {
				os.Exit(1)
			}
			time.Sleep(5 * time.Second)
			continue
		}

		if len(entries.Entries) == 0 && !*follow {
			fmt.Println("No DLQ entries found.")
			return
		}

		// Print entries (skip already-seen in follow mode)
		for _, e := range entries.Entries {
			key := e.Timestamp + "|" + e.Error
			if *follow && seenTimestamps[key] {
				continue
			}
			seenTimestamps[key] = true

			raw := string(e.RawJSON)
			if len(raw) > 80 {
				raw = raw[:77] + "..."
			}

			ts := e.Timestamp
			if len(ts) > 19 {
				ts = ts[:19]
			}

			fmt.Printf("%-19s  %-50s  %s\n", ts, truncate(e.Error, 50), raw)
		}

		if !*follow {
			return
		}

		time.Sleep(5 * time.Second)
	}
}

func fetchDLQEntries(gwURL, apiKey string, limit int) (*dlqEntryResponse, error) {
	url := fmt.Sprintf("%s/api/gateway/dlq?limit=%d", gwURL, limit)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gateway returned %d: %s", resp.StatusCode, string(body))
	}

	var result dlqEntryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
