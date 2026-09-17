// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testConfig is a config whose every dependency succeeds, so a test can break
// exactly one thing and know that is what it measured.
func testConfig(t *testing.T) DoctorConfig {
	t.Helper()
	return DoctorConfig{
		Endpoint:     "http://ingestion.invalid",
		GatewayURL:   "http://gateway.invalid",
		CubeURL:      "http://cube.invalid",
		DashboardURL: "http://dashboard.invalid",
		BaseDir:      t.TempDir(),
		APIKey:       "grvx_test",
		HTTPClient:   &http.Client{Timeout: time.Second},
		LookPath:     func(string) (string, error) { return "/usr/bin/docker", nil },
		RunCommand:   func(string, ...string) ([]byte, error) { return []byte("go version go1.24.9 linux/amd64"), nil },
		Stat:         func(string) (os.FileInfo, error) { return nil, nil },
		Glob:         func(string) ([]string, error) { return []string{"a.parquet"}, nil },
	}
}

// ─── AC-1 ───

func TestAllChecksHasTenEntriesInOrder(t *testing.T) {
	if len(AllChecks) != 10 {
		t.Fatalf("AC-1 FAILED: AllChecks has %d entries, want exactly 10 — G3.5 is \"10/10\"",
			len(AllChecks))
	}

	want := []string{
		"checkDockerCLI",
		"checkGoVersion",
		"checkEnvConfigured",
		"checkPortsAvailable",
		"checkIngestionReachable",
		"checkIngestionAuth",
		"checkTenantDBSeeded",
		"checkCubeReachable",
		"checkWarehouseData",
		"checkDashboardCSP",
	}
	for i, fn := range AllChecks {
		full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
		got := full[strings.LastIndex(full, ".")+1:]
		if got != want[i] {
			t.Errorf("AC-1 FAILED: position %d is %s, want %s — §6's numbering is the order "+
				"the user reads, and a reordered list changes which failure they see first",
				i, got, want[i])
		}
	}

	// Every check must produce a non-empty name and detail whatever happens,
	// or a line of output would read as blank.
	cfg := testConfig(t)
	for i, c := range doctorRun(cfg) {
		if c.Name == "" || c.Detail == "" {
			t.Errorf("check %d produced an empty name or detail: %+v", i, c)
		}
	}
}

// TestEveryFailProvidesAFix is the §9 line item: a diagnosis without a remedy
// is just a complaint.
func TestEveryFailProvidesAFix(t *testing.T) {
	for _, c := range doctorRun(brokenConfig(t)) {
		if c.Status == DoctorFail && c.FixCmd == "" {
			t.Errorf("%q failed with no fix command", c.Name)
		}
	}
}

// ─── AC-2 ───

func TestCheckDockerCLI(t *testing.T) {
	cfg := testConfig(t)
	if got := checkDockerCLI(cfg); got.Status != DoctorOK {
		t.Errorf("AC-2 FAILED: docker present gave %s: %+v", got.Status, got)
	}

	cfg.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	got := checkDockerCLI(cfg)
	if got.Status != DoctorFail {
		t.Fatalf("AC-2 FAILED: docker missing gave %s", got.Status)
	}
	if got.FixCmd != "install Docker: https://docs.docker.com/get-docker/" {
		t.Errorf("AC-2 FAILED: fix is %q", got.FixCmd)
	}
	if got.Detail != `"docker" was not found on PATH` {
		t.Errorf("AC-2 FAILED: detail is %q", got.Detail)
	}
}

// ─── AC-3 ───

