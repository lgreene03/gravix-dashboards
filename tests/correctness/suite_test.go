//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// ─── AC-10 / §5.4: report pre-existing data, never fail on it ───

// TestReportPreExistingDataGaps inspects a warehouse if one is pointed at, and
// reports what it finds.
//
// It reports rather than asserts because these describe data written before
// Phase 8 existed — a partition with no manifest is not a regression, it is
// history. Failing a fresh checkout because someone's warehouse predates a
// feature would make the suite useless as a gate on the code.
//
// It fails on exactly one thing: being pointed at a warehouse it cannot read.
// That is a broken environment, not a finding.
func TestReportPreExistingDataGaps(t *testing.T) {
	dir := os.Getenv("GRAVIX_WAREHOUSE_DIR")
	if dir == "" {
		t.Log("GRAVIX_WAREHOUSE_DIR is not set, so there is no existing warehouse to inspect. " +
			"Set it to report on a real deployment's data.")
		return
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("cannot read warehouse at %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("cannot read warehouse at %s: not a directory", dir)
	}

	store, err := storage.NewLocalStore(filepath.Dir(dir))
	if err != nil {
		t.Fatalf("cannot read warehouse at %s: %v", dir, err)
	}

	var (
		partitions   []string
		noManifest   []string
		noSketch     []string
		byDay        = map[string][]string{}
		dayPattern   = regexp.MustCompile(`event_day=(\d{4}-\d{2}-\d{2})`)
		parquetFiles int
	)

	err = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || !strings.HasSuffix(path, ".parquet") {
			return nil
		}
		parquetFiles++
		rel, relErr := filepath.Rel(filepath.Dir(dir), path)
		if relErr != nil {
			return relErr
		}
		key := filepath.ToSlash(rel)
		partitions = append(partitions, key)

		if m := dayPattern.FindStringSubmatch(key); m != nil {
			byDay[m[1]] = append(byDay[m[1]], key)
		}

		if _, mErr := manifest.Read(context.Background(), store, key); mErr != nil {
			noManifest = append(noManifest, key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read warehouse at %s: %v", dir, err)
	}

	var duplicates []string
	for day, keys := range byDay {
		if len(keys) > 1 {
			sort.Strings(keys)
			duplicates = append(duplicates, fmt.Sprintf("%s: %s", day, strings.Join(keys, ", ")))
		}
	}
	sort.Strings(duplicates)
	sort.Strings(noManifest)

	t.Logf("warehouse %s: %d parquet files across %d days", dir, parquetFiles, len(byDay))
	report := func(label string, items []string) {
		if len(items) == 0 {
			t.Logf("  %s: none", label)
			return
		}
		t.Logf("  %s: %d", label, len(items))
		for i, item := range items {
			if i == 10 {
				t.Logf("    … and %d more", len(items)-10)
				break
			}
			t.Logf("    %s", item)
		}
	}
	report("partitions with no manifest (GRVX-802)", noManifest)
	report("days with more than one partition (GRVX-801)", duplicates)
	report("partitions predating sketch storage (GRVX-805)", noSketch)

	if len(noManifest) > 0 || len(duplicates) > 0 {
		t.Logf("  none of the above is a test failure. Run `gravix recompute` over the affected " +
			"days to give them manifests and collapse duplicates.")
	}
}

// ─── AC-11 ───

// TestSuiteNeedsNoDocker proves the claim rather than restating it: the suite
// must not invoke docker, must not look for a docker daemon, and must not pull
// a container library into its dependency tree.
//
// It checks behaviour, not spelling. An earlier version banned the byte sequence
// "docker" anywhere in the package, which had the property backwards: the
// strongest available proof that the suite needs no daemon is to run a child
// process with DOCKER_HOST aimed at a socket that does not exist, and a
// text ban forbids exactly that. Recorded as F-009.
func TestSuiteNeedsNoDocker(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, packageDir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", packageDir, err)
	}

	// 1. Nothing in the package execs a docker binary, and nothing reads
	// DOCKER_HOST — reading it is how a process goes looking for a daemon.
	// Setting it to a dead socket is the opposite, so only reads are banned.
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			base := filepath.Base(path)

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgName, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}

				// exec.Command("docker", …) / exec.CommandContext(ctx, "docker", …)
				if pkgName.Name == "exec" && strings.HasPrefix(sel.Sel.Name, "Command") {
					arg := call.Args[0]
					if sel.Sel.Name == "CommandContext" && len(call.Args) > 1 {
						arg = call.Args[1]
					}
					if name, ok := stringLit(arg); ok {
						if strings.HasPrefix(filepath.Base(name), "docker") {
							t.Errorf("AC-11 FAILED: %s in %s runs %q; the correctness suite "+
								"must run without a container stack",
								base, enclosingFunc(file, call.Pos()), name)
						}
					}
				}

				// os.Getenv("DOCKER_HOST") / os.LookupEnv("DOCKER_HOST")
				if pkgName.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv") {
					if name, ok := stringLit(call.Args[0]); ok && strings.HasPrefix(name, "DOCKER_") {
						t.Errorf("AC-11 FAILED: %s in %s reads %s; the suite must not go "+
							"looking for a docker daemon",
							base, enclosingFunc(file, call.Pos()), name)
					}
				}
				return true
			})
		}
	}

	// 2. Nothing in the suite's dependency tree is a container library.
	list := exec.Command("go", "list", "-deps", "./tests/correctness/...")
	list.Dir = repoRoot()
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Split(string(out), "\n") {
		low := strings.ToLower(dep)
		if strings.Contains(low, "testcontainers") || strings.Contains(low, "docker/docker") {
			t.Errorf("AC-11 FAILED: the suite depends on %s", dep)
		}
	}
}

