// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		Name:        "my-notifier",
		Version:     "1.2.3",
		ABIVersion:  ABIVersion,
		Kind:        KindNotifier,
		Description: "sends alerts somewhere",
		License:     "Apache-2.0",
		ConfigSchema: map[string]ConfigField{
			"url":   {Type: ConfigTypeString, Required: true},
			"token": {Type: ConfigTypeSecret, Required: true},
		},
	}
}

func TestManifestValidateAcceptsAValidManifest(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("a valid manifest was rejected: %v", err)
	}
}

// Everything in a manifest arrives from third-party code, so each rule is
// checked rather than assumed.
func TestManifestValidateRejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Manifest)
		want   error
	}{
		"empty name":            {func(m *Manifest) { m.Name = "" }, ErrManifestInvalid},
		"name with spaces":      {func(m *Manifest) { m.Name = "my notifier" }, ErrManifestInvalid},
		"name in CamelCase":     {func(m *Manifest) { m.Name = "MyNotifier" }, ErrManifestInvalid},
		"name with underscore":  {func(m *Manifest) { m.Name = "my_notifier" }, ErrManifestInvalid},
		"name with double dash": {func(m *Manifest) { m.Name = "my--notifier" }, ErrManifestInvalid},
		"non-semver version":    {func(m *Manifest) { m.Version = "v1" }, ErrManifestInvalid},
		"empty version":         {func(m *Manifest) { m.Version = "" }, ErrManifestInvalid},
		"unknown kind":          {func(m *Manifest) { m.Kind = "transformer" }, ErrManifestInvalid},
		"empty kind":            {func(m *Manifest) { m.Kind = "" }, ErrManifestInvalid},
		"unknown config type":   {func(m *Manifest) { m.ConfigSchema["url"] = ConfigField{Type: "url"} }, ErrManifestInvalid},
		"abi mismatch":          {func(m *Manifest) { m.ABIVersion = "99" }, ErrABIMismatch},
		"empty abi":             {func(m *Manifest) { m.ABIVersion = "" }, ErrABIMismatch},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(&m)
			if err := m.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A secret with a default would put that default in every copy of the
// plugin's documentation, and in every registry listing.
func TestManifestRejectsASecretWithADefault(t *testing.T) {
	m := validManifest()
	m.ConfigSchema["token"] = ConfigField{Type: ConfigTypeSecret, Default: "hunter2"}

	err := m.Validate()
	if !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("err = %v, want ErrManifestInvalid", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q does not name the offending field", err)
	}
}

func TestManifestValidateAcceptsEveryDeclaredConfigType(t *testing.T) {
	for _, typ := range []string{ConfigTypeString, ConfigTypeInt, ConfigTypeBool, ConfigTypeDuration, ConfigTypeSecret} {
		m := validManifest()
		m.ConfigSchema = map[string]ConfigField{"field": {Type: typ}}
		if err := m.Validate(); err != nil {
			t.Errorf("config type %q rejected: %v", typ, err)
		}
	}
}

func TestSecretFields(t *testing.T) {
	m := validManifest()
	m.ConfigSchema["api_key"] = ConfigField{Type: ConfigTypeSecret}
	m.ConfigSchema["retries"] = ConfigField{Type: ConfigTypeInt}

	got := m.SecretFields()
	sort.Strings(got)
	if want := []string{"api_key", "token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SecretFields() = %v, want %v", got, want)
	}
}

// RedactConfig is what stands between a secret and a log line, so both halves
// matter: the secret goes, everything else stays.
func TestRedactConfig(t *testing.T) {
	m := validManifest()
	config := map[string]any{
		"url":     "https://example.com/hook",
		"token":   "super-secret",
		"unknown": "kept, because nothing says it is a secret",
	}

	got := m.RedactConfig(config)

	if got["token"] != RedactedValue {
		t.Errorf("token = %v, want %q", got["token"], RedactedValue)
	}
	if got["url"] != "https://example.com/hook" {
		t.Errorf("url was altered: %v", got["url"])
	}
	if got["unknown"] != config["unknown"] {
		t.Errorf("an unknown field was altered: %v", got["unknown"])
	}

	// The original must not be mutated: the caller still needs the real value
	// to actually call the plugin.
	if config["token"] != "super-secret" {
		t.Error("RedactConfig mutated the caller's config")
	}
}

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{KindNotifier, KindExporter, KindAdapter} {
		if !k.Valid() {
			t.Errorf("Kind(%q).Valid() = false", k)
		}
	}
	for _, k := range []Kind{"", "transform", "auth", "storage"} {
		if k.Valid() {
			t.Errorf("Kind(%q).Valid() = true; it is not a v2 kind", k)
		}
	}
}

// The ABI version is a published contract. Changing it is a deliberate act
// that breaks every existing plugin, so it is pinned here: a change that
// fails this test is a change that needs a migration plan, not a nudge.
func TestABIVersionIsPinned(t *testing.T) {
	if ABIVersion != "1" {
		t.Fatalf("ABIVersion = %q; changing it breaks every published plugin and needs a migration plan", ABIVersion)
	}
}

// The isolation defaults are the promise the plugin guide makes to operators.
func TestIsolationDefaultsMatchTheDocumentedContract(t *testing.T) {
	if DefaultCallTimeout.Seconds() != 30 {
		t.Errorf("DefaultCallTimeout = %s, want 30s", DefaultCallTimeout)
	}
	if DefaultMemoryLimitMB != 256 {
		t.Errorf("DefaultMemoryLimitMB = %d, want 256", DefaultMemoryLimitMB)
	}
	if FailureBudget != 5 {
		t.Errorf("FailureBudget = %d, want 5", FailureBudget)
	}
	if MaxRestartBackoff.Minutes() != 5 {
		t.Errorf("MaxRestartBackoff = %s, want 5m", MaxRestartBackoff)
	}
}
