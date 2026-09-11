// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recompute

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/lateness"
	"github.com/lgreene/gravix-dashboards/pkg/leaderelect"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/montanaflynn/stats"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// MetricRequestMinute is the only metric this engine can currently rebuild.
const MetricRequestMinute = "request_metrics_minute"

// MetricVersion is the version of the metric definitions this engine computes.
// It is part of a partition's idempotency key, so changing a formula must change
// this: two files with the same key are claimed to describe the same thing.
//
// v2 adds the mergeable latency sketch (GRVX-804). The per-bucket scalar
// percentiles are unchanged from v1 — within one bucket they were always exact —
// so a v1 and a v2 partition report the same numbers for a single bucket and
// differ only in whether a correct cross-bucket percentile is possible.
const MetricVersion = "v2"

// CompressionLevel pins the zstd level used for every Parquet file this package
// writes. It is an explicit constant rather than zstd.SpeedDefault because a
// library upgrade may redefine the default, which would change the bytes of an
// otherwise identical rebuild and break reproducibility.
const CompressionLevel = zstd.SpeedFastest

// LockName is the leader-election lock shared with the cron rollup job in
// transforms/request_metrics_minute, so a rollup and a recompute can never write
// the same partition at the same time.
const LockName = "request-metrics-rollup"

// dataRoot is the local-store data root. Callers pass filesystem-style
// directories ("./data/warehouse"), while ObjectStore keys are relative to the
// store's root, so this leading element is stripped when converting one to the
// other. This is the same convention the cron rollup's -input-dir and
// -output-dir flags already use.
const dataRoot = "data"

var (
	// ErrUnknownMetric is returned when Options.Metric names a metric this
	// engine cannot rebuild.
	ErrUnknownMetric = errors.New("recompute: unknown metric")
	// ErrLockHeld is returned when the cron rollup, or another recompute, holds
	// the partition lock.
	ErrLockHeld = errors.New("recompute: another rollup or recompute holds the lock")
)

// MetricRow represents a 1-minute bucket for a specific service/path/method tuple.
//
// Field order is part of the reproducibility contract: the Parquet schema is
// derived from this struct, so reordering these fields changes the bytes of every
// output file. Do not reorder them without a schema version bump.
type MetricRow struct {
	TenantID     string  `json:"tenant_id" parquet:"tenant_id"`
	BucketStart  string  `json:"bucket_start" parquet:"bucket_start"`
	Service      string  `json:"service" parquet:"service"`
	Method       string  `json:"method" parquet:"method"`
	PathTemplate string  `json:"path_template" parquet:"path_template"`
	RequestCount int64   `json:"request_count" parquet:"request_count"`
	ErrorCount   int64   `json:"error_count" parquet:"error_count"`
	ErrorRate    float64 `json:"error_rate" parquet:"error_rate"`
	P50LatencyMs float64 `json:"p50_latency_ms" parquet:"p50_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms" parquet:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms" parquet:"p99_latency_ms"`

	// LatencySketch is a serialised quantile sketch over this bucket's latencies,
	// mergeable across buckets. Empty when the bucket has no observations.
	//
	// The scalars above stay: within one bucket they are exact and cheaper to
	// read. The sketch is what makes a correct percentile over many buckets
	// possible at all.
	LatencySketch []byte `json:"latency_sketch" parquet:"latency_sketch"`

	// SketchVersion is sketch.Version at write time, so a reader can refuse a
	// format it does not understand rather than misreading it.
	SketchVersion string `json:"sketch_version" parquet:"sketch_version"`

	EventDay string `json:"event_day" parquet:"event_day"`
}

// AggregationKey identifies one output row before it is materialised.
type AggregationKey struct {
	BucketStart  time.Time
	Service      string
	Method       string
	PathTemplate string
}

// Aggregator accumulates the facts belonging to one AggregationKey.
type Aggregator struct {
	Latencies []float64
	Requests  int64
	Errors    int64
}

