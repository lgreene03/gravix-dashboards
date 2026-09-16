// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package dogfood implements Gravix Cloud's self-monitoring: each health check
// performed against Cloud's own infrastructure is reported as an ordinary
// RequestFact through the public ingestion API, into a Gravix tenant Cloud
// operates on itself.
//
// This is what makes Loop L10 ("Gravix monitors Gravix") a mechanism rather
// than a sentence. Cloud's published uptime number comes out of a Gravix
// error_rate query over these facts — not from Prometheus, not from a
// third-party status tool. If Gravix's aggregation is wrong, Cloud's own SLA
// number is wrong with it, and the people who would have to pay the credit are
// the people who would have to fix it.
//
// Nothing here is a capability a self-hoster lacks. cmd/status_page is in the
// Apache-2.0 core and watches whatever endpoints you point it at. What lives
// here is the part that only exists because there is a contract: a tenant Cloud
// runs on itself, and a credit owed to somebody else.
package dogfood

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// probeTimeout matches docs/sla.md §1, which defines uptime in terms of a
// response "within 5 seconds". A prober with a different timeout would be
// measuring something other than the thing the contract promises.
const probeTimeout = 5 * time.Second

// HealthCheck names one target the prober polls.
type HealthCheck struct {
	Name string // e.g. "gateway-live", "gateway-ready", "ingestion-live"
	URL  string
}

// ProbeResult is the outcome of one HTTP GET against a HealthCheck's URL.
type ProbeResult struct {
	Check      HealthCheck
	StatusCode int   // 0 when the request could not complete (timeout, connection error)
	LatencyMs  int64 // elapsed time in milliseconds, capped at the probe timeout
	CheckedAt  time.Time
}

// Probe performs one GET against check.URL with a 5-second timeout.
//
// A network error is StatusCode 0 rather than an error return, and that is the
// point: an endpoint that refuses the connection is down, which is a
// measurement, not a failure of the prober. Reporting it as an error would
// lose exactly the observation the SLA is computed from.
func Probe(ctx context.Context, client *http.Client, check HealthCheck) ProbeResult {
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	start := time.Now()
	res := ProbeResult{Check: check, CheckedAt: start.UTC()}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.URL, nil)
	if err != nil {
		res.LatencyMs = elapsedMs(start)
		return res
	}

	resp, err := client.Do(req)
	if err != nil {
		res.LatencyMs = elapsedMs(start)
		return res
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()

	res.StatusCode = resp.StatusCode
	res.LatencyMs = elapsedMs(start)
	return res
}

// elapsedMs returns milliseconds since start, capped at the probe timeout. The
// cap keeps latency_ms honest: a client whose own deadline fired at 5s did not
// observe a 5.3-second response, it observed a timeout.
func elapsedMs(start time.Time) int64 {
	d := time.Since(start)
	if d > probeTimeout {
		d = probeTimeout
	}
	return d.Milliseconds()
}

// UnhealthyStatus is the status_code recorded for a failed check.
//
// The probe's own status code is not recorded verbatim. A RequestFact's
// status_code must be 100-599, and a network error has no status at all; more
// importantly, error_rate counts 5xx, so mapping every failure to one 5xx value
// is what makes "100 - error_rate" mean "uptime". A 404 from a health endpoint
// is a broken deployment, not a client error, and it belongs on the same side
// of that line as a refused connection.
const (
	UnhealthyStatus = 503
	HealthyStatus   = 200
)

// HealthPathTemplate is the path template every probe fact is recorded under.
//
// One template, with the check name as a {check} placeholder rather than
// interpolated: docs/04-non-goals.md §5 forbids high-cardinality dimensions,
// and a path template per check would grow a dimension with every endpoint
// Cloud adds. The service dimension carries which deployment; the fact stream
// answers "is Cloud up", which is one question.
const HealthPathTemplate = "/health/{check}"

// ReportFact builds the JSON body schemas.ParseRequestFact expects for one
// ProbeResult.
func ReportFact(result ProbeResult, service string) []byte {
	status := HealthyStatus
	if result.StatusCode == 0 || result.StatusCode >= 400 {
		status = UnhealthyStatus
	}

	checkedAt := result.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	latency := result.LatencyMs
	if latency < 0 {
		latency = 0
	}

	// UUIDv7, not v4. ValidateRequestFact rejects a v4 with "event_id must be
	// UUIDv7", and a rejected probe fact is not an error anybody sees: it is a
	// month with no data points, which MonthlyUptime scores as 100%. A version
	// mistake here would have published a perfect SLA report through an outage.
	id, err := uuid.NewV7()
	if err != nil {
		return nil
	}

	body, err := json.Marshal(map[string]any{
		"event_id":      id.String(),
		"event_time":    checkedAt.UTC().Format(time.RFC3339Nano),
		"service":       service,
		"method":        http.MethodGet,
		"path_template": HealthPathTemplate,
		"status_code":   status,
		"latency_ms":    latency,
	})
	if err != nil {
		// Every field is a string or an int; there is no input that can make
		// this fail. Returning nil rather than panicking keeps a monitoring
		// process from being the thing that takes itself down.
		return nil
	}
	return body
}

// SendFact POSTs a ProbeResult's fact to the public ingestion API.
func SendFact(ctx context.Context, client *http.Client, ingestionEndpoint, apiKey string, result ProbeResult, service string) error {
	body := ReportFact(result, service)
	if body == nil {
		return fmt.Errorf("dogfood: could not build a fact for %s", result.Check.Name)
	}

	endpoint := strings.TrimRight(ingestionEndpoint, "/") + "/api/v1/facts"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("dogfood: building the request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dogfood: reporting %s: %w", result.Check.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("dogfood: reporting %s: ingestion returned %d: %s",
			result.Check.Name, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// ParseTargets reads the "name=url,name=url" form cmd/status_page already uses
// for STATUS_ENDPOINTS, so the human-facing page and the fact stream are
// configured from one list rather than two that can drift apart.
func ParseTargets(spec string) ([]HealthCheck, error) {
	var out []HealthCheck
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, url, ok := strings.Cut(pair, "=")
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if !ok || name == "" || url == "" {
			return nil, fmt.Errorf("dogfood: %q is not a name=url pair", pair)
		}
		out = append(out, HealthCheck{Name: name, URL: url})
	}
	return out, nil
}
