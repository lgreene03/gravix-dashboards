// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validIncident() Incident {
	return Incident{
		ID:                  "INC-2026-0001",
		OccurredAt:          time.Date(2026, 9, 1, 3, 12, 0, 0, time.UTC),
		Severity:            SEV2,
		Summary:             "Rollup job stalled for two hours; under 1% of ingest delayed, no facts lost.",
		Disposition:         DispositionImproved,
		OSSChangePR:         412,
		DiagnosedWithGravix: true,
	}
}

// AC-1 — a third terminal value is where "cloud-only problem" and "wontfix"
// would go to live, and §3 names that as the failure to prevent.
func TestOnlyTwoTerminalDispositions(t *testing.T) {
	if len(TerminalDispositions) != 2 {
		t.Fatalf("there are %d terminal dispositions, want exactly 2: %v",
			len(TerminalDispositions), TerminalDispositions)
	}
	for _, d := range TerminalDispositions {
		if !d.IsTerminal() {
			t.Errorf("%q is in TerminalDispositions but IsTerminal reports false", d)
		}
	}
	if DispositionPending.IsTerminal() {
		t.Error("pending is terminal; it is a waiting room, not an outcome")
	}

	// Anything else is rejected outright.
	for _, invented := range []Disposition{"wontfix", "cloud_only", "deferred", "acknowledged"} {
		in := validIncident()
		in.Disposition = invented
		in.OSSChangePR = 0
		if err := in.Validate(); !errors.Is(err, ErrNoDisposition) {
			t.Errorf("disposition %q was accepted: %v", invented, err)
		}
	}
}

// AC-2
func TestImprovedRequiresPR(t *testing.T) {
	in := validIncident()
	in.Disposition = DispositionImproved
	in.OSSChangePR = 0

	if err := in.Validate(); !errors.Is(err, ErrImprovedNeedsPR) {
		t.Fatalf("got %v, want ErrImprovedNeedsPR", err)
	}

	in.OSSChangePR = 412
	if err := in.Validate(); err != nil {
		t.Errorf("a claimed improvement with a PR was rejected: %v", err)
	}
	// A negative PR number is not a PR.
	in.OSSChangePR = -1
	if err := in.Validate(); !errors.Is(err, ErrImprovedNeedsPR) {
		t.Errorf("PR -1 was accepted: %v", err)
	}
}

// AC-3
func TestNoChangeRequiresReason(t *testing.T) {
	in := validIncident()
	in.Disposition = DispositionNoChange
	in.OSSChangePR = 0
	in.NoChangeReason = ""

	if err := in.Validate(); !errors.Is(err, ErrNoChangeNeedsReason) {
		t.Fatalf("got %v, want ErrNoChangeNeedsReason", err)
	}
	// Whitespace is not a reason.
	in.NoChangeReason = "   \n\t "
	if err := in.Validate(); !errors.Is(err, ErrNoChangeNeedsReason) {
		t.Errorf("a whitespace reason was accepted: %v", err)
	}

	in.NoChangeReason = "Caused by a cloud provider zone outage; nothing in Gravix would have changed the outcome."
	if err := in.Validate(); err != nil {
		t.Errorf("a no_change with a reason was rejected: %v", err)
	}
}

// AC-4 — the dogfood question has no "did not say" state. An incident with
// DiagnosedWithGravix false and no explanation is rejected, so leaving the
// field at its zero value cannot be silently recorded as a clean diagnosis.
func TestDogfoodQuestionMandatory(t *testing.T) {
	in := validIncident()
	in.DiagnosedWithGravix = false
	in.WhatWasMissing = ""

	err := in.Validate()
	if !errors.Is(err, ErrMissingCapabilityUnstated) {
		t.Fatalf("got %v, want ErrMissingCapabilityUnstated", err)
	}
	// The message argues rather than instructs, because the person reading it
	// is tired and about to skip the field.
	if !strings.Contains(err.Error(), "the most useful thing an outage produces") {
		t.Errorf("the message does not say why it matters: %v", err)
	}

	// It is checked before the disposition, so a pending incident Gravix could
	// not diagnose is caught while the fix is still undecided.
	in.Disposition = DispositionPending
	if err := in.Validate(); !errors.Is(err, ErrMissingCapabilityUnstated) {
		t.Errorf("a pending undiagnosed incident was accepted: %v", err)
	}
}

