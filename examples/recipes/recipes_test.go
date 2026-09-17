// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package recipes tests the framework integration recipes.
//
// Two layers. The byte-identity tests stop a recipe documenting one thing while
// its runnable example does another — the failure mode of every "copy this into
// your app" snippet ever written. The live-execution tests then run the examples
// against a stub ingestion server and assert the path_template they actually
// send, because an example that compiles and reports /users/1234 has documented
// the opposite of the thing the recipe is about.
package recipes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── the stub ingestion server ───

// newStubIngestionServer accepts the batch endpoint every SDK posts to and
// records the path_template of each fact it receives.
func newStubIngestionServer(t *testing.T, received *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		for {
			var fact struct {
				PathTemplate string `json:"path_template"`
			}
			if err := dec.Decode(&fact); err != nil {
				break
			}
			if fact.PathTemplate != "" {
				mu.Lock()
				*received = append(*received, fact.PathTemplate)
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"accepted":1,"rejected":0}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─── docs/example byte identity ───

// assertDocMatchesExample fails unless the doc's first fenced block is exactly
// the example file.
func assertDocMatchesExample(t *testing.T, docPath, examplePath string) {
	t.Helper()

	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	example, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	block, ok := firstFencedBlock(string(doc))
	if !ok {
		t.Fatalf("%s has no fenced code block", docPath)
	}
	if block == string(example) {
		return
	}

	t.Errorf("%s code block does not match %s", docPath, examplePath)
	for _, line := range diffLines(block, string(example)) {
		t.Log(line)
	}
}

// firstFencedBlock returns the content between the first ``` line and the next
// one, exclusive of both.
func firstFencedBlock(doc string) (string, bool) {
	_, after, ok := strings.Cut(doc, "```")
	if !ok {
		return "", false
	}
	// Skip the language tag on the opening fence.
	_, body, ok := strings.Cut(after, "\n")
	if !ok {
		return "", false
	}
	block, _, ok := strings.Cut(body, "```")
	if !ok {
		return "", false
	}
	return block, true
}

// diffLines renders the first few differing lines, so a failure says where.
func diffLines(got, want string) []string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	var out []string
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			out = append(out, fmt.Sprintf("  line %d:\n    doc:     %q\n    example: %q", i+1, gl, wl))
			if len(out) >= 5 {
				return append(out, "  ... (further differences not shown)")
			}
		}
	}
	return out
}

func TestExpressDocMatchesExample(t *testing.T) {
	assertDocMatchesExample(t, "../../docs/recipes/express.md", "express/server.js")
}

func TestFastAPIDocMatchesExample(t *testing.T) {
	assertDocMatchesExample(t, "../../docs/recipes/fastapi.md", "fastapi/main.py")
}

func TestFlaskDocMatchesExample(t *testing.T) {
	assertDocMatchesExample(t, "../../docs/recipes/flask.md", "flask/app.py")
}

func TestDjangoDocMatchesExample(t *testing.T) {
	assertDocMatchesExample(t, "../../docs/recipes/django.md", "django/app.py")
}

func TestGinDocMatchesExample(t *testing.T) {
	assertDocMatchesExample(t, "../../docs/recipes/gin.md", "gin/main.go")
}

func TestRailsDocMatchesExample(t *testing.T) {
	assertDocMatchesExample(t, "../../docs/recipes/rails.md", "rails/client.rb")
}

// ─── live execution ───

// freePort asks the kernel for a port nobody is using, rather than picking one
// and hoping.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitForServer polls until the example's own server accepts a connection.
func waitForServer(t *testing.T, framework string, port int, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s example server did not become ready within %s", framework, budget)
}

// runExample starts one example as a subprocess, requests reqPath, and returns
// the path_templates the stub server received.
func runExample(t *testing.T, framework, interpreter string, args []string, reqPath string, env ...string) []string {
	t.Helper()

	if _, err := exec.LookPath(interpreter); err != nil {
		// A missing runtime is not a failing recipe. CI always has all of them,
		// so this path is never taken there.
		t.Skipf("%s not found on PATH", interpreter)
	}

	var mu sync.Mutex
	var received []string
	stub := newStubIngestionServer(t, &received, &mu)

	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, interpreter, args...)
	cmd.Env = append(os.Environ(),
		"GRAVIX_ENDPOINT="+stub.URL,
		"GRAVIX_API_KEY=recipe-test-key",
		fmt.Sprintf("PORT=%d", port),
	)
	cmd.Env = append(cmd.Env, env...)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s example: %v", framework, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if t.Failed() {
			t.Logf("%s example output:\n%s", framework, out.String())
		}
	})

	waitForServer(t, framework, port, 20*time.Second)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, reqPath))
	if err != nil {
		t.Fatalf("GET %s from the %s example: %v", reqPath, framework, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s example returned %d for %s", framework, resp.StatusCode, reqPath)
	}

	// The fact is recorded after the response is written, so give the flush a
	// moment rather than racing it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), received...)
}

// assertTemplate is the assertion every live test shares: the recipe must send
// the route, not the URL.
func assertTemplate(t *testing.T, framework string, got []string, want string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: the stub server received no fact", framework)
	}
	if got[0] != want {
		t.Errorf("%s sent path_template %q, want %q — the recipe is documenting a template it "+
			"does not produce", framework, got[0], want)
	}
	for _, tpl := range got {
		if strings.Contains(tpl, "1234") {
			t.Errorf("%s sent a raw id in %q; that is one dimension value per user", framework, tpl)
		}
	}
}

