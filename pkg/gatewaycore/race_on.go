// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

//go:build race

package gatewaycore

// raceDetectorEnabled reports whether this binary was built with -race.
// See race_off.go for why the distinction is worth making.
const raceDetectorEnabled = true
