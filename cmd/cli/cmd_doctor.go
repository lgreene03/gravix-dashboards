// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// `gravix doctor` answers the question a self-hoster actually has when nothing
// works: not "is the stack up" but "which part is wrong, and what do I type".
//
// Every failing check prints the exact command that fixes it. Nothing here
// fixes anything itself — a tool that silently mutates someone's stack is a
// bigger support burden than the failures it diagnoses.

// DoctorStatus is the outcome of one diagnostic check.
type DoctorStatus string

const (
	DoctorOK   DoctorStatus = "ok"
	DoctorWarn DoctorStatus = "warn"
	DoctorFail DoctorStatus = "fail"
)

// DoctorCheck is the result of one diagnostic.
type DoctorCheck struct {
	Name   string
	Status DoctorStatus
	Detail string // one line, always non-empty
	FixCmd string // exact shell command; non-empty whenever Status != DoctorOK
}

// DoctorConfig carries every dependency a check needs, so every check is a pure
// function of cfg and is deterministically testable without a live Docker
// daemon, network, or filesystem beyond what the test supplies.
type DoctorConfig struct {
	Endpoint     string
	GatewayURL   string
	CubeURL      string
	DashboardURL string
	BaseDir      string
	APIKey       string
	HTTPClient   *http.Client

	LookPath   func(file string) (string, error)
	RunCommand func(name string, args ...string) ([]byte, error)
	Stat       func(name string) (os.FileInfo, error)
	Glob       func(pattern string) ([]string, error)
}

// NewDoctorConfig builds a DoctorConfig with every real dependency wired in.
func NewDoctorConfig(endpoint, gatewayURL, cubeURL, dashboardURL, baseDir, apiKey string) DoctorConfig {
	return DoctorConfig{
		Endpoint:     strings.TrimRight(endpoint, "/"),
		GatewayURL:   strings.TrimRight(gatewayURL, "/"),
		CubeURL:      strings.TrimRight(cubeURL, "/"),
		DashboardURL: strings.TrimRight(dashboardURL, "/"),
		BaseDir:      baseDir,
		APIKey:       apiKey,
		// Short: ten checks that each wait a default timeout would take longer
		// than reading the logs the tool is meant to save you from.
		HTTPClient: &http.Client{Timeout: 3 * time.Second},
		LookPath:   exec.LookPath,
		RunCommand: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		Stat: os.Stat,
		Glob: filepath.Glob,
	}
}

// AllChecks is the fixed, ordered list of the ten diagnostics gravix doctor
// runs, in the order printed.
var AllChecks = []func(DoctorConfig) DoctorCheck{
	checkDockerCLI,
	checkGoVersion,
	checkEnvConfigured,
	checkPortsAvailable,
	checkIngestionReachable,
	checkIngestionAuth,
	checkTenantDBSeeded,
	checkCubeReachable,
	checkWarehouseData,
	checkDashboardCSP,
}

// doctorRun executes every check in order. It performs no I/O to stdout and
// never calls os.Exit, so the whole diagnosis is testable as data.
func doctorRun(cfg DoctorConfig) []DoctorCheck {
	results := make([]DoctorCheck, 0, len(AllChecks))
	for _, check := range AllChecks {
		results = append(results, check(cfg))
	}
	return results
}

// anyFailed reports whether the run should exit non-zero. A warning never
// fails the command: "the first rollup has not happened yet" is a normal state
// for a stack that started two minutes ago, and exiting 1 on it would teach
// people to ignore the exit code.
func anyFailed(checks []DoctorCheck) bool {
	for _, c := range checks {
		if c.Status == DoctorFail {
			return true
		}
	}
	return false
}

// ─── 1 ───

func checkDockerCLI(cfg DoctorConfig) DoctorCheck {
	const name = "Docker CLI available"
	if _, err := cfg.LookPath("docker"); err != nil {
		return DoctorCheck{name, DoctorFail,
			`"docker" was not found on PATH`,
			"install Docker: https://docs.docker.com/get-docker/"}
	}
	return DoctorCheck{name, DoctorOK, "docker CLI found", ""}
}

// ─── 2 ───

var goVersionRe = regexp.MustCompile(`go(\d+)\.(\d+)`)

func checkGoVersion(cfg DoctorConfig) DoctorCheck {
	const name = "Go toolchain (source builds only)"
	const fix = "install Go 1.24 or newer: https://go.dev/dl/"

	out, err := cfg.RunCommand("go", "version")
	if err != nil {
		return DoctorCheck{name, DoctorFail, "go was not found on PATH", fix}
	}

	m := goVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("could not parse \"go version\" output: %s", strings.TrimSpace(string(out))),
			fix}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	parsed := fmt.Sprintf("%d.%d", major, minor)

	if major < 1 || (major == 1 && minor < 24) {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("go %s is older than go.mod's requirement of 1.24", parsed), fix}
	}
	return DoctorCheck{name, DoctorOK,
		fmt.Sprintf("go %s satisfies go.mod's 1.24 requirement", parsed), ""}
}