func TestCheckGoVersion(t *testing.T) {
	cfg := testConfig(t)

	for _, tc := range []struct {
		output string
		want   DoctorStatus
	}{
		{"go version go1.24.9 linux/amd64", DoctorOK},
		{"go version go1.25.0 darwin/arm64", DoctorOK},
		{"go version go1.30.1 linux/amd64", DoctorOK},
		{"go version go2.0.0 linux/amd64", DoctorOK},
		{"go version go1.23.8 linux/amd64", DoctorFail},
		{"go version go1.21.0 linux/amd64", DoctorFail},
		{"go version go1.9.7 linux/amd64", DoctorFail},
	} {
		cfg.RunCommand = func(string, ...string) ([]byte, error) { return []byte(tc.output), nil }
		got := checkGoVersion(cfg)
		if got.Status != tc.want {
			t.Errorf("AC-3 FAILED: %q gave %s, want %s (%s)", tc.output, got.Status, tc.want, got.Detail)
		}
		if got.Status != DoctorOK && got.FixCmd == "" {
			t.Errorf("AC-3 FAILED: %q failed with no fix", tc.output)
		}
	}

	// 1.9 must not compare greater than 1.24 — the trap of comparing versions
	// as text rather than as numbers.
	cfg.RunCommand = func(string, ...string) ([]byte, error) {
		return []byte("go version go1.9.7 linux/amd64"), nil
	}
	if got := checkGoVersion(cfg); got.Status != DoctorFail {
		t.Error("AC-3 FAILED: go1.9 was accepted — versions compared as strings, not numbers")
	}

	cfg.RunCommand = func(string, ...string) ([]byte, error) { return nil, errors.New("exec: \"go\"") }
	if got := checkGoVersion(cfg); got.Status != DoctorFail || got.Detail != "go was not found on PATH" {
		t.Errorf("AC-3 FAILED: missing go gave %+v", got)
	}

	cfg.RunCommand = func(string, ...string) ([]byte, error) { return []byte("banana"), nil }
	got := checkGoVersion(cfg)
	if got.Status != DoctorFail {
		t.Errorf("AC-3 FAILED: unparseable output gave %s", got.Status)
	}
	if !strings.Contains(got.Detail, "could not parse") {
		t.Errorf("AC-3 FAILED: detail is %q", got.Detail)
	}
}

// ─── AC-4 ───

func TestCheckEnvConfigured(t *testing.T) {
	missing := func(want string) func(string) (os.FileInfo, error) {
		return func(name string) (os.FileInfo, error) {
			if strings.Contains(name, want) {
				return nil, os.ErrNotExist
			}
			return nil, nil
		}
	}

	cfg := testConfig(t)

	// Either one alone is enough: the full stack uses .env, the bootstrap stack
	// generates api_key.txt, and both are supported.
	cfg.Stat = missing("api_key.txt")
	if got := checkEnvConfigured(cfg); got.Status != DoctorOK {
		t.Errorf("AC-4 FAILED: .env alone gave %s: %s", got.Status, got.Detail)
	}
	cfg.Stat = missing(".env")
	if got := checkEnvConfigured(cfg); got.Status != DoctorOK {
		t.Errorf("AC-4 FAILED: api_key.txt alone gave %s: %s", got.Status, got.Detail)
	}

	cfg.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	got := checkEnvConfigured(cfg)
	if got.Status != DoctorFail {
		t.Fatalf("AC-4 FAILED: neither present gave %s", got.Status)
	}
	// The fix must cover both stacks, because doctor cannot know which one the
	// user meant to run.
	if !strings.Contains(got.FixCmd, ".env") || !strings.Contains(got.FixCmd, "bootstrap") {
		t.Errorf("AC-4 FAILED: the fix does not cover both stacks: %q", got.FixCmd)
	}
}

// ─── AC-5 ───

