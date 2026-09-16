// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// A release history with two LTS lines and the minors between them, which is
// the shape every window calculation below is checked against.
func history() []Release {
	return []Release{
		{Version: "v1.0.0", ReleasedAt: day(2026, time.January, 15), IsLTS: true},
		{Version: "v1.1.0", ReleasedAt: day(2026, time.April, 15)},
		{Version: "v1.2.0", ReleasedAt: day(2026, time.July, 15)},
		{Version: "v2.0.0", ReleasedAt: day(2027, time.January, 15), IsLTS: true},
		{Version: "v2.1.0", ReleasedAt: day(2027, time.March, 15)},
	}
}

// AC-2
func TestLTSWindowIsTwelveMonths(t *testing.T) {
	// v2.0.0 is the newest LTS and not the newest release, so the window
	// applies rather than the current-line rule.
	releases := history()

	s, err := Classify("v2.0.0", day(2027, time.June, 1), releases)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if s.Line != LineLTS {
		t.Errorf("line = %q, want %q", s.Line, LineLTS)
	}

	want := day(2027, time.January, 15).Add(LTSWindow)
	if !s.SupportedUntil.Equal(want) {
		t.Errorf("supported until %v, want %v (12 months from designation)", s.SupportedUntil, want)
	}
	if LTSWindow != 365*24*time.Hour {
		t.Errorf("LTSWindow = %v, want 365 days", LTSWindow)
	}

	// Supported the day before it ends, and not the day it does.
	if s, _ := Classify("v2.0.0", want.Add(-24*time.Hour), releases); s.Line != LineLTS {
		t.Errorf("the day before the window closes, line = %q, want %q", s.Line, LineLTS)
	}
	if s, _ := Classify("v2.0.0", want, releases); s.Line != LineUnsupported {
		t.Errorf("on the day the window closes, line = %q, want %q", s.Line, LineUnsupported)
	}
}

// AC-3
func TestPreviousLTSOverlap(t *testing.T) {
	releases := history()

	// v1.0.0 is superseded by v2.0.0, designated 2027-01-15. On the annual
	// cadence its own twelve-month window ends on that very day, so the overlap
	// has to be additive or it would be exactly zero — which is the opposite of
	// what an upgrade window is for.
	s, err := Classify("v1.0.0", day(2027, time.January, 20), releases)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if s.Line != LinePreviousLTS {
		t.Errorf("line = %q, want %q — its 12-month window ended five days ago and the "+
			"three-month overlap is what keeps it supported", s.Line, LinePreviousLTS)
	}
	overlap := day(2027, time.January, 15).Add(PreviousLTSOverlap)
	if !s.SupportedUntil.Equal(overlap) {
		t.Errorf("supported until %v, want %v — three months after the new LTS was designated",
			s.SupportedUntil, overlap)
	}

	// And it ends there. A previous LTS does not linger.
	if s, _ := Classify("v1.0.0", overlap, releases); s.Line != LineUnsupported {
		t.Errorf("on the day the overlap closes, line = %q, want %q", s.Line, LineUnsupported)
	}

	// A previous LTS receives security fixes only. That is the whole point of
	// the overlap: enough to stay safe while you upgrade, not enough to stay.
	if len(s.Receives) != 1 || s.Receives[0] != "security fixes" {
		t.Errorf("receives = %v, want security fixes only", s.Receives)
	}

	// An LTS superseded early gets three months from that point rather than the
	// rest of its year. That is the deliberate edge: once it is the previous
	// LTS it receives security fixes only, so keeping it on the LTS line for
	// another ten months would be promising more than the table says.
	early := []Release{
		{Version: "v1.0.0", ReleasedAt: day(2026, time.January, 15), IsLTS: true},
		{Version: "v2.0.0", ReleasedAt: day(2026, time.March, 15), IsLTS: true},
	}
	s, err = Classify("v1.0.0", day(2026, time.April, 1), early)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	overlapEnds := day(2026, time.March, 15).Add(PreviousLTSOverlap)
	if !s.SupportedUntil.Equal(overlapEnds) {
		t.Errorf("supported until %v, want %v — three months from the new designation",
			s.SupportedUntil, overlapEnds)
	}
	if PreviousLTSOverlap != 90*24*time.Hour {
		t.Errorf("PreviousLTSOverlap = %v, want 90 days", PreviousLTSOverlap)
	}
}

func TestCurrentIsTheNewestRelease(t *testing.T) {
	releases := history()

	s, err := Classify("v2.1.0", day(2027, time.April, 1), releases)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if s.Line != LineCurrent {
		t.Errorf("line = %q, want %q", s.Line, LineCurrent)
	}
	// Zero rather than a guessed date: we do not know when the next minor
	// lands, and inventing one publishes a support promise with a made-up
	// expiry on it.
	if !s.SupportedUntil.IsZero() {
		t.Errorf("supported until %v; current has no computable end date", s.SupportedUntil)
	}
}

