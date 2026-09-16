// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The other direction from cmd_migrate_export.go, and core for the symmetric
// reason: a self-hoster moving to Cloud is not made to pay to bring their own
// history with them.
//
// There is no new server-side code behind this. It replays facts through
// POST /api/v1/facts/batch — the same public endpoint every SDK already calls
// — because ValidateRequestFact has never had a staleness check on EventTime.
// A fact from eighteen months ago is accepted exactly as one from a second
// ago, which is what makes historical replay possible without a special
// import API that would then need its own auth, its own limits and its own
// bugs.

var (
	ErrMissingTenantDir  = errors.New("migrate import-cloud: --tenant-dir-name is required")
	ErrMissingAPIKey     = errors.New("migrate import-cloud: --api-key is required (or set GRAVIX_API_KEY)")
	ErrIngestionRejected = errors.New("migrate import-cloud: ingestion API returned a non-2xx status")
)

const migrateImportUsage = "usage: gravix migrate import-cloud --tenant-dir-name <name> [flags]"

// maxBatchBytes is the byte budget for one /api/v1/facts/batch body.
//
// Ingestion's own limit is 1 MB (maxBodyBytes in services/ingestion/main.go).
// This leaves ~148 KB of headroom rather than sitting on the boundary: a
// request one byte over is rejected whole, and a migration that discovers that
// on the ten-thousandth batch has wasted an afternoon.
const maxBatchBytes = 900_000

// defaultRetryAfter is used when a 429 arrives without a parseable
// Retry-After. Ingestion always sends 30, but a proxy in between may not.
const defaultRetryAfter = 30 * time.Second

// importSummary is printed to stdout as JSON when the run completes.
//
// Rejected and failed counts are reported rather than treated as fatal,
// because the honest outcome of replaying a year of history is usually "almost
// all of it". A command that aborted on the first rejected line would make a
// customer restart a migration over one bad fact from last March.
type importSummary struct {
	FilesRead      int       `json:"files_read"`
	FactsAccepted  int       `json:"facts_accepted"`
	FactsRejected  int       `json:"facts_rejected"`
	EventsAccepted int       `json:"events_accepted"`
	EventsFailed   int       `json:"events_failed"`
	ImportedAt     time.Time `json:"imported_at"`
}

// chunkJSONLLines groups lines into batches of at most maxLines lines and at
// most maxBytes total bytes, counting one newline per line, preserving input
// order.
//
// A single line longer than maxBytes forms its own one-line batch. It will
// almost certainly be rejected by the server, and that is the right outcome:
// silently dropping it here would make the summary's "accepted" count a lie,
// while splitting it would produce two invalid JSON fragments.
func chunkJSONLLines(lines [][]byte, maxLines, maxBytes int) [][][]byte {
	if len(lines) == 0 {
		return nil
	}
	if maxLines < 1 {
		maxLines = 1
	}

	var (
		out     [][][]byte
		current [][]byte
		size    int
	)
	flush := func() {
		if len(current) > 0 {
			out = append(out, current)
			current, size = nil, 0
		}
	}

	for _, line := range lines {
		cost := len(line) + 1
		if len(current) > 0 && (len(current) >= maxLines || size+cost > maxBytes) {
			flush()
		}
		current = append(current, line)
		size += cost
	}
	flush()
	return out
}

