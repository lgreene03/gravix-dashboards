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
)

// writeImportFixture writes a Datadog export and returns its path. aggr and
// interval control whether Datadog declares the series pre-aggregated.
func writeImportFixture(t *testing.T, aggr string, interval int) string {
	t.Helper()

	type ddSeries struct {
		Metric   string       `json:"metric"`
		Tags     []string     `json:"tags"`
		Points   [][2]float64 `json:"pointlist"`
		Aggr     string       `json:"aggr"`
		Interval int          `json:"interval"`
	}
	export := struct {
		Series []ddSeries `json:"series"`
	}{Series: []ddSeries{{
		Metric:   "trace.http.request.hits",
		Tags:     []string{"service:checkout-prod", "resource:/orders/{id}", "method:GET"},
		Points:   [][2]float64{{1767225600000, 4201}, {1767225660000, 3117}},
		Aggr:     aggr,
		Interval: interval,
	}}}

	path := filepath.Join(t.TempDir(), "export.json")
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func runImportCmd(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = importMain(context.Background(), args, strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func importArgs(t *testing.T, input, root string, extra ...string) []string {
	t.Helper()
	return append([]string{
		"datadog",
		"--input", input,
		"--data-root", root,
		"--service-map", "checkout-prod=checkout",
	}, extra...)
}

// §6.1: facts mode over aggregates exits 2 with the exact sentence.
func TestImportRefusesFactsModeOverAggregates(t *testing.T) {
	input := writeImportFixture(t, "avg", 60)
	root := t.TempDir()

	code, _, stderr := runImportCmd(t, "", importArgs(t, input, root, "--mode", "facts", "--yes")...)

	if code != importExitUsage {
		t.Fatalf("exit = %d, want %d", code, importExitUsage)
	}
	want := "importer: datadog holds aggregates; facts mode would fabricate data that never existed. Use --mode metrics."
	if strings.TrimSpace(stderr) != want {
		t.Fatalf("stderr = %q\nwant   = %q", strings.TrimSpace(stderr), want)
	}
}

// AC-10: confirmation is required without --yes.
func TestImportRequiresConfirmation(t *testing.T) {
	cases := map[string]struct {
		stdin string
		code  int
	}{
		"declined with no":    {"no\n", importExitDeclined},
		"declined with empty": {"\n", importExitDeclined},
		"declined with y":     {"y\n", importExitDeclined},
		"declined with EOF":   {"", importExitDeclined},
		"accepted with yes":   {"yes\n", importExitOK},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			input := writeImportFixture(t, "avg", 60)
			root := t.TempDir()

			code, stdout, stderr := runImportCmd(t, tc.stdin, importArgs(t, input, root)...)

			if code != tc.code {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.code, stderr)
			}
			if !strings.Contains(stdout, "proceed with the import?") {
				t.Error("no confirmation was requested")
			}

			if tc.code == importExitDeclined {
				if !strings.Contains(stderr, "importer: aborted") {
					t.Errorf("stderr = %q, want the abort message", stderr)
				}
				assertNoWarehouse(t, root)
			}
		})
	}
}

// The report — limitations included — must be shown BEFORE the prompt. A price
// disclosed after the decision is not disclosed.
func TestImportShowsLimitationsBeforeAsking(t *testing.T) {
	input := writeImportFixture(t, "avg", 60)

	_, stdout, _ := runImportCmd(t, "no\n", importArgs(t, input, t.TempDir())...)

	limitations := strings.Index(stdout, "Imported partitions hold derived metrics, not facts.")
	prompt := strings.Index(stdout, "proceed with the import?")

	if limitations < 0 {
		t.Fatalf("the limitations were never shown:\n%s", stdout)
	}
	if prompt < 0 {
		t.Fatalf("no prompt was shown:\n%s", stdout)
	}
	if limitations > prompt {
		t.Error("the limitations were shown after the prompt")
	}

	for _, line := range []string{
		"gravix recompute cannot rebuild these partitions.",
		"gravix explain reports import provenance, not source facts.",
		"Adding a percentile or dimension retroactively does not apply to them.",
		"Percentiles carry the source system's accuracy, not Gravix's sketch bound.",
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("output is missing: %s", line)
		}
	}
}

