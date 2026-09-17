// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the exit path out of Gravix Cloud, and it is deliberately in the
// Apache-2.0 core. Charter §2.2: "Export is the anti-lock-in guarantee; gating
// it is hostage-taking." A customer who wants to leave runs one command and
// gets a directory the free, self-hosted stack reads with no conversion step —
// because the archive the gateway already streams uses the object-store keys
// verbatim, and those keys are what a LocalStore reads.

var (
	ErrMissingTenantID  = errors.New("migrate export-cloud: --tenant-id is required")
	ErrMissingSince     = errors.New("migrate export-cloud: --since is required")
	ErrMissingToken     = errors.New("migrate export-cloud: --gateway-token is required (or set GRAVIX_GATEWAY_TOKEN)")
	ErrInvalidDateRange = errors.New("migrate export-cloud: --until must not be before --since")
	ErrExportFailed     = errors.New("migrate export-cloud: gateway export request failed")
)

// migrateExportUsage is printed for every flag error, and names the two flags
// that have no sensible default.
const migrateExportUsage = "usage: gravix migrate export-cloud --tenant-id <id> --since <YYYY-MM-DD> [flags]"

// exportWindowDays is the gateway's own cap on a single export request
// (pkg/gatewaycore: "export range cannot exceed 30 days"). The CLI works
// around it by issuing several requests rather than by asking the server to
// relax it: a limit that exists to bound one response's size is not a limit on
// how much history a customer may take with them.
const exportWindowDays = 30

// rateLimitBackoff is how long to wait after the gateway reports that another
// export is already running for this tenant. One retry, then give up — the
// concurrent-export limit is per tenant, so a second 429 means somebody else is
// mid-export and hammering it would only make both slower.
var rateLimitBackoff = 5 * time.Second

// migrationManifest is written to <out-dir>/MIGRATION_MANIFEST.json after a
// successful export. It records what was asked for, not just what arrived, so
// that a short export is visibly short rather than quietly incomplete.
type migrationManifest struct {
	TenantID        string    `json:"tenant_id"`
	GatewayEndpoint string    `json:"gateway_endpoint"`
	Since           string    `json:"since"`
	Until           string    `json:"until"`
	DataTypes       []string  `json:"data_types"`
	FilesExported   int       `json:"files_exported"`
	ExportedAt      time.Time `json:"exported_at"`
}

// dateWindow is one inclusive [Start, End] date range of at most maxDays days.
type dateWindow struct {
	Start time.Time
	End   time.Time
}

// dateWindows splits the inclusive range [since, until] into consecutive
// dateWindows of at most maxDays days each, in chronological order. The final
// window may be shorter than maxDays.
//
// Inclusive at both ends, which is where an off-by-one here would hurt: a
// window that dropped its last day would lose one day of facts per 30, and
// nothing downstream would report a gap — the rollup would simply produce no
// metrics for days that were never exported.
func dateWindows(since, until time.Time, maxDays int) []dateWindow {
	if maxDays < 1 || until.Before(since) {
		return nil
	}
	var out []dateWindow
	for start := since; !start.After(until); start = start.AddDate(0, 0, maxDays) {
		end := start.AddDate(0, 0, maxDays-1)
		if end.After(until) {
			end = until
		}
		out = append(out, dateWindow{Start: start, End: end})
	}
	return out
}

func (w dateWindow) startString() string { return w.Start.Format("2006-01-02") }
func (w dateWindow) endString() string   { return w.End.Format("2006-01-02") }

// fetchExportWindow calls POST <gatewayEndpoint>/api/gateway/export for one
// window and extracts the tar.gz response into outDir.
//
// Every tar entry's Name is the object-store key exactly as the gateway holds
// it — the handler sets hdr.Name = key and transforms nothing. Writing each
// entry at that same relative path under outDir is therefore the whole of the
// "conversion": the result is a directory a storage.LocalStore rooted at outDir
// reads with the identical keys, which is what makes the self-hosted rollup
// binary work against it unmodified.
//
// A 404 is not an error. It means that window held no data, which for a tenant
// exporting a year of history is the normal state of most windows.
func fetchExportWindow(ctx context.Context, client *http.Client, gatewayEndpoint, token, dataType string, w dateWindow, outDir string) (filesWritten int, err error) {
	body, err := json.Marshal(map[string]string{
		"start_date": w.startString(),
		"end_date":   w.endString(),
		"data_type":  dataType,
	})
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrExportFailed, err)
	}

	endpoint := strings.TrimRight(gatewayEndpoint, "/") + "/api/gateway/export"

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrExportFailed, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrExportFailed, err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			n, err := extractExportArchive(resp.Body, outDir)
			resp.Body.Close()
			if err != nil {
				return 0, fmt.Errorf("%w: %v", ErrExportFailed, err)
			}
			return n, nil

		case http.StatusNotFound:
			resp.Body.Close()
			return 0, nil

		case http.StatusTooManyRequests:
			resp.Body.Close()
			if attempt > 0 {
				return 0, fmt.Errorf("%w: another export is already in progress for this tenant", ErrExportFailed)
			}
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(rateLimitBackoff):
			}

		default:
			msg := gatewayErrorMessage(resp.Body)
			resp.Body.Close()
			return 0, fmt.Errorf("%w: %s (HTTP %d)", ErrExportFailed, msg, resp.StatusCode)
		}
	}
}

