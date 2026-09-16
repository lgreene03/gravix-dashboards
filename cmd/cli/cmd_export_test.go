// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedExportRoot builds a data root holding two days of raw facts, in the
// layout ingestion writes.
func seedExportRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	for _, day := range []string{"2026-01-01", "2026-01-02"} {
		dir := filepath.Join(root, "raw", "request_facts", day, "00")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		var buf bytes.Buffer
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
			line, err := json.Marshal(fact)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			buf.Write(line)
			buf.WriteByte('\n')
		}

		if err := os.WriteFile(filepath.Join(dir, "batch.jsonl"), buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	return root
}

func runExportCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = exportMain(context.Background(), args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestExportCommandWritesFilesAndManifest(t *testing.T) {
	root := seedExportRoot(t)
	outDir := t.TempDir()

	code, stdout, stderr := runExportCmd(t,
		"--dataset", "facts",
		"--format", "csv",
		"--from", "2026-01-01",
		"--to", "2026-01-03",
		"--out", "file://"+outDir,
		"--data-root", root,
	)

	if code != exportExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, exportExitOK, stderr)
	}
	if !strings.Contains(stdout, "exported 10 rows") {
		t.Errorf("stdout = %q, want a row count", stdout)
	}
	// The instruction for opening the export must reach the user who has not
	// opened the manifest yet.
	if !strings.Contains(stdout, "read it with:") || !strings.Contains(stdout, "read_csv_auto") {
		t.Errorf("stdout does not tell the user how to read the export:\n%s", stdout)
	}

	for _, name := range []string{"facts_20260101.csv", "facts_20260102.csv", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
}

func TestExportCommandRequiresBothRangeEnds(t *testing.T) {
	root := seedExportRoot(t)

	cases := map[string][]string{
		"no from": {"--to", "2026-01-03", "--data-root", root},
		"no to":   {"--from", "2026-01-01", "--data-root", root},
		"neither": {"--data-root", root},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runExportCmd(t, args...)
			if code != exportExitUsage {
				t.Fatalf("exit = %d, want %d", code, exportExitUsage)
			}
			if !strings.Contains(stderr, "both --from and --to are required") {
				t.Errorf("stderr = %q", stderr)
			}
		})
	}
}

func TestExportCommandRejectsBadFlags(t *testing.T) {
	root := seedExportRoot(t)
	base := []string{"--from", "2026-01-01", "--to", "2026-01-03", "--data-root", root, "--out", "file://" + t.TempDir()}

	cases := map[string][]string{
		"unknown dataset":  append([]string{"--dataset", "traces"}, base...),
		"unknown format":   append([]string{"--format", "avro"}, base...),
		"reversed range":   {"--from", "2026-01-03", "--to", "2026-01-01", "--data-root", root, "--out", "file://" + t.TempDir()},
		"bad destination":  {"--from", "2026-01-01", "--to", "2026-01-03", "--data-root", root, "--out", "ftp://host/x"},
		"unparseable from": {"--from", "last tuesday", "--to", "2026-01-03", "--data-root", root},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runExportCmd(t, args...)
			if code != exportExitUsage {
				t.Fatalf("exit = %d, want %d (usage)\nstderr: %s", code, exportExitUsage, stderr)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Error("a usage failure said nothing about what was wrong")
			}
		})
	}
}

func TestExportCommandReportsNoDataDistinctly(t *testing.T) {
	root := seedExportRoot(t)

	code, _, stderr := runExportCmd(t,
		"--dataset", "facts",
		"--from", "2025-01-01",
		"--to", "2025-01-03",
		"--data-root", root,
		"--out", "file://"+t.TempDir(),
	)

	// An empty range is not a usage error and not a crash: it is its own
	// outcome, so a script can tell the three apart.
	if code != exportExitNoData {
		t.Fatalf("exit = %d, want %d", code, exportExitNoData)
	}
	if !strings.Contains(stderr, "no data in range") {
		t.Errorf("stderr = %q", stderr)
	}
}

// A stdout export IS the data stream. The summary must go to stderr, or it
// corrupts the file the user is piping.
func TestExportCommandKeepsStdoutCleanForPiping(t *testing.T) {
	root := seedExportRoot(t)

	stdoutFile := filepath.Join(t.TempDir(), "captured")
	f, err := os.Create(stdoutFile)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	realStdout := os.Stdout
	os.Stdout = f
	code, cmdStdout, stderr := runExportCmd(t,
		"--dataset", "facts",
		"--format", "jsonl",
		"--from", "2026-01-01",
		"--to", "2026-01-03",
		"--out", "-",
		"--data-root", root,
	)
	os.Stdout = realStdout
	f.Close()

	if code != exportExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	if strings.TrimSpace(cmdStdout) != "" {
		t.Errorf("the summary was written to the data stream: %q", cmdStdout)
	}
	if !strings.Contains(stderr, "exported 10 rows") {
		t.Errorf("the summary did not go to stderr: %q", stderr)
	}

	data, err := os.ReadFile(stdoutFile)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 10 {
		t.Fatalf("piped stream has %d lines, want 10 JSONL records", len(lines))
	}
	for i, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("piped line %d is not valid JSON — the summary leaked into the stream: %q", i, line)
		}
	}
}

func TestParseExportTimeAcceptsBothDocumentedForms(t *testing.T) {
	cases := map[string]time.Time{
		"2026-01-01":           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"2026-01-01T06:30:00Z": time.Date(2026, 1, 1, 6, 30, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := parseExportTime(in)
		if err != nil {
			t.Errorf("parseExportTime(%q): %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseExportTime(%q) = %v, want %v", in, got, want)
		}
	}

	if _, err := parseExportTime("not a date"); err == nil {
		t.Error("parseExportTime accepted nonsense")
	}
}

// The CLI must expose no filter or query flag: export takes a range and a
// dataset (non-goal §5).
func TestExportCommandExposesNoQueryFlag(t *testing.T) {
	_, _, stderr := runExportCmd(t, "--help")

	for _, banned := range []string{"--filter", "--where", "--query", "--sql", "--limit", "--select"} {
		if strings.Contains(stderr, banned) {
			t.Errorf("export exposes %s; it takes a range and a dataset, never a query", banned)
		}
	}

	// Every flag §5.3 promises must be there.
	for _, want := range []string{"-dataset", "-format", "-from", "-to", "-out", "-tenant", "-compress"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("export is missing the documented flag %s\nusage:\n%s", want, stderr)
		}
	}
}
