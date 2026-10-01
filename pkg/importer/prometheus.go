// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// prometheusReader reads the text `promtool tsdb dump` writes, not a TSDB block
// directory (SD-030, DD-028). Reading a block means either implementing
// Prometheus's index and chunk formats or depending on prometheus/prometheus,
// which brings 296 modules and is the dependency GRVX-1102 §3 refused. promtool
// ships with every Prometheus, so a migrating user already has it:
//
//	promtool tsdb dump /path/to/prometheus/data > prometheus-dump.txt
//
// Each line is a label set, a value and a millisecond timestamp, exactly as
// cmd/promtool formats them:
//
//	{__name__="http_requests_total", job="checkout", method="GET"} 4201 1700000000000
//
// Every Prometheus series is an aggregate, so the reader marks every series
// aggregated and facts mode is always refused for this source.
type prometheusReader struct{}

func (prometheusReader) Name() Source { return SourcePrometheus }

// Skip reasons this reader reports. They are counted in the import report
// rather than dropped silently.
const (
	skipNotACounter     = "not a counter: only _total and _count series are imported, as per-minute request counts"
	skipNativeHistogram = "native histogram sample: only float samples are imported"
	skipNoMetricName    = "no __name__ label"
)

type promSample struct {
	ts    int64
	value float64
}

type promSeries struct {
	labels  map[string]string
	samples []promSample
	skip    string
}

func (r prometheusReader) Read(input string) ([]series, error) {
	f, err := os.Open(input)
	if err != nil {
		return nil, fmt.Errorf("importer: read prometheus dump %s: %w", input, err)
	}
	defer f.Close()

	byKey := map[string]*promSeries{}
	var order []string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		labels, rest, err := parsePromLabels(line)
		if err != nil {
			return nil, fmt.Errorf("importer: prometheus dump %s line %d: %w", input, lineNo, err)
		}
		key := line[:len(line)-len(rest)]
		ps, ok := byKey[key]
		if !ok {
			ps = &promSeries{labels: labels}
			byKey[key] = ps
			order = append(order, key)
		}

		fields := strings.Fields(rest)
		if len(fields) < 2 {
			return nil, fmt.Errorf("importer: prometheus dump %s line %d: want a value and a timestamp after the labels", input, lineNo)
		}
		ts, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("importer: prometheus dump %s line %d: timestamp %q: %w", input, lineNo, fields[len(fields)-1], err)
		}
		// A native histogram prints a structure, not a number, between the
		// labels and the timestamp.
		value, err := strconv.ParseFloat(strings.Join(fields[:len(fields)-1], " "), 64)
		if err != nil {
			ps.skip = skipNativeHistogram
			continue
		}
		ps.samples = append(ps.samples, promSample{ts: ts, value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("importer: read prometheus dump %s: %w", input, err)
	}

	out := make([]series, 0, len(order))
	for _, key := range order {
		ps := byKey[key]
		name := ps.labels["__name__"]
		delete(ps.labels, "__name__")

		s := series{Metric: name, Labels: ps.labels, Aggregated: true}
		switch {
		case ps.skip != "":
			s.Skip = ps.skip
		case name == "":
			s.Skip = skipNoMetricName
		case !strings.HasSuffix(name, "_total") && !strings.HasSuffix(name, "_count"):
			s.Skip = skipNotACounter
		default:
			s.Samples = perMinuteIncreases(ps.samples)
		}
		out = append(out, s)
	}
	return out, nil
}

// perMinuteIncreases turns a counter's cumulative samples into one sample per
// minute holding how much the counter rose in that minute. A counter that goes
// down has reset, so its new value is the increase since the reset. The first
// sample has nothing before it to rise from and contributes nothing. Minutes in
// which the counter did not move produce no sample, as a minute with no facts
// produces no row in a native rollup.
func perMinuteIncreases(samples []promSample) []sample {
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].ts < samples[j].ts })

	byMinute := map[int64]float64{}
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1].value, samples[i].value
		inc := cur - prev
		if cur < prev {
			inc = cur
		}
		if inc <= 0 {
			continue
		}
		minute := time.UnixMilli(samples[i].ts).UTC().Truncate(time.Minute).UnixMilli()
		byMinute[minute] += inc
	}

	out := make([]sample, 0, len(byMinute))
	for minute, inc := range byMinute {
		out = append(out, sample{TimeMillis: minute, Value: inc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TimeMillis < out[j].TimeMillis })
	return out
}

// parsePromLabels parses the leading `{name="value", ...}` of a dump line and
// returns the labels and the rest of the line. Values are Go-quoted, as
// labels.Labels.String formats them, so they may contain commas, braces and
// escaped quotes.
func parsePromLabels(line string) (map[string]string, string, error) {
	if !strings.HasPrefix(line, "{") {
		return nil, "", fmt.Errorf("want a line starting with a {label set}")
	}
	labels := map[string]string{}
	s := line[1:]
	for {
		s = strings.TrimLeft(s, " ")
		if strings.HasPrefix(s, "}") {
			return labels, s[1:], nil
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, "", fmt.Errorf("malformed label set")
		}
		name := strings.TrimSpace(s[:eq])
		quoted, err := strconv.QuotedPrefix(s[eq+1:])
		if err != nil {
			return nil, "", fmt.Errorf("label %s: value is not a quoted string", name)
		}
		value, err := strconv.Unquote(quoted)
		if err != nil {
			return nil, "", fmt.Errorf("label %s: %w", name, err)
		}
		labels[name] = value
		s = s[eq+1+len(quoted):]
		s = strings.TrimLeft(s, " ")
		if strings.HasPrefix(s, ",") {
			s = s[1:]
			continue
		}
		if !strings.HasPrefix(s, "}") {
			return nil, "", fmt.Errorf("malformed label set after label %s", name)
		}
	}
}
