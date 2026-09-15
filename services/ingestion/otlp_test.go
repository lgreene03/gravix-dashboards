// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	remotewritev1 "github.com/lgreene/gravix-dashboards/gen/remotewrite/v1"
	"github.com/lgreene/gravix-dashboards/pkg/cardinality"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// otlpRequest builds a POST to path carrying body, with the tenant the auth
// middleware would have attached in a multi-tenant deployment.
func otlpRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	}
	ctx := context.WithValue(r.Context(), tenantInfoKey, &tenantdb.APIKeyInfo{TenantID: testTenant})
	return r.WithContext(ctx)
}

// otlpSinkContents returns everything written to the sink's buffer directory.
func otlpSinkContents(t *testing.T, sink *DurableSink) string {
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

// otlpErrorField returns the decoded "error" string from a writeErrorJSON
// response. Comparing against the raw body would compare against JSON's
// escaping, so a message containing quotes could never match.
func otlpErrorField(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response %q: %v", w.Body.String(), err)
	}
	return body.Error
}

// gaugeRequest is the well-formed baseline most rejection tests start from.
func gaugeRequest() map[string]any {
	return map[string]any{
		"resourceMetrics": []any{map[string]any{
			"resource": map[string]any{
				"attributes": []any{map[string]any{
					"key":   "service.name",
					"value": map[string]any{"stringValue": "checkout"},
				}},
			},
			"scopeMetrics": []any{map[string]any{
				"metrics": []any{map[string]any{
					"name": "queue_depth",
					"gauge": map[string]any{
						"dataPoints": []any{map[string]any{
							"attributes": []any{map[string]any{
								"key":   "region",
								"value": map[string]any{"stringValue": "eu-west-1"},
							}},
							"timeUnixNano": "1767225600000000000",
							"asDouble":     17.5,
						}},
					},
				}},
			}},
		}},
	}
}

func encodeOTLP(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal OTLP payload: %v", err)
	}
	return data
}

// firstDataPoint reaches into the nested payload so a test can mutate exactly
// one data point without rebuilding the whole structure.
func firstDataPoint(payload map[string]any) map[string]any {
	rm := payload["resourceMetrics"].([]any)[0].(map[string]any)
	sm := rm["scopeMetrics"].([]any)[0].(map[string]any)
	m := sm["metrics"].([]any)[0].(map[string]any)
	g := m["gauge"].(map[string]any)
	return g["dataPoints"].([]any)[0].(map[string]any)
}

func firstMetric(payload map[string]any) map[string]any {
	rm := payload["resourceMetrics"].([]any)[0].(map[string]any)
	sm := rm["scopeMetrics"].([]any)[0].(map[string]any)
	return sm["metrics"].([]any)[0].(map[string]any)
}

func newOTLPMetricsHandler(t *testing.T, max int) (http.HandlerFunc, *DurableSink) {
	t.Helper()
	sink := setupSink(t)
	return handleOTLPMetrics(sink, cardinality.NewBudget(max)), sink
}

// ─── Traces and logs: refused at the door ───

// AC-1: a well-formed legacy OTLP span body is still rejected. The old
// receiver would have converted it; nothing about the payload's validity
// earns it a hearing now.
func TestHandleOTLPTracesRejectsAllRequests(t *testing.T) {
	legacySpanBody := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"traceId":"abc","spanId":"def","name":"GET /orders","startTimeUnixNano":"1767225600000000000","endTimeUnixNano":"1767225601000000000","attributes":[{"key":"http.method","value":{"stringValue":"GET"}}]}]}]}]}`)

	w := httptest.NewRecorder()
	handleOTLPTracesRejected(w, otlpRequest(t, http.MethodPost, "/v1/traces", legacySpanBody))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), ErrOTLPTracesRejected.Error()) {
		t.Errorf("body = %s, want %q", w.Body.String(), ErrOTLPTracesRejected.Error())
	}
}

// AC-2: the trace endpoint never writes. The body is not even read, so there
// is nothing it could write.
func TestHandleOTLPTracesNeverWritesToSink(t *testing.T) {
	sink := setupSink(t)

	w := httptest.NewRecorder()
	handleOTLPTracesRejected(w, otlpRequest(t, http.MethodPost, "/v1/traces", []byte(`{"resourceSpans":[]}`)))

	if got := otlpSinkContents(t, sink); got != "" {
		t.Fatalf("the trace endpoint wrote to the sink: %s", got)
	}
}

// AC-3: rejection is unconditional on method.
func TestHandleOTLPTracesRejectsRegardlessOfMethod(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			handleOTLPTracesRejected(w, otlpRequest(t, method, "/v1/traces", nil))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if method != http.MethodHead && !strings.Contains(w.Body.String(), ErrOTLPTracesRejected.Error()) {
				t.Errorf("body = %s, want the identical rejection message", w.Body.String())
			}
		})
	}
}

// AC-4
func TestHandleOTLPLogsRejectsAllRequests(t *testing.T) {
	logBody := []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"database password is hunter2"}}]}]}]}`)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			sink := setupSink(t)

			w := httptest.NewRecorder()
			handleOTLPLogsRejected(w, otlpRequest(t, method, "/v1/logs", logBody))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), ErrOTLPLogsRejected.Error()) {
				t.Errorf("body = %s, want %q", w.Body.String(), ErrOTLPLogsRejected.Error())
			}
			// A log payload must never be parsed, stored or forwarded — not
			// even the part of it that looks like a secret.
			if got := otlpSinkContents(t, sink); got != "" {
				t.Errorf("the log endpoint wrote to the sink: %s", got)
			}
		})
	}
}

