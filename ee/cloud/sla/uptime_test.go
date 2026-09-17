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
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const testDogfoodService = "gravix-cloud-dogfood"

// fakeMetrics stands in for the core Public Metrics API, which returns the raw
// Cube.js REST response verbatim.
func fakeMetrics(t *testing.T, rates []any, seen *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen.Store(r.URL.String() + "|" + r.Header.Get("X-Gravix-Key"))
		}
		data := make([]map[string]any, 0, len(rates))
		for _, v := range rates {
			data = append(data, map[string]any{
				"RequestMetrics.eventDay":   "2026-08-01T00:00:00.000",
				"RequestMetrics.errorRate":  v,
				"RequestMetrics.requestSum": 1000,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

// AC-6
func TestMonthlyUptimeComputesFromErrorRate(t *testing.T) {
	var seen atomic.Value
	// mean = (0.1 + 0.3 + 0.2 + 0.0) / 4 = 0.15 -> 99.85% uptime
	srv := fakeMetrics(t, []any{0.1, 0.3, 0.2, 0.0}, &seen)
	defer srv.Close()

	got, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "grvx_key", testDogfoodService, "2026-08")
	if err != nil {
		t.Fatalf("MonthlyUptime: %v", err)
	}
	if math.Abs(got-99.85) > 1e-9 {
		t.Errorf("uptime = %v, want 99.85", got)
	}

	// §6 step 4: one GET, granularity=day, month bounds as RFC3339 UTC.
	raw, _ := seen.Load().(string)
	query, key, _ := strings.Cut(raw, "|")
	if key != "grvx_key" {
		t.Errorf("X-Gravix-Key = %q", key)
	}
	u, err := url.Parse(query)
	if err != nil {
		t.Fatalf("parsing the request URL: %v", err)
	}
	if u.Path != "/api/v1/metrics" {
		t.Errorf("path = %q, want /api/v1/metrics", u.Path)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"metric":      "error_rate",
		"service":     testDogfoodService,
		"granularity": "day",
		"from":        "2026-08-01T00:00:00Z",
		"to":          "2026-09-01T00:00:00Z",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// TestMonthlyUptimeOnAnEmptyMonthIsOneHundred states the decision plainly: no
// traffic recorded is not evidence of downtime, and treating it as an outage
// would mean a prober that failed to start bills Cloud a credit it may not owe.
func TestMonthlyUptimeOnAnEmptyMonthIsOneHundred(t *testing.T) {
	srv := fakeMetrics(t, nil, nil)
	defer srv.Close()

	got, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08")
	if err != nil {
		t.Fatalf("MonthlyUptime: %v", err)
	}
	if got != 100 {
		t.Errorf("uptime = %v on a month with no data, want 100", got)
	}
}

// TestMonthlyUptimeReadsStringMeasures: some Cube drivers return numeric
// measures as strings, and reading those as zero would report perfect uptime.
func TestMonthlyUptimeReadsStringMeasures(t *testing.T) {
	srv := fakeMetrics(t, []any{"1.0", "3.0"}, nil)
	defer srv.Close()

	got, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08")
	if err != nil {
		t.Fatalf("MonthlyUptime: %v", err)
	}
	if math.Abs(got-98.0) > 1e-9 {
		t.Errorf("uptime = %v, want 98", got)
	}
}

// TestMonthlyUptimeRefusesAPointWithNoErrorRate is the single most expensive
// way this could be wrong. A data point whose measure was renamed and silently
// read as zero reports perfect uptime during an outage, and Cloud publishes it.
func TestMonthlyUptimeRefusesAPointWithNoErrorRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"RequestMetrics.eventDay":"2026-08-01","RequestMetrics.failureRatio":5.0}]}`)
	}))
	defer srv.Close()

	_, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08")
	if err == nil {
		t.Fatal("a data point with no error-rate field was read as zero, which reports perfect uptime")
	}
	if !strings.Contains(err.Error(), "perfect uptime") {
		t.Errorf("the error does not explain the consequence: %v", err)
	}
}

func TestMonthlyUptimeClampsOutOfRangeResults(t *testing.T) {
	t.Run("an error rate over 100", func(t *testing.T) {
		srv := fakeMetrics(t, []any{150.0}, nil)
		defer srv.Close()
		got, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08")
		if err != nil {
			t.Fatalf("MonthlyUptime: %v", err)
		}
		if got != 0 {
			t.Errorf("uptime = %v, want 0", got)
		}
	})

	t.Run("a negative error rate", func(t *testing.T) {
		srv := fakeMetrics(t, []any{-5.0}, nil)
		defer srv.Close()
		got, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08")
		if err != nil {
			t.Fatalf("MonthlyUptime: %v", err)
		}
		// Publishing "101.2% uptime" would destroy the credibility of the whole
		// exercise faster than any outage.
		if got != 100 {
			t.Errorf("uptime = %v, want 100", got)
		}
	})
}

func TestMonthlyUptimeRejectsABadMonth(t *testing.T) {
	for _, bad := range []string{"", "2026", "2026-13", "August 2026", "2026-08-01"} {
		if _, err := MonthlyUptime(context.Background(), nil, "http://x", "k", testDogfoodService, bad); err == nil {
			t.Errorf("MonthlyUptime accepted %q as a month", bad)
		}
	}
}

func TestMonthlyUptimeSurfacesAMetricsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"public metrics API requires Pro plan or above"}`)
	}))
	defer srv.Close()

	_, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08")
	if err == nil {
		t.Fatal("a 403 from the metrics API was not reported")
	}
	if !strings.Contains(err.Error(), "requires Pro plan") {
		t.Errorf("the API's own message was swallowed: %v", err)
	}
}

func TestMonthlyUptimeRejectsNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html>gateway timeout</html>")
	}))
	defer srv.Close()

	if _, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-08"); err == nil {
		t.Fatal("an HTML body was accepted as a metrics response")
	}
}

// TestMonthlyUptimeSpansTheWholeMonth checks the boundary arithmetic across a
// December, where AddDate has to roll the year.
func TestMonthlyUptimeSpansTheWholeMonth(t *testing.T) {
	var seen atomic.Value
	srv := fakeMetrics(t, []any{0.0}, &seen)
	defer srv.Close()

	if _, err := MonthlyUptime(context.Background(), srv.Client(), srv.URL, "k", testDogfoodService, "2026-12"); err != nil {
		t.Fatalf("MonthlyUptime: %v", err)
	}
	raw, _ := seen.Load().(string)
	query, _, _ := strings.Cut(raw, "|")
	u, _ := url.Parse(query)
	if got := u.Query().Get("to"); got != "2027-01-01T00:00:00Z" {
		t.Errorf("to = %q, want 2027-01-01T00:00:00Z", got)
	}
}
