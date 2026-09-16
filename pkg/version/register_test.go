// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	return root
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(raw)
}

// AC-1 — the security policy cannot claim a support window the tooling does
// not agree with, because the table is not written by hand.
func TestSecurityTableGenerated(t *testing.T) {
	doc := repoFile(t, "SECURITY.md")

	if !strings.Contains(doc, TableBegin) || !strings.Contains(doc, TableEnd) {
		t.Fatal("SECURITY.md has no generated-table markers; the table would be hand-maintained")
	}

	releases, err := LoadRegister(filepath.Join(repoRoot(t), RegisterPath))
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	want := SecurityTable(time.Now().UTC(), releases)

	generated, _, _ := strings.Cut(doc[strings.Index(doc, TableBegin)+len(TableBegin):], TableEnd)
	if strings.TrimSpace(generated) != strings.TrimSpace(want) {
		t.Errorf("SECURITY.md's table is stale; run `make supported-versions`\n got:%s\nwant:\n%s",
			generated, want)
	}

	// And the generator says so too, which is what CI runs.
	cmd := exec.Command("go", "run", "./pkg/version/cmd/gen", "-check")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("`make supported-versions-check` fails: %v\n%s", err, out)
	}
}

// TestSecurityTableSaysSoWhenNothingIsSupported: an empty table under a
// "Supported versions" heading reads as a guarantee nobody made.
func TestSecurityTableSaysSoWhenNothingIsSupported(t *testing.T) {
	got := SecurityTable(day(2026, time.January, 1), nil)
	if !strings.Contains(got, "_none yet_") {
		t.Errorf("the empty table does not say it is empty:\n%s", got)
	}
	if !strings.Contains(got, "no versioned release") {
		t.Errorf("the empty table does not explain why:\n%s", got)
	}
}

func TestSecurityTableRendersEveryLine(t *testing.T) {
	got := SecurityTable(day(2027, time.April, 1), history())

	for _, want := range []string{
		"| v2.1.0 | current | next minor |",
		"| v2.0.0 | LTS | 2028-01-15 |",
		"| v1.0.0 | previous LTS | 2027-04-15 |",
		"security fixes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the table does not contain %q:\n%s", want, got)
		}
	}
	// A current release has no computable end date, and the table says so in
	// words rather than printing a made-up one.
	if strings.Contains(got, "| current | 0001-01-01") {
		t.Errorf("the current line rendered a zero date:\n%s", got)
	}
}

func TestLoadRegister(t *testing.T) {
	t.Run("the committed register", func(t *testing.T) {
		got, err := LoadRegister(filepath.Join(repoRoot(t), RegisterPath))
		if err != nil {
			t.Fatalf("LoadRegister: %v", err)
		}
		// Gravix has cut no versioned release. When that changes, this
		// assertion changes with it — deliberately, so adding the first
		// release is a decision somebody makes rather than a file that drifts.
		if len(got) != 0 {
			t.Errorf("the register holds %d release(s); update this test when the first is cut", len(got))
		}
	})

	t.Run("round trip", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "releases.json")
		body := `[{"version":"v1.0.0","released_at":"2026-01-15T00:00:00Z","is_lts":true}]`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		got, err := LoadRegister(path)
		if err != nil {
			t.Fatalf("LoadRegister: %v", err)
		}
		if len(got) != 1 || got[0].Version != "v1.0.0" || !got[0].IsLTS {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		dir := t.TempDir()
		cases := map[string]string{
			"not json":      "{not json",
			"no version":    `[{"released_at":"2026-01-15T00:00:00Z"}]`,
			"no date":       `[{"version":"v1.0.0"}]`,
			"blank version": `[{"version":"  ","released_at":"2026-01-15T00:00:00Z"}]`,
		}
		for name, body := range cases {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := LoadRegister(path); err == nil {
				t.Errorf("%s was accepted", name)
			}
		}

		if _, err := LoadRegister(filepath.Join(dir, "absent.json")); err == nil {
			t.Error("a missing register was accepted")
		}
	})
}

func TestReplaceTableNeedsItsMarkers(t *testing.T) {
	if _, err := ReplaceTable("# SECURITY\n\nno markers here\n", "table"); err == nil {
		t.Fatal("ReplaceTable accepted a document with no markers")
	}

	doc := "before\n" + TableBegin + "\nold\n" + TableEnd + "\nafter\n"
	got, err := ReplaceTable(doc, "new\n")
	if err != nil {
		t.Fatalf("ReplaceTable: %v", err)
	}
	if !strings.Contains(got, "new") || strings.Contains(got, "old") {
		t.Errorf("the table was not replaced: %q", got)
	}
	// The prose around it is untouched, which is the reason for the markers.
	if !strings.HasPrefix(got, "before\n") || !strings.HasSuffix(got, "after\n") {
		t.Errorf("the surrounding prose changed: %q", got)
	}
}