// ─── Metrics subset ───

// AC-5
func TestHandleOTLPMetricsAcceptsValidGauge(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, gaugeRequest())))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("204 response carried a body: %q", w.Body.String())
	}

	written := otlpSinkContents(t, sink)
	for _, want := range []string{
		`"source":"otlp_metrics"`,
		`"metric_name":"queue_depth"`,
		`"metric_type":"gauge"`,
		`"tenant_id":"acme"`,
		`"region":"eu-west-1"`,
		`"value":17.5`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("persisted sample missing %s\ngot: %s", want, written)
		}
	}
	if got := strings.Count(written, `"sample_id"`); got != 1 {
		t.Errorf("persisted %d samples for one data point, want 1", got)
	}
}

// AC-6
func TestHandleOTLPMetricsSumBecomesCounter(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	m := firstMetric(payload)
	m["name"] = "orders_processed"
	m["sum"] = m["gauge"]
	delete(m, "gauge")

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := otlpSinkContents(t, sink); !strings.Contains(got, `"metric_type":"counter"`) {
		t.Errorf("a sum was not persisted as a counter: %s", got)
	}
}

// AC-7
func TestHandleOTLPMetricsRejectsExemplar(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	firstDataPoint(payload)["exemplars"] = []any{map[string]any{"traceId": "abc123", "spanId": "def456"}}

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	// The exact message is GRVX-1102's, reused rather than re-worded: one
	// refusal, one wording, whichever protocol carried the exemplar.
	if !strings.Contains(w.Body.String(), ErrExemplarRejected.Error()) {
		t.Errorf("body = %s, want %q", w.Body.String(), ErrExemplarRejected.Error())
	}
	if got := otlpSinkContents(t, sink); got != "" {
		t.Errorf("a rejected payload was persisted: %s", got)
	}
}

// AC-8
func TestHandleOTLPMetricsRejectsTraceIDAttribute(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	dp := firstDataPoint(payload)
	dp["attributes"] = append(dp["attributes"].([]any), map[string]any{
		"key":   "trace_id",
		"value": map[string]any{"stringValue": "abc123"},
	})

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	want := fmt.Sprintf("%s: %s", ErrCorrelationLabel.Error(), "trace_id")
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("body = %s, want %q", w.Body.String(), want)
	}
	if got := otlpSinkContents(t, sink); got != "" {
		t.Errorf("a rejected payload was persisted: %s", got)
	}
}

// The ban is on the dimension, not on one spelling of it — the same standard
// GRVX-1102 holds remote-write to, applied to OTLP attributes.
func TestHandleOTLPMetricsRejectsEveryCorrelationAttributeSpelling(t *testing.T) {
	for _, key := range []string{"trace_id", "TRACE_ID", "TraceId", "traceid", "span_id", "SpanID", "spanid", "parent_span_id", "ParentSpanId", "parentspanid"} {
		t.Run(key, func(t *testing.T) {
			handler, _ := newOTLPMetricsHandler(t, 10)

			payload := gaugeRequest()
			dp := firstDataPoint(payload)
			dp["attributes"] = append(dp["attributes"].([]any), map[string]any{
				"key":   key,
				"value": map[string]any{"stringValue": "x"},
			})

			w := httptest.NewRecorder()
			handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("attribute %q accepted with status %d; it is a tracing-correlation dimension", key, w.Code)
			}
			if !strings.Contains(w.Body.String(), ErrCorrelationLabel.Error()) {
				t.Errorf("attribute %q rejected for the wrong reason: %s", key, w.Body.String())
			}
		})
	}
}

