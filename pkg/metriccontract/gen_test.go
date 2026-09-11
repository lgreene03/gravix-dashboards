// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package metriccontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func generatedDocPath() string { return repoFile("docs", "02-derived-metrics.md") }

func readGeneratedDoc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(generatedDocPath())
	if err != nil {
		t.Fatalf("read generated doc: %v", err)
	}
	return string(data)
}

// ─── AC-11, AC-12: the document cannot drift from the registry ───

func TestGeneratedDocIsCurrent(t *testing.T) {
	want := Render(loadReal(t))
	got := readGeneratedDoc(t)

	if got != want {
		// Show the first differing line rather than two full documents.
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
			var g, w string
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if g != w {
				t.Fatalf("docs/02-derived-metrics.md is stale at line %d\n committed: %q\n generated: %q\n"+
					"Run 'make contracts' and commit the result.", i+1, g, w)
			}
		}
		t.Fatal("documents differ in length but not in any line")
	}
}

func TestGeneratedDocHasMarker(t *testing.T) {
	doc := readGeneratedDoc(t)

	first := strings.SplitN(doc, "\n", 2)[0]
	if first != GeneratedMarker {
		t.Fatalf("first line = %q, want %q", first, GeneratedMarker)
	}
	if !strings.Contains(first, "DO NOT EDIT") {
		t.Error("the marker does not warn against editing")
	}
}

// ─── AC-13: nothing was lost when the prose doc became generated ───

// priorDocMetrics are the metrics described in docs/02-derived-metrics.md before
// GRVX-803 made it generated output. The prose used a different naming convention
// for the percentiles, so each is mapped to the contract that now carries it.
//
// Note p99: the prose doc never documented it, although the rollup has always
// computed it and the Cube model has always exposed it. The registry closes that
// gap rather than inheriting it.
var priorDocMetrics = map[string]string{
	"request_count": "request_count",
	"error_count":   "error_count",
	"error_rate":    "error_rate",
	"p50_latency":   "latency_p50",
	"p95_latency":   "latency_p95",
}

func TestNoMetricLostFromPriorDoc(t *testing.T) {
	reg := loadReal(t)

	for prose, contract := range priorDocMetrics {
		if _, err := reg.Latest(contract); err != nil {
			t.Errorf("metric %q from the prose doc has no contract (expected %q): %v", prose, contract, err)
		}
	}

	// Every prior metric must also still be described in the generated doc, so a
	// reader who knew the old page still finds what they came for.
	doc := readGeneratedDoc(t)
	for prose, contract := range priorDocMetrics {
		if !strings.Contains(doc, "`"+contract+"`") {
			t.Errorf("the generated doc does not document %q (was %q in the prose doc)", contract, prose)
		}
	}

	// The prose doc's other content — bucketing, late arrival, recomputation —
	// must survive somewhere. The recompute command moved to the operations doc.
	ops, err := os.ReadFile(repoFile("docs", "06-operations.md"))
	if err != nil {
		t.Fatalf("read operations doc: %v", err)
	}
	if !strings.Contains(string(ops), "gravix recompute") {
		t.Error("the gravix recompute reference was dropped rather than moved to docs/06-operations.md")
	}

	for _, subject := range []string{"Late data", "Grain", "Aggregating it"} {
		if !strings.Contains(doc, subject) {
			t.Errorf("the generated doc covers no %q, which the prose doc did", subject)
		}
	}
}

// ─── AC-14: every recompute command actually runs ───

func TestRecomputeCommandsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI; skipped under -short")
	}
	reg := loadReal(t)

	// Build the CLI once, then run each distinct command against it.
	bin := filepath.Join(t.TempDir(), "gravix")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cli")
	build.Dir = repoFile()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}

	seen := map[string]struct{}{}
	for _, c := range reg.Contracts {
		cmd := strings.TrimSpace(c.RecomputeCmd)
		if _, done := seen[cmd]; done {
			continue
		}
		seen[cmd] = struct{}{}

		t.Run(c.Name, func(t *testing.T) {
			fields := strings.Fields(cmd)
			if len(fields) == 0 || fields[0] != "gravix" {
				t.Fatalf("recompute_cmd = %q, want it to start with the gravix CLI", cmd)
			}

			// Run it for real, in an empty tree, with --dry-run appended so it
			// plans and reports without writing. Anything wrong with the flags,
			// the metric name or the dates shows up as a non-zero exit.
			args := append(fields[1:], "--dry-run")
			run := exec.Command(bin, args...)
			run.Dir = t.TempDir()
			out, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --dry-run exited non-zero: %v\n%s", cmd, err, out)
			}
			if !strings.Contains(string(out), "partitions:") {
				t.Errorf("%s produced no plan report:\n%s", cmd, out)
			}
		})
	}

	if len(seen) == 0 {
		t.Fatal("no recompute commands were tested")
	}
}