func TestCheckPortsAvailable(t *testing.T) {
	cfg := testConfig(t)

	// Occupy one of the checked ports for real. Skipping when it is already
	// taken would hide a genuine regression, so the test reports that instead.
	ln, err := net.Listen("tcp", "127.0.0.1:8090")
	if err != nil {
		t.Logf("port 8090 is already in use in this environment (%v); the warn path is "+
			"still exercised because something is listening", err)
	} else {
		defer ln.Close()
	}

	got := checkPortsAvailable(cfg)
	if got.Status != DoctorWarn {
		t.Fatalf("AC-5 FAILED: a listening port gave %s, want warn: %s", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "8090") {
		t.Errorf("AC-5 FAILED: the detail does not name the busy port: %q", got.Detail)
	}
	if !strings.HasPrefix(got.FixCmd, "lsof -i :") {
		t.Errorf("AC-5 FAILED: fix is %q", got.FixCmd)
	}
	// Never a failure: once the stack is up these ports are supposed to be busy.
	if got.Status == DoctorFail {
		t.Error("AC-5 FAILED: a busy port must not fail the command — it is the normal state " +
			"of a running stack")
	}
}

// ─── AC-6 ───

func TestCheckIngestionReachable(t *testing.T) {
	cfg := testConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg.Endpoint = srv.URL
	if got := checkIngestionReachable(cfg); got.Status != DoctorOK {
		t.Errorf("AC-6 FAILED: a healthy /live gave %s: %s", got.Status, got.Detail)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	cfg.Endpoint = down.URL
	got := checkIngestionReachable(cfg)
	if got.Status != DoctorFail {
		t.Fatalf("AC-6 FAILED: a 503 gave %s", got.Status)
	}
	if !strings.Contains(got.Detail, "status 503") {
		t.Errorf("AC-6 FAILED: detail does not name the status: %q", got.Detail)
	}

	// A connection refused is a failure too, and must read as one.
	cfg.Endpoint = "http://127.0.0.1:1"
	if got := checkIngestionReachable(cfg); got.Status != DoctorFail {
		t.Errorf("AC-6 FAILED: an unreachable endpoint gave %s", got.Status)
	}
}

// ─── AC-7 ───

func TestCheckIngestionAuth(t *testing.T) {
	cfg := testConfig(t)

	cfg.APIKey = ""
	got := checkIngestionAuth(cfg)
	if got.Status != DoctorFail {
		t.Fatalf("AC-7 FAILED: no key gave %s", got.Status)
	}
	if got.Detail != "GRAVIX_API_KEY is not set" {
		t.Errorf("AC-7 FAILED: detail is %q", got.Detail)
	}
	if !strings.Contains(got.FixCmd, "api_key.txt") {
		t.Errorf("AC-7 FAILED: the fix does not point at the generated key: %q", got.FixCmd)
	}

	for _, tc := range []struct {
		status int
		want   DoctorStatus
		detail string
	}{
		{http.StatusOK, DoctorOK, "API key is valid"},
		{http.StatusUnauthorized, DoctorFail, "the API key is invalid"},
		{http.StatusInternalServerError, DoctorFail, "returned 500"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") == "" {
				t.Error("AC-7 FAILED: the key was not sent as X-API-Key")
			}
			w.WriteHeader(tc.status)
		}))
		cfg.Endpoint = srv.URL
		cfg.APIKey = "grvx_test"
		got := checkIngestionAuth(cfg)
		srv.Close()

		if got.Status != tc.want {
			t.Errorf("AC-7 FAILED: %d gave %s, want %s", tc.status, got.Status, tc.want)
		}
		if !strings.Contains(got.Detail, tc.detail) {
			t.Errorf("AC-7 FAILED: %d detail is %q, want it to contain %q",
				tc.status, got.Detail, tc.detail)
		}
	}

	// An unreachable service is a warning, not a second failure: the previous
	// check already reported it, and two failures for one cause sends the
	// reader chasing a problem that does not exist.
	cfg.Endpoint = "http://127.0.0.1:1"
	if got := checkIngestionAuth(cfg); got.Status != DoctorWarn {
		t.Errorf("AC-7 FAILED: an unreachable service gave %s, want warn", got.Status)
	}
}

// ─── AC-8 ───

func TestCheckTenantDBSeeded(t *testing.T) {
	cfg := testConfig(t)

	if got := checkTenantDBSeeded(cfg); got.Status != DoctorOK {
		t.Errorf("AC-8 FAILED: an existing db gave %s", got.Status)
	}

	cfg.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	got := checkTenantDBSeeded(cfg)
	// A warning, not a failure: legacy single-API-key mode has no tenant
	// database and is supported.
	if got.Status != DoctorWarn {
		t.Errorf("AC-8 FAILED: a missing db gave %s, want warn — legacy mode has no tenant "+
			"database and is a supported configuration", got.Status)
	}
	if !strings.Contains(got.Detail, "legacy single-API-key mode") {
		t.Errorf("AC-8 FAILED: the detail does not explain when this is fine: %q", got.Detail)
	}
}

// ─── AC-9 ───