// AggregateResult is what reading a day of facts produced.
type AggregateResult struct {
	// Aggregators holds one accumulator per output row.
	Aggregators map[AggregationKey]*Aggregator
	// FactsRead counts the facts accepted into the aggregation: duplicates and
	// facts belonging to another day are not counted.
	FactsRead int64
	// SourceKeys lists, sorted, every fact object actually read. It is the
	// partition's lineage and goes into its manifest.
	SourceKeys []string

	// CrossDayDays lists, sorted and de-duplicated, the other UTC days whose facts
	// were found filed under this day's prefix. A late batch delivered after
	// midnight lands in the arrival day's directory while its facts belong to the
	// previous day; those facts are not dropped and not mis-bucketed — the
	// partitions they belong to are rebuilt as well.
	CrossDayDays []time.Time
}

// Options configures one recompute run.
type Options struct {
	Store     storage.ObjectStore
	InputDir  string // raw facts root, e.g. "./data/raw"
	OutputDir string // warehouse root, e.g. "./data/warehouse"
	// Metric names the metric to rebuild. Only MetricRequestMinute is
	// supported; anything else yields ErrUnknownMetric.
	Metric string
	Window Window
	// TenantIDs is the set of tenants to rebuild. Empty means single-tenant
	// mode with TenantID "".
	TenantIDs []string
	// DryRun plans and reports without writing or deleting anything.
	DryRun bool
	// Concurrency is the number of partitions rebuilt in parallel. 0 means 1.
	Concurrency int
}

// Result reports what a run did.
type Result struct {
	Partitions   int // planned
	Rebuilt      int // written
	Unchanged    int // byte-identical to the existing output, so left alone
	FactsRead    int64
	RowsWritten  int64
	ReplacedKeys []string // object keys replaced, sorted
	// ManifestsAdded counts partitions whose data was already correct but had no
	// manifest beside it. They are reported rather than backfilled quietly.
	ManifestsAdded int
	// Revised counts partitions whose published rows changed in this run. A
	// non-zero value means numbers someone may have already read have moved.
	Revised int
	// ExtraPartitions counts partitions rebuilt because a late fact belonging to
	// them was found filed under a different day. They were not in the plan; they
	// are rebuilt anyway, because otherwise that fact would never reach a metric.
	ExtraPartitions int
	Duration        time.Duration
}

// PartitionOptions configures the rebuild of a single tenant-day. Unlike
// Options, both directories are already resolved down to the tenant.
type PartitionOptions struct {
	Store storage.ObjectStore
	// FactsDir is the fact directory for this tenant, e.g.
	// "./data/raw/request_facts".
	FactsDir string
	// MetricDir is the metric output directory for this tenant, e.g.
	// "./data/warehouse/request_metrics_minute".
	MetricDir string
	Metric    string
	TenantID  string
	Day       time.Time
	DryRun    bool
	// OnFact, when set, is called once per accepted fact. The cron rollup uses
	// it to drive its Prometheus counters without this package importing them.
	OnFact func(service, day string)
	// ExtraFactDays names other day prefixes to scan for facts belonging to this
	// partition. It exists for late batches filed under the day they arrived
	// rather than the day they describe.
	ExtraFactDays []time.Time
}

// PartitionResult reports the rebuild of a single tenant-day.
type PartitionResult struct {
	Key          string
	FactsRead    int64
	RowsWritten  int64
	Written      bool
	Unchanged    bool
	ReplacedKeys []string
	// Manifest is the manifest written beside the data file, or nil when the
	// partition held no facts and was cleared.
	Manifest *manifest.Manifest
	// ManifestAdded is true when the data file was already correct but carried no
	// manifest, so one was written for it.
	ManifestAdded bool
	// Revised is true when this rebuild changed rows that had already been
	// published — a late fact landing in a closed bucket, most often.
	Revised bool
	// CrossDayDays names other days whose facts were found under this partition's
	// prefix. Those partitions need rebuilding too.
	CrossDayDays []time.Time
}

// DeterministicKey returns the output object key for a partition. The same
// partition always maps to the same key, so a recompute replaces its prior
// output instead of adding a second file beside it.
func DeterministicKey(partitionDir, metric string, day time.Time) string {
	return fmt.Sprintf("%s/%s_%s.parquet", partitionDir, metric, day.UTC().Format("20060102"))
}

// KeyPrefix converts a filesystem-style directory into an ObjectStore key prefix
// by stripping the local-store data root. "./data/warehouse", "data/warehouse"
// and "./data/warehouse/" all name the same prefix, "warehouse".
func KeyPrefix(dir string) string {
	cleaned := path.Clean(dir)
	if cleaned == dataRoot {
		return ""
	}
	return strings.TrimPrefix(cleaned, dataRoot+"/")
}

