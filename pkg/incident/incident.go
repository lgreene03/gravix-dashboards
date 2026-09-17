// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package incident records operational incidents and enforces that each one
// produces a change to the open-source project, or a public reason why not.
//
// It is in the Apache-2.0 core on purpose, and that placement is the whole
// point of the spec that created it (charter §7.3 Q1 = YES). The mechanism
// that returns operational learning to the free product cannot itself be paid,
// or the free product stops benefiting from the hosted product's existence.
// A self-hoster runs the same loop over their own incidents with the same code.
//
// Two rules do the work:
//
//  1. Every incident ends as `improved` (a merged change in the core) or
//     `no_change` (a public reason). There is no third terminal value —
//     "we are still thinking about it" is bounded at thirty days, not
//     permitted as an outcome.
//  2. Every incident records whether Gravix was sufficient to diagnose it, and
//     when it was not, what was missing. An observability company discovering
//     that its own tool could not diagnose its own outage has learned
//     something no user survey will tell it.
package incident

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Disposition is what an incident produced. There are exactly two terminal
// values, deliberately.
type Disposition string

const (
	// DispositionPending means not yet dispositioned. Bounded by MaxPendingDays.
	DispositionPending Disposition = "pending"
	// DispositionImproved means a merged change in the OSS core. Requires a PR.
	DispositionImproved Disposition = "improved"
	// DispositionNoChange means no code change is warranted, with a public reason.
	DispositionNoChange Disposition = "no_change"
)

// TerminalDispositions is the complete set of ways an incident may end.
//
// It has exactly two members and a test asserts that. A third would be where
// "cloud-only problem" or "wontfix" went to live, and §3 of the spec that
// created this package names that as the failure to prevent: most incidents
// are software problems observed at scale.
var TerminalDispositions = []Disposition{DispositionImproved, DispositionNoChange}

// IsTerminal reports whether d ends an incident.
func (d Disposition) IsTerminal() bool {
	for _, t := range TerminalDispositions {
		if d == t {
			return true
		}
	}
	return false
}

// MaxPendingDays bounds how long an incident may sit undispositioned. Thirty
// days is long enough for a real investigation and short enough that the
// backlog cannot become a place things go to be forgotten.
const MaxPendingDays = 30

// PostmortemDeadlineWorkingDays is how long a high-severity incident may go
// without a published postmortem.
const PostmortemDeadlineWorkingDays = 5

// Severity levels, using the SEV1-SEV4 scale docs/incident-response.md already
// defines rather than inventing a second vocabulary for the same thing.
//
// "high or above", which the spec phrases it as, means SEV1 or SEV2 — the two
// levels that runbook already requires a blameless review for. The aliases are
// accepted because the spec and the roadmap use the words; they resolve to the
// same ranks.
const (
	SEV1 = "SEV1"
	SEV2 = "SEV2"
	SEV3 = "SEV3"
	SEV4 = "SEV4"
)

var severityRank = map[string]int{
	SEV1: 4, "critical": 4,
	SEV2: 3, "high": 3,
	SEV3: 2, "medium": 2,
	SEV4: 1, "low": 1,
}

// RequiresPostmortem reports whether severity is high enough to require a
// public postmortem — SEV1 or SEV2, matching docs/incident-response.md's own
// "hold a blameless review within 3 business days of any SEV1 or SEV2".
func RequiresPostmortem(severity string) bool {
	return severityRank[normaliseSeverity(severity)] >= severityRank[SEV2]
}

// IsValidSeverity reports whether severity is one of the known levels.
func IsValidSeverity(severity string) bool {
	_, ok := severityRank[normaliseSeverity(severity)]
	return ok
}

func normaliseSeverity(s string) string {
	s = strings.TrimSpace(s)
	if _, ok := severityRank[strings.ToUpper(s)]; ok {
		return strings.ToUpper(s)
	}
	return strings.ToLower(s)
}

// Incident is one operational event.
type Incident struct {
	ID          string      `json:"id"`
	OccurredAt  time.Time   `json:"occurred_at"`
	Severity    string      `json:"severity"`
	Summary     string      `json:"summary"` // no customer identifiers
	Disposition Disposition `json:"disposition"`

	// OSSChangePR is the merged pull request. Required when improved.
	OSSChangePR int `json:"oss_change_pr"`
	// NoChangeReason is the public explanation. Required when no_change.
	NoChangeReason string `json:"no_change_reason"`

	// DiagnosedWithGravix answers the dogfood question, and it is required on
	// every incident. There is no zero value that means "did not say": Validate
	// requires WhatWasMissing whenever this is false, so leaving both empty is
	// rejected rather than silently recorded as a clean diagnosis.
	DiagnosedWithGravix bool `json:"diagnosed_with_gravix"`
	// WhatWasMissing is required when DiagnosedWithGravix is false.
	WhatWasMissing string `json:"what_was_missing"`

	PublishedAt *time.Time `json:"published_at"`
}

