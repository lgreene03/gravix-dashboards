// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package degrade

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/license"
)

const repoRoot = "../.."

var expiry = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

// licensed builds a Result the way verification would, without needing a token.
func licensed(expiresAt time.Time) Result {
	return Result{License: &license.License{
		LicenseID: "lic-test",
		Customer:  "Test Customer",
		Plan:      license.PlanPro,
		Features:  []string{license.FeatureWildcard},
		IssuedAt:  expiresAt.AddDate(-1, 0, 0),
		ExpiresAt: expiresAt,
	}}
}

func TestEvaluateStates(t *testing.T) {
	cases := []struct {
		name string
		res  Result
		now  time.Time
		want State
	}{
		{"no licence configured", Result{}, expiry, StateAbsent},
		{"malformed token", Result{Err: license.ErrMalformed}, expiry, StateAbsent},
		{"bad signature", Result{Err: license.ErrSignatureInvalid}, expiry, StateAbsent},
		{"well inside term", licensed(expiry), expiry.AddDate(0, -6, 0), StateLicensed},
		{"on the expiry instant", licensed(expiry), expiry, StateLicensed},
		{"an hour past expiry", licensed(expiry), expiry.Add(time.Hour), StateLicensed},
		{"at the skew tolerance", licensed(expiry), expiry.Add(ClockSkewTolerance), StateLicensed},
		{"just past tolerance", licensed(expiry), expiry.Add(ClockSkewTolerance + time.Minute), StateGrace},
		{"day 13 of grace", licensed(expiry), expiry.AddDate(0, 0, 13), StateGrace},
		{"the last instant of grace", licensed(expiry), expiry.Add(GracePeriod), StateGrace},
		{"one second past grace", licensed(expiry), expiry.Add(GracePeriod + time.Second), StateReadOnly},
		{"a year past grace", licensed(expiry), expiry.AddDate(1, 0, 0), StateReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Evaluate(tc.res, tc.now); got != tc.want {
				t.Errorf("Evaluate = %q; want %q", got, tc.want)
			}
		})
	}
}

// A forged or corrupt token is StateAbsent, not a distinct "invalid" state.
// Charter §7.5: it is not an error condition. It also denies a forger any signal
// that would tell them their edit changed anything.
func TestTamperedLicenceIsIndistinguishableFromAbsent(t *testing.T) {
	tampered := Result{Err: license.ErrSignatureInvalid}
	if got := Evaluate(tampered, expiry); got != StateAbsent {
		t.Errorf("a tampered licence evaluated to %q; want %q", got, StateAbsent)
	}
	if n := Notice(Evaluate(tampered, expiry), time.Time{}); n != "" {
		t.Errorf("a tampered licence produced the notice %q; want silence", n)
	}
}

// AC-6. Full function for fourteen days after expiry, and the notice says the
// date it ends, because "grace period" means nothing to somebody reading it.
func TestGracePeriodFullFunction(t *testing.T) {
	if GracePeriod != 14*24*time.Hour {
		t.Fatalf("GracePeriod = %v; want 14 days", GracePeriod)
	}

	for d := 0; d <= 14; d++ {
		now := expiry.AddDate(0, 0, d)
		s := Evaluate(licensed(expiry), now)
		if s == StateReadOnly {
			t.Errorf("day %d after expiry is already %q; full function must last 14 days", d, s)
		}
		// A write must succeed on every one of those days.
		called := false
		if err := Guard(context.Background(), s, "update", func() error { called = true; return nil }); err != nil {
			t.Errorf("day %d after expiry: Guard refused a write: %v", d, err)
		}
		if !called {
			t.Errorf("day %d after expiry: the write did not run", d)
		}
	}

	if s := Evaluate(licensed(expiry), expiry.AddDate(0, 0, 15)); s != StateReadOnly {
		t.Errorf("day 15 after expiry = %q; want %q", s, StateReadOnly)
	}
}

func TestNoticeTextIsFactualAndDated(t *testing.T) {
	if n := Notice(StateLicensed, expiry); n != "" {
		t.Errorf("a working licence produced the notice %q; want silence", n)
	}

	grace := Notice(StateGrace, expiry)
	readOnly := Notice(StateReadOnly, expiry)

	for name, n := range map[string]string{"grace": grace, "read-only": readOnly} {
		if !strings.Contains(n, "2026-11-04") {
			t.Errorf("%s notice does not say when the licence expired: %q", name, n)
		}
		if !strings.Contains(n, "unaffected") {
			t.Errorf("%s notice does not say core monitoring is unaffected: %q", name, n)
		}
		// Charter §7.4. A notice that sells is a nag.
		if m := nagPattern.FindString(n); m != "" {
			t.Errorf("%s notice contains sales language %q: %q", name, m, n)
		}
	}
	if !strings.Contains(grace, "continue to work until 2026-11-18") {
		t.Errorf("the grace notice does not say when function ends: %q", grace)
	}
	if !strings.Contains(readOnly, "readable and exportable") {
		t.Errorf("the read-only notice does not say what still works: %q", readOnly)
	}
}

