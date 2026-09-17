// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/snappy"
	remotewritev1 "github.com/lgreene/gravix-dashboards/gen/remotewrite/v1"
	"github.com/lgreene/gravix-dashboards/pkg/cardinality"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"google.golang.org/protobuf/proto"
)

const testTenant = "acme"

// label is a terse constructor so the payload builders below read as the wire
// format they represent.
func label(name, value string) *remotewritev1.Label {
	return &remotewritev1.Label{Name: name, Value: value}
}

// encodeWriteRequest produces exactly what a Prometheus remote-write client
// sends: a protobuf WriteRequest, snappy block-compressed.
func encodeWriteRequest(t *testing.T, req *remotewritev1.WriteRequest) []byte {
	t.Helper()
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal WriteRequest: %v", err)
	}
	return snappy.Encode(nil, raw)
}

// remoteWriteRequest builds a POST carrying body, with the tenant the auth
// middleware would have attached in a multi-tenant deployment.
func remoteWriteRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/remote_write", bytes.NewReader(body))
	r.Header.Set("Content-Encoding", "snappy")
	ctx := context.WithValue(r.Context(), tenantInfoKey, &tenantdb.APIKeyInfo{TenantID: testTenant})
	return r.WithContext(ctx)
}

// oneSeries is the well-formed baseline every rejection test starts from.
func oneSeries() *remotewritev1.WriteRequest {
	return &remotewritev1.WriteRequest{
		Timeseries: []*remotewritev1.TimeSeries{{
			Labels: []*remotewritev1.Label{
				label("__name__", "http_requests_total"),
				label("service", "checkout"),
			},
			Samples: []*remotewritev1.Sample{{Value: 12, Timestamp: 1767225600000}},
		}},
	}
}

func newRemoteWriteHandler(t *testing.T, max int) (http.HandlerFunc, *DurableSink) {
	t.Helper()
	sink := setupSink(t)
	return handleRemoteWrite(sink, cardinality.NewBudget(max)), sink
}

// sinkContents returns everything written to the sink's buffer directory, so a
// test can assert on persistence rather than trusting the status code.
func sinkContents(t *testing.T, sink *DurableSink) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.Walk(sink.bufferDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sb.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk buffer dir: %v", err)
	}
	return sb.String()
}

// AC-1
func TestHandleRemoteWriteAcceptsValidSeries(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, oneSeries())))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("204 response carried a body: %q", w.Body.String())
	}

	written := sinkContents(t, sink)
	for _, want := range []string{
		`"metric_name":"http_requests_total"`,
		`"source":"prometheus_remote_write"`,
		`"metric_type":"counter"`,
		`"tenant_id":"acme"`,
		`"service":"checkout"`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("persisted sample missing %s\ngot: %s", want, written)
		}
	}
	// __name__ is carried by metric_name; leaving it in labels too would
	// double-count it against the label cap and the cardinality budget.
	if strings.Contains(written, "__name__") {
		t.Errorf("__name__ leaked into the persisted label set: %s", written)
	}
}

// AC-2
func TestHandleRemoteWriteRejectsExemplar(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	req := oneSeries()
	req.Timeseries[0].Exemplars = []*remotewritev1.Exemplar{{
		Labels:    []*remotewritev1.Label{label("trace_id", "abc123")},
		Value:     12,
		Timestamp: 1767225600000,
	}}

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), ErrExemplarRejected.Error()) {
		t.Errorf("body = %s, want %q", w.Body.String(), ErrExemplarRejected.Error())
	}
	if got := sinkContents(t, sink); got != "" {
		t.Errorf("a rejected payload was persisted: %s", got)
	}
}

// AC-3
func TestHandleRemoteWriteRejectsTraceIDLabel(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	req := oneSeries()
	req.Timeseries[0].Labels = append(req.Timeseries[0].Labels, label("trace_id", "abc123"))

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	want := fmt.Sprintf("%s: %s", ErrCorrelationLabel.Error(), "trace_id")
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("body = %s, want %q", w.Body.String(), want)
	}
	if got := sinkContents(t, sink); got != "" {
		t.Errorf("a rejected payload was persisted: %s", got)
	}
}