// PartitionDir returns the Hive-partitioned output directory for a day, as an
// ObjectStore key prefix.
func PartitionDir(metricDir string, day time.Time) string {
	return fmt.Sprintf("%s/event_day=%s", KeyPrefix(metricDir), day.UTC().Format("2006-01-02"))
}

// FactsDirFor resolves the raw fact directory for a tenant under inputDir.
func FactsDirFor(inputDir, tenantID string) string {
	if tenantID == "" {
		return path.Join(inputDir, "request_facts")
	}
	return path.Join(inputDir, tenantID, "request_facts")
}

// MetricDirFor resolves the metric output directory for a tenant under outputDir.
func MetricDirFor(outputDir, tenantID, metric string) string {
	if tenantID == "" {
		return path.Join(outputDir, metric)
	}
	return path.Join(outputDir, tenantID, metric)
}

// Run executes a recompute. It is idempotent: running twice over the same window
// with the same facts produces byte-identical output and reports Rebuilt == 0 on
// the second run.
//
// Run holds the same lock the cron rollup uses for the whole run, so the two can
// never write the same partition concurrently. A held lock yields ErrLockHeld.
func Run(ctx context.Context, opts Options) (*Result, error) {
	started := time.Now()

	if opts.Metric == "" {
		opts.Metric = MetricRequestMinute
	}
	if opts.Metric != MetricRequestMinute {
		return nil, fmt.Errorf("%w %q", ErrUnknownMetric, opts.Metric)
	}
	if opts.Store == nil {
		return nil, errors.New("recompute: Options.Store is required")
	}

	partitions, err := Plan(opts.Window, opts.TenantIDs)
	if err != nil {
		return nil, err
	}

	release, err := acquire(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer release()

	result := &Result{Partitions: len(partitions)}
	results := make([]*PartitionResult, len(partitions))
	errs := make([]error, len(partitions))

	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, p := range partitions {
		if ctx.Err() != nil {
			errs[i] = ctx.Err()
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p Partition) {
			defer wg.Done()
			defer func() { <-sem }()
			pr, perr := ProcessPartition(ctx, PartitionOptions{
				Store:     opts.Store,
				FactsDir:  FactsDirFor(opts.InputDir, p.TenantID),
				MetricDir: MetricDirFor(opts.OutputDir, p.TenantID, opts.Metric),
				Metric:    opts.Metric,
				TenantID:  p.TenantID,
				Day:       p.Day,
				DryRun:    opts.DryRun,
			})
			results[i], errs[i] = pr, perr
		}(i, p)
	}
	wg.Wait()

	var failures []error
	for i, p := range partitions {
		if errs[i] != nil {
			failures = append(failures, fmt.Errorf("recompute: partition %s failed: %w", p, errs[i]))
			continue
		}
		pr := results[i]
		result.FactsRead += pr.FactsRead
		result.RowsWritten += pr.RowsWritten
		switch {
		case pr.Unchanged:
			result.Unchanged++
		case pr.Written:
			result.Rebuilt++
		}
		if pr.ManifestAdded {
			result.ManifestsAdded++
		}
		if pr.Revised {
			result.Revised++
		}
		result.ReplacedKeys = append(result.ReplacedKeys, pr.ReplacedKeys...)
	}
	// Facts belonging to another day, found filed under one of the planned days,
	// name partitions that also need rebuilding. Doing this after the planned pass
	// keeps the plan deterministic while still letting a late batch reach the
	// bucket its event_time demands.
	extra, extraErrs := runCrossDayPartitions(ctx, opts, partitions, results, result)
	failures = append(failures, extraErrs...)
	result.ExtraPartitions = extra

	sort.Strings(result.ReplacedKeys)
	result.Duration = time.Since(started)

	if len(failures) > 0 {
		return result, errors.Join(failures...)
	}
	return result, nil
}

// runCrossDayPartitions rebuilds the partitions named by cross-day facts that the
// planned pass turned up, skipping any the plan already covered. It runs one pass
// only: a rebuild of day X can itself surface facts for day Y, but chasing that
// indefinitely would let one badly-filed batch expand a run without bound. Any
// remaining days are picked up by the next run, which is what a batch system is
// allowed to do.
func runCrossDayPartitions(ctx context.Context, opts Options, planned []Partition, results []*PartitionResult, result *Result) (int, []error) {
	inPlan := make(map[string]struct{}, len(planned))
	for _, p := range planned {
		inPlan[p.String()] = struct{}{}
	}

	// Collect the extra partitions, remembering which prefixes their facts were
	// found under — without that, rebuilding the affected day would read only its
	// own directory and miss the very facts that prompted the rebuild.
	extraSet := make(map[string]Partition)
	foundUnder := make(map[string][]time.Time)
	for i, pr := range results {
		if pr == nil {
			continue
		}
		for _, d := range pr.CrossDayDays {
			p := Partition{TenantID: planned[i].TenantID, Day: d}
			if _, ok := inPlan[p.String()]; ok {
				continue
			}
			extraSet[p.String()] = p
			foundUnder[p.String()] = append(foundUnder[p.String()], planned[i].Day)
		}
	}
	if len(extraSet) == 0 {
		return 0, nil
	}

	keys := make([]string, 0, len(extraSet))
	for k := range extraSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var failures []error
	rebuilt := 0
	for _, k := range keys {
		p := extraSet[k]
		pr, err := ProcessPartition(ctx, PartitionOptions{
			Store:         opts.Store,
			FactsDir:      FactsDirFor(opts.InputDir, p.TenantID),
			MetricDir:     MetricDirFor(opts.OutputDir, p.TenantID, opts.Metric),
			Metric:        opts.Metric,
			TenantID:      p.TenantID,
			Day:           p.Day,
			DryRun:        opts.DryRun,
			ExtraFactDays: foundUnder[k],
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("recompute: partition %s failed: %w", p, err))
			continue
		}
		rebuilt++
		result.FactsRead += pr.FactsRead
		result.RowsWritten += pr.RowsWritten
		switch {
		case pr.Unchanged:
			result.Unchanged++
		case pr.Written:
			result.Rebuilt++
		}
		if pr.Revised {
			result.Revised++
		}
		if pr.ManifestAdded {
			result.ManifestsAdded++
		}
		result.ReplacedKeys = append(result.ReplacedKeys, pr.ReplacedKeys...)
	}
	return rebuilt, failures
}

// acquire takes the shared rollup lock for every tenant in the run and returns a
// release function. Locks are taken in sorted tenant order so two concurrent runs
// cannot deadlock against each other.
func acquire(ctx context.Context, opts Options) (func(), error) {
	tenants := normalizeTenants(opts.TenantIDs)
	held := make([]*leaderelect.FileElector, 0, len(tenants))

	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i].Release(context.Background())
		}
	}

	for _, tenant := range tenants {
		dir := MetricDirFor(opts.OutputDir, tenant, opts.Metric)
		elector := leaderelect.NewFileElector(dir, LockName)
		acquired, err := elector.Acquire(ctx)
		if err != nil {
			release()
			return nil, fmt.Errorf("recompute: acquiring lock in %s: %w", dir, err)
		}
		if !acquired {
			release()
			return nil, ErrLockHeld
		}
		held = append(held, elector)
	}
	return release, nil
}

