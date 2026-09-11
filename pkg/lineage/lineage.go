// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package lineage assembles the provenance of a derived metric value: the
// contract that defines it, the facts it came from, and how to reproduce it.
//
// No competitor can answer this, because none of them kept the facts. Prometheus
// discarded the raw observation at scrape time; a hosted platform never stored the
// request. What is left in both cases is a number with no way back to its cause.
//
// Two rules shape everything here:
//
//   - Provenance is aggregate. docs/04-non-goals.md §5 forbids per-request
//     querying, so lineage names the fact *files* a partition was built from and
//     how many facts they held. It never returns a fact record, and no output path
//     is capable of it: the manifest is the only source, and the manifest holds
//     keys and counts, not contents.
//   - A gap is reported, never filled. A partition written before manifests
//     existed has no lineage, and saying so is the only honest answer. Inferring a
//     plausible one would defeat the point of a feature whose whole value is that
//     its output can be trusted.
package lineage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/metriccontract"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
)

var (
	// ErrNoPartition is returned when no partition covers the requested bucket.
	ErrNoPartition = errors.New("lineage: no partition covers that bucket")
	// ErrNoManifest is returned for a partition written before manifests existed.
	// It is a first-class answer, not a failure: the data is fine, its provenance
	// simply was not recorded, and that is recoverable by a recompute.
	ErrNoManifest = errors.New("lineage: partition has no manifest; lineage is unavailable")
	// ErrNoContract is returned when no contract defines the partition's version.
	ErrNoContract = errors.New("lineage: no contract for that metric version")
	// ErrNoRowMatch is returned when the bucket exists but no row matches.
	ErrNoRowMatch = errors.New("lineage: no row matches those filters in that bucket")
	// ErrNotADimension is returned for a filter on a field that is not a dimension.
	ErrNotADimension = errors.New("lineage: field is not a dimension")
)

// Options configures lineage lookups.
type Options struct {
	Store        storage.ObjectStore
	WarehouseDir string // e.g. "./data/warehouse"
	ContractsDir string // e.g. "contracts"
}

// Query identifies one metric value.
type Query struct {
	// Metric is the rollup name, e.g. "request_metrics_minute", or one of its
	// contract names, e.g. "latency_p95". Naming a contract focuses the report on
	// that metric's definition; naming the rollup reports the weakest guarantee
	// present, because that is the one a reader most needs to know about.
	Metric string
	// Bucket is the bucket_start being explained.
	Bucket time.Time
	// TenantID is the owning tenant, empty for single-tenant.
	TenantID string
	// Filters are dimension equality filters, e.g. {"service": "api"}.
	Filters map[string]string
}

// Revision is one prior state of a partition.
type Revision struct {
	Number    int    `json:"number"`
	Digest    string `json:"digest"`
	RevisedAt string `json:"revised_at"`
}

// Lineage is the full provenance of a value.
type Lineage struct {
	Metric        string `json:"metric"`
	MetricVersion string `json:"metric_version"`
	// ContractRef names the contract whose formula, exactness and mergeability are
	// reported. It differs from Metric when a rollup was asked about: a rollup is
	// six metrics, so one of their contracts has to be the one described, and the
	// reader is entitled to know which.
	ContractRef     string            `json:"contract_ref"`
	Bucket          string            `json:"bucket"`
	Filters         map[string]string `json:"filters,omitempty"`
	Values          map[string]any    `json:"values"`
	Formula         string            `json:"formula"`
	Grain           string            `json:"grain"`
	Exactness       string            `json:"exactness"`
	ErrorBound      string            `json:"error_bound"`
	Mergeability    string            `json:"mergeability"`
	MergeNote       string            `json:"merge_note,omitempty"`
	KnownDefect     string            `json:"known_defect,omitempty"`
	DataFile        string            `json:"data_file"`
	IdempotencyKey  string            `json:"idempotency_key"`
	ContentDigest   string            `json:"content_digest"`
	SourceFactKeys  []string          `json:"source_fact_keys"`
	FactCount       int64             `json:"fact_count"`
	CurrentRevision int               `json:"current_revision"`
	RevisionHistory []Revision        `json:"revision_history"`
	RecomputeCmd    string            `json:"recompute_cmd"`
}

// MissingManifest describes a partition whose provenance was never recorded.
type MissingManifest struct {
	DataFile     string
	Metric       string
	Day          time.Time
	RecomputeCmd string
}

// Error makes MissingManifest usable as the ErrNoManifest case while carrying
// everything the caller needs to tell the user how to fix it.
func (m *MissingManifest) Error() string {
	return "lineage unavailable: this partition was written before manifests existed"
}

func (m *MissingManifest) Unwrap() error { return ErrNoManifest }

