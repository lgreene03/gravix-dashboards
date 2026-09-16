// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/boundary"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

const repoRoot = "../.."

// fakeExt is an Extension that exists only inside this test binary. It is
// registered from a test body, never from a package init(), so it cannot leak
// into a production build of either gateway.
type fakeExt struct{}

func (fakeExt) Name() string       { return "fake" }
func (fakeExt) PathPrefix() string { return "/ee/fake/" }
func (fakeExt) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "fake-ok %s", r.URL.Path)
	})
}

// eeStatus issues GET /api/gateway/ee/status against a freshly mounted mux and
// returns the decoded extension list.
func eeStatus(t *testing.T) []eeStatusEntry {
	t.Helper()
	mux := http.NewServeMux()
	mountExtensions(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gateway/ee/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/gateway/ee/status = %d; want 200", rec.Code)
	}
	var body struct {
		Extensions []eeStatusEntry `json:"extensions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Extensions
}

// AC-6. This is the permanent state of every OSS install: nothing is registered,
// so the endpoint answers 200 with an empty list.
//
// It must run before TestEEStatusListsRegistered, which registers the fake and
// cannot undo it — extpoint has no Deregister, deliberately, because an ee/ init()
// has no reason to ever unregister. Go runs tests in source order within a file and
// files in sorted order, so this holds; the guard below turns a future reordering
// into a clear failure rather than a silent pass.
func TestEEStatusEmptyByDefault(t *testing.T) {
	if n := len(extpoint.Registered()); n != 0 {
		t.Fatalf("%d extension(s) were already registered when this test ran; it must run "+
			"before any test that calls extpoint.Register", n)
	}

	mux := http.NewServeMux()
	mountExtensions(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gateway/ee/status", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 — this endpoint never 404s or 503s", rec.Code)
	}
	// The exact bytes matter: a nil slice would marshal to {"extensions":null},
	// which a client checking `.extensions.length` handles differently.
	if got, want := strings.TrimSpace(rec.Body.String()), `{"extensions":[]}`; got != want {
		t.Errorf("body = %s; want %s", got, want)
	}
}

// A non-GET is the one error this endpoint has (§6.1).
func TestEEStatusRejectsNonGET(t *testing.T) {
	mux := http.NewServeMux()
	mountExtensions(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/gateway/ee/status", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d; want 405", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "GET required") {
		t.Errorf("body = %q; want it to say %q", rec.Body.String(), "GET required")
	}
}

// AC-5. StripPrefix semantics are the contract ee/ packages are written against:
// an extension mounted at /ee/fake/ sees /ping, not /ee/fake/ping.
func TestMountLoopStripsPrefix(t *testing.T) {
	extpoint.Register(fakeExt{})

	mux := http.NewServeMux()
	mountExtensions(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ee/fake/ping", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ee/fake/ping = %d; want 200", rec.Code)
	}
	if got, want := rec.Body.String(), "fake-ok /ping"; got != want {
		t.Errorf("handler saw %q; want %q — the prefix must be stripped", got, want)
	}
}

// AC-7. Runs after TestMountLoopStripsPrefix, which did the registering.
func TestEEStatusListsRegistered(t *testing.T) {
	entries := eeStatus(t)
	if len(entries) != 1 {
		t.Fatalf("status listed %d extensions; want 1", len(entries))
	}
	if entries[0].Name != "fake" || entries[0].PathPrefix != "/ee/fake/" {
		t.Errorf("entry = %+v; want {fake /ee/fake/}", entries[0])
	}
}

// AC-8. The OSS gateway is built from the same path, by the same command, and
// answers the same things it did before its body moved into this package. The
// binary is started for real rather than inspected, because the claim being made
// is about the shipped artefact and not about the source that produced it.
func TestOSSGatewayBehaviourUnchanged(t *testing.T) {
	work := t.TempDir()
	bin := filepath.Join(work, "gateway")

	// Unchanged build command: deployment artefacts still run exactly this.
	build := exec.Command("go", "build", "-o", bin, "./services/gateway/")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -o bin/gateway ./services/gateway/: %v\n%s", err, out)
	}

	addr := freeAddr(t)
	cmd := exec.Command(bin)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"GATEWAY_ADDR="+addr,
		"TENANT_DB_PATH="+filepath.Join(work, "tenants.db"),
		"JWT_SECRET=test-secret-that-is-long-enough-32",
		"RAW_DATA_DIR="+filepath.Join(work, "raw"),
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	var logs strings.Builder
	go func() { io.Copy(&logs, bufio.NewReader(stderr)) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			t.Logf("gateway stderr:\n%s", logs.String())
		}
	})

	base := "http://" + addr
	waitForLive(t, base)

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/live", http.StatusOK, "up"},
		{"/ready", http.StatusOK, `{"db":"ok"}`},
		{"/api/gateway/ee/status", http.StatusOK, `{"extensions":[]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(base + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			got, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.code {
				t.Errorf("GET %s = %d; want %d (body %s)", tc.path, resp.StatusCode, tc.code, got)
			}
			if strings.TrimSpace(string(got)) != tc.body {
				t.Errorf("GET %s body = %q; want %q", tc.path, strings.TrimSpace(string(got)), tc.body)
			}
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitForLive(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/live")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("gateway did not answer /live within 20s")
}

// AC-10. Every Phase 13 ee capability is recorded in the boundary map with its
// Crippleware Test answered, so no later spec declares its own placement.
func TestPhase13CapabilitiesRecorded(t *testing.T) {
	m, err := boundary.Load(filepath.Join(repoRoot, "docs/oss/boundary.yaml"))
	if err != nil {
		t.Fatalf("load boundary map: %v", err)
	}
	want := []string{
		"tenancy-fleet-console",
		"billing-manual-invoicing",
		"identity-scim",
		"fleet-console",
		"compliance-siem-evidence",
		"intelligence-forecasting",
		"whitelabel-custom-domains",
		"warehouse-continuous-sync",
	}
	for _, id := range want {
		c, ok := m.Get(id)
		if !ok {
			t.Errorf("%s is not in docs/oss/boundary.yaml", id)
			continue
		}
		if c.Placement != boundary.PlacementEE {
			t.Errorf("%s: placement = %q; want ee", id, c.Placement)
		}
		ct := c.CrippleWareTest
		if ct == nil {
			t.Errorf("%s: no crippleware_test block; charter §7.3 requires all five answers", id)
			continue
		}
		// All five must be no. Any yes means the capability belongs in the core.
		for _, a := range []struct {
			q      string
			answer bool
		}{
			{"Q1 a team of ten notices its absence", ct.Q1TeamOfTenNotices},
			{"Q2 affects accuracy", ct.Q2AffectsAccuracy},
			{"Q3 makes the OSS build less secure", ct.Q3WorseSecurity},
			{"Q4 was previously open", ct.Q4PreviouslyOpen},
			{"Q5 the only reason is money", ct.Q5OnlyReasonIsMoney},
		} {
			if a.answer {
				t.Errorf("%s: %s = yes; an ee placement requires all five answers to be no", id, a.q)
			}
		}
	}
}

// AC-11. The relocation moved 13,588 lines between packages. This asserts it lost
// nothing: every test that existed under services/gateway/ before the move still
// exists here, by name. A count alone would let a rename hide a deletion.
func TestRelocationPreservesTestSuite(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "pre_move_tests.txt"))
	if err != nil {
		t.Fatalf("read the pre-move test list: %v", err)
	}
	present := map[string]bool{}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if !strings.HasPrefix(line, "func Test") {
				continue
			}
			if i := strings.IndexByte(line, '('); i > 0 {
				present[line[len("func "):i]] = true
			}
		}
	}

	var missing []string
	for _, name := range strings.Fields(string(golden)) {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d test(s) did not survive the move from services/gateway/: %s",
			len(missing), strings.Join(missing, ", "))
	}
	if want := len(strings.Fields(string(golden))); want != 243 {
		t.Errorf("the golden list holds %d names; it was captured with 243", want)
	}
}

