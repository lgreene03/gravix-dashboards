// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/google/uuid"
	remotewritev1 "github.com/lgreene/gravix-dashboards/gen/remotewrite/v1"
	"github.com/lgreene/gravix-dashboards/pkg/cardinality"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const remoteWriteMaxBodyBytes = 10 << 20 // 10 MB, matches otlpMaxBodyBytes

// metricNameLabel is Prometheus' reserved label carrying the metric name.
const metricNameLabel = "__name__"

// bannedCorrelationLabels lists label names rejected regardless of case,
// because their presence means the series is carrying tracing-correlation
// data (docs/04-non-goals.md §1), not a metric.
var bannedCorrelationLabels = map[string]bool{
	"trace_id": true, "traceid": true,
	"span_id": true, "spanid": true,
	"parent_span_id": true, "parentspanid": true,
}

var (
	ErrExemplarRejected    = errors.New("exemplars are rejected: gravix does not ingest distributed tracing signals (see docs/04-non-goals.md §1)")
	ErrCorrelationLabel    = errors.New("label carries tracing correlation data, which gravix does not ingest (see docs/04-non-goals.md §1)")
	ErrMissingMetricName   = errors.New("time series missing __name__ label")
	ErrCardinalityExceeded = errors.New("cardinality budget exceeded")
)

// ingestionRemoteWriteSeriesTotal counts time series by outcome. Both labels
// are bounded: tenant is a known set, and result has exactly the five values
// below. Nothing per-series — no metric name, no label value — may be added
// here, or this counter becomes the high-cardinality leak the handler exists
// to prevent.
var ingestionRemoteWriteSeriesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ingestion_remote_write_series_total",
		Help: "Prometheus remote-write time series by outcome.",
	},
	[]string{"tenant", "result"},
)

const (
	resultAccepted            = "accepted"
	resultRejectedMissingName = "rejected_missing_name"
	resultRejectedExemplar    = "rejected_exemplar"
	resultRejectedLabel       = "rejected_label"
	resultRejectedCardinality = "rejected_cardinality"
)

// seriesView is one decoded TimeSeries after its labels have been checked and
// indexed, so the write phase does not re-derive what the validation phase
// already established.
type seriesView struct {
	metricName string
	// labels excludes __name__, which is carried by metricName.
	labels  map[string]string
	samples []*remotewritev1.Sample
}