// The ban is on the correlation dimension, not on one spelling of it. A guard
// that only matched the names its author typed would be bypassed by changing
// the case or dropping an underscore.
func TestHandleRemoteWriteRejectsEveryCorrelationLabelSpelling(t *testing.T) {
	spellings := []string{
		"trace_id", "TRACE_ID", "Trace_Id", "traceid", "TraceID",
		"span_id", "SPAN_ID", "spanid", "SpanId",
		"parent_span_id", "Parent_Span_Id", "parentspanid", "ParentSpanID",
	}

	for _, name := range spellings {
		t.Run(name, func(t *testing.T) {
			handler, _ := newRemoteWriteHandler(t, 10)

			req := oneSeries()
			req.Timeseries[0].Labels = append(req.Timeseries[0].Labels, label(name, "x"))

			w := httptest.NewRecorder()
			handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("label %q accepted with status %d; it is a tracing-correlation dimension", name, w.Code)
			}
			if !strings.Contains(w.Body.String(), ErrCorrelationLabel.Error()) {
				t.Errorf("label %q rejected for the wrong reason: %s", name, w.Body.String())
			}
		})
	}
}

// AC-4
func TestHandleRemoteWriteRejectsMissingMetricName(t *testing.T) {
	cases := map[string][]*remotewritev1.Label{
		"no __name__":        {label("service", "checkout")},
		"empty __name__":     {label("__name__", ""), label("service", "checkout")},
		"duplicate __name__": {label("__name__", "a"), label("__name__", "b")},
	}

	for name, labels := range cases {
		t.Run(name, func(t *testing.T) {
			handler, sink := newRemoteWriteHandler(t, 10)

			req := oneSeries()
			req.Timeseries[0].Labels = labels

			w := httptest.NewRecorder()
			handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), ErrMissingMetricName.Error()) {
				t.Errorf("body = %s, want %q", w.Body.String(), ErrMissingMetricName.Error())
			}
			if got := sinkContents(t, sink); got != "" {
				t.Errorf("a rejected payload was persisted: %s", got)
			}
		})
	}
}

// AC-5
func TestHandleRemoteWriteRejectsWrongContentEncoding(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip", ""} {
		t.Run("encoding="+encoding, func(t *testing.T) {
			handler, _ := newRemoteWriteHandler(t, 10)

			body := encodeWriteRequest(t, oneSeries())
			r := httptest.NewRequest(http.MethodPost, "/api/v1/remote_write", bytes.NewReader(body))
			if encoding != "" {
				r.Header.Set("Content-Encoding", encoding)
			}

			w := httptest.NewRecorder()
			handler(w, r)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), "Content-Encoding must be snappy") {
				t.Errorf("body = %s", w.Body.String())
			}
		})
	}
}

// AC-6
func TestHandleRemoteWriteRejectsMalformedSnappy(t *testing.T) {
	handler, _ := newRemoteWriteHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, []byte("this is not snappy-compressed")))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid snappy payload") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHandleRemoteWriteRejectsMalformedProtobuf(t *testing.T) {
	handler, _ := newRemoteWriteHandler(t, 10)

	// Valid snappy framing around bytes that are not a WriteRequest.
	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, snappy.Encode(nil, []byte{0xff, 0xff, 0xff, 0xff})))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid protobuf payload") {
		t.Errorf("body = %s", w.Body.String())
	}
}

// AC-7: the (n+1)-th distinct label set fails the whole batch, including the
// series that would otherwise have been accepted alongside it.
func TestHandleRemoteWriteRejectsOverBudgetCardinality(t *testing.T) {
	const max = 3
	handler, sink := newRemoteWriteHandler(t, max)

	req := &remotewritev1.WriteRequest{}
	for i := 0; i <= max; i++ { // max+1 distinct label sets
		req.Timeseries = append(req.Timeseries, &remotewritev1.TimeSeries{
			Labels: []*remotewritev1.Label{
				label("__name__", "http_requests_total"),
				label("pod", fmt.Sprintf("pod-%d", i)),
			},
			Samples: []*remotewritev1.Sample{{Value: 1, Timestamp: 1767225600000}},
		})
	}

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	want := fmt.Sprintf("cardinality budget exceeded: tenant=%s metric=%s limit=%d distinct label-sets per day",
		testTenant, "http_requests_total", max)
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("body = %s, want %q", w.Body.String(), want)
	}
	// The whole batch fails: not one of the first max series may be persisted.
	if got := sinkContents(t, sink); got != "" {
		t.Errorf("an over-budget batch wrote partial data: %s", got)
	}
}

// AC-12
func TestHandleRemoteWriteMethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			handler, _ := newRemoteWriteHandler(t, 10)

			r := httptest.NewRequest(method, "/api/v1/remote_write", nil)
			w := httptest.NewRecorder()
			handler(w, r)

			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", w.Code)
			}
			if !strings.Contains(w.Body.String(), "only POST is accepted") {
				t.Errorf("body = %s", w.Body.String())
			}
		})
	}
}

