// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// seedRecomputeFacts writes a day of facts into a fresh ./data tree and returns
// the day it wrote. The test runs inside its own directory so both the objects
// and the lock file stay there.
func seedRecomputeFacts(t *testing.T) time.Time {
	t.Helper()
	t.Chdir(t.TempDir())

	store, err := storage.NewLocalStore(dataRoot)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	d := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	at := d.Add(10*time.Hour + 30*time.Minute)

	var buf bytes.Buffer
	for _, f := range []*gravixv1.RequestFact{
		newFact(t, "api", "GET", "/users/{id}", 200, 12, at),
		newFact(t, "api", "GET", "/users/{id}", 500, 88, at.Add(time.Second)),
		newFact(t, "billing", "POST", "/invoices/{id}", 201, 40, at.Add(2*time.Second)),
	} {
		data, err := protojson.Marshal(f)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	key := fmt.Sprintf("raw/request_facts/%s/10/batch.jsonl", d.Format("2006-01-02"))
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put facts: %v", err)
	}
	return d
}

func newFact(t *testing.T, service, method, path string, status, latency int32, at time.Time) *gravixv1.RequestFact {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return &gravixv1.RequestFact{
		EventId:      id.String(),
		EventTime:    timestamppb.New(at),
		Service:      service,
		Method:       method,
		PathTemplate: path,
		StatusCode:   status,
		LatencyMs:    latency,
	}
}

// runCLI invokes the subcommand and returns its exit code with both streams.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := recomputeMain(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRecomputeCLIDryRunReport(t *testing.T) {
	seedRecomputeFacts(t)

	code, stdout, stderr := runCLI(t, "--from", "2026-09-09", "--to", "2026-09-10", "--dry-run")
	if code != recomputeExitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("report has %d lines, want 4:\n%s", len(lines), stdout)
	}
	wantPrefixes := []string{
		"recompute: request_metrics_minute 2026-09-09T00:00:00Z .. 2026-09-10T00:00:00Z",
		"partitions: 1   rebuilt: 1   unchanged: 0",
		"facts read: 3   rows written: 2",
		"duration: ",
	}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want prefix %q", i+1, lines[i], want)
		}
	}

	// A dry run must produce no output. It still takes the rollup lock, which
	// creates the metric directory, so look for written files rather than dirs.
	written, err := filepath.Glob(filepath.Join(dataRoot, "warehouse", "**", "*", "*.parquet"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("dry run wrote %v, want nothing", written)
	}
}

func TestRecomputeCLIWritesAndThenReportsUnchanged(t *testing.T) {
	d := seedRecomputeFacts(t)

	code, stdout, stderr := runCLI(t, "--from", "2026-09-09", "--to", "2026-09-10")
	if code != recomputeExitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "rebuilt: 1") {
		t.Errorf("first run report = %q, want rebuilt: 1", stdout)
	}

	want := filepath.Join(dataRoot,
		recompute.DeterministicKey(
			recompute.PartitionDir("./data/warehouse/request_metrics_minute", d),
			recompute.MetricRequestMinute, d))
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected output at %s: %v", want, err)
	}

	code, stdout, stderr = runCLI(t, "--from", "2026-09-09", "--to", "2026-09-10")
	if code != recomputeExitOK {
		t.Fatalf("second exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "rebuilt: 0   unchanged: 1") {
		t.Errorf("second run report = %q, want rebuilt: 0 unchanged: 1", stdout)
	}
}

func TestRecomputeCLIRejectsInvertedWindow(t *testing.T) {
	seedRecomputeFacts(t)

	code, stdout, stderr := runCLI(t, "--from", "2026-09-02", "--to", "2026-09-01")
	if code != recomputeExitUsage {
		t.Errorf("exit = %d, want %d", code, recomputeExitUsage)
	}
	if want := "recompute: window To must be after From"; strings.TrimSpace(stderr) != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestRecomputeCLIRejectsUnparseableDates(t *testing.T) {
	seedRecomputeFacts(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "bad from",
			args: []string{"--from", "yesterday", "--to", "2026-09-10"},
			want: `recompute: cannot parse --from "yesterday": want RFC3339 or YYYY-MM-DD`,
		},
		{
			name: "bad to",
			args: []string{"--from", "2026-09-09", "--to", "09/10/2026"},
			want: `recompute: cannot parse --to "09/10/2026": want RFC3339 or YYYY-MM-DD`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, tc.args...)
			if code != recomputeExitUsage {
				t.Errorf("exit = %d, want %d", code, recomputeExitUsage)
			}
			if strings.TrimSpace(stderr) != tc.want {
				t.Errorf("stderr = %q, want %q", stderr, tc.want)
			}
		})
	}
}

