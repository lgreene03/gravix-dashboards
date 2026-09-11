// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
)

// The percentile endpoint exists because neither DuckDB nor Trino can merge a
// t-digest written by our Go code, and a percentile over many buckets cannot be
// computed any other way that is correct.
//
// GRVX-808 §5.1 fixed the architecture and it is worth restating so it is not
// relitigated: the merge happens here, in Go, with the same pkg/sketch code that
// wrote the sketches. Implementing t-digest arithmetic in DuckDB SQL and again in
// Trino SQL would mean two more implementations to prove correct, in two dialects,
// and the error bound measured in GRVX-804 would not transfer to either. The cost
// is one extra hop for windowed percentiles, which is accepted.

// maxPercentileWindow bounds a request. A month of minute buckets is ~44,640
// sketches; beyond that the merge stops being an interactive operation.
const maxPercentileWindow = 31 * 24 * time.Hour

// percentileResponse is the endpoint's success body.
type percentileResponse struct {
	Metric      string             `json:"metric"`
	Quantile    float64            `json:"quantile"`
	Granularity string             `json:"granularity"`
	Exactness   string             `json:"exactness"`
	ErrorBound  string             `json:"error_bound"`
	Buckets     []percentileBucket `json:"buckets"`
}

// percentileBucket is one answered window.
type percentileBucket struct {
	BucketStart    string  `json:"bucket_start"`
	Value          float64 `json:"value"`
	Observations   int64   `json:"observations"`
	SketchesMerged int     `json:"sketches_merged"`
}

// percentileRequest is a validated request.
type percentileRequest struct {
	metric      string
	quantile    float64
	from        time.Time
	to          time.Time
	granularity string
	filters     map[string]string
	tenantID    string
}

// handleWindowPercentile serves GET /api/v1/percentile.
func (g *gateway) handleWindowPercentile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is accepted")
		return
	}

	// Authenticated by Gravix API key, the same path /api/v1/metrics uses.
	tenantID, ok := gatewayTenantFromAPIKey(g, w, r)
	if !ok {
		return
	}

	req, code, msg := parsePercentileRequest(r)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	req.tenantID = tenantID

	resp, code, msg := g.computeWindowPercentile(r.Context(), req)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// parsePercentileRequest validates every parameter, naming the offending one so a
// caller can fix it without guessing.
func parsePercentileRequest(r *http.Request) (percentileRequest, int, string) {
	q := r.URL.Query()
	var req percentileRequest

	req.metric = q.Get("metric")
	if req.metric == "" {
		req.metric = recompute.MetricRequestMinute
	}
	if req.metric != recompute.MetricRequestMinute {
		return req, http.StatusNotFound, fmt.Sprintf("unknown metric %q", req.metric)
	}

	quantile, err := strconv.ParseFloat(q.Get("quantile"), 64)
	if err != nil || quantile <= 0 || quantile >= 1 {
		return req, http.StatusBadRequest, `invalid parameter "quantile": must be strictly between 0 and 1`
	}
	req.quantile = quantile

	req.from, err = time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		return req, http.StatusBadRequest, `invalid parameter "from": must be RFC3339`
	}
	req.to, err = time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		return req, http.StatusBadRequest, `invalid parameter "to": must be RFC3339`
	}
	if !req.to.After(req.from) {
		return req, http.StatusBadRequest, `invalid parameter "to": must be after "from"`
	}
	if req.to.Sub(req.from) > maxPercentileWindow {
		return req, http.StatusBadRequest, `invalid parameter "to": window must not exceed 31 days`
	}

	req.granularity = q.Get("granularity")
	if req.granularity == "" {
		req.granularity = "all"
	}
	switch req.granularity {
	case "minute", "hour", "day", "all":
	default:
		return req, http.StatusBadRequest, `invalid parameter "granularity": must be one of minute, hour, day, all`
	}

	req.filters = map[string]string{}
	for _, dim := range []string{"service", "method", "path_template"} {
		if v := q.Get(dim); v != "" {
			req.filters[dim] = v
		}
	}
	return req, 0, ""
}