// AC-5
func TestMissingCapabilityRecorded(t *testing.T) {
	in := validIncident()
	in.DiagnosedWithGravix = false
	in.WhatWasMissing = "no way to see per-partition rollup lag; we read the Parquet directory by hand"

	if err := in.Validate(); err != nil {
		t.Fatalf("an incident that says what was missing was rejected: %v", err)
	}

	// Whitespace is not an answer.
	in.WhatWasMissing = "  "
	if err := in.Validate(); !errors.Is(err, ErrMissingCapabilityUnstated) {
		t.Errorf("a whitespace answer was accepted: %v", err)
	}
}

// AC-6 — a false positive costs somebody a rewording; a false negative
// publishes a customer's domain in a postmortem that is then in the git
// history forever.
func TestAllIdentifierClassesRedacted(t *testing.T) {
	cases := []struct {
		kind, text string
	}{
		{"tenant id", "Ingestion failed for ten_abc123 between 03:00 and 04:00."},
		{"organisation domain", "The affected customer was acmecorp.com."},
		{"email", "Reported by ops@acmecorp.io during the window."},
		{"API key fragment", "The request carried grvx_9f3a2b1c and was rejected."},
		{"IPv4", "Traffic from 203.0.113.44 saturated the ingest path."},
		{"IPv6", "Traffic from 2001:0db8:85a3:0000:0000:8a2e:0370:7334 saturated the ingest path."},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if err := CheckRedaction(tc.text); !errors.Is(err, ErrCustomerIdentifier) {
				t.Fatalf("%s was not caught in %q: %v", tc.kind, tc.text, err)
			}

			// And it is caught through Validate, on every published field.
			for _, field := range []string{"summary", "reason", "missing"} {
				in := validIncident()
				switch field {
				case "summary":
					in.Summary = tc.text
				case "reason":
					in.Disposition = DispositionNoChange
					in.OSSChangePR = 0
					in.NoChangeReason = tc.text
				case "missing":
					in.DiagnosedWithGravix = false
					in.WhatWasMissing = tc.text
				}
				if err := in.Validate(); !errors.Is(err, ErrCustomerIdentifier) {
					t.Errorf("%s slipped through the %s field: %v", tc.kind, field, err)
				}
			}
		})
	}
}

// TestRedactionAcceptsBandedScale: scale belongs in bands, never as a figure
// that identifies anyone, and the banded form must actually be writable.
func TestRedactionAcceptsBandedScale(t *testing.T) {
	for _, ok := range []string{
		"Several tenants saw delayed ingest for about 40 minutes.",
		"Under 1% of ingest was affected; no facts were lost.",
		"A minority of tenants on the shared cell were affected.",
		"Rollup lag exceeded two hours for a single partition.",
		"The status page at status.gravix.io showed the degradation throughout.",
		"Fixed in https://github.com/lgreene03/gravix-dashboards/pull/412.",
		"",
	} {
		if err := CheckRedaction(ok); err != nil {
			t.Errorf("a properly redacted line was rejected: %q: %v", ok, err)
		}
	}
}

func TestRedactionNamesWhichClassItFound(t *testing.T) {
	err := CheckRedaction("Reported by ops@acmecorp.io.")
	if err == nil {
		t.Fatal("no error")
	}
	// "contains a customer identifier" alone leaves somebody rereading a
	// paragraph looking for it.
	if !strings.Contains(err.Error(), "email address") {
		t.Errorf("the error does not say what it found: %v", err)
	}
}

func TestValidateRequiresTheBasics(t *testing.T) {
	t.Run("id", func(t *testing.T) {
		in := validIncident()
		in.ID = " "
		if err := in.Validate(); !errors.Is(err, ErrNoID) {
			t.Errorf("got %v, want ErrNoID", err)
		}
	})
	t.Run("summary", func(t *testing.T) {
		in := validIncident()
		in.Summary = ""
		if err := in.Validate(); !errors.Is(err, ErrNoSummary) {
			t.Errorf("got %v, want ErrNoSummary", err)
		}
	})
	t.Run("severity", func(t *testing.T) {
		in := validIncident()
		in.Severity = "catastrophic"
		if err := in.Validate(); !errors.Is(err, ErrBadSeverity) {
			t.Errorf("got %v, want ErrBadSeverity", err)
		}
	})
	t.Run("empty disposition", func(t *testing.T) {
		in := validIncident()
		in.Disposition = ""
		in.OSSChangePR = 0
		if err := in.Validate(); !errors.Is(err, ErrNoDisposition) {
			t.Errorf("got %v, want ErrNoDisposition", err)
		}
	})
}