func TestCheckCubeReachable(t *testing.T) {
	cfg := testConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg.CubeURL = srv.URL
	if got := checkCubeReachable(cfg); got.Status != DoctorOK {
		t.Errorf("AC-9 FAILED: a healthy /readyz gave %s: %s", got.Status, got.Detail)
	}

	cfg.CubeURL = "http://127.0.0.1:1"
	got := checkCubeReachable(cfg)
	if got.Status != DoctorFail {
		t.Fatalf("AC-9 FAILED: an unreachable cube gave %s", got.Status)
	}
	if !strings.Contains(got.FixCmd, "logs cube") {
		t.Errorf("AC-9 FAILED: fix is %q", got.FixCmd)
	}
}

// ─── AC-10 ───

func TestCheckWarehouseData(t *testing.T) {
	cfg := testConfig(t)

	if got := checkWarehouseData(cfg); got.Status != DoctorOK {
		t.Errorf("AC-10 FAILED: parquet present gave %s", got.Status)
	}

	cfg.Glob = func(string) ([]string, error) { return nil, nil }
	got := checkWarehouseData(cfg)
	if got.Status != DoctorWarn {
		t.Errorf("AC-10 FAILED: no parquet gave %s, want warn — an empty warehouse is the "+
			"normal state four minutes after boot", got.Status)
	}
	if !strings.Contains(got.Detail, "~4 minutes") {
		t.Errorf("AC-10 FAILED: the detail does not tell the user to wait: %q", got.Detail)
	}

	cfg.Glob = func(string) ([]string, error) { return nil, errors.New("bad pattern") }
	if got := checkWarehouseData(cfg); got.Status != DoctorWarn {
		t.Errorf("AC-10 FAILED: a glob error gave %s", got.Status)
	}
}

// ─── AC-11 ───

func TestCheckDashboardCSP(t *testing.T) {
	cfg := testConfig(t)
	cfg.Endpoint = "http://localhost:8090"

	withCSP := func(policy string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if policy != "" {
				w.Header().Set("Content-Security-Policy", policy)
			}
			w.WriteHeader(http.StatusOK)
		}))
	}

	good := withCSP("default-src 'self'; connect-src 'self' http://localhost:4000 http://localhost:8090 http://localhost:8091")
	defer good.Close()
	cfg.DashboardURL = good.URL
	if got := checkDashboardCSP(cfg); got.Status != DoctorOK {
		t.Errorf("AC-11 FAILED: a permissive CSP gave %s: %s", got.Status, got.Detail)
	}

	// The exact failure this check exists for: every server-side probe is green
	// and the browser still blocks the dashboard's requests.
	stale := withCSP("default-src 'self'; connect-src 'self' http://localhost:4000 http://localhost:8091")
	defer stale.Close()
	cfg.DashboardURL = stale.URL
	got := checkDashboardCSP(cfg)
	if got.Status != DoctorFail {
		t.Fatalf("AC-11 FAILED: a CSP omitting ingestion gave %s", got.Status)
	}
	if !strings.Contains(got.Detail, "the browser will block") {
		t.Errorf("AC-11 FAILED: the detail does not explain the symptom: %q", got.Detail)
	}

	// No header at all is the same problem.
	none := withCSP("")
	defer none.Close()
	cfg.DashboardURL = none.URL
	if got := checkDashboardCSP(cfg); got.Status != DoctorFail {
		t.Errorf("AC-11 FAILED: a missing CSP header gave %s", got.Status)
	}

	cfg.DashboardURL = "http://127.0.0.1:1"
	if got := checkDashboardCSP(cfg); got.Status != DoctorFail {
		t.Errorf("AC-11 FAILED: an unreachable dashboard gave %s", got.Status)
	}
}

// ─── AC-12 ───

// brokenConfig makes every check fail or warn: nothing on PATH, nothing on
// disk, nothing listening.
func brokenConfig(t *testing.T) DoctorConfig {
	t.Helper()
	return DoctorConfig{
		Endpoint:     "http://127.0.0.1:1",
		GatewayURL:   "http://127.0.0.1:1",
		CubeURL:      "http://127.0.0.1:1",
		DashboardURL: "http://127.0.0.1:1",
		BaseDir:      filepath.Join(t.TempDir(), "nope"),
		APIKey:       "",
		HTTPClient:   &http.Client{Timeout: 500 * time.Millisecond},
		LookPath:     func(string) (string, error) { return "", errors.New("not found") },
		RunCommand:   func(string, ...string) ([]byte, error) { return nil, errors.New("not found") },
		Stat:         func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		Glob:         func(string) ([]string, error) { return nil, nil },
	}
}

