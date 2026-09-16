// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package registry parses and validates the Gravix plugin registry.
//
// The registry is a file in this repository, not a service. A service would
// need uptime, a bill and an operator, and would make plugin discovery fail
// whenever Gravix's infrastructure did. A JSON file updated by pull request
// has none of those properties, and it works offline.
//
// Listing is not endorsement. The checks in this package are deliberately
// mechanical: the repository exists, it has the licence it claims, it declares
// a supported ABI, its name is unique, its kind is real, and its README says
// what data the plugin sends where. Nothing here is a quality judgement,
// because a quality bar would make Gravix responsible for what it lists.
package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/lgreene/gravix-dashboards/pkg/plugin"
)

// SchemaVersion is the registry file format version.
const SchemaVersion = 1

// NamePrefix is required on every listed plugin name. It keeps a registry
// entry recognisable as a Gravix plugin wherever it is quoted.
const NamePrefix = "gravix-"

// File is the parsed registry.
type File struct {
	SchemaVersion int     `json:"schema_version"`
	Plugins       []Entry `json:"plugins"`
}

// Entry is one listed plugin.
type Entry struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
	License     string `json:"license"`
	ABIVersion  string `json:"abi_version"`
	Maintainer  string `json:"maintainer"`
	Added       string `json:"added"`
	FirstParty  bool   `json:"first_party"`
}

// Violation is one failed listing check.
//
// Check carries the §5.2 check number rather than a category, so the message
// points at the rule that rejected the entry and a submitter can see exactly
// what to change.
type Violation struct {
	Name   string
	Check  int
	Detail string
}

func (v Violation) Error() string {
	return fmt.Sprintf("registry: %q failed check %d: %s", v.Name, v.Check, v.Detail)
}

// RepoFacts is what checks 1, 2 and 6 need to know about a repository.
//
// They are facts about a repository rather than about the JSON, so they cannot
// be answered by reading the registry file. Gathering them is separated from
// judging them: a test supplies them directly, and a listing reviewer fetches
// them over the network.
type RepoFacts struct {
	// Exists is true when the repository resolves and is publicly readable.
	Exists bool
	// LicenseFile is the content of the repository's LICENSE file, empty when
	// there is none.
	LicenseFile string
	// Readme is the content of the repository's README.
	Readme string
}

var (
	// dataFlowHeading is what check 6 looks for. "States what data it sends
	// where" is a judgement until it is pinned to something a script can find,
	// and an unpinnable check is one that gets applied unevenly.
	dataFlowHeading = regexp.MustCompile(`(?im)^#{1,6}\s*data\s+flow\s*$`)

	// dateRe is the ISO date `added` must use. A registry sorted by a free-form
	// date field is a registry that cannot be sorted.
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// ValidateName reports whether a name is usable as a plugin name: prefixed,
// kebab-case, and therefore safe as a Go module path, a directory name and a
// registry key at once.
func ValidateName(name string) error {
	if !strings.HasPrefix(name, NamePrefix) {
		return fmt.Errorf("plugin name must start with %q, got %q", NamePrefix, name)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("plugin name must be lower-case words joined by single hyphens, got %q", name)
	}
	return nil
}

// Parse reads a registry file.
func Parse(r io.Reader) (*File, error) {
	var f File
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("registry: parse: %w", err)
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("registry: schema_version %d, this Gravix reads %d", f.SchemaVersion, SchemaVersion)
	}
	return &f, nil
}

// ParseBytes reads a registry file from memory.
func ParseBytes(b []byte) (*File, error) { return Parse(bytes.NewReader(b)) }

