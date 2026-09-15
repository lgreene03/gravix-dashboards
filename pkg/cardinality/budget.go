// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package cardinality enforces a hard per-tenant, per-metric cap on the
// number of distinct label-set combinations accepted from external metrics
// protocols (Prometheus remote-write, OTLP metrics) within one rolling
// window, so neither protocol can be used to smuggle per-request or
// per-user dimensions into Gravix. State is in-memory only; it does not
// survive a process restart.
package cardinality

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultMaxSeriesPerMetricPerDay is the hard cap on distinct label-set
// combinations accepted for one (tenant, metric name) pair within one
// rolling 24-hour window.
const DefaultMaxSeriesPerMetricPerDay = 2000

// Budget is safe for concurrent use.
type Budget struct {
	max int

	mu sync.Mutex
	// series maps a (tenant, metric) pair to the set of label-set
	// fingerprints admitted for it in the current window.
	series map[metricKey]map[string]struct{}
}

// metricKey identifies one (tenant, metric name) pair. It is a struct rather
// than a joined string so that a tenant ID containing the separator cannot
// be made to collide with another tenant's metric.
type metricKey struct {
	tenantID   string
	metricName string
}

// NewBudget constructs a Budget with the given per-metric window cap.
// NewBudget panics if max < 1.
func NewBudget(max int) *Budget {
	if max < 1 {
		panic("cardinality: NewBudget requires max >= 1")
	}
	return &Budget{
		max:    max,
		series: make(map[metricKey]map[string]struct{}),
	}
}

// Admit reports whether a data point with the given tenant, metric name,
// and label set (excluding the metric name itself) is within budget. A
// label set already admitted in the current window is always re-admitted
// without consuming additional budget. count is the number of distinct
// label sets recorded for (tenantID, metricName) after this call.
func (b *Budget) Admit(tenantID, metricName string, labels map[string]string) (admitted bool, count int) {
	fp := fingerprint(labels)
	key := metricKey{tenantID: tenantID, metricName: metricName}

	b.mu.Lock()
	defer b.mu.Unlock()

	seen, ok := b.series[key]
	if !ok {
		seen = make(map[string]struct{})
		b.series[key] = seen
	}

	// A label set already inside the window costs nothing. Without this a
	// steady, well-behaved series would exhaust its own budget by being
	// scraped.
	if _, known := seen[fp]; known {
		return true, len(seen)
	}

	if len(seen) >= b.max {
		return false, len(seen)
	}

	seen[fp] = struct{}{}
	return true, len(seen)
}

// Max returns the configured per-metric cap.
func (b *Budget) Max() int {
	return b.max
}

// Reset clears all tracked series. The caller is responsible for invoking
// Reset on a schedule (see services/ingestion/main.go's 24h ticker); Budget
// itself runs no background goroutine.
func (b *Budget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.series = make(map[metricKey]map[string]struct{})
}

// fingerprint returns a collision-free encoding of a label set.
//
// Label names and values arrive from an untrusted remote-write payload, so a
// naive "k=v,k=v" join is not good enough: a sender who controls a value can
// embed the separators and make two genuinely different label sets produce
// the same string, which would let one series masquerade as another and slip
// past the budget this package exists to enforce. Every part is therefore
// length-prefixed, which no choice of label content can forge.
func fingerprint(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		value := labels[name]
		sb.WriteString(strconv.Itoa(len(name)))
		sb.WriteByte(':')
		sb.WriteString(name)
		sb.WriteString(strconv.Itoa(len(value)))
		sb.WriteByte(':')
		sb.WriteString(value)
	}
	return sb.String()
}
