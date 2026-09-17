// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package registryfile embeds the plugin registry so that `gravix plugin list`
// works from an installed binary, with no checkout and no network.
//
// The embed lives beside the JSON rather than in cmd/cli because go:embed
// cannot reach a parent directory: a copy under cmd/ would be a second source
// of truth that goes stale silently. There is exactly one plugins.json, and
// this is it.
package registryfile

import _ "embed"

// PluginsJSON is registry/plugins.json.
//
//go:embed plugins.json
var PluginsJSON []byte

// SchemaJSON is registry/schema.json, the JSON Schema plugins.json is
// validated against in CI.
//
//go:embed schema.json
var SchemaJSON []byte