// Names returns the listed plugin names in sorted order.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Plugins))
	for _, e := range f.Plugins {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

// ByKind returns the entries of one kind, sorted by name.
func (f *File) ByKind(kind string) []Entry {
	var out []Entry
	for _, e := range f.Plugins {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Check runs the six listing checks against one entry.
//
// `others` are the names already listed, for the uniqueness check. Checks 1, 2
// and 6 use facts; passing the zero RepoFacts fails them, which is the correct
// default — an unverified repository is not a verified one.
func Check(entry Entry, others []string, facts RepoFacts) []Violation {
	var vs []Violation

	// 1. The repository resolves and is public.
	if !facts.Exists {
		vs = append(vs, Violation{entry.Name, 1, fmt.Sprintf("repository %q does not resolve or is not public", entry.Repository)})
	}

	// 2. The repository has a LICENSE file and `license` matches it.
	switch {
	case facts.LicenseFile == "":
		vs = append(vs, Violation{entry.Name, 2, "repository has no LICENSE file"})
	case entry.License == "":
		vs = append(vs, Violation{entry.Name, 2, "license is empty"})
	case !licenseMatches(entry.License, facts.LicenseFile):
		vs = append(vs, Violation{entry.Name, 2, fmt.Sprintf("license %q does not match the LICENSE file", entry.License)})
	}

	// 3. abi_version equals the ABI this Gravix speaks.
	if entry.ABIVersion != plugin.ABIVersion {
		vs = append(vs, Violation{entry.Name, 3, fmt.Sprintf("declares ABI %q, this Gravix supports %q", entry.ABIVersion, plugin.ABIVersion)})
	}

	// 4. The name is unique and prefixed.
	if !strings.HasPrefix(entry.Name, NamePrefix) {
		vs = append(vs, Violation{entry.Name, 4, fmt.Sprintf("name must start with %q", NamePrefix)})
	} else if !nameRe.MatchString(entry.Name) {
		vs = append(vs, Violation{entry.Name, 4, "name must be lower-case words joined by single hyphens"})
	}
	for _, other := range others {
		if other == entry.Name {
			vs = append(vs, Violation{entry.Name, 4, "name is already listed"})
			break
		}
	}

	// 5. The kind is one Gravix calls.
	if !plugin.Kind(entry.Kind).Valid() {
		vs = append(vs, Violation{entry.Name, 5, fmt.Sprintf("kind %q is not notifier, exporter, or adapter", entry.Kind)})
	}

	// 6. The README says what data the plugin sends where.
	//
	// This is the only check about the reader rather than the mechanics.
	// Installing a notifier that forwards alert contents to a third party is a
	// data-flow decision, and a decision cannot be made from a README that
	// does not describe it.
	if !dataFlowHeading.MatchString(facts.Readme) {
		vs = append(vs, Violation{entry.Name, 6, "README has no \"Data flow\" section saying what data the plugin sends where"})
	}

	return vs
}

// Validate runs the structural checks that need no network: the fields a
// listing cannot omit, and uniqueness across the file.
//
// CI runs this on every pull request. The repository checks are not run there
// because a registry that goes red when an unrelated third-party repository
// has an outage is a registry nobody can merge into.
func (f *File) Validate() []Violation {
	var vs []Violation
	seen := map[string]bool{}

	for _, e := range f.Plugins {
		if e.Name == "" {
			vs = append(vs, Violation{"", 4, "name is empty"})
			continue
		}
		if !strings.HasPrefix(e.Name, NamePrefix) {
			vs = append(vs, Violation{e.Name, 4, fmt.Sprintf("name must start with %q", NamePrefix)})
		} else if !nameRe.MatchString(e.Name) {
			vs = append(vs, Violation{e.Name, 4, "name must be lower-case words joined by single hyphens"})
		}
		if seen[e.Name] {
			vs = append(vs, Violation{e.Name, 4, "name is already listed"})
		}
		seen[e.Name] = true

		if !plugin.Kind(e.Kind).Valid() {
			vs = append(vs, Violation{e.Name, 5, fmt.Sprintf("kind %q is not notifier, exporter, or adapter", e.Kind)})
		}
		if e.ABIVersion != plugin.ABIVersion {
			vs = append(vs, Violation{e.Name, 3, fmt.Sprintf("declares ABI %q, this Gravix supports %q", e.ABIVersion, plugin.ABIVersion)})
		}
		if e.Description == "" {
			vs = append(vs, Violation{e.Name, 0, "description is empty"})
		}
		if !strings.HasPrefix(e.Repository, "https://") {
			vs = append(vs, Violation{e.Name, 1, fmt.Sprintf("repository %q is not an https URL", e.Repository)})
		}
		if e.License == "" {
			vs = append(vs, Violation{e.Name, 2, "license is empty"})
		}
		if e.Maintainer == "" {
			vs = append(vs, Violation{e.Name, 0, "maintainer is empty"})
		}
		if !dateRe.MatchString(e.Added) {
			vs = append(vs, Violation{e.Name, 0, fmt.Sprintf("added %q is not a YYYY-MM-DD date", e.Added)})
		}
	}
	return vs
}

// licenseMatches reports whether a LICENSE file is the licence an entry claims.
//
// The comparison is on the licence's own title text, not an SPDX header:
// almost no LICENSE file carries one, so requiring it would reject nearly
// every honest repository.
func licenseMatches(claimed, file string) bool {
	norm := strings.ToLower(strings.Join(strings.Fields(file), " "))
	for _, phrase := range licensePhrases[claimed] {
		if strings.Contains(norm, phrase) {
			return true
		}
	}
	// A licence Gravix does not have a phrase for is accepted on an exact
	// mention of its identifier. Refusing an unknown licence would make the
	// registry a licence allowlist, which it is not.
	if _, known := licensePhrases[claimed]; !known {
		return strings.Contains(norm, strings.ToLower(claimed))
	}
	return false
}

// licensePhrases maps an SPDX identifier to text that appears in that licence.
var licensePhrases = map[string][]string{
	"Apache-2.0":   {"apache license", "version 2.0"},
	"MIT":          {"permission is hereby granted, free of charge"},
	"BSD-3-Clause": {"redistribution and use in source and binary forms"},
	"BSD-2-Clause": {"redistribution and use in source and binary forms"},
	"MPL-2.0":      {"mozilla public license"},
	"GPL-3.0":      {"gnu general public license"},
	"AGPL-3.0":     {"gnu affero general public license"},
}
