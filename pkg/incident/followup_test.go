// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var auditNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func findingsOfKind(rep Report, kind string) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// AC-7
func TestPendingBeyondLimitFails(t *testing.T) {
	fresh := validIncident()
	fresh.ID = "INC-fresh"
	fresh.Disposition = DispositionPending
	fresh.OSSChangePR = 0
	fresh.Severity = SEV3 // no postmortem requirement, to isolate this check
	fresh.OccurredAt = auditNow.AddDate(0, 0, -MaxPendingDays)

	stale := fresh
	stale.ID = "INC-stale"
	stale.OccurredAt = auditNow.AddDate(0, 0, -MaxPendingDays-1)

	rep := Audit([]Incident{fresh, stale}, auditNow)

	got := findingsOfKind(rep, FindingPendingTooLong)
	if len(got) != 1 {
		t.Fatalf("got %d pending-too-long findings, want 1: %+v", len(got), rep.Findings)
	}
	if got[0].IncidentID != "INC-stale" {
		t.Errorf("flagged %s, want INC-stale", got[0].IncidentID)
	}
	// §6.1 fixes the wording, and it ends with the action rather than the
	// complaint.
	if !strings.Contains(got[0].Message, "disposition it") {
		t.Errorf("message = %q, want it to end with the action", got[0].Message)
	}
	if rep.OK() {
		t.Error("the report is OK with a stale pending incident")
	}
	if rep.Pending != 2 {
		t.Errorf("pending = %d, want 2", rep.Pending)
	}
}

// AC-8
func TestPostmortemDeadlineEnforced(t *testing.T) {
	// A SEV2 nine calendar days ago is seven working days ago — past the
	// five-day deadline.
	overdue := validIncident()
	overdue.ID = "INC-overdue"
	overdue.Severity = SEV2
	overdue.OccurredAt = auditNow.AddDate(0, 0, -9)
	overdue.PublishedAt = nil

	// The same age, published.
	published := overdue
	published.ID = "INC-published"
	at := auditNow.AddDate(0, 0, -6)
	published.PublishedAt = &at

	// The same age, low severity: no postmortem is owed.
	minor := overdue
	minor.ID = "INC-minor"
	minor.Severity = SEV3

	rep := Audit([]Incident{overdue, published, minor}, auditNow)

	got := findingsOfKind(rep, FindingPostmortemOverdue)
	if len(got) != 1 {
		t.Fatalf("got %d overdue-postmortem findings, want 1: %+v", len(got), rep.Findings)
	}
	if got[0].IncidentID != "INC-overdue" {
		t.Errorf("flagged %s, want INC-overdue", got[0].IncidentID)
	}
	if !strings.Contains(got[0].Message, "working days") {
		t.Errorf("message = %q; the deadline is in working days and should say so", got[0].Message)
	}
}

func TestPostmortemDeadlineCountsWorkingDays(t *testing.T) {
	// A SEV1 five calendar days before a Monday spans a weekend: three working
	// days, inside the deadline.
	monday := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	if monday.Weekday() != time.Monday {
		t.Fatalf("the fixture date is a %s", monday.Weekday())
	}

	in := validIncident()
	in.ID = "INC-weekend"
	in.Severity = SEV1
	in.OccurredAt = monday.AddDate(0, 0, -5)
	in.PublishedAt = nil

	rep := Audit([]Incident{in}, monday)
	if got := findingsOfKind(rep, FindingPostmortemOverdue); len(got) != 0 {
		t.Errorf("a Wednesday incident was overdue by Monday: %+v", got)
	}
}

