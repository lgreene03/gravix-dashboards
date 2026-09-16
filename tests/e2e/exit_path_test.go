//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// seededAPIKeyMarker is written into the tenant database as a password hash and
// a 2FA secret. It must never appear in the exported tree.
const seededAPIKeyMarker = "grvx_sk_live_DO_NOT_LEAK_2f7a19c4e8b6"

const exitRepoRoot = "../.."

// seedExitFixture builds a data root and a tenant database holding facts,
// metrics, events, an alert rule, a dashboard, an API key and a user, then
// returns the data root and database path.
func seedExitFixture(t *testing.T) (dataRoot, dbPath, tenantID string) {
	t.Helper()

	dataRoot = t.TempDir()
	ctx := context.Background()
	tenantID = "acme"

	// Raw facts and service events, in the JSONL layout ingestion writes.
	for _, day := range []string{"2026-01-01", "2026-01-02"} {
		var facts, events bytes.Buffer
		for i := 0; i < 5; i++ {
			fact := map[string]any{
				"event_id":          fmt.Sprintf("0194d0a0-0000-7000-8000-%012d", i),
				"event_time":        fmt.Sprintf("%sT00:%02d:00Z", day, i),
				"service":           "checkout",
				"method":            "GET",
				"path_template":     "/orders/{id}",
				"status_code":       200,
				"latency_ms":        i + 1,
				"user_agent_family": "chrome",
			}
			writeJSONLine(t, &facts, fact)

			writeJSONLine(t, &events, map[string]any{
				"event_id":   fmt.Sprintf("0194d0a1-0000-7000-8000-%012d", i),
				"event_time": fmt.Sprintf("%sT00:%02d:00Z", day, i),
				"service":    "checkout",
				"event_type": "deploy",
			})
		}
		writeFixtureFile(t, filepath.Join(dataRoot, "raw", tenantID, "request_facts", day, "00", "batch.jsonl"), facts.Bytes())
		writeFixtureFile(t, filepath.Join(dataRoot, "raw", tenantID, "service_events", day, "00", "batch.jsonl"), events.Bytes())

		// Rolled-up metrics, in the Hive layout the rollup writes. Without
		// them the exit would be tested against two of its three datasets,
		// and the README's metrics command would have nothing to read.
		metrics := make([]recompute.MetricRow, 0, 5)
		for i := 0; i < 5; i++ {
			metrics = append(metrics, recompute.MetricRow{
				TenantID:     tenantID,
				BucketStart:  fmt.Sprintf("%sT00:%02d:00Z", day, i),
				Service:      "checkout",
				Method:       "GET",
				PathTemplate: "/orders/{id}",
				RequestCount: int64(i + 1),
				P95LatencyMs: 20,
				EventDay:     day,
			})
		}
		var pq bytes.Buffer
		w := parquet.NewGenericWriter[recompute.MetricRow](&pq, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
		if _, err := w.Write(metrics); err != nil {
			t.Fatalf("write metrics parquet: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close metrics parquet: %v", err)
		}
		compact := strings.ReplaceAll(day, "-", "")
		writeFixtureFile(t, filepath.Join(dataRoot, "warehouse", tenantID, "request_metrics_minute",
			"event_day="+day, "request_metrics_minute_"+compact+".parquet"), pq.Bytes())
	}

	dbPath = filepath.Join(t.TempDir(), "gravix.db")
	db, err := tenantdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open tenantdb: %v", err)
	}
	defer db.Close()

	tenant := &tenantdb.Tenant{ID: tenantID, Name: "Acme", Email: "owner@example.com", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	if _, _, err := db.APIKeys().Create(ctx, tenant.ID, "ingest key", nil); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if err := db.Users().Create(ctx, &tenantdb.User{
		ID: "user-1", TenantID: tenant.ID, Email: "owner@example.com",
		PasswordHash: seededAPIKeyMarker, TwoFactorSecret: seededAPIKeyMarker,
		Role: "admin", Status: "active",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	channel := &tenantdb.NotificationChannel{
		ID: "chan-1", TenantID: tenant.ID, Name: "ops", Type: "webhook",
		Config: fmt.Sprintf(`{"webhook_url":"https://hooks.example.com/x","auth_header":"Bearer %s"}`, seededAPIKeyMarker),
		Status: "active",
	}
	if err := db.NotificationChannels().Create(ctx, channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.AlertRules().Create(ctx, &tenantdb.AlertRule{
		ID: "rule-1", TenantID: tenant.ID, ChannelID: channel.ID,
		Name: "checkout error rate", Metric: "error_rate", Operator: "gt",
		Threshold: 0.05, WindowMinutes: 5, Status: "active",
	}); err != nil {
		t.Fatalf("create alert rule: %v", err)
	}
	if err := db.CustomDashboards().Create(ctx, &tenantdb.CustomDashboard{
		ID: "dash-1", TenantID: tenant.ID, Name: "checkout health",
		Config:     `[{"metric":"error_rate","title":"errors"}]`,
		SharedWith: "team",
	}); err != nil {
		t.Fatalf("create dashboard: %v", err)
	}

	return dataRoot, dbPath, tenantID
}

func writeJSONLine(t *testing.T, buf *bytes.Buffer, v any) {
	t.Helper()
	line, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	buf.Write(line)
	buf.WriteByte('\n')
}

func writeFixtureFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runExitPath runs scripts/export_everything.sh and returns the output
// directory. This is §5.4 step 2: the published script, not a Go shortcut
// around it.
func runExitPath(t *testing.T, dataRoot, dbPath, tenantID string) string {
	t.Helper()

	outDir := filepath.Join(t.TempDir(), "exit")
	script := filepath.Join(exitRepoRoot, "scripts", "export_everything.sh")

	cmd := exec.Command("bash", script, "--out", outDir, "--from", "2026-01-01", "--to", "2026-01-03")
	cmd.Env = append(os.Environ(),
		"GRAVIX_DATA_ROOT="+dataRoot,
		"GRAVIX_TENANT_DB="+dbPath,
		"GRAVIX_TENANT_ID="+tenantID,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("export_everything.sh failed: %v\n%s", err, out)
	}
	return outDir
}

// AC-1
func TestExportEverythingCompletes(t *testing.T) {
	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	for _, rel := range []string{
		"README.md", "MANIFEST.json", "checksums.txt",
		"facts", "metrics", "events",
		filepath.Join("config", "alert_rules.json"),
		filepath.Join("config", "dashboards.json"),
		filepath.Join("config", "slos.json"),
		filepath.Join("config", "api_keys.json"),
		filepath.Join("config", "team.json"),
		filepath.Join("config", "scheduled_exports.json"),
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

// AC-2: the claim this spec exists to prove. Nothing Gravix is running, and
// the data still reads.
func TestExitPathReadableWithGravixStopped(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip(duckDBMissing)
	}

	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	// Step 3: prove no Gravix process is listening before reading a byte.
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8090", "127.0.0.1:8091"} {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatalf("something is listening on %s; this test proves the exit is readable with Gravix stopped", addr)
		}
	}

	got := runDuckDB(t, outDir, "SELECT count(*) AS n FROM read_parquet('facts/**/*.parquet');")
	if got != "n\n10" {
		t.Fatalf("reading the exported facts returned %q, want 10 rows", got)
	}
}

// AC-3
func TestExportRowCountsMatchManifest(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip(duckDBMissing)
	}

	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	manifest := readExitManifest(t, outDir)

	for dataset, want := range manifest.RowCounts {
		if want == 0 {
			continue
		}
		query := fmt.Sprintf("SELECT count(*) AS n FROM read_parquet('%s/**/*.parquet');", dataset)
		got := runDuckDB(t, outDir, query)
		wantText := fmt.Sprintf("n\n%d", want)
		if got != wantText {
			t.Errorf("%s: DuckDB counted %q, MANIFEST.json says %d", dataset, got, want)
		}
	}

	if manifest.RowCounts["facts"] != 10 {
		t.Errorf("manifest facts = %d, want 10", manifest.RowCounts["facts"])
	}
}

// AC-4: step 6 of §5.4 — the export must not become a credential leak.
func TestNoSecretInExitPath(t *testing.T) {
	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	var hits []string
	err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(data, []byte(seededAPIKeyMarker)) {
			rel, _ := filepath.Rel(outDir, path)
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(hits) != 0 {
		t.Fatalf("the seeded secret appears in %v; an export is a file people email", hits)
	}
}

// AC-7
func TestConfigExportComplete(t *testing.T) {
	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	rules := readJSONRecords(t, filepath.Join(outDir, "config", "alert_rules.json"))
	if len(rules) != 1 {
		t.Fatalf("exported %d alert rules, want 1", len(rules))
	}
	if rules[0]["Name"] != "checkout error rate" || rules[0]["Metric"] != "error_rate" {
		t.Errorf("alert rule incomplete: %v", rules[0])
	}

	dashboards := readJSONRecords(t, filepath.Join(outDir, "config", "dashboards.json"))
	if len(dashboards) != 1 {
		t.Fatalf("exported %d dashboards, want 1", len(dashboards))
	}
	if dashboards[0]["Name"] != "checkout health" {
		t.Errorf("dashboard incomplete: %v", dashboards[0])
	}
	// The layout is the part that makes a dashboard rebuildable elsewhere.
	if layout, _ := dashboards[0]["Config"].(string); !strings.Contains(layout, "error_rate") {
		t.Errorf("dashboard layout lost: %v", dashboards[0]["Config"])
	}
}

// AC-8: every command the generated README publishes is executed here. A
// published instruction that does not run is worse than none.
func TestReadmeCommandsRun(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip(duckDBMissing)
	}

	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	readme, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	for _, section := range []string{
		"## What is in here",
		"## Read it with DuckDB",
		"## Read it with pandas",
		"## Read the configuration",
		"## What was redacted, and how to get it",
		"## Verify nothing was lost",
	} {
		if !strings.Contains(string(readme), section) {
			t.Errorf("README is missing the required section %q", section)
		}
	}

	// Run every duckdb and sha256sum line the README publishes.
	var ran int
	for _, line := range strings.Split(string(readme), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "duckdb ") && !strings.HasPrefix(line, "sha256sum ") {
			continue
		}
		ran++

		cmd := exec.Command("sh", "-c", line)
		cmd.Dir = outDir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("README command failed\ncommand: %s\noutput: %s\nerror: %v", line, out, err)
			continue
		}
		if len(bytes.TrimSpace(out)) == 0 {
			t.Errorf("README command returned no data\ncommand: %s", line)
		}
	}
	if ran == 0 {
		t.Fatal("the README published no runnable commands")
	}
}

