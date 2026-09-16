// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package version computes Gravix support windows so tooling and documentation
// agree about what is supported, rather than each maintaining its own list.
//
// The list that matters is SECURITY.md's. A security policy claiming a support
// window the maintainers do not actually honour is worse than no policy, so
// that table is generated from this package rather than written by hand.
//
// One rule is not negotiable and is enforced here rather than remembered:
// security fixes are free, on every supported line, forever. Charter §7.3 Q3.
// A paid-only security patch would leave the majority of installs knowingly
// vulnerable, which is not a business model, it is a hazard.
package version

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Line is a support line.
type Line string

const (
	LineCurrent     Line = "current"
	LineLTS         Line = "lts"
	LinePreviousLTS Line = "previous_lts"
	LineUnsupported Line = "unsupported"
)

// LTSWindow is how long an LTS is supported from its designation.
const LTSWindow = 365 * 24 * time.Hour

// PreviousLTSOverlap is how long the outgoing LTS keeps receiving security
// fixes after a new one is designated.
//
// Three months is an upgrade window, not a grace period: it is there so a team
// that plans annually is never in a position where the only supported version
// is one they have not had time to test.
const PreviousLTSOverlap = 90 * 24 * time.Hour

// Release is one tagged Gravix release.
//
// GRVX-1501 §5.3 names this type in Classify's signature without defining it;
// this is the minimum that makes §5.1's table computable. IsLTS is recorded at
// release time rather than derived, because §5.1 requires an LTS to be
// "designated at its release rather than retroactively, so a team can plan an
// upgrade before they need one" — deriving it later would make the designation
// a thing that could change under somebody who had already planned around it.
type Release struct {
	Version    string    `json:"version"`
	ReleasedAt time.Time `json:"released_at"`
	IsLTS      bool      `json:"is_lts"`
}

// Support describes one version's status.
type Support struct {
	Version        string    `json:"version"`
	Line           Line      `json:"line"`
	DesignatedAt   time.Time `json:"designated_at"`
	SupportedUntil time.Time `json:"supported_until"`
	Receives       []string  `json:"receives"`
}

// What each line receives, per §5.1. These strings are published in
// SECURITY.md, so they are the promise rather than a summary of it.
var (
	receivesEverything = []string{"security fixes", "data-correctness fixes", "data-loss fixes", "bug fixes", "new capabilities"}
	receivesLTS        = []string{"security fixes", "data-correctness fixes", "data-loss fixes"}
	receivesPrevious   = []string{"security fixes"}
)

var ErrUnknownVersion = errors.New("version: unknown release")