var (
	ErrNoDisposition       = errors.New("incident: disposition required")
	ErrImprovedNeedsPR     = errors.New("incident: improved disposition requires a merged pull request")
	ErrNoChangeNeedsReason = errors.New("incident: no_change disposition requires a public reason")
	ErrCustomerIdentifier  = errors.New("incident: summary contains a customer identifier")

	// ErrMissingCapabilityUnstated is the message §6.1 fixes for an incident
	// that Gravix could not diagnose and that does not say what was missing.
	// It argues rather than instructs, because the person reading it is tired
	// and is about to skip the field.
	ErrMissingCapabilityUnstated = errors.New(
		"incident: say what was missing; this is the most useful thing an outage produces")

	ErrNoID        = errors.New("incident: id required")
	ErrNoSummary   = errors.New("incident: summary required")
	ErrBadSeverity = errors.New("incident: severity must be one of SEV1, SEV2, SEV3, SEV4")
)

// Validate checks one incident against every rule in this package.
func (i Incident) Validate() error {
	if strings.TrimSpace(i.ID) == "" {
		return ErrNoID
	}
	if !IsValidSeverity(i.Severity) {
		return fmt.Errorf("%w (got %q)", ErrBadSeverity, i.Severity)
	}
	if strings.TrimSpace(i.Summary) == "" {
		return ErrNoSummary
	}
	if err := CheckRedaction(i.Summary); err != nil {
		return err
	}
	// The reason is published too, so it is held to the same standard as the
	// summary. A tenant name leaked in an explanation is leaked just the same.
	if err := CheckRedaction(i.NoChangeReason); err != nil {
		return err
	}
	if err := CheckRedaction(i.WhatWasMissing); err != nil {
		return err
	}

	// The dogfood question, before the disposition: an incident Gravix could
	// not diagnose is worth recording even while the fix is undecided.
	if !i.DiagnosedWithGravix && strings.TrimSpace(i.WhatWasMissing) == "" {
		return ErrMissingCapabilityUnstated
	}

	switch i.Disposition {
	case DispositionPending:
		// Allowed, and bounded by MaxPendingDays rather than by this function.
		return nil
	case DispositionImproved:
		if i.OSSChangePR <= 0 {
			return ErrImprovedNeedsPR
		}
		return nil
	case DispositionNoChange:
		if strings.TrimSpace(i.NoChangeReason) == "" {
			return ErrNoChangeNeedsReason
		}
		return nil
	case "":
		return ErrNoDisposition
	default:
		// Any other value is an invented third outcome.
		return fmt.Errorf("%w: %q is not one of pending, improved, no_change",
			ErrNoDisposition, i.Disposition)
	}
}

// identifierPatterns are the classes §5.3 forbids in anything published.
//
// The list is deliberately over-eager. A false positive costs somebody a
// rewording; a false negative publishes a customer's domain in a postmortem
// that is then in the git history forever.
var identifierPatterns = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"email address", regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)},
	{"tenant id", regexp.MustCompile(`(?i)\bten_[a-z0-9_\-]+`)},
	{"API key", regexp.MustCompile(`(?i)\bgrvx_[a-z0-9_\-]{4,}`)},
	{"IP address", regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)},
	{"IP address", regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){2,7}[0-9a-f]{1,4}\b`)},
	{"domain name", regexp.MustCompile(`(?i)\b[a-z0-9\-]+\.(?:com|net|org|io|dev|co|ai|cloud|app|de|fr|uk)\b`)},
}

// allowedDomains are Gravix's own, which are not customer identifiers.
var allowedDomains = []string{
	"gravix.io", "status.gravix.io", "cloud.gravix.io", "docs.gravix.io",
	"example.com", "example.org", "example.net", // the RFC 2606 reserved names
	"claude.ai", "github.com", "duckdb.org",
}

// CheckRedaction reports whether text contains anything that identifies a
// customer.
//
// Scale belongs in bands — "several tenants", "under 1% of ingest" — never as
// a figure that identifies anyone. That rule is for humans; this function
// enforces the mechanical half.
func CheckRedaction(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	scrubbed := text
	for _, d := range allowedDomains {
		scrubbed = strings.ReplaceAll(scrubbed, d, "")
	}

	for _, p := range identifierPatterns {
		if m := p.pattern.FindString(scrubbed); m != "" {
			return fmt.Errorf("%w: %s", ErrCustomerIdentifier, p.kind)
		}
	}
	return nil
}

// PendingDays returns how long i has been awaiting a disposition as of now.
// A dispositioned incident returns 0.
func (i Incident) PendingDays(now time.Time) int {
	if i.Disposition.IsTerminal() {
		return 0
	}
	d := now.Sub(i.OccurredAt)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}

// WorkingDaysSince counts Monday-to-Friday days between from and to,
// exclusive of from and inclusive of to.
//
// Working days rather than calendar days because the postmortem deadline is a
// commitment about human effort, and a Friday incident should not be overdue
// on Wednesday.
func WorkingDaysSince(from, to time.Time) int {
	if !to.After(from) {
		return 0
	}
	n := 0
	for d := from.AddDate(0, 0, 1); !d.After(to); d = d.AddDate(0, 0, 1) {
		switch d.Weekday() {
		case time.Saturday, time.Sunday:
		default:
			n++
		}
	}
	return n
}