func TestAuditFlagsBrokenDispositions(t *testing.T) {
	improved := validIncident()
	improved.ID = "INC-nopr"
	improved.Disposition = DispositionImproved
	improved.OSSChangePR = 0

	noChange := validIncident()
	noChange.ID = "INC-noreason"
	noChange.Disposition = DispositionNoChange
	noChange.OSSChangePR = 0
	noChange.NoChangeReason = ""

	rep := Audit([]Incident{improved, noChange}, auditNow)

	if got := findingsOfKind(rep, FindingImprovedWithoutPR); len(got) != 1 {
		t.Errorf("got %d improved-without-PR findings, want 1", len(got))
	}
	if got := findingsOfKind(rep, FindingNoChangeNoReason); len(got) != 1 {
		t.Errorf("got %d no_change-without-reason findings, want 1", len(got))
	}
	// Both are also invalid records, so the invalid finding fires too. That
	// redundancy is deliberate: the audit reports every reason a human should
	// look, not the first one it found.
	if len(findingsOfKind(rep, FindingInvalid)) != 2 {
		t.Errorf("got %d invalid-record findings, want 2", len(findingsOfKind(rep, FindingInvalid)))
	}
}

// AC-9 — a machine deciding that an incident produced no learning would defeat
// the purpose of the loop, which exists precisely because that decision is the
// one people are tempted to skip.
func TestAuditDoesNotAutoDecide(t *testing.T) {
	before := []Incident{
		func() Incident {
			in := validIncident()
			in.ID = "INC-pending"
			in.Disposition = DispositionPending
			in.OSSChangePR = 0
			in.OccurredAt = auditNow.AddDate(0, 0, -400) // very stale
			return in
		}(),
		func() Incident {
			in := validIncident()
			in.ID = "INC-nopr"
			in.Disposition = DispositionImproved
			in.OSSChangePR = 0
			return in
		}(),
	}

	// A copy to compare against, because Audit takes a slice and could mutate
	// it in place.
	snapshot := make([]Incident, len(before))
	copy(snapshot, before)

	rep := Audit(before, auditNow)
	if rep.OK() {
		t.Fatal("the audit found nothing wrong with two broken records")
	}

	for i := range before {
		if before[i] != snapshot[i] {
			t.Errorf("incident %s was modified by the audit:\n got %+v\nwant %+v",
				snapshot[i].ID, before[i], snapshot[i])
		}
	}
	// In particular, nothing was auto-dispositioned or auto-closed.
	if before[0].Disposition != DispositionPending {
		t.Errorf("a stale pending incident was auto-dispositioned to %q", before[0].Disposition)
	}
	if before[1].PublishedAt != nil {
		t.Error("the audit set PublishedAt")
	}
}

// AC-10 — one incident Gravix could not diagnose is bad luck; the same missing
// capability three times is a roadmap item nobody had to guess at.
func TestRecurringGapsSurfaced(t *testing.T) {
	const recurring = "no per-partition rollup lag metric"
	const once = "no way to correlate a deploy with a latency step"

	var incidents []Incident
	for i, what := range []string{recurring, recurring, recurring, once} {
		in := validIncident()
		in.ID = "INC-" + string(rune('a'+i))
		in.DiagnosedWithGravix = false
		in.WhatWasMissing = what
		incidents = append(incidents, in)
	}
	// And one that Gravix did diagnose, which must not appear.
	fine := validIncident()
	fine.ID = "INC-fine"
	incidents = append(incidents, fine)

	rep := Audit(incidents, auditNow)

	if rep.UndiagnosedCount != 4 {
		t.Errorf("undiagnosed_count = %d, want 4", rep.UndiagnosedCount)
	}
	// A gap seen once is not yet a pattern.
	if len(rep.Gaps) != 1 {
		t.Fatalf("got %d gap patterns, want 1: %+v", len(rep.Gaps), rep.Gaps)
	}
	if rep.Gaps[0].WhatWasMissing != recurring {
		t.Errorf("the pattern is %q, want %q", rep.Gaps[0].WhatWasMissing, recurring)
	}
	if rep.Gaps[0].Count != 3 {
		t.Errorf("count = %d, want 3", rep.Gaps[0].Count)
	}
	if len(rep.Gaps[0].IncidentIDs) != 3 {
		t.Errorf("incident ids = %v, want 3", rep.Gaps[0].IncidentIDs)
	}
}

