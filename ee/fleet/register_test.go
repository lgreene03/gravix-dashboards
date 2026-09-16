// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

// testConsole builds a console over a fresh registry at a fixed licence state,
// so the tests never depend on the process's own environment.
func testConsole(t *testing.T, state degrade.State) (http.Handler, *Registry) {
	t.Helper()
	r := seeded(t)
	c := console{registry: r, state: func() degrade.State { return state }}
	return http.StripPrefix("/ee/fleet", c.Handler()), r
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

// The console registers itself with the core extension point, at the prefix
// core will mount it on. This is the whole of the wiring between the two.
func TestConsoleRegistersWithTheExtensionPoint(t *testing.T) {
	ext, ok := extpoint.Mounted("/ee/fleet/")
	if !ok {
		t.Fatal("nothing is registered at /ee/fleet/; importing this package should have registered the console")
	}
	if ext.Name() != "fleet" {
		t.Errorf("Name() = %q; want fleet", ext.Name())
	}
	var found bool
	for _, e := range extpoint.Registered() {
		if e.Name() == "fleet" {
			found = true
		}
	}
	if !found {
		t.Error("the console does not appear in extpoint.Registered()")
	}
}

func TestConsoleListsAndRegistersInstalls(t *testing.T) {
	h, _ := testConsole(t, degrade.StateLicensed)

	rec := do(t, h, http.MethodGet, "/ee/fleet/installs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET installs = %d", rec.Code)
	}
	var listed struct {
		Installs []Install `json:"installs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Installs) != 2 {
		t.Errorf("listed %d installs; want 2", len(listed.Installs))
	}

	rec = do(t, h, http.MethodPost, "/ee/fleet/installs", Install{ID: "edge-03", Name: "Edge, Oslo"})
	if rec.Code != http.StatusCreated {
		t.Errorf("POST installs = %d; want 201 (%s)", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodDelete, "/ee/fleet/installs", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE installs = %d; want 405", rec.Code)
	}
}

// A self-report is accepted whatever the console's licence says, and an install
// the console has never heard of is named rather than silently dropped.
func TestConsoleAcceptsReportsInEveryState(t *testing.T) {
	for _, state := range []degrade.State{degrade.StateLicensed, degrade.StateGrace, degrade.StateReadOnly, degrade.StateAbsent} {
		h, reg := testConsole(t, state)
		batch := []Report{
			SelfReport("edge-01", "v3.0.0", "cfg", true, now),
			SelfReport("who-is-this", "v3.0.0", "cfg", true, now),
		}
		rec := do(t, h, http.MethodPost, "/ee/fleet/report", batch)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: POST report = %d (%s)", state, rec.Code, rec.Body.String())
		}
		var got struct {
			Accepted int      `json:"accepted"`
			Unknown  []string `json:"unknown"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Accepted != 1 {
			t.Errorf("%s: accepted %d reports; want 1", state, got.Accepted)
		}
		if len(got.Unknown) != 1 || got.Unknown[0] != "who-is-this" {
			t.Errorf("%s: unknown = %v; want the unregistered id named", state, got.Unknown)
		}
		if in, _ := reg.Get("edge-01"); in.Version != "v3.0.0" {
			t.Errorf("%s: the report was not recorded", state)
		}
	}
}

// A refused console mutation answers 402 with the degrade payload, so an
// operator can tell a licence state from a bad request.
func TestConsoleRefusalIs402WithTheDegradePayload(t *testing.T) {
	h, _ := testConsole(t, degrade.StateReadOnly)

	rec := do(t, h, http.MethodPost, "/ee/fleet/installs", Install{ID: "edge-03", Name: "Edge, Oslo"})
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("POST installs in read-only = %d; want 402 (%s)", rec.Code, rec.Body.String())
	}
	var body degrade.ExpiredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.CoreUnaffected {
		t.Error("core_unaffected is false")
	}
	if body.ExportEndpoint != degrade.ExportEndpoint {
		t.Errorf("export_endpoint = %q", body.ExportEndpoint)
	}

	// A bad request is still a bad request, not a billing message.
	rec = do(t, h, http.MethodGet, "/ee/fleet/installs", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("reading the fleet in read-only = %d; configuration stays readable", rec.Code)
	}
}

