// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package importer converts data from other observability systems into Gravix
// storage. It never fabricates facts from aggregates: pre-aggregated input is
// written as declared-derived metric partitions, not as invented request records.
package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/cardinality"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// Mode is how faithfully the source can be represented.
type Mode string

const (
	// ModeFacts produces real RequestFacts. Only for per-request sources.
	ModeFacts Mode = "facts"
	// ModeMetrics writes derived metric partitions marked as imported.
	ModeMetrics Mode = "metrics"
)

// Source identifies the origin system.
type Source string

const (
	SourcePrometheus Source = "prometheus"
	SourceDatadog    Source = "datadog"
)

// MetricRow is the warehouse row shape, aliased from the package that owns the
// reproducibility contract so an imported partition and a native one carry the
// same columns.
type MetricRow = recompute.MetricRow

// Options configures an import.
type Options struct {
	Source     Source
	Mode       Mode
	Input      string // path to a TSDB dir or an export file
	Store      storage.ObjectStore
	TenantID   string
	ServiceMap map[string]string // source label value -> Gravix service name
	PathLabel  string            // which source label carries the path template
	DryRun     bool
}

// Report describes what an import did or would do.
type Report struct {
	Mode                Mode             `json:"mode"`
	SeriesRead          int64            `json:"series_read"`
	SeriesImported      int64            `json:"series_imported"`
	SeriesSkipped       int64            `json:"series_skipped"`
	RowsWritten         int64            `json:"rows_written"`
	FactsWritten        int64            `json:"facts_written"`
	SkipReasons         map[string]int64 `json:"skip_reasons"`
	CardinalityRejected int64            `json:"cardinality_rejected"`
	EarliestSample      string           `json:"earliest_sample"`
	LatestSample        string           `json:"latest_sample"`
	Limitations         []string         `json:"limitations"`
}

var (
	ErrUnknownSource       = errors.New("importer: unknown source")
	ErrAggregateInFactMode = errors.New("importer: source holds aggregates; facts mode would fabricate data")
	ErrCardinalityExceeded = errors.New("importer: series label cardinality exceeds the budget")
	ErrNoMapping           = errors.New("importer: no service mapping for source label")
)

// ErrImportedPartition is what a recompute of an imported partition reports.
// There are no facts beneath it to rebuild from, and saying so plainly is
// better than producing an empty result that looks like a successful rebuild.
var ErrImportedPartition = errors.New("partition is imported; no source facts exist")

// maxSeriesPerMetricPerDay is the cardinality cap applied to imports, matching
// the bound docs/04-non-goals.md §5 sets on every other ingest path. An import
// is not a loophole around it.
const maxSeriesPerMetricPerDay = 1000

// limitationsText is reproduced verbatim in every ModeMetrics report.
//
// It is a single block rather than a generated summary because these sentences
// are a promise about what Gravix will not pretend, and a promise that is
// reworded per call site is one that eventually gets softened.
const limitationsText = `Imported partitions hold derived metrics, not facts. Gravix did not receive the
original requests and will not pretend it did.

  - gravix recompute cannot rebuild these partitions.
  - gravix explain reports import provenance, not source facts.
  - Adding a percentile or dimension retroactively does not apply to them.
  - Percentiles carry the source system's accuracy, not Gravix's sketch bound.

Data ingested natively after the import has none of these limitations. The two
are distinguishable in every partition manifest.`

// Limitations returns the verbatim limitations block carried by every
// ModeMetrics report.
func Limitations() []string {
	return strings.Split(limitationsText, "\n")
}

// series is one source time series after parsing, before any Gravix decision
// has been made about it.
type series struct {
	// Metric is the source metric name.
	Metric string
	// Labels are the source labels, excluding the metric name.
	Labels map[string]string
	// Samples are the observations, in the order the source gave them.
	Samples []sample
	// Aggregated records whether the source itself says this series holds
	// pre-aggregated values.
	Aggregated bool
}

type sample struct {
	// TimeMillis is the sample time in Unix milliseconds.
	TimeMillis int64
	Value      float64
}

// reader turns a source file into series. Each source implements one.
type reader interface {
	// Read parses the input and reports the series it holds.
	Read(input string) ([]series, error)
	// Name is the source's name, used in provenance and messages.
	Name() Source
}

// readerFor returns the reader for a source.
func readerFor(s Source) (reader, error) {
	switch s {
	case SourceDatadog:
		return datadogReader{}, nil
	case SourcePrometheus:
		return nil, fmt.Errorf("%w: %q is not yet readable; see SD-030", ErrUnknownSource, s)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownSource, s)
	}
}