func TestGapsAreOrderedMostFrequentFirst(t *testing.T) {
	var incidents []Incident
	add := func(n int, what, prefix string) {
		for i := 0; i < n; i++ {
			in := validIncident()
			in.ID = prefix + string(rune('0'+i))
			in.DiagnosedWithGravix = false
			in.WhatWasMissing = what
			incidents = append(incidents, in)
		}
	}
	add(2, "rare gap", "r")
	add(5, "common gap", "c")

	rep := Audit(incidents, auditNow)
	if len(rep.Gaps) != 2 {
		t.Fatalf("got %d gaps, want 2", len(rep.Gaps))
	}
	// The thing that keeps happening is the thing to fix.
	if rep.Gaps[0].WhatWasMissing != "common gap" {
		t.Errorf("first gap = %q, want the most frequent", rep.Gaps[0].WhatWasMissing)
	}
}

func TestAuditOnNoIncidentsIsClean(t *testing.T) {
	rep := Audit(nil, auditNow)
	if !rep.OK() {
		t.Errorf("an empty audit reported findings: %+v", rep.Findings)
	}
	if rep.Total != 0 || rep.Pending != 0 {
		t.Errorf("totals = %+v, want zeroes", rep)
	}
}

func TestLoadReadsRecords(t *testing.T) {
	dir := t.TempDir()

	in := validIncident()
	raw, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "INC-2026-0001.json"), raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A file that does not carry its own id takes it from the filename, so a
	// record cannot be anonymous.
	bare := `{"severity":"SEV3","summary":"Log noise","disposition":"no_change",` +
		`"no_change_reason":"cosmetic","diagnosed_with_gravix":true}`
	if err := os.WriteFile(filepath.Join(dir, "INC-2026-0002.json"), []byte(bare), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d records, want 2", len(got))
	}
	if got[0].ID != "INC-2026-0001" || got[1].ID != "INC-2026-0002" {
		t.Errorf("ids = %q, %q", got[0].ID, got[1].ID)
	}
	if got[0].OSSChangePR != 412 {
		t.Errorf("the record did not round-trip: %+v", got[0])
	}
}

// TestLoadOnAMissingDirectoryIsZeroIncidents: a project with no incidents yet
// is the normal starting state, and failing the audit for it would teach
// people to ignore the audit.
func TestLoadOnAMissingDirectoryIsZeroIncidents(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load on a missing directory: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("loaded %d records, want 0", len(got))
	}
}

func TestLoadRejectsAMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted a malformed record")
	}
}

func TestRenderShowsWhatMattersFirst(t *testing.T) {
	var buf bytes.Buffer

	// Published, because the default fixture is a SEV2 from a month ago and an
	// unpublished SEV2 is legitimately a finding.
	published := validIncident()
	at := auditNow.AddDate(0, 0, -28)
	published.PublishedAt = &at

	clean := Audit([]Incident{published}, auditNow)
	clean.Render(&buf)
	if !strings.Contains(buf.String(), "no enforcement failures") {
		t.Errorf("a clean report does not say so: %s", buf.String())
	}

	buf.Reset()
	stale := validIncident()
	stale.ID = "INC-stale"
	stale.Disposition = DispositionPending
	stale.OSSChangePR = 0
	stale.Severity = SEV3
	stale.OccurredAt = auditNow.AddDate(0, 0, -90)

	gap := validIncident()
	gap.ID = "INC-gap1"
	gap.DiagnosedWithGravix = false
	gap.WhatWasMissing = "no per-partition rollup lag metric"
	gap.PublishedAt = &at
	gap2 := gap
	gap2.ID = "INC-gap2"

	Audit([]Incident{stale, gap, gap2}, auditNow).Render(&buf)
	out := buf.String()
	for _, want := range []string{
		"incidents Gravix could not diagnose: 2",
		"recurring gaps",
		"no per-partition rollup lag metric",
		"disposition it",
		"enforcement failure",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered report does not contain %q:\n%s", want, out)
		}
	}
}
