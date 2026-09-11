// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

var percentileDay = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)

// cubeModelPath resolves the Cube model file from the package directory.
func cubeModelPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "cube", "model", "schema", "RequestMetricsMinute.js")
}

func readCubeModel(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(cubeModelPath(t))
	if err != nil {
		t.Fatalf("read cube model: %v", err)
	}
	return string(data)
}

// ─── AC-1, AC-2, AC-5, AC-9, AC-10: the model ───

func TestNoMaxOverPercentiles(t *testing.T) {
	model := readCubeModel(t)

	// The defect this spec exists to remove: a percentile column aggregated with
	// max. It must not survive anywhere, under any measure name.
	for _, col := range []string{"p50_latency_ms", "p95_latency_ms", "p99_latency_ms"} {
		idx := strings.Index(model, "`"+col+"`")
		if idx < 0 {
			t.Errorf("the model no longer references %s at all", col)
			continue
		}
		// Look at the measure block that follows the column reference.
		block := model[idx:min(idx+220, len(model))]
		if strings.Contains(block, "type: `max`") {
			t.Errorf("%s is still aggregated with max:\n%s", col, block)
		}
	}

	if strings.Contains(model, "type: `max`") {
		t.Error("the model still contains a max aggregation somewhere")
	}
	// And the old measure names must be gone, not merely unused: a wrong number
	// that is still reachable will be read by someone.
	for _, name := range []string{"p95Latency:", "p50Latency:", "p99Latency:"} {
		if strings.Contains(model, name) {
			t.Errorf("the old measure %s is still defined", name)
		}
	}
}

func TestBucketMeasuresMarkedNonAggregatable(t *testing.T) {
	model := readCubeModel(t)

	for _, measure := range []string{"bucketP50LatencyMs", "bucketP95LatencyMs", "bucketP99LatencyMs"} {
		idx := strings.Index(model, measure+": {")
		if idx < 0 {
			t.Errorf("%s is not defined", measure)
			continue
		}
		block := model[idx:min(idx+320, len(model))]

		if !strings.Contains(block, "aggregatable: false") {
			t.Errorf("%s is not marked aggregatable: false:\n%s", measure, block)
		}
		if !strings.Contains(block, "correctOnlyAtGranularity: `minute`") {
			t.Errorf("%s does not record the granularity it is correct at", measure)
		}
		if !strings.Contains(block, "type: `min`") {
			t.Errorf("%s is not type min:\n%s", measure, block)
		}
	}

	// The reasoning for min-over-max must be in the file, or someone will
	// "correct" it back.
	if !strings.Contains(model, "obviously-too-low") || !strings.Contains(model, "Do not \"fix\" this to max") {
		t.Error("the min-over-max reasoning is not recorded in the model")
	}
}

func TestCorrectMeasuresUnchanged(t *testing.T) {
	model := readCubeModel(t)

	// errorRate is already correct across buckets and must not be touched.
	if !strings.Contains(model, "sum(error_count) / NULLIF(sum(request_count), 0)") {
		t.Error("errorRate's formula changed; it was already correct")
	}
	for _, want := range []string{
		"requestCount: {", "errorCount: {", "errorRate: {",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("the measure %s was removed", want)
		}
	}
	// requestCount and errorCount are sums, which is right for counts.
	for _, measure := range []string{"requestCount", "errorCount"} {
		idx := strings.Index(model, measure+": {")
		block := model[idx:min(idx+160, len(model))]
		if !strings.Contains(block, "type: `sum`") {
			t.Errorf("%s is no longer a sum:\n%s", measure, block)
		}
	}
}

func TestSketchDimensionHidden(t *testing.T) {
	model := readCubeModel(t)

	idx := strings.Index(model, "latencySketch: {")
	if idx < 0 {
		t.Fatal("latencySketch is not exposed; the endpoint cannot read the sketch column")
	}
	block := model[idx:min(idx+260, len(model))]
	if !strings.Contains(block, "shown: false") {
		t.Errorf("latencySketch is not hidden:\n%s", block)
	}
	if !strings.Contains(block, "latency_sketch") {
		t.Errorf("latencySketch does not map to the sketch column:\n%s", block)
	}
}