func TestSkewWarning(t *testing.T) {
	if w := SkewWarning(Result{}, expiry); w != "" {
		t.Errorf("no licence produced the warning %q; want silence", w)
	}
	if w := SkewWarning(licensed(expiry), expiry.Add(time.Hour)); w != "" {
		t.Errorf("an hour of skew produced %q; want silence below the tolerance", w)
	}
	w := SkewWarning(licensed(expiry), expiry.AddDate(0, 0, 3))
	if !strings.Contains(w, "check NTP") {
		t.Errorf("SkewWarning = %q; want it to name the likely cause", w)
	}
	if !strings.Contains(w, "72h0m0s") {
		t.Errorf("SkewWarning = %q; want it to say how far in the past", w)
	}
}

// AC-7. The OSS build is the overwhelming majority of installs, and it must be
// unable to tell that any of this code exists.
func TestAbsentStateIsSilent(t *testing.T) {
	if got := Notice(StateAbsent, time.Time{}); got != "" {
		t.Errorf("Notice(StateAbsent) = %q; want \"\"", got)
	}

	var buf bytes.Buffer
	r := NewReporter(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for _, now := range []time.Time{expiry, expiry.AddDate(1, 0, 0), {}} {
		r.Report(Result{}, now)
		r.Report(Result{Err: license.ErrSignatureInvalid}, now)
		r.Report(Result{Err: license.ErrMalformed}, now)
	}
	if buf.Len() != 0 {
		t.Errorf("StateAbsent wrote %d bytes to the log:\n%s", buf.Len(), buf.String())
	}

	// No metric either: the package must not register a collector.
	src := packageSource(t)
	for _, banned := range []string{"prometheus.", "promauto.", "MustRegister"} {
		if strings.Contains(src, banned) {
			t.Errorf("the package references %q; StateAbsent must emit no metric", banned)
		}
	}
	// And no header outside the explicit refusal path.
	if n := strings.Count(src, "w.Header()"); n != 1 {
		t.Errorf("the package sets headers in %d places; only WriteRefusal may, and only on a refusal", n)
	}
}

// The skew warning is logged once per process, not once per request. A licence
// is checked on a timer; a line per check is a line every minute forever.
func TestSkewIsLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(slog.New(slog.NewTextHandler(&buf, nil)))
	for i := 0; i < 50; i++ {
		r.Report(licensed(expiry), expiry.AddDate(0, 0, 3))
	}
	if n := strings.Count(buf.String(), "check NTP"); n != 1 {
		t.Errorf("the skew warning was logged %d times; want exactly 1", n)
	}
}

func TestNilReporterAndNilLoggerAreSafe(t *testing.T) {
	var r *Reporter
	r.Report(licensed(expiry), expiry.AddDate(0, 0, 3)) // must not panic
	NewReporter(nil).Report(licensed(expiry), expiry.AddDate(0, 0, 3))
}

// AC-9. Nothing here dials. Two checks, because either alone is weak: the source
// contains no client construct, and exercising every state with a transport that
// fails the test produces no call.
func TestNoNetworkInAnyState(t *testing.T) {
	src := packageSource(t)
	for _, banned := range []string{
		"http.Get(", "http.Post(", "http.Head(", "http.DefaultClient",
		"http.Client{", "net.Dial", "url.Parse(", "tls.Dial",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("the package contains %q; licence handling must never reach the network", banned)
		}
	}

	prev := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("a request was made to %s; degrade must never dial", r.URL)
		return nil, context.Canceled
	})
	t.Cleanup(func() { http.DefaultTransport = prev })

	for _, res := range []Result{{}, licensed(expiry), {Err: license.ErrExpired}} {
		for _, now := range []time.Time{expiry.AddDate(0, -1, 0), expiry.Add(time.Hour), expiry.AddDate(0, 0, 3), expiry.AddDate(1, 0, 0)} {
			s := Evaluate(res, now)
			_ = Notice(s, expiry)
			_ = SkewWarning(res, now)
			_ = Payload(s, expiry)
			_ = Guard(context.Background(), s, "probe", func() error { return nil })
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AC-10. This spec may not edit core, and the durable form of that is not "the
// diff was clean once" — it is that nothing in core knows this package exists.
// A core file that imported ee/degrade would break `make build-oss` the moment
// ee/ was deleted, which is charter §7.1's whole point.
func TestNoCoreFilesModified(t *testing.T) {
	needle := "gravix-dashboards/" + "ee/degrade"
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
			case "ee", ".git", "node_modules", "bin", "data", "docs":
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
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("core files reference ee/degrade, so the core no longer builds without ee/: %s",
			strings.Join(offenders, ", "))
	}
}

// AC-11. The OSS binaries' dependency graphs contain nothing under ee/. This is
// the same guarantee `make build-oss` proves by deletion, asserted cheaply
// enough to run on every commit rather than only in the oss-integrity job.
func TestCoreBuildsWithoutEE(t *testing.T) {
	targets := []string{
		"./services/gateway/", "./services/ingestion/", "./cmd/cli/",
		"./transforms/request_metrics_minute/", "./cmd/purge/",
	}
	args := append([]string{"list", "-deps"}, targets...)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "gravix-dashboards/ee/") {
			t.Errorf("an OSS binary depends on %s; the core must build with ee/ deleted", dep)
		}
	}

	// And the job that proves it by actually deleting ee/ still runs.
	ci, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	for _, target := range []string{"make build-oss", "make test-oss"} {
		if !strings.Contains(string(ci), target) {
			t.Errorf("CI does not run %q, so charter §7.1 is unproven on each commit", target)
		}
	}
}

