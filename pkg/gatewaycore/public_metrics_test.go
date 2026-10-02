// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// F-077. GET /api/v1/metrics had never returned a number. It appended
// /cubejs-api/v1/load to a Cube URL that already ends in it, sent Cube the API
// secret where Cube verifies a JWT, asked for a time dimension the model does
// not have, and asked for percentile measures that no longer exist. Its tests
// covered authentication and validation only, so none of that showed.

// TestPublicMetricsAsksCubeAsTheKeysTenant puts a Cube in front of the handler
// that checks what it is sent, and answers "Continue wait" once, as a real Cube
// does while a query runs.
func TestPublicMetricsAsksCubeAsTheKeysTenant(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, apiKey, _ := createTestTenantWithUser(t, gw)

	asked := 0
	cube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.Method != http.MethodPost || r.URL.Path != "/cubejs-api/v1/load" {
			t.Errorf("Cube was asked %s %s, want POST /cubejs-api/v1/load", r.Method, r.URL.Path)
		}
		claims, err := gw.tokens.Validate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil {
			t.Errorf("Cube could not verify the token it was sent: %v", err)
		} else if claims.TenantID != tenant.ID {
			t.Errorf("the token is for tenant %q, want the key's tenant %q", claims.TenantID, tenant.ID)
		}
		var body struct {
			Query struct {
				Measures       []string         `json:"measures"`
				TimeDimensions []map[string]any `json:"timeDimensions"`
				Filters        []map[string]any `json:"filters"`
			} `json:"query"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("the query is not JSON: %v\n%s", err, raw)
		}
		q := body.Query
		if len(q.Measures) != 1 || q.Measures[0] != "RequestMetricsMinute.errorRate" {
			t.Errorf("measures = %v, want [RequestMetricsMinute.errorRate]", q.Measures)
		}
		if len(q.TimeDimensions) != 1 || q.TimeDimensions[0]["dimension"] != "RequestMetricsMinute.bucketStart" ||
			q.TimeDimensions[0]["granularity"] != "hour" {
			t.Errorf("timeDimensions = %v, want bucketStart by hour", q.TimeDimensions)
		}
		if len(q.Filters) != 1 || q.Filters[0]["member"] != "RequestMetricsMinute.service" {
			t.Errorf("filters = %v, want only the service filter; Cube adds the tenant's from the token", q.Filters)
		}
		w.Header().Set("Content-Type", "application/json")
		if asked == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Continue wait"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"RequestMetricsMinute.bucketStart.hour": "2026-09-09T00:00:00.000", "RequestMetricsMinute.errorRate": 0.0125},
		}})
	}))
	defer cube.Close()
	gw.cubeAPIURL = cube.URL + "/cubejs-api/v1/load"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?metric=error_rate&service=api&granularity=hour", nil)
	req.Header.Set("X-Gravix-Key", apiKey)
	rr := httptest.NewRecorder()
	gw.handlePublicMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if asked != 2 {
		t.Errorf("Cube was asked %d time(s), want 2: once answered Continue wait, then again", asked)
	}
	var got struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got.Data) != 1 ||
		got.Data[0]["RequestMetricsMinute.errorRate"] != 0.0125 {
		t.Errorf("body = %s, want Cube's row", rr.Body.String())
	}
}

// TestPublicMetricsPercentileMergesSketches asks for p95 with Cube unreachable.
// A percentile over a window has no correct answer in Cube (GRVX-808), so it
// must come from the sketches, in /api/v1/percentile's shape.
func TestPublicMetricsPercentileMergesSketches(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(77)
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes, true))
	gw.cubeAPIURL = "http://127.0.0.1:1/cubejs-api/v1/load"

	url := "/api/v1/metrics?metric=p95_latency&granularity=hour" +
		"&from=" + percentileDay.Format(time.RFC3339) +
		"&to=" + percentileDay.Add(time.Hour).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("X-Gravix-Key", apiKey)
	rr := httptest.NewRecorder()
	gw.handlePublicMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var got percentileResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a percentile response: %v\n%s", err, rr.Body.String())
	}
	if got.Quantile != 0.95 || len(got.Buckets) != 1 || got.Buckets[0].Value <= 0 || got.Buckets[0].SketchesMerged != 60 {
		t.Errorf("response = %+v, want one hourly p95 merged from 60 sketches", got)
	}
}
