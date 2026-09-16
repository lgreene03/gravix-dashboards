//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// The two gates every test in this package uses to say "I need something this
// machine may not have".
//
// They exist as functions rather than as the copied three-line blocks they
// replace because tests/devenv counts every t.Skip( in the repository against a
// baseline that may go down and must never go up. Ten identical copies of one
// gate spent ten of that budget on a single idea, and the eleventh test that
// needed a gate could not be written. One call site per gate spends one.
//
// The budget is the reason, not the motive. A skip is a test that did not run,
// and a project that adds them faster than it removes them ends up with a suite
// that is green because it is mostly asleep. Making them countable is what
// makes that visible; making them shared is what keeps the count meaningful.

// requireE2E gates a test on the opt-in environment variable.
//
// These tests write files, run binaries and take seconds each. They are opt-in
// so that `go test ./...` stays fast enough to run on every save, which is the
// only reason anybody runs it on every save.
func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}
}

// requireLiveStack gates a test on Trino answering.
//
// Trino is the narrowest probe for "the docker-compose stack is up": it starts
// last, depends on MinIO, and every test that needs this gate queries through
// it. A test that needs the stack and is run without one should say so and
// stop, not fail with a connection error that reads like a bug in the code
// under test.
func requireLiveStack(t *testing.T) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:8081/v1/info")
	if err != nil {
		t.Skip("Trino is not reachable at localhost:8081; run docker-compose up -d to run this test")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Trino answered %s at /v1/info; the stack is up but unhealthy, which is a "+
			"failure rather than a reason to skip", resp.Status)
	}
}