func TestRecomputeCLIRequiresWindowFlags(t *testing.T) {
	seedRecomputeFacts(t)

	for _, args := range [][]string{
		{},
		{"--from", "2026-09-09"},
		{"--to", "2026-09-10"},
	} {
		code, _, stderr := runCLI(t, args...)
		if code != recomputeExitUsage {
			t.Errorf("args %v: exit = %d, want %d", args, code, recomputeExitUsage)
		}
		if !strings.Contains(stderr, "--from and --to are required") {
			t.Errorf("args %v: stderr = %q, want the required-flags message", args, stderr)
		}
	}
}

func TestRecomputeCLIRejectsUnknownMetric(t *testing.T) {
	seedRecomputeFacts(t)

	code, _, stderr := runCLI(t, "--from", "2026-09-09", "--to", "2026-09-10", "--metric", "cpu_seconds")
	if code != recomputeExitUsage {
		t.Errorf("exit = %d, want %d", code, recomputeExitUsage)
	}
	if want := `recompute: unknown metric "cpu_seconds"`; strings.TrimSpace(stderr) != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestRecomputeCLIRejectsUnknownFlag(t *testing.T) {
	seedRecomputeFacts(t)

	code, _, _ := runCLI(t, "--from", "2026-09-09", "--to", "2026-09-10", "--turbo")
	if code != recomputeExitUsage {
		t.Errorf("exit = %d, want %d", code, recomputeExitUsage)
	}
}

func TestRecomputeCLIExitsThreeWhenLocked(t *testing.T) {
	seedRecomputeFacts(t)

	lockDir := recompute.MetricDirFor("./data/warehouse", "", recompute.MetricRequestMinute)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lockPath := filepath.Join(lockDir, "."+recompute.LockName+".lock")
	held := fmt.Sprintf("pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(lockPath, []byte(held), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	code, _, stderr := runCLI(t, "--from", "2026-09-09", "--to", "2026-09-10")
	if code != recomputeExitLocked {
		t.Errorf("exit = %d, want %d", code, recomputeExitLocked)
	}
	if want := "recompute: another rollup or recompute holds the lock"; strings.TrimSpace(stderr) != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestRecomputeCLIAcceptsRFC3339Window(t *testing.T) {
	seedRecomputeFacts(t)

	code, stdout, stderr := runCLI(t,
		"--from", "2026-09-09T00:00:00Z", "--to", "2026-09-09T12:00:00Z", "--dry-run")
	if code != recomputeExitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "partitions: 1") {
		t.Errorf("report = %q, want one partition", stdout)
	}
}

func TestRecomputeCLITenantFlagIsRepeatable(t *testing.T) {
	seedRecomputeFacts(t)

	code, stdout, stderr := runCLI(t,
		"--from", "2026-09-09", "--to", "2026-09-10",
		"--tenant", "acme", "--tenant", "globex", "--dry-run")
	if code != recomputeExitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "partitions: 2") {
		t.Errorf("report = %q, want two partitions", stdout)
	}
}

func TestTenantListFlag(t *testing.T) {
	var list tenantList
	if err := list.Set("acme"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := list.Set("globex"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := list.String(); got != "acme,globex" {
		t.Errorf("String() = %q, want %q", got, "acme,globex")
	}
	if err := list.Set(""); err == nil {
		t.Error("Set(\"\") = nil, want an error — an empty tenant id is not single-tenant mode")
	}
}

func TestParseWindowBound(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "2026-09-09", want: "2026-09-09T00:00:00Z"},
		{in: "2026-09-09T13:45:00Z", want: "2026-09-09T13:45:00Z"},
		{in: "2026-09-09T08:45:00-05:00", want: "2026-09-09T13:45:00Z"},
		{in: "not a date", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := parseWindowBound(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseWindowBound(%q) = %v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWindowBound(%q): %v", tc.in, err)
			continue
		}
		if g := got.Format(time.RFC3339); g != tc.want {
			t.Errorf("parseWindowBound(%q) = %s, want %s", tc.in, g, tc.want)
		}
	}
}

func TestOpenRecomputeStoreUsesLocalRootByDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("S3_ENDPOINT", "")

	store, err := openRecomputeStore(context.Background())
	if err != nil {
		t.Fatalf("openRecomputeStore: %v", err)
	}
	if _, ok := store.(*storage.LocalStore); !ok {
		t.Errorf("store = %T, want *storage.LocalStore", store)
	}
	if _, err := os.Stat(dataRoot); err != nil {
		t.Errorf("data root was not created: %v", err)
	}
}
