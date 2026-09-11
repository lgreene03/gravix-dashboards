// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package metriccontract defines the published contract for every Gravix metric:
// its formula, inputs, grain, exactness, mergeability and reproduction command.
//
// The registry exists to make an undisclosed approximation impossible to keep. A
// metric that is approximate must say so and must name the defect; a metric built
// on a sketch must state its error bound. Those are validation rules, not
// conventions, so a contract that hides an approximation fails a test.
package metriccontract

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Exactness says how faithful a metric is to the underlying facts.
type Exactness string

const (
	// ExactnessExact means computed from raw facts with no approximation.
	ExactnessExact Exactness = "exact"
	// ExactnessSketch means computed from a bounded-error sketch.
	ExactnessSketch Exactness = "sketch"
	// ExactnessApproximate means approximate with an error that is not bounded.
	// A metric in this state is a defect to be fixed, not a design choice.
	ExactnessApproximate Exactness = "approximate"
)

// NotRecomputableByCLI prefixes a recompute_cmd for a metric the gravix CLI
// cannot rebuild yet. The contract must still say what does rebuild it.
//
// It exists because `gravix recompute` handles request_metrics_minute and
// nothing else, so the service-event metrics have no CLI path. Quoting a command
// that exits non-zero would be a worse answer to "how do I reproduce this
// number?" than admitting there is not one yet. See CD-002.
const NotRecomputableByCLI = "not rebuildable by the gravix CLI yet; run:"

// Mergeability says whether two grains of this metric may be combined.
type Mergeability string

const (
	// MergeabilitySum means the grains add.
	MergeabilitySum Mergeability = "sum"
	// MergeabilityWeightedMean means the grains combine as a weighted mean, which
	// is not the same as averaging the per-grain values.
	MergeabilityWeightedMean Mergeability = "weighted_mean"
	// MergeabilitySketchMerge means the grains combine by merging their sketches.
	MergeabilitySketchMerge Mergeability = "sketch_merge"
	// MergeabilityNone means the grains cannot be combined at all. Any query that
	// aggregates such a metric across grains is reporting a number with no defined
	// meaning.
	MergeabilityNone Mergeability = "none"
)

// versionPattern is the only accepted version form. Anything else would make
// ordering ambiguous, and the registry's job is to be unambiguous.
var versionPattern = regexp.MustCompile(`^v[0-9]+$`)

var (
	// ErrNoContract is returned when a name@version is not in the registry.
	ErrNoContract = errors.New("metriccontract: no such contract")
	// ErrDuplicateVersion is returned when one name@version appears twice.
	ErrDuplicateVersion = errors.New("metriccontract: duplicate name@version")
	// ErrMissingErrorBound is returned when a sketch metric states no bound.
	ErrMissingErrorBound = errors.New("metriccontract: sketch exactness requires an error_bound")
	// ErrMissingDefectNote is returned when an approximate metric names no defect.
	// An approximation with no recorded defect is exactly what charter-level
	// honesty forbids.
	ErrMissingDefectNote = errors.New("metriccontract: approximate exactness requires a known_defect")
	// ErrMissingRecomputeCmd is returned when a contract has no reproduction command.
	ErrMissingRecomputeCmd = errors.New("metriccontract: recompute_cmd is required")
	// ErrSketchMergeNeedsSketch is returned when sketch_merge is claimed without
	// sketch exactness.
	ErrSketchMergeNeedsSketch = errors.New("metriccontract: sketch_merge mergeability requires sketch exactness")
	// ErrMissingField is returned when a required field is empty.
	ErrMissingField = errors.New("metriccontract: required field is empty")
	// ErrBadVersion is returned when a version is not of the form v<n>.
	ErrBadVersion = errors.New("metriccontract: version must match ^v[0-9]+$")
	// ErrDanglingSupersedes is returned when supersedes names a contract that is
	// not in the registry.
	ErrDanglingSupersedes = errors.New("metriccontract: supersedes names an unknown contract")
	// ErrUnknownExactness is returned for an exactness outside the three values.
	ErrUnknownExactness = errors.New("metriccontract: unknown exactness")
	// ErrUnknownMergeability is returned for a mergeability outside the four values.
	ErrUnknownMergeability = errors.New("metriccontract: unknown mergeability")
)

