// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package plugin defines Gravix's third-party extension ABI. Plugins run as
// subprocesses speaking JSON-RPC 2.0 over stdio, so a plugin built against one
// Gravix release keeps working against the next.
//
// Go's own plugin package is deliberately not used. It couples every plugin to
// the exact toolchain and dependency versions of the host, is ELF-only, and
// cannot isolate a crash — so every Gravix release would break every plugin,
// which is the opposite of a stable ABI.
package plugin

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ABIVersion is the wire contract version. It changes only when an existing
// method's request or response shape changes; adding a method does not change it.
const ABIVersion = "1"

// Kind is what a plugin extends.
type Kind string

const (
	KindNotifier Kind = "notifier"
	KindExporter Kind = "exporter"
	KindAdapter  Kind = "adapter"
)

// Valid reports whether k is a kind this ABI defines.
func (k Kind) Valid() bool {
	switch k {
	case KindNotifier, KindExporter, KindAdapter:
		return true
	default:
		return false
	}
}

// Config field types a plugin may declare.
const (
	ConfigTypeString   = "string"
	ConfigTypeInt      = "int"
	ConfigTypeBool     = "bool"
	ConfigTypeDuration = "duration"
	// ConfigTypeSecret is a string that is never logged, never included in an
	// error message, and never written to a `gravix doctor` bundle.
	ConfigTypeSecret = "secret"
)

// Manifest is what a plugin declares at startup, in response to "gravix.describe".
type Manifest struct {
	Name         string                 `json:"name"`        // kebab-case, unique
	Version      string                 `json:"version"`     // semver
	ABIVersion   string                 `json:"abi_version"` // must equal ABIVersion
	Kind         Kind                   `json:"kind"`
	Description  string                 `json:"description"`
	ConfigSchema map[string]ConfigField `json:"config_schema"`
	Homepage     string                 `json:"homepage"`
	License      string                 `json:"license"`
}

// ConfigField describes one configuration value the plugin accepts.
type ConfigField struct {
	Type        string `json:"type"` // "string"|"int"|"bool"|"duration"|"secret"
	Required    bool   `json:"required"`
	Default     any    `json:"default"`
	Description string `json:"description"`
}

var (
	ErrABIMismatch     = errors.New("plugin: ABI version mismatch")
	ErrDuplicateName   = errors.New("plugin: a plugin with that name is already registered")
	ErrManifestInvalid = errors.New("plugin: manifest is invalid")
	ErrTimeout         = errors.New("plugin: call timed out")
	ErrCrashed         = errors.New("plugin: subprocess exited")
	// ErrDisabled is returned once a plugin has spent its failure budget.
	ErrDisabled = errors.New("plugin: disabled after repeated failures")
)

// Defaults for the isolation rules. Each is overridable per plugin.
const (
	DefaultCallTimeout   = 30 * time.Second
	DefaultMemoryLimitMB = 256
	// FailureBudget is how many consecutive failures disable a plugin. A
	// plugin that keeps failing is a plugin that is wrong about something;
	// retrying it forever turns one broken integration into a permanent
	// source of load.
	FailureBudget = 5
	// MaxRestartBackoff caps the exponential backoff between restarts.
	MaxRestartBackoff = 5 * time.Minute
)

// pluginNamePattern is kebab-case: lowercase letters, digits and single
// hyphens. A name appears in configuration, logs and the registry, so it is
// constrained rather than free-form.
var pluginNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// semverPattern is a pragmatic semver check: three dot-separated numbers with
// an optional pre-release or build suffix.
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Validate checks a manifest received from a plugin.
//
// It is called on the handshake response, before the plugin is registered or
// invoked, because everything in a manifest arrives from third-party code.
func (m Manifest) Validate() error {
	if !pluginNamePattern.MatchString(m.Name) {
		return fmt.Errorf("%w: name %q is not kebab-case", ErrManifestInvalid, m.Name)
	}
	if !semverPattern.MatchString(m.Version) {
		return fmt.Errorf("%w: version %q is not semver", ErrManifestInvalid, m.Version)
	}
	if !m.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrManifestInvalid, m.Kind)
	}

	// The ABI check is its own error, not a manifest error: a plugin built
	// against a different ABI is well-formed, just incompatible, and the
	// operator needs to be told which version to get.
	if m.ABIVersion != ABIVersion {
		return fmt.Errorf("%w: plugin %q declares ABI %s, this Gravix supports %s",
			ErrABIMismatch, m.Name, m.ABIVersion, ABIVersion)
	}

	for field, spec := range m.ConfigSchema {
		switch spec.Type {
		case ConfigTypeString, ConfigTypeInt, ConfigTypeBool, ConfigTypeDuration, ConfigTypeSecret:
		default:
			return fmt.Errorf("%w: config field %q has unknown type %q", ErrManifestInvalid, field, spec.Type)
		}
		// A secret with a default in the manifest would put that default in
		// every copy of the plugin's documentation.
		if spec.Type == ConfigTypeSecret && spec.Default != nil {
			return fmt.Errorf("%w: config field %q is a secret and must not declare a default", ErrManifestInvalid, field)
		}
	}

	return nil
}

// SecretFields returns the names of config fields declared as secrets.
func (m Manifest) SecretFields() []string {
	var out []string
	for field, spec := range m.ConfigSchema {
		if spec.Type == ConfigTypeSecret {
			out = append(out, field)
		}
	}
	return out
}

// RedactConfig returns a copy of config with every secret-typed value replaced
// by "[redacted]".
//
// Everything that logs, reports or reports an error about a plugin passes its
// configuration through here first. A secret that reaches a log line has
// leaked, and log lines are copied into issue reports.
func (m Manifest) RedactConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for key, value := range config {
		if spec, ok := m.ConfigSchema[key]; ok && spec.Type == ConfigTypeSecret {
			out[key] = RedactedValue
			continue
		}
		out[key] = value
	}
	return out
}

// RedactedValue replaces a secret configuration value wherever one would
// otherwise be printed.
const RedactedValue = "[redacted]"
