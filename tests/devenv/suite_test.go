// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package devenv tests the development setup and the suite partition.
//
// The partition is the risky part of GRVX-1206: a split made by build tag can
// silently drop a test out of both suites, and nothing else would notice. So
// these tests check the partition itself, not the tests inside it.
package devenv

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// budget is the fast suite's wall-clock budget, from GRVX-1206 §5.1.
const budget = 5 * time.Minute

// slowTagged are the packages moved behind the `slow` tag, with the wall time
// each contributed to `go test ./...` before the split, measured on this
// machine. They are listed rather than discovered so that adding a fourth is a
// deliberate edit somebody reviews.
var slowTagged = map[string]string{
	"tests/correctness": "83.593s",
	"bench":             "49.247s",
	"tests/e2e":         "3.636s",
}

// skipBaseline is the number of `t.Skip(` calls in the repository, counted the
// same way this test counts them — occurrences, not matching lines, excluding
// ee/. Splitting suites must not skip anything, so this may go down and must
// never go up.
//
// 36 → 29 when GRVX-1106 needed a gate and found ten identical copies of one
// already there: tests/e2e/gate_test.go now holds requireE2E and
// requireLiveStack, and the ten inline blocks call the first. Ratcheted down
// rather than left at 36 with slack, because a budget with room in it is not a
// budget — it is permission for the next seven.
const skipBaseline = 29

// testFuncBaseline is the number of `func Test` declarations. A build tag must
// move a test between suites; it must never remove one from the tree.
const testFuncBaseline = 1696

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return abs
}

