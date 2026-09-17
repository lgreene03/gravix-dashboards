// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/cardinality"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This file implements Gravix's OTLP/HTTP surface, which is deliberately one
// signal wide. Metrics are accepted as a gauge-and-sum subset; traces and logs
// are refused at the door, in every wire format, at every endpoint.
//
// The server-side OTLP trace receiver that used to live here converted spans
// into RequestFacts. It was deleted rather than hardened because the use case
// it served is already served correctly elsewhere: sdk/go/otel/exporter.go
// does the same conversion client-side, so no trace ID, span ID or parent span
// ID ever leaves the caller's process. A receiver that accepts trace exports is
// a standing invitation to send Gravix exactly what docs/04-non-goals.md §1
// says it does not ingest.

// OTLPResource describes the entity producing telemetry.
type OTLPResource struct {
	Attributes []OTLPAttribute `json:"attributes"`
}

// OTLPAttribute is a key-value pair.
type OTLPAttribute struct {
	Key   string        `json:"key"`
	Value OTLPAttrValue `json:"value"`
}

// OTLPAttrValue holds the attribute value (only stringValue and intValue supported).
type OTLPAttrValue struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    string `json:"intValue,omitempty"`
}

const otlpMaxBodyBytes = 10 << 20 // 10 MB max for OTLP payloads

// OTLPMetricsRequest is a simplified OTLP/HTTP JSON ExportMetricsServiceRequest,
// covering only the fields this subset reads.
type OTLPMetricsRequest struct {
	ResourceMetrics []OTLPResourceMetrics `json:"resourceMetrics"`
}

type OTLPResourceMetrics struct {
	Resource     OTLPResource       `json:"resource"`
	ScopeMetrics []OTLPScopeMetrics `json:"scopeMetrics"`
}

type OTLPScopeMetrics struct {
	Metrics []OTLPMetric `json:"metrics"`
}

// OTLPMetric holds one metric's data points. Per the OTLP spec, at most one
// of Gauge, Sum, Histogram, ExponentialHistogram, Summary is populated. This
// subset converts Gauge and Sum only; the other three are detected (as raw,
// unparsed JSON) only to be rejected with ErrOTLPMetricUnsupportedType.
type OTLPMetric struct {
	Name                 string          `json:"name"`
	Gauge                *OTLPGaugeOrSum `json:"gauge,omitempty"`
	Sum                  *OTLPGaugeOrSum `json:"sum,omitempty"`
	Histogram            json.RawMessage `json:"histogram,omitempty"`
	ExponentialHistogram json.RawMessage `json:"exponentialHistogram,omitempty"`
	Summary              json.RawMessage `json:"summary,omitempty"`
}

type OTLPGaugeOrSum struct {
	DataPoints []OTLPNumberDataPoint `json:"dataPoints"`
}

// OTLPNumberDataPoint mirrors OTLP's NumberDataPoint. TimeUnixNano and AsInt
// are strings because OTLP/JSON encodes 64-bit integers as decimal strings.
type OTLPNumberDataPoint struct {
	Attributes   []OTLPAttribute `json:"attributes"`
	TimeUnixNano string          `json:"timeUnixNano"`
	AsDouble     *float64        `json:"asDouble,omitempty"`
	AsInt        string          `json:"asInt,omitempty"`
	Exemplars    []OTLPExemplar  `json:"exemplars,omitempty"`
}

// OTLPExemplar mirrors OTLP's Exemplar. Its presence on any data point
// causes the entire request to be rejected with ErrExemplarRejected
// (services/ingestion/remote_write.go, GRVX-1102) — an exemplar links a
// metric sample to a trace/span, which is exactly the correlation this
// project does not ingest.
type OTLPExemplar struct {
	TraceID string `json:"traceId,omitempty"`
	SpanID  string `json:"spanId,omitempty"`
}

var (
	ErrOTLPTracesRejected        = errors.New("gravix does not ingest OTLP trace signals: distributed tracing is not supported (see docs/04-non-goals.md §1)")
	ErrOTLPLogsRejected          = errors.New("gravix does not ingest OTLP log signals: log aggregation is not supported (see docs/04-non-goals.md §2)")
	ErrOTLPMetricMissingName     = errors.New("metric missing name")
	ErrOTLPMetricUnsupportedType = errors.New("histogram, exponential_histogram, and summary points are not supported in the OTLP metrics subset; only gauge and sum are accepted")
	ErrOTLPMetricMissingValue    = errors.New("data point missing asDouble/asInt, or has an invalid or missing timeUnixNano")

	// ingestionOTLPMetricPointsTotal counts data points by outcome. Both
	// labels are bounded — tenant is a known set and result is the closed set
	// below — so this counter cannot become the high-cardinality leak the
	// handler exists to prevent.
	ingestionOTLPMetricPointsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingestion_otlp_metric_points_total",
			Help: "Total OTLP metrics data points processed, by outcome.",
		},
		[]string{"tenant", "result"},
	)
)