// AC-9: the two protocols share one ledger, so a tenant cannot double its
// effective budget by switching wire format.
func TestHandleOTLPMetricsSharesCardinalityBudgetWithRemoteWrite(t *testing.T) {
	const max = 2
	sink := setupSink(t)
	budget := cardinality.NewBudget(max)

	remoteWrite := handleRemoteWrite(sink, budget)
	otlp := handleOTLPMetrics(sink, budget)

	// Fill the budget for (acme, queue_depth) entirely through remote-write.
	for i := 0; i < max; i++ {
		req := &remotewritev1.WriteRequest{
			Timeseries: []*remotewritev1.TimeSeries{{
				Labels: []*remotewritev1.Label{
					label("__name__", "queue_depth"),
					label("pod", fmt.Sprintf("pod-%d", i)),
				},
				Samples: []*remotewritev1.Sample{{Value: 1, Timestamp: 1767225600000}},
			}},
		}
		w := httptest.NewRecorder()
		remoteWrite(w, remoteWriteRequest(t, encodeWriteRequest(t, req)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("remote-write %d: status = %d, want 204; body: %s", i, w.Code, w.Body.String())
		}
	}

	// A brand-new label set arriving over OTLP for the same (tenant, metric)
	// must now be refused, on budget spent by the other protocol.
	payload := gaugeRequest()
	firstMetric(payload)["name"] = "queue_depth"
	// Drop the resource so service_name is not merged, keeping the label set
	// comparable to the remote-write ones.
	payload["resourceMetrics"].([]any)[0].(map[string]any)["resource"] = map[string]any{}

	w := httptest.NewRecorder()
	otlp(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; OTLP did not see remote-write's budget consumption", w.Code)
	}
	want := fmt.Sprintf("cardinality budget exceeded: tenant=%s metric=%s limit=%d distinct label-sets per day", testTenant, "queue_depth", max)
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("body = %s, want %q", w.Body.String(), want)
	}
}

// AC-10
func TestHandleOTLPMetricsRejectsHistogram(t *testing.T) {
	for _, field := range []string{"histogram", "exponentialHistogram", "summary"} {
		t.Run(field, func(t *testing.T) {
			handler, sink := newOTLPMetricsHandler(t, 10)

			payload := gaugeRequest()
			m := firstMetric(payload)
			m["name"] = "request_duration"
			delete(m, "gauge")
			m[field] = map[string]any{"dataPoints": []any{map[string]any{"count": "3"}}}

			w := httptest.NewRecorder()
			handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			want := fmt.Sprintf("metric %q: %s", "request_duration", ErrOTLPMetricUnsupportedType.Error())
			if got := otlpErrorField(t, w); got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
			// Rejected with a clear message, not silently dropped.
			if got := otlpSinkContents(t, sink); got != "" {
				t.Errorf("an unsupported type was persisted: %s", got)
			}
		})
	}
}

// AC-11
func TestHandleOTLPMetricsMergesServiceNameLabel(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, gaugeRequest())))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := otlpSinkContents(t, sink); !strings.Contains(got, `"service_name":"checkout"`) {
		t.Errorf("service.name was not merged as service_name: %s", got)
	}
}

// The resource is the authoritative statement of who produced the metric, so
// a data point must not be able to claim a different origin by declaring its
// own service_name attribute.
func TestHandleOTLPMetricsResourceServiceNameWinsOverAttribute(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	dp := firstDataPoint(payload)
	dp["attributes"] = append(dp["attributes"].([]any), map[string]any{
		"key":   "service_name",
		"value": map[string]any{"stringValue": "impostor"},
	})

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	got := otlpSinkContents(t, sink)
	if !strings.Contains(got, `"service_name":"checkout"`) {
		t.Errorf("resource service.name did not win: %s", got)
	}
	if strings.Contains(got, "impostor") {
		t.Errorf("a data-point attribute overrode the resource's service.name: %s", got)
	}
}

// AC-12
func TestHandleOTLPMetricsMethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			handler, _ := newOTLPMetricsHandler(t, 10)

			w := httptest.NewRecorder()
			handler(w, otlpRequest(t, method, "/v1/metrics", nil))

			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", w.Code)
			}
			if !strings.Contains(w.Body.String(), "only POST is accepted") {
				t.Errorf("body = %s", w.Body.String())
			}
		})
	}
}