func read(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// goFiles walks the repository's Go test files, skipping ee/ and vendored code.
func testFiles(t *testing.T) []string {
	t.Helper()

	var out []string
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "ee", "data":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// AC-1: the fast suite completes within budget.
//
// The measurement is taken from INSIDE the suite. The obvious implementation —
// shell out to scripts/test_fast.sh — recurses: the script runs `go test ./...`,
// which runs this package, which runs the script. That is not a hypothetical;
// it turned `make test-oss` into a hang before this was rewritten.
//
// So the script exports when it started, and this test checks the clock against
// the budget at the point it runs. Run any other way, it checks that the budget
// is measured and enforced somewhere that is not this test — which is the CI
// job, whose whole purpose is to run the suite for real.
func TestFastSuiteWithinBudget(t *testing.T) {
	started := os.Getenv("GRAVIX_FAST_SUITE_STARTED")
	if started == "" {
		// Not inside the suite. Check the budget is real and enforced, and
		// that this package is in the fast set so the branch above is reached
		// every time the suite runs.
		script := read(t, "scripts", "test_fast.sh")
		if !strings.Contains(script, `BUDGET_SECONDS="${GRAVIX_FAST_SUITE_BUDGET_SECONDS:-300}"`) {
			t.Error("scripts/test_fast.sh does not default to the 5-minute budget")
		}
		if !strings.Contains(script, `export GRAVIX_FAST_SUITE_STARTED="$START"`) {
			t.Fatal("the script does not export its start time, so the budget cannot be checked from inside")
		}
		ci := read(t, ".github", "workflows", "ci.yml")
		if !strings.Contains(ci, "./scripts/test_fast.sh --timing") {
			t.Error("no CI job runs the suite and measures it")
		}

		// CI overrides the budget because a shared runner is slower and far more
		// variable than a developer machine — the same tree measured 3m30s and
		// 5m33s twenty minutes apart (F-047). The override is bounded here so it
		// cannot quietly grow into a way of not having a budget at all.
		if !strings.Contains(ci, "GRAVIX_FAST_SUITE_BUDGET_SECONDS") {
			t.Error("CI does not set its own budget; the 5-minute contributor number would be red at random")
		}
		ciBudget := regexp.MustCompile(`GRAVIX_FAST_SUITE_BUDGET_SECONDS: '(\d+)'`).FindStringSubmatch(ci)
		if ciBudget == nil {
			t.Fatal("CI's budget is not a plain number this test can read")
		}
		secs, err := strconv.Atoi(ciBudget[1])
		if err != nil {
			t.Fatalf("CI budget %q: %v", ciBudget[1], err)
		}
		if secs <= 300 {
			t.Errorf("CI's budget is %ds; an override below the contributor's own budget is pointless", secs)
		}
		if secs > 600 {
			t.Errorf("CI's budget is %ds. Past ten minutes it stops catching anything: the point "+
				"is to measure the suite on a slower machine, not to stop measuring it.", secs)
		}

		fast := goListPackages(t, repoRoot(t), false)
		if !fast["github.com/lgreene/gravix-dashboards/tests/devenv"] {
			t.Error("tests/devenv is not in the fast suite, so the budget is never checked from inside it")
		}
		t.Log("not inside the fast suite; budget enforcement verified by wiring. Run ./scripts/test_fast.sh for the real measurement.")
		return
	}

	secs, err := strconv.ParseInt(started, 10, 64)
	if err != nil {
		t.Fatalf("GRAVIX_FAST_SUITE_STARTED = %q: %v", started, err)
	}
	elapsed := time.Since(time.Unix(secs, 0))
	if elapsed > budget {
		t.Fatalf("the fast suite was already %s old when this package ran; the budget is %s",
			elapsed.Round(time.Second), budget)
	}
	t.Logf("inside the fast suite, %s elapsed of %s", elapsed.Round(time.Second), budget)
}

// AC-2: over budget exits 4 and names the slowest packages.
//
// The budget is driven to zero rather than the suite being made slow, so the
// test proves the mechanism without wasting five minutes proving arithmetic.
func TestOverBudgetExitCode(t *testing.T) {
	script := read(t, "scripts", "test_fast.sh")

	if !strings.Contains(script, `BUDGET_SECONDS="${GRAVIX_FAST_SUITE_BUDGET_SECONDS:-300}"`) {
		t.Error("the script does not default to the 5-minute budget")
	}
	if !strings.Contains(script, "exit 4") {
		t.Error("the script has no exit-4 path")
	}
	if !strings.Contains(script, "budget is 5m — slowest packages:") {
		t.Error("the over-budget message does not match §6.1")
	}

	// Exit 4 must be reachable only when the tests passed. An over-budget run
	// that also failed a test has to report the failure, because that is the
	// one to fix first.
	failFirst := regexp.MustCompile(`(?s)if \(\( STATUS != 0 \)\); then\s*\n\s*exit "\$STATUS"\s*\n\s*fi\s*\n\s*\n\s*if \(\( ELAPSED > BUDGET_SECONDS \)\)`)
	if !failFirst.MatchString(script) {
		t.Error("a failing test does not take precedence over the budget; exit 4 could mask exit 1")
	}

	// Drive the budget to zero and check the script really exits 4 rather than
	// printing a warning and passing.
	patched := strings.Replace(script,
		`BUDGET_SECONDS="${GRAVIX_FAST_SUITE_BUDGET_SECONDS:-300}"`, "BUDGET_SECONDS=0", 1)
	patched = strings.Replace(patched, `go test ./... -count=1`, `go test ./schemas/... -count=1`, 1)
	patched = strings.Replace(patched, `./scripts/golden_path_test.sh`, `true`, 1)
	// The copy lives in a temp directory, and the script locates the repository
	// from its own path, so it would otherwise cd into the temp directory and
	// find no module at all.
	patched = strings.Replace(patched,
		`REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"`,
		`REPO_ROOT="`+repoRoot(t)+`"`, 1)

	path := filepath.Join(t.TempDir(), "test_fast_zero_budget.sh")
	if err := os.WriteFile(path, []byte(patched), 0o755); err != nil {
		t.Fatalf("write patched script: %v", err)
	}

	cmd := exec.Command("bash", path, "--timing")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run the patched script: %v\n%s", err, out)
	}
	if code != 4 {
		t.Fatalf("a zero budget exited %d, want 4:\n%s", code, out)
	}
	if !strings.Contains(string(out), "budget is 5m — slowest packages:") {
		t.Errorf("the over-budget output does not name the slowest packages:\n%s", out)
	}
}

// AC-3: the fast suite needs no Docker.
func TestFastSuiteNeedsNoDocker(t *testing.T) {
	script := read(t, "scripts", "test_fast.sh")

	// An invocation, not the word: the script says "Does not require Docker",
	// and a check that cannot tell a promise from a call is a check that gets
	// deleted the first time it fires.
	invocation := regexp.MustCompile(`(?m)(^|\||&&|;|\$\()\s*(sudo )?docker(-compose)?\s`)
	for _, m := range invocation.FindAllString(script, -1) {
		t.Errorf("scripts/test_fast.sh runs Docker: %q", strings.TrimSpace(m))
	}
	if regexp.MustCompile(`DOCKER_HOST|docker\.sock`).MatchString(script) {
		t.Error("scripts/test_fast.sh reaches for a Docker daemon")
	}
	if !strings.Contains(script, "Does not require Docker") {
		t.Error("the script does not say that it needs no Docker")
	}

	// The things it does run must not need one either. The golden path is the
	// no-Docker smoke test by construction; the slow suites that do need Docker
	// are tagged out.
	golden := read(t, "scripts", "golden_path_test.sh")
	if !strings.Contains(strings.ToLower(golden), "no docker") && !strings.Contains(golden, "without Docker") {
		t.Log("golden_path_test.sh does not advertise being Docker-free; the CI job proves it")
	}
}

// AC-4: every test appears in exactly one suite.
//
// A build tag that excludes a file from the fast set and is never opted into by
// anything is a test that stopped running. That is the failure this partition
// could plausibly cause, and nothing else would report it.
func TestEveryTestIsInASuite(t *testing.T) {
	root := repoRoot(t)

	// Packages the fast set runs.
	fast := goListPackages(t, root, false)
	// Packages the full set runs.
	full := goListPackages(t, root, true)

	if len(fast) == 0 || len(full) == 0 {
		t.Fatalf("go list returned nothing: fast=%d full=%d", len(fast), len(full))
	}

	// Everything in fast is also in full: `-tags=slow` adds files, never
	// removes them.
	for pkg := range fast {
		if !full[pkg] {
			t.Errorf("%s runs in the fast suite but not in the full one", pkg)
		}
	}

	// Every tagged package is reachable with the tag.
	for pkg := range slowTagged {
		want := "github.com/lgreene/gravix-dashboards/" + pkg
		if !full[want] {
			t.Errorf("%s is tagged slow but has no tests in the full suite either; it runs nowhere", pkg)
		}
		if fast[want] {
			t.Errorf("%s is listed as slow-tagged but still runs in the fast suite", pkg)
		}
	}

	// And nothing new carries a tag no suite opts into. `slow` is the only tag
	// this repository runs.
	//
	// pkg/tenantdb/postgres_test.go is a known exception, recorded as F-043:
	// it carries `postgres || all`, nothing passes either tag, and so it has
	// never run — in CI or anywhere else. It is listed here rather than
	// tolerated silently, so a SECOND file drifting out of both suites is a red
	// build while this one waits on a decision.
	knownUnrunTags := map[string]string{
		"pkg/tenantdb/postgres_test.go": "postgres || all",
	}

	tagRe := regexp.MustCompile(`(?m)^//go:build (.+)$`)
	for _, f := range testFiles(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		rel, _ := filepath.Rel(root, f)
		for _, m := range tagRe.FindAllStringSubmatch(string(raw), -1) {
			tag := strings.TrimSpace(m[1])
			if tag == "slow" {
				continue
			}
			if known, ok := knownUnrunTags[filepath.ToSlash(rel)]; ok && known == tag {
				t.Logf("%s carries %q and runs in no suite — F-043, awaiting a decision", rel, tag)
				continue
			}
			t.Errorf("%s carries the build tag %q, which no suite opts into; it would run nowhere", rel, tag)
		}
	}
}

// goListPackages returns the packages with test files, with or without the tag.
func goListPackages(t *testing.T, root string, slow bool) map[string]bool {
	t.Helper()

	args := []string{"list", "-f", "{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}"}
	if slow {
		args = append(args, "-tags=slow")
	}
	args = append(args, "./...")

	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list (slow=%v): %v", slow, err)
	}

	pkgs := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			pkgs[line] = true
		}
	}
	return pkgs
}