func TestDoctorRunAggregateExitDecision(t *testing.T) {
	results := doctorRun(brokenConfig(t))

	if len(results) != 10 {
		t.Fatalf("AC-12 FAILED: doctorRun returned %d results, want 10", len(results))
	}
	if !anyFailed(results) {
		t.Fatal("AC-12 FAILED: a fully broken stack did not fail the command")
	}

	// Six of the ten are hard failures on a broken stack; the other four are
	// warnings by design (ports free, no tenant db, no rollup yet, and auth
	// unverifiable because the service is down).
	var failed, warned int
	for _, c := range results {
		switch c.Status {
		case DoctorFail:
			failed++
		case DoctorWarn:
			warned++
		}
	}
	if failed == 0 {
		t.Error("AC-12 FAILED: no check reported a failure against a broken stack")
	}
	t.Logf("broken stack: %d fail, %d warn, %d ok", failed, warned, 10-failed-warned)

	// A healthy-but-quiet stack must exit 0. Warnings never fail the command,
	// or a user four minutes into their first boot is told they are broken.
	warnOnly := []DoctorCheck{
		{"a", DoctorOK, "fine", ""},
		{"b", DoctorWarn, "not yet", "wait"},
	}
	if anyFailed(warnOnly) {
		t.Error("AC-12 FAILED: warnings alone failed the command. The first rollup takes " +
			"minutes, and exiting 1 on that teaches people to ignore the exit code")
	}
	if anyFailed(nil) {
		t.Error("AC-12 FAILED: an empty result set failed the command")
	}
}

// ─── dispatch wiring (§4.2) ───

// TestMainDispatchIncludesDoctor checks main.go routes the subcommand and
// advertises it.
//
// Deliberately NOT named TestMain: that identifier is Go's test-harness hook,
// and defining it here would replace the package's test runner rather than add
// a test. It still matches the spec's `-run TestMain` by prefix.
func TestMainDispatchIncludesDoctor(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, `case "doctor":`) {
		t.Error("main.go has no \"doctor\" case, so `gravix doctor` would print the unknown-command help")
	}
	if !strings.Contains(body, "runDoctor(os.Args[2:])") {
		t.Error("the \"doctor\" case does not call runDoctor with the remaining arguments")
	}
	if !strings.Contains(body, "gravix doctor") {
		t.Error("printUsage does not list `gravix doctor`, so nobody discovers it")
	}
}

// TestDoctorIcons pins the three icons, since they are how a reader scans the
// output.
func TestDoctorIcons(t *testing.T) {
	for status, want := range map[DoctorStatus]string{
		DoctorOK:   "✓",
		DoctorWarn: "⚠",
		DoctorFail: "✗",
	} {
		if got := doctorIcon(status); got != want {
			t.Errorf("icon for %s is %q, want %q", status, got, want)
		}
	}
	if got := doctorIcon(DoctorStatus("nonsense")); got != "✗" {
		t.Errorf("an unknown status rendered as %q; it must not read as success", got)
	}
}

func TestNewDoctorConfigTrimsTrailingSlashes(t *testing.T) {
	cfg := NewDoctorConfig("http://a/", "http://b/", "http://c/", "http://d/", "./data", "k")
	for name, got := range map[string]string{
		"endpoint": cfg.Endpoint, "gateway": cfg.GatewayURL,
		"cube": cfg.CubeURL, "dashboard": cfg.DashboardURL,
	} {
		if strings.HasSuffix(got, "/") {
			t.Errorf("%s kept its trailing slash (%q), which would produce a //live URL", name, got)
		}
	}
	for _, fn := range []any{cfg.LookPath, cfg.RunCommand, cfg.Stat, cfg.Glob} {
		if fn == nil {
			t.Error("NewDoctorConfig left a dependency nil; the real run would panic")
		}
	}
	if cfg.HTTPClient == nil || cfg.HTTPClient.Timeout != 3*time.Second {
		t.Errorf("http client is %+v, want a 3-second timeout", cfg.HTTPClient)
	}
}

var _ = fmt.Sprintf