func TestExpressRecipeSendsSanitizedFact(t *testing.T) {
	requireNodeModules(t, "express")
	got := runExample(t, "express", "node", []string{"express/server.js"}, "/users/1234")
	assertTemplate(t, "express", got, "/users/{id}")
}

func TestFastAPIRecipeSendsSanitizedFact(t *testing.T) {
	python := requirePython(t, "fastapi", "uvicorn", "gravix")
	port := freePort(t)
	got := runExampleAt(t, "fastapi", python, []string{"fastapi/main.py", fmt.Sprint(port)}, port, "/users/1234")
	assertTemplate(t, "fastapi", got, "/users/{user_id}")
}

func TestFlaskRecipeSendsSanitizedFact(t *testing.T) {
	python := requirePython(t, "flask", "gravix")
	port := freePort(t)
	got := runExampleAt(t, "flask", python, []string{"flask/app.py", fmt.Sprint(port)}, port, "/users/1234")
	assertTemplate(t, "flask", got, "/users/{user_id}")
}

func TestDjangoRecipeSendsSanitizedFact(t *testing.T) {
	python := requirePython(t, "django", "gravix")
	port := freePort(t)
	got := runExampleAt(t, "django", python, []string{"django/app.py", fmt.Sprint(port)}, port, "/users/1234/")
	assertTemplate(t, "django", got, "/users/{user_id}")
}

// runExampleAt is runExample for examples that take their port as an argument
// rather than reading PORT from the environment.
func runExampleAt(t *testing.T, framework, interpreter string, args []string, port int, reqPath string) []string {
	t.Helper()

	var mu sync.Mutex
	var received []string
	stub := newStubIngestionServer(t, &received, &mu)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, interpreter, args...)
	cmd.Env = append(os.Environ(),
		"GRAVIX_ENDPOINT="+stub.URL,
		"GRAVIX_API_KEY=recipe-test-key",
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s example: %v", framework, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if t.Failed() {
			t.Logf("%s example output:\n%s", framework, out.String())
		}
	})

	waitForServer(t, framework, port, 20*time.Second)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, reqPath))
	if err != nil {
		t.Fatalf("GET %s from the %s example: %v", reqPath, framework, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s example returned %d for %s", framework, resp.StatusCode, reqPath)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), received...)
}

// requirePython finds an interpreter that has the named modules, preferring
// GRAVIX_RECIPE_PYTHON so a virtualenv can be pointed at.
func requirePython(t *testing.T, modules ...string) string {
	t.Helper()
	candidates := []string{}
	if v := os.Getenv("GRAVIX_RECIPE_PYTHON"); v != "" {
		candidates = append(candidates, v)
	}
	candidates = append(candidates, "python3")

	for _, python := range candidates {
		if _, err := exec.LookPath(python); err != nil {
			if _, statErr := os.Stat(python); statErr != nil {
				continue
			}
		}
		args := append([]string{"-c"}, "import "+strings.Join(modules, ", "))
		if err := exec.Command(python, args...).Run(); err == nil {
			return python
		}
	}
	t.Skipf("python3 with %s not found on PATH", strings.Join(modules, ", "))
	return ""
}

// requireNodeModules skips when the example's dependencies are not installed.
func requireNodeModules(t *testing.T, names ...string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found on PATH")
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join("express", "node_modules", name)); err != nil {
			t.Skipf("express/node_modules/%s missing; run `npm install` in examples/recipes/express", name)
		}
	}
}

// ─── AC-11 ───

// TestGinRecipeSendsSanitizedFact runs the Gin example as a subprocess rather
// than in-process.
//
// §6.7e asks for in-process, but §6.5 requires Gin never to enter the root
// module's dependency graph — and this test file *is* in the root module, so
// importing Gin here would do exactly what §6.5 forbids (`go vet` confirms:
// "no required module provides package github.com/gin-gonic/gin"). The two
// sections cannot both hold. Recorded as SD-018; the criterion AC-11 actually
// states — request /users/1234, assert the recorded template is /users/{id} —
// is unaffected by which process the router runs in.
func TestGinRecipeSendsSanitizedFact(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not found on PATH")
	}

	var mu sync.Mutex
	var received []string
	stub := newStubIngestionServer(t, &received, &mu)

	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Built first, then executed — not `go run`.
	//
	// `go run` compiles and then execs a child. Killing the `go` process leaves
	// that child alive holding the stdout pipe this test handed it, so
	// cmd.Wait() blocks forever and the whole package times out. Observed:
	// 300s timeout against a server that answers in under 25s standalone.
	binary := filepath.Join(t.TempDir(), "gin-example")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = "gin"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the gin example: %v\n%s", err, out)
	}

	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = "gin"
	cmd.Env = append(os.Environ(),
		"GRAVIX_ENDPOINT="+stub.URL,
		"GRAVIX_API_KEY=recipe-test-key",
		fmt.Sprintf("PORT=%d", port),
		"GIN_MODE=release",
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start the gin example: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if t.Failed() {
			t.Logf("gin example output:\n%s", out.String())
		}
	})

	waitForServer(t, "gin", port, 20*time.Second)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/users/1234", port))
	if err != nil {
		t.Fatalf("GET /users/1234 from the gin example: %v", err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), received...)
	mu.Unlock()

	assertTemplate(t, "gin", got, "/users/{id}")
}
