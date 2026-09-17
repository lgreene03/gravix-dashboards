//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// trinoIsUp answers the same question the script's own health check asks, so a
// test and the script it wraps never disagree about whether the stack is up.
func trinoIsUp() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:8081/v1/info")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// GRVX-1103. Metabase and Superset need no custom connector for Gravix: they
// speak to Trino, and Trino is all Gravix exposes. AC-1 and AC-2 prove that by
// actually making the connection; AC-3 proves the script refuses a tool it does
// not know rather than doing something surprising.

func biScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRootFromE2E(t), "scripts", "verify_bi_connections.sh")
}

// runBIScript returns the script's exit code and combined output.
func runBIScript(t *testing.T, args ...string) (int, string) {
	t.Helper()

	cmd := exec.Command(biScript(t), args...)
	cmd.Dir = repoRootFromE2E(t)
	out, err := cmd.CombinedOutput()

	if err == nil {
		return 0, string(out)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("running the script: %v\n%s", err, out)
	}
	return exit.ExitCode(), string(out)
}

// AC-3. The one criterion that needs neither Docker nor Trino, and so the one
// that runs everywhere.
func TestVerifyBIConnectionsInvalidArg(t *testing.T) {
	code, out := runBIScript(t, "badtool")

	if code != 2 {
		t.Errorf("exit = %d, want 2 for an unknown tool name", code)
	}
	const want = "usage: verify_bi_connections.sh [metabase|superset|all]"
	if !strings.Contains(out, want) {
		t.Errorf("output does not contain %q:\n%s", want, out)
	}
}

// Argument validation must happen before the Trino health check, or the script
// answers "Trino is down" to a question about a typo. Asserted separately
// because the ordering is the only thing keeping AC-3 runnable without a stack.
func TestVerifyBIConnectionsChecksArgsBeforeTrino(t *testing.T) {
	if trinoIsUp() {
		t.Skip("Trino is reachable, so this cannot distinguish the two orderings")
	}

	_, out := runBIScript(t, "badtool")
	if strings.Contains(out, "Trino is not reachable") {
		t.Error("an invalid argument reported Trino as the problem; validate the argument first")
	}
}

// Exit 3, the documented signal that the stack is not up. Proven here by the
// absence of Trino, which is the normal state of a machine that has not started
// docker-compose.
func TestVerifyBIConnectionsNoTrino(t *testing.T) {
	if trinoIsUp() {
		t.Skip("Trino is reachable; this test proves the unreachable path")
	}

	code, out := runBIScript(t, "all")
	if code != 3 {
		t.Errorf("exit = %d, want 3 when Trino is unreachable", code)
	}
	const want = "Trino is not reachable at localhost:8081"
	if !strings.Contains(out, want) {
		t.Errorf("output does not contain %q:\n%s", want, out)
	}
}

// AC-1 and AC-2. Both need Docker and a live Trino with the gravix catalog.
// Gated rather than mocked: a mock of Metabase's setup API would be written by
// whoever wrote the assumption under test, which is exactly the thing these two
// exist to check.
func TestVerifyBIConnectionsMetabase(t *testing.T) {
	requireBIStack(t)

	if code, out := runBIScript(t, "metabase"); code != 0 {
		t.Errorf("exit = %d, want 0:\n%s", code, out)
	}
}

func TestVerifyBIConnectionsSuperset(t *testing.T) {
	requireBIStack(t)

	if code, out := runBIScript(t, "superset"); code != 0 {
		t.Errorf("exit = %d, want 0:\n%s", code, out)
	}
}

// requireBIStack gates on both things these tests need, and says which one is
// missing. It also demands an explicit opt-in: the script pulls two large
// images and boots both tools, which does not belong in any suite that did not
// ask for it by name — the lesson of SD-051, where a Docker-only gate let a
// container test into a 120-second budget.
func requireBIStack(t *testing.T) {
	t.Helper()

	if os.Getenv("BI_CONNECTIONS_E2E") != "1" {
		t.Skip("set BI_CONNECTIONS_E2E=1 to run this; it boots Metabase and Superset")
	}
	requireDocker(t)
	if !trinoIsUp() {
		t.Skip("Trino is not reachable at localhost:8081; run docker-compose up -d trino")
	}
}
