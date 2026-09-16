// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// AC-2. docs/04-non-goals.md §3 forbids collecting system metrics from hosts.
// The way this package keeps that promise is that the type carrying the data
// has nowhere to put them, so the check is on the struct's exact shape rather
// than on whether anyone remembered not to fill in a field.
func TestAgentCollectsNoHostMetrics(t *testing.T) {
	rt := reflect.TypeOf(Report{})
	var fields []string
	for i := 0; i < rt.NumField(); i++ {
		fields = append(fields, rt.Field(i).Name)
	}
	sort.Strings(fields)
	want := "ConfigHash,Healthy,InstallID,ReportedAt,Version"
	if strings.Join(fields, ",") != want {
		t.Errorf("Report has fields %v\nwant exactly %s — a new field here is a new thing "+
			"collected from somebody's machine, and needs to be argued for against non-goal §3",
			fields, want)
	}

	// And nothing in the package reads anything about the host.
	src := packageSource(t)
	for _, banned := range []string{
		"cpu_percent", "mem_total", "disk_usage", "/proc/", "host_metrics",
		"runtime.NumCPU", "runtime.MemStats", "os.Hostname", "net.Interfaces",
		"gopsutil", "syscall.Statfs", "os.ReadDir(\"/",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("the package references %q; it reports on one process, not on a host", banned)
		}
	}

	// SelfReport takes everything it reports as an argument. It has no way to
	// find anything out on its own, which is the structural version of the rule.
	fn := reflect.TypeOf(SelfReport)
	if fn.NumIn() != 5 {
		t.Errorf("SelfReport takes %d arguments; everything it reports must be passed in", fn.NumIn())
	}
}

// AC-3. Off by default, and one local flag turns it off again.
func TestAgentOptionalAndDisableable(t *testing.T) {
	if NewAgent(AgentConfig{}).Enabled() {
		t.Error("the zero config produced an enabled agent; absent is the default")
	}
	if NewAgent(AgentConfig{Enabled: true}).Enabled() {
		t.Error("an agent with no console URL is enabled; it has nowhere to report")
	}
	if NewAgent(AgentConfig{ConsoleURL: "https://console.example"}).Enabled() {
		t.Error("an agent that was never enabled is enabled")
	}
	a := NewAgent(AgentConfig{Enabled: true, ConsoleURL: "https://console.example"})
	if !a.Enabled() {
		t.Fatal("an explicitly configured agent is not enabled")
	}

	// A disabled agent does nothing at all: no request, no error, no queue.
	off := NewAgent(AgentConfig{ConsoleURL: "http://127.0.0.1:1"})
	if err := off.Send(context.Background(), SelfReport("edge-01", "v1", "h", true, now)); err != nil {
		t.Errorf("a disabled agent returned an error: %v", err)
	}
	if off.Queued() != 0 {
		t.Errorf("a disabled agent queued %d reports", off.Queued())
	}
	// Run returns immediately rather than blocking on a ticker.
	done := make(chan error, 1)
	go func() { done <- off.Run(context.Background(), func(time.Time) Report { return Report{} }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a disabled agent's Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("a disabled agent's Run did not return")
	}
}

// AC-4. The console never connects in. The agent reports outbound; nothing in
// this package listens on anything.
func TestNoInboundConnections(t *testing.T) {
	src := packageSource(t)
	for _, banned := range []string{
		"net.Listen", "http.ListenAndServe", "http.Server{", "&http.Server",
		"ListenAndServeTLS", "net.ListenPacket",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("the package contains %q; an install must be unreachable from the console", banned)
		}
	}
	// The console's own surface is an http.Handler mounted on the gateway's
	// existing listener, which is core's. ee/ opens no socket of its own.
	if !strings.Contains(src, "http.Handler") {
		t.Error("the console no longer exposes an http.Handler; check this test still means anything")
	}
}

