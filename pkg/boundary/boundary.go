// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package boundary parses and validates the open-core placement map in
// docs/oss/boundary.yaml, and cross-checks it against plan gates in the codebase.
package boundary

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Version is the only boundary-map schema version this binary understands.
const Version = 1

// Placement is where a capability's code may live.
type Placement string

const (
	// PlacementCore is the Apache-2.0 core: free forever.
	PlacementCore Placement = "core"
	// PlacementEE is the BUSL-1.1 ee/ tree: source-available.
	PlacementEE Placement = "ee"
)

// CrippleWareTest records the five charter §7.3 answers. A single yes forces
// core placement, which is why every field is recorded rather than a verdict.
type CrippleWareTest struct {
	Q1TeamOfTenNotices  bool `yaml:"q1_team_of_ten_notices"`
	Q2AffectsAccuracy   bool `yaml:"q2_affects_accuracy"`
	Q3WorseSecurity     bool `yaml:"q3_worse_security"`
	Q4PreviouslyOpen    bool `yaml:"q4_previously_open"`
	Q5OnlyReasonIsMoney bool `yaml:"q5_only_reason_is_money"`
}

// AnyYes reports whether any answer is yes, which forces core placement.
func (c CrippleWareTest) AnyYes() bool {
	return c.Q1TeamOfTenNotices || c.Q2AffectsAccuracy || c.Q3WorseSecurity ||
		c.Q4PreviouslyOpen || c.Q5OnlyReasonIsMoney
}

// YesAnswers returns the names of the questions answered yes, so an error can
// say which question forced the ruling rather than only that one did.
func (c CrippleWareTest) YesAnswers() []string {
	var out []string
	if c.Q1TeamOfTenNotices {
		out = append(out, "q1_team_of_ten_notices")
	}
	if c.Q2AffectsAccuracy {
		out = append(out, "q2_affects_accuracy")
	}
	if c.Q3WorseSecurity {
		out = append(out, "q3_worse_security")
	}
	if c.Q4PreviouslyOpen {
		out = append(out, "q4_previously_open")
	}
	if c.Q5OnlyReasonIsMoney {
		out = append(out, "q5_only_reason_is_money")
	}
	return out
}

// Capability is one gateable unit of functionality.
type Capability struct {
	ID              string           `yaml:"id"`
	Name            string           `yaml:"name"`
	Placement       Placement        `yaml:"placement"`
	CharterRef      string           `yaml:"charter_ref"`
	Rationale       string           `yaml:"rationale"`
	CrippleWareTest *CrippleWareTest `yaml:"crippleware_test"`
	Paths           []string         `yaml:"paths"`
	Gate            string           `yaml:"gate"`
	Note            string           `yaml:"note"`
}

// Map is the whole boundary file.
type Map struct {
	Version      int          `yaml:"version"`
	Capabilities []Capability `yaml:"capabilities"`
}

// Sentinel errors. Each is wrapped with the offending capability id so a
// violation names what to fix.
var (
	ErrUnsupportedVersion = errors.New("boundary: unsupported version")
	ErrDuplicateID        = errors.New("boundary: duplicate capability id")
	ErrMissingField       = errors.New("boundary: missing required field")
	ErrCrippleWareYes     = errors.New("boundary: ee placement with a yes answer forces core placement")
	ErrTestOnCore         = errors.New("boundary: crippleware_test present on a core capability")
	ErrTestMissingOnEE    = errors.New("boundary: crippleware_test required for ee placement")
)

// Load reads and validates a boundary map from path.
func Load(path string) (*Map, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("boundary: open %s: %w", path, err)
	}
	var m Map
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("boundary: parse %s: %w", path, err)
	}
	if errs := m.Validate(); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &m, nil
}

// Validate returns every rule violation found, or nil when the map is valid.
// It returns all violations rather than the first, so one run fixes one map.
func (m *Map) Validate() []error {
	var errs []error

	if m.Version != Version {
		errs = append(errs, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, m.Version, Version))
	}

	seen := make(map[string]bool, len(m.Capabilities))
	for _, c := range m.Capabilities {
		if c.ID == "" {
			errs = append(errs, fmt.Errorf("%w: id", ErrMissingField))
			continue
		}
		if seen[c.ID] {
			errs = append(errs, fmt.Errorf("%w: %s", ErrDuplicateID, c.ID))
		}
		seen[c.ID] = true

		for field, val := range map[string]string{
			"name":        c.Name,
			"charter_ref": c.CharterRef,
			"rationale":   c.Rationale,
		} {
			if val == "" {
				errs = append(errs, fmt.Errorf("%w: %s.%s", ErrMissingField, c.ID, field))
			}
		}
		if len(c.Paths) == 0 {
			errs = append(errs, fmt.Errorf("%w: %s.paths", ErrMissingField, c.ID))
		}

		switch c.Placement {
		case PlacementCore:
			if c.CrippleWareTest != nil {
				errs = append(errs, fmt.Errorf("%w: %s", ErrTestOnCore, c.ID))
			}
		case PlacementEE:
			if c.CrippleWareTest == nil {
				errs = append(errs, fmt.Errorf("%w: %s", ErrTestMissingOnEE, c.ID))
			} else if c.CrippleWareTest.AnyYes() {
				errs = append(errs, fmt.Errorf("%w: %s answered yes to %v",
					ErrCrippleWareYes, c.ID, c.CrippleWareTest.YesAnswers()))
			}
		default:
			errs = append(errs, fmt.Errorf("%w: %s.placement must be core or ee, got %q",
				ErrMissingField, c.ID, c.Placement))
		}
	}
	return errs
}

// Get returns the capability with the given id.
func (m *Map) Get(id string) (*Capability, bool) {
	for i := range m.Capabilities {
		if m.Capabilities[i].ID == id {
			return &m.Capabilities[i], true
		}
	}
	return nil, false
}

// CoreCapabilities returns every capability placed in the Apache-2.0 core.
func (m *Map) CoreCapabilities() []Capability {
	return m.byPlacement(PlacementCore)
}

// EECapabilities returns every capability placed in ee/.
func (m *Map) EECapabilities() []Capability {
	return m.byPlacement(PlacementEE)
}

func (m *Map) byPlacement(p Placement) []Capability {
	var out []Capability
	for _, c := range m.Capabilities {
		if c.Placement == p {
			out = append(out, c)
		}
	}
	return out
}