// ─── 3 ───

func checkEnvConfigured(cfg DoctorConfig) DoctorCheck {
	const name = "Environment configured"
	keyPath := filepath.Join(cfg.BaseDir, "api_key.txt")

	_, envErr := cfg.Stat(".env")
	_, keyErr := cfg.Stat(keyPath)

	if envErr != nil && keyErr != nil {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf(`neither ".env" nor %q was found — neither the full stack nor the bootstrap stack has been configured`, keyPath),
			"cp .env.example .env   # full stack — or —   docker compose -f docker-compose.bootstrap.yml up -d --build   # bootstrap stack, generates api_key.txt automatically"}
	}

	var found []string
	if envErr == nil {
		found = append(found, `".env"`)
	}
	if keyErr == nil {
		found = append(found, fmt.Sprintf("%q", keyPath))
	}
	return DoctorCheck{name, DoctorOK, "found " + strings.Join(found, " and "), ""}
}

// ─── 4 ───

var doctorPorts = []int{8000, 8090, 8091, 4000}

func checkPortsAvailable(cfg DoctorConfig) DoctorCheck {
	const name = "Required ports"

	var inUse []string
	for _, port := range doctorPorts {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			inUse = append(inUse, strconv.Itoa(port))
		}
	}

	if len(inUse) > 0 {
		// A warning, never a failure: once the stack is up these ports are
		// supposed to be busy, and it is Gravix's own containers holding them.
		return DoctorCheck{name, DoctorWarn,
			fmt.Sprintf("ports already in use: %s — this is expected once the stack is up; if `docker compose up` is currently failing to start, one of these may be a conflicting process",
				strings.Join(inUse, ", ")),
			fmt.Sprintf("lsof -i :%s", inUse[0])}
	}
	return DoctorCheck{name, DoctorOK, "ports 8000, 8090, 8091, 4000 are free", ""}
}

// ─── 5 ───

func checkIngestionReachable(cfg DoctorConfig) DoctorCheck {
	const name = "Ingestion service reachable"

	status, err := doctorGet(cfg, cfg.Endpoint+"/live", nil)
	if err != nil || status != http.StatusOK {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("GET %s/live failed: %s", cfg.Endpoint, describe(status, err)),
			"docker compose -f docker-compose.bootstrap.yml up -d --build"}
	}
	return DoctorCheck{name, DoctorOK,
		fmt.Sprintf("ingestion is healthy at %s", cfg.Endpoint), ""}
}

// ─── 6 ───

func checkIngestionAuth(cfg DoctorConfig) DoctorCheck {
	const name = "API key valid"
	exportFix := fmt.Sprintf("export GRAVIX_API_KEY=$(cat %s)", filepath.Join(cfg.BaseDir, "api_key.txt"))

	if cfg.APIKey == "" {
		return DoctorCheck{name, DoctorFail, "GRAVIX_API_KEY is not set", exportFix}
	}

	status, err := doctorGet(cfg, cfg.Endpoint+"/api/v1/services",
		map[string]string{"X-API-Key": cfg.APIKey})
	switch {
	case err != nil:
		// Not a failure of the key: the previous check already reported the
		// service being unreachable, and repeating it as a second failure would
		// send the reader chasing two problems that are one.
		return DoctorCheck{name, DoctorWarn,
			fmt.Sprintf("could not verify the API key: %v — check \"Ingestion service reachable\" above", err),
			""}
	case status == http.StatusUnauthorized:
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("GET %s/api/v1/services returned 401 — the API key is invalid", cfg.Endpoint),
			exportFix}
	case status == http.StatusOK:
		return DoctorCheck{name, DoctorOK, "API key is valid", ""}
	default:
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("GET %s/api/v1/services returned %d", cfg.Endpoint, status),
			"docker compose -f docker-compose.bootstrap.yml logs ingestion"}
	}
}

// ─── 7 ───

func checkTenantDBSeeded(cfg DoctorConfig) DoctorCheck {
	const name = "Tenant database seeded"
	dbPath := filepath.Join(cfg.BaseDir, "gravix.db")

	if _, err := cfg.Stat(dbPath); err != nil {
		// A warning, not a failure: legacy single-API-key mode has no tenant
		// database and is a supported configuration.
		return DoctorCheck{name, DoctorWarn,
			fmt.Sprintf("%s not found — fine in legacy single-API-key mode; if you expected the bootstrap stack, bootstrap-init may not have finished yet", dbPath),
			"docker compose -f docker-compose.bootstrap.yml logs bootstrap-init"}
	}
	return DoctorCheck{name, DoctorOK, fmt.Sprintf("%s exists", dbPath), ""}
}

// ─── 8 ───

