// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/plugin"
)

// repoURL is this repository. A first-party entry must point at it, because
// "first-party" means the code ships here under the same licence and the same
// CI as Gravix itself.
const repoURL = "https://github.com/lgreene03/gravix-dashboards"

func validEntry() Entry {
	return Entry{
		Name:        "gravix-notifier-mattermost",
		Kind:        "notifier",
		Description: "Sends Gravix alerts to a Mattermost channel.",
		Repository:  "https://github.com/example/gravix-notifier-mattermost",
		License:     "Apache-2.0",
		ABIVersion:  plugin.ABIVersion,
		Maintainer:  "@example",
		Added:       "2026-11-04",
	}
}

func validFacts() RepoFacts {
	return RepoFacts{
		Exists:      true,
		LicenseFile: "Apache License\nVersion 2.0, January 2004\n",
		Readme:      "# mattermost\n\n## Data flow\n\nSends the alert id and rule name to the configured server.\n",
	}
}

// AC-5: the registry in this repository validates.
func TestRegistryValidates(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "registry", "plugins.json"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	file, err := ParseBytes(raw)
	if err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	if len(file.Plugins) == 0 {
		t.Fatal("the registry is empty; it should list the first-party plugins")
	}
	for _, v := range file.Validate() {
		t.Errorf("%v", v)
	}
}

// The JSON Schema and the Go struct describe the same file. If one gains a
// field and the other does not, CI validates a shape the code cannot read, or
// the code reads a shape CI rejects — and nobody finds out until a listing
// pull request is wrongly refused.
func TestSchemaRequiresEveryEntryField(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "registry", "schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	text := string(raw)

	for _, field := range []string{
		"name", "kind", "description", "repository",
		"license", "abi_version", "maintainer", "added", "first_party",
	} {
		if !strings.Contains(text, `"`+field+`"`) {
			t.Errorf("schema.json does not mention %q, which Entry carries", field)
		}
	}
	if !strings.Contains(text, `"additionalProperties": false`) {
		t.Error("schema.json allows unknown fields; Parse rejects them, so CI would pass a file the CLI cannot read")
	}
}

// Parse rejects a file it cannot read rather than silently dropping fields.
func TestParseRejectsUnknownFieldsAndWrongVersion(t *testing.T) {
	if _, err := ParseBytes([]byte(`{"schema_version":1,"plugins":[],"extra":true}`)); err == nil {
		t.Error("an unknown top-level field was accepted")
	}
	if _, err := ParseBytes([]byte(`{"schema_version":99,"plugins":[]}`)); err == nil {
		t.Error("a future schema_version was accepted")
	}
	if _, err := ParseBytes([]byte(`not json`)); err == nil {
		t.Error("malformed JSON was accepted")
	}
	f, err := ParseBytes([]byte(`{"schema_version":1,"plugins":[]}`))
	if err != nil {
		t.Fatalf("an empty registry was rejected: %v", err)
	}
	if len(f.Plugins) != 0 {
		t.Errorf("got %d plugins, want 0", len(f.Plugins))
	}
}

