//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// AC-8's proof builds the gateway binary and starts it, which is minutes of
// link time on a cold machine rather than milliseconds of assertion. It carries
// the `slow` tag for the same reason tests/e2e does: the contributor suite has
// a five-minute budget and a `go build` of the whole gateway is the single
// largest thing that can be moved out of it without deleting a test.
//
// It still runs on every pull request, in the `test` job and in `make test`,
// which pass -tags=slow. Nothing is skipped; the split is by cost.

package gatewaycore

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AC-8. The OSS gateway is built from the same path, by the same command, and
// answers the same things it did before its body moved into this package. The
// binary is started for real rather than inspected, because the claim being made
// is about the shipped artefact and not about the source that produced it.
func TestOSSGatewayBehaviourUnchanged(t *testing.T) {
	work := t.TempDir()
	bin := filepath.Join(work, "gateway")

	// Unchanged build command: deployment artefacts still run exactly this.
	build := exec.Command("go", "build", "-o", bin, "./services/gateway/")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -o bin/gateway ./services/gateway/: %v\n%s", err, out)
	}

	addr := freeAddr(t)
	cmd := exec.Command(bin)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"GATEWAY_ADDR="+addr,
		"TENANT_DB_PATH="+filepath.Join(work, "tenants.db"),
		"JWT_SECRET=test-secret-that-is-long-enough-32",
		"RAW_DATA_DIR="+filepath.Join(work, "raw"),
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	var logs strings.Builder
	go func() { io.Copy(&logs, bufio.NewReader(stderr)) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			t.Logf("gateway stderr:\n%s", logs.String())
		}
	})

	base := "http://" + addr
	waitForLive(t, base)

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/live", http.StatusOK, "up"},
		{"/ready", http.StatusOK, `{"db":"ok"}`},
		{"/api/gateway/ee/status", http.StatusOK, `{"extensions":[]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(base + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			got, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.code {
				t.Errorf("GET %s = %d; want %d (body %s)", tc.path, resp.StatusCode, tc.code, got)
			}
			if strings.TrimSpace(string(got)) != tc.body {
				t.Errorf("GET %s body = %q; want %q", tc.path, strings.TrimSpace(string(got)), tc.body)
			}
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitForLive(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/live")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("gateway did not answer /live within 20s")
}
