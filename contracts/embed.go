// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package contracts embeds the metric contract files into the binary.
//
// Contracts are source, not data: they ship with the code that computes the
// metrics they define. Reading them from a directory relative to the working
// directory meant `gravix explain` — the command whose entire purpose is showing
// where a number came from — answered "no contract for that metric version" for
// anyone who ran it from anywhere but the repository root. See F-008.
//
// The gateway image copies contracts/ in for the same reason (SD-010 part
// three); embedding removes that requirement too, but the COPY is left in place
// so a future reader can still find the files.
package contracts

import "embed"

// FS holds every contract file, exactly as committed.
//
//go:embed *.yaml
var FS embed.FS