// AC-9
func TestChecksumsVerify(t *testing.T) {
	dataRoot, dbPath, tenantID := seedExitFixture(t)
	outDir := runExitPath(t, dataRoot, dbPath, tenantID)

	cmd := exec.Command("sha256sum", "-c", "checksums.txt")
	cmd.Dir = outDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sha256sum -c failed: %v\n%s", err, out)
	}

	// And it must actually cover the tree, not just verify an empty list.
	data, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 8 {
		t.Fatalf("checksums.txt covers only %d files", len(lines))
	}
	for _, want := range []string{"README.md", "MANIFEST.json", "config/team.json"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("checksums.txt does not cover %s", want)
		}
	}

	// A tampered file must fail verification, or the checksums prove nothing.
	readme := filepath.Join(outDir, "README.md")
	original, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if err := os.WriteFile(readme, append(original, []byte("\ntampered\n")...), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	tamperCmd := exec.Command("sha256sum", "-c", "checksums.txt")
	tamperCmd.Dir = outDir
	if err := tamperCmd.Run(); err == nil {
		t.Error("sha256sum -c passed after a file was modified")
	}
}

// AC-11
func TestNonEmptyDestinationRefused(t *testing.T) {
	dataRoot, dbPath, tenantID := seedExitFixture(t)

	outDir := filepath.Join(t.TempDir(), "exit")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "already-here"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	script := filepath.Join(exitRepoRoot, "scripts", "export_everything.sh")
	cmd := exec.Command("bash", script, "--out", outDir, "--from", "2026-01-01", "--to", "2026-01-03")
	cmd.Env = append(os.Environ(),
		"GRAVIX_DATA_ROOT="+dataRoot, "GRAVIX_TENANT_DB="+dbPath, "GRAVIX_TENANT_ID="+tenantID)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a non-empty destination was accepted:\n%s", out)
	}
	want := fmt.Sprintf("destination %s is not empty; refusing to overwrite an existing export", outDir)
	if !strings.Contains(string(out), want) {
		t.Errorf("output = %s\nwant it to contain %q", out, want)
	}
}