// Explain assembles the lineage for a query. It reads manifests, contracts and
// one Parquet row; it writes nothing and recomputes nothing.
func Explain(ctx context.Context, opts Options, q Query) (*Lineage, error) {
	if q.Metric == "" {
		q.Metric = recompute.MetricRequestMinute
	}
	rollup := recompute.MetricRequestMinute
	day := utcDay(q.Bucket)

	metricDir := recompute.MetricDirFor(opts.WarehouseDir, q.TenantID, rollup)
	dataKey := recompute.DeterministicKey(recompute.PartitionDir(metricDir, day), rollup, day)

	exists, err := opts.Store.Exists(ctx, dataKey)
	if err != nil {
		return nil, fmt.Errorf("lineage: checking %s: %w", dataKey, err)
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrNoPartition, q.Bucket.UTC().Format(time.RFC3339))
	}

	m, err := manifest.Read(ctx, opts.Store, dataKey)
	if errors.Is(err, manifest.ErrNoManifest) {
		return nil, &MissingManifest{
			DataFile:     dataKey,
			Metric:       rollup,
			Day:          day,
			RecomputeCmd: recomputeCommand(rollup, day),
		}
	}
	if err != nil {
		return nil, err
	}

	// LoadDirOrEmbedded, not Load: contracts are source and ship with the binary,
	// and reading them only from a directory relative to the working directory
	// meant `gravix explain` answered "no contract for that metric version" for
	// anyone running it outside the repository. A provenance command that only
	// works in the source tree is not a provenance command. See F-008.
	//
	// One line, in a file GRVX-812 §4.3 fences off. Recorded as a deviation
	// rather than routed around, because the alternative was having the CLI
	// materialise the embedded contracts into a temp directory to avoid touching
	// this line, which is worse code written to respect the letter of a fence.
	registry, err := metriccontract.LoadDirOrEmbedded(opts.ContractsDir)
	if err != nil {
		return nil, fmt.Errorf("lineage: loading contracts: %w", err)
	}

	contract, err := resolveContract(registry, q.Metric, m.MetricVersion)
	if err != nil {
		return nil, err
	}

	if err := validateFilters(q.Filters, contract); err != nil {
		return nil, err
	}

	row, err := findRow(ctx, opts, dataKey, q)
	if err != nil {
		return nil, err
	}

	// Report the metric the caller asked about, not the contract that happened to
	// be selected — answering "request_metrics_minute" with "latency_p50" reads
	// like a different question was answered.
	reportedMetric, reportedVersion := q.Metric, m.MetricVersion
	if q.Metric != rollup {
		reportedMetric, reportedVersion = contract.Name, contract.Version
	}

	return &Lineage{
		Metric:          reportedMetric,
		MetricVersion:   reportedVersion,
		ContractRef:     contract.Name + "@" + contract.Version,
		Bucket:          q.Bucket.UTC().Format(time.RFC3339),
		Filters:         q.Filters,
		Values:          rowValues(row),
		Formula:         contract.Formula,
		Grain:           contract.Grain,
		Exactness:       string(contract.Exactness),
		ErrorBound:      contract.ErrorBound,
		Mergeability:    string(contract.Mergeability),
		MergeNote:       collapse(contract.MergeNote),
		KnownDefect:     strings.TrimSpace(contract.KnownDefect),
		DataFile:        dataKey,
		IdempotencyKey:  m.IdempotencyKey,
		ContentDigest:   m.ContentDigest,
		SourceFactKeys:  m.SourceFactKeys,
		FactCount:       m.FactCount,
		CurrentRevision: m.Revision,
		RevisionHistory: revisionHistory(m),
		RecomputeCmd:    recomputeCommand(rollup, day),
	}, nil
}

// resolveContract picks the contract to report.
//
// A named contract is reported as asked. The rollup name has no single contract —
// it is six of them — so the weakest guarantee present is reported instead: an
// approximate metric outranks a sketch, which outranks an exact one. A reader
// looking at a row wants to know the worst thing true about it, not the best.
func resolveContract(r *metriccontract.Registry, metric, version string) (*metriccontract.Contract, error) {
	if metric != recompute.MetricRequestMinute {
		if c, err := r.Get(metric + "@" + version); err == nil {
			return c, nil
		}
		c, err := r.Latest(metric)
		if err != nil {
			return nil, fmt.Errorf("%w: %s@%s", ErrNoContract, metric, version)
		}
		return c, nil
	}

	var weakest *metriccontract.Contract
	for _, name := range r.Names() {
		c, err := r.Latest(name)
		if err != nil {
			continue
		}
		if weakest == nil || exactnessRank(c.Exactness) > exactnessRank(weakest.Exactness) {
			weakest = c
		}
	}
	if weakest == nil {
		return nil, fmt.Errorf("%w: %s@%s", ErrNoContract, metric, version)
	}
	return weakest, nil
}

