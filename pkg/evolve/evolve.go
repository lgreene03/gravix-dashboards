// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package evolve adds percentiles and dimensions to a metric and backfills them
// from raw facts, so a definition change applies to history as well as to new data.
//
// This is the capability docs/00-system-truth.md §4 was written to make possible.
// Prometheus cannot do it because it discarded the raw observation at scrape time;
// a hosted platform cannot because it never stored the request. Gravix kept the
// facts, so a metric that never existed can be computed for last month.
//
// The two kinds of change cost very different things:
//
//   - A new percentile needs no fact read at all. GRVX-804 stored a mergeable
//     sketch per bucket, and a quantile is a question the sketch already answers.
//   - A new dimension does need a full re-read, because the dimension was never in
//     the aggregation key: the rows that would carry it were never separated.
//
// Both are refused rather than fudged when the information genuinely is not there:
// a percentile over partitions written before sketches existed, or a window
// reaching past fact retention.
package evolve

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Kind is what is being added.
type Kind string

const (
	// KindPercentile derives a new quantile from the stored sketch. No fact re-read.
	KindPercentile Kind = "percentile"
	// KindDimension adds a grouping key. Requires re-reading facts.
	KindDimension Kind = "dimension"
)

var (
	// ErrUnboundedDimension is returned for a field that may never be a dimension.
	ErrUnboundedDimension = errors.New("evolve: dimension is unbounded and cannot be added")
	// ErrUnknownField is returned for a field that is not on RequestFact at all.
	ErrUnknownField = errors.New("evolve: field is not a RequestFact field")
	// ErrQuantileRange is returned for a quantile at or outside the open interval.
	ErrQuantileRange = errors.New("evolve: quantile must be strictly between 0 and 1")
	// ErrBeyondRetention is returned when no requested day still has facts.
	ErrBeyondRetention = errors.New("evolve: requested window extends beyond fact retention")
	// ErrNoSketch is returned when a percentile is asked of partitions written
	// before sketches existed. Those partitions genuinely lack the information.
	ErrNoSketch = errors.New("evolve: partitions predate sketch storage; a new percentile cannot be derived")
	// ErrUnsupportedDimension is returned for a bounded RequestFact field that has
	// no column in the metric row. See docs/oss/spec-defects.md SD-008.
	ErrUnsupportedDimension = errors.New("evolve: field has no metric column and cannot yet be a dimension")
	// ErrNoChange is returned when a Change names neither a quantile nor a field.
	ErrNoChange = errors.New("evolve: change specifies nothing to add")
)

// Change is one addition to a metric.
type Change struct {
	Kind     Kind
	Metric   string  // e.g. "request_metrics_minute"
	Quantile float64 // KindPercentile only; 0 < q < 1
	Field    string  // KindDimension only; a RequestFact field name
}

// Detail renders what is being added, for the CLI report.
func (c Change) Detail() string {
	switch c.Kind {
	case KindPercentile:
		return "p" + strconv.FormatFloat(c.Quantile*100, 'f', -1, 64)
	case KindDimension:
		return c.Field
	default:
		return string(c.Kind)
	}
}

// Options configures planning and execution.
type Options struct {
	Store     storage.ObjectStore
	InputDir  string // raw facts root, e.g. "./data/raw"
	OutputDir string // warehouse root, e.g. "./data/warehouse"
	TenantID  string
	// RetentionDays bounds how far back facts still exist. 0 means 30.
	RetentionDays int
	// Now is the clock used to decide what has fallen out of retention. Zero means
	// time.Now.
	Now time.Time
}