// handleRemoteWrite returns the POST /api/v1/remote_write handler.
//
// Every series in the request is validated before any of them is written. A
// remote-write payload is a batch from one scrape, and accepting half of it
// would leave the sender with no way to know which half — so a single bad
// series fails the whole request and the sender retries it entire.
func handleRemoteWrite(sink *DurableSink, budget *cardinality.Budget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}

		if r.Header.Get("Content-Encoding") != "snappy" {
			writeErrorJSON(w, http.StatusBadRequest, "Content-Encoding must be snappy")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, remoteWriteMaxBodyBytes)
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 10MB)")
			return
		}

		decompressed, err := snappy.Decode(nil, body)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid snappy payload: %v", err))
			return
		}

		var req remotewritev1.WriteRequest
		if err := proto.Unmarshal(decompressed, &req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid protobuf payload: %v", err))
			return
		}

		tenantID := getTenantID(r)

		views, rejection := validateSeries(req.GetTimeseries(), tenantID, budget)
		if rejection != nil {
			ingestionRemoteWriteSeriesTotal.WithLabelValues(tenantID, rejection.result).Inc()
			writeErrorJSON(w, http.StatusBadRequest, rejection.message)
			return
		}

		for _, view := range views {
			if err := writeSeries(sink, tenantID, view); err != nil {
				slog.Error("remote-write persist failed", "tenant", tenantID, "metric", view.metricName, "error", err)
				writeErrorJSON(w, http.StatusInternalServerError, "failed to persist external metric sample")
				return
			}
			ingestionRemoteWriteSeriesTotal.WithLabelValues(tenantID, resultAccepted).Inc()
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// seriesRejection is a whole-request rejection: the reason to report to the
// sender, and the counter bucket it belongs in.
type seriesRejection struct {
	result  string
	message string
}

// validateSeries runs every check in order over every series, and returns
// either the decoded views ready to write or the first rejection found.
//
// The phases are kept in the order the spec defines — name, then exemplar,
// then correlation label, then budget — because the reason reported to the
// sender should be the most specific thing wrong with the payload, not
// whichever check happened to run first on a given series.
func validateSeries(timeseries []*remotewritev1.TimeSeries, tenantID string, budget *cardinality.Budget) ([]seriesView, *seriesRejection) {
	views := make([]seriesView, 0, len(timeseries))

	for _, ts := range timeseries {
		labels := make(map[string]string, len(ts.GetLabels()))
		metricName := ""
		nameCount := 0

		for _, label := range ts.GetLabels() {
			if label.GetName() == metricNameLabel {
				nameCount++
				metricName = label.GetValue()
				continue
			}
			labels[label.GetName()] = label.GetValue()
		}

		// Duplicates have to be counted while reading, not after: a map
		// collapses two __name__ labels into one and the ambiguity disappears
		// before anyone can object to it.
		if nameCount != 1 || metricName == "" {
			return nil, &seriesRejection{result: resultRejectedMissingName, message: ErrMissingMetricName.Error()}
		}

		views = append(views, seriesView{metricName: metricName, labels: labels, samples: ts.GetSamples()})
	}

	for _, ts := range timeseries {
		if len(ts.GetExemplars()) > 0 {
			return nil, &seriesRejection{result: resultRejectedExemplar, message: ErrExemplarRejected.Error()}
		}
	}

	for _, ts := range timeseries {
		for _, label := range ts.GetLabels() {
			if bannedCorrelationLabels[strings.ToLower(label.GetName())] {
				return nil, &seriesRejection{
					result:  resultRejectedLabel,
					message: fmt.Sprintf("%s: %s", ErrCorrelationLabel.Error(), label.GetName()),
				}
			}
		}
	}

	for _, view := range views {
		if admitted, _ := budget.Admit(tenantID, view.metricName, view.labels); !admitted {
			return nil, &seriesRejection{
				result: resultRejectedCardinality,
				message: fmt.Sprintf("cardinality budget exceeded: tenant=%s metric=%s limit=%d distinct label-sets per day",
					tenantID, view.metricName, budget.Max()),
			}
		}
	}

	return views, nil
}

// writeSeries converts one validated series into one ExternalMetricSample per
// sample and persists each through the durable sink.
func writeSeries(sink *DurableSink, tenantID string, view seriesView) error {
	topic := topicForTenant(tenantID, "external_metrics")
	metricType := metricTypeFor(view.metricName)

	for _, sample := range view.samples {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate sample id: %w", err)
		}

		out := &schemas.ExternalMetricSample{
			SampleId:   id.String(),
			SampleTime: timestamppb.New(time.UnixMilli(sample.GetTimestamp()).UTC()),
			TenantId:   tenantID,
			Source:     "prometheus_remote_write",
			MetricName: view.metricName,
			Labels:     view.labels,
			Value:      sample.GetValue(),
			MetricType: metricType,
		}

		if err := schemas.ValidateExternalMetricSample(out); err != nil {
			return fmt.Errorf("validate sample: %w", err)
		}

		data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(out)
		if err != nil {
			return fmt.Errorf("marshal sample: %w", err)
		}

		if err := sink.Write(topic, data); err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
	}

	return nil
}

// metricTypeFor infers the metric type from Prometheus' own naming
// convention, which is all the wire format gives us — remote-write carries no
// type information per series, and this spec does not accept the metadata
// field that would.
func metricTypeFor(metricName string) string {
	switch {
	case strings.HasSuffix(metricName, "_total"):
		return "counter"
	case strings.HasSuffix(metricName, "_bucket"):
		return "histogram_bucket"
	default:
		return "gauge"
	}
}