// AC-9
func TestLTSCIRunsWeekly(t *testing.T) {
	wf := repoFile(t, ".github/workflows/lts-ci.yml")

	if !strings.Contains(wf, "schedule:") || !strings.Contains(wf, "cron:") {
		t.Fatal("the LTS workflow has no schedule; a branch that only builds when somebody " +
			"touches it will be broken by a dependency change and nobody will know")
	}
	if !strings.Contains(wf, "* * 1") {
		t.Errorf("the schedule is not weekly on a Monday:\n%s", wf)
	}
	if !strings.Contains(wf, "branches:") || !strings.Contains(wf, "lts/**") {
		t.Error("the LTS workflow does not run on pushes to LTS branches")
	}
	// A failure nobody sees is the same as no run.
	if !strings.Contains(wf, "gh issue create") {
		t.Error("a failing weekly run opens no issue")
	}
}

// AC-10
func TestLTSBranchesRespectBoundary(t *testing.T) {
	wf := repoFile(t, ".github/workflows/lts-ci.yml")
	for _, gate := range []string{"make check-boundary", "make build-oss", "make test-oss"} {
		if !strings.Contains(wf, gate) {
			t.Errorf("the LTS workflow does not run %q; an LTS shipping a core that depends "+
				"on ee/ is a licence problem on a branch people run in production", gate)
		}
	}
	if !strings.Contains(wf, "-tags=slow") {
		t.Error("the LTS workflow does not run the full suite")
	}
}

// AC-11
func TestUpgradeGuideCoversLTS(t *testing.T) {
	guide := repoFile(t, "docs/upgrade-guide.md")

	if !strings.Contains(guide, "## Upgrading between LTS releases") {
		t.Fatal("the upgrade guide has no LTS-to-LTS section")
	}
	for _, want := range []string{
		"one minor at a time",
		"recompute",
		"backup",
	} {
		if !strings.Contains(strings.ToLower(guide), strings.ToLower(want)) {
			t.Errorf("the LTS section does not mention %q", want)
		}
	}
}

// AC-7 — an LTS branch whose history cannot be traced back to main is a fork
// nobody can reason about.
func TestBackportTraceability(t *testing.T) {
	script := repoFile(t, "scripts/backport.sh")

	if !strings.Contains(script, "git cherry-pick -x") {
		t.Error("backport.sh does not cherry-pick with -x; backported commits would not record " +
			"the commit they came from")
	}
	if !strings.Contains(script, "cherry picked from commit") {
		t.Error("backport.sh does not explain what -x does; the traceability is the point")
	}
}

// AC-8
func TestFailedLTSSuiteBlocksPush(t *testing.T) {
	script := repoFile(t, "scripts/backport.sh")

	if !strings.Contains(script, "backport: LTS test suite failed; not pushing") {
		t.Error("backport.sh does not refuse to leave a failing backport in place")
	}
	// And it undoes the cherry-pick rather than leaving a broken commit on the
	// branch for somebody else to find.
	if !strings.Contains(script, "git reset --hard HEAD~1") {
		t.Error("a failing backport is left applied")
	}
	if !strings.Contains(script, "go test ./...") {
		t.Error("backport.sh does not run the suite at all")
	}
}

func TestBackportScriptRefusesIneligibleKinds(t *testing.T) {
	root := repoRoot(t)

	cases := map[string]int{
		"security":         0,
		"data-correctness": 0,
		"data-loss":        0,
		"crash":            0,
		"performance":      3,
		"feature":          3,
		"dependency":       3,
		"mystery":          2,
	}
	for kind, want := range cases {
		cmd := exec.Command("./scripts/backport.sh",
			"--commit", "HEAD", "--to", "lts/v1", "--kind", kind, "--dry-run")
		cmd.Dir = root
		err := cmd.Run()

		got := 0
		if exit, ok := err.(*exec.ExitError); ok {
			got = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		if got != want {
			t.Errorf("kind %q exited %d, want %d", kind, got, want)
		}
	}
}

// TestPolicyDocumentAndCodeAgree holds the published table and version.Eligible
// together. A policy page promising something the tooling refuses is the
// failure this whole spec is trying to prevent, one level up.
func TestPolicyDocumentAndCodeAgree(t *testing.T) {
	policy := repoFile(t, "docs/oss/lts-policy.md")

	if !strings.Contains(policy, "Security fixes are free") {
		t.Error("the policy does not lead with the charter §7.3 Q3 commitment")
	}
	for _, want := range []string{"12 months", "3 months", "designated at release"} {
		if !strings.Contains(policy, want) {
			t.Errorf("the policy does not state %q", want)
		}
	}
	// Every kind the code classifies appears in the published table, so a
	// reader is never left guessing about one.
	for _, kind := range []BackportKind{
		KindSecurity, KindCorrectness, KindDataLoss, KindCrash,
		KindPerformance, KindFeature, KindDependency,
	} {
		word := map[BackportKind]string{
			KindSecurity:    "Security fix",
			KindCorrectness: "Data-correctness fix",
			KindDataLoss:    "loses data",
			KindCrash:       "crash",
			KindPerformance: "Performance improvement",
			KindFeature:     "New capability",
			KindDependency:  "Dependency bump",
		}[kind]
		if !strings.Contains(policy, word) {
			t.Errorf("the policy's backport table has no row for %s (looked for %q)", kind, word)
		}
	}
}
