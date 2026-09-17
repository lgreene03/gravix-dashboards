// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ingestion records every fact the prober reports.
type ingestion struct {
	srv   *httptest.Server
	mu    sync.Mutex
	facts []map[string]any
	fail  bool
}

func newIngestion(t *testing.T) *ingestion {
	t.Helper()
	i := &ingestion{}
	i.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fact map[string]any
		json.NewDecoder(r.Body).Decode(&fact)

		i.mu.Lock()
		i.facts = append(i.facts, fact)
		fail := i.fail
		i.mu.Unlock()

		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid or missing X-API-Key header"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(i.srv.Close)
	return i
}

func (i *ingestion) recorded() []map[string]any {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]map[string]any, len(i.facts))
	copy(out, i.facts)
	return out
}

func TestProberOneCycleReportsEveryTarget(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	down := freeURL(t) // nothing is listening

	ing := newIngestion(t)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--targets", "gateway-live=" + up.URL + ",ingestion-live=" + down,
		"--ingestion-endpoint", ing.srv.URL,
		"--api-key", "grvx_key",
		"--once",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}

	facts := ing.recorded()
	if len(facts) != 2 {
		t.Fatalf("reported %d facts, want 2 (one per target)", len(facts))
	}

	// The healthy target is 200; the unreachable one is 503, so it counts
	// against error_rate and therefore against uptime.
	var healthy, unhealthy int
	for _, f := range facts {
		switch int(f["status_code"].(float64)) {
		case 200:
			healthy++
		case 503:
			unhealthy++
		default:
			t.Errorf("unexpected status_code %v", f["status_code"])
		}
	}
	if healthy != 1 || unhealthy != 1 {
		t.Errorf("recorded %d healthy and %d unhealthy, want 1 of each", healthy, unhealthy)
	}
}

// TestProberOneCycleSurvivesAReportingFailure: a monitoring process that stops
// monitoring because one write failed has turned a partial outage into a total
// loss of visibility, and the missing facts are the ones that would have
// explained it.
func TestProberOneCycleSurvivesAReportingFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	ing := newIngestion(t)
	ing.fail = true

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--targets", "a=" + up.URL + ",b=" + up.URL + ",c=" + up.URL,
		"--ingestion-endpoint", ing.srv.URL,
		"--api-key", "grvx_key",
		"--once",
	}, &stdout, &stderr)

	// --once exits 0 regardless of individual reporting errors.
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got := len(ing.recorded()); got != 3 {
		t.Errorf("attempted %d reports, want 3; the cycle stopped at the first failure", got)
	}
	if !strings.Contains(stderr.String(), "invalid or missing X-API-Key header") {
		t.Errorf("the failure was not reported to stderr: %s", stderr.String())
	}
}

func TestProberRequiresTargets(t *testing.T) {
	for _, args := range [][]string{
		{"--api-key", "k", "--once"},
		{"--api-key", "k", "--targets", "", "--once"},
		{"--api-key", "k", "--targets", " , ", "--once"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
			t.Errorf("args %v: exit code %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), usageTargets) {
			t.Errorf("args %v: stderr = %q", args, stderr.String())
		}
	}
}

func TestProberRejectsMalformedTargets(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--api-key", "k", "--targets", "gateway-live", "--once",
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "is not a name=url pair") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestProberRequiresAnAPIKey(t *testing.T) {
	t.Setenv("GRAVIX_DOGFOOD_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--targets", "a=http://example.invalid/live", "--once",
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), usageAPIKey) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestProberReadsTheKeyFromTheEnvironment: a key on a command line is in shell
// history and visible in `ps`, and this process runs continuously.
func TestProberReadsTheKeyFromTheEnvironment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	var seen string
	var mu sync.Mutex
	ing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Get("X-API-Key")
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer ing.Close()

	t.Setenv("GRAVIX_DOGFOOD_API_KEY", "env-key")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{
		"--targets", "a=" + up.URL, "--ingestion-endpoint", ing.URL, "--once",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if seen != "env-key" {
		t.Errorf("X-API-Key = %q, want %q", seen, "env-key")
	}
}

// TestProberStopsOnACancelledContext pins that the loop is interruptible.
// Without --once the process runs until SIGTERM, and a shutdown that hung
// would be noticed only during a deployment.
func TestProberStopsOnACancelledContext(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	ing := newIngestion(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- run(ctx, []string{
			"--targets", "a=" + up.URL,
			"--ingestion-endpoint", ing.srv.URL,
			"--api-key", "k",
			"--interval", "1h", // long enough that only cancellation ends it
		}, &stdout, &stderr)
	}()

	// Let the first cycle land, then cancel. Polled with a sleep rather than
	// spun on: a busy loop here can starve the goroutine it is waiting for.
	deadline := time.Now().Add(10 * time.Second)
	for len(ing.recorded()) == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("the prober reported nothing within 10s")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	if code := <-done; code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

// freeURL reserves and releases a port, so nothing is listening on the URL it
// returns. A probe against it fails to connect, which is the condition under
// test rather than a flaky one.
func freeURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr + "/live"
}
