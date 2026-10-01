// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeDump writes text in `promtool tsdb dump` format and returns its path.
func writeDump(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dump.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 2026-01-01T00:00:00Z in milliseconds, and offsets from it.
const t0 = int64(1767225600000)

func line(labels string, value string, offsetSeconds int64) string {
	return labels + " " + value + " " + strconv.FormatInt(t0+offsetSeconds*1000, 10) + "\n"
}

// TestPrometheusLabelsParseAsPromtoolWritesThem: promtool quotes values with
// strconv.Quote, so a value can hold a comma, a brace or an escaped quote.
func TestPrometheusLabelsParseAsPromtoolWritesThem(t *testing.T) {
	labels, rest, err := parsePromLabels(`{__name__="http_requests_total", path="/a,b}", quote="say \"hi\""} 5 1700000000000`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"__name__": "http_requests_total", "path": "/a,b}", "quote": `say "hi"`}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
	if strings.TrimSpace(rest) != "5 1700000000000" {
		t.Errorf("rest = %q", rest)
	}

	for _, bad := range []string{`no braces 1 2`, `{name=unquoted} 1 2`, `{name="x" 1 2`} {
		if _, _, err := parsePromLabels(bad); err == nil {
			t.Errorf("parsed %q; want an error", bad)
		}
	}
}

// TestPrometheusCountersBecomePerMinuteIncreases is the heart of the reader. A
// counter is cumulative, so its raw value is never a per-minute request count.
// Scrapes every 15 seconds collapse into one increase per minute, a reset
// counts the new value as the increase, and the first sample adds nothing.
func TestPrometheusCountersBecomePerMinuteIncreases(t *testing.T) {
	lbl := `{__name__="http_requests_total", job="checkout"}`
	body := line(lbl, "100", 0) + // first sample: no increase
		line(lbl, "110", 15) + line(lbl, "130", 30) + line(lbl, "160", 45) + // minute 0: +60
		line(lbl, "200", 60) + line(lbl, "5", 75) + line(lbl, "25", 90) + // minute 1: +40, reset to 5 (+5), +20
		line(lbl, "25", 120) // minute 2: no change, no sample

	got, err := prometheusReader{}.Read(writeDump(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d series, want 1", len(got))
	}
	s := got[0]
	if s.Metric != "http_requests_total" || s.Labels["job"] != "checkout" || s.Labels["__name__"] != "" {
		t.Errorf("series = %+v", s)
	}
	if !s.Aggregated {
		t.Error("a Prometheus series must be marked aggregated, or facts mode would accept it")
	}
	want := []sample{{TimeMillis: t0, Value: 60}, {TimeMillis: t0 + 60000, Value: 65}}
	if len(s.Samples) != len(want) {
		t.Fatalf("samples = %+v, want %+v", s.Samples, want)
	}
	for i := range want {
		if s.Samples[i] != want[i] {
			t.Errorf("sample %d = %+v, want %+v", i, s.Samples[i], want[i])
		}
	}
}

// TestPrometheusSkipsAreCountedNotDropped: a gauge, a native histogram and a
// series with no name are each reported under their own reason.
func TestPrometheusSkipsAreCountedNotDropped(t *testing.T) {
	body := line(`{__name__="process_resident_memory_bytes", job="checkout"}`, "1e+06", 0) +
		`{__name__="http_request_duration_seconds", job="checkout"} {count:2, sum:0.5, [-1,1]:2} ` + strconv.FormatInt(t0, 10) + "\n" +
		line(`{job="checkout"}`, "1", 0) +
		line(`{__name__="http_requests_total", job="checkout"}`, "1", 0) +
		line(`{__name__="http_requests_total", job="checkout"}`, "4", 60)

	store, _ := newStore(t)
	report, err := Plan(context.Background(), Options{
		Source: SourcePrometheus, Mode: ModeMetrics, Input: writeDump(t, body), Store: store,
		ServiceMap: map[string]string{"checkout": "checkout"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SeriesRead != 4 || report.SeriesImported != 1 || report.SeriesSkipped != 3 {
		t.Errorf("read %d, imported %d, skipped %d; want 4, 1, 3", report.SeriesRead, report.SeriesImported, report.SeriesSkipped)
	}
	for _, reason := range []string{skipNotACounter, skipNativeHistogram, skipNoMetricName} {
		if report.SkipReasons[reason] != 1 {
			t.Errorf("skip reason %q counted %d times, want 1 (all: %v)", reason, report.SkipReasons[reason], report.SkipReasons)
		}
	}
}

// TestPrometheusRefusesFactMode: every Prometheus series is an aggregate.
func TestPrometheusRefusesFactMode(t *testing.T) {
	body := line(`{__name__="http_requests_total", job="checkout"}`, "1", 0) +
		line(`{__name__="http_requests_total", job="checkout"}`, "9", 60)
	store, _ := newStore(t)
	_, err := Run(context.Background(), Options{
		Source: SourcePrometheus, Mode: ModeFacts, Input: writeDump(t, body), Store: store,
		ServiceMap: map[string]string{"checkout": "checkout"},
	})
	if !errors.Is(err, ErrAggregateInFactMode) {
		t.Fatalf("err = %v, want ErrAggregateInFactMode", err)
	}
}

// TestPrometheusImportWritesDeclaredDerivedRows: in metrics mode the increases
// become request counts in an imported partition, never a cumulative total.
func TestPrometheusImportWritesDeclaredDerivedRows(t *testing.T) {
	lbl := `{__name__="http_requests_total", job="checkout", handler="/orders/{id}"}`
	body := line(lbl, "100", 0) + line(lbl, "4300", 30) + line(lbl, "4400", 90)
	store, _ := newStore(t)
	report, err := Run(context.Background(), Options{
		Source: SourcePrometheus, Mode: ModeMetrics, Input: writeDump(t, body), Store: store,
		ServiceMap: map[string]string{"checkout": "checkout"}, PathLabel: "handler",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RowsWritten != 2 {
		t.Errorf("rows written = %d, want 2 (one per minute with an increase)", report.RowsWritten)
	}
	if len(report.Limitations) == 0 {
		t.Error("a metrics-mode import must carry its limitations")
	}
}

// TestPrometheusReaderReadsFilesOnly is AC-11's Prometheus half: a missing file
// fails on the file, and the reader holds no network client.
func TestPrometheusReaderReadsFilesOnly(t *testing.T) {
	_, err := prometheusReader{}.Read(filepath.Join(t.TempDir(), "absent.txt"))
	if err == nil || !strings.Contains(err.Error(), "read prometheus dump") {
		t.Fatalf("err = %v, want a file-read failure", err)
	}
	src, err := os.ReadFile("prometheus.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"net/http"`, `"net"`, "http.Get", "http.Client", `"github.com/prometheus/prometheus`} {
		if strings.Contains(string(src), banned) {
			t.Errorf("prometheus.go references %s; the importer reads a file and needs no running Prometheus", banned)
		}
	}
}