// The report reaches the console, and it carries what SelfReport was given.
func TestAgentSendsItsSelfReport(t *testing.T) {
	var got []Report
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("the agent used %s; reporting is a POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(AgentConfig{Enabled: true, ConsoleURL: srv.URL})
	rep := SelfReport("edge-01", "v1.2.3", "cfg-hash", true, now)
	if err := a.Send(context.Background(), rep); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(got) != 1 || got[0] != rep {
		t.Errorf("console received %+v; want %+v", got, rep)
	}
	if a.Queued() != 0 {
		t.Errorf("%d reports queued after a successful send", a.Queued())
	}
}

// §6.1: a console that cannot be reached queues locally and retries, and says
// in as many words that nothing local is affected.
func TestConsoleUnreachableQueuesAndSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	a := NewAgent(AgentConfig{Enabled: true, ConsoleURL: srv.URL})

	err := a.Send(context.Background(), SelfReport("edge-01", "v1", "h", true, now))
	if err == nil {
		t.Fatal("a failed send reported success")
	}
	if !strings.Contains(err.Error(), "console unreachable; local operation unaffected") {
		t.Errorf("err = %v; want it to say local operation is unaffected", err)
	}
	if a.Queued() != 1 {
		t.Errorf("%d reports queued; want 1", a.Queued())
	}

	// A second failure queues both, and the backlog is bounded.
	for i := 0; i < maxQueued+10; i++ {
		_ = a.Send(context.Background(), SelfReport("edge-01", "v1", "h", true, now))
	}
	if a.Queued() > maxQueued {
		t.Errorf("the backlog grew to %d; an unbounded queue in a monitoring agent is how "+
			"the monitoring becomes the outage", a.Queued())
	}

	// When the console comes back, the backlog goes with the next report.
	srv.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	a.cfg.ConsoleURL = ok.URL
	if err := a.Send(context.Background(), SelfReport("edge-01", "v1", "h", true, now)); err != nil {
		t.Fatalf("Send after recovery: %v", err)
	}
	if a.Queued() != 0 {
		t.Errorf("%d reports still queued after a successful send", a.Queued())
	}
}

// AC-13. The OSS binaries do not depend on anything under ee/.
func TestCoreBuildsWithoutEE(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps",
		"./services/gateway/", "./services/ingestion/", "./cmd/cli/",
		"./transforms/request_metrics_minute/")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "gravix-dashboards/ee/") {
			t.Errorf("an OSS binary depends on %s; the core must build with ee/ deleted", dep)
		}
	}
}

// The ticker loop reports and stops when its context is cancelled. A monitoring
// agent that ignored cancellation would outlive the process it reports on.
func TestAgentRunReportsUntilCancelled(t *testing.T) {
	var mu sync.Mutex
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(AgentConfig{Enabled: true, ConsoleURL: srv.URL, Interval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, func(at time.Time) Report {
			return SelfReport("edge-01", "v1", "h", true, at)
		})
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := seen
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v; want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	mu.Lock()
	defer mu.Unlock()
	if seen < 2 {
		t.Errorf("the console saw %d reports; the ticker should have fired more than once", seen)
	}
}

// A default interval is applied rather than a zero one, which would spin.
func TestAgentIntervalDefaults(t *testing.T) {
	if got := NewAgent(AgentConfig{}).cfg.Interval; got != DefaultInterval {
		t.Errorf("interval = %v; want %v", got, DefaultInterval)
	}
	if got := NewAgent(AgentConfig{Interval: -1}).cfg.Interval; got != DefaultInterval {
		t.Errorf("a negative interval became %v; want %v", got, DefaultInterval)
	}
}

// An unreachable host, rather than an error status, is the ordinary case: the
// console's machine is off.
func TestAgentHandlesAnUnreachableHost(t *testing.T) {
	a := NewAgent(AgentConfig{Enabled: true, ConsoleURL: "http://127.0.0.1:1/report"})
	var observed bool
	a.Observe = func(sent bool, err error) {
		observed = true
		if sent || err == nil {
			t.Errorf("Observe(sent=%v, err=%v); want a failure", sent, err)
		}
	}
	if err := a.Send(context.Background(), SelfReport("edge-01", "v1", "h", true, now)); err == nil {
		t.Error("reporting to a closed port reported success")
	}
	if !observed {
		t.Error("Observe was not called")
	}
	if a.Queued() != 1 {
		t.Errorf("%d queued; want 1", a.Queued())
	}
}
