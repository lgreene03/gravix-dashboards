// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package violating is a FIXTURE, not real code. It deliberately imports an ee/
// package so cmd/checkboundary can prove it detects a core-to-ee import. The
// real check excludes cmd/checkboundary/testdata/, so this file does not fail it.
package violating

import _ "github.com/lgreene/gravix-dashboards/ee/placeholder"