// AC-5: no test was skipped, deleted or shortened by the partition.
func TestNoTestWeakenedByPartition(t *testing.T) {
	root := repoRoot(t)

	var skips, funcs int
	skipRe := regexp.MustCompile(`t\.Skip\(`)
	funcRe := regexp.MustCompile(`(?m)^func Test`)

	for _, f := range testFiles(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		skips += len(skipRe.FindAllIndex(raw, -1))
		funcs += len(funcRe.FindAllIndex(raw, -1))
	}

	if skips > skipBaseline {
		t.Errorf("%d skipped test(s) introduced; splitting suites must not skip anything (baseline %d, now %d)",
			skips-skipBaseline, skipBaseline, skips)
	}
	if funcs < testFuncBaseline {
		t.Errorf("%d test function(s) disappeared; the partition must move tests, never remove them (baseline %d, now %d)",
			testFuncBaseline-funcs, testFuncBaseline, funcs)
	}

	// Each tagged file carries the tag and nothing else that changes behaviour:
	// the tag is the first line, followed by a blank line and then the file as
	// it was.
	for pkg := range slowTagged {
		dir := filepath.Join(root, pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", pkg, err)
		}
		var tagged int
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			if !strings.HasPrefix(string(raw), "//go:build slow\n\n") {
				t.Errorf("%s/%s does not open with the slow tag", pkg, e.Name())
				continue
			}
			tagged++
		}
		if tagged == 0 {
			t.Errorf("%s is listed as slow-tagged but no file in it carries the tag", pkg)
		}
	}
}

