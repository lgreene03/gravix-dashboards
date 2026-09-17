// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package evolve

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/schemas"
)

// DeniedDimensions are fact fields that may never become rollup dimensions,
// because their cardinality is unbounded by construction.
//
// This list implements docs/04-non-goals.md §5 and is deliberately not
// configurable at runtime. A config option would turn a constraint into a
// suggestion, and an unbounded dimension admitted once is unrecoverable at scale:
// the partitions are already written by the time anyone notices the bill.
var DeniedDimensions = []string{
	"event_id",
	"user_id",
	"request_id",
	"session_id",
	"ip_address",
	"trace_id",
	"span_id",
}

// MaxDistinctValuesPerDay is the admission threshold for a candidate dimension,
// matching the bound docs/04-non-goals.md §5 already states.
const MaxDistinctValuesPerDay = 1000

// cardinalitySampleDays is how many days of the window are sampled when checking
// a candidate dimension. Sampling the whole window would mean reading every fact
// twice — once to check, once to build — for a question the busiest days already
// answer.
const cardinalitySampleDays = 3

// IsDenied reports whether a field is on the permanent deny list.
func IsDenied(field string) bool {
	for _, d := range DeniedDimensions {
		if d == field {
			return true
		}
	}
	return false
}

// RequestFactFields returns every field name on RequestFact, read from the
// protobuf descriptor rather than a hand-maintained list — so a field added to
// the schema is immediately known here, and a typo is immediately not.
func RequestFactFields() []string {
	fields := (&gravixv1.RequestFact{}).ProtoReflect().Descriptor().Fields()
	out := make([]string, 0, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		out = append(out, string(fields.Get(i).Name()))
	}
	sort.Strings(out)
	return out
}

// IsRequestFactField reports whether field exists on RequestFact.
func IsRequestFactField(field string) bool {
	for _, f := range RequestFactFields() {
		if f == field {
			return true
		}
	}
	return false
}

// CheckDimension samples facts over the window and reports whether field stays
// within MaxDistinctValuesPerDay. It returns the observed maximum so the caller
// can report a real number rather than a verdict alone.
//
// Sampling method: up to cardinalitySampleDays days are examined — the first, the
// middle and the last day that hold facts — and every fact in each sampled day is
// read. The count is therefore exact for the days sampled and an estimate for the
// window. That is the right trade: a dimension's cardinality is a property of the
// data's shape, which does not usually change between days, and the alternative is
// reading the whole window twice.
//
// The estimate can be wrong in one direction that matters: a field that is bounded
// on the sampled days and unbounded on an unsampled one would be admitted. The
// deny list exists because that risk cannot be sampled away for fields that are
// unbounded by construction.
func CheckDimension(ctx context.Context, opts Options, field string, w recompute.Window) (int, error) {
	// The deny list is checked first, and deliberately so. These names are refused
	// because their cardinality is unbounded by construction, which is true whether
	// or not the current fact schema happens to contain them. Checking membership
	// first would answer "no such field" today and "unbounded" after someone adds
	// one — two different messages for the same permanent answer.
	if IsDenied(field) {
		return 0, fmt.Errorf("%w: %q; see docs/04-non-goals.md §5", ErrUnboundedDimension, field)
	}
	if !IsRequestFactField(field) {
		return 0, fmt.Errorf("%w: %q", ErrUnknownField, field)
	}

	days, err := sampleDays(w)
	if err != nil {
		return 0, err
	}

	observedMax := 0
	for _, day := range days {
		distinct, err := distinctValuesOnDay(ctx, opts, field, day)
		if err != nil {
			return 0, err
		}
		if distinct > observedMax {
			observedMax = distinct
		}
	}
	return observedMax, nil
}

// sampleDays picks the first, middle and last day of a window.
func sampleDays(w recompute.Window) ([]time.Time, error) {
	partitions, err := recompute.Plan(w, nil)
	if err != nil {
		return nil, err
	}
	if len(partitions) == 0 {
		return nil, nil
	}

	idx := map[int]struct{}{
		0:                   {},
		len(partitions) / 2: {},
		len(partitions) - 1: {},
	}
	out := make([]time.Time, 0, cardinalitySampleDays)
	for i := range partitions {
		if _, ok := idx[i]; ok {
			out = append(out, partitions[i].Day)
		}
	}
	return out, nil
}

// distinctValuesOnDay counts the distinct values of a field across one day's facts.
func distinctValuesOnDay(ctx context.Context, opts Options, field string, day time.Time) (int, error) {
	dayStr := day.UTC().Format("2006-01-02")
	prefix := fmt.Sprintf("%s/%s", recompute.KeyPrefix(recompute.FactsDirFor(opts.InputDir, opts.TenantID)), dayStr)

	keys, err := opts.Store.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("evolve: listing %s: %w", prefix, err)
	}
	sort.Strings(keys)

	distinct := make(map[string]struct{})
	for _, key := range keys {
		if !strings.HasSuffix(key, ".jsonl") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		rc, err := opts.Store.Get(ctx, key)
		if err != nil {
			return 0, fmt.Errorf("evolve: reading %s: %w", key, err)
		}

		scanner := bufio.NewScanner(rc)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			fact, err := schemas.ParseRequestFact(line)
			if err != nil {
				continue
			}
			distinct[DimensionValue(fact, field)] = struct{}{}

			// Stop early once the budget is blown. The exact count past the limit
			// is not worth the remaining read; the answer is already no.
			if len(distinct) > MaxDistinctValuesPerDay {
				rc.Close()
				return len(distinct), nil
			}
		}
		scanErr := scanner.Err()
		rc.Close()
		if scanErr != nil {
			return 0, fmt.Errorf("evolve: scanning %s: %w", key, scanErr)
		}
	}
	return len(distinct), nil
}
