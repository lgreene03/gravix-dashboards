// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command checkboundary enforces the Gravix open-core boundary.
//
// It fails when a core package imports ee/, when a plan gate is absent from
// docs/oss/boundary.yaml, when a source file is missing its licence header, or
// when the boundary map itself is invalid.
//
// A charter that is only prose decays. This command is what stops that.
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lgreene/gravix-dashboards/pkg/boundary"
)

// eeImportPrefix identifies an import of the commercial tree.
const eeImportPrefix = "gravix-dashboards/ee/"

// excludedDirs are never walked when checking imports: generated code, vendored
// dependencies, the commercial tree itself, and the fixture that deliberately
// violates the rule so the checker can be tested.
var excludedDirs = map[string]bool{
	".git":         true,
	"ee":           true,
	"gen":          true,
	"node_modules": true,
	"data":         true,
	"bin":          true,
	// Compiler output. `npm run build` in sdk/node writes dist/ with no licence
	// headers, which made `make check-boundary` fail for anyone who had built the
	// SDK locally — a check that depends on which commands you ran last is a check
	// nobody trusts. git ignores dist/ for the same reason.
	"dist": true,
}

// excludedPaths are specific repo-relative paths exempt from the import check.
var excludedPaths = []string{
	"cmd/checkboundary/testdata",
}

// gateGgateRe matches the two ways a plan gate is expressed in this codebase.
var (
	requirePlanRe = regexp.MustCompile(`requirePlan\("([a-z]+)"\)`)
	planRankRe    = regexp.MustCompile(`planRank\[[^\]]+\]\s*<\s*planRank\["([a-z]+)"\]`)
)

// Violation is one boundary breach.
type Violation struct {
	Kind    string // "import" | "ungated" | "header" | "map"
	Path    string // repo-relative
	Line    int    // 1-indexed; 0 when not line-specific
	Message string
}

func (v Violation) String() string {
	if v.Line > 0 {
		return fmt.Sprintf("%s: %s:%d: %s", v.Kind, v.Path, v.Line, v.Message)
	}
	return fmt.Sprintf("%s: %s: %s", v.Kind, v.Path, v.Message)
}

func isExcludedPath(rel string) bool {
	for _, p := range excludedPaths {
		if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// checkImports reports every non-ee package that imports an ee package.
func checkImports(root string) ([]Violation, error) {
	var out []Violation
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if excludedDirs[d.Name()] || isExcludedPath(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || isExcludedPath(rel) {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			// An unparseable file is a compile problem, not a boundary problem.
			return nil
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(p, eeImportPrefix) {
				out = append(out, Violation{
					Kind:    "import",
					Path:    rel,
					Line:    fset.Position(imp.Pos()).Line,
					Message: fmt.Sprintf("core package imports ee/: %s", p),
				})
			}
		}
		return nil
	})
	return out, err
}

// checkGates reports every plan-gate call site whose file is absent from the map.
func checkGates(root string, m *boundary.Map) ([]Violation, error) {
	// Build the set of files any capability claims.
	claimed := func(rel string) bool {
		for _, c := range m.Capabilities {
			for _, p := range c.Paths {
				clean := strings.TrimSuffix(p, "/")
				if rel == clean || strings.HasPrefix(rel, clean+string(filepath.Separator)) {
					return true
				}
			}
		}
		return false
	}

	var out []Violation
	servicesDir := filepath.Join(root, "services")
	if _, err := os.Stat(servicesDir); err != nil {
		return nil, nil
	}

	err := filepath.WalkDir(servicesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !requirePlanRe.MatchString(line) && !planRankRe.MatchString(line) {
				continue
			}
			// The definition of requirePlan is not itself a gate.
			if strings.Contains(line, "func (gw *gateway) requirePlan") {
				continue
			}
			if !claimed(rel) {
				out = append(out, Violation{
					Kind:    "ungated",
					Path:    rel,
					Line:    i + 1,
					Message: "plan gate at this location is absent from boundary.yaml",
				})
			}
		}
		return nil
	})
	return out, err
}

// checkHeaders reports every source file missing its licence header, by
// delegating to the script that owns that rule.
func checkHeaders(root string) ([]Violation, error) {
	script := filepath.Join(root, "scripts", "add_license_headers.sh")
	if _, err := os.Stat(script); err != nil {
		return nil, nil
	}
	cmd := exec.Command(script, "--check")
	cmd.Dir = root
	stdout, err := cmd.Output()
	if err == nil {
		return nil, nil
	}
	var out []Violation
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		if line == "" {
			continue
		}
		out = append(out, Violation{Kind: "header", Path: line, Message: "missing licence header"})
	}
	return out, nil
}

// checkMap reports validation errors in the boundary map itself.
func checkMap(mapPath string, m *boundary.Map) []Violation {
	var out []Violation
	for _, err := range m.Validate() {
		out = append(out, Violation{Kind: "map", Path: mapPath, Message: err.Error()})
	}
	return out
}

func run(root, mapPath string, stdout, stderr *os.File) int {
	var all []Violation

	m, err := boundary.Load(filepath.Join(root, mapPath))
	if err != nil {
		fmt.Fprintln(stdout, Violation{Kind: "map", Path: mapPath, Message: err.Error()})
		fmt.Fprintln(stdout, "boundary: 1 violations")
		return 1
	}
	all = append(all, checkMap(mapPath, m)...)

	imports, err := checkImports(root)
	if err != nil {
		fmt.Fprintf(stderr, "checkboundary: walking imports: %v\n", err)
		return 1
	}
	all = append(all, imports...)

	gates, err := checkGates(root, m)
	if err != nil {
		fmt.Fprintf(stderr, "checkboundary: walking gates: %v\n", err)
		return 1
	}
	all = append(all, gates...)

	headers, err := checkHeaders(root)
	if err != nil {
		fmt.Fprintf(stderr, "checkboundary: checking headers: %v\n", err)
		return 1
	}
	all = append(all, headers...)

	sort.Slice(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})
	for _, v := range all {
		fmt.Fprintln(stdout, v)
	}
	fmt.Fprintf(stdout, "boundary: %d violations\n", len(all))
	if len(all) > 0 {
		return 1
	}
	return 0
}

func main() {
	root := flag.String("root", ".", "repository root to check")
	mapPath := flag.String("map", "docs/oss/boundary.yaml", "path to the boundary map")
	flag.Parse()
	os.Exit(run(*root, *mapPath, os.Stdout, os.Stderr))
}
