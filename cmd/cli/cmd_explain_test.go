// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/lineage"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	explainDay    = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	explainBucket = time.Date(2026, 9, 9, 14, 23, 0, 0, time.UTC)
)

// contractsDir resolves the repository's contracts directory before any chdir.
var contractsDir = func() string {
	d, err := filepathAbs("../../contracts")
	if err != nil {
		panic(err)
	}
	return d
}()

func filepathAbs(p string) (string, error) { return filepath.Abs(p) }

// seedExplainFixture writes facts and builds the partition they roll up into.
func seedExplainFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())

	store, err := storage.NewLocalStore(dataRoot)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	var buf bytes.Buffer
	for i, spec := range []struct {
		service, method, path string
		status, latency       int32
	}{
		{"api", "GET", "/users/{id}", 200, 10},
		{"api", "GET", "/users/{id}", 500, 84},
		{"api", "GET", "/users/{id}", 200, 22},
		{"billing", "POST", "/invoices/{id}", 201, 33},
	} {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		fact := &gravixv1.RequestFact{
			EventId:      id.String(),
			EventTime:    timestamppb.New(explainBucket.Add(time.Duration(i) * time.Second)),
			Service:      spec.service,
			Method:       spec.method,
			PathTemplate: spec.path,
			StatusCode:   spec.status,
			LatencyMs:    spec.latency,
		}
		data, err := protojson.Marshal(fact)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := store.Put(ctx, "raw/request_facts/2026-09-09/14/facts.jsonl", bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: "./data/raw", OutputDir: "./data/warehouse",
		Window: recompute.Window{From: explainDay, To: explainDay.AddDate(0, 0, 1)},
	}); err != nil {
		t.Fatalf("build partition: %v", err)
	}
}

func runExplainCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	args = append(args, "--contracts", contractsDir)
	var stdout, stderr bytes.Buffer
	code := explainMain(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestExplainCLIReport(t *testing.T) {
	seedExplainFixture(t)

	code, stdout, stderr := runExplainCLI(t,
		"request_metrics_minute", "2026-09-09 14:23", "--filter", "service=api")
	if code != explainExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}

	// Every line of the §5.3 report shape must be present, in order.
	wanted := []string{
		"metric:", "bucket:", "filters:", "values:",
		"formula:", "grain:", "exactness:", "mergeability:",
		"derived from:", "data file:", "idempotency:", "digest:", "revision:", "reproduce:",
	}
	pos := 0
	for _, w := range wanted {
		idx := strings.Index(stdout[pos:], w)
		if idx < 0 {
			t.Errorf("report is missing %q, or it is out of order:\n%s", w, stdout)
			continue
		}
		pos += idx
	}

	if !strings.Contains(stdout, "request_count=3") {
		t.Errorf("values line is wrong:\n%s", stdout)
	}
	if !strings.Contains(stdout, "gravix recompute --metric request_metrics_minute") {
		t.Errorf("no reproduce command:\n%s", stdout)
	}
	if !strings.Contains(stdout, "facts.jsonl") {
		t.Errorf("no source fact file named:\n%s", stdout)
	}
}

func TestExplainCLIJSON(t *testing.T) {
	seedExplainFixture(t)

	code, stdout, stderr := runExplainCLI(t,
		"request_metrics_minute", "2026-09-09 14:23", "--filter", "service=api", "--json")
	if code != explainExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}

	var l lineage.Lineage
	if err := json.Unmarshal([]byte(stdout), &l); err != nil {
		t.Fatalf("--json output does not unmarshal into Lineage: %v\n%s", err, stdout)
	}
	if l.IdempotencyKey == "" || l.ContentDigest == "" || l.RecomputeCmd == "" {
		t.Errorf("JSON output is missing fields: %+v", l)
	}
	if len(l.SourceFactKeys) == 0 {
		t.Error("JSON output names no source fact files")
	}
}

func TestExplainCLIRejectsNonDimensionFilter(t *testing.T) {
	seedExplainFixture(t)

	for _, f := range []string{"event_id=abc", "user_id=42", "latency_ms=10"} {
		t.Run(f, func(t *testing.T) {
			code, _, stderr := runExplainCLI(t,
				"request_metrics_minute", "2026-09-09 14:23", "--filter", f)
			if code != explainExitUsage {
				t.Errorf("exit = %d, want %d", code, explainExitUsage)
			}
			if !strings.Contains(stderr, "is not a dimension") {
				t.Errorf("stderr = %q, want the non-dimension refusal", stderr)
			}
		})
	}
}