// TestSeverityScaleMatchesTheRunbook: docs/incident-response.md already
// defines SEV1-SEV4 and already requires a blameless review for SEV1 and SEV2.
// A second vocabulary for the same thing would mean two definitions of "bad
// enough to write up".
func TestSeverityScaleMatchesTheRunbook(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "incident-response.md"))
	if err != nil {
		t.Fatalf("reading the runbook: %v", err)
	}
	doc := string(raw)
	for _, sev := range []string{SEV1, SEV2, SEV3, SEV4} {
		if !strings.Contains(doc, sev) {
			t.Errorf("docs/incident-response.md no longer defines %s", sev)
		}
	}
	if !strings.Contains(doc, "any SEV1 or SEV2") {
		t.Error("the runbook no longer singles out SEV1 and SEV2; RequiresPostmortem was " +
			"aligned to that sentence")
	}

	if !RequiresPostmortem(SEV1) || !RequiresPostmortem(SEV2) {
		t.Error("SEV1 or SEV2 does not require a postmortem")
	}
	if RequiresPostmortem(SEV3) || RequiresPostmortem(SEV4) {
		t.Error("SEV3 or SEV4 requires a postmortem")
	}
	// The spec phrases it as "high or above"; the aliases resolve to the same
	// ranks so both vocabularies mean one thing.
	if !RequiresPostmortem("high") || !RequiresPostmortem("critical") {
		t.Error("the high/critical aliases do not require a postmortem")
	}
	if RequiresPostmortem("medium") || RequiresPostmortem("low") {
		t.Error("medium or low requires a postmortem")
	}
	if RequiresPostmortem("nonsense") {
		t.Error("an unknown severity requires a postmortem")
	}
}

func TestPendingDays(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	in := validIncident()
	in.Disposition = DispositionPending
	in.OSSChangePR = 0
	in.OccurredAt = now.AddDate(0, 0, -45)
	if got := in.PendingDays(now); got != 45 {
		t.Errorf("PendingDays = %d, want 45", got)
	}

	// A dispositioned incident is not pending, however old it is.
	in.Disposition = DispositionImproved
	in.OSSChangePR = 1
	if got := in.PendingDays(now); got != 0 {
		t.Errorf("a dispositioned incident reports %d pending days, want 0", got)
	}

	// A clock skew must not produce a negative age.
	in.Disposition = DispositionPending
	in.OccurredAt = now.AddDate(0, 0, 5)
	if got := in.PendingDays(now); got != 0 {
		t.Errorf("a future incident reports %d pending days, want 0", got)
	}
}

// TestWorkingDaysSince: the postmortem deadline is a commitment about human
// effort, so a Friday incident should not be overdue on Wednesday.
func TestWorkingDaysSince(t *testing.T) {
	friday := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if friday.Weekday() != time.Friday {
		t.Fatalf("the fixture date is a %s, not a Friday", friday.Weekday())
	}

	cases := []struct {
		days int // calendar days after the Friday
		want int
	}{
		{0, 0},
		{1, 0}, // Saturday
		{2, 0}, // Sunday
		{3, 1}, // Monday
		{4, 2}, // Tuesday
		{7, 5}, // Friday
		{8, 5}, // Saturday: still 5
	}
	for _, tc := range cases {
		got := WorkingDaysSince(friday, friday.AddDate(0, 0, tc.days))
		if got != tc.want {
			t.Errorf("%d calendar days after Friday = %d working days, want %d", tc.days, got, tc.want)
		}
	}

	if got := WorkingDaysSince(friday, friday.AddDate(0, 0, -1)); got != 0 {
		t.Errorf("a backwards span = %d, want 0", got)
	}
}