// nagPattern matches calls to action, not every mention of a plan name. Charter
// §7.4 forbids showing a free user a locked feature and inviting them to pay;
// what it forbids is the invitation. A feature that is simply absent for a plan
// sells nobody anything.
//
// It is anchored so that "Go produced no ..." in a benchmark test is not a nag
// screen, and the "upgrade" branch names a plan or a moment ("upgrade to Pro",
// "upgrade now") rather than the bare word — ee/fleet orchestrates software
// upgrades, and "upgrade to v2.0.0" in a log line is not an advertisement. The
// dashboard keeps the broader `upgrade (to|now|your)` in its own
// TestNoUpsellInDashboard, where the word almost always is one.
var nagPattern = regexp.MustCompile(`(?i)` +
	`upgrade\s+(now\b|today\b|to\s+(the\s+)?(pro|scale|enterprise|paid|premium)|your\s+plan)` +
	`|go\s+pro\b` +
	`|unlock\s+(this|these|with|by)` +
	`|premium\s+feature` +
	`|start\s+(your\s+)?free\s+trial` +
	`|available\s+on\s+(the\s+)?(pro|scale|enterprise)`)

// The nag pattern has to catch what it is for. Narrowing it so that
// "upgrade to v2.0.0" is not an advertisement would be worthless if it stopped
// catching "upgrade to Pro", so both directions are pinned here.
func TestNagPatternCatchesUpsellAndNotSoftwareUpgrades(t *testing.T) {
	upsell := []string{
		"Upgrade now to keep your dashboards",
		"Upgrade to Pro for alerting",
		"upgrade to the Enterprise plan",
		"Upgrade your plan to continue",
		"Go Pro to unlock this",
		"Unlock this with Gravix Scale",
		"This is a premium feature",
		"Start your free trial",
		"Available on the Pro plan",
		"upgrade today for unlimited retention",
	}
	for _, s := range upsell {
		if !nagPattern.MatchString(s) {
			t.Errorf("nagPattern does not catch %q, which is exactly what charter §7.4 forbids", s)
		}
	}

	fine := []string{
		"upgrade to v2.0.0",
		"fleet: upgrade to " + "v1.4.2" + " refused",
		"Go produced no output for this deployment",
		"unlock the mutex before returning",
		"the free tier includes every capability listed above",
		"Pro and Enterprise are described on the pricing page",
	}
	for _, s := range fine {
		if m := nagPattern.FindString(s); m != "" {
			t.Errorf("nagPattern flags %q on %q; it must match a sales pitch, not the words in one", m, s)
		}
	}
}

// AC-12. Charter §7.4. Not one of these phrases appears in the paid tier or the
// free dashboard, and the free dashboard is the one that matters: an OSS user
// who never pays anything must never be sold to.
func TestNoNagUI(t *testing.T) {
	// Two files carry these patterns because their job is to forbid them, so a
	// scan that failed on them would be a check that cannot pass. §8's command 6
	// has the same flaw and reports 4 rather than 0; see SD-039.
	selfReferential := map[string]string{
		"ee/degrade/degrade_test.go":           "this file: the pattern is the assertion",
		"dashboards/lib/lineage-panel.test.js": "the dashboard's own TestNoUpsellInDashboard pattern list",
	}

	for _, dir := range []string{"ee", "dashboards"} {
		root := filepath.Join(repoRoot, dir)
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || strings.Contains(path, "node_modules") {
				return nil
			}
			switch filepath.Ext(path) {
			case ".go", ".js", ".ts", ".html", ".css", ".md", ".json":
			default:
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, _ := filepath.Rel(repoRoot, path)
			if _, ok := selfReferential[filepath.ToSlash(rel)]; ok {
				return nil
			}
			for _, m := range nagPattern.FindAllString(string(src), -1) {
				t.Errorf("%s contains upsell language %q", rel, m)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// packageSource concatenates the package's non-test sources, for the assertions
// that are about what the code may not contain.
func packageSource(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var b strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		b.Write(src)
	}
	if b.Len() == 0 {
		t.Fatal("no package sources found")
	}
	return b.String()
}