// TestANonLTSMinorIsUnsupportedOnceSuperseded is the row that makes the LTS
// designation worth planning around.
func TestANonLTSMinorIsUnsupportedOnceSuperseded(t *testing.T) {
	releases := history()
	for _, v := range []string{"v1.1.0", "v1.2.0"} {
		s, err := Classify(v, day(2027, time.April, 1), releases)
		if err != nil {
			t.Fatalf("Classify(%s): %v", v, err)
		}
		if s.Line != LineUnsupported {
			t.Errorf("%s line = %q, want %q", v, s.Line, LineUnsupported)
		}
		if len(s.Receives) != 0 {
			t.Errorf("%s receives %v, want nothing", v, s.Receives)
		}
	}
}

func TestClassifyRejectsAnUnknownVersion(t *testing.T) {
	_, err := Classify("v9.9.9", day(2027, time.April, 1), history())
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got %v, want ErrUnknownVersion", err)
	}
	if !strings.Contains(err.Error(), `"v9.9.9"`) {
		t.Errorf("the error does not name the version: %v", err)
	}
}

func TestSupportedVersionsListsOnlyWhatIsSupported(t *testing.T) {
	names := func(in []Support) []string {
		var out []string
		for _, s := range in {
			out = append(out, s.Version)
		}
		return out
	}

	// During the overlap: three lines. v2.1.0 current, v2.0.0 LTS, and v1.0.0
	// still receiving security fixes while teams upgrade. The two non-LTS
	// minors were never eligible for anything.
	got := SupportedVersions(day(2027, time.April, 1), history())
	if want := "v2.1.0,v2.0.0,v1.0.0"; strings.Join(names(got), ",") != want {
		t.Errorf("during the overlap, supported = %v, want %s", names(got), want)
	}

	// Newest first: a reader scanning the table wants the line they should be
	// on at the top.
	if len(got) > 1 && CompareSemver(got[0].Version, got[1].Version) < 0 {
		t.Errorf("the table is oldest-first: %v", names(got))
	}

	// After it closes: two.
	got = SupportedVersions(day(2027, time.May, 1), history())
	if want := "v2.1.0,v2.0.0"; strings.Join(names(got), ",") != want {
		t.Errorf("after the overlap, supported = %v, want %s", names(got), want)
	}
}