// gatewayTenantFromAPIKey resolves the caller's tenant from their API key,
// writing the 401 itself when the key is missing or revoked.
func gatewayTenantFromAPIKey(g *gateway, w http.ResponseWriter, r *http.Request) (string, bool) {
	rawKey := r.Header.Get("X-Gravix-Key")
	if rawKey == "" {
		rawKey = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if rawKey == "" {
		writeError(w, http.StatusUnauthorized, "API key required (X-Gravix-Key or Authorization: Bearer)")
		return "", false
	}
	info, err := g.db.APIKeys().ValidateKey(r.Context(), rawKey)
	if err != nil || info.Status != "active" {
		writeError(w, http.StatusUnauthorized, "invalid or revoked API key")
		return "", false
	}
	return info.TenantID, true
}

// computeWindowPercentile merges the sketches covering the window and answers the
// quantile for each requested bucket.
func (g *gateway) computeWindowPercentile(ctx context.Context, req percentileRequest) (*percentileResponse, int, string) {
	if g.metricStore == nil {
		return nil, http.StatusServiceUnavailable, "metric storage is not configured"
	}

	groups := map[time.Time][]*sketch.Sketch{}
	counts := map[time.Time]int64{}
	var preSketchDays []string

	for day := utcDay(req.from); day.Before(req.to); day = day.AddDate(0, 0, 1) {
		rows, found, err := g.readPartitionRows(ctx, req.tenantID, day)
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Sprintf("reading partition for %s: %v", day.Format("2006-01-02"), err)
		}
		if !found {
			continue
		}

		dayHasSketch := false
		dayHasRows := false
		for i := range rows {
			row := &rows[i]
			if !rowMatchesFilters(row, req.filters) {
				continue
			}
			bucket, err := time.Parse("2006-01-02 15:04:05", row.BucketStart)
			if err != nil {
				continue
			}
			bucket = bucket.UTC()
			if bucket.Before(req.from) || !bucket.Before(req.to) {
				continue
			}
			dayHasRows = true

			if row.SketchVersion == "" || len(row.LatencySketch) == 0 {
				continue
			}
			if row.SketchVersion != sketch.Version {
				return nil, http.StatusInternalServerError,
					fmt.Sprintf("sketch version mismatch: file %s, server %s", row.SketchVersion, sketch.Version)
			}
			var s sketch.Sketch
			if err := s.UnmarshalBinary(row.LatencySketch); err != nil {
				return nil, http.StatusInternalServerError, fmt.Sprintf("decoding sketch: %v", err)
			}
			dayHasSketch = true

			key := truncateTo(bucket, req.granularity, req.from)
			groups[key] = append(groups[key], &s)
			counts[key] += row.RequestCount
		}

		// A day with rows but no sketch anywhere predates GRVX-804. Saying so is
		// the honest answer; falling back to max of the scalars would be the wrong
		// number this whole spec exists to remove.
		if dayHasRows && !dayHasSketch {
			preSketchDays = append(preSketchDays, day.Format("2006-01-02"))
		}
	}

	if len(preSketchDays) > 0 {
		return nil, http.StatusUnprocessableEntity,
			fmt.Sprintf("partitions %s predate sketch storage; run gravix recompute for those days",
				strings.Join(preSketchDays, ", "))
	}

	keys := make([]time.Time, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })

	buckets := make([]percentileBucket, 0, len(keys))
	for _, k := range keys {
		merged, err := sketch.MergeAll(groups[k])
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Sprintf("merging sketches: %v", err)
		}
		value, err := merged.Quantile(req.quantile)
		if errors.Is(err, sketch.ErrEmptySketch) {
			continue
		}
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Sprintf("querying quantile: %v", err)
		}
		buckets = append(buckets, percentileBucket{
			BucketStart:    k.Format(time.RFC3339),
			Value:          value,
			Observations:   counts[k],
			SketchesMerged: len(groups[k]),
		})
	}

	return &percentileResponse{
		Metric:      req.metric,
		Quantile:    req.quantile,
		Granularity: req.granularity,
		Exactness:   "sketch",
		ErrorBound:  fmt.Sprintf("relative error <= %g%% for q in [0.5, 0.99]", sketch.MaxRelativeError*100),
		Buckets:     buckets,
	}, 0, ""
}

// readPartitionRows loads one day's metric rows, reporting whether the partition
// exists at all.
func (g *gateway) readPartitionRows(ctx context.Context, tenantID string, day time.Time) ([]recompute.MetricRow, bool, error) {
	metricDir := recompute.MetricDirFor(g.warehouseDir(), tenantID, recompute.MetricRequestMinute)
	key := recompute.DeterministicKey(recompute.PartitionDir(metricDir, day), recompute.MetricRequestMinute, day)

	exists, err := g.metricStore.Exists(ctx, key)
	if err != nil || !exists {
		return nil, false, err
	}

	rc, err := g.metricStore.Get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, false, err
	}

	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false, err
	}
	reader := parquet.NewGenericReader[recompute.MetricRow](file)
	defer reader.Close()

	rows := make([]recompute.MetricRow, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	return rows[:n], true, nil
}

// newMetricStore opens the store the rollup writes metric partitions to. It
// mirrors transforms/request_metrics_minute: S3 when S3_ENDPOINT is set, the
// local data root otherwise. Rooting it anywhere else would make every key miss.
func newMetricStore() (storage.ObjectStore, error) {
	if os.Getenv("S3_ENDPOINT") != "" {
		return storage.NewS3Store(
			context.Background(),
			os.Getenv("S3_ENDPOINT"),
			os.Getenv("S3_REGION"),
			os.Getenv("S3_BUCKET"),
			os.Getenv("S3_ACCESS_KEY"),
			os.Getenv("S3_SECRET_KEY"),
		)
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	return storage.NewLocalStore(dataDir)
}

// warehouseDir is where metric partitions live, relative to the object store root.
func (g *gateway) warehouseDir() string {
	if g.metricWarehouseDir != "" {
		return g.metricWarehouseDir
	}
	return "./data/warehouse"
}

// rowMatchesFilters applies the dimension filters. Only the three declared
// dimensions are accepted as parameters, so this cannot become a drill-down.
func rowMatchesFilters(row *recompute.MetricRow, filters map[string]string) bool {
	for k, want := range filters {
		var got string
		switch k {
		case "service":
			got = row.Service
		case "method":
			got = row.Method
		case "path_template":
			got = row.PathTemplate
		default:
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}

// truncateTo buckets a time by the requested granularity. "all" collapses the
// whole window into one answer, anchored at its start.
func truncateTo(t time.Time, granularity string, windowStart time.Time) time.Time {
	switch granularity {
	case "minute":
		return t.Truncate(time.Minute).UTC()
	case "hour":
		return t.Truncate(time.Hour).UTC()
	case "day":
		return utcDay(t)
	default:
		return windowStart.UTC()
	}
}

func utcDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
