// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !race

package main

// raceDetectorEnabled reports whether this binary was built with -race.
//
// It exists for one reason: a timing budget measured under the race detector
// measures the detector. The instrumented merge of 1,440 sketches takes 582ms
// against a 400ms production budget it meets in 68ms uninstrumented — a 8.5x
// factor that says nothing about whether the code is fast enough.
const raceDetectorEnabled = false
