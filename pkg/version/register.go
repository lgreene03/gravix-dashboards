// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// RegisterPath is where the release record lives, relative to the repository
// root.
//
// A committed file rather than git tags, for a reason SD-036 already
// established: scripts/build_oss.sh copies the tree WITHOUT .git to prove the
// core builds with ee/ deleted, so anything that needed git history would fail
// there. It is maintained by whoever cuts a release, the same way
// spec-status.json is maintained by whoever merges the work.
const RegisterPath = "docs/oss/releases.json"

// LoadRegister reads the release record.
//
// An empty register is valid and is the current state: Gravix has cut no
// versioned release. Everything downstream is written to say so plainly rather
// than to render an empty table implying a guarantee.
func LoadRegister(path string) ([]Release, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("version: reading %s: %w", path, err)
	}
	var out []Release
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("version: %s is not a valid release record: %w", path, err)
	}
	for i, r := range out {
		if strings.TrimSpace(r.Version) == "" {
			return nil, fmt.Errorf("version: release %d in %s has no version", i, path)
		}
		if r.ReleasedAt.IsZero() {
			return nil, fmt.Errorf("version: release %q in %s has no release date", r.Version, path)
		}
	}
	return out, nil
}

// SecurityTable renders the supported-versions table SECURITY.md publishes.
//
// It is generated so the security policy cannot claim a support window the
// tooling does not agree with. When nothing is supported it says exactly that,
// because an empty table under a "Supported versions" heading reads as a
// guarantee nobody made.
func SecurityTable(now time.Time, releases []Release) string {
	supported := SupportedVersions(now, releases)

	var b strings.Builder
	b.WriteString("| Version | Line | Supported until | Receives |\n")
	b.WriteString("|---|---|---|---|\n")

	if len(supported) == 0 {
		b.WriteString("| _none yet_ | — | — | Gravix has cut no versioned release. " +
			"The default branch receives everything. |\n")
		return b.String()
	}

	for _, s := range supported {
		until := "next minor"
		if !s.SupportedUntil.IsZero() {
			until = s.SupportedUntil.UTC().Format("2006-01-02")
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			s.Version, lineLabel(s.Line), until, strings.Join(s.Receives, ", ")))
	}
	return b.String()
}

func lineLabel(l Line) string {
	switch l {
	case LineCurrent:
		return "current"
	case LineLTS:
		return "LTS"
	case LinePreviousLTS:
		return "previous LTS"
	default:
		return "unsupported"
	}
}

// Markers delimit the generated region of SECURITY.md, so regenerating it
// cannot touch the prose around it.
const (
	TableBegin = "<!-- BEGIN GENERATED: supported-versions -->"
	TableEnd   = "<!-- END GENERATED: supported-versions -->"
)

// ReplaceTable substitutes the generated table between the markers in doc.
func ReplaceTable(doc, table string) (string, error) {
	start := strings.Index(doc, TableBegin)
	end := strings.Index(doc, TableEnd)
	if start < 0 || end < 0 || end < start {
		return "", fmt.Errorf("version: SECURITY.md has no %s / %s markers", TableBegin, TableEnd)
	}
	return doc[:start+len(TableBegin)] + "\n" + table + doc[end:], nil
}