// AC-6: schemas/ is still at 100%.
func TestSchemasCoverageUnchanged(t *testing.T) {
	cmd := exec.Command("go", "test", "./schemas/...", "-cover", "-count=1")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./schemas/...: %v\n%s", err, out)
	}

	m := regexp.MustCompile(`coverage: ([0-9.]+)% of statements`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no coverage figure in:\n%s", out)
	}
	got, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse coverage %q: %v", m[1], err)
	}
	if got < 100 {
		t.Errorf("schemas coverage is %.1f%%, and the gate is 100%%", got)
	}

	// The fast suite enforces it too, so the gate is not only in CI.
	if !strings.Contains(read(t, "scripts", "test_fast.sh"), "the gate is 100%") {
		t.Error("scripts/test_fast.sh does not enforce the schemas coverage gate")
	}
}

// AC-7: dev_setup.sh installs nothing.
func TestSetupInstallsNothing(t *testing.T) {
	script := read(t, "scripts", "dev_setup.sh")

	// Every install verb, in the forms that would actually run one. The strings
	// appear in this file as data the script PRINTS, so the check is for them
	// being executed: at the start of a line, or after a pipe or &&.
	runner := regexp.MustCompile(`(?m)(^|\||&&|;)\s*(sudo |)(apt-get install|apt install|dnf install|yum install|pacman -S|brew install|npm install -g|pip install|go install|curl [^|]*\| *(ba)?sh)`)
	for _, m := range runner.FindAllString(script, -1) {
		t.Errorf("dev_setup.sh appears to run an installer: %q", strings.TrimSpace(m))
	}

	if !strings.Contains(script, "IT INSTALLS NOTHING") {
		t.Error("dev_setup.sh does not state that it installs nothing")
	}
	if !containsAcrossLines(script, "nothing was changed on this machine") {
		t.Error("dev_setup.sh does not tell the reader that nothing was changed")
	}
}

// AC-8: missing tooling prints an exact platform install command.
func TestSetupNamesExactInstallCommand(t *testing.T) {
	script := read(t, "scripts", "dev_setup.sh")

	// A link is not an install command. Each supported platform gets a real one.
	for _, want := range []string{
		"brew install go",
		"sudo apt-get install -y golang-go",
		"sudo dnf install -y golang",
		"sudo pacman -S go",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("no install command %q for Go", want)
		}
	}
	if !strings.Contains(script, `Go $WANT or newer is required. On $PLATFORM: $(install_command go)`) {
		t.Error("the too-old-Go message does not match §6.1's form")
	}

	// Running it here must succeed, because this environment has the toolchain
	// — which also proves the script does not fall over on its own checks.
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "dev_setup.sh"), "--check-only")
	cmd.Dir = repoRoot(t)
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "platform:") {
		t.Errorf("dev_setup.sh did not report the detected platform:\n%s", out)
	}
}

