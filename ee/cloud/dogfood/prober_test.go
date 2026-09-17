// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package dogfood

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testService = "gravix-cloud-dogfood"

// freeAddr reserves and releases a port, giving an address nothing is
// listening on. Nothing binds it afterwards, so a probe against it fails to
// connect — which is the condition under test, not a flaky one.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// AC-1 — a refused connection is a measurement, not an error. Reporting it as
// an error return would lose exactly the observation the SLA is computed from.
func TestProbeReturnsZeroOnNetworkError(t *testing.T) {
	check := HealthCheck{Name: "gateway-live", URL: "http://" + freeAddr(t) + "/live"}

	got := Probe(context.Background(), &http.Client{Timeout: time.Second}, check)
	if got.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", got.StatusCode)
	}
	if got.Check != check {
		t.Errorf("Check = %+v, want %+v", got.Check, check)
	}
	if got.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero; the observation has no time")
	}
	if got.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d", got.LatencyMs)
	}
}

func TestProbeRecordsTheStatusAndLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	}))
	defer srv.Close()

	got := Probe(context.Background(), srv.Client(), HealthCheck{Name: "gateway-live", URL: srv.URL})
	if got.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", got.StatusCode)
	}
	if got.LatencyMs < 20 {
		t.Errorf("LatencyMs = %d, want at least 20", got.LatencyMs)
	}
}

// TestProbeCapsLatencyAtTheSLATimeout: docs/sla.md §1 defines uptime in terms
// of a response "within 5 seconds", so a client whose own deadline fired did
// not observe a 5.3-second response — it observed a timeout.
func TestProbeCapsLatencyAtTheSLATimeout(t *testing.T) {
	if got := elapsedMs(time.Now().Add(-30 * time.Second)); got != probeTimeout.Milliseconds() {
		t.Errorf("elapsedMs for a 30s wait = %d, want the %dms cap", got, probeTimeout.Milliseconds())
	}
}

// TestProbeTimeoutMatchesTheSLADocument pins the constant against the contract
// it is meant to measure. A prober with a different timeout is measuring
// something other than the thing Cloud promises.
func TestProbeTimeoutMatchesTheSLADocument(t *testing.T) {
	raw, err := readRepoFile("docs/sla.md")
	if err != nil {
		t.Fatalf("reading docs/sla.md: %v", err)
	}
	if !strings.Contains(raw, "within 5 seconds") {
		t.Error("docs/sla.md no longer defines uptime as a response within 5 seconds; " +
			"the prober's timeout was chosen to match that sentence")
	}
	if probeTimeout != 5*time.Second {
		t.Errorf("probeTimeout = %v, want 5s to match docs/sla.md §1", probeTimeout)
	}
}

func TestProbeSurvivesAnUnparseableURL(t *testing.T) {
	got := Probe(context.Background(), nil, HealthCheck{Name: "bad", URL: "://not a url"})
	if got.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", got.StatusCode)
	}
}

// AC-2
func TestReportFactMapsNetworkErrorToUnhealthy(t *testing.T) {
	body := ReportFact(ProbeResult{
		Check:     HealthCheck{Name: "gateway-live"},
		CheckedAt: time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC),
	}, testService)

	got := decodeFact(t, body)
	if got.StatusCode != UnhealthyStatus {
		t.Errorf("status_code = %d, want %d", got.StatusCode, UnhealthyStatus)
	}
}

// AC-3
func TestReportFactMapsHealthyProbe(t *testing.T) {
	body := ReportFact(ProbeResult{
		Check:      HealthCheck{Name: "gateway-live"},
		StatusCode: http.StatusOK,
		LatencyMs:  12,
		CheckedAt:  time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC),
	}, testService)

	got := decodeFact(t, body)
	if got.StatusCode != HealthyStatus {
		t.Errorf("status_code = %d, want 200", got.StatusCode)
	}
	if got.Service != testService {
		t.Errorf("service = %q, want %q", got.Service, testService)
	}
	if got.LatencyMs != 12 {
		t.Errorf("latency_ms = %d, want 12", got.LatencyMs)
	}
	if got.EventTime != "2026-09-16T08:00:00Z" {
		t.Errorf("event_time = %q, want the probe's own time", got.EventTime)
	}
	if got.EventID == "" {
		t.Error("event_id is empty")
	}
}

// TestReportFactStatusMapping covers the whole range in one place, because the
// boundary between "healthy" and "counts against uptime" is the thing the
// credit is computed from.
func TestReportFactStatusMapping(t *testing.T) {
	cases := []struct {
		probe int
		want  int
	}{
		{0, UnhealthyStatus},   // no response at all
		{200, HealthyStatus},   //
		{204, HealthyStatus},   //
		{301, HealthyStatus},   // a redirect from a health endpoint is odd but reachable
		{399, HealthyStatus},   //
		{404, UnhealthyStatus}, // a broken deployment, not a client error
		{429, UnhealthyStatus}, //
		{500, UnhealthyStatus}, //
		{503, UnhealthyStatus}, //
	}
	for _, tc := range cases {
		got := decodeFact(t, ReportFact(ProbeResult{StatusCode: tc.probe}, testService))
		if got.StatusCode != tc.want {
			t.Errorf("a probe status of %d recorded status_code %d, want %d", tc.probe, got.StatusCode, tc.want)
		}
	}
}