// AC-10: the exit path must not depend on anything under ee/.
func TestExitPathWorksWithoutEE(t *testing.T) {
	for _, name := range []string{
		filepath.Join(exitRepoRoot, "pkg", "export", "config.go"),
		filepath.Join(exitRepoRoot, "cmd", "cli", "cmd_export.go"),
		filepath.Join(exitRepoRoot, "scripts", "export_everything.sh"),
	} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "gravix-dashboards/ee/") {
			t.Errorf("%s imports from ee/; the exit path must work with ee/ deleted", name)
		}
	}

	// The build-with-ee-deleted guarantee is enforced repository-wide by
	// `make build-oss`, which the oss-integrity CI job runs on every commit.
	// This test pins the exit path's own files so a future import is caught
	// here, with a message naming the reason, rather than only in that job.
}

// AC-12: the exit path runs in CI on every commit.
func TestExitPathRunsInCI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(exitRepoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	workflow := string(data)

	if !strings.Contains(workflow, "tests/e2e") {
		t.Fatal("no CI job runs tests/e2e, so the exit path is not proved on every commit")
	}
	// The e2e job must install DuckDB, or the readable-with-Gravix-stopped
	// tests skip and the proof quietly stops happening.
	if !strings.Contains(workflow, "Install DuckDB CLI") {
		t.Error("the e2e job does not install DuckDB; the exit-path reads would skip")
	}
	if !strings.Contains(workflow, "Exit path") {
		t.Error("ci.yml has no step named for the exit path")
	}
}

func readExitManifest(t *testing.T, outDir string) struct {
	RowCounts map[string]int64 `json:"row_counts"`
	Redacted  []string         `json:"redacted_fields"`
} {
	t.Helper()
	var m struct {
		RowCounts map[string]int64 `json:"row_counts"`
		Redacted  []string         `json:"redacted_fields"`
	}
	data, err := os.ReadFile(filepath.Join(outDir, "MANIFEST.json"))
	if err != nil {
		t.Fatalf("read MANIFEST.json: %v", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse MANIFEST.json: %v", err)
	}
	return m
}

func readJSONRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}