// ─── rendering details ───

func TestRenderIsDeterministic(t *testing.T) {
	reg := loadReal(t)
	first := Render(reg)
	for i := 0; i < 3; i++ {
		if again := Render(reg); again != first {
			t.Fatalf("render %d differs from the first", i)
		}
	}
}

func TestRenderEscapesTableCells(t *testing.T) {
	reg := &Registry{Contracts: []Contract{{
		Name: "m", Version: "v1", Title: "M",
		Formula:      "a | b",
		InputFacts:   []string{"RequestFact"},
		InputFields:  []string{"x"},
		Grain:        "1 minute",
		Exactness:    ExactnessExact,
		Mergeability: MergeabilitySum,
		RecomputeCmd: "gravix recompute --from 2026-09-01 --to 2026-09-02",
	}}}

	out := Render(reg)
	if !strings.Contains(out, `a \| b`) {
		t.Errorf("a pipe in a field was not escaped; it would split the table row:\n%s", out)
	}
}

func TestRenderFoldsMultilineNotes(t *testing.T) {
	reg := &Registry{Contracts: []Contract{{
		Name: "m", Version: "v1", Title: "M",
		Formula:      "COUNT(*)",
		InputFacts:   []string{"RequestFact"},
		InputFields:  []string{"x"},
		Grain:        "1 minute",
		Exactness:    ExactnessExact,
		Mergeability: MergeabilitySum,
		MergeNote:    "line one\nline two\n\nline three",
		RecomputeCmd: "gravix recompute --from 2026-09-01 --to 2026-09-02",
	}}}

	out := Render(reg)
	if !strings.Contains(out, "**Aggregating it.** line one line two line three") {
		t.Errorf("a multi-line note was not folded into one line:\n%s", out)
	}
}

func TestRenderEmptyRegistrySaysSoPlainly(t *testing.T) {
	out := Render(&Registry{})

	if !strings.HasPrefix(out, GeneratedMarker) {
		t.Error("the marker is missing from an empty registry's output")
	}
	if !strings.Contains(out, "## Known defects") {
		t.Error("the defects section is absent; its absence reads as nobody having looked")
	}
	if !strings.Contains(out, "None.") {
		t.Errorf("an empty registry should say plainly that there are no defects:\n%s", out)
	}
}

func TestRenderNamesTheFixingSpec(t *testing.T) {
	doc := readGeneratedDoc(t)

	defects := strings.SplitN(doc, "## Known defects", 2)
	if len(defects) != 2 {
		t.Fatal("no Known defects section")
	}
	if !strings.Contains(defects[1], "`GRVX-804`") {
		t.Errorf("the defects table does not name the spec that fixes them:\n%s", defects[1])
	}
	for _, name := range []string{"latency_p50", "latency_p95", "latency_p99"} {
		if !strings.Contains(defects[1], name) {
			t.Errorf("the defects table omits %s", name)
		}
	}
}

func TestFixedByFallsBackWhenNoSpecNamed(t *testing.T) {
	got := fixedBy(Contract{KnownDefect: "this is wrong and nobody owns it"})
	if got != "not yet assigned" {
		t.Errorf("fixedBy = %q, want %q", got, "not yet assigned")
	}
}

func TestCountWord(t *testing.T) {
	for n, want := range map[int]string{1: "One", 2: "Two", 3: "Three", 4: "4"} {
		if got := countWord(n); got != want {
			t.Errorf("countWord(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestInlineCellAndCodeListHandleEmpty(t *testing.T) {
	if got := inlineCell("   "); got != "—" {
		t.Errorf("inlineCell(blank) = %q, want an em dash", got)
	}
	if got := codeList(nil); got != "—" {
		t.Errorf("codeList(nil) = %q, want an em dash", got)
	}
	if got := codeList([]string{"a", "b"}); got != "`a`, `b`" {
		t.Errorf("codeList = %q, want backticked and comma separated", got)
	}
}

func TestRenderShowsSupersedesAndDeprecated(t *testing.T) {
	reg := &Registry{Contracts: []Contract{{
		Name: "m", Version: "v2", Title: "M",
		Formula: "COUNT(*)", InputFacts: []string{"RequestFact"}, InputFields: []string{"x"},
		Grain: "1 minute", Exactness: ExactnessExact, Mergeability: MergeabilitySum,
		Supersedes: "m@v1", Deprecated: true,
		RecomputeCmd: "gravix recompute --from 2026-09-01 --to 2026-09-02",
	}}}

	out := Render(reg)
	if !strings.Contains(out, "| **Supersedes** | `m@v1` |") {
		t.Errorf("Supersedes is not rendered:\n%s", out)
	}
	if !strings.Contains(out, "| **Deprecated** | yes |") {
		t.Errorf("Deprecated is not rendered:\n%s", out)
	}
}
