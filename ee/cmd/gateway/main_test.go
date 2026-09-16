// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package main

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const repoRoot = "../../.."

// AC-9. The Enterprise entrypoint builds, and today it includes no ee/ feature
// package at all — the two binaries are the same program. That is the whole point
// of the skeleton: it proves the composition mechanism before anything uses it, so
// the first real feature adds one import line and nothing else.
//
// GRVX-1304 through GRVX-1311 each raise wantEEImports by exactly one and add the
// package they expect to see. A spec that added two, or that reached into core,
// fails here rather than in review.
func TestEEGatewayBuildsWithNoExtensions(t *testing.T) {
	const wantEEImports = 0

	bin := filepath.Join(t.TempDir(), "gateway-ee")
	build := exec.Command("go", "build", "-o", bin, "./ee/cmd/gateway/")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -o bin/gateway-ee ./ee/cmd/gateway/: %v\n%s", err, out)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("the build reported success but produced no binary: %v", err)
	}

	imports := importPaths(t, "main.go")
	var ee []string
	for _, p := range imports {
		if strings.Contains(p, "gravix-dashboards/ee/") {
			ee = append(ee, p)
		}
	}
	if len(ee) != wantEEImports {
		t.Errorf("main.go imports %d ee/ package(s) (%s); want %d",
			len(ee), strings.Join(ee, ", "), wantEEImports)
	}
}

// The Enterprise binary must run the identical core. If it ever grew its own copy
// of the gateway, "the paid build is the free build plus extensions" would stop
// being true and charter §7.2 would be violated by divergence rather than by an
// import.
func TestEEGatewayRunsTheSameCore(t *testing.T) {
	imports := importPaths(t, "main.go")
	found := false
	for _, p := range imports {
		if p == "github.com/lgreene/gravix-dashboards/pkg/gatewaycore" {
			found = true
		}
	}
	if !found {
		t.Fatalf("main.go does not import pkg/gatewaycore; imports = %v", imports)
	}

	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(src), "gatewaycore.Run()") {
		t.Error("main.go does not call gatewaycore.Run(); the two binaries must run the same code")
	}
}

func importPaths(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var out []string
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote %s: %v", spec.Path.Value, err)
		}
		out = append(out, p)
	}
	return out
}