// A series repeated across scrapes is one series, not a new one each time, so
// a well-behaved sender must never exhaust its own budget.
func TestHandleRemoteWriteRepeatedScrapesDoNotExhaustBudget(t *testing.T) {
	handler, _ := newRemoteWriteHandler(t, 1)

	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		handler(w, remoteWriteRequest(t, encodeWriteRequest(t, oneSeries())))
		if w.Code != http.StatusNoContent {
			t.Fatalf("scrape %d: status = %d, want 204; body: %s", i, w.Code, w.Body.String())
		}
	}
}

func TestHandleRemoteWriteInfersMetricTypeFromName(t *testing.T) {
	cases := map[string]string{
		"http_requests_total":           "counter",
		"http_request_duration_bucket":  "histogram_bucket",
		"process_resident_memory_bytes": "gauge",
	}

	for metricName, wantType := range cases {
		t.Run(metricName, func(t *testing.T) {
			handler, sink := newRemoteWriteHandler(t, 10)

			req := oneSeries()
			req.Timeseries[0].Labels[0] = label("__name__", metricName)

			w := httptest.NewRecorder()
			handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
			}

			want := fmt.Sprintf(`"metric_type":"%s"`, wantType)
			if got := sinkContents(t, sink); !strings.Contains(got, want) {
				t.Errorf("metric %s: persisted %s, want %s", metricName, got, want)
			}
		})
	}
}

// Every sample in a series becomes its own record: a batch carrying several
// points for one series must not collapse to one.
func TestHandleRemoteWriteWritesOneRecordPerSample(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	req := oneSeries()
	req.Timeseries[0].Samples = []*remotewritev1.Sample{
		{Value: 1, Timestamp: 1767225600000},
		{Value: 2, Timestamp: 1767225660000},
		{Value: 3, Timestamp: 1767225720000},
	}

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}

	if got := strings.Count(sinkContents(t, sink), `"metric_name"`); got != 3 {
		t.Fatalf("persisted %d records for 3 samples, want 3", got)
	}
}

// docs/00-system-truth.md §1 forbids derived data in a Fact. A Prometheus
// sample is already aggregated, so it must land in its own topic and never in
// request_facts.
func TestHandleRemoteWriteNeverWritesToTheFactTopic(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, oneSeries())))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	// The buffer nests topics under the tenant: <bufferDir>/<tenant>/<topic>/.
	var paths []string
	err := filepath.Walk(sink.bufferDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(sink.bufferDir, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk buffer dir: %v", err)
	}

	found := false
	for _, p := range paths {
		if strings.Contains(p, "request_facts") {
			t.Errorf("a Prometheus sample was written to the fact topic: %s", p)
		}
		if strings.Contains(p, filepath.Join(testTenant, "external_metrics")) {
			found = true
		}
	}
	if !found {
		t.Errorf("no external_metrics topic written; got %v", paths)
	}
}

// An empty WriteRequest is well-formed and asks for nothing. It must succeed
// without writing, not fail.
func TestHandleRemoteWriteAcceptsEmptyRequest(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, encodeWriteRequest(t, &remotewritev1.WriteRequest{})))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := sinkContents(t, sink); got != "" {
		t.Errorf("an empty request wrote data: %s", got)
	}
}

// Prometheus remote-write metadata is real field 3 of the upstream
// WriteRequest and is deliberately not declared in ours. A payload carrying it
// must still decode, with the field ignored, rather than being rejected as
// malformed.
func TestHandleRemoteWriteIgnoresUndeclaredMetadataField(t *testing.T) {
	handler, _ := newRemoteWriteHandler(t, 10)

	raw, err := proto.Marshal(oneSeries())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Append field 3, wire type 2 (length-delimited), with a short payload —
	// the shape upstream's metadata takes on the wire.
	raw = append(raw, 0x1a, 0x02, 0x08, 0x01)

	w := httptest.NewRecorder()
	handler(w, remoteWriteRequest(t, snappy.Encode(nil, raw)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

// KNOWN DEFECT, registered as SD-027. §5.4 makes tenant_id required while §6
// step 7 says it is empty in legacy single-key mode, so on a legacy
// deployment every remote-write request fails at validation. The shipped
// compose files set TENANT_DB_PATH and are unaffected. This test pins the
// current behaviour: when the defect is resolved it will fail, which is the
// point.
func TestHandleRemoteWriteFailsInLegacySingleKeyMode(t *testing.T) {
	handler, sink := newRemoteWriteHandler(t, 10)

	// No tenant in the context: exactly what getTenantID returns in legacy mode.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/remote_write", bytes.NewReader(encodeWriteRequest(t, oneSeries())))
	r.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	handler(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (see SD-027); if this now succeeds, the defect is fixed and this test should be replaced with one asserting 204", w.Code)
	}
	if got := sinkContents(t, sink); got != "" {
		t.Errorf("a sample that failed validation was persisted: %s", got)
	}
}
