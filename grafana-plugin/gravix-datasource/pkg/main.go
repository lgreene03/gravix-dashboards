// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

// This file exists so the package has a single obvious place to look for the
// plugin's identity. The executable entrypoint is ../cmd/main.go; Grafana runs
// the binary named by plugin.json's "executable" field.

// PluginID must match plugin.json's "id". Grafana matches the two, and a
// mismatch fails at load time with a message about neither of them.
const PluginID = "gravix-datasource"