// contractError attaches a contract's identity to a rule violation while still
// matching its sentinel under errors.Is. The sentinels already carry the package
// prefix, so it is stripped here rather than printed twice.
type contractError struct {
	id       string
	sentinel error
	extra    string
}

func (e contractError) Error() string {
	msg := fmt.Sprintf("metriccontract: %s: %s", e.id, strings.TrimPrefix(e.sentinel.Error(), "metriccontract: "))
	if e.extra != "" {
		msg += ": " + e.extra
	}
	return msg
}

func (e contractError) Unwrap() error { return e.sentinel }

// supersedesError names both contracts, since knowing which one is dangling is
// the whole content of the message.
type supersedesError struct {
	id     string
	target string
}

func (e supersedesError) Error() string {
	return fmt.Sprintf("metriccontract: %s supersedes unknown %s", e.id, e.target)
}

func (e supersedesError) Unwrap() error { return ErrDanglingSupersedes }

// violation reports a rule failure against one contract.
func violation(id string, sentinel error, extra string) error {
	return contractError{id: id, sentinel: sentinel, extra: extra}
}

// Contract is one metric version's published definition.
type Contract struct {
	Name         string       `yaml:"name"`
	Version      string       `yaml:"version"`
	Title        string       `yaml:"title"`
	Formula      string       `yaml:"formula"`
	InputFacts   []string     `yaml:"input_facts"`
	InputFields  []string     `yaml:"input_fields"`
	Grain        string       `yaml:"grain"`
	Dimensions   []string     `yaml:"dimensions"`
	Exactness    Exactness    `yaml:"exactness"`
	ErrorBound   string       `yaml:"error_bound"`
	Mergeability Mergeability `yaml:"mergeability"`
	MergeNote    string       `yaml:"merge_note"`
	LateData     string       `yaml:"late_data"`
	RecomputeCmd string       `yaml:"recompute_cmd"`
	Supersedes   string       `yaml:"supersedes"`
	Deprecated   bool         `yaml:"deprecated"`
	KnownDefect  string       `yaml:"known_defect"`
}

// ID is the contract's "name@version" identity.
func (c Contract) ID() string { return c.Name + "@" + c.Version }

// VersionNumber is the numeric part of the version, for ordering.
func (c Contract) VersionNumber() int {
	n, err := strconv.Atoi(strings.TrimPrefix(c.Version, "v"))
	if err != nil {
		return -1
	}
	return n
}

// Registry is every contract, keyed by "name@version".
type Registry struct {
	Contracts []Contract `yaml:"contracts"`
}

// Load reads every *.yaml in dir and validates the result. Files are read in
// sorted filename order so the registry's order — and therefore the generated
// documentation — does not depend on the filesystem.
func Load(dir string) (*Registry, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("metriccontract: globbing %s: %w", dir, err)
	}
	sort.Strings(paths)

	reg := &Registry{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("metriccontract: reading %s: %w", path, err)
		}
		var file Registry
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("metriccontract: parsing %s: %w", path, err)
		}
		reg.Contracts = append(reg.Contracts, file.Contracts...)
	}

	if errs := reg.Validate(); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return reg, nil
}

// Get returns the contract for "name@version", or ErrNoContract.
func (r *Registry) Get(nameVersion string) (*Contract, error) {
	for i := range r.Contracts {
		if r.Contracts[i].ID() == nameVersion {
			return &r.Contracts[i], nil
		}
	}
	return nil, fmt.Errorf("%w %s", ErrNoContract, nameVersion)
}