// exactnessRank orders guarantees from strongest to weakest.
func exactnessRank(e metriccontract.Exactness) int {
	switch e {
	case metriccontract.ExactnessExact:
		return 0
	case metriccontract.ExactnessSketch:
		return 1
	case metriccontract.ExactnessApproximate:
		return 2
	default:
		return 3
	}
}

// validateFilters refuses a filter on anything that is not a declared dimension.
//
// This is what stops explain becoming an ad-hoc per-request query interface. A
// filter on event_id would be exactly the drill-down docs/04-non-goals.md §5
// forbids, and the guard is a whitelist rather than a blacklist so a new fact
// field cannot quietly become queryable.
func validateFilters(filters map[string]string, c *metriccontract.Contract) error {
	if len(filters) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(c.Dimensions))
	for _, d := range c.Dimensions {
		allowed[d] = struct{}{}
	}

	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("%w: %q is not a dimension of %s@%s", ErrNotADimension, k, c.Name, c.Version)
		}
	}
	return nil
}

// findRow reads the partition and returns the row matching the bucket and filters.
func findRow(ctx context.Context, opts Options, dataKey string, q Query) (*recompute.MetricRow, error) {
	rc, err := opts.Store.Get(ctx, dataKey)
	if err != nil {
		return nil, fmt.Errorf("lineage: reading %s: %w", dataKey, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("lineage: reading %s: %w", dataKey, err)
	}
	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("lineage: opening %s: %w", dataKey, err)
	}

	reader := parquet.NewGenericReader[recompute.MetricRow](file)
	defer reader.Close()
	rows := make([]recompute.MetricRow, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("lineage: reading rows from %s: %w", dataKey, err)
	}

	bucket := q.Bucket.UTC().Format("2006-01-02 15:04:05")
	for i := range rows[:n] {
		if rows[i].BucketStart != bucket {
			continue
		}
		if matchesFilters(&rows[i], q.Filters) {
			return &rows[i], nil
		}
	}
	return nil, ErrNoRowMatch
}

// matchesFilters reports whether a row satisfies every dimension filter.
func matchesFilters(row *recompute.MetricRow, filters map[string]string) bool {
	for k, want := range filters {
		if dimensionOf(row, k) != want {
			return false
		}
	}
	return true
}

// dimensionOf reads a dimension from a row. Only declared dimensions reach here,
// because validateFilters ran first.
func dimensionOf(row *recompute.MetricRow, field string) string {
	switch field {
	case "tenant_id":
		return row.TenantID
	case "service":
		return row.Service
	case "method":
		return row.Method
	case "path_template":
		return row.PathTemplate
	case "user_agent_family":
		return row.UserAgentFamily
	default:
		return ""
	}
}

// rowValues renders a row's measures.
//
// Only measures and dimensions appear. The latency sketch is deliberately absent:
// it is an implementation detail of how a percentile is computed, not a value
// anyone asked to be explained, and printing a kilobyte of binary would bury the
// answer.
func rowValues(row *recompute.MetricRow) map[string]any {
	values := map[string]any{
		"request_count":  row.RequestCount,
		"error_count":    row.ErrorCount,
		"error_rate":     row.ErrorRate,
		"p50_latency_ms": row.P50LatencyMs,
		"p95_latency_ms": row.P95LatencyMs,
		"p99_latency_ms": row.P99LatencyMs,
		"service":        row.Service,
		"method":         row.Method,
		"path_template":  row.PathTemplate,
	}
	if row.UserAgentFamily != "" {
		values["user_agent_family"] = row.UserAgentFamily
	}
	if row.ExtraQuantileLabel != "" {
		values[row.ExtraQuantileLabel+"_latency_ms"] = row.ExtraQuantileMs
	}
	return values
}

// revisionHistory builds what the manifest actually knows.
//
// A manifest records one prior digest, so the history is one entry deep. It is
// left empty at revision 0, and the renderer must not imply there is more: a
// partition revised five times can show that it is at revision 5 and what it
// superseded, but not the three states before that.
func revisionHistory(m *manifest.Manifest) []Revision {
	if m.Revision == 0 || m.PreviousDigest == "" {
		return nil
	}
	return []Revision{{
		Number:    m.Revision - 1,
		Digest:    m.PreviousDigest,
		RevisedAt: m.RevisedAt,
	}}
}

// recomputeCommand renders the command that rebuilds a partition.
func recomputeCommand(metric string, day time.Time) string {
	return fmt.Sprintf("gravix recompute --metric %s --from %s --to %s",
		metric, day.Format("2006-01-02"), day.AddDate(0, 0, 1).Format("2006-01-02"))
}

func utcDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func collapse(v string) string { return strings.Join(strings.Fields(v), " ") }