// gatewayErrorMessage pulls the "error" field out of the gateway's JSON error
// body, falling back to whatever it did send. An operator debugging a failed
// migration needs the server's own words, not "request failed".
func gatewayErrorMessage(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, 8<<10))
	if err != nil || len(raw) == 0 {
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

// extractExportArchive writes every entry of a gzip+tar stream under outDir and
// returns how many regular files it wrote.
func extractExportArchive(r io.Reader, outDir string) (int, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("reading the archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	written := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return written, nil
		}
		if err != nil {
			return written, fmt.Errorf("reading the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		dest, err := safeJoin(outDir, hdr.Name)
		if err != nil {
			return written, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return written, fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		f, err := os.Create(dest)
		if err != nil {
			return written, fmt.Errorf("creating %s: %w", dest, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return written, fmt.Errorf("writing %s: %w", dest, err)
		}
		if err := f.Close(); err != nil {
			return written, fmt.Errorf("closing %s: %w", dest, err)
		}
		written++
	}
}

// safeJoin resolves name under base and refuses anything that escapes it.
//
// The archive comes from a server, and this command's whole purpose is to run
// against a server the customer is in the process of leaving. An entry named
// "../../.ssh/authorized_keys" must land nowhere.
func safeJoin(base, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing archive entry outside the output directory: %q", name)
	}
	dest := filepath.Join(base, cleaned)

	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if absDest != absBase && !strings.HasPrefix(absDest, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing archive entry outside the output directory: %q", name)
	}
	return dest, nil
}

// runMigrateExportCloud implements `gravix migrate export-cloud`.
func runMigrateExportCloud(args []string) {
	os.Exit(migrateExportCloud(context.Background(), args, os.Stdout, os.Stderr))
}

// migrateExportCloud is runMigrateExportCloud with the process exit and the
// output streams lifted out, so the exit codes in §6.1 can be asserted by a
// test rather than described by one.
func migrateExportCloud(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate export-cloud", flag.ExitOnError)
	gatewayEndpoint := fs.String("gateway-endpoint", "http://localhost:8091", "Base URL of the Gravix Cloud gateway API")
	tenantID := fs.String("tenant-id", "", "Tenant ID to export (required)")
	gatewayToken := fs.String("gateway-token", "", "JWT bearer token for gateway auth; falls back to GRAVIX_GATEWAY_TOKEN env var if empty")
	since := fs.String("since", "", "Earliest date to export, YYYY-MM-DD (required)")
	until := fs.String("until", time.Now().UTC().Format("2006-01-02"), "Latest date to export, YYYY-MM-DD, inclusive")
	outDir := fs.String("out-dir", "./gravix-export", "Directory to extract exported raw facts and service events into")
	dataTypes := fs.String("data-types", "request_facts,service_events", "Comma-separated data types to export")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// ErrMissingTenantID and ErrMissingSince are the programmatic forms, for a
	// caller that needs to tell the two apart. What is PRINTED is the usage
	// line: somebody who forgot a flag needs the shape of the command, not a
	// restatement of which flag they left out.
	if *tenantID == "" || *since == "" {
		fmt.Fprintln(stderr, migrateExportUsage)
		return 2
	}

	token := *gatewayToken
	if token == "" {
		token = os.Getenv("GRAVIX_GATEWAY_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(stderr, ErrMissingToken)
		return 2
	}

	sinceTime, err := time.Parse("2006-01-02", *since)
	if err != nil {
		fmt.Fprintf(stderr, "migrate export-cloud: --since must be YYYY-MM-DD: %v\n", err)
		return 2
	}
	untilTime, err := time.Parse("2006-01-02", *until)
	if err != nil {
		fmt.Fprintf(stderr, "migrate export-cloud: --until must be YYYY-MM-DD: %v\n", err)
		return 2
	}
	if untilTime.Before(sinceTime) {
		fmt.Fprintln(stderr, ErrInvalidDateRange)
		return 2
	}

	var types []string
	for _, t := range strings.Split(*dataTypes, ",") {
		if t = strings.TrimSpace(t); t != "" {
			types = append(types, t)
		}
	}
	if len(types) == 0 {
		fmt.Fprintln(stderr, "migrate export-cloud: --data-types must name at least one data type")
		return 2
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "migrate export-cloud: %v\n", err)
		return 1
	}

	windows := dateWindows(sinceTime, untilTime, exportWindowDays)

	// No overall timeout. A year of history is a large number of requests over
	// somebody else's network, and a migration that gives up halfway because a
	// deadline fired is worse than one that takes an hour. Ctrl-C still works.
	client := &http.Client{Timeout: 10 * time.Minute}

	total, requests := 0, 0
	for _, dataType := range types {
		for _, w := range windows {
			requests++
			n, err := fetchExportWindow(ctx, client, *gatewayEndpoint, token, dataType, w, *outDir)
			if err != nil {
				fmt.Fprintf(stderr, "migrate export-cloud: %v\n", err)
				return 1
			}
			total += n
		}
	}

	manifest := migrationManifest{
		TenantID:        *tenantID,
		GatewayEndpoint: *gatewayEndpoint,
		Since:           *since,
		Until:           *until,
		DataTypes:       types,
		FilesExported:   total,
		ExportedAt:      time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate export-cloud: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(*outDir, "MIGRATION_MANIFEST.json"), append(raw, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "migrate export-cloud: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "exported %d files across %d requests into %s\n", total, requests, *outDir)
	return 0
}