func TestNamesAndByKindAreSorted(t *testing.T) {
	f := &File{SchemaVersion: 1, Plugins: []Entry{
		{Name: "gravix-notifier-z", Kind: "notifier"},
		{Name: "gravix-exporter-a", Kind: "exporter"},
		{Name: "gravix-notifier-a", Kind: "notifier"},
	}}

	got := f.Names()
	want := []string{"gravix-exporter-a", "gravix-notifier-a", "gravix-notifier-z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	notifiers := f.ByKind("notifier")
	if len(notifiers) != 2 || notifiers[0].Name != "gravix-notifier-a" {
		t.Errorf("ByKind(notifier) = %v", notifiers)
	}
	if got := f.ByKind("adapter"); len(got) != 0 {
		t.Errorf("ByKind(adapter) = %v, want none", got)
	}
}

// AC-6: each of the six §5.2 checks rejects a violating entry.
//
// The table is keyed by check number so a check that stops firing shows up as
// the number that went missing, not as a vague count mismatch.
func TestRegistryChecksEnforced(t *testing.T) {
	tests := []struct {
		name  string
		check int
		mut   func(*Entry, *RepoFacts)
	}{
		{"repository does not resolve", 1, func(_ *Entry, f *RepoFacts) { f.Exists = false }},
		{"no LICENSE file", 2, func(_ *Entry, f *RepoFacts) { f.LicenseFile = "" }},
		{"licence does not match the file", 2, func(e *Entry, _ *RepoFacts) { e.License = "MIT" }},
		{"licence field empty", 2, func(e *Entry, _ *RepoFacts) { e.License = "" }},
		{"wrong ABI", 3, func(e *Entry, _ *RepoFacts) { e.ABIVersion = "2" }},
		{"name missing the prefix", 4, func(e *Entry, _ *RepoFacts) { e.Name = "mattermost-notifier" }},
		{"name not kebab-case", 4, func(e *Entry, _ *RepoFacts) { e.Name = "gravix-Notifier_X" }},
		{"unknown kind", 5, func(e *Entry, _ *RepoFacts) { e.Kind = "dashboard" }},
		{"README has no data-flow section", 6, func(_ *Entry, f *RepoFacts) { f.Readme = "# mattermost\n\nA plugin.\n" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, facts := validEntry(), validFacts()
			tc.mut(&entry, &facts)

			vs := Check(entry, nil, facts)
			if len(vs) == 0 {
				t.Fatalf("check %d did not fire", tc.check)
			}
			var found bool
			for _, v := range vs {
				if v.Check == tc.check {
					found = true
				}
			}
			if !found {
				t.Fatalf("got violations %v, want one from check %d", vs, tc.check)
			}
			// The message names the plugin and the check, so a submitter can
			// see what to change without reading our source.
			if !strings.Contains(vs[0].Error(), "registry: ") {
				t.Errorf("message %q is not in the documented form", vs[0].Error())
			}
		})
	}

	// Check 4 also rejects a name already listed.
	entry, facts := validEntry(), validFacts()
	vs := Check(entry, []string{"gravix-something-else", entry.Name}, facts)
	if len(vs) != 1 || vs[0].Check != 4 {
		t.Fatalf("a duplicate name produced %v, want one check-4 violation", vs)
	}

	// A wholly valid entry passes all six.
	if vs := Check(validEntry(), []string{"gravix-other"}, validFacts()); len(vs) != 0 {
		t.Fatalf("a valid entry was rejected: %v", vs)
	}
}

// An unknown licence is accepted on an exact mention rather than refused: the
// registry lists plugins, it is not a licence allowlist.
func TestUnknownLicenceIsNotAnAllowlist(t *testing.T) {
	entry, facts := validEntry(), validFacts()
	entry.License = "Zlib"
	facts.LicenseFile = "The zlib/libpng License\n\nPermission is granted ..."
	for _, v := range Check(entry, nil, facts) {
		if v.Check == 2 {
			t.Errorf("an honest unknown licence was refused: %v", v)
		}
	}

	entry.License = "Zlib"
	facts.LicenseFile = "Some other text entirely"
	var refused bool
	for _, v := range Check(entry, nil, facts) {
		if v.Check == 2 {
			refused = true
		}
	}
	if !refused {
		t.Error("a LICENSE file that does not mention the claimed licence was accepted")
	}
}

func TestLicenceMatchingCoversTheCommonLicences(t *testing.T) {
	cases := map[string]string{
		"MIT":          "MIT License\n\nPermission is hereby granted, free of charge, to any person",
		"BSD-3-Clause": "Redistribution and use in source and binary forms, with or without",
		"BSD-2-Clause": "Redistribution and use in source and binary forms",
		"MPL-2.0":      "Mozilla Public License Version 2.0",
		"GPL-3.0":      "GNU General Public License Version 3",
		"AGPL-3.0":     "GNU Affero General Public License",
	}
	for spdx, text := range cases {
		if !licenseMatches(spdx, text) {
			t.Errorf("%s did not match its own licence text", spdx)
		}
	}
	if licenseMatches("MIT", "Apache License Version 2.0") {
		t.Error("an Apache LICENSE was accepted as MIT")
	}
}

// AC-7: the disclaimer is present verbatim.
//
// It is quoted here in full rather than referenced, so that softening it in
// the README turns into a failing test rather than a quiet edit.
func TestDisclaimerVerbatim(t *testing.T) {
	const disclaimer = `Listing here is not endorsement.

Gravix does not review, audit, test, or vouch for third-party plugins. A plugin
is a subprocess with network access running on your infrastructure, with the
configuration you give it. Treat installing one exactly as you would treat
adding any other dependency: read the source, check the licence, and decide
whether you trust its maintainer.

We check that the repository exists, that it has a licence, that it declares a
supported ABI version, and that its README says what data it sends where. That
is all we check.`

	raw, err := os.ReadFile(filepath.Join("..", "..", "registry", "README.md"))
	if err != nil {
		t.Fatalf("read registry README: %v", err)
	}
	if !strings.Contains(string(raw), disclaimer) {
		t.Error("registry/README.md does not contain the disclaimer verbatim")
	}
}

// AC-9: first_party is true only for plugins that actually ship here.
//
// The check is not that the URL is ours — that would pass for a plugin we
// never wrote. Each first-party entry must correspond to code in this
// repository, so the claim is verified against the tree rather than asserted.
func TestFirstPartyFlagAccurate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "registry", "plugins.json"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	file, err := ParseBytes(raw)
	if err != nil {
		t.Fatalf("parse registry: %v", err)
	}

	sources := repoSources(t)

	var firstParty int
	for _, e := range file.Plugins {
		if !e.FirstParty {
			if strings.HasPrefix(e.Repository, repoURL) {
				t.Errorf("%s points at this repository but is not marked first_party", e.Name)
			}
			continue
		}
		firstParty++

		if e.Repository != repoURL {
			t.Errorf("%s is marked first_party but its repository is %q", e.Name, e.Repository)
		}

		// gravix-<kind>-<feature> — the feature has to exist in the tree.
		parts := strings.SplitN(e.Name, "-", 3)
		if len(parts) != 3 {
			t.Errorf("%s is not named gravix-<kind>-<feature>", e.Name)
			continue
		}
		if !strings.Contains(sources, parts[2]) {
			t.Errorf("%s is marked first_party but %q appears nowhere in pkg/notify or pkg/export", e.Name, parts[2])
		}
	}

	if firstParty == 0 {
		t.Error("no first-party plugins listed; the registry should start with ours")
	}
}

