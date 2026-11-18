// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/boundary"
	"gopkg.in/yaml.v3"
)

const repoRoot = "../.."

func loadRealMap(t *testing.T) *boundary.Map {
	t.Helper()
	m, err := boundary.Load(filepath.Join(repoRoot, "docs/oss/boundary.yaml"))
	if err != nil {
		t.Fatalf("load real map: %v", err)
	}
	return m
}

func TestCheckBoundaryCleanRepo(t *testing.T) {
	m := loadRealMap(t)

	imports, err := checkImports(repoRoot)
	if err != nil {
		t.Fatalf("checkImports: %v", err)
	}
	if len(imports) != 0 {
		t.Errorf("core packages importing ee/: %v", imports)
	}

	gates, err := checkGates(repoRoot, m)
	if err != nil {
		t.Fatalf("checkGates: %v", err)
	}
	if len(gates) != 0 {
		t.Errorf("unmapped plan gates: %v", gates)
	}

	if v := checkMap("docs/oss/boundary.yaml", m); len(v) != 0 {
		t.Errorf("map violations: %v", v)
	}
}

// TestCheckImportsDetectsEEImport proves the checker bites. A checker that
// never fails provides no protection at all.
func TestCheckImportsDetectsEEImport(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg", "offender")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package offender\n\nimport _ \"github.com/lgreene/gravix-dashboards/ee/placeholder\"\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "bad.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := checkImports(dir)
	if err != nil {
		t.Fatalf("checkImports: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("violations = %d, want 1: %v", len(got), got)
	}
	if got[0].Kind != "import" {
		t.Errorf("Kind = %q, want import", got[0].Kind)
	}
	if !strings.Contains(got[0].Message, "ee/placeholder") {
		t.Errorf("message should name the import: %q", got[0].Message)
	}
	if got[0].Line != 3 {
		t.Errorf("Line = %d, want 3", got[0].Line)
	}
}

func TestCheckImportsIgnoresEETree(t *testing.T) {
	dir := t.TempDir()
	eeDir := filepath.Join(dir, "ee", "feature")
	if err := os.MkdirAll(eeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// ee/ importing ee/ is entirely legitimate.
	src := "package feature\n\nimport _ \"github.com/lgreene/gravix-dashboards/ee/placeholder\"\n"
	if err := os.WriteFile(filepath.Join(eeDir, "ok.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := checkImports(dir)
	if err != nil {
		t.Fatalf("checkImports: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ee/ importing ee/ must not be a violation: %v", got)
	}
}

func TestCheckImportsIgnoresTestdata(t *testing.T) {
	// The real repo carries cmd/checkboundary/testdata/violating/importer.go,
	// which imports ee/ deliberately. It must not fail the real check.
	got, err := checkImports(repoRoot)
	if err != nil {
		t.Fatalf("checkImports: %v", err)
	}
	for _, v := range got {
		if strings.Contains(v.Path, "testdata") {
			t.Errorf("testdata fixture must be excluded, got %v", v)
		}
	}
}

func TestCheckGatesDetectsUngatedCall(t *testing.T) {
	dir := t.TempDir()
	svc := filepath.Join(dir, "services", "rogue")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package rogue\n\nfunc f(p string) bool {\n\treturn planRank[p] < planRank[\"pro\"]\n}\n"
	if err := os.WriteFile(filepath.Join(svc, "gate.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// An empty map claims no paths, so the gate is unmapped by construction.
	m := &boundary.Map{Version: boundary.Version}

	got, err := checkGates(dir, m)
	if err != nil {
		t.Fatalf("checkGates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("violations = %d, want 1: %v", len(got), got)
	}
	if got[0].Kind != "ungated" {
		t.Errorf("Kind = %q, want ungated", got[0].Kind)
	}
}

func TestCheckGatesAcceptsMappedCall(t *testing.T) {
	dir := t.TempDir()
	svc := filepath.Join(dir, "services", "mapped")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package mapped\n\nfunc f(p string) bool {\n\treturn planRank[p] < planRank[\"pro\"]\n}\n"
	if err := os.WriteFile(filepath.Join(svc, "gate.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &boundary.Map{Version: boundary.Version, Capabilities: []boundary.Capability{{
		ID: "mapped", Name: "Mapped", Placement: boundary.PlacementCore,
		CharterRef: "§2.1", Rationale: "r", Paths: []string{"services/mapped"},
	}}}

	got, err := checkGates(dir, m)
	if err != nil {
		t.Fatalf("checkGates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a mapped gate must not be a violation: %v", got)
	}
}

func TestCheckGatesIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	svc := filepath.Join(dir, "services", "x")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package x\n\nfunc f(p string) bool { return planRank[p] < planRank[\"pro\"] }\n"
	if err := os.WriteFile(filepath.Join(svc, "gate_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := checkGates(dir, &boundary.Map{Version: boundary.Version})
	if err != nil {
		t.Fatalf("checkGates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("_test.go files must be ignored: %v", got)
	}
}

func TestCheckMapSurfacesValidationErrors(t *testing.T) {
	bad := &boundary.Map{Version: 99}
	got := checkMap("x.yaml", bad)
	if len(got) == 0 {
		t.Fatal("an invalid map must produce violations")
	}
	if got[0].Kind != "map" {
		t.Errorf("Kind = %q, want map", got[0].Kind)
	}
}

func TestViolationString(t *testing.T) {
	withLine := Violation{Kind: "import", Path: "a/b.go", Line: 7, Message: "m"}
	if got, want := withLine.String(), "import: a/b.go:7: m"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	noLine := Violation{Kind: "header", Path: "a/b.go", Message: "m"}
	if got, want := noLine.String(), "header: a/b.go: m"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestIsExcludedPath(t *testing.T) {
	if !isExcludedPath("cmd/checkboundary/testdata") {
		t.Error("the testdata root must be excluded")
	}
	if !isExcludedPath(filepath.Join("cmd", "checkboundary", "testdata", "violating", "importer.go")) {
		t.Error("files under testdata must be excluded")
	}
	if isExcludedPath("cmd/checkboundary/main.go") {
		t.Error("main.go must not be excluded")
	}
}

// TestExistingMakeTargetsIntact guards the pre-existing developer workflow:
// GRVX-704 adds three targets and must not disturb any that were there.
func TestExistingMakeTargetsIntact(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, target := range []string{
		"build:", "build-cli:", "test:", "test-race:", "coverage:", "up:", "down:",
		"clean:", "lint:", "lint-all:", "helm-lint:", "purge:", "trino-init:", "docs:", "chaos:",
	} {
		if !strings.Contains(body, "\n"+target) {
			t.Errorf("pre-existing target %q is missing", target)
		}
	}
	for _, target := range []string{"build-oss:", "test-oss:", "check-boundary:"} {
		if !strings.Contains(body, "\n"+target) {
			t.Errorf("new target %q is missing", target)
		}
	}
}

// TestCIJobEnforcesBoundary asserts the gate is wired and cannot be soft-failed.
func TestCIJobEnforcesBoundary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "oss-integrity:") {
		t.Fatal("ci.yml has no oss-integrity job")
	}
	for _, cmd := range []string{"make check-boundary", "make build-oss", "make test-oss"} {
		if !strings.Contains(body, cmd) {
			t.Errorf("oss-integrity must run %q", cmd)
		}
	}
	// The whole point is that a violation blocks the merge. Check the job's own
	// keys rather than grepping the file, so a comment mentioning the phrase
	// elsewhere cannot fail — or pass — this assertion.
	var wf struct {
		Jobs map[string]struct {
			ContinueOnError *bool `yaml:"continue-on-error"`
			Steps           []struct {
				Name            string `yaml:"name"`
				Run             string `yaml:"run"`
				ContinueOnError *bool  `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	job, ok := wf.Jobs["oss-integrity"]
	if !ok {
		t.Fatal("oss-integrity job not found after parsing")
	}
	if job.ContinueOnError != nil && *job.ContinueOnError {
		t.Error("oss-integrity is continue-on-error; a boundary violation must block the merge")
	}
	var ran []string
	for _, st := range job.Steps {
		if st.ContinueOnError != nil && *st.ContinueOnError {
			t.Errorf("step %q is continue-on-error", st.Name)
		}
		if st.Run != "" {
			ran = append(ran, strings.TrimSpace(st.Run))
		}
	}
	for _, want := range []string{"make check-boundary", "make build-oss", "make test-oss"} {
		found := false
		for _, got := range ran {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("oss-integrity does not run %q (runs: %v)", want, ran)
		}
	}
}

// TestRunCleanRepoExitsZero exercises the full command path end to end.
func TestRunCleanRepoExitsZero(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	code := run(repoRoot, "docs/oss/boundary.yaml", out, out)
	if code != 0 {
		body, _ := os.ReadFile(out.Name())
		t.Fatalf("run() = %d, want 0\n%s", code, body)
	}
	body, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(body), "boundary: 0 violations") {
		t.Errorf("expected the zero-violations summary, got:\n%s", body)
	}
}

// TestRunMissingMapExitsOne proves a missing map is a failure, not a silent pass.
func TestRunMissingMapExitsOne(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	code := run(repoRoot, "docs/oss/does-not-exist.yaml", out, out)
	if code != 1 {
		t.Errorf("run() = %d, want 1 for a missing map", code)
	}
	body, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(body), "boundary: 1 violations") {
		t.Errorf("expected a violation summary, got:\n%s", body)
	}
}

// TestCheckHeadersOnCleanRepo confirms the header delegation reports nothing
// when every file already carries a header.
func TestCheckHeadersOnCleanRepo(t *testing.T) {
	got, err := checkHeaders(repoRoot)
	if err != nil {
		t.Fatalf("checkHeaders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unexpected header violations: %v", got)
	}
}

// headerSandbox builds a throwaway repository holding a copy of the real
// header script, so a test can plant files without touching this checkout.
func headerSandbox(t *testing.T) string {
	t.Helper()
	return headerSandboxAt(t, t.TempDir())
}

// headerSandboxAt builds the sandbox in dir, creating it if needed.
func headerSandboxAt(t *testing.T, dir string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "add_license_headers.sh"))
	if err != nil {
		t.Fatalf("read header script: %v", err)
	}
	writeSandboxFile(t, dir, "scripts/add_license_headers.sh", string(src))
	if err := os.Chmod(filepath.Join(dir, "scripts", "add_license_headers.sh"), 0o755); err != nil {
		t.Fatalf("chmod header script: %v", err)
	}
	return dir
}

// depth counts the path separators in an absolute path.
func depth(p string) int {
	return strings.Count(filepath.Clean(p), string(filepath.Separator))
}

func writeSandboxFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestCheckHeadersRunsTheScriptFromARelativeRoot guards the defect that made
// TestCheckHeadersOnCleanRepo pass without checking anything: given a relative
// root such as "../..", the script path was resolved against cmd.Dir a second
// time, the script never started, and that was read as "no missing headers".
func TestCheckHeadersRunsTheScriptFromARelativeRoot(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// The sandbox must sit deeper than this package's directory. Otherwise the
	// relative path climbs to "/" when resolved a second time against cmd.Dir,
	// the extra "../" steps are absorbed there, and it lands on the right file by
	// coincidence, so the test would pass against the defect it guards.
	dir := t.TempDir()
	for depth(dir) <= depth(wd)+1 {
		dir = filepath.Join(dir, "deeper")
	}
	headerSandboxAt(t, dir)
	writeSandboxFile(t, dir, "pkg/widget/widget.go", "package widget\n")

	rel, err := filepath.Rel(wd, dir)
	if err != nil {
		t.Fatalf("relative path to the sandbox: %v", err)
	}

	got, err := checkHeaders(rel)
	if err != nil {
		t.Fatalf("checkHeaders(%q): %v", rel, err)
	}
	if len(got) != 1 || got[0].Path != "pkg/widget/widget.go" {
		t.Fatalf("checkHeaders(%q) = %v, want one violation for pkg/widget/widget.go", rel, got)
	}
}

// TestCheckHeadersSkipsPythonEnvironments plants installed packages, with no
// headers, in the places a Python environment puts them, beside a first-party
// file that has one. None of them is first-party source, so the check must pass.
func TestCheckHeadersSkipsPythonEnvironments(t *testing.T) {
	dir := headerSandbox(t)
	writeSandboxFile(t, dir, "sdk/python/gravix/client.py",
		"# Copyright 2026 The Gravix Authors\n# SPDX-License-Identifier: Apache-2.0\n")
	for _, rel := range []string{
		"sdk/python/.venv/lib/python3.14/site-packages/pip/__init__.py",
		"sdk/python/venv/bin/activate_this.py",
		"sdk/python/env/lib/python3.12/site-packages/requests/api.py", // an environment by another name
		"sdk/python/gravix/__pycache__/client.cpython-314.py",
		"sdk/python/.tox/py314/lib/python3.14/site-packages/x.py",
	} {
		writeSandboxFile(t, dir, rel, "import os\n")
	}

	got, err := checkHeaders(dir)
	if err != nil {
		t.Fatalf("checkHeaders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("installed packages were checked as first-party source: %v", got)
	}
}

// TestAddModeLeavesInstalledPackagesAlone runs the script without --check: it
// must add the header to first-party files and never write into an environment.
func TestAddModeLeavesInstalledPackagesAlone(t *testing.T) {
	dir := headerSandbox(t)
	const installed = "sdk/python/.venv/lib/python3.14/site-packages/pip/__init__.py"
	writeSandboxFile(t, dir, installed, "import os\n")
	writeSandboxFile(t, dir, "pkg/widget/widget.go", "package widget\n")

	cmd := exec.Command(filepath.Join(dir, "scripts", "add_license_headers.sh"))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add mode: %v\n%s", err, out)
	}

	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(installed)))
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if string(body) != "import os\n" {
		t.Errorf("add mode rewrote an installed package:\n%s", body)
	}
	first, err := os.ReadFile(filepath.Join(dir, "pkg", "widget", "widget.go"))
	if err != nil {
		t.Fatalf("read first-party file: %v", err)
	}
	if !strings.Contains(string(first), "SPDX-License-Identifier: Apache-2.0") {
		t.Errorf("add mode did not header a first-party file:\n%s", first)
	}
}