// ProcessPartition rebuilds one tenant-day: it reads the day's facts, aggregates
// them, encodes the Parquet file, and writes it at the deterministic key only if
// the bytes differ from what is already there. A manifest describing the result
// is written beside the data file — always after it, never before, so a manifest
// without its data file stays a detectable inconsistency.
func ProcessPartition(ctx context.Context, o PartitionOptions) (*PartitionResult, error) {
	if o.Metric == "" {
		o.Metric = MetricRequestMinute
	}
	dayStr := o.Day.UTC().Format("2006-01-02")
	metricPrefix := KeyPrefix(o.MetricDir)
	partitionDir := PartitionDir(o.MetricDir, o.Day)
	destKey := DeterministicKey(partitionDir, o.Metric, o.Day)

	agg, err := Aggregate(ctx, o.Store, o.FactsDir, o.Day, o.OnFact, o.ExtraFactDays...)
	if err != nil {
		return nil, err
	}

	res := &PartitionResult{Key: destKey, FactsRead: agg.FactsRead, CrossDayDays: agg.CrossDayDays}

	if len(agg.Aggregators) == 0 {
		// No facts for this day: the partition must end up empty rather than
		// keeping a stale file that would be read as current. The manifest goes
		// with its data file.
		stale, err := staleKeys(ctx, o.Store, metricPrefix, partitionDir, dayStr, "")
		if err != nil {
			return nil, err
		}
		res.ReplacedKeys = stale
		if !o.DryRun {
			if err := deleteWithManifests(ctx, o.Store, stale); err != nil {
				return nil, err
			}
		}
		return res, nil
	}

	rows := BuildRows(agg.Aggregators, o.TenantID, dayStr)
	data, err := EncodeParquet(rows)
	if err != nil {
		return nil, err
	}
	res.RowsWritten = int64(len(rows))

	identical, err := objectMatches(ctx, o.Store, destKey, data)
	if err != nil {
		return nil, err
	}

	stale, err := staleKeys(ctx, o.Store, metricPrefix, partitionDir, dayStr, destKey)
	if err != nil {
		return nil, err
	}
	res.ReplacedKeys = stale

	m, hadManifest, revised, err := buildManifest(ctx, o, rows, agg, destKey, dayStr, identical)
	if err != nil {
		return nil, err
	}
	res.Manifest = m
	res.ManifestAdded = identical && !hadManifest
	res.Revised = revised

	if o.DryRun {
		res.Written = !identical
		res.Unchanged = identical && len(stale) == 0
		return res, nil
	}

	if !identical {
		// Write the new file first, then remove the old ones. If the process
		// dies in between, the partition holds stale data rather than no data.
		if err := o.Store.Put(ctx, destKey, bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("uploading metrics to %s: %w", destKey, err)
		}
		res.Written = true
	}
	// The manifest follows the data file, on both paths: an unchanged partition
	// whose lineage moved — a new fact file that added no rows — still needs its
	// manifest brought up to date.
	if err := manifest.Write(ctx, o.Store, m); err != nil {
		return nil, err
	}
	if err := deleteWithManifests(ctx, o.Store, stale); err != nil {
		return nil, err
	}
	if identical {
		res.Unchanged = true
	}
	return res, nil
}