// TestReportFactUsesOnePathTemplate pins the non-goal: docs/04-non-goals.md §5
// forbids high-cardinality dimensions, and a path template per check would
// grow a dimension with every endpoint Cloud adds.
func TestReportFactUsesOnePathTemplate(t *testing.T) {
	for _, name := range []string{"gateway-live", "gateway-ready", "ingestion-live", "cube-ready"} {
		got := decodeFact(t, ReportFact(ProbeResult{
			Check: HealthCheck{Name: name}, StatusCode: 200,
		}, testService))
		if got.PathTemplate != HealthPathTemplate {
			t.Errorf("check %q recorded path_template %q, want the single %q",
				name, got.PathTemplate, HealthPathTemplate)
		}
	}
}

// TestReportFactIsAcceptedByTheRealValidator is the assertion that matters:
// these facts go through the same public API as a customer's, so they must
// satisfy the same schema. A prober whose facts are rejected records nothing,
// and "no data" scores as 100% uptime.
func TestReportFactIsAcceptedByTheRealValidator(t *testing.T) {
	for _, status := range []int{0, 200, 503} {
		body := ReportFact(ProbeResult{
			Check:      HealthCheck{Name: "gateway-live"},
			StatusCode: status,
			LatencyMs:  7,
			CheckedAt:  time.Now().UTC(),
		}, testService)

		if err := validateAgainstSchemas(body); err != nil {
			t.Errorf("a probe fact with status %d was rejected by the real validator: %v", status, err)
		}
	}
}

func TestReportFactDefaultsAZeroTime(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := decodeFact(t, ReportFact(ProbeResult{StatusCode: 200}, testService))
	parsed, err := time.Parse(time.RFC3339Nano, got.EventTime)
	if err != nil {
		t.Fatalf("event_time %q: %v", got.EventTime, err)
	}
	if parsed.Before(before) {
		t.Errorf("event_time = %v, want roughly now", parsed)
	}
}

func TestReportFactClampsANegativeLatency(t *testing.T) {
	got := decodeFact(t, ReportFact(ProbeResult{StatusCode: 200, LatencyMs: -5}, testService))
	if got.LatencyMs != 0 {
		t.Errorf("latency_ms = %d, want 0; a negative latency fails validation", got.LatencyMs)
	}
}

func TestSendFact(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotKey, gotPath atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKey.Store(r.Header.Get("X-API-Key"))
			gotPath.Store(r.URL.Path)
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		err := SendFact(context.Background(), srv.Client(), srv.URL, "grvx_key",
			ProbeResult{Check: HealthCheck{Name: "gateway-live"}, StatusCode: 200}, testService)
		if err != nil {
			t.Fatalf("SendFact: %v", err)
		}
		if got, _ := gotKey.Load().(string); got != "grvx_key" {
			t.Errorf("X-API-Key = %q", got)
		}
		// The public endpoint, not a private one. Cloud's own monitoring uses
		// the API it sells.
		if got, _ := gotPath.Load().(string); got != "/api/v1/facts" {
			t.Errorf("path = %q, want /api/v1/facts", got)
		}
	})

	t.Run("a non-201 carries the server's own words", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid or missing X-API-Key header"}`))
		}))
		defer srv.Close()

		err := SendFact(context.Background(), srv.Client(), srv.URL, "bad",
			ProbeResult{Check: HealthCheck{Name: "gateway-live"}}, testService)
		if err == nil {
			t.Fatal("SendFact returned no error on a 401")
		}
		if !strings.Contains(err.Error(), "invalid or missing X-API-Key header") {
			t.Errorf("the server's message was swallowed: %v", err)
		}
	})
}

// TestParseTargetsMatchesStatusPageFormat: the human-facing page and the fact
// stream are configured from one list, so they cannot drift apart.
func TestParseTargetsMatchesStatusPageFormat(t *testing.T) {
	got, err := ParseTargets("gateway-live=http://gw/live, ingestion-live=http://ing/live ,")
	if err != nil {
		t.Fatalf("ParseTargets: %v", err)
	}
	want := []HealthCheck{
		{Name: "gateway-live", URL: "http://gw/live"},
		{Name: "ingestion-live", URL: "http://ing/live"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d targets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if _, err := ParseTargets(""); err != nil {
		t.Errorf("an empty spec should parse to zero targets, not an error: %v", err)
	}
	for _, bad := range []string{"noequals", "=http://x", "name="} {
		if _, err := ParseTargets(bad); err == nil {
			t.Errorf("ParseTargets(%q) accepted a malformed pair", bad)
		}
	}
}

// --- helpers ----------------------------------------------------------------

type factJSON struct {
	EventID      string `json:"event_id"`
	EventTime    string `json:"event_time"`
	Service      string `json:"service"`
	Method       string `json:"method"`
	PathTemplate string `json:"path_template"`
	StatusCode   int    `json:"status_code"`
	LatencyMs    int64  `json:"latency_ms"`
}

func decodeFact(t *testing.T, body []byte) factJSON {
	t.Helper()
	if body == nil {
		t.Fatal("ReportFact returned nil")
	}
	var got factJSON
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the fact is not valid JSON: %v\n%s", err, body)
	}
	return got
}
