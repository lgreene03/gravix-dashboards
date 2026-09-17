// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/plugin"
	"github.com/lgreene/gravix-dashboards/pkg/registry"
)

// scaffold runs `gravix plugin new` into a fresh directory and returns it.
func scaffold(t *testing.T, name, kind, language string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	var stdout, stderr bytes.Buffer
	code := pluginMain(context.Background(),
		[]string{"new", "--name", name, "--kind", kind, "--language", language, "--out", dir},
		&stdout, &stderr)
	if code != pluginExitOK {
		t.Fatalf("plugin new %s/%s exited %d\nstderr: %s", kind, language, code, stderr.String())
	}
	return dir
}

// goEnv clears GOFLAGS, which in the parent can carry -mod=vendor that a
// freshly scaffolded module has no vendor directory for.
func goEnv() []string { return append(os.Environ(), "GOFLAGS=") }

func runIn(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = goEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// AC-1: a scaffolded Go plugin builds and passes its tests with no edits.
//
// The scaffold is the author's first experience of Gravix. One that does not
// build makes them debug our template before they can write a line of their
// own, so this is asserted by running their commands, not ours.
func TestGoScaffoldBuildsAndTests(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dir := scaffold(t, "gravix-notifier-demo", "notifier", "go")

	// Checked before building: a stray {{.Name}} in a published scaffold is a
	// template bug that only shows up in somebody else's repository, and
	// `go build ./...` drops a binary here that is full of braces.
	assertFullyRendered(t, dir)

	if out, err := runIn(t, dir, "go", "build", "./..."); err != nil {
		t.Fatalf("the scaffold does not build:\n%s", out)
	}
	out, err := runIn(t, dir, "go", "test", "./...")
	if err != nil {
		t.Fatalf("the scaffold's own tests fail:\n%s", out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("go test said %q", out)
	}
}

// AC-2: a scaffolded Python plugin runs its tests with no edits.
func TestPythonScaffoldTests(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}

	dir := scaffold(t, "gravix-notifier-demo", "notifier", "python")

	out, err := runIn(t, dir, python, "-m", "unittest", "discover", "-p", "test_*.py")
	if err != nil {
		t.Fatalf("the Python scaffold's tests fail:\n%s", out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("unittest said %q", out)
	}
	assertFullyRendered(t, dir)

	// Gravix runs the interpreter against this file, so it has to be readable
	// as a program and marked executable for anyone who runs it directly.
	info, err := os.Stat(filepath.Join(dir, "plugin.py"))
	if err != nil {
		t.Fatalf("stat plugin.py: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("plugin.py is not executable")
	}
}

// AC-3: all three kinds scaffold correctly, in both languages.
func TestScaffoldAllKinds(t *testing.T) {
	methods := map[string]string{
		"notifier": "gravix.notify",
		"exporter": "gravix.export",
		"adapter":  "gravix.convert",
	}

	for _, kind := range []string{"notifier", "exporter", "adapter"} {
		for _, language := range []string{"go", "python"} {
			t.Run(kind+"/"+language, func(t *testing.T) {
				dir := scaffold(t, "gravix-"+kind+"-demo", kind, language)

				source := filepath.Join(dir, "main.go")
				if language == "python" {
					source = filepath.Join(dir, "plugin.py")
				}
				raw, err := os.ReadFile(source)
				if err != nil {
					t.Fatalf("read %s: %v", source, err)
				}
				text := string(raw)

				// The handshake and this kind's one method, and no other
				// kind's: a notifier that also answers gravix.export would be
				// advertising a method it cannot serve.
				if !strings.Contains(text, "gravix.describe") {
					t.Error("the scaffold does not answer gravix.describe")
				}
				for k, method := range methods {
					if k == kind {
						if !strings.Contains(text, method) {
							t.Errorf("a %s scaffold does not handle %s", kind, method)
						}
					} else if strings.Contains(text, method) {
						t.Errorf("a %s scaffold also handles %s", kind, method)
					}
				}

				for _, f := range []string{"README.md", "LICENSE", "Makefile"} {
					if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
						t.Errorf("the scaffold has no %s: %v", f, err)
					}
				}

				// Listing check 6 needs a data-flow section, so the scaffold
				// ships the heading rather than leaving the author to discover
				// the requirement when their listing is refused.
				readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
				if err != nil {
					t.Fatalf("read README: %v", err)
				}
				if !strings.Contains(string(readme), "## Data flow") {
					t.Error("the scaffold's README has no Data flow section")
				}

				assertFullyRendered(t, dir)
			})
		}
	}
}

// AC-4: a scaffolded plugin loads under pkg/plugin's real host.
//
// Building it and driving it through the host is the only evidence that
// matters: a scaffold that passes its own tests but fails the handshake is
// still a scaffold that does not work.
func TestScaffoldLoadsInHost(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dir := scaffold(t, "gravix-notifier-demo", "notifier", "go")
	bin := filepath.Join(dir, "gravix-notifier-demo")
	if out, err := runIn(t, dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	h := plugin.NewHost(plugin.HostOptions{
		Command:     bin,
		CallTimeout: 10 * time.Second,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer h.Close()

	m, err := h.Start(context.Background())
	if err != nil {
		t.Fatalf("the scaffold failed the handshake: %v", err)
	}
	if m.Name != "gravix-notifier-demo" || m.Kind != plugin.KindNotifier {
		t.Fatalf("manifest = %+v", m)
	}
	if m.ABIVersion != plugin.ABIVersion {
		t.Fatalf("scaffold declares ABI %q, this Gravix supports %q", m.ABIVersion, plugin.ABIVersion)
	}

	impl, err := h.Impl(nil)
	if err != nil {
		t.Fatalf("Impl: %v", err)
	}
	if err := impl.(plugin.Notifier).Notify(context.Background(), plugin.Alert{AlertID: "a-1", RuleName: "checkout errors"}); err != nil {
		t.Fatalf("the scaffold failed a notify call: %v", err)
	}

	// The same binary is what `gravix plugin validate` is pointed at.
	var stdout, stderr bytes.Buffer
	if code := pluginMain(context.Background(), []string{"validate", "--path", bin}, &stdout, &stderr); code != pluginExitOK {
		t.Fatalf("validate exited %d\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is valid against ABI "+plugin.ABIVersion) {
		t.Errorf("validate said %q", stdout.String())
	}
}

// AC-8: no command downloads or installs a plugin from the registry.
//
// Listing something is not running it. A tool that fetched and executed
// third-party code because it appeared in a list would be a supply-chain
// hazard wearing a convenience feature's clothing.
func TestNoAutoInstall(t *testing.T) {
	// The file that reads the registry imports nothing that can reach the
	// network, so listing cannot fetch whatever a repository URL points at —
	// whoever wrote that URL.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "cmd_plugin.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse cmd_plugin.go: %v", err)
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("import path %s: %v", imp.Path.Value, err)
		}
		switch path {
		case "net", "net/http", "net/url", "os/exec":
			t.Errorf("cmd_plugin.go imports %q; listing must not be able to fetch or run anything", path)
		}
	}

	// Neither does the parser. os/exec is not on this list: it reaches
	// pkg/registry through pkg/plugin, which is how Gravix runs a plugin the
	// operator has already installed — not how one would be fetched.
	out, err := exec.Command("go", "list", "-deps", "github.com/lgreene/gravix-dashboards/pkg/registry").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "net/http" || dep == "net" {
			t.Errorf("pkg/registry depends on %q", dep)
		}
	}

	// And listing writes nothing: no clone, no cache, no state.
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{{"list"}, {"list", "--json"}, {"list", "--kind", "notifier"}} {
		var stdout, stderr bytes.Buffer
		if code := pluginMain(context.Background(), args, &stdout, &stderr); code != pluginExitOK {
			t.Fatalf("plugin %v exited %d: %s", args, code, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Fatalf("plugin %v printed nothing", args)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("listing left %d files behind: %v", len(entries), entries)
	}
}

// AC-10: validate catches an ABI mismatch.
//
// The fake declares a well-formed manifest for a different ABI. Gravix must
// refuse it before calling it, and say which version it found, so the operator
// knows which build to fetch rather than guessing.
func TestValidateCatchesABIMismatch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "future-plugin")
	script := "#!/bin/sh\n" +
		"while IFS= read -r line; do\n" +
		`  printf '{"jsonrpc":"2.0","id":1,"result":{"name":"future-plugin","version":"1.0.0","abi_version":"2","kind":"notifier","description":"from the future"}}` + "\\n'\n" +
		"done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake plugin: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := pluginMain(context.Background(), []string{"validate", "--path", bin}, &stdout, &stderr)
	if code != pluginExitFailed {
		t.Fatalf("validate exited %d, want %d\nstdout: %s\nstderr: %s", code, pluginExitFailed, stdout.String(), stderr.String())
	}
	want := "plugin declares ABI 2, this Gravix supports " + plugin.ABIVersion
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

// AC-11: a non-empty output directory is refused.
func TestScaffoldRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed the directory: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := pluginMain(context.Background(),
		[]string{"new", "--name", "gravix-notifier-demo", "--out", dir}, &stdout, &stderr)
	if code != pluginExitUsage {
		t.Fatalf("exited %d, want %d", code, pluginExitUsage)
	}
	if want := dir + " is not empty; refusing to overwrite"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	// The seeded file is still there. Refusing and then writing anyway would
	// be worse than not refusing at all.
	raw, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil || string(raw) != "package main\n" {
		t.Errorf("the existing file was touched: %q, %v", raw, err)
	}
}

// §6.1: the failure modes and their exact messages.
func TestNewRejectsBadFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing name", []string{"new"}, "--name is required"},
		{"name without the prefix", []string{"new", "--name", "mattermost"}, `plugin name must start with "gravix-", got "mattermost"`},
		{"name not kebab-case", []string{"new", "--name", "gravix-Bad_Name"}, "lower-case words joined by single hyphens"},
		{"unknown kind", []string{"new", "--name", "gravix-x-y", "--kind", "dashboard"}, "--kind must be notifier, exporter, or adapter"},
		{"unknown language", []string{"new", "--name", "gravix-x-y", "--language", "rust"}, "--language must be go or python"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(tc.args, "--out", filepath.Join(t.TempDir(), "out"))
			if code := pluginMain(context.Background(), args, &stdout, &stderr); code != pluginExitUsage {
				t.Fatalf("exited %d, want %d", code, pluginExitUsage)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPluginUsageAndUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := pluginMain(context.Background(), nil, &stdout, &stderr); code != pluginExitUsage {
		t.Errorf("no subcommand exited %d, want %d", code, pluginExitUsage)
	}

	stdout.Reset()
	stderr.Reset()
	if code := pluginMain(context.Background(), []string{"install"}, &stdout, &stderr); code != pluginExitUsage {
		t.Errorf("`plugin install` exited %d, want %d", code, pluginExitUsage)
	}
	// There is no install subcommand and the usage text says why.
	if !strings.Contains(stderr.String(), "Gravix never downloads") {
		t.Errorf("usage does not say that nothing is installed: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := pluginMain(context.Background(), []string{"help"}, &stdout, &stderr); code != pluginExitOK {
		t.Errorf("`plugin help` exited %d", code)
	}
}

func TestListFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := pluginMain(context.Background(), []string{"list", "--kind", "nonsense"}, &stdout, &stderr); code != pluginExitUsage {
		t.Errorf("an unknown --kind exited %d, want %d", code, pluginExitUsage)
	}

	stdout.Reset()
	stderr.Reset()
	if code := pluginMain(context.Background(), []string{"list", "--json", "--kind", "exporter"}, &stdout, &stderr); code != pluginExitOK {
		t.Fatalf("exited %d: %s", code, stderr.String())
	}
	var entries []registry.Entry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("--json did not emit JSON: %v\n%s", err, stdout.String())
	}
	if len(entries) == 0 {
		t.Fatal("no exporters listed")
	}
	for _, e := range entries {
		if e.Kind != "exporter" {
			t.Errorf("--kind exporter returned a %s", e.Kind)
		}
	}

	// A filter that matches nothing says so rather than printing an empty list.
	stdout.Reset()
	stderr.Reset()
	if code := pluginMain(context.Background(), []string{"list", "--kind", "adapter"}, &stdout, &stderr); code != pluginExitOK {
		t.Fatalf("exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No plugins listed of kind adapter") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestValidateRejectsMissingPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := pluginMain(context.Background(), []string{"validate"}, &stdout, &stderr); code != pluginExitUsage {
		t.Errorf("no --path exited %d, want %d", code, pluginExitUsage)
	}

	stdout.Reset()
	stderr.Reset()
	missing := filepath.Join(t.TempDir(), "nope")
	if code := pluginMain(context.Background(), []string{"validate", "--path", missing}, &stdout, &stderr); code != pluginExitUsage {
		t.Errorf("a missing path exited %d, want %d", code, pluginExitUsage)
	}
}

// The published page shows the message `validate` prints on an ABI mismatch.
// A reader who sees different words than the documentation promised assumes
// they are looking at a different failure, so the two are pinned together.
func TestDocumentedABIMismatchMessageMatches(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs-site", "docs", "plugin-registry.md"))
	if err != nil {
		t.Fatalf("read the published page: %v", err)
	}
	want := "plugin declares ABI 2, this Gravix supports " + plugin.ABIVersion
	if !strings.Contains(string(raw), want) {
		t.Errorf("plugin-registry.md does not show %q, which is what validate prints", want)
	}

	// The flags the page documents are the flags `new` actually has.
	for _, flag := range []string{"`--name`", "`--kind`", "`--out`", "`--language`"} {
		if !strings.Contains(string(raw), flag) {
			t.Errorf("plugin-registry.md does not document %s", flag)
		}
	}
}

// assertFullyRendered fails if any scaffold file still carries template syntax.
func assertFullyRendered(t *testing.T, dir string) {
	t.Helper()

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte("{{")) {
			rel, _ := filepath.Rel(dir, path)
			t.Errorf("%s still contains template syntax", rel)
		}
		if strings.HasSuffix(path, ".tmpl") {
			t.Errorf("%s was written with its template suffix", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}
