// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package degrade implements Gravix Enterprise's behaviour when a licence is
// absent, expired, or invalid. Its central guarantee is negative: nothing in
// this package can affect ingestion, rollup, alerting, dashboards, or export.
//
// That is not a promise about intent. Every function here is pure — it reads a
// licence value and a clock and returns a string, a state or an error. It opens
// no file, holds no handle, starts no goroutine and dials nothing, so there is
// no mechanism by which a lapsed licence could reach a core code path. The one
// test that matters, TestCoreUnaffectedInEveryState, runs the whole core
// pipeline under all four states and compares the bytes.
//
// A customer whose card expires on a Friday does not have an incident.
package degrade

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/license"
)

// State is the licence-driven capability level.
type State string

const (
	// StateLicensed: a valid, unexpired licence. Full ee/ function.
	StateLicensed State = "licensed"
	// StateGrace: expired within the grace window. Full ee/ function, notice shown.
	StateGrace State = "grace"
	// StateReadOnly: expired past grace. ee/ reads and exports work; ee/ writes refused.
	StateReadOnly State = "read_only"
	// StateAbsent: no licence at all. The normal OSS state. Silent; no notice, no log.
	StateAbsent State = "absent"
)

// GracePeriod is how long after expiry ee/ keeps working fully. Fourteen days
// covers a lapsed card, a purchasing cycle, and a holiday, which are the actual
// reasons licences expire.
const GracePeriod = 14 * 24 * time.Hour

// ClockSkewTolerance is how far past expiry a licence is still treated as fully
// valid with no notice at all, because a machine whose clock has drifted is a
// likelier explanation than a customer who let a licence lapse by an hour.
//
// It moves only the boundary between StateLicensed and StateGrace. Full function
// still ends exactly GracePeriod after the recorded expiry, so the fourteen days
// a customer is told about are fourteen days.
const ClockSkewTolerance = 24 * time.Hour

// Result is what verification produced: the licence that was parsed, if any, and
// the error that came with it.
//
// GRVX-1303 §5.1 names this parameter license.Result. pkg/license (GRVX-1301)
// defines no such type — it returns (*License, error) — and adding one would be
// an edit to core, which §4.3 forbids this spec from making. Carrying the pair
// here costs nothing and keeps the boundary intact. See SD-039.
type Result struct {
	License *license.License
	Err     error
}

// FromEnv reads the licence the way the Enterprise binary does at startup.
// It is here rather than at each call site so that "how ee/ learns its state"
// has exactly one answer.
func FromEnv() Result {
	lic, err := license.FromEnv()
	return Result{License: lic, Err: err}
}

// Evaluate returns the current state from a verification result.
//
// Anything that did not yield a licence — no licence configured, a malformed
// token, a bad signature — is StateAbsent. Charter §7.5: a tampered or absent
// licence is not an error condition, it is the normal state of the OSS build,
// and it must produce no warning noise. Treating a forged token as "absent"
// rather than "invalid" is also what denies a forger any signal to iterate on.
func Evaluate(v Result, now time.Time) State {
	if v.License == nil {
		return StateAbsent
	}
	expiry := v.License.ExpiresAt.UTC()
	now = now.UTC()

	switch {
	case !now.After(expiry.Add(ClockSkewTolerance)):
		return StateLicensed
	case !now.After(expiry.Add(GracePeriod)):
		return StateGrace
	default:
		return StateReadOnly
	}
}

// Notice returns the message to display for a state, or "" when nothing should
// be shown.
//
// StateAbsent always returns "" — the OSS build must be silent. StateLicensed
// returns "" because a working licence is not news. The two messages that do
// exist are factual, dated, and say in their own text that core monitoring is
// unaffected, because the first question anybody reading them has is whether
// their monitoring just stopped.
func Notice(s State, expiresAt time.Time) string {
	on := expiresAt.UTC().Format("2006-01-02")
	switch s {
	case StateGrace:
		return fmt.Sprintf(
			"Your Gravix Enterprise licence expired on %s. Enterprise features continue to work "+
				"until %s. Core monitoring, ingestion, rollup, alerting and export are unaffected.",
			on, expiresAt.UTC().Add(GracePeriod).Format("2006-01-02"))
	case StateReadOnly:
		return fmt.Sprintf(
			"Your Gravix Enterprise licence expired on %s. Enterprise configuration is readable "+
				"and exportable, but cannot be changed. Core monitoring, ingestion, rollup, "+
				"alerting and export are unaffected.",
			on)
	default:
		return ""
	}
}

// SkewWarning returns the one diagnostic ee/ emits about a clock, or "".
//
// It fires when a licence's expiry is in the past by more than the tolerance,
// because an unexpected expiry and a wrong system clock look identical from
// inside the process and the operator is the only one who can tell them apart.
// It says nothing about the licence's validity: the state has already been
// decided by Evaluate, and this cannot change it.
func SkewWarning(v Result, now time.Time) string {
	if v.License == nil {
		return ""
	}
	behind := now.UTC().Sub(v.License.ExpiresAt.UTC())
	if behind <= ClockSkewTolerance {
		return ""
	}
	return fmt.Sprintf("licence expiry is %s in the past relative to system clock; check NTP",
		behind.Round(time.Hour))
}

// Reporter emits the at-most-one line ee/ is allowed to log about a licence.
//
// It exists so that "log once" is a property of a type rather than a convention
// every ee/ package is trusted to follow, and so that TestAbsentStateIsSilent
// can hold a real logger and assert that nothing reached it.
type Reporter struct {
	log  *slog.Logger
	once sync.Once
}

// NewReporter returns a Reporter that writes to log. A nil logger makes every
// Report a no-op, which is the correct behaviour for a caller that has decided
// it wants no licence diagnostics at all.
func NewReporter(log *slog.Logger) *Reporter {
	return &Reporter{log: log}
}

// Report logs the clock-skew warning at most once for the life of the Reporter.
//
// In StateAbsent it does nothing whatsoever: no record, no level, no attribute.
// An OSS user must not be able to tell from a log that ee/ code paths exist
// (charter §7.4, §7.5).
func (r *Reporter) Report(v Result, now time.Time) {
	if r == nil || r.log == nil {
		return
	}
	if Evaluate(v, now) == StateAbsent {
		return
	}
	msg := SkewWarning(v, now)
	if msg == "" {
		return
	}
	r.once.Do(func() { r.log.Warn(msg) })
}