// buildManifest assembles the manifest for a partition, carrying the revision
// forward from whatever is already stored. It reports whether a manifest was
// already there, which is how a partition that predates manifests is told apart
// from one being written for the first time.
func buildManifest(ctx context.Context, o PartitionOptions, rows []MetricRow, agg *AggregateResult, destKey, dayStr string, identical bool) (*manifest.Manifest, bool, bool, error) {
	digest, err := manifest.ContentDigest(rows)
	if err != nil {
		return nil, false, false, err
	}

	day := o.Day.UTC()
	next := manifest.Manifest{
		SchemaVersion:  manifest.SchemaVersion,
		Metric:         o.Metric,
		MetricVersion:  MetricVersion,
		IdempotencyKey: manifest.IdempotencyKey(o.Metric, MetricVersion, o.TenantID, day),
		ContentDigest:  digest,
		TenantID:       o.TenantID,
		EventDay:       dayStr,
		WindowFrom:     day.Format(time.RFC3339),
		WindowTo:       day.AddDate(0, 0, 1).Format(time.RFC3339),
		RowCount:       int64(len(rows)),
		FactCount:      agg.FactsRead,
		SourceFactKeys: agg.SourceKeys,
		DataFile:       destKey,
	}

	outcome, err := decideRevision(ctx, o.Store, destKey, next)
	if err != nil {
		return nil, false, false, err
	}
	return outcome.Manifest, outcome.HadManifest, outcome.Revised, nil
}

// deleteWithManifests removes data files and the manifests beside them, data
// file first. A manifest left without its data file is detectable; a data file
// left without its manifest is not.
func deleteWithManifests(ctx context.Context, store storage.ObjectStore, keys []string) error {
	for _, k := range keys {
		if err := store.Delete(ctx, k); err != nil {
			return fmt.Errorf("deleting stale object %s: %w", k, err)
		}
		manifestKey := manifest.Path(k)
		exists, err := store.Exists(ctx, manifestKey)
		if err != nil {
			return fmt.Errorf("checking %s: %w", manifestKey, err)
		}
		if !exists {
			continue
		}
		if err := store.Delete(ctx, manifestKey); err != nil {
			return fmt.Errorf("deleting stale manifest %s: %w", manifestKey, err)
		}
	}
	return nil
}