func TestAllFourCubeConfigs(t *testing.T) {
	model := readCubeModel(t)

	// The model switches on CUBEJS_DB_TYPE and TENANT_DB_PATH. All four
	// combinations must still resolve to a SQL source.
	for _, want := range []string{
		"CUBEJS_DB_TYPE === 'duckdb'",
		"TENANT_DB_PATH",
		"read_parquet('/cube/data/warehouse/*/request_metrics_minute/**/*.parquet'",
		"read_parquet('/cube/data/warehouse/request_metrics_minute/**/*.parquet'",
		"gravix.raw.request_metrics_minute",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("the model no longer handles %q", want)
		}
	}

	// The sketch column must be readable in every configuration, which it is
	// because every branch selects *.
	if strings.Count(model, "SELECT * FROM") < 2 {
		t.Error("a configuration no longer selects every column, so the sketch may be missing")
	}
}

// ─── the endpoint ───

// seedPartition writes a day's rows for one tenant. The gateway reads partitions
// under the tenant resolved from the API key, so the tenant must match or the
// handler will correctly find nothing.
func seedPartition(t *testing.T, store storage.ObjectStore, tenantID string, day time.Time, rows []recompute.MetricRow) {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[recompute.MetricRow](&buf,
		parquet.Compression(&zstd.Codec{Level: recompute.CompressionLevel}))
	if _, err := w.Write(rows); err != nil {
		t.Fatalf("write rows: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	metricDir := recompute.MetricDirFor("./data/warehouse", tenantID, recompute.MetricRequestMinute)
	key := recompute.DeterministicKey(recompute.PartitionDir(metricDir, day), recompute.MetricRequestMinute, day)
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put partition: %v", err)
	}
}

// latencyRows builds one minute-bucket row per minute, each carrying a sketch
// over the latencies given for that minute.
func latencyRows(t *testing.T, day time.Time, perMinute [][]float64, withSketch bool) []recompute.MetricRow {
	t.Helper()
	rows := make([]recompute.MetricRow, 0, len(perMinute))
	for i, latencies := range perMinute {
		sorted := append([]float64(nil), latencies...)
		sort.Float64s(sorted)

		row := recompute.MetricRow{
			BucketStart:  day.Add(time.Duration(i) * time.Minute).Format("2006-01-02 15:04:05"),
			Service:      "api",
			Method:       "GET",
			PathTemplate: "/users/{id}",
			RequestCount: int64(len(sorted)),
			EventDay:     day.Format("2006-01-02"),
		}
		if len(sorted) > 0 {
			row.P95LatencyMs = sorted[len(sorted)*95/100]
		}
		if withSketch {
			data, err := sketch.FromSorted(sorted).MarshalBinary()
			if err != nil {
				t.Fatalf("marshal sketch: %v", err)
			}
			row.LatencySketch = data
			row.SketchVersion = sketch.Version
		}
		rows = append(rows, row)
	}
	return rows
}

// percentileGateway returns a gateway wired to a store rooted at a temp dir,
// along with the tenant that owns it and an API key that authenticates as them.
func percentileGateway(t *testing.T) (*gateway, storage.ObjectStore, string, string) {
	t.Helper()
	gw, store := newTestGatewayWithStore(t)
	// The percentile endpoint reads the metric store, which is rooted at the data
	// root rather than RAW_DATA_DIR. In a test both are the same temp dir.
	gw.metricStore = store
	tenant, _, apiKey, _ := createTestTenantWithUser(t, gw)
	return gw, store, tenant.ID, apiKey
}

func callPercentile(t *testing.T, gw *gateway, apiKey string, params map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	q := make([]string, 0, len(params))
	for k, v := range params {
		q = append(q, k+"="+v)
	}
	sort.Strings(q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/percentile?"+strings.Join(q, "&"), nil)
	req.Header.Set("X-Gravix-Key", apiKey)
	rr := httptest.NewRecorder()
	gw.handleWindowPercentile(rr, req)

	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr, body
}

func windowParams(q string) map[string]string {
	return map[string]string{
		"metric":   "request_metrics_minute",
		"quantile": q,
		"from":     percentileDay.Format(time.RFC3339),
		"to":       percentileDay.Add(60 * time.Minute).Format(time.RFC3339),
	}
}

// trueQuantile is the exact answer from the full sorted dataset.
func trueQuantile(sorted []float64, q float64) float64 {
	idx := q * float64(len(sorted)-1)
	lo, hi := int(math.Floor(idx)), int(math.Ceil(idx))
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// skewedMinutes builds 60 minutes of heavy-tailed latency, which is where
// max-of-percentiles goes most wrong.
func skewedMinutes(seed int64) ([][]float64, []float64) {
	rng := rand.New(rand.NewSource(seed))
	var all []float64
	minutes := make([][]float64, 60)
	for m := 0; m < 60; m++ {
		n := 200
		minute := make([]float64, n)
		for i := 0; i < n; i++ {
			v := 10 / math.Pow(rng.Float64(), 1/1.5) // pareto
			minute[i] = v
			all = append(all, v)
		}
		sort.Float64s(minute)
		minutes[m] = minute
	}
	sort.Float64s(all)
	return minutes, all
}

// AC-3: a 60-minute p95 is within the sketch bound.
func TestWindowPercentileAccurate(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, all := skewedMinutes(42)
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes, true))

	rr, body := callPercentile(t, gw, apiKey, windowParams("0.95"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	buckets, _ := body["buckets"].([]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1 for granularity=all", len(buckets))
	}
	b := buckets[0].(map[string]any)
	got := b["value"].(float64)
	want := trueQuantile(all, 0.95)

	rel := math.Abs(got-want) / want
	t.Logf("true p95 = %.4f, merged sketch = %.4f, relative error = %.6f", want, got, rel)
	if rel > sketch.MaxRelativeError {
		t.Errorf("relative error %.6f exceeds the published bound %.6f", rel, sketch.MaxRelativeError)
	}
	if int(b["sketches_merged"].(float64)) != 60 {
		t.Errorf("sketches_merged = %v, want 60", b["sketches_merged"])
	}
	if body["exactness"] != "sketch" {
		t.Errorf("exactness = %v, want sketch", body["exactness"])
	}
}

// AC-4: and it differs materially from what the old model produced.
func TestEndpointBeatsOldMaxBehaviour(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, all := skewedMinutes(7)
	rows := latencyRows(t, percentileDay, minutes, true)
	seedPartition(t, store, tenantID, percentileDay, rows)

	// What the old model did: max of the per-minute p95 scalars.
	oldMax := 0.0
	for _, r := range rows {
		if r.P95LatencyMs > oldMax {
			oldMax = r.P95LatencyMs
		}
	}

	_, body := callPercentile(t, gw, apiKey, windowParams("0.95"))
	buckets := body["buckets"].([]any)
	got := buckets[0].(map[string]any)["value"].(float64)

	want := trueQuantile(all, 0.95)
	oldErr := math.Abs(oldMax-want) / want
	newErr := math.Abs(got-want) / want

	t.Logf("true p95 = %.4f | old max-of-p95 = %.4f (error %.4f) | merged sketch = %.4f (error %.6f)",
		want, oldMax, oldErr, got, newErr)

	if newErr >= oldErr {
		t.Errorf("the endpoint is no better than max-of-percentiles: %.6f vs %.6f", newErr, oldErr)
	}
	if oldErr < 0.05 {
		t.Errorf("the old behaviour was only %.4f off on this data, so the comparison proves little", oldErr)
	}
}

// AC-6: pre-sketch partitions are refused, not silently approximated.
func TestPreSketchWindowReturns422(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(3)
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes[:5], false))

	rr, body := callPercentile(t, gw, apiKey, windowParams("0.95"))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body.String())
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, percentileDay.Format("2006-01-02")) {
		t.Errorf("error = %q, want it to name the affected day", msg)
	}
	if !strings.Contains(msg, "gravix recompute") {
		t.Errorf("error = %q, want it to say how to fix it", msg)
	}
}