func checkCubeReachable(cfg DoctorConfig) DoctorCheck {
	const name = "Cube.js reachable"

	status, err := doctorGet(cfg, cfg.CubeURL+"/readyz", nil)
	if err != nil || status != http.StatusOK {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("GET %s/readyz failed: %s", cfg.CubeURL, describe(status, err)),
			"docker compose -f docker-compose.bootstrap.yml logs cube"}
	}
	return DoctorCheck{name, DoctorOK,
		fmt.Sprintf("cube.js is healthy at %s", cfg.CubeURL), ""}
}

// ─── 9 ───

func checkWarehouseData(cfg DoctorConfig) DoctorCheck {
	const name = "Rollup has produced data"
	dir := filepath.Join(cfg.BaseDir, "warehouse", "request_metrics_minute")

	matches, err := cfg.Glob(filepath.Join(dir, "*", "*.parquet"))
	if err != nil || len(matches) == 0 {
		// A warning: an empty warehouse is the normal state of a stack that
		// started four minutes ago, and the fix is to wait.
		return DoctorCheck{name, DoctorWarn,
			fmt.Sprintf("no Parquet files under %s yet — the first rollup can take up to ~4 minutes after traffic starts (see the dashboard's first-run countdown)", dir),
			"docker compose -f docker-compose.bootstrap.yml logs request-metrics-rollup"}
	}
	return DoctorCheck{name, DoctorOK,
		fmt.Sprintf("found %d Parquet file(s) under %s", len(matches), dir), ""}
}

// ─── 10 ───

// checkDashboardCSP catches the failure every other check misses.
//
// If the dashboard's Content-Security-Policy does not list the ingestion
// origin, the *browser* blocks the dashboard's requests while every server-side
// health check still reports healthy. `docker compose ps` is green, `gravix
// status` is green, and the dashboard is empty.
//
// It reads the live response header rather than the repository's nginx.conf:
// gravix is installed via Homebrew and `go install`, and frequently runs with no
// checkout present at all.
func checkDashboardCSP(cfg DoctorConfig) DoctorCheck {
	const name = "Dashboard CSP allows ingestion"

	resp, err := doctorDo(cfg, cfg.DashboardURL+"/", nil)
	if err != nil {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("GET %s/ failed: %v", cfg.DashboardURL, err),
			"docker compose -f docker-compose.bootstrap.yml up -d --build"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("GET %s/ failed: status %d", cfg.DashboardURL, resp.StatusCode),
			"docker compose -f docker-compose.bootstrap.yml up -d --build"}
	}

	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), cfg.Endpoint) {
		return DoctorCheck{name, DoctorFail,
			fmt.Sprintf("the dashboard's Content-Security-Policy does not list %s in connect-src — the browser will block requests to ingestion", cfg.Endpoint),
			fmt.Sprintf("check storage/dashboard/nginx.conf's connect-src directive includes %s, then: docker compose -f docker-compose.bootstrap.yml restart dashboard", cfg.Endpoint)}
	}
	return DoctorCheck{name, DoctorOK,
		fmt.Sprintf("dashboard's Content-Security-Policy allows %s", cfg.Endpoint), ""}
}

// ─── helpers ───

func doctorDo(cfg DoctorConfig, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return client.Do(req)
}

func doctorGet(cfg DoctorConfig, url string, headers map[string]string) (int, error) {
	resp, err := doctorDo(cfg, url, headers)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// describe renders whichever of the two failure shapes actually happened.
func describe(status int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("status %d", status)
}

// ─── entry point ───

func runDoctor(args []string) {
	fs := flag.NewFlagSet("gravix doctor", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "Ingestion endpoint (env: GRAVIX_ENDPOINT)")
	gatewayEndpoint := fs.String("gateway-endpoint", "http://localhost:8091", "Gateway endpoint")
	cubeEndpoint := fs.String("cube-endpoint", "http://localhost:4000", "Cube.js endpoint")
	dashboardEndpoint := fs.String("dashboard-endpoint", "http://localhost:8000", "Dashboard endpoint")
	baseDir := fs.String("base-dir", "./data", "Local data directory used by the bootstrap stack")
	fs.Parse(args)

	cfg := NewDoctorConfig(
		resolveStr(*endpoint, getEndpoint()),
		*gatewayEndpoint, *cubeEndpoint, *dashboardEndpoint,
		*baseDir, getAPIKey())

	fmt.Printf("Gravix doctor — %s\n\n", cfg.Endpoint)

	results := doctorRun(cfg)
	for _, c := range results {
		fmt.Printf("%s %s: %s\n", doctorIcon(c.Status), c.Name, c.Detail)
		if c.Status != DoctorOK && c.FixCmd != "" {
			fmt.Printf("    fix: %s\n", c.FixCmd)
		}
	}

	fmt.Println()
	if anyFailed(results) {
		fmt.Println(`Some checks failed. Run the "fix:" command under each failing check, then re-run: gravix doctor`)
		os.Exit(1)
	}
	fmt.Println("All checks passed.")
}

func doctorIcon(s DoctorStatus) string {
	switch s {
	case DoctorOK:
		return "✓"
	case DoctorWarn:
		return "⚠"
	default:
		return "✗"
	}
}
