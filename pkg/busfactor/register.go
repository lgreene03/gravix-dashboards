// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package busfactor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RegisterPath is the subsystem register, relative to the repository root.
const RegisterPath = "docs/oss/subsystems.md"

// Criticality reasons, per §5.1's four failures. Declaring everything critical
// means nothing is prioritised, so anything that is not one of these four is
// important rather than critical.
const (
	FailureDataLoss    = "data loss"
	FailureWrongNumber = "wrong number"
	FailureSecurity    = "security breach"
	FailureUpgrade     = "unrecoverable upgrade"
)

// CriticalFailures is the complete list. A subsystem marked critical for any
// other reason has been marked critical by somebody who did not apply the test.
var CriticalFailures = []string{
	FailureDataLoss, FailureWrongNumber, FailureSecurity, FailureUpgrade,
}

// LoadRegister reads the subsystem table out of docs/oss/subsystems.md.
//
// The register is a markdown table rather than JSON on purpose: it is a
// document people read, and its verdicts are arguments rather than values. The
// parser is strict enough that a malformed row is an error rather than a
// silently skipped subsystem.
func LoadRegister(path string) ([]Subsystem, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("busfactor: reading %s: %w", path, err)
	}

	var out []Subsystem
	inTable := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "| Subsystem ") {
			inTable = true
			continue
		}
		if inTable && !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		if !inTable || strings.HasPrefix(line, "|---") {
			continue
		}

		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 3 {
			return nil, fmt.Errorf("busfactor: %s: row %q has fewer than three columns", path, line)
		}
		p := strings.Trim(strings.TrimSpace(cells[0]), "`")
		critical := strings.Contains(cells[1], "✅")
		mode := strings.TrimSpace(cells[2])

		if p == "" {
			continue
		}
		if critical && !validFailure(mode) {
			return nil, fmt.Errorf(
				"busfactor: %s is marked critical for %q, which is not one of the four failures "+
					"in the criticality test (%s)", p, mode, strings.Join(CriticalFailures, ", "))
		}
		out = append(out, Subsystem{Path: p, Critical: critical, FailureMode: mode})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("busfactor: %s contains no subsystem table", path)
	}
	return out, nil
}

func validFailure(mode string) bool {
	for _, f := range CriticalFailures {
		if strings.Contains(strings.ToLower(mode), f) {
			return true
		}
	}
	return false
}

// RepoRoot walks up from dir to the directory holding go.mod.
func RepoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("busfactor: no go.mod above %s", dir)
		}
		abs = parent
	}
}