// AC-7, AC-8: parameter validation.
func TestInvalidQuantileReturns400(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(1)
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes[:2], true))

	for _, q := range []string{"0", "1", "1.5", "-0.1", "abc", ""} {
		params := windowParams(q)
		rr, body := callPercentile(t, gw, apiKey, params)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("quantile=%q: status = %d, want 400", q, rr.Code)
			continue
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, `"quantile"`) {
			t.Errorf("quantile=%q: error = %q, want it to name the parameter", q, msg)
		}
	}
}

func TestWindowLimitEnforced(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(1)
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes[:2], true))

	params := windowParams("0.95")
	params["to"] = percentileDay.AddDate(0, 0, 40).Format(time.RFC3339)

	rr, body := callPercentile(t, gw, apiKey, params)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "31 days") {
		t.Errorf("error = %q, want it to state the limit", msg)
	}
}

func TestPercentileUnknownMetricReturns404(t *testing.T) {
	gw, _, _, apiKey := percentileGateway(t)

	params := windowParams("0.95")
	params["metric"] = "cpu_seconds"
	rr, body := callPercentile(t, gw, apiKey, params)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "cpu_seconds") {
		t.Errorf("error = %q, want it to name the metric", msg)
	}
}

func TestPercentileRequiresAPIKey(t *testing.T) {
	gw, _, _, _ := percentileGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/percentile?quantile=0.95", nil)
	rr := httptest.NewRecorder()
	gw.handleWindowPercentile(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestPercentileRejectsNonGET(t *testing.T) {
	gw, _, _, apiKey := percentileGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/percentile?quantile=0.95", nil)
	req.Header.Set("X-Gravix-Key", apiKey)
	rr := httptest.NewRecorder()
	gw.handleWindowPercentile(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestPercentileGranularitySplitsBuckets(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(11)
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes, true))

	for _, tc := range []struct {
		granularity string
		wantBuckets int
	}{
		{"all", 1},
		{"hour", 1},
		{"minute", 60},
	} {
		params := windowParams("0.95")
		params["granularity"] = tc.granularity
		rr, body := callPercentile(t, gw, apiKey, params)
		if rr.Code != http.StatusOK {
			t.Fatalf("granularity=%s: status = %d: %s", tc.granularity, rr.Code, rr.Body.String())
		}
		buckets := body["buckets"].([]any)
		if len(buckets) != tc.wantBuckets {
			t.Errorf("granularity=%s: buckets = %d, want %d", tc.granularity, len(buckets), tc.wantBuckets)
		}
	}

	params := windowParams("0.95")
	params["granularity"] = "century"
	rr, _ := callPercentile(t, gw, apiKey, params)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("an unknown granularity returned %d, want 400", rr.Code)
	}
}

func TestPercentileDimensionFilter(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(5)
	rows := latencyRows(t, percentileDay, minutes[:10], true)
	// A second service in the same buckets, which the filter must exclude.
	other := latencyRows(t, percentileDay, minutes[:10], true)
	for i := range other {
		other[i].Service = "billing"
	}
	seedPartition(t, store, tenantID, percentileDay, append(rows, other...))

	params := windowParams("0.95")
	params["service"] = "api"
	rr, body := callPercentile(t, gw, apiKey, params)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	b := body["buckets"].([]any)[0].(map[string]any)
	if int(b["sketches_merged"].(float64)) != 10 {
		t.Errorf("sketches_merged = %v, want 10 — the filter did not exclude the other service",
			b["sketches_merged"])
	}
}

// AC-12: the latency budget for a full day's merge.
func TestPercentileEndpointLatencyBudget(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)

	// A full day: 1,440 one-minute sketches, which is the worst realistic case.
	rng := rand.New(rand.NewSource(99))
	minutes := make([][]float64, 1440)
	for m := range minutes {
		minute := make([]float64, 200)
		for i := range minute {
			minute[i] = 10 / math.Pow(rng.Float64(), 1/1.5)
		}
		sort.Float64s(minute)
		minutes[m] = minute
	}
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes, true))

	params := windowParams("0.95")
	params["to"] = percentileDay.AddDate(0, 0, 1).Format(time.RFC3339)

	const runs = 5
	var durations []time.Duration
	for i := 0; i < runs; i++ {
		start := time.Now()
		rr, _ := callPercentile(t, gw, apiKey, params)
		durations = append(durations, time.Since(start))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[len(durations)*95/100]

	t.Logf("merging 1,440 sketches: p95 = %s (runs: %v)", p95, durations)

	// The merge itself is checked either way: every one of the five runs returned
	// 200 with an answer, above. What changes under the race detector is whether
	// the stopwatch means anything.
	const budget = 400 * time.Millisecond
	if raceDetectorEnabled {
		// Not a skip, and not a softened assertion: the work ran and its result was
		// checked. The race detector adds roughly 8x to this merge, so timing it
		// measures the instrumentation. G4.5 is a budget for the binary users run.
		t.Logf("race detector enabled, so the %s budget is not asserted: it is a production "+
			"number and this binary is instrumented. Run without -race to check it.", budget)
		return
	}
	if p95 > budget {
		t.Errorf("p95 = %s, above the %s budget (G4.5). Report to perf-cost-engineer; "+
			"do not fall back to max — a fast wrong answer is not an improvement", p95, budget)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = fmt.Sprintf

// One tenant's sketches must never answer another tenant's question. This is not
// one of the spec's acceptance criteria, but the endpoint reads files off a
// shared store keyed by tenant, so the isolation is worth pinning.
func TestPercentileIsTenantScoped(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(11)

	// Seed the caller's own tenant with nothing and a different tenant with data.
	seedPartition(t, store, tenantID+"-someone-else", percentileDay,
		latencyRows(t, percentileDay, minutes, true))

	rr, body := callPercentile(t, gw, apiKey, windowParams("0.95"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if buckets, _ := body["buckets"].([]any); len(buckets) != 0 {
		t.Errorf("buckets = %d, want 0: another tenant's sketches were read", len(buckets))
	}
}

// With no metric store the endpoint must say so, not fall back to the scalars.
func TestPercentileWithoutMetricStore(t *testing.T) {
	gw, _, _, apiKey := percentileGateway(t)
	gw.metricStore = nil

	rr, _ := callPercentile(t, gw, apiKey, windowParams("0.95"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: %s", rr.Code, rr.Body.String())
	}
}

// ─── alert evaluation ───
//
// Removing the Cube percentile measures would have left the alert evaluator
// querying members that no longer exist, so latency alerts would have stopped
// firing silently. They are answered from the sketches instead, which also fixes
// the number: an alert on a 60-minute p95 was evaluating max-of-60-p95s.

func percentileAlertRule(tenantID, metric string, windowMinutes int) *tenantdb.AlertRule {
	return &tenantdb.AlertRule{
		ID:            "rule-1",
		TenantID:      tenantID,
		Name:          "latency",
		Metric:        metric,
		Operator:      "gt",
		Threshold:     100,
		WindowMinutes: windowMinutes,
		Status:        "active",
	}
}

func TestAlertPercentileComesFromSketches(t *testing.T) {
	gw, store, tenantID, _ := percentileGateway(t)

	// The window is measured back from now, so the partition has to be today's.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	minutes, _ := skewedMinutes(23)
	// Place the buckets in the ten minutes just gone.
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	rows := latencyRows(t, start, minutes[:10], true)
	for i := range rows {
		rows[i].EventDay = today.Format("2006-01-02")
	}
	seedPartition(t, store, tenantID, today, rows)

	// Cube is unreachable in this test. If the evaluator still asked it, this
	// would fail rather than silently returning a wrong number.
	gw.cubeAPIURL = "http://127.0.0.1:1/cubejs-api/v1/load"

	got, err := gw.queryCubeMetric(context.Background(), "", percentileAlertRule(tenantID, "p95_latency", 15))
	if err != nil {
		t.Fatalf("queryCubeMetric: %v", err)
	}
	if got <= 0 {
		t.Fatalf("value = %v, want a merged percentile", got)
	}

	// The answer should track the true p95 of the seeded minutes, not the max of
	// their per-minute p95s.
	var seeded []float64
	for _, m := range minutes[:10] {
		seeded = append(seeded, m...)
	}
	sort.Float64s(seeded)
	want := trueQuantile(seeded, 0.95)
	if rel := math.Abs(got-want) / want; rel > sketch.MaxRelativeError {
		t.Errorf("alert value %.4f vs true p95 %.4f: relative error %.6f exceeds %.6f",
			got, want, rel, sketch.MaxRelativeError)
	}
}

func TestAlertNonPercentileMetricsStillUseCube(t *testing.T) {
	// error_rate and throughput aggregate correctly across buckets, so they must
	// keep their Cube measures rather than being dragged through the merge.
	for _, metric := range []string{"error_rate", "throughput"} {
		if _, ok := percentileAlertQuantile[metric]; ok {
			t.Errorf("%s was routed to the sketch merge; it aggregates correctly in Cube", metric)
		}
		if _, ok := metricToCubeMeasure[metric]; !ok {
			t.Errorf("%s lost its Cube measure", metric)
		}
	}
	// And every metric a rule may be created with must be evaluable by one path
	// or the other, or the rule silently never fires.
	for metric := range validMetrics {
		if !knownAlertMetric(metric) {
			t.Errorf("%s passes rule validation but no evaluator can answer it", metric)
		}
	}
}

// ─── CD-001: the bound must describe the answer it is attached to ───

func TestErrorBoundMatchesSampleSize(t *testing.T) {
	// A flat "relative error <= 1%" was returned for every query regardless of
	// how much data backed it. CD-001 measured up to 157% at a hundred
	// observations, so the response now says which regime the caller is in.
	cases := []struct {
		observations int64
		wantContains string
		wantAbsent   string
	}{
		{100, "NOT bounded", "value error <= 1%"},
		{999, "NOT bounded", "value error <= 1%"},
		{1_000, "up to ~17%", "NOT bounded"},
		{10_000, "up to ~6%", "NOT bounded"},
		{100_000, "value error <= 1%", "NOT bounded"},
		{5_000_000, "value error <= 1%", "NOT bounded"},
	}
	for _, c := range cases {
		got := errorBoundFor(c.observations)
		if !strings.Contains(got, c.wantContains) {
			t.Errorf("errorBoundFor(%d) = %q, want it to contain %q", c.observations, got, c.wantContains)
		}
		if c.wantAbsent != "" && strings.Contains(got, c.wantAbsent) {
			t.Errorf("errorBoundFor(%d) = %q, want it NOT to contain %q", c.observations, got, c.wantAbsent)
		}
		// The rank bound is the one that always holds, so it is always stated.
		if !strings.Contains(got, "rank error <= 1%") {
			t.Errorf("errorBoundFor(%d) omits the rank bound, which holds at every size: %q",
				c.observations, got)
		}
	}
}

func TestResponseBoundReflectsTheDataBehindIt(t *testing.T) {
	gw, store, tenantID, apiKey := percentileGateway(t)
	minutes, _ := skewedMinutes(31)
	// Ten minutes of 200 requests each: 2,000 observations, the middle regime.
	seedPartition(t, store, tenantID, percentileDay, latencyRows(t, percentileDay, minutes[:10], true))

	rr, body := callPercentile(t, gw, apiKey, windowParams("0.95"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	bound, _ := body["error_bound"].(string)
	if !strings.Contains(bound, "up to ~17%") {
		t.Errorf("error_bound = %q, want the 1,000+ observation regime for 2,000 observations", bound)
	}
	if strings.Contains(bound, "relative value error <= 1%") {
		t.Error("the response claims a 1% value bound on 2,000 observations, which CD-001 measured at up to 17%")
	}
}
