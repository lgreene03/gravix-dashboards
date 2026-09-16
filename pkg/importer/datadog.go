// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// datadogReader reads a Datadog metric export.
//
// The export is JSON, which is why this reader needs no dependency and no
// running Datadog account: it opens a file the user already has.
type datadogReader struct{}

func (datadogReader) Name() Source { return SourceDatadog }

// datadogExport mirrors the shape of a Datadog metric query export: a series
// list, each with a metric name, a tag list, a point list, and the aggregation
// Datadog applied.
type datadogExport struct {
	Series []datadogSeries `json:"series"`
}

type datadogSeries struct {
	Metric string `json:"metric"`
	// Tags are Datadog's "key:value" strings.
	Tags []string `json:"tags"`
	// Points are [timestamp_seconds, value] pairs.
	Points [][2]float64 `json:"pointlist"`
	// Aggr is the aggregation Datadog applied — "avg", "sum", "max" and so on.
	// A non-empty value is the source telling us the series is pre-aggregated,
	// which is the whole basis on which facts mode is refused.
	Aggr string `json:"aggr"`
	// Interval is the rollup interval in seconds. Any value above zero means
	// Datadog bucketed the data before exporting it.
	Interval int `json:"interval"`
}

func (r datadogReader) Read(input string) ([]series, error) {
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, fmt.Errorf("importer: read datadog export %s: %w", input, err)
	}

	var export datadogExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("importer: parse datadog export %s: %w", input, err)
	}

	out := make([]series, 0, len(export.Series))
	for _, s := range export.Series {
		if s.Metric == "" {
			continue
		}

		labels := make(map[string]string, len(s.Tags))
		for _, tag := range s.Tags {
			key, value, found := strings.Cut(tag, ":")
			if !found || key == "" {
				continue
			}
			labels[key] = value
		}

		samples := make([]sample, 0, len(s.Points))
		for _, p := range s.Points {
			// Datadog timestamps are milliseconds in the API response and
			// seconds in some exports. Values below this threshold cannot be a
			// millisecond timestamp in any year this software will see, so
			// they are seconds.
			ts := int64(p[0])
			if ts < 1e11 {
				ts *= 1000
			}
			samples = append(samples, sample{TimeMillis: ts, Value: p[1]})
		}

		out = append(out, series{
			Metric: s.Metric,
			Labels: labels,
			// Datadog says so itself, in two independent ways. Trusting the
			// source's own declaration is more reliable than inferring
			// aggregation from the values, and §10 says that when in doubt,
			// declare the data derived.
			Aggregated: s.Aggr != "" || s.Interval > 0,
			Samples:    samples,
		})
	}

	return out, nil
}
