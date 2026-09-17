// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package slo

import (
	"fmt"
	"math"

	"github.com/lgreene/gravix-dashboards/pkg/sketch"
)

// fastEnough returns how many of a bucket's requests beat the threshold.
//
// The sketch answers "what value sits at quantile q"; this needs the inverse,
// "what quantile does this value sit at". pkg/sketch has no CDF and GRVX-804
// settled that package, so the inverse is found by bisection over Quantile
// rather than by reaching into the digest — 50 iterations, which is far finer
// than the sketch's own resolution, so the bisection contributes nothing to the
// error and the sketch's bound is the whole of it.
func fastEnough(b *Bucket, thresholdMs float64) (int64, error) {
	if b.RequestCount == 0 {
		return 0, nil
	}
	if len(b.LatencySketch) == 0 {
		// A bucket written before sketches existed cannot answer this. Saying so
		// is the honest failure; counting it as all-good would make a latency SLO
		// report perfect health for exactly the period it cannot see.
		return 0, fmt.Errorf("slo: bucket %s has no latency sketch, so a latency SLO cannot be "+
			"computed for it; run gravix recompute for that day", b.Start.Format("2006-01-02 15:04"))
	}

	var s sketch.Sketch
	if err := s.UnmarshalBinary(b.LatencySketch); err != nil {
		return 0, fmt.Errorf("slo: decoding sketch for bucket %s: %w",
			b.Start.Format("2006-01-02 15:04"), err)
	}

	frac := cdf(&s, thresholdMs)
	// Round rather than truncate: over a month of buckets, truncating loses up to
	// one request per bucket, which is 43,200 requests of silent pessimism.
	good := int64(math.Round(frac * float64(b.RequestCount)))

	// A sketch's quantile function is continuous; a bucket's requests are not.
	// At a threshold sitting exactly on the fastest request, the continuous CDF
	// is ~0 and rounds to zero requests — but that request did meet the
	// threshold. Floor it at one, and cap it below the total for the same reason
	// at the other end. Same continuous-versus-discrete tension as CD-003, in the
	// one place here where it changes an answer rather than a decimal.
	if good == 0 && frac > 0 {
		good = 1
	}
	if good > b.RequestCount {
		good = b.RequestCount
	}
	return good, nil
}

// cdf estimates the fraction of a sketch's observations at or below v.
//
// Bisection on a monotone function. Quantile is non-decreasing in q, so the
// largest q whose value is <= v is exactly the fraction sought.
func cdf(s *sketch.Sketch, v float64) float64 {
	lo, hi := 0.0, 1.0

	// Both ends first: outside the observed range the answer is 0 or 1 exactly,
	// and bisecting to find that out would give 0.9999999 instead of 1.
	minimum, err := s.Quantile(1e-9)
	if err != nil || v < minimum {
		return 0
	}
	if q, err := s.Quantile(1 - 1e-9); err == nil && v >= q {
		return 1
	}

	for i := 0; i < 50; i++ {
		mid := (lo + hi) / 2
		q, qerr := s.Quantile(mid)
		if qerr != nil {
			return lo
		}
		if q <= v {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}