// staleKeys lists every object in the partition that is not keepKey, plus any
// legacy flat-layout file for the same day from before Hive partitioning. Passing
// an empty keepKey means every object in the partition is stale.
func staleKeys(ctx context.Context, store storage.ObjectStore, metricPrefix, partitionDir, dayStr, keepKey string) ([]string, error) {
	var stale []string

	existing, err := store.List(ctx, partitionDir)
	if err != nil {
		return nil, fmt.Errorf("listing partition %s: %w", partitionDir, err)
	}
	for _, k := range existing {
		if k == keepKey || strings.HasSuffix(k, manifest.Extension) {
			// A manifest is removed with its data file, never on its own.
			continue
		}
		stale = append(stale, k)
	}

	legacy, err := store.List(ctx, metricPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing metric prefix %s: %w", metricPrefix, err)
	}
	for _, k := range legacy {
		if strings.HasSuffix(k, manifest.Extension) {
			continue
		}
		if strings.Contains(k, dayStr) && !strings.Contains(k, "event_day=") {
			stale = append(stale, k)
		}
	}

	sort.Strings(stale)
	return stale, nil
}

// objectMatches reports whether the object at key already holds exactly data.
func objectMatches(ctx context.Context, store storage.ObjectStore, key string, data []byte) (bool, error) {
	exists, err := store.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("checking %s: %w", key, err)
	}
	if !exists {
		return false, nil
	}
	rc, err := store.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", key, err)
	}
	defer rc.Close()
	current, err := io.ReadAll(rc)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", key, err)
	}
	return bytes.Equal(current, data), nil
}

// Aggregate reads every JSONL fact file for a day and folds the facts into
// per-minute aggregators. Facts are de-duplicated by event ID across the whole
// day, and facts whose event time falls on another day are discarded.
//
// onFact, when non-nil, is called once per accepted fact.
func Aggregate(ctx context.Context, store storage.ObjectStore, factsDir string, day time.Time, onFact func(service, day string), extraDays ...time.Time) (*AggregateResult, error) {
	dayStr := day.UTC().Format("2006-01-02")

	res := &AggregateResult{Aggregators: make(map[AggregationKey]*Aggregator)}
	aggs := res.Aggregators
	seen := make(map[string]struct{})
	crossDay := make(map[time.Time]struct{})

	// This day's own prefix, plus any prefix a caller knows holds facts belonging
	// to this day. A batch delivered after midnight is filed under the day it
	// arrived, not the day it describes, so rebuilding this partition from its own
	// directory alone would silently miss those facts.
	prefixes := []string{fmt.Sprintf("%s/%s", KeyPrefix(factsDir), dayStr)}
	for _, d := range extraDays {
		extra := fmt.Sprintf("%s/%s", KeyPrefix(factsDir), d.UTC().Format("2006-01-02"))
		if extra != prefixes[0] {
			prefixes = append(prefixes, extra)
		}
	}
	sort.Strings(prefixes)

	var keys []string
	for _, prefix := range prefixes {
		found, err := store.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("list error: %w", err)
		}
		keys = append(keys, found...)
	}
	// The object store gives no ordering guarantee; read in a fixed order so a
	// rebuild sees the same facts in the same sequence every time.
	sort.Strings(keys)

	for _, key := range keys {
		if !strings.HasSuffix(key, ".jsonl") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		rc, err := store.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", key, err)
		}
		res.SourceKeys = append(res.SourceKeys, key)

		scanner := bufio.NewScanner(rc)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			fact, err := schemas.ParseRequestFact(line)
			if err != nil {
				continue // invalid line: not a fact, so not counted
			}

			if _, exists := seen[fact.EventId]; exists {
				continue
			}
			seen[fact.EventId] = struct{}{}

			eventTime := fact.EventTime.AsTime()
			if eventTime.UTC().Format("2006-01-02") != dayStr {
				// The fact belongs to another day. docs/00-system-truth.md §5 makes
				// event_time the only source of truth for ordering, so it must not
				// be folded into this partition. Record the day it does belong to
				// so the caller can rebuild that partition rather than lose the
				// fact to a directory it was merely filed under.
				crossDay[lateness.AffectedDay(eventTime)] = struct{}{}
				continue
			}

			bucket := eventTime.Truncate(time.Minute).UTC()
			keyAgg := AggregationKey{
				BucketStart:  bucket,
				Service:      fact.Service,
				Method:       fact.Method,
				PathTemplate: fact.PathTemplate,
			}
			agg, exists := aggs[keyAgg]
			if !exists {
				agg = &Aggregator{}
				aggs[keyAgg] = agg
			}
			agg.Requests++
			if fact.StatusCode >= 500 {
				agg.Errors++
			}
			agg.Latencies = append(agg.Latencies, float64(fact.LatencyMs))
			res.FactsRead++

			if onFact != nil {
				onFact(fact.Service, dayStr)
			}
		}
		scanErr := scanner.Err()
		rc.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("scanning %s: %w", key, scanErr)
		}
	}

	res.CrossDayDays = sortedDays(crossDay)

	// keys came back sorted, so SourceKeys is already in a stable order.
	return res, nil
}

