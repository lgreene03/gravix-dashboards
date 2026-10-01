// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// exportedAPI returns a package's exported top-level names and the exported
// methods of each of its types.
func exportedAPI(t *testing.T, dir string) (names map[string]bool, methods map[string]map[string]bool) {
	t.Helper()
	names, methods = map[string]bool{}, map[string]map[string]bool{}
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if !d.Name.IsExported() {
						continue
					}
					if d.Recv == nil {
						names[d.Name.Name] = true
						continue
					}
					recv := d.Recv.List[0].Type
					if star, ok := recv.(*ast.StarExpr); ok {
						recv = star.X
					}
					if id, ok := recv.(*ast.Ident); ok {
						if methods[id.Name] == nil {
							methods[id.Name] = map[string]bool{}
						}
						methods[id.Name][d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.TypeSpec:
							names[s.Name.Name] = s.Name.IsExported()
						case *ast.ValueSpec:
							for _, n := range s.Names {
								if n.IsExported() {
									names[n.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	return names, methods
}

// TestGoSDKDocsUseTheRealAPI keeps the Go SDK's published examples honest. The
// page once documented gravix.NewClient(gravix.Config{...}), client.Send,
// gravix.Fact, gravix.Middleware, GinMiddleware and EchoMiddleware — none of
// which existed — under an import path that was not the SDK's module path.
// Every gravix.X, client.X and otel.X on the pages must now be something the
// SDK exports, and the import path must be the one sdk/go/go.mod declares.
func TestGoSDKDocsUseTheRealAPI(t *testing.T) {
	root := repoRoot(t)
	sdkNames, sdkMethods := exportedAPI(t, filepath.Join(root, "sdk", "go"))
	otelNames, _ := exportedAPI(t, filepath.Join(root, "sdk", "go", "otel"))

	gomod, err := os.ReadFile(filepath.Join(root, "sdk", "go", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(gomod), "\n", 2)[0], "module"))

	ref := regexp.MustCompile(`\b(gravix|client|otel)\.([A-Z][A-Za-z0-9_]*)`)
	checked := 0
	for _, page := range []string{"sdk-go.md", "getting-started.md"} {
		body, err := os.ReadFile(filepath.Join(root, "docs-site", "docs", page))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Contains(text, "gravix-dashboards/sdk/go\"") || strings.Contains(text, "go get github.com/lgreene/") {
			t.Errorf("%s imports or installs the SDK by a path that is not its module path (%s)", page, module)
		}
		if page == "sdk-go.md" && !strings.Contains(text, `gravix "`+module+`"`) {
			t.Errorf("%s never imports the SDK by its module path %q", page, module)
		}
		for _, m := range ref.FindAllStringSubmatch(text, -1) {
			checked++
			var ok bool
			switch m[1] {
			case "gravix":
				ok = sdkNames[m[2]]
			case "client":
				ok = sdkMethods["Client"][m[2]]
			case "otel":
				ok = otelNames[m[2]]
			}
			if !ok {
				t.Errorf("%s uses %s.%s, which the Go SDK does not export", page, m[1], m[2])
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d SDK references found; the pages changed shape and this test is checking nothing", checked)
	}
}