// AC-12. The core must build with ee/ physically deleted (charter §7.1), so no Go
// file outside ee/ may import an ee/ package. cmd/checkboundary enforces this over
// parsed imports; this asserts the stronger, dumber property over raw bytes, which
// also catches the string in a comment, a struct tag or a build-tagged file that a
// parser configured for one build would skip.
func TestNoCoreFileReferencesEE(t *testing.T) {
	// Assembled at runtime so this file is not itself a match.
	needle := "gravix-dashboards/" + "ee/"

	// Four files name the forbidden string as data rather than importing it: the
	// enforcer that searches for it, its tests, and the fixture it is pointed at.
	// cmd/checkboundary excludes its own testdata for the same reason. An
	// allowlist keeps the raw-byte check on everything else, where a new entry
	// has to be added deliberately and shows up in review.
	allowed := map[string]string{
		"cmd/checkboundary/main.go":                        "defines eeImportPrefix, the string it forbids",
		"cmd/checkboundary/main_test.go":                   "builds offending sources to prove the enforcer catches them",
		"cmd/checkboundary/testdata/violating/importer.go": "the deliberate violation the enforcer is tested against",
		"tests/e2e/exit_path_test.go":                      "asserts the exit path's own files do not import ee/",
	}

	var offenders []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			switch rel {
			case "ee", ".git", "node_modules", "bin", "data":
				return filepath.SkipDir
			}
			if strings.HasPrefix(info.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(src), needle) {
			if _, ok := allowed[filepath.ToSlash(rel)]; !ok {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("these files outside ee/ name an ee/ import path, which would break "+
			"the OSS build when ee/ is deleted: %s", strings.Join(offenders, ", "))
	}
}