// sortedDays flattens a day set into ascending order, so a run's report and any
// follow-up rebuilds are deterministic.
func sortedDays(set map[time.Time]struct{}) []time.Time {
	if len(set) == 0 {
		return nil
	}
	out := make([]time.Time, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// BuildRows turns aggregators into output rows, sorted into a total order.
//
// Each aggregator's latencies are sorted before the percentiles are taken. The
// facts of a day arrive in whatever order the object store lists them, and an
// unsorted input would let two rebuilds of the same day differ in the last bits
// of a float64. Sorting first removes read order from the result.
func BuildRows(aggs map[AggregationKey]*Aggregator, tenantID, dayStr string) []MetricRow {
	rows := make([]MetricRow, 0, len(aggs))
	var sketchVersion string
	for key, agg := range aggs {
		sort.Float64s(agg.Latencies)

		p50, _ := stats.Percentile(agg.Latencies, 50)
		p95, _ := stats.Percentile(agg.Latencies, 95)
		p99, _ := stats.Percentile(agg.Latencies, 99)

		rate := 0.0
		if agg.Requests > 0 {
			rate = float64(agg.Errors) / float64(agg.Requests)
		}

		// The latencies are already sorted, which is what makes the sketch's
		// serialised bytes depend only on the multiset and not on the order the
		// facts were read in.
		sketchBytes, err := sketch.FromSorted(agg.Latencies).MarshalBinary()
		if err != nil {
			// MarshalBinary on a fixed-layout buffer cannot fail; a nil sketch
			// would silently lose the bucket, so record the version as empty and
			// let the reader refuse it rather than misread it.
			sketchBytes, sketchVersion = nil, ""
		} else {
			sketchVersion = sketch.Version
		}

		rows = append(rows, MetricRow{
			TenantID:      tenantID,
			BucketStart:   key.BucketStart.Format("2006-01-02 15:04:05"),
			Service:       key.Service,
			Method:        key.Method,
			PathTemplate:  key.PathTemplate,
			RequestCount:  agg.Requests,
			EventDay:      dayStr,
			ErrorCount:    agg.Errors,
			ErrorRate:     rate,
			P50LatencyMs:  p50,
			P95LatencyMs:  p95,
			P99LatencyMs:  p99,
			LatencySketch: sketchBytes,
			SketchVersion: sketchVersion,
		})
	}

	SortRows(rows)
	return rows
}

// SortRows orders rows by the full aggregation key: bucket, service, method,
// then path template. Sorting on fewer fields leaves ties in map iteration
// order, which differs between runs.
func SortRows(rows []MetricRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.BucketStart != b.BucketStart {
			return a.BucketStart < b.BucketStart
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.PathTemplate != b.PathTemplate {
			return a.PathTemplate < b.PathTemplate
		}
		// Two rows that agree on the whole aggregation key cannot occur from one
		// aggregation, but comparing the sketch bytes last keeps the order total
		// rather than relying on that.
		return bytes.Compare(a.LatencySketch, b.LatencySketch) < 0
	})
}

// EncodeParquet encodes rows into a Parquet file. The encoding carries no
// timestamp, hostname, or run identifier, so identical rows produce identical
// bytes.
func EncodeParquet(rows []MetricRow) ([]byte, error) {
	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[MetricRow](&buf, parquet.Compression(&zstd.Codec{Level: CompressionLevel}))
	if _, err := writer.Write(rows); err != nil {
		return nil, fmt.Errorf("writing parquet rows: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing parquet writer: %w", err)
	}
	return buf.Bytes(), nil
}