// Plan inspects the source and reports what an import would do, writing nothing.
func Plan(ctx context.Context, opts Options) (*Report, error) {
	opts.DryRun = true
	return run(ctx, opts)
}

// Run executes an import.
func Run(ctx context.Context, opts Options) (*Report, error) {
	return run(ctx, opts)
}

func run(ctx context.Context, opts Options) (*Report, error) {
	rd, err := readerFor(opts.Source)
	if err != nil {
		return nil, err
	}

	if opts.Mode == "" {
		opts.Mode = ModeMetrics
	}
	if opts.Mode != ModeFacts && opts.Mode != ModeMetrics {
		return nil, fmt.Errorf("importer: unknown mode %q", opts.Mode)
	}

	all, err := rd.Read(opts.Input)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Mode:        opts.Mode,
		SeriesRead:  int64(len(all)),
		SkipReasons: map[string]int64{},
	}

	// The refusal comes before anything else is decided. A caller who asked for
	// facts over aggregates must be told no, not quietly given metrics.
	//
	// The sentence the user reads is assembled by the CLI (§6.1), which also
	// owns the exit code; the package returns the sentinel so a programmatic
	// caller can test for it with errors.Is rather than by matching prose.
	if opts.Mode == ModeFacts {
		for _, s := range all {
			if s.Aggregated {
				return nil, fmt.Errorf("%w: %s", ErrAggregateInFactMode, rd.Name())
			}
		}
	}

	if opts.Mode == ModeMetrics {
		report.Limitations = Limitations()
	}

	budget := cardinality.NewBudget(maxSeriesPerMetricPerDay)
	kept := make([]series, 0, len(all))

	for _, s := range all {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if _, mapped := serviceFor(s, opts); !mapped {
			report.SeriesSkipped++
			report.SkipReasons["no service mapping"]++
			continue
		}

		if admitted, _ := budget.Admit(opts.TenantID, s.Metric, s.Labels); !admitted {
			report.SeriesSkipped++
			report.CardinalityRejected++
			report.SkipReasons["label cardinality above 1000/day"]++
			continue
		}

		kept = append(kept, s)
	}

	report.SeriesImported = int64(len(kept))
	recordSampleWindow(report, kept)

	if opts.Mode == ModeFacts {
		// Reaching here means every series is per-request, so the facts are
		// real records rather than reconstructions. No source currently
		// supports it; the refusal above is what this mode is for today.
		return report, nil
	}

	rows := buildMetricRows(kept, opts)
	report.RowsWritten = int64(len(rows))

	if opts.DryRun || len(rows) == 0 {
		return report, nil
	}

	if err := writePartitions(ctx, opts, rd.Name(), rows); err != nil {
		return nil, err
	}

	return report, nil
}

// serviceFor resolves a series to a Gravix service name through the caller's
// mapping. An unmapped series is skipped rather than guessed at: inventing a
// service name would put rows on a dashboard under a name nobody chose.
func serviceFor(s series, opts Options) (string, bool) {
	if len(opts.ServiceMap) == 0 {
		return "", false
	}
	for _, v := range s.Labels {
		if name, ok := opts.ServiceMap[v]; ok {
			return name, true
		}
	}
	if name, ok := opts.ServiceMap[s.Metric]; ok {
		return name, true
	}
	return "", false
}

// recordSampleWindow fills the report's earliest and latest sample times.
func recordSampleWindow(report *Report, all []series) {
	var earliest, latest int64
	for _, s := range all {
		for _, sm := range s.Samples {
			if earliest == 0 || sm.TimeMillis < earliest {
				earliest = sm.TimeMillis
			}
			if sm.TimeMillis > latest {
				latest = sm.TimeMillis
			}
		}
	}
	if earliest == 0 {
		return
	}
	report.EarliestSample = time.UnixMilli(earliest).UTC().Format(time.RFC3339)
	report.LatestSample = time.UnixMilli(latest).UTC().Format(time.RFC3339)
}