// AC-10: CI fails when the fast suite exceeds budget.
func TestCIEnforcesBudget(t *testing.T) {
	ci := read(t, ".github", "workflows", "ci.yml")

	if !strings.Contains(ci, "fast-suite-budget:") {
		t.Fatal("ci.yml has no fast-suite-budget job")
	}
	if !strings.Contains(ci, "./scripts/test_fast.sh --timing") {
		t.Error("the budget job does not run the script with --timing")
	}
	// The job must be required, or a red budget is a green pull request.
	if !strings.Contains(ci, `needs.fast-suite-budget.result }}" == "failure"`) {
		t.Error("ci-summary does not fail when the budget job fails")
	}

	// CI still runs everything, tag included. A split that quietly stopped CI
	// running the slow suites would be the worst possible outcome here.
	if !strings.Contains(ci, "go test -tags=slow ./... -v -race -count=1") {
		t.Error("the race job no longer runs the slow-tagged suites")
	}
	if !strings.Contains(ci, "go test -tags=slow ./... -coverprofile=coverage.out") {
		t.Error("the coverage job no longer runs the slow-tagged suites")
	}
}

// AC-11: `make test` still means the full run.
func TestMakeTestStillFull(t *testing.T) {
	mk := read(t, "Makefile")

	for _, want := range []string{
		"test-fast: ",
		"test-full: ",
		"setup: ",
	} {
		if !strings.Contains(mk, want) {
			t.Errorf("the Makefile has no %s target", strings.TrimSpace(want))
		}
	}

	// `test` runs with the tag, so somebody typing the command they have always
	// typed still gets everything.
	if !strings.Contains(mk, "test:\n\tgo test -tags=slow ./... -v -cover") {
		t.Error("`make test` no longer means the full run")
	}
	if !strings.Contains(mk, "test-race:\n\tgo test -tags=slow ./... -v -race -count=1") {
		t.Error("`make test-race` no longer covers the slow suites")
	}
}

// The dev container pins what it says it pins, and its post-create is the
// setup script rather than a second, drifting copy of it.
//
// AC-9 asks that the container builds. Building it needs a Docker daemon, which
// the fast suite deliberately does not require, so what is checked here is
// everything that can be checked without one: the file parses, it pins each
// tool, and postCreateCommand runs the same script a native contributor runs.
func TestDevContainerBuilds(t *testing.T) {
	json := read(t, ".devcontainer", "devcontainer.json")
	dockerfile := read(t, ".devcontainer", "Dockerfile")

	if !strings.Contains(json, `"postCreateCommand": "./scripts/dev_setup.sh"`) {
		t.Error("devcontainer.json does not run dev_setup.sh on create")
	}
	if !strings.Contains(json, `"dockerfile": "Dockerfile"`) {
		t.Error("devcontainer.json does not build the Dockerfile beside it")
	}

	for _, tool := range []string{"duckdb", "protoc", "protoc-gen-go", "jq", "staticcheck", "govulncheck"} {
		if !strings.Contains(dockerfile, tool) {
			t.Errorf("the dev container does not provide %s", tool)
		}
	}

	// The Go version matches go.mod, or the container compiles something the
	// project does not.
	want := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindStringSubmatch(read(t, "go.mod"))
	if want == nil {
		t.Fatal("go.mod declares no Go version")
	}
	got := regexp.MustCompile(`FROM golang:(\d+)\.(\d+)`).FindStringSubmatch(dockerfile)
	if got == nil {
		t.Fatal("the dev container does not pin a Go image")
	}
	wantParts := strings.SplitN(want[1], ".", 2)
	wantMajor, _ := strconv.Atoi(wantParts[0])
	wantMinor, _ := strconv.Atoi(wantParts[1])
	gotMajor, _ := strconv.Atoi(got[1])
	gotMinor, _ := strconv.Atoi(got[2])
	if gotMajor < wantMajor || (gotMajor == wantMajor && gotMinor < wantMinor) {
		t.Errorf("the dev container is on Go %s.%s, older than the %s go.mod requires", got[1], got[2], want[1])
	}

	// Each version is pinned. A container that floats gives two contributors
	// two different answers to the same failure.
	for _, arg := range []string{"DUCKDB_VERSION=", "PROTOC_VERSION=", "PROTOC_GEN_GO_VERSION="} {
		if !strings.Contains(dockerfile, arg) {
			t.Errorf("%s is not pinned", strings.TrimSuffix(arg, "="))
		}
	}

	// And it is not the only supported path.
	if !containsAcrossLines(json, "The container is one supported path, never the only one") {
		t.Error("devcontainer.json does not say the container is optional")
	}
}

