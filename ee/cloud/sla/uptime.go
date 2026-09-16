// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package sla

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MonthlyUptime computes the measured uptime percentage for a calendar month
// from the dogfood tenant's error_rate metric, queried through the unmodified
// core Public Metrics API.
//
// uptimePct = 100 - mean(errorRatePct) over the daily points Cube returns.
//
// A month with no data points returns 100. That is a decision worth stating:
// no traffic recorded is not evidence of downtime, and treating it as an
// outage would mean a prober that failed to start bills Cloud a credit it may
// not owe. The opposite risk — a real outage that also stopped the prober,
// scoring 100% — is real, and it is why cmd/status_page runs beside this as an
// independent display rather than reading the same numbers.
func MonthlyUptime(ctx context.Context, client *http.Client, metricsEndpoint, apiKey, service, yearMonth string) (float64, error) {
	start, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return 0, fmt.Errorf("sla: %q is not a YYYY-MM month: %w", yearMonth, err)
	}
	start = start.UTC()
	end := start.AddDate(0, 1, 0)

	q := url.Values{}
	q.Set("metric", "error_rate")
	q.Set("service", service)
	q.Set("granularity", "day")
	q.Set("from", start.Format(time.RFC3339))
	q.Set("to", end.Format(time.RFC3339))

	endpoint := strings.TrimRight(metricsEndpoint, "/") + "/api/v1/metrics?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("sla: building the metrics request: %w", err)
	}
	req.Header.Set("X-Gravix-Key", apiKey)

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("sla: querying the dogfood metrics API: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, fmt.Errorf("sla: reading the metrics response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("sla: the dogfood metrics API returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	rates, err := errorRates(raw)
	if err != nil {
		return 0, err
	}
	if len(rates) == 0 {
		return 100, nil
	}

	var sum float64
	for _, r := range rates {
		sum += r
	}
	uptime := 100 - sum/float64(len(rates))

	// Clamped, because a measured uptime outside [0, 100] is a bug somewhere
	// upstream and publishing "101.2% uptime" would destroy the credibility of
	// the whole exercise faster than any outage.
	if uptime < 0 {
		uptime = 0
	}
	if uptime > 100 {
		uptime = 100
	}
	return uptime, nil
}

// errorRates pulls the daily error_rate values out of the raw Cube.js REST
// response the Public Metrics API returns verbatim.
//
// Cube's `data` is a list of objects whose keys are cube-qualified measure
// names ("RequestMetrics.errorRate"), which vary with the model. Rather than
// hard-coding one key and silently reading zeroes when the model is renamed,
// this takes any key whose name contains "error" case-insensitively, and
// reports an error when a data point has none — a silent zero here reads as
// perfect uptime, which is the single most expensive way this could be wrong.
func errorRates(raw []byte) ([]float64, error) {
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("sla: the metrics response is not JSON: %w", err)
	}

	var out []float64
	for i, point := range payload.Data {
		v, ok := errorRateOf(point)
		if !ok {
			return nil, fmt.Errorf("sla: data point %d has no error-rate field (keys: %v); "+
				"reading it as zero would report perfect uptime", i, keysOf(point))
		}
		out = append(out, v)
	}
	return out, nil
}

func errorRateOf(point map[string]any) (float64, bool) {
	for k, v := range point {
		if !strings.Contains(strings.ToLower(k), "error") {
			continue
		}
		switch n := v.(type) {
		case float64:
			return n, true
		case string:
			// Cube returns numeric measures as strings for some drivers.
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return f, true
			}
		case nil:
			// A null measure is a day with no requests, not a day at 0% error.
			// Skipping it keeps an empty day from dragging the mean down or up.
			return 0, true
		}
	}
	return 0, false
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
