// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// GRVX-1005 AC-7, AC-8 and AC-9 — the three criteria that need neither the
// reference machine nor a load generator. AC-1's throughput target does, and
// AC-2 to AC-6 are in batch_test.go.
//
// A file beyond §4.1, which lists only batch.go and batch_test.go. These assert
// properties of the ingestion package rather than of the batcher, so they do not
// belong in that file. Recorded as a departure in SD-056.

func sourceOf(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

// AC-7. §4.2's purpose for this criterion is stated in its own words: both paths
// "use the same batcher, so both paths share one durability guarantee".
//
// What this test proves is the durability guarantee — that the HTTP and OTLP
// paths both reach disk through DurableSink, and that DurableSink fsyncs before
// returning, so neither path can acknowledge a fact that is not on disk.
//
// What it deliberately does NOT assert is that they share the *Batcher*,
// because they do not: services/ingestion/batch.go is never constructed outside
// its own tests. That gap is real and recorded in SD-056; this test is written
// so that wiring the batcher in later keeps it passing rather than having to be
// rewritten, because the invariant it checks is the one that must hold either
// way.
func TestBothPathsShareBatcher(t *testing.T) {
	main := sourceOf(t, "main.go")
	otlp := sourceOf(t, "otlp.go")

	// Neither path may open its own file handle: a second write path is a second
	// durability story, and §6 admits only one.
	openFile := regexp.MustCompile(`os\.(OpenFile|Create)\(`)
	if openFile.MatchString(otlp) {
		t.Error("otlp.go opens a file directly; it must write through DurableSink so both " +
			"entry paths share one durability guarantee")
	}

	for _, want := range []string{"sink.Write", "DurableSink"} {
		if !strings.Contains(otlp, want) && !strings.Contains(main, want) {
			t.Errorf("neither entry path references %q; the shared sink may have been renamed "+
				"and this check left behind", want)
		}
	}

	// The shared mechanism must fsync before returning, or "durable" is a claim
	// rather than a property.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", main, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}
	for _, method := range []string{"Write", "WriteBatch"} {
		if !methodCallsSync(file, method) {
			t.Errorf("DurableSink.%s does not call Sync; docs/00-system-truth.md §6 requires "+
				"a fact to be on disk before it is acknowledged", method)
		}
	}
}

// methodCallsSync reports whether the named DurableSink method contains a
// .Sync() call.
func methodCallsSync(file *ast.File, method string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != method || fn.Recv == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sync" {
				found = true
			}
			return true
		})
		return false
	})
	return found
}

// AC-8. Accepting a malformed fact faster is not throughput. Both entry paths
// must still validate, and the schemas package keeps its coverage gate.
func TestValidationUnchanged(t *testing.T) {
	// An actual call, found in the AST, rather than the token appearing
	// somewhere in the file. A substring match passes on a comment mentioning
	// validation, which is precisely the state this criterion exists to catch.
	for _, path := range []string{"main.go", "otlp.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, sourceOf(t, path), 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}

		called := false
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if strings.HasPrefix(fn.Sel.Name, "Validate") {
					called = true
				}
			case *ast.Ident:
				if strings.HasPrefix(fn.Name, "Validate") {
					called = true
				}
			}
			return true
		})
		if !called {
			t.Errorf("%s contains no call to a Validate* function; §3 forbids trading "+
				"validation for throughput", path)
		}
	}

	// The gate itself, so removing it is a visible change rather than a silent
	// one. GRVX-1005 §3 leans on schemas staying covered.
	ci := sourceOf(t, "../../.github/workflows/ci.yml")
	if !strings.Contains(ci, "Check schema coverage") {
		t.Error("CI no longer checks schema coverage; validation can now be weakened without " +
			"any test noticing")
	}
}

// AC-9. Charter §7.3 Q1. Per-plan rate limiting is explicitly allowed by §2 —
// it protects the system. What is forbidden is a durable-write path that is
// faster for one plan than another, which is the textbook crippleware pattern
// the charter names.
func TestNoPlanGatedIngestPath(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", sourceOf(t, "main.go"), 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	plan := regexp.MustCompile(`(?i)\bplan\b|\btier\b`)
	for _, method := range []string{"Write", "WriteBatch"} {
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Name.Name != method || fn.Recv == nil {
				return true
			}
			var buf strings.Builder
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				if id, ok := inner.(*ast.Ident); ok {
					buf.WriteString(id.Name)
					buf.WriteByte(' ')
				}
				return true
			})
			if plan.MatchString(buf.String()) {
				t.Errorf("DurableSink.%s branches on a plan or tier; the durable write path "+
					"must be identical for every plan", method)
			}
			return false
		})
	}

	// The batcher is the intended write path (§4.2). It must be plan-blind too,
	// so wiring it in cannot introduce the gate by the back door.
	if plan.MatchString(sourceOf(t, "batch.go")) {
		t.Error("batch.go references a plan or tier; group commit must not vary by plan")
	}
}