// ─── Remaining §6.1 failure modes ───

func TestHandleOTLPMetricsRejectsMalformedJSON(t *testing.T) {
	handler, _ := newOTLPMetricsHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", []byte("{not json")))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid JSON: ") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHandleOTLPMetricsRejectsMissingName(t *testing.T) {
	handler, _ := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	firstMetric(payload)["name"] = ""

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), ErrOTLPMetricMissingName.Error()) {
		t.Errorf("body = %s, want %q", w.Body.String(), ErrOTLPMetricMissingName.Error())
	}
}

func TestHandleOTLPMetricsRejectsMissingValueOrTimestamp(t *testing.T) {
	cases := map[string]func(map[string]any){
		"no value": func(dp map[string]any) {
			delete(dp, "asDouble")
		},
		"unparseable timestamp": func(dp map[string]any) {
			dp["timeUnixNano"] = "not-a-number"
		},
		"missing timestamp": func(dp map[string]any) {
			delete(dp, "timeUnixNano")
		},
		"unparseable asInt": func(dp map[string]any) {
			delete(dp, "asDouble")
			dp["asInt"] = "twelve"
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			handler, sink := newOTLPMetricsHandler(t, 10)

			payload := gaugeRequest()
			mutate(firstDataPoint(payload))

			w := httptest.NewRecorder()
			handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			want := fmt.Sprintf("metric %q: %s", "queue_depth", ErrOTLPMetricMissingValue.Error())
			if got := otlpErrorField(t, w); got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
			if got := otlpSinkContents(t, sink); got != "" {
				t.Errorf("a rejected payload was persisted: %s", got)
			}
		})
	}
}

// asInt arrives as a decimal string in OTLP/JSON, and must round-trip as a
// number rather than being dropped for want of asDouble.
func TestHandleOTLPMetricsAcceptsAsIntValue(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	dp := firstDataPoint(payload)
	delete(dp, "asDouble")
	dp["asInt"] = "42"

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := otlpSinkContents(t, sink); !strings.Contains(got, `"value":42`) {
		t.Errorf("asInt was not carried through: %s", got)
	}
}

// A metric carrying neither gauge nor sum is skipped, not rejected: there is
// nothing to convert and nothing wrong with the payload.
func TestHandleOTLPMetricsSkipsMetricWithNoDataPoints(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	m := firstMetric(payload)
	delete(m, "gauge")

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := otlpSinkContents(t, sink); got != "" {
		t.Errorf("a metric with no data points wrote something: %s", got)
	}
}

// A batch is accepted whole or refused whole: one bad point must not leave
// its well-formed neighbours persisted.
func TestHandleOTLPMetricsRejectionIsAtomic(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	payload := gaugeRequest()
	rm := payload["resourceMetrics"].([]any)[0].(map[string]any)
	sm := rm["scopeMetrics"].([]any)[0].(map[string]any)
	// A second metric, well-formed, declared before the offending one.
	good := map[string]any{
		"name": "cache_entries",
		"gauge": map[string]any{
			"dataPoints": []any{map[string]any{"timeUnixNano": "1767225600000000000", "asDouble": 3.0}},
		},
	}
	bad := map[string]any{
		"name":      "request_duration",
		"histogram": map[string]any{"dataPoints": []any{}},
	}
	sm["metrics"] = []any{good, bad}

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, payload)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := otlpSinkContents(t, sink); got != "" {
		t.Errorf("the good metric was persisted despite the batch being rejected: %s", got)
	}
}

// Both protocols write to one topic, so a dashboard reading external_metrics
// sees Prometheus and OTLP samples together.
func TestHandleOTLPMetricsWritesToTheSharedExternalMetricsTopic(t *testing.T) {
	handler, sink := newOTLPMetricsHandler(t, 10)

	w := httptest.NewRecorder()
	handler(w, otlpRequest(t, http.MethodPost, "/v1/metrics", encodeOTLP(t, gaugeRequest())))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

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
		if strings.Contains(p, "request_facts") || strings.Contains(p, "trace_samples") {
			t.Errorf("an OTLP metric was written to %s", p)
		}
		if strings.Contains(p, filepath.Join(testTenant, "external_metrics")) {
			found = true
		}
	}
	if !found {
		t.Errorf("no external_metrics topic written; got %v", paths)
	}
}