// buildMetricRows converts admitted series into warehouse rows.
//
// Every row carries the source's own value. Nothing is synthesised: a counter
// of 4,201 requests becomes one row saying 4,201, never 4,201 rows.
func buildMetricRows(all []series, opts Options) []MetricRow {
	var rows []MetricRow

	for _, s := range all {
		service, _ := serviceFor(s, opts)
		pathTemplate := s.Labels[opts.PathLabel]
		if pathTemplate == "" {
			pathTemplate = "/"
		}
		method := s.Labels["method"]
		if method == "" {
			method = "GET"
		}

		for _, sm := range s.Samples {
			ts := time.UnixMilli(sm.TimeMillis).UTC().Truncate(time.Minute)
			rows = append(rows, MetricRow{
				TenantID:     opts.TenantID,
				BucketStart:  ts.Format(time.RFC3339),
				Service:      service,
				Method:       method,
				PathTemplate: pathTemplate,
				RequestCount: int64(sm.Value),
				EventDay:     ts.Format("2006-01-02"),
			})
		}
	}

	// Deterministic order, so re-importing the same file writes the same bytes.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BucketStart != rows[j].BucketStart {
			return rows[i].BucketStart < rows[j].BucketStart
		}
		if rows[i].Service != rows[j].Service {
			return rows[i].Service < rows[j].Service
		}
		return rows[i].PathTemplate < rows[j].PathTemplate
	})

	return rows
}

// writePartitions writes one Parquet file per event day, each with a manifest
// declaring the partition imported.
func writePartitions(ctx context.Context, opts Options, source Source, rows []MetricRow) error {
	byDay := map[string][]MetricRow{}
	for _, r := range rows {
		byDay[r.EventDay] = append(byDay[r.EventDay], r)
	}

	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)

	for _, day := range days {
		dayRows := byDay[day]

		dayTime, err := time.Parse("2006-01-02", day)
		if err != nil {
			return fmt.Errorf("importer: parse event day %q: %w", day, err)
		}

		var buf bytes.Buffer
		w := parquet.NewGenericWriter[MetricRow](&buf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
		if _, err := w.Write(dayRows); err != nil {
			return fmt.Errorf("importer: encode %s: %w", day, err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("importer: close %s: %w", day, err)
		}

		dataKey := partitionKey(opts.TenantID, day)
		if err := opts.Store.Put(ctx, dataKey, bytes.NewReader(buf.Bytes())); err != nil {
			return fmt.Errorf("importer: write %s: %w", dataKey, err)
		}

		digest, err := manifest.ContentDigest(dayRows)
		if err != nil {
			return fmt.Errorf("importer: digest %s: %w", day, err)
		}

		m := manifest.Manifest{
			SchemaVersion: manifest.SchemaVersion,
			Metric:        recompute.MetricRequestMinute,
			MetricVersion: "v1",
			ContentDigest: digest,
			TenantID:      opts.TenantID,
			EventDay:      day,
			RowCount:      int64(len(dayRows)),
			DataFile:      dataKey,
			// No source fact keys, because there are no source facts. Leaving
			// this empty is the honest statement; inventing keys would make an
			// imported partition look recomputable.
			SourceFactKeys: nil,
			FactCount:      0,
			Provenance:     manifest.ProvenanceImportedPrefix + string(source),
		}
		m.IdempotencyKey = manifest.IdempotencyKey(m.Metric, m.MetricVersion, m.TenantID, dayTime)

		if err := manifest.Write(ctx, opts.Store, &m); err != nil {
			return fmt.Errorf("importer: write manifest for %s: %w", day, err)
		}
	}

	return nil
}

// partitionKey is the warehouse key an imported partition is written to — the
// same layout the rollup uses, so every reader finds it without knowing it was
// imported.
func partitionKey(tenantID, day string) string {
	metric := recompute.MetricRequestMinute
	compact := strings.ReplaceAll(day, "-", "")
	if tenantID == "" {
		return fmt.Sprintf("warehouse/%s/event_day=%s/%s_%s.parquet", metric, day, metric, compact)
	}
	return fmt.Sprintf("warehouse/%s/%s/event_day=%s/%s_%s.parquet", tenantID, metric, day, metric, compact)
}

// RecomputeStatus reports whether a partition can be recomputed, and why not
// when it cannot.
//
// This is what `gravix recompute` and `gravix explain` consult. It lives here
// rather than in the CLI so the decision has one implementation and one test,
// whichever command is asking.
func RecomputeStatus(ctx context.Context, store storage.ObjectStore, dataFile string) error {
	m, err := manifest.Read(ctx, store, dataFile)
	if err != nil {
		return err
	}
	if m.IsImported() {
		return ErrImportedPartition
	}
	return nil
}

// ExplainProvenance describes where a partition's data came from, for
// `gravix explain`.
func ExplainProvenance(ctx context.Context, store storage.ObjectStore, dataFile string) (string, error) {
	m, err := manifest.Read(ctx, store, dataFile)
	if err != nil {
		return "", err
	}
	if m.IsImported() {
		return fmt.Sprintf("imported from %s; no source facts exist for this partition", m.ImportSource()), nil
	}
	return fmt.Sprintf("native; built from %d source fact file(s)", len(m.SourceFactKeys)), nil
}