const (
	otlpResultAccepted                = "accepted"
	otlpResultRejectedMissingName     = "rejected_missing_name"
	otlpResultRejectedUnsupportedType = "rejected_unsupported_type"
	otlpResultRejectedExemplar        = "rejected_exemplar"
	otlpResultRejectedLabel           = "rejected_label"
	otlpResultRejectedMissingValue    = "rejected_missing_value"
	otlpResultRejectedCardinality     = "rejected_cardinality"
)

func init() {
	prometheus.MustRegister(ingestionOTLPMetricPointsTotal)
}

// handleOTLPTracesRejected is the POST /v1/traces handler. It rejects every
// request, of every HTTP method, without reading the request body: gravix
// does not ingest distributed tracing signals, in any wire format, at any
// endpoint.
func handleOTLPTracesRejected(w http.ResponseWriter, r *http.Request) {
	ingestionRequestsTotal.WithLabelValues("/v1/traces", "400", getTenantID(r)).Inc()
	writeErrorJSON(w, http.StatusBadRequest, ErrOTLPTracesRejected.Error())
}

// handleOTLPLogsRejected is the POST /v1/logs handler. It rejects every
// request, of every HTTP method, without reading the request body: gravix
// does not ingest log aggregation signals, in any wire format, at any
// endpoint.
func handleOTLPLogsRejected(w http.ResponseWriter, r *http.Request) {
	ingestionRequestsTotal.WithLabelValues("/v1/logs", "400", getTenantID(r)).Inc()
	writeErrorJSON(w, http.StatusBadRequest, ErrOTLPLogsRejected.Error())
}

// otlpPoint is one data point that has passed every check, carrying the
// values the write phase needs so it does not re-derive what validation
// already established.
type otlpPoint struct {
	metricName string
	metricType string
	labels     map[string]string
	value      float64
	timeNanos  uint64
}

// otlpRejection is a whole-request rejection: the reason to report and the
// counter bucket it belongs in.
type otlpRejection struct {
	result  string
	message string
}

// handleOTLPMetrics returns the POST /v1/metrics handler. budget must be the
// same *cardinality.Budget instance constructed for
// POST /api/v1/remote_write (GRVX-1102), so Prometheus and OTLP metrics
// share one per-(tenant, metric) cardinality ledger — otherwise a tenant
// could double its effective budget just by switching wire format.
func handleOTLPMetrics(sink *DurableSink, budget *cardinality.Budget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, otlpMaxBodyBytes)
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 10MB)")
			return
		}

		var req OTLPMetricsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}

		tenantID := getTenantID(r)

		points, rejection := validateOTLPMetrics(req, tenantID, budget)
		if rejection != nil {
			ingestionOTLPMetricPointsTotal.WithLabelValues(tenantID, rejection.result).Inc()
			writeErrorJSON(w, http.StatusBadRequest, rejection.message)
			return
		}

		topic := topicForTenant(tenantID, "external_metrics")
		for _, p := range points {
			if err := writeOTLPPoint(sink, topic, tenantID, p); err != nil {
				slog.Error("otlp metrics persist failed", "tenant", tenantID, "metric", p.metricName, "error", err)
				writeErrorJSON(w, http.StatusInternalServerError, "failed to persist external metric sample")
				return
			}
			ingestionOTLPMetricPointsTotal.WithLabelValues(tenantID, otlpResultAccepted).Inc()
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// validateOTLPMetrics checks every metric and every data point before any of
// them is written, so a batch is accepted whole or refused whole. A partially
// accepted export leaves the sender with no way to know which half landed.
func validateOTLPMetrics(req OTLPMetricsRequest, tenantID string, budget *cardinality.Budget) ([]otlpPoint, *otlpRejection) {
	var points []otlpPoint

	for _, rm := range req.ResourceMetrics {
		serviceName := extractResourceAttr(rm.Resource, "service.name")

		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "" {
					return nil, &otlpRejection{result: otlpResultRejectedMissingName, message: ErrOTLPMetricMissingName.Error()}
				}

				if len(m.Histogram) > 0 || len(m.ExponentialHistogram) > 0 || len(m.Summary) > 0 {
					return nil, &otlpRejection{
						result:  otlpResultRejectedUnsupportedType,
						message: fmt.Sprintf("metric %q: %s", m.Name, ErrOTLPMetricUnsupportedType.Error()),
					}
				}

				var dataPoints []OTLPNumberDataPoint
				var metricType string
				switch {
				case m.Gauge != nil:
					dataPoints, metricType = m.Gauge.DataPoints, "gauge"
				case m.Sum != nil:
					dataPoints, metricType = m.Sum.DataPoints, "counter"
				default:
					// Nothing this subset converts, and nothing it rejects.
					continue
				}

				for _, p := range dataPoints {
					point, rejection := validateOTLPPoint(m.Name, metricType, serviceName, p, tenantID, budget)
					if rejection != nil {
						return nil, rejection
					}
					points = append(points, *point)
				}
			}
		}
	}

	return points, nil
}