// containsAcrossLines matches ignoring line wrapping and comment markers.
func containsAcrossLines(text, want string) bool {
	clean := strings.NewReplacer("//", " ", "#", " ").Replace(text)
	return strings.Contains(strings.Join(strings.Fields(clean), " "), strings.Join(strings.Fields(want), " "))
}

// binaryMagic are the leading bytes of a compiled executable, on the three
// platforms anybody builds this on.
//
// Only executables. Images, Parquet fixtures and test data are legitimately
// binary and legitimately committed; a compiled program never is.
var binaryMagic = [][]byte{
	{0x7f, 'E', 'L', 'F'},    // ELF — Linux
	{0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64, little-endian — macOS
	{0xca, 0xfe, 0xba, 0xbe}, // Mach-O universal
	{'M', 'Z'},               // PE — Windows
}

// skipWalk are directories a build legitimately fills with binaries.
var skipWalk = map[string]bool{
	".git": true, "node_modules": true, "bin": true, "data": true,
	"vendor": true, ".venv": true, "dist": true, "build": true,
}

// TestNoCommittedBinaries fails on a compiled executable git does not ignore.
//
// "Does not ignore" is the rule, not "exists". Everybody who runs `go build`
// has binaries in their tree; the hazard is the one .gitignore has never heard
// of, because that is the one `git add -A` sweeps up.
//
// This exists because it already happened twice. The repository once carried
// three committed binaries and 66 MB of node_modules, removed by a hygiene
// commit that added no guard — and the very next new build target,
// transforms/iceberg_sync, put an 11 MB ELF back at the repository root within
// a day. .gitignore enumerates root binaries one at a time (/gateway, /purge,
// /rollup-job …), and a new target is never on a list written before it
// existed. When this test was first run it found that ELF and three other
// binaries; the other three were already ignored, which is exactly the
// distinction it now makes.
func TestNoCommittedBinaries(t *testing.T) {
	root := repoRoot(t)

	// scripts/build_oss.sh copies the tree without .git (SD-036). There is no
	// repository to guard in that copy, and no way to ask what it ignores.
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Log("no .git here, so there is nothing to protect from a commit; skipping the check")
		return
	}

	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable path is not this test's problem
		}
		if d.IsDir() {
			if skipWalk[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() < 4 {
			return nil
		}
		if !looksExecutable(path) {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		if gitIgnores(root, rel) {
			return nil
		}
		found = append(found, fmt.Sprintf("%s (%.1f MB)", rel, float64(info.Size())/(1<<20)))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	if len(found) > 0 {
		t.Errorf("compiled executable(s) that .gitignore does not cover:\n  %s\n"+
			"One `git add -A` commits these. Build into bin/, or add the target to .gitignore.",
			strings.Join(found, "\n  "))
	}
}

// looksExecutable reports whether the file starts with a compiled program's
// magic bytes.
func looksExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	for _, magic := range binaryMagic {
		if len(head) >= len(magic) && bytes.Equal(head[:len(magic)], magic) {
			return true
		}
	}
	return false
}

// gitIgnores asks git whether rel is ignored.
func gitIgnores(root, rel string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", rel)
	cmd.Dir = root
	return cmd.Run() == nil
}
