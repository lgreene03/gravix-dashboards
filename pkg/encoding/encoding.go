// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package encoding selects Parquet column encodings for Gravix metric rows.
//
// Encodings are pinned rather than left to the writer's defaults, because a
// library upgrade changing a default would change every file's size without any
// Gravix change, making a regression untraceable. That is the same reasoning
// behind pkg/recompute.CompressionLevel, and for the same reason: a number that
// moves on its own cannot be a baseline.
//
// GRVX-1003 §5.2. This package deliberately imports nothing from the writers
// that use it, so the rollup, recompute and compaction paths can all take their
// encodings from one place without an import cycle.
package encoding

// Encoding names, as Parquet defines them.
const (
	// Dict is dictionary encoding, for a column whose distinct values are few
	// relative to its rows.
	Dict = "dict"
	// RLE is run-length encoding, for small integers that repeat.
	RLE = "rle"
	// Delta stores differences rather than values, for a column that is
	// monotonic within a partition.
	Delta = "delta"
	// Plain is no encoding, for bytes that are already compressed.
	Plain = "plain"
	// ByteStreamSplit separates a float's mantissa bytes into streams that
	// compress far better than the interleaved original.
	ByteStreamSplit = "byte_stream_split"
)

// ColumnEncoding pairs a column with the encoding it must use.
type ColumnEncoding struct {
	Column   string
	Encoding string
}

// metricRowEncodings is the pinned assignment for every MetricRow column.
//
// GRVX-1003 §5.2's table covers 14 columns. MetricRow has 17. The three it does
// not name were added after the spec was written, by the Evolution work that
// lets a dimension or a percentile be introduced retroactively, and they are
// assigned here by the same reasoning §5.2 applies to their neighbours:
//
//   - user_agent_family — Dict, as for service and method: a bounded set of
//     low-cardinality strings.
//   - extra_quantile_label — Dict, as for sketch_version: one value per
//     partition, empty when no percentile was added.
//   - extra_quantile_ms — ByteStreamSplit, as for every other latency float.
//
// §2's "MetricRow has 12 fields plus the two added by GRVX-804" is stale for the
// same reason: that count reaches 14, and the struct now has 17. Recorded as
// SD-055. TestEveryMetricRowColumnIsPinned reads the struct reflectively, so the
// next column added fails this package rather than silently taking whatever
// encoding the library defaults to.
var metricRowEncodings = []ColumnEncoding{
	{"tenant_id", Dict},                 // very low cardinality
	{"bucket_start", Delta},             // monotonic within a partition
	{"service", Dict},                   // bounded by non-goal §5
	{"method", Dict},                    // at most ~8 values
	{"path_template", Dict},             // bounded at <1000/day
	{"request_count", RLE},              // small integers, many repeats
	{"error_count", RLE},                // same
	{"error_rate", ByteStreamSplit},     // float
	{"p50_latency_ms", ByteStreamSplit}, // float
	{"p95_latency_ms", ByteStreamSplit}, // float
	{"p99_latency_ms", ByteStreamSplit}, // float
	{"latency_sketch", Plain},           // already-compressed binary
	{"sketch_version", Dict},            // one value per partition
	{"event_day", Dict},                 // one value per partition

	// Not named by §5.2; see the note above.
	{"user_agent_family", Dict},
	{"extra_quantile_label", Dict},
	{"extra_quantile_ms", ByteStreamSplit},
}

// MetricRowEncodings returns the pinned encoding for every MetricRow column.
//
// The slice is copied, because a caller mutating the pinned table would defeat
// the point of pinning it.
func MetricRowEncodings() []ColumnEncoding {
	out := make([]ColumnEncoding, len(metricRowEncodings))
	copy(out, metricRowEncodings)
	return out
}

// EncodingFor returns the pinned encoding for a column, and whether one exists.
func EncodingFor(column string) (string, bool) {
	for _, ce := range metricRowEncodings {
		if ce.Column == column {
			return ce.Encoding, true
		}
	}
	return "", false
}