func TestExplainCLINoManifestExitsFour(t *testing.T) {
	seedExplainFixture(t)

	store, err := storage.NewLocalStore(dataRoot)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	metricDir := recompute.MetricDirFor("./data/warehouse", "", recompute.MetricRequestMinute)
	dataKey := recompute.DeterministicKey(recompute.PartitionDir(metricDir, explainDay),
		recompute.MetricRequestMinute, explainDay)
	if err := store.Delete(context.Background(), manifest.Path(dataKey)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	code, _, stderr := runExplainCLI(t, "request_metrics_minute", "2026-09-09 14:23")
	if code != explainExitNoManifest {
		t.Fatalf("exit = %d, want %d — a missing manifest is its own recoverable state", code, explainExitNoManifest)
	}
	for _, want := range []string{
		"lineage unavailable",
		"before manifests existed",
		"data file:",
		"metric version: unknown",
		"gravix recompute",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestExplainCLIUnparseableBucket(t *testing.T) {
	seedExplainFixture(t)

	code, _, stderr := runExplainCLI(t, "request_metrics_minute", "yesterday afternoon")
	if code != explainExitUsage {
		t.Errorf("exit = %d, want %d", code, explainExitUsage)
	}
	if !strings.Contains(stderr, `want RFC3339 or "YYYY-MM-DD HH:MM"`) {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestExplainCLIMissingArguments(t *testing.T) {
	seedExplainFixture(t)

	code, _, stderr := runExplainCLI(t, "request_metrics_minute")
	if code != explainExitUsage {
		t.Errorf("exit = %d, want %d", code, explainExitUsage)
	}
	if !strings.Contains(stderr, "want <metric> and <bucket>") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestExplainCLINoPartition(t *testing.T) {
	seedExplainFixture(t)

	code, _, stderr := runExplainCLI(t, "request_metrics_minute", "2026-01-02 10:00")
	if code != explainExitNotFound {
		t.Errorf("exit = %d, want %d", code, explainExitNotFound)
	}
	if !strings.Contains(stderr, "no partition covers") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestExplainCLIAcceptsRFC3339Bucket(t *testing.T) {
	seedExplainFixture(t)

	code, stdout, stderr := runExplainCLI(t, "request_metrics_minute", "2026-09-09T14:23:00Z")
	if code != explainExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if !strings.Contains(stdout, "2026-09-09T14:23:00Z") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestParseBucket(t *testing.T) {
	want := time.Date(2026, 9, 9, 14, 23, 0, 0, time.UTC)
	for _, in := range []string{
		"2026-09-09T14:23:00Z",
		"2026-09-09 14:23",
		"2026-09-09 14:23:00",
	} {
		got, err := parseBucket(in)
		if err != nil {
			t.Errorf("parseBucket(%q): %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseBucket(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "nonsense", "2026-09-09"} {
		if _, err := parseBucket(in); err == nil {
			t.Errorf("parseBucket(%q) was accepted", in)
		}
	}
}

func TestFilterListFlag(t *testing.T) {
	f := filterList{}
	if err := f.Set("service=api"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := f.Set("method=GET"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := f.String(); got != "method=GET,service=api" {
		t.Errorf("String() = %q, want a sorted rendering", got)
	}
	for _, bad := range []string{"noequals", "=novalue"} {
		if err := f.Set(bad); err == nil {
			t.Errorf("Set(%q) was accepted", bad)
		}
	}
	if got := (filterList{}).String(); got != "" {
		t.Errorf("empty String() = %q", got)
	}
}

func TestFormatValuesOrdersMeasuresFirst(t *testing.T) {
	got := formatValues(map[string]any{
		"service":       "api",
		"error_rate":    0.25,
		"request_count": int64(4),
		"error_count":   int64(1),
	})
	if !strings.HasPrefix(got, "request_count=4  error_count=1  error_rate=0.25") {
		t.Errorf("formatValues = %q, want measures first in a fixed order", got)
	}
	if !strings.Contains(got, "service=api") {
		t.Errorf("formatValues = %q, want dimensions after the measures", got)
	}
}

func TestSplitExplainArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantPositional []string
		wantFlags      []string
	}{
		{
			name:           "flags after positionals",
			args:           []string{"metric", "bucket", "--filter", "service=api", "--json"},
			wantPositional: []string{"metric", "bucket"},
			wantFlags:      []string{"--filter", "service=api", "--json"},
		},
		{
			name:           "flags before positionals",
			args:           []string{"--json", "--tenant", "acme", "metric", "bucket"},
			wantPositional: []string{"metric", "bucket"},
			wantFlags:      []string{"--json", "--tenant", "acme"},
		},
		{
			name:           "flags on both sides",
			args:           []string{"--json", "metric", "--tenant", "acme", "bucket"},
			wantPositional: []string{"metric", "bucket"},
			wantFlags:      []string{"--json", "--tenant", "acme"},
		},
		{
			name:           "equals form does not swallow the next token",
			args:           []string{"--tenant=acme", "metric", "bucket"},
			wantPositional: []string{"metric", "bucket"},
			wantFlags:      []string{"--tenant=acme"},
		},
		{
			name:           "a bucket containing a space is one token",
			args:           []string{"metric", "2026-09-09 14:23"},
			wantPositional: []string{"metric", "2026-09-09 14:23"},
			wantFlags:      nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPos, gotFlags := splitExplainArgs(tc.args)
			if strings.Join(gotPos, "|") != strings.Join(tc.wantPositional, "|") {
				t.Errorf("positional = %v, want %v", gotPos, tc.wantPositional)
			}
			if strings.Join(gotFlags, "|") != strings.Join(tc.wantFlags, "|") {
				t.Errorf("flags = %v, want %v", gotFlags, tc.wantFlags)
			}
		})
	}
}

func TestExplainCLIFlagsBeforePositionals(t *testing.T) {
	seedExplainFixture(t)

	// Both orders must work, or the tool is annoying for no reason.
	code, stdout, stderr := runExplainCLI(t,
		"--filter", "service=api", "request_metrics_minute", "2026-09-09 14:23")
	if code != explainExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if !strings.Contains(stdout, "filters:") {
		t.Errorf("the filter was not applied:\n%s", stdout)
	}
}