func (o Options) retentionDays() int {
	if o.RetentionDays <= 0 {
		return 30
	}
	return o.RetentionDays
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

func (o Options) metric() string { return recompute.MetricRequestMinute }

// Plan describes what a Change requires, before anything is written.
type Plan struct {
	Change              Change
	NewMetricVersion    string
	RequiresFactRead    bool
	Partitions          int
	EarliestDay         time.Time
	LatestDay           time.Time
	DaysBeyondRetention int
	EstimatedRows       int64

	// ObservedCardinality is the distinct-value count CheckDimension measured, for
	// a dimension change. Zero for a percentile.
	ObservedCardinality int

	// window is the portion of the request that can actually be backfilled.
	window recompute.Window
	// days are the partitions to rebuild, all within retention.
	days []time.Time
}

// PlanChange validates a Change and returns what executing it would do. It writes
// nothing.
func PlanChange(ctx context.Context, opts Options, c Change, w recompute.Window) (*Plan, error) {
	if c.Metric == "" {
		c.Metric = opts.metric()
	}

	partitions, err := recompute.Plan(w, nil)
	if err != nil {
		return nil, err
	}

	inRetention, beyond := splitByRetention(partitions, opts)
	if len(inRetention) == 0 {
		return nil, fmt.Errorf("%w: all %d requested day(s) are older than %d days",
			ErrBeyondRetention, len(partitions), opts.retentionDays())
	}

	plan := &Plan{
		Change:              c,
		Partitions:          len(inRetention),
		DaysBeyondRetention: beyond,
		EarliestDay:         inRetention[0],
		LatestDay:           inRetention[len(inRetention)-1],
		days:                inRetention,
		window:              recompute.Window{From: inRetention[0], To: inRetention[len(inRetention)-1].AddDate(0, 0, 1)},
	}

	switch c.Kind {
	case KindPercentile:
		if err := validateQuantile(c.Quantile); err != nil {
			return nil, err
		}
		plan.RequiresFactRead = false
		plan.NewMetricVersion = nextVersion(recompute.MetricVersion)
		if err := requireSketches(ctx, opts, plan); err != nil {
			return nil, err
		}

	case KindDimension:
		observed, err := CheckDimension(ctx, opts, c.Field, plan.window)
		if err != nil {
			return nil, err
		}
		if observed > MaxDistinctValuesPerDay {
			return nil, fmt.Errorf("evolve: dimension %q has %d distinct values per day, above the limit of %d",
				c.Field, observed, MaxDistinctValuesPerDay)
		}
		if !recompute.SupportsDimension(c.Field) {
			return nil, fmt.Errorf("%w: %q; supported: %v",
				ErrUnsupportedDimension, c.Field, recompute.SupportedDimensions())
		}
		plan.ObservedCardinality = observed
		plan.RequiresFactRead = true
		plan.NewMetricVersion = nextVersion(recompute.MetricVersion)

	default:
		return nil, ErrNoChange
	}

	return plan, nil
}

// Apply executes a planned change, backfilling every partition in the window.
//
// Both kinds go through pkg/recompute, so the output stays deterministic and the
// manifest revision increments exactly as a late fact would make it — an evolution
// is a revision of a published value, and is recorded as one.
func Apply(ctx context.Context, opts Options, p *Plan, dryRun bool) (*recompute.Result, error) {
	if p == nil {
		return nil, ErrNoChange
	}

	evo := recompute.Evolution{}
	switch p.Change.Kind {
	case KindPercentile:
		evo.ExtraQuantiles = []float64{p.Change.Quantile}
	case KindDimension:
		evo.Dimensions = []string{p.Change.Field}
	default:
		return nil, ErrNoChange
	}

	return recompute.Run(ctx, recompute.Options{
		Store:     opts.Store,
		InputDir:  opts.InputDir,
		OutputDir: opts.OutputDir,
		Metric:    p.Change.Metric,
		Window:    p.window,
		TenantIDs: tenantList(opts.TenantID),
		DryRun:    dryRun,
		Evolution: evo,
	})
}

func tenantList(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

// validateQuantile enforces the open interval. A quantile of 0 or 1 is the
// minimum or maximum, which is not a percentile question and which the sketch
// answers poorly at the extremes.
func validateQuantile(q float64) error {
	if q <= 0 || q >= 1 {
		return fmt.Errorf("%w, got %v", ErrQuantileRange, q)
	}
	return nil
}

// splitByRetention divides planned partitions into those whose facts still exist
// and a count of those whose do not.
func splitByRetention(partitions []recompute.Partition, opts Options) ([]time.Time, int) {
	cutoff := opts.now().AddDate(0, 0, -opts.retentionDays())
	cutoffDay := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.UTC)

	var kept []time.Time
	beyond := 0
	for _, p := range partitions {
		if p.Day.Before(cutoffDay) {
			beyond++
			continue
		}
		kept = append(kept, p.Day)
	}
	return kept, beyond
}

// requireSketches refuses a percentile addition over partitions written before
// sketches existed, naming the days. Deriving a quantile from a partition with no
// sketch would mean inventing one.
func requireSketches(ctx context.Context, opts Options, plan *Plan) error {
	metricDir := recompute.MetricDirFor(opts.OutputDir, opts.TenantID, plan.Change.Metric)

	var missing []string
	for _, day := range plan.days {
		key := recompute.DeterministicKey(
			recompute.PartitionDir(metricDir, day), plan.Change.Metric, day)

		m, err := manifest.Read(ctx, opts.Store, key)
		if errors.Is(err, manifest.ErrNoManifest) {
			// No partition at all yet: it will be built from facts, sketch included.
			continue
		}
		if err != nil {
			return err
		}
		if versionNumber(m.MetricVersion) < 2 {
			missing = append(missing, day.Format("2006-01-02"))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("evolve: partitions %v predate sketch storage; a new percentile cannot be derived from them: %w",
			missing, ErrNoSketch)
	}
	return nil
}

// nextVersion returns the version after v: "v2" -> "v3".
func nextVersion(v string) string {
	return "v" + strconv.Itoa(versionNumber(v)+1)
}

// versionNumber parses "v2" into 2, returning 0 for anything unparseable.
func versionNumber(v string) int {
	if len(v) < 2 || v[0] != 'v' {
		return 0
	}
	n, err := strconv.Atoi(v[1:])
	if err != nil {
		return 0
	}
	return n
}

// DimensionValue renders a fact's field as the string a dimension groups by.
// It reads through the protobuf reflection API, so every scalar field is
// available without a hand-maintained switch.
func DimensionValue(fact *gravixv1.RequestFact, field string) string {
	fields := fact.ProtoReflect().Descriptor().Fields()
	fd := fields.ByName(protoName(field))
	if fd == nil {
		return ""
	}
	return fact.ProtoReflect().Get(fd).String()
}

func protoName(field string) protoreflect.Name { return protoreflect.Name(field) }