// validateOTLPPoint runs every per-point check, in the order §6 step 5e
// defines, so the reason reported is the most specific thing wrong with the
// point rather than whichever check happened to run first.
func validateOTLPPoint(metricName, metricType, serviceName string, p OTLPNumberDataPoint, tenantID string, budget *cardinality.Budget) (*otlpPoint, *otlpRejection) {
	if len(p.Exemplars) > 0 {
		return nil, &otlpRejection{result: otlpResultRejectedExemplar, message: ErrExemplarRejected.Error()}
	}

	labels := make(map[string]string, len(p.Attributes)+1)
	for _, a := range p.Attributes {
		if a.Key == "" {
			continue
		}
		if a.Value.StringValue != "" {
			labels[a.Key] = a.Value.StringValue
		} else {
			labels[a.Key] = a.Value.IntValue
		}
	}

	// The resource's service.name wins over a data-point attribute of the same
	// name: the resource is the authoritative statement of who produced the
	// metric, and a point must not be able to claim a different origin.
	if serviceName != "" {
		labels["service_name"] = serviceName
	}

	for k := range labels {
		if bannedCorrelationLabels[strings.ToLower(k)] {
			return nil, &otlpRejection{
				result:  otlpResultRejectedLabel,
				message: fmt.Sprintf("%s: %s", ErrCorrelationLabel.Error(), k),
			}
		}
	}

	missingValue := &otlpRejection{
		result:  otlpResultRejectedMissingValue,
		message: fmt.Sprintf("metric %q: %s", metricName, ErrOTLPMetricMissingValue.Error()),
	}

	if p.AsDouble == nil && p.AsInt == "" {
		return nil, missingValue
	}

	nanos, err := strconv.ParseUint(p.TimeUnixNano, 10, 64)
	if err != nil {
		return nil, missingValue
	}

	value := 0.0
	if p.AsDouble != nil {
		value = *p.AsDouble
	} else {
		v, err := strconv.ParseInt(p.AsInt, 10, 64)
		if err != nil {
			return nil, missingValue
		}
		value = float64(v)
	}

	if admitted, _ := budget.Admit(tenantID, metricName, labels); !admitted {
		return nil, &otlpRejection{
			result: otlpResultRejectedCardinality,
			message: fmt.Sprintf("cardinality budget exceeded: tenant=%s metric=%s limit=%d distinct label-sets per day",
				tenantID, metricName, budget.Max()),
		}
	}

	return &otlpPoint{
		metricName: metricName,
		metricType: metricType,
		labels:     labels,
		value:      value,
		timeNanos:  nanos,
	}, nil
}

// writeOTLPPoint persists one validated data point as an ExternalMetricSample,
// to the same topic POST /api/v1/remote_write writes to.
func writeOTLPPoint(sink *DurableSink, topic, tenantID string, p otlpPoint) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate sample id: %w", err)
	}

	out := &schemas.ExternalMetricSample{
		SampleId:   id.String(),
		SampleTime: timestamppb.New(time.Unix(0, int64(p.timeNanos)).UTC()),
		TenantId:   tenantID,
		Source:     "otlp_metrics",
		MetricName: p.metricName,
		Labels:     p.labels,
		Value:      p.value,
		MetricType: p.metricType,
	}

	if err := schemas.ValidateExternalMetricSample(out); err != nil {
		return fmt.Errorf("validate sample: %w", err)
	}

	data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal sample: %w", err)
	}

	return sink.Write(topic, data)
}

// extractResourceAttr extracts an attribute value from a resource.
func extractResourceAttr(res OTLPResource, key string) string {
	for _, attr := range res.Attributes {
		if attr.Key == key {
			return attr.Value.StringValue
		}
	}
	return ""
}