// repoSources concatenates the non-test source of the packages that implement
// the first-party plugins.
func repoSources(t *testing.T) string {
	t.Helper()

	var b strings.Builder
	for _, dir := range []string{
		filepath.Join("..", "notify"),
		filepath.Join("..", "export"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

func TestValidateReportsEveryMissingField(t *testing.T) {
	f := &File{SchemaVersion: 1, Plugins: []Entry{
		{},
		{Name: "gravix-ok-one", Kind: "notifier", ABIVersion: plugin.ABIVersion,
			Description: "d", Repository: "http://insecure", License: "MIT", Maintainer: "@x", Added: "yesterday"},
		{Name: "gravix-ok-one", Kind: "notifier", ABIVersion: plugin.ABIVersion,
			Description: "d", Repository: "https://x", License: "MIT", Maintainer: "@x", Added: "2026-01-01"},
		{Name: "not-prefixed", Kind: "notifier", ABIVersion: plugin.ABIVersion,
			Description: "d", Repository: "https://x", License: "MIT", Maintainer: "@x", Added: "2026-01-01"},
		{Name: "gravix-Bad_Name", Kind: "notifier", ABIVersion: plugin.ABIVersion,
			Description: "d", Repository: "https://x", License: "MIT", Maintainer: "@x", Added: "2026-01-01"},
	}}

	vs := f.Validate()
	want := map[string]bool{
		"name is empty":                  false,
		"is not an https URL":            false,
		`added "yesterday"`:              false,
		"name is already listed":         false,
		`name must start with "gravix-"`: false,
		"lower-case words":               false,
	}
	for _, v := range vs {
		for phrase := range want {
			if strings.Contains(v.Error(), phrase) {
				want[phrase] = true
			}
		}
	}
	for phrase, found := range want {
		if !found {
			t.Errorf("Validate() did not report %q; got %v", phrase, vs)
		}
	}
}

func TestValidateReportsEmptyOptionalTextFields(t *testing.T) {
	f := &File{SchemaVersion: 1, Plugins: []Entry{{
		Name: "gravix-notifier-x", Kind: "notifier", ABIVersion: plugin.ABIVersion,
		Repository: "https://x", License: "", Maintainer: "", Added: "2026-01-01",
	}}}

	var sawDescription, sawLicense, sawMaintainer bool
	for _, v := range f.Validate() {
		switch {
		case strings.Contains(v.Error(), "description is empty"):
			sawDescription = true
		case strings.Contains(v.Error(), "license is empty"):
			sawLicense = true
		case strings.Contains(v.Error(), "maintainer is empty"):
			sawMaintainer = true
		}
	}
	if !sawDescription || !sawLicense || !sawMaintainer {
		t.Errorf("Validate() missed an empty field: description=%v license=%v maintainer=%v",
			sawDescription, sawLicense, sawMaintainer)
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName("gravix-notifier-demo"); err != nil {
		t.Errorf("a valid name was rejected: %v", err)
	}
	if err := ValidateName("notifier-demo"); err == nil {
		t.Error("a name without the prefix was accepted")
	} else if !strings.Contains(err.Error(), `must start with "gravix-"`) {
		t.Errorf("message %q does not say what to change", err)
	}
	if err := ValidateName("gravix-Notifier_Demo"); err == nil {
		t.Error("a name that is not kebab-case was accepted")
	}
}

// The data-flow heading is matched at any Markdown level and with any casing,
// because insisting on "## Data flow" exactly would reject honest READMEs on a
// formatting detail.
func TestDataFlowHeadingIsFlexibleButRequired(t *testing.T) {
	ok := []string{"## Data flow\n", "# DATA FLOW\n", "###   data   flow   \n", "prose\n\n##### Data Flow\nmore\n"}
	for _, r := range ok {
		entry, facts := validEntry(), validFacts()
		facts.Readme = r
		for _, v := range Check(entry, nil, facts) {
			if v.Check == 6 {
				t.Errorf("heading %q was refused", strings.TrimSpace(r))
			}
		}
	}

	bad := []string{"", "Data flow: we send everything\n", "## Dataflow\n", "some data flows here\n"}
	for _, r := range bad {
		entry, facts := validEntry(), validFacts()
		facts.Readme = r
		var refused bool
		for _, v := range Check(entry, nil, facts) {
			if v.Check == 6 {
				refused = true
			}
		}
		if !refused {
			t.Errorf("README %q was accepted without a data-flow section", strings.TrimSpace(r))
		}
	}
}