func TestSupportedVersionsOnAnEmptyRegister(t *testing.T) {
	if got := SupportedVersions(day(2026, time.January, 1), nil); len(got) != 0 {
		t.Errorf("an empty register produced %v", got)
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.9.0", "v1.10.0", -1}, // not string order
		{"v2.0.0", "v1.99.99", 1},
		{"1.0.0", "v1.0.0", 0}, // the v is optional
		{"v1.0", "v1.0.0", 0},  // a missing component is zero
	}
	for _, tc := range cases {
		if got := CompareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestNewestIsByVersionNotByDate: a patch to an old line is often released
// after a newer minor, and "current" must still mean the highest version.
func TestNewestIsByVersionNotByDate(t *testing.T) {
	releases := []Release{
		{Version: "v2.0.0", ReleasedAt: day(2027, time.January, 15), IsLTS: true},
		{Version: "v1.0.1", ReleasedAt: day(2027, time.February, 1), IsLTS: true}, // patched later
	}
	s, err := Classify("v2.0.0", day(2027, time.March, 1), releases)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if s.Line != LineCurrent {
		t.Errorf("v2.0.0 line = %q, want current — a later-dated patch to v1 must not "+
			"make v1.0.1 the current release", s.Line)
	}
}

// AC-4 — the charter constraint, and the reason this package exists in the
// Apache-2.0 core rather than in ee/.
func TestSecurityBackportsAreFree(t *testing.T) {
	ok, reason := Eligible(KindSecurity)
	if !ok {
		t.Fatalf("security fixes are not backport-eligible: %s", reason)
	}
	if !strings.Contains(reason, "free") {
		t.Errorf("the reason does not say they are free: %q", reason)
	}

	// No kind requires a licence, and this is asserted rather than left as the
	// absence of a check.
	for _, k := range []BackportKind{
		KindSecurity, KindCorrectness, KindDataLoss, KindCrash,
		KindPerformance, KindFeature, KindDependency,
	} {
		if RequiresLicence(k) {
			t.Errorf("backporting a %s change requires a licence; charter §7.3 Q3 forbids it", k)
		}
	}

	// And there is no licence check anywhere in the backport path, which is
	// what §8 greps for by hand.
	root := filepath.Join("..", "..")
	for _, path := range []string{
		filepath.Join(root, "scripts", "backport.sh"),
		filepath.Join(root, "pkg", "version", "support.go"),
		filepath.Join(root, "pkg", "version", "cmd", "eligible", "main.go"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		body := string(raw)
		for _, forbidden := range []string{"requirePlan", "HasFeature", "GRAVIX_LICENSE", "pkg/license"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s references %q; the backport path must have no licence check",
					filepath.Base(path), forbidden)
			}
		}
	}
}

// AC-5
func TestCorrectnessFixesBackported(t *testing.T) {
	for _, k := range []BackportKind{KindCorrectness, KindDataLoss} {
		ok, reason := Eligible(k)
		if !ok {
			t.Errorf("%s is not backport-eligible: %s", k, reason)
		}
	}
	// At the same priority as a security fix, and the reason says why: both
	// mean somebody is acting on something untrue.
	_, reason := Eligible(KindCorrectness)
	if !strings.Contains(reason, "untrue") {
		t.Errorf("the reason does not explain the equivalence: %q", reason)
	}
}

// AC-6
func TestFeatureBackportRefused(t *testing.T) {
	for _, k := range []BackportKind{KindFeature, KindPerformance, KindDependency} {
		ok, reason := Eligible(k)
		if ok {
			t.Errorf("%s is backport-eligible; an LTS receives fixes only", k)
		}
		if reason == "" {
			t.Errorf("%s was refused with no reason", k)
		}
	}
}

func TestUnknownKindIsNotSilentlyIneligible(t *testing.T) {
	ok, reason := Eligible("mystery")
	if ok {
		t.Error("an unclassified change was declared eligible")
	}
	if !strings.Contains(reason, "classify it") {
		t.Errorf("the reason does not ask for a classification: %q", reason)
	}
	if KnownKind("mystery") {
		t.Error("KnownKind accepted an invented kind")
	}
}

// TestEligibleCommandReportsAVerdictOnStdout: go run collapses every non-zero
// exit to 1, so the verdict cannot live in the exit code — backport.sh could
// not otherwise tell "not eligible" from "unclassified", and those need
// different answers.
func TestEligibleCommandReportsAVerdictOnStdout(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	cases := map[string]string{
		"security":         "eligible: yes",
		"data-correctness": "eligible: yes",
		"feature":          "eligible: no",
		"mystery":          "eligible: unknown",
		"":                 "eligible: unknown",
	}
	for kind, want := range cases {
		args := []string{"run", "./pkg/version/cmd/eligible"}
		if kind != "" {
			args = append(args, "-kind", kind)
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		if first != want {
			t.Errorf("kind %q: first line = %q, want %q", kind, first, want)
		}
	}
}

// TestDegenerateInputs covers the paths a malformed register reaches. They are
// not hypothetical: docs/oss/releases.json is edited by hand at release time,
// and the failure mode of a typo there is a support table that is quietly
// wrong rather than obviously broken.
func TestDegenerateInputs(t *testing.T) {
	t.Run("a malformed version does not reorder the real ones", func(t *testing.T) {
		releases := []Release{
			{Version: "v1.0.0", ReleasedAt: day(2026, time.January, 15), IsLTS: true},
			{Version: "not-a-version", ReleasedAt: day(2026, time.February, 1)},
			{Version: "v1.1.0", ReleasedAt: day(2026, time.March, 1)},
		}
		s, err := Classify("v1.1.0", day(2026, time.April, 1), releases)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if s.Line != LineCurrent {
			t.Errorf("v1.1.0 line = %q, want current — a malformed entry sorted above it", s.Line)
		}
	})

	t.Run("same version, different dates", func(t *testing.T) {
		// Sorted by date as the tie-break, so a duplicated version does not
		// make the ordering depend on map iteration or input order.
		releases := []Release{
			{Version: "v1.0.0", ReleasedAt: day(2026, time.March, 1)},
			{Version: "v1.0.0", ReleasedAt: day(2026, time.January, 1)},
		}
		sorted := sortedReleases(releases)
		if !sorted[0].ReleasedAt.Equal(day(2026, time.January, 1)) {
			t.Errorf("ties are not broken by date: %+v", sorted)
		}
	})

	t.Run("semver components that are not numbers", func(t *testing.T) {
		// A component that does not parse reads as 0 rather than panicking or
		// producing an arbitrary order. The components that DO parse are still
		// honoured, so a typo in the patch field does not lose the major.
		for _, v := range []string{"vX.Y.Z", "", "v", "v.."} {
			if got := CompareSemver(v, "v0.0.0"); got != 0 {
				t.Errorf("CompareSemver(%q, v0.0.0) = %d, want 0", v, got)
			}
		}
		if got := CompareSemver("v1.beta.0", "v1.0.0"); got != 0 {
			t.Errorf("CompareSemver(v1.beta.0, v1.0.0) = %d, want 0 — the unparseable "+
				"component reads as 0 and the major is kept", got)
		}
		if got := CompareSemver("v1.beta.0", "v0.0.0"); got != 1 {
			t.Errorf("CompareSemver(v1.beta.0, v0.0.0) = %d, want 1", got)
		}
	})

	t.Run("an unlabelled support line", func(t *testing.T) {
		if got := lineLabel(Line("invented")); got != "unsupported" {
			t.Errorf("lineLabel(invented) = %q, want %q", got, "unsupported")
		}
	})

	t.Run("KnownKind rejects every non-kind", func(t *testing.T) {
		for _, k := range []BackportKind{"", "Security", "fix", "SECURITY"} {
			if KnownKind(k) {
				t.Errorf("KnownKind(%q) = true", k)
			}
		}
	})
}
