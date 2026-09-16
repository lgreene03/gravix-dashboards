// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Finding is one thing the audit wants a human to look at.
//
// The audit REPORTS. It never auto-closes and never auto-dispositions: a
// machine deciding that an incident produced no learning would defeat the
// purpose of the whole loop, which exists precisely because that decision is
// the one people are tempted to skip.
type Finding struct {
	IncidentID string `json:"incident_id"`
	Kind       string `json:"kind"`
	Message    string `json:"message"`
}

// Finding kinds.
const (
	FindingPendingTooLong    = "pending_too_long"
	FindingImprovedWithoutPR = "improved_without_pr"
	FindingNoChangeNoReason  = "no_change_without_reason"
	FindingPostmortemOverdue = "postmortem_overdue"
	FindingInvalid           = "invalid_record"
)

// GapPattern is a WhatWasMissing value that has come up more than once.
//
// A recurring gap is the single most valuable input the roadmap has, and it is
// only visible as a pattern. One incident Gravix could not diagnose is bad
// luck; the same missing capability three times is a roadmap item nobody had
// to guess at.
type GapPattern struct {
	WhatWasMissing string   `json:"what_was_missing"`
	Count          int      `json:"count"`
	IncidentIDs    []string `json:"incident_ids"`
}

// Report is the audit's whole output.
type Report struct {
	Total            int          `json:"total"`
	Pending          int          `json:"pending"`
	Improved         int          `json:"improved"`
	NoChange         int          `json:"no_change"`
	UndiagnosedCount int          `json:"undiagnosed_count"`
	Gaps             []GapPattern `json:"gaps"`
	Findings         []Finding    `json:"findings"`
}

// OK reports whether the audit found nothing to escalate.
func (r Report) OK() bool { return len(r.Findings) == 0 }

// Audit checks every incident and returns what a human needs to act on.
//
// It takes now explicitly so the deadlines are testable, and so an audit run
// against a fixed date produces the same answer twice.
func Audit(incidents []Incident, now time.Time) Report {
	rep := Report{Total: len(incidents)}
	gaps := map[string][]string{}

	for _, in := range incidents {
		if err := in.Validate(); err != nil {
			rep.Findings = append(rep.Findings, Finding{
				IncidentID: in.ID,
				Kind:       FindingInvalid,
				Message:    fmt.Sprintf("incident %s is not a valid record: %v", in.ID, err),
			})
		}

		switch in.Disposition {
		case DispositionImproved:
			rep.Improved++
			if in.OSSChangePR <= 0 {
				rep.Findings = append(rep.Findings, Finding{
					IncidentID: in.ID,
					Kind:       FindingImprovedWithoutPR,
					Message:    fmt.Sprintf("incident %s claims an improvement with no merged pull request", in.ID),
				})
			}
		case DispositionNoChange:
			rep.NoChange++
			if strings.TrimSpace(in.NoChangeReason) == "" {
				rep.Findings = append(rep.Findings, Finding{
					IncidentID: in.ID,
					Kind:       FindingNoChangeNoReason,
					Message:    fmt.Sprintf("incident %s is closed as no_change with no public reason", in.ID),
				})
			}
		default:
			rep.Pending++
			if days := in.PendingDays(now); days > MaxPendingDays {
				rep.Findings = append(rep.Findings, Finding{
					IncidentID: in.ID,
					Kind:       FindingPendingTooLong,
					Message:    fmt.Sprintf("incident %s has been pending for %d days; disposition it", in.ID, days),
				})
			}
		}

		if RequiresPostmortem(in.Severity) && in.PublishedAt == nil {
			if days := WorkingDaysSince(in.OccurredAt, now); days > PostmortemDeadlineWorkingDays {
				rep.Findings = append(rep.Findings, Finding{
					IncidentID: in.ID,
					Kind:       FindingPostmortemOverdue,
					Message: fmt.Sprintf("incident %s is severity %s with no published postmortem after %d working days",
						in.ID, in.Severity, days),
				})
			}
		}

		if !in.DiagnosedWithGravix {
			rep.UndiagnosedCount++
			if key := strings.TrimSpace(in.WhatWasMissing); key != "" {
				gaps[key] = append(gaps[key], in.ID)
			}
		}
	}

	for what, ids := range gaps {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		rep.Gaps = append(rep.Gaps, GapPattern{WhatWasMissing: what, Count: len(ids), IncidentIDs: ids})
	}
	// Most frequent first: the thing that keeps happening is the thing to fix.
	sort.Slice(rep.Gaps, func(a, b int) bool {
		if rep.Gaps[a].Count != rep.Gaps[b].Count {
			return rep.Gaps[a].Count > rep.Gaps[b].Count
		}
		return rep.Gaps[a].WhatWasMissing < rep.Gaps[b].WhatWasMissing
	})

	sort.SliceStable(rep.Findings, func(a, b int) bool {
		return rep.Findings[a].IncidentID < rep.Findings[b].IncidentID
	})
	return rep
}

// Load reads every *.json incident record in dir.
//
// A missing directory is zero incidents rather than an error: a project with
// no incidents yet is the normal starting state, and failing the audit for it
// would teach people to ignore the audit.
func Load(dir string) ([]Incident, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("incident: reading %s: %w", dir, err)
	}
	sort.Strings(matches)

	var out []Incident
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("incident: reading %s: %w", path, err)
		}
		var in Incident
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("incident: %s is not a valid record: %w", path, err)
		}
		if in.ID == "" {
			in.ID = strings.TrimSuffix(filepath.Base(path), ".json")
		}
		out = append(out, in)
	}
	return out, nil
}

// Render writes the audit report as text, for the script and for CI logs.
func (r Report) Render(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, "incidents: %d total — %d pending, %d improved, %d no_change\n",
		r.Total, r.Pending, r.Improved, r.NoChange)
	fmt.Fprintf(w, "incidents Gravix could not diagnose: %d\n", r.UndiagnosedCount)

	if len(r.Gaps) > 0 {
		fmt.Fprintln(w, "\nrecurring gaps (what Gravix could not tell us, more than once):")
		for _, g := range r.Gaps {
			fmt.Fprintf(w, "  %dx  %s  (%s)\n", g.Count, g.WhatWasMissing, strings.Join(g.IncidentIDs, ", "))
		}
	}

	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "\nno enforcement failures")
		return
	}
	fmt.Fprintf(w, "\n%d enforcement failure(s):\n", len(r.Findings))
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  [%s] %s\n", f.Kind, f.Message)
	}
}