// Classify returns the support status of version at time now.
func Classify(version string, now time.Time, releases []Release) (*Support, error) {
	sorted := sortedReleases(releases)

	idx := -1
	for i, r := range sorted {
		if r.Version == version {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w %q", ErrUnknownVersion, version)
	}
	rel := sorted[idx]

	// Current is simply the newest release. There is no window to compute:
	// it is supported until the next one exists, and then it is not.
	if idx == len(sorted)-1 {
		return &Support{
			Version:      rel.Version,
			Line:         LineCurrent,
			DesignatedAt: rel.ReleasedAt,
			// Zero rather than a guessed date. We do not know when the next
			// minor lands, and inventing one would publish a support promise
			// with a made-up expiry on it.
			Receives: receivesEverything,
		}, nil
	}

	if !rel.IsLTS {
		// A non-LTS release that is no longer current receives nothing. §5.1's
		// last row, and the reason the LTS designation is worth planning
		// around.
		return &Support{
			Version:      rel.Version,
			Line:         LineUnsupported,
			DesignatedAt: rel.ReleasedAt,
			Receives:     nil,
		}, nil
	}

	// An LTS. Which one it is depends on whether a newer LTS has superseded it.
	newerLTS := nextLTSAfter(sorted, idx)

	if newerLTS == nil {
		until := rel.ReleasedAt.Add(LTSWindow)
		line := LineLTS
		receives := receivesLTS
		if !now.Before(until) {
			line, receives = LineUnsupported, nil
		}
		return &Support{
			Version:        rel.Version,
			Line:           line,
			DesignatedAt:   rel.ReleasedAt,
			SupportedUntil: until,
			Receives:       receives,
		}, nil
	}

	// Superseded, so the previous-LTS row governs: three months from the NEW
	// LTS's designation, and its own twelve-month window no longer applies.
	//
	// Additive rather than a minimum, and that is the whole point. On the
	// annual cadence the outgoing LTS's window ends on the very day the new one
	// is designated, so taking the earlier of the two would make the overlap
	// exactly zero — and the overlap exists precisely so that a team is never
	// in the position where the only supported version is one they have not had
	// time to test.
	//
	// The edge it creates is deliberate and documented: an LTS superseded early
	// gets three months from that point rather than the rest of its year,
	// because once it is the previous LTS it receives security fixes only.
	until := newerLTS.ReleasedAt.Add(PreviousLTSOverlap)

	line := LinePreviousLTS
	receives := receivesPrevious
	if !now.Before(until) {
		line, receives = LineUnsupported, nil
	}
	return &Support{
		Version:        rel.Version,
		Line:           line,
		DesignatedAt:   rel.ReleasedAt,
		SupportedUntil: until,
		Receives:       receives,
	}, nil
}

// SupportedVersions returns every currently supported version, newest first.
//
// An unsupported version is absent rather than listed as unsupported: this is
// what SECURITY.md's table is generated from, and a table listing everything
// ever released would bury the two lines a reader needs.
func SupportedVersions(now time.Time, releases []Release) []Support {
	sorted := sortedReleases(releases)

	var out []Support
	for i := len(sorted) - 1; i >= 0; i-- {
		s, err := Classify(sorted[i].Version, now, sorted)
		if err != nil || s.Line == LineUnsupported {
			continue
		}
		out = append(out, *s)
	}
	return out
}

// nextLTSAfter returns the first LTS released after index i, or nil.
func nextLTSAfter(sorted []Release, i int) *Release {
	for j := i + 1; j < len(sorted); j++ {
		if sorted[j].IsLTS {
			r := sorted[j]
			return &r
		}
	}
	return nil
}

// sortedReleases returns a copy ordered oldest first.
//
// Sorted by version rather than by date, because a patch to an old line can be
// released after a newer minor and "newest release" must still mean the
// highest version. Ties fall back to the date.
func sortedReleases(releases []Release) []Release {
	out := make([]Release, len(releases))
	copy(out, releases)
	sort.SliceStable(out, func(a, b int) bool {
		if c := CompareSemver(out[a].Version, out[b].Version); c != 0 {
			return c < 0
		}
		return out[a].ReleasedAt.Before(out[b].ReleasedAt)
	})
	return out
}

// CompareSemver orders two "vMAJOR.MINOR.PATCH" strings, returning -1, 0 or 1.
//
// Deliberately small: Gravix's own semver policy (.claude/agents/
// sre-release-manager.md) describes MAJOR/MINOR/PATCH and nothing else, so
// pre-release and build metadata are not part of the version space this has to
// order. A component that does not parse sorts as 0, which keeps a malformed
// entry in the register from silently reordering the real ones.
func CompareSemver(a, b string) int {
	av, bv := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n := 0
		for _, r := range parts[i] {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out
}

// BackportKind is what a change is, for the purpose of §5.2's eligibility
// table.
type BackportKind string

const (
	KindSecurity    BackportKind = "security"
	KindCorrectness BackportKind = "data-correctness"
	KindDataLoss    BackportKind = "data-loss"
	KindCrash       BackportKind = "crash"
	KindPerformance BackportKind = "performance"
	KindFeature     BackportKind = "feature"
	KindDependency  BackportKind = "dependency"
)

// Eligible reports whether a change of this kind is backported to an LTS
// branch, and why.
//
// A data-correctness fix is backported at the same priority as a security fix,
// because charter-wise they are the same class of problem: both mean somebody
// is acting on something untrue.
//
// A crash is eligible at maintainer discretion — the function reports it
// eligible and says so, because §5.2's "recorded" means the discretion is
// exercised in the open rather than by this function silently.
func Eligible(kind BackportKind) (bool, string) {
	switch kind {
	case KindSecurity:
		return true, "security fixes are backported to every supported line, free, always (charter §7.3 Q3)"
	case KindCorrectness:
		return true, "a wrong number is the same class of problem as a vulnerability: somebody is acting on something untrue"
	case KindDataLoss:
		return true, "a defect that loses facts defeats the point of the product"
	case KindCrash:
		return true, "eligible at maintainer discretion; record the decision on the backport"
	case KindPerformance:
		return false, "performance improvements are not backported; an LTS is a stable line, not a faster one"
	case KindFeature:
		return false, "features are never backported; an LTS receives fixes only"
	case KindDependency:
		return false, "a dependency bump with no security or correctness impact is not backported"
	default:
		return false, fmt.Sprintf("unknown change kind %q; classify it before backporting", kind)
	}
}

// RequiresLicence reports whether backporting a change of this kind requires a
// commercial licence.
//
// It returns false for every kind, and it exists so that the claim is testable
// rather than merely absent. GRVX-1504 sells support response time and advice;
// it does not sell patches, and TestSecurityBackportsAreFree fails if this ever
// grows a branch.
func RequiresLicence(BackportKind) bool { return false }

// KnownKind reports whether kind is one this policy classifies. An unknown
// kind is a different failure from an ineligible one: the first means somebody
// has not decided what the change is, the second means they have.
func KnownKind(kind BackportKind) bool {
	switch kind {
	case KindSecurity, KindCorrectness, KindDataLoss, KindCrash,
		KindPerformance, KindFeature, KindDependency:
		return true
	default:
		return false
	}
}
