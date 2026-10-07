// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

// testSurface mounts the console over a fixed series and a fixed licence state,
// so no test depends on the process's environment or on a warehouse on disk.
func testSurface(t *testing.T, state degrade.State, s Series) http.Handler {
	t.Helper()
	sf := surface{
		state: func() degrade.State { return state },
		source: func(*http.Request) (Source, error) {
			return fixed(s), nil
		},
	}
	return http.StripPrefix("/ee/intelligence", sf.Handler())
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSurfaceRegistersWithTheExtensionPoint(t *testing.T) {
	ext, ok := extpoint.Mounted("/ee/intelligence/")
	if !ok {
		t.Fatal("nothing is registered at /ee/intelligence/")
	}
	if ext.Name() != "intelligence" {
		t.Errorf("Name() = %q; want intelligence", ext.Name())
	}
}

func TestForecastEndpoint(t *testing.T) {
	h := testSurface(t, degrade.StateLicensed, realShapes[0].build(31))

	rec := get(t, h, "/ee/intelligence/forecast?metric=p95_latency_ms@1&service=checkout&horizon=6h")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET forecast = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Predictions []Prediction `json:"predictions"`
		Explanation *Explanation `json:"explanation"`
		Explained   string       `json:"explained"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Predictions) != 6 {
		t.Errorf("%d predictions for a 6h horizon; want 6", len(body.Predictions))
	}
	for _, p := range body.Predictions {
		if !p.IsPrediction {
			t.Error("a prediction crossed the wire without is_prediction set")
		}
	}
	if body.Explanation == nil || body.Explained == "" {
		t.Error("the response carries no explanation")
	}
}

// A refusal to forecast is 422: the request was well formed and the answer is
// "no, and here is why". A 500 would send somebody looking for a bug.
func TestRefusalIs422NotAnError(t *testing.T) {
	short := shape{name: "too_short", days: 2, base: 100, amp: 20, noise: 1}.build(32)
	h := testSurface(t, degrade.StateLicensed, short)

	rec := get(t, h, "/ee/intelligence/forecast?metric=m&service=checkout&horizon=3h")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a refusal answered %d; want 422 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error   string `json:"error"`
		Refused bool   `json:"refused"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Refused {
		t.Error("the response does not mark itself a refusal")
	}
	if !strings.Contains(body.Error, "need at least") {
		t.Errorf("the refusal does not say what is missing: %q", body.Error)
	}
}

func TestCapacityEndpoint(t *testing.T) {
	s := shape{name: "disk_used_pct", days: 21, base: 40, amp: 2, slope: 0.02, noise: 0.5}.build(33)
	h := testSurface(t, degrade.StateLicensed, s)

	rec := get(t, h, "/ee/intelligence/capacity?metric=disk_used_pct@1&service=checkout&threshold=90")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET capacity = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Projection CapacityProjection `json:"projection"`
		Summary    string             `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.Summary, "between ") {
		t.Errorf("summary = %q; want a range", body.Summary)
	}
	if body.Projection.Latest.Before(body.Projection.Earliest) {
		t.Error("the projection's range is inverted")
	}

	rec = get(t, h, "/ee/intelligence/capacity?service=checkout&threshold=notanumber")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a non-numeric threshold = %d; want 400", rec.Code)
	}
}

// Forecasting is a read, so read-only keeps it. An install with no licence at
// all never had it, so nothing is being taken away (charter §7.3 Q4).
func TestEntitlementByLicenceState(t *testing.T) {
	s := realShapes[0].build(34)
	cases := []struct {
		state degrade.State
		want  int
	}{
		{degrade.StateLicensed, http.StatusOK},
		{degrade.StateGrace, http.StatusOK},
		{degrade.StateReadOnly, http.StatusOK},
		{degrade.StateAbsent, http.StatusPaymentRequired},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			h := testSurface(t, tc.state, s)
			rec := get(t, h, "/ee/intelligence/forecast?metric=m&service=checkout&horizon=4h")
			if rec.Code != tc.want {
				t.Errorf("forecast in %s = %d; want %d (%s)", tc.state, rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusPaymentRequired {
				var body degrade.ExpiredResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !body.CoreUnaffected {
					t.Error("core_unaffected is false")
				}
			}
		})
	}
}

func TestSurfaceRejectsBadRequests(t *testing.T) {
	h := testSurface(t, degrade.StateLicensed, realShapes[0].build(35))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ee/intelligence/forecast", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST forecast = %d; want 405", rec.Code)
	}

	rec = get(t, h, "/ee/intelligence/forecast?service=checkout&horizon=notaduration")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a bad horizon = %d; want 400", rec.Code)
	}
}

// Without a service there is nothing to forecast, and the message says why
// rather than returning an empty answer.
func TestSourceRequiresAService(t *testing.T) {
	sf := surface{state: func() degrade.State { return degrade.StateLicensed }}
	_, err := sf.resolveSource(asTenant(httptest.NewRequest(http.MethodGet, "/forecast?metric=m", nil), "acme"))
	if err == nil {
		t.Fatal("a request with no service produced a source")
	}
	if !strings.Contains(err.Error(), "forecast of nothing in particular") {
		t.Errorf("err = %v; want it to say why a service is required", err)
	}
}

// asTenant is what the gateway's guard leaves on a request it lets through.
func asTenant(r *http.Request, tenantID string) *http.Request {
	return r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{TenantID: tenantID, Role: auth.RoleAdmin}))
}

// F-080. The tenant came from a query parameter, so any caller could read any
// tenant's warehouse by naming it. It is the caller's, from the verified token,
// and a request with no caller reads nothing.
func TestSourceIsTheCallersTenant(t *testing.T) {
	t.Setenv("GRAVIX_WAREHOUSE_DIR", t.TempDir())
	sf := surface{state: func() degrade.State { return degrade.StateLicensed }}

	src, err := sf.resolveSource(asTenant(httptest.NewRequest(http.MethodGet, "/forecast?service=checkout", nil), "acme"))
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if got := src.(WarehouseSource).TenantID; got != "acme" {
		t.Errorf("source reads tenant %q; want the caller's, acme", got)
	}

	if _, err := sf.resolveSource(asTenant(httptest.NewRequest(http.MethodGet, "/forecast?service=checkout&tenant=other", nil), "acme")); !errors.Is(err, errTenantParam) {
		t.Errorf("naming another tenant: err = %v; want errTenantParam", err)
	}
	if _, err := sf.resolveSource(httptest.NewRequest(http.MethodGet, "/forecast?service=checkout&tenant=acme", nil)); !errors.Is(err, errNoCaller) {
		t.Errorf("no caller: err = %v; want errNoCaller", err)
	}

	h := http.StripPrefix("/ee/intelligence", sf.Handler())
	for path, want := range map[string]int{
		"/ee/intelligence/forecast?service=checkout":              http.StatusUnauthorized,
		"/ee/intelligence/capacity?service=checkout&threshold=90": http.StatusUnauthorized,
	} {
		if rec := get(t, h, path); rec.Code != want {
			t.Errorf("GET %s with no caller = %d; want %d", path, rec.Code, want)
		}
	}
}

func TestSurfaceWithNoStateFunctionFailsClosed(t *testing.T) {
	sf := surface{}
	if got := sf.currentState(); got != degrade.StateAbsent {
		t.Errorf("currentState() = %q; want absent", got)
	}
	if sf.entitled() {
		t.Error("a surface with no licence state considers itself entitled")
	}
}

func TestLiveStateReadsTheEnvironment(t *testing.T) {
	t.Setenv("GRAVIX_LICENSE", "")
	t.Setenv("GRAVIX_LICENSE_FILE", "")
	if got := liveState(); got != degrade.StateAbsent {
		t.Errorf("liveState() = %q with no licence; want absent", got)
	}
}

// The forecast path does no I/O beyond reading the warehouse, so a cancelled
// request does not leave anything running.
func TestForecastRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Forecast(ctx, cancelledSource{}, "m", time.Hour)
	if err == nil {
		t.Error("a cancelled context produced a forecast")
	}
}

type cancelledSource struct{}

func (cancelledSource) History(ctx context.Context, _ string, _ time.Duration) (Series, error) {
	return Series{}, ctx.Err()
}
