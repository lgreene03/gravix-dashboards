// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package lateness classifies how late a fact arrived relative to its event_time
// and decides what that means for derived partitions.
//
// Lateness is a property of the gap between when something happened and when
// Gravix heard about it. It is never a property of the fact's validity: a fact
// from six hours ago is as true as one from six seconds ago, and
// docs/00-system-truth.md §5 makes event_time the only source of truth for
// ordering. What lateness decides is how derivatives must react — which partition
// a fact belongs to, and whether that partition's published value has to change.
//
// Facts are classified into bands rather than tracked individually. An operator
// needs to know "is late data a growing problem", which a band answers, and
// per-fact inspection is a non-goal of the whole system.
package lateness

import "time"

// Class is a lateness band.
type Class string

const (
	// ClassOnTime means the fact arrived within the current rollup interval, so
	// its bucket had not been built yet and nothing needs revising.
	ClassOnTime Class = "on_time"
	// ClassLate means the fact arrived after its bucket was rolled up, but within
	// retention. Its partition can and must be rebuilt.
	ClassLate Class = "late"
	// ClassVeryLate means more than VeryLateAfter passed between the event and its
	// arrival. Still rebuildable; separated from late because a sustained rise
	// here usually means a sender is buffering or a clock is wrong.
	ClassVeryLate Class = "very_late"
	// ClassUnprocessable means the event_time falls in a window that has already
	// been purged. The fact is stored — facts are immutable and storage is cheap —
	// but no derivative can ever be built from it.
	ClassUnprocessable Class = "unprocessable"
)

// Valid reports whether c is one of the four defined classes. It exists so the
// metric's label set can be proven bounded rather than assumed.
func (c Class) Valid() bool {
	switch c {
	case ClassOnTime, ClassLate, ClassVeryLate, ClassUnprocessable:
		return true
	}
	return false
}

// Classes returns every defined class, in increasing order of lateness. The
// cardinality of the lateness metric's class label is exactly len(Classes()).
func Classes() []Class {
	return []Class{ClassOnTime, ClassLate, ClassVeryLate, ClassUnprocessable}
}

// Config bounds the classification.
type Config struct {
	// RollupInterval is how often derivatives are rebuilt. A fact younger than
	// this has not missed its bucket.
	RollupInterval time.Duration
	// VeryLateAfter is the gap beyond which lateness is worth investigating.
	VeryLateAfter time.Duration
	// RetentionDays is how long facts are kept. Beyond it, no derivative exists to
	// rebuild.
	RetentionDays int
}

// DefaultConfig returns RollupInterval 5m, VeryLateAfter 24h, RetentionDays 30.
func DefaultConfig() Config {
	return Config{
		RollupInterval: 5 * time.Minute,
		VeryLateAfter:  24 * time.Hour,
		RetentionDays:  30,
	}
}

// withDefaults fills in any zero field, so a caller passing a partial Config gets
// sensible bounds rather than a classification where everything is unprocessable.
func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.RollupInterval <= 0 {
		c.RollupInterval = d.RollupInterval
	}
	if c.VeryLateAfter <= 0 {
		c.VeryLateAfter = d.VeryLateAfter
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = d.RetentionDays
	}
	return c
}

// Classify returns the class of a fact with the given event time, observed at now.
//
// A fact whose event_time is in the future is classified on_time: its bucket has
// not been built yet, which is exactly what on_time means. A clock that is badly
// wrong is a separate problem, reported by the caller rather than hidden in a
// lateness band.
func Classify(cfg Config, eventTime, now time.Time) Class {
	cfg = cfg.withDefaults()
	age := now.Sub(eventTime)

	switch {
	case age >= retentionWindow(cfg):
		return ClassUnprocessable
	case age >= cfg.VeryLateAfter:
		return ClassVeryLate
	case age >= cfg.RollupInterval:
		return ClassLate
	default:
		return ClassOnTime
	}
}

// retentionWindow is how far back derivatives still exist.
func retentionWindow(cfg Config) time.Duration {
	return time.Duration(cfg.RetentionDays) * 24 * time.Hour
}

// AffectedDay returns the UTC day whose partition a fact belongs to. It is
// derived only from eventTime, never from arrival time — which is the whole point
// of event-time bucketing, and the thing a late fact must not be allowed to break.
func AffectedDay(eventTime time.Time) time.Time {
	u := eventTime.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// NeedsRebuild reports whether a fact of this class requires an already-published
// partition to be rebuilt. on_time facts do not: their bucket has not been
// published. unprocessable facts do not either: there is no partition left.
func NeedsRebuild(c Class) bool {
	return c == ClassLate || c == ClassVeryLate
}

// FutureBy returns how far an event time is ahead of now, or zero when it is not
// in the future. A caller uses it to warn about a sender's clock without letting
// that warning change how the fact is classified or whether it is accepted.
func FutureBy(cfg Config, eventTime, now time.Time) time.Duration {
	cfg = cfg.withDefaults()
	ahead := eventTime.Sub(now)
	if ahead <= cfg.RollupInterval {
		return 0
	}
	return ahead
}