func TestConsoleProposalShowsWhatItWouldChange(t *testing.T) {
	h, reg := testConsole(t, degrade.StateLicensed)
	if err := reg.Pin(context.Background(), degrade.StateLicensed, "edge-01", pinnedRetention()); err != nil {
		t.Fatalf("pin: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/ee/fleet/proposals", Proposal{
		ID: "p-http", InstallID: "edge-01", Reason: "standardising retention",
		Changes: map[string]any{"retention_days": 30, "sample_rate": 0.5},
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST proposals = %d (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Disposition string         `json:"disposition"`
		WouldChange map[string]any `json:"would_change"`
		Pinned      []string       `json:"pinned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Disposition != string(DispositionPending) {
		t.Errorf("disposition = %q; a new proposal awaits the operator", got.Disposition)
	}
	if _, ok := got.WouldChange["retention_days"]; ok {
		t.Error("the response says it would change a pinned key")
	}
	if len(got.Pinned) != 1 {
		t.Errorf("pinned = %v; want the one pinned key explained", got.Pinned)
	}
}

func TestConsoleRejectsMalformedBodies(t *testing.T) {
	h, _ := testConsole(t, degrade.StateLicensed)
	for _, path := range []string{"/ee/fleet/installs", "/ee/fleet/report", "/ee/fleet/proposals"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("{not json"))))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s with a malformed body = %d; want 400", path, rec.Code)
		}
	}
}

// A console built with no state function refuses rather than assuming a licence.
func TestConsoleWithNoStateFunctionFailsClosed(t *testing.T) {
	c := console{registry: NewRegistry()}
	if got := c.currentState(); got != degrade.StateAbsent {
		t.Errorf("currentState() = %q with no state function; want absent", got)
	}
}

// liveState reads the environment at request time. With no licence configured —
// the state of any Enterprise binary somebody has started without one — it is
// absent, and every console mutation is refused.
func TestLiveStateReadsTheEnvironment(t *testing.T) {
	t.Setenv("GRAVIX_LICENSE", "")
	t.Setenv("GRAVIX_LICENSE_FILE", "")
	if got := liveState(); got != degrade.StateAbsent {
		t.Errorf("liveState() = %q with no licence; want absent", got)
	}

	token, err := os.ReadFile(filepath.Join(repoRoot, "pkg", "license", "testdata", "valid_pro.token"))
	if err != nil {
		t.Fatalf("read the development token: %v", err)
	}
	t.Setenv("GRAVIX_LICENSE", strings.TrimSpace(string(token)))
	if got := liveState(); got != degrade.StateLicensed {
		t.Errorf("liveState() = %q with a valid licence; want licensed", got)
	}
	if exp := expiryOf(liveState()); exp.IsZero() {
		t.Error("expiryOf returned the zero time for a licensed state")
	}
	if exp := expiryOf(degrade.StateAbsent); !exp.IsZero() {
		t.Errorf("expiryOf(absent) = %v; there is no expiry to report", exp)
	}
}

// An unlicensed console refuses writes with its own message rather than
// claiming an expiry that never happened.
func TestConsoleRefusalWhenUnlicensed(t *testing.T) {
	h, _ := testConsole(t, degrade.StateAbsent)
	rec := do(t, h, http.MethodPost, "/ee/fleet/installs", Install{ID: "edge-03", Name: "Edge, Oslo"})
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("POST installs with no licence = %d; want 402", rec.Code)
	}
	var body degrade.ExpiredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "licence_absent" {
		t.Errorf("error = %q; want licence_absent", body.Error)
	}
	if body.ExpiredAt != "" {
		t.Errorf("expired_at = %q; nothing expired", body.ExpiredAt)
	}
}

// An unknown install is a 404, not a 402: a bad request and a billing state must
// not look the same to whoever is reading the response.
func TestConsoleUnknownInstallIs404(t *testing.T) {
	h, _ := testConsole(t, degrade.StateLicensed)
	rec := do(t, h, http.MethodPost, "/ee/fleet/proposals", Proposal{
		ID: "p-x", InstallID: "never-registered", Reason: "because", Changes: map[string]any{"x": 1},
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("proposing to an unknown install = %d; want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// A proposal id is used once. Reusing one would silently replace a decision an
// operator already made.
func TestProposalIdsAreNotReused(t *testing.T) {
	r := seeded(t)
	p := Proposal{ID: "p-dup", InstallID: "edge-01", Reason: "because", Changes: map[string]any{"x": 1}}
	if err := r.Propose(context.Background(), degrade.StateLicensed, p); err != nil {
		t.Fatalf("propose: %v", err)
	}
	err := r.Propose(context.Background(), degrade.StateLicensed, p)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("reusing a proposal id = %v; want a refusal", err)
	}
	if _, ok := r.Disposition("never-proposed"); ok {
		t.Error("Disposition reported an unknown proposal as known")
	}
	if err := r.Decide("never-proposed", DispositionAccepted, now); err == nil {
		t.Error("deciding an unknown proposal succeeded")
	}
}

// firstLine keeps a verifier's error to one line, so a refusal in a log is
// readable rather than a page of cosign output.
func TestFirstLine(t *testing.T) {
	if got := firstLine([]byte("Error: no matching signatures\nstack trace...\n")); got != "Error: no matching signatures" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine([]byte("one line")); got != "one line" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine(nil); got != "" {
		t.Errorf("firstLine(nil) = %q", got)
	}
}