// Latest returns the highest non-deprecated version of name.
func (r *Registry) Latest(name string) (*Contract, error) {
	var best *Contract
	for i := range r.Contracts {
		c := &r.Contracts[i]
		if c.Name != name || c.Deprecated {
			continue
		}
		if best == nil || c.VersionNumber() > best.VersionNumber() {
			best = c
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w %s", ErrNoContract, name)
	}
	return best, nil
}

// Names returns every distinct metric name, sorted.
func (r *Registry) Names() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, c := range r.Contracts {
		if _, ok := seen[c.Name]; ok {
			continue
		}
		seen[c.Name] = struct{}{}
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// KnownDefects returns every contract whose exactness is approximate, in registry
// order. These are the metrics the project owes a fix on.
func (r *Registry) KnownDefects() []Contract {
	var out []Contract
	for _, c := range r.Contracts {
		if c.Exactness == ExactnessApproximate {
			out = append(out, c)
		}
	}
	return out
}

// Validate returns every rule violation, or nil.
func (r *Registry) Validate() []error {
	var errs []error
	seen := map[string]struct{}{}
	ids := map[string]struct{}{}
	for _, c := range r.Contracts {
		ids[c.ID()] = struct{}{}
	}

	for _, c := range r.Contracts {
		errs = append(errs, validateRequired(c)...)

		if !versionPattern.MatchString(c.Version) {
			errs = append(errs, violation(c.ID(), ErrBadVersion, fmt.Sprintf("got %q", c.Version)))
		}

		if _, dup := seen[c.ID()]; dup {
			errs = append(errs, fmt.Errorf("%w %s", ErrDuplicateVersion, c.ID()))
		}
		seen[c.ID()] = struct{}{}

		switch c.Exactness {
		case ExactnessExact:
		case ExactnessSketch:
			if strings.TrimSpace(c.ErrorBound) == "" {
				errs = append(errs, violation(c.ID(), ErrMissingErrorBound, ""))
			}
		case ExactnessApproximate:
			if strings.TrimSpace(c.KnownDefect) == "" {
				errs = append(errs, violation(c.ID(), ErrMissingDefectNote, ""))
			}
		default:
			errs = append(errs, violation(c.ID(), ErrUnknownExactness, fmt.Sprintf("%q", c.Exactness)))
		}

		switch c.Mergeability {
		case MergeabilitySum, MergeabilityWeightedMean, MergeabilityNone:
		case MergeabilitySketchMerge:
			if c.Exactness != ExactnessSketch {
				errs = append(errs, violation(c.ID(), ErrSketchMergeNeedsSketch, ""))
			}
		default:
			errs = append(errs, violation(c.ID(), ErrUnknownMergeability, fmt.Sprintf("%q", c.Mergeability)))
		}

		if c.Supersedes != "" {
			if _, ok := ids[c.Supersedes]; !ok {
				errs = append(errs, supersedesError{id: c.ID(), target: c.Supersedes})
			}
		}
	}
	return errs
}

// validateRequired checks the fields every contract must carry.
func validateRequired(c Contract) []error {
	var errs []error
	for field, value := range map[string]string{
		"name":    c.Name,
		"version": c.Version,
		"title":   c.Title,
		"formula": c.Formula,
		"grain":   c.Grain,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, violation(c.ID(), ErrMissingField, field))
		}
	}
	if strings.TrimSpace(c.RecomputeCmd) == "" {
		errs = append(errs, violation(c.ID(), ErrMissingRecomputeCmd, ""))
	}
	if len(c.InputFacts) == 0 {
		errs = append(errs, violation(c.ID(), ErrMissingField, "input_facts"))
	}
	if len(c.InputFields) == 0 {
		errs = append(errs, violation(c.ID(), ErrMissingField, "input_fields"))
	}
	// Sort so the error order does not depend on map iteration.
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}