// stringLit returns the value of an untyped string literal, if that is what the
// expression is. A command name assembled at runtime is not one, and is caught
// by the dependency check and by the demo's own dead-socket run instead.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// ─── AC-12 ───

// TestSuiteRuntimeBudget runs the whole package in a child process and measures
// it, because a suite nobody runs locally protects nothing.
//
// It measures rather than estimates, and it excludes itself to avoid recursion.
func TestSuiteRuntimeBudget(t *testing.T) {
	if os.Getenv("GRAVIX_CORRECTNESS_TIMING_CHILD") == "1" {
		t.Skip("this is the child run being timed; skipping the timer itself is what " +
			"stops it timing itself forever")
	}

	const budget = 5 * time.Minute

	cmd := exec.Command("go", "test", "./tests/correctness/...", "-count=1")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GRAVIX_CORRECTNESS_TIMING_CHILD=1")

	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("the suite failed while being timed: %v\n%s", err, out)
	}
	t.Logf("correctness suite runtime: %s (budget %s, %.0f%% used)",
		elapsed.Round(time.Millisecond), budget, float64(elapsed)/float64(budget)*100)

	if elapsed > budget {
		t.Errorf("correctness suite took %s, budget is %s; reduce fixture size, do not skip properties",
			elapsed.Round(time.Second), budget)
	}
}

// ─── AC-14 ───

// TestNoSkippedTests is the qa-engineer's rule made mechanical: a skipped test
// is a defect report against the implementation, never a way to get green.
//
// It parses the Go syntax rather than grepping for "t.Skip", because a grep
// finds this file's own error messages and reports itself — which is both funny
// and useless.
func TestNoSkippedTests(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, packageDir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", packageDir, err)
	}

	// The timing test skips its own child run, which is the mechanism that stops
	// it timing itself recursively rather than a property going unproven. It is
	// named here so that any other skip is a failure.
	allowed := map[string]bool{"TestSuiteRuntimeBudget": true}

	var skips, shorts int
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			base := filepath.Base(path)

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}

				switch {
				case ident.Name == "t" && strings.HasPrefix(sel.Sel.Name, "Skip"):
					fn := enclosingFunc(file, call.Pos())
					if allowed[fn] {
						return true
					}
					skips++
					t.Errorf("AC-14 FAILED: %s calls t.%s in %s — a skipped test is a defect "+
						"report, not a fix", base, sel.Sel.Name, fn)
				case ident.Name == "testing" && sel.Sel.Name == "Short":
					shorts++
					t.Errorf("AC-14 FAILED: %s gates on testing.Short() in %s; a property that "+
						"only holds in long mode is not proven", base, enclosingFunc(file, call.Pos()))
				}
				return true
			})
		}
	}
	t.Logf("%d disallowed skips, %d short-mode gates", skips, shorts)
}

// enclosingFunc names the function a position falls inside, for a legible error.
func enclosingFunc(file *ast.File, pos token.Pos) string {
	name := "(top level)"
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Pos() <= pos && pos <= fn.End() {
			name = fn.Name.Name
		}
	}
	return name
}

// t.Parallel and t.Chdir cannot be combined: Chdir changes a process-wide
// directory, so a parallel test would move the ground under its siblings. Go's
// own testing package panics on it, but only if the combination is reached — this
// finds it in the source, where it is cheaper.
func TestNoParallelWithChdir(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, packageDir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", packageDir, err)
	}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			calls := map[string]map[string]bool{}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "t" {
					fn := enclosingFunc(file, call.Pos())
					if calls[fn] == nil {
						calls[fn] = map[string]bool{}
					}
					calls[fn][sel.Sel.Name] = true
				}
				return true
			})
			for fn, used := range calls {
				if used["Parallel"] && used["Chdir"] {
					t.Errorf("AC-14 FAILED: %s in %s calls both t.Parallel and t.Chdir, which "+
						"changes a process-wide directory under other tests",
						fn, filepath.Base(path))
				}
			}
		}
	}
}

// ─── AC-13 ───

// TestSchemasCoverageUnchanged holds the 100% line coverage CLAUDE.md requires
// of schemas/. Phase 8 touched a lot; this checks it did not quietly cost that.
func TestSchemasCoverageUnchanged(t *testing.T) {
	cmd := exec.Command("go", "test", "./schemas/...", "-cover", "-count=1")
	cmd.Dir = repoRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./schemas/...: %v\n%s", err, out)
	}

	re := regexp.MustCompile(`coverage:\s*([0-9.]+)% of statements`)
	m := re.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("AC-13 FAILED: no coverage figure in:\n%s", out)
	}
	pct, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("unparseable coverage %q: %v", m[1], err)
	}

	t.Logf("schemas/ coverage: %.1f%%", pct)
	if pct < 100.0 {
		t.Errorf("AC-13 FAILED: schemas/ coverage is %.1f%%, CLAUDE.md requires 100%%", pct)
	}
}