// AC-9
func TestImportDryRunWritesNothingAndSaysSo(t *testing.T) {
	input := writeImportFixture(t, "avg", 60)
	root := t.TempDir()

	code, stdout, stderr := runImportCmd(t, "", importArgs(t, input, root, "--dry-run")...)

	if code != importExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "dry run: nothing was written") {
		t.Errorf("stdout = %q", stdout)
	}
	// A dry run must not prompt: there is nothing to confirm.
	if strings.Contains(stdout, "proceed with the import?") {
		t.Error("a dry run asked for confirmation")
	}
	assertNoWarehouse(t, root)
}

func TestImportWritesPartitionsOnConfirmation(t *testing.T) {
	input := writeImportFixture(t, "avg", 60)
	root := t.TempDir()

	code, stdout, stderr := runImportCmd(t, "yes\n", importArgs(t, input, root)...)
	if code != importExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "imported 1 series as 2 rows") {
		t.Errorf("stdout = %q", stdout)
	}

	var parquet, manifests int
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".parquet"):
			parquet++
		case strings.HasSuffix(path, ".manifest.json"):
			manifests++
		}
		return nil
	})
	if parquet != 1 || manifests != 1 {
		t.Fatalf("wrote %d parquet and %d manifest files, want 1 each", parquet, manifests)
	}
}

func TestImportRejectsBadFlags(t *testing.T) {
	input := writeImportFixture(t, "avg", 60)
	root := t.TempDir()

	cases := map[string][]string{
		"no source":        {},
		"unknown source":   {"newrelic", "--input", input, "--data-root", root, "--yes"},
		"no input":         {"datadog", "--data-root", root, "--yes"},
		"bad service map":  {"datadog", "--input", input, "--data-root", root, "--service-map", "nonsense", "--yes"},
		"unparseable from": {"datadog", "--input", input, "--data-root", root, "--from", "last tuesday", "--yes"},
		"unparseable to":   {"datadog", "--input", input, "--data-root", root, "--to", "soon", "--yes"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runImportCmd(t, "", args...)
			if code != importExitUsage {
				t.Fatalf("exit = %d, want %d (usage)\nstderr: %s", code, importExitUsage, stderr)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Error("a usage failure said nothing about what was wrong")
			}
		})
	}
}

// SD-030: the Prometheus reader is blocked on an owner decision. It must say
// so, not fail in a way that looks like the user's mistake.
func TestImportPrometheusReportsTheOpenDecision(t *testing.T) {
	input := writeImportFixture(t, "avg", 60)

	code, _, stderr := runImportCmd(t, "",
		"prometheus", "--input", input, "--data-root", t.TempDir(),
		"--service-map", "checkout-prod=checkout", "--yes")

	if code != importExitUsage {
		t.Fatalf("exit = %d, want %d", code, importExitUsage)
	}
	if !strings.Contains(stderr, "SD-030") {
		t.Errorf("stderr = %q, want it to name the open decision", stderr)
	}
}

func TestParseServiceMap(t *testing.T) {
	got, err := parseServiceMap("a=b, c=d ,e=f")
	if err != nil {
		t.Fatalf("parseServiceMap: %v", err)
	}
	want := map[string]string{"a": "b", "c": "d", "e": "f"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if m, err := parseServiceMap("  "); err != nil || m != nil {
		t.Errorf("empty map = (%v, %v), want (nil, nil)", m, err)
	}

	for _, bad := range []string{"nopair", "=b", "a=", "a=b,broken"} {
		if _, err := parseServiceMap(bad); err == nil {
			t.Errorf("parseServiceMap(%q) accepted a malformed pair", bad)
		}
	}
}

func assertNoWarehouse(t *testing.T, root string) {
	t.Helper()
	var written []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		written = append(written, rel)
		return nil
	})
	if len(written) != 0 {
		t.Fatalf("expected nothing written, got %v", written)
	}
}