// postFactsBatch sends one newline-joined batch to /api/v1/facts/batch.
//
// A second consecutive 429 counts the batch rejected and returns a nil error,
// so the caller moves on. That is deliberate: hitting a quota partway through
// a year of replay should leave the customer with the months that fit and a
// number telling them what did not, rather than an aborted run and nothing.
func postFactsBatch(ctx context.Context, client *http.Client, ingestionEndpoint, apiKey string, lines [][]byte) (accepted, rejected int, err error) {
	if len(lines) == 0 {
		return 0, 0, nil
	}
	body := bytes.Join(lines, []byte("\n"))
	endpoint := strings.TrimRight(ingestionEndpoint, "/") + "/api/v1/facts/batch"

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return 0, len(lines), fmt.Errorf("%w: %v", ErrIngestionRejected, err)
		}
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return 0, len(lines), fmt.Errorf("%w: %v", ErrIngestionRejected, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			if attempt > 0 {
				return 0, len(lines), nil
			}
			select {
			case <-ctx.Done():
				return 0, len(lines), ctx.Err()
			case <-time.After(wait):
			}
			continue
		}

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		status := resp.StatusCode
		resp.Body.Close()

		if status < 200 || status >= 300 {
			return 0, len(lines), fmt.Errorf("%w: %s (HTTP %d)", ErrIngestionRejected, ingestionMessage(raw), status)
		}
		if readErr != nil {
			return 0, len(lines), fmt.Errorf("%w: %v", ErrIngestionRejected, readErr)
		}

		var parsed struct {
			Accepted int `json:"accepted"`
			Rejected int `json:"rejected"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return 0, len(lines), fmt.Errorf("%w: unreadable response body: %v", ErrIngestionRejected, err)
		}
		return parsed.Accepted, parsed.Rejected, nil
	}
}

// postEvent sends one line to /api/v1/events. Events go one at a time because
// there is no /api/v1/events/batch, and this spec works inside the contract
// the server already offers rather than adding an endpoint for its own
// convenience.
func postEvent(ctx context.Context, client *http.Client, ingestionEndpoint, apiKey string, line []byte) error {
	endpoint := strings.TrimRight(ingestionEndpoint, "/") + "/api/v1/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(line))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIngestionRejected, err)
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIngestionRejected, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s (HTTP %d)", ErrIngestionRejected, ingestionMessage(raw), resp.StatusCode)
	}
	return nil
}

// retryAfter parses a Retry-After header given in seconds.
func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs < 0 {
		return defaultRetryAfter
	}
	return time.Duration(secs) * time.Second
}

// ingestionMessage pulls the "error" field out of the server's JSON body so a
// failed migration reports the server's own words.
func ingestionMessage(raw []byte) string {
	if len(raw) == 0 {
		return "no response body"
	}
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return strings.TrimSpace(string(raw))
}

// runMigrateImportCloud implements `gravix migrate import-cloud`.
func runMigrateImportCloud(args []string) {
	os.Exit(migrateImportCloud(context.Background(), args, os.Stdout, os.Stderr))
}

// migrateImportCloud is runMigrateImportCloud with the process exit and the
// output streams lifted out, so the exit codes in §6.1 can be asserted.
func migrateImportCloud(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate import-cloud", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data",
		"Local self-host data directory; facts are read from <data-dir>/raw/<tenant-dir-name>/request_facts and .../service_events")
	tenantDir := fs.String("tenant-dir-name", "",
		"The <tenant> path segment under --data-dir/raw/ to read from (required)")
	endpoint := fs.String("ingestion-endpoint", "http://localhost:8090", "Base URL of the target Cloud ingestion API")
	apiKey := fs.String("api-key", "", "Cloud tenant API key; falls back to GRAVIX_API_KEY env var if empty")
	batchLines := fs.Int("batch-lines", 500, "Number of JSONL lines grouped per /api/v1/facts/batch request")
	dryRun := fs.Bool("dry-run", false, "Walk and count files/lines without sending any HTTP request")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *tenantDir == "" {
		fmt.Fprintln(stderr, migrateImportUsage)
		return 2
	}

	key := *apiKey
	if key == "" {
		key = os.Getenv("GRAVIX_API_KEY")
	}
	// A dry run makes no requests, so it needs no credential. That matters:
	// "how much is there, and will it fit" is the question somebody asks
	// BEFORE they have a Cloud account.
	if key == "" && !*dryRun {
		fmt.Fprintln(stderr, ErrMissingAPIKey)
		return 2
	}

	// No overall timeout: a year of history is a long series of requests over
	// somebody else's network, and a migration that gives up halfway because a
	// deadline fired is worse than one that takes an hour.
	client := &http.Client{Timeout: 2 * time.Minute}
	summary := importSummary{ImportedAt: time.Now().UTC()}

	root := filepath.Join(*dataDir, "raw", *tenantDir)

	factFiles, err := jsonlFiles(filepath.Join(root, "request_facts"))
	if err != nil {
		fmt.Fprintf(stderr, "migrate import-cloud: %v\n", err)
		return 1
	}
	for _, path := range factFiles {
		lines, err := readJSONLLines(path)
		if err != nil {
			fmt.Fprintf(stderr, "migrate import-cloud: %v\n", err)
			return 1
		}
		summary.FilesRead++
		for _, chunk := range chunkJSONLLines(lines, *batchLines, maxBatchBytes) {
			if *dryRun {
				summary.FactsAccepted += len(chunk)
				continue
			}
			accepted, rejected, err := postFactsBatch(ctx, client, *endpoint, key, chunk)
			if err != nil {
				fmt.Fprintf(stderr, "migrate import-cloud: %v\n", err)
				return 1
			}
			summary.FactsAccepted += accepted
			summary.FactsRejected += rejected
		}
	}

	eventFiles, err := jsonlFiles(filepath.Join(root, "service_events"))
	if err != nil {
		fmt.Fprintf(stderr, "migrate import-cloud: %v\n", err)
		return 1
	}
	for _, path := range eventFiles {
		lines, err := readJSONLLines(path)
		if err != nil {
			fmt.Fprintf(stderr, "migrate import-cloud: %v\n", err)
			return 1
		}
		summary.FilesRead++
		for _, line := range lines {
			if *dryRun {
				summary.EventsAccepted++
				continue
			}
			// One bad event does not abort a migration. It is counted, and the
			// count is in the summary the customer reads.
			if err := postEvent(ctx, client, *endpoint, key, line); err != nil {
				summary.EventsFailed++
				continue
			}
			summary.EventsAccepted++
		}
	}

	out, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate import-cloud: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// jsonlFiles returns every *.jsonl file under dir, sorted lexicographically.
//
// The YYYY-MM-DD/HH segments in the key make lexicographic order chronological,
// so history is replayed in the order it happened. A missing directory is zero
// files rather than an error: an install that has only ever ingested facts has
// no service_events/ directory, and that is not a fault.
func jsonlFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	sort.Strings(out)
	return out, nil
}

// readJSONLLines returns a file's non-empty lines.
func readJSONLLines(path string) ([][]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var out [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			out = append(out, bytes.TrimSpace(line))
		}
	}
	return out, nil
}
