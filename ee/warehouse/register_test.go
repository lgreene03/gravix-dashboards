// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

func testSurface(t *testing.T, state degrade.State) (http.Handler, *surface) {
	t.Helper()
	s := &surface{state: func() degrade.State { return state }}
	return http.StripPrefix("/ee/warehouse", s.Handler()), s
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, rdr))
	return rec
}

func TestSurfaceRegistersWithTheExtensionPoint(t *testing.T) {
	ext, ok := extpoint.Mounted("/ee/warehouse/")
	if !ok {
		t.Fatal("nothing is registered at /ee/warehouse/")
	}
	if ext.Name() != "warehouse" {
		t.Errorf("Name() = %q; want warehouse", ext.Name())
	}
}

// With nothing configured, the status says so and names the free alternative.
// A paid feature's own status endpoint pointing at the free path is the charter
// working rather than a marketing decision.
func TestStatusNamesTheFreeAlternativeWhenUnconfigured(t *testing.T) {
	h, _ := testSurface(t, degrade.StateLicensed)
	rec := do(t, h, http.MethodGet, "/ee/warehouse/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	var body struct {
		Configured bool   `json:"configured"`
		Note       string `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Configured {
		t.Error("an unconfigured sync reports itself configured")
	}
	if !strings.Contains(body.Note, "gravix export") {
		t.Errorf("the note does not name the free alternative: %q", body.Note)
	}
}

// Status answers in every licence state, including read-only: an operator whose
// licence lapsed still needs to see whether their warehouse is in step.
func TestStatusAnswersInEveryState(t *testing.T) {
	for _, state := range []degrade.State{degrade.StateLicensed, degrade.StateGrace, degrade.StateReadOnly, degrade.StateAbsent} {
		h, s := testSurface(t, state)
		s.Record(&SyncReport{Target: "snowflake", Inserted: 3, Reconciled: 1, RowsLoaded: 900})

		rec := do(t, h, http.MethodGet, "/ee/warehouse/status", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: GET status = %d; reads stay readable", state, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"reconciled":1`) {
			t.Errorf("%s: the last run is not reported: %s", state, rec.Body.String())
		}
	}
}

// Changing which warehouse is synced to is configuration, and stops when the
// licence does. A sync already scheduled is not affected by this endpoint at all.
func TestConfigIsGuarded(t *testing.T) {
	cases := []struct {
		state degrade.State
		want  int
	}{
		{degrade.StateLicensed, http.StatusOK},
		{degrade.StateGrace, http.StatusOK},
		{degrade.StateReadOnly, http.StatusPaymentRequired},
		{degrade.StateAbsent, http.StatusPaymentRequired},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			h, s := testSurface(t, tc.state)
			rec := do(t, h, http.MethodPost, "/ee/warehouse/config", map[string]string{"target": "snowflake"})
			if rec.Code != tc.want {
				t.Fatalf("POST config in %s = %d; want %d (%s)", tc.state, rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusPaymentRequired {
				var body degrade.ExpiredResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !body.CoreUnaffected {
					t.Error("core_unaffected is false")
				}
				if s.configured {
					t.Error("a refused configuration took effect anyway")
				}
			}
		})
	}
}

func TestSurfaceRejectsBadRequests(t *testing.T) {
	h, _ := testSurface(t, degrade.StateLicensed)

	if rec := do(t, h, http.MethodPost, "/ee/warehouse/status", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d; want 405", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/ee/warehouse/config", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET config = %d; want 405", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/ee/warehouse/config", map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Errorf("POST config with no target = %d; want 400", rec.Code)
	}
}

func TestSurfaceWithNoStateFunctionFailsClosed(t *testing.T) {
	s := &surface{}
	if got := s.currentState(); got != degrade.StateAbsent {
		t.Errorf("currentState() = %q; want absent", got)
	}
}

func TestLiveStateReadsTheEnvironment(t *testing.T) {
	t.Setenv("GRAVIX_LICENSE", "")
	t.Setenv("GRAVIX_LICENSE_FILE", "")
	if got := liveState(); got != degrade.StateAbsent {
		t.Errorf("liveState() = %q with no licence; want absent", got)
	}
}

// The HTTP surface never runs a sync. A warehouse outage reaching a handler on
// the gateway is the shape of coupling this package is written to avoid.
func TestSurfaceNeverRunsASync(t *testing.T) {
	src := readFile(t, "register.go")
	for _, banned := range []string{"Sync(", "Plan(", "Reconcile("} {
		if strings.Contains(src, banned) {
			t.Errorf("register.go calls %s; the gateway must not be where a sync happens", banned)
		}
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
