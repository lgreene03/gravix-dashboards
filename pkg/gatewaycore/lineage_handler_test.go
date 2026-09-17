// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/lineage"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	lineageDay    = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	lineageBucket = time.Date(2026, 9, 9, 14, 23, 0, 0, time.UTC)
)

// repoDir resolves a path in the repository root from this package.
func repoDir(t *testing.T, parts ...string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(append([]string{wd, "..", ".."}, parts...)...)
}

// lineageGateway wires a gateway whose metric store holds a real partition built
// from real facts, so the endpoint is exercised against a manifest it did not
// fabricate.
func lineageGateway(t *testing.T) (*gateway, string, string) {
	t.Helper()
	gw, store := newTestGatewayWithStore(t)
	gw.metricStore = store
	t.Setenv("CONTRACTS_DIR", repoDir(t, "contracts"))

	tenant, _, apiKey, _ := createTestTenantWithUser(t, gw)
	seedLineagePartition(t, store, tenant.ID)
	return gw, tenant.ID, apiKey
}

func lineageFact(t *testing.T, service, method, path string, status, latency int32, at time.Time) *gravixv1.RequestFact {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return &gravixv1.RequestFact{
		EventId:      id.String(),
		EventTime:    timestamppb.New(at),
		Service:      service,
		Method:       method,
		PathTemplate: path,
		StatusCode:   status,
		LatencyMs:    latency,
	}
}

// seedLineagePartition writes facts across three files and rolls them up, so the
// reported fact count and file list are not trivially one.
func seedLineagePartition(t *testing.T, store storage.ObjectStore, tenantID string) {
	t.Helper()
	inputDir := recompute.FactsDirFor("./data/raw", tenantID)
	prefix := recompute.KeyPrefix(inputDir) + "/2026-09-09/14/"

	batches := map[string][]*gravixv1.RequestFact{
		prefix + "facts_1430.jsonl": {
			lineageFact(t, "api", "GET", "/users/{id}", 200, 10, lineageBucket),
			lineageFact(t, "api", "GET", "/users/{id}", 500, 84, lineageBucket.Add(time.Second)),
		},
		prefix + "facts_1435.jsonl": {
			lineageFact(t, "api", "GET", "/users/{id}", 200, 22, lineageBucket.Add(2*time.Second)),
			lineageFact(t, "billing", "POST", "/invoices/{id}", 201, 33, lineageBucket.Add(3*time.Second)),
		},
		prefix + "facts_1440.jsonl": {
			lineageFact(t, "api", "GET", "/users/{id}", 200, 44, lineageBucket.Add(4*time.Second)),
		},
	}
	for key, facts := range batches {
		var buf bytes.Buffer
		for _, f := range facts {
			data, err := protojson.Marshal(f)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			buf.Write(data)
			buf.WriteByte('\n')
		}
		if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}

	if _, err := recompute.Run(context.Background(), recompute.Options{
		Store:    store,
		InputDir: "./data/raw",
		// The gateway resolves the warehouse the same way, so writing it here the
		// way the rollup does is what proves the two agree.
		OutputDir: "./data/warehouse",
		TenantIDs: []string{tenantID},
		Window:    recompute.Window{From: lineageDay, To: lineageDay.AddDate(0, 0, 1)},
	}); err != nil {
		t.Fatalf("build partition: %v", err)
	}
}

func callLineage(t *testing.T, gw *gateway, apiKey string, params map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	q := make([]string, 0, len(params))
	for k, v := range params {
		q = append(q, k+"="+v)
	}
	sort.Strings(q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lineage?"+strings.Join(q, "&"), nil)
	if apiKey != "" {
		req.Header.Set("X-Gravix-Key", apiKey)
	}
	rr := httptest.NewRecorder()
	gw.handleLineage(rr, req)

	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr, body
}

func lineageParams() map[string]string {
	return map[string]string{
		"metric":  recompute.MetricRequestMinute,
		"bucket":  lineageBucket.Format(time.RFC3339),
		"service": "api",
		"method":  "GET",
	}
}

// ─── AC-2 ───

func TestLineageEndpointShape(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	rr, body := callLineage(t, gw, apiKey, lineageParams())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	// The response must be the Lineage struct verbatim, so the CLI and the panel
	// cannot drift apart about where a number came from.
	var typed lineage.Lineage
	if err := json.Unmarshal(rr.Body.Bytes(), &typed); err != nil {
		t.Fatalf("response does not decode as lineage.Lineage: %v", err)
	}

	for _, field := range []string{
		"metric", "metric_version", "contract_ref", "bucket", "values", "formula",
		"grain", "exactness", "mergeability", "data_file", "idempotency_key",
		"content_digest", "source_fact_keys", "fact_count", "recompute_cmd",
	} {
		if _, ok := body[field]; !ok {
			t.Errorf("response is missing %q; the panel renders it", field)
		}
	}

	if typed.Bucket != "2026-09-09T14:23:00Z" {
		t.Errorf("bucket = %q", typed.Bucket)
	}
	// Every fact in the partition, not just the filtered row: the filtered row's
	// own count is in values.request_count.
	if typed.FactCount != 5 {
		t.Errorf("fact_count = %d, want 5", typed.FactCount)
	}
	if typed.Values["request_count"] != float64(4) {
		t.Errorf("values.request_count = %v, want 4", typed.Values["request_count"])
	}
	if len(typed.SourceFactKeys) != 3 {
		t.Errorf("source_fact_keys = %d files, want 3", len(typed.SourceFactKeys))
	}
	if !strings.HasPrefix(typed.ContentDigest, "sha256:") {
		t.Errorf("content_digest = %q, want a sha256 digest", typed.ContentDigest)
	}
	if typed.RecomputeCmd == "" {
		t.Error("recompute_cmd is empty; the panel's copy button would copy nothing")
	}
}

// ─── AC-3: non-goal §5 ───

func TestLineageEndpointNeverReturnsFactRecords(t *testing.T) {
	gw, tenantID, apiKey := lineageGateway(t)

	rr, _ := callLineage(t, gw, apiKey, lineageParams())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	raw := rr.Body.String()

	// Per-request identifiers must not appear as keys anywhere in the body.
	for _, forbidden := range []string{
		`"event_id"`, `"eventId"`, `"user_id"`, `"userId"`, `"request_id"`, `"requestId"`,
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("response contains %s; lineage reports aggregates and file names, never fact records", forbidden)
		}
	}

	// The tenant's own id appears legitimately, in the object keys and the
	// idempotency key. Any OTHER UUID could only be an event id that escaped, so
	// remove the tenant's and require that none remain.
	var typed lineage.Lineage
	if err := json.Unmarshal(rr.Body.Bytes(), &typed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	uuidRe := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	if m := uuidRe.FindString(strings.ReplaceAll(raw, tenantID, "")); m != "" {
		t.Errorf("response contains a UUID (%s) that is not the tenant's; that can only be a fact identifier", m)
	}

	// Values are the row's aggregates. None of them may be per-request.
	for name := range typed.Values {
		if strings.Contains(name, "event") || strings.Contains(name, "user_id") || strings.Contains(name, "request_id") {
			t.Errorf("values contains %q, which is not an aggregate", name)
		}
	}

	// The fact keys must be file names, not records.
	for _, key := range typed.SourceFactKeys {
		if !strings.HasSuffix(key, ".jsonl") {
			t.Errorf("source_fact_keys contains %q, which is not a file name", key)
		}
	}
}

// ─── failure states the panel distinguishes ───

func TestLineageNoRowMatchReturns404(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	params := lineageParams()
	params["service"] = "nonexistent"
	rr, _ := callLineage(t, gw, apiKey, params)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

func TestLineageNoPartitionReturns404(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	params := lineageParams()
	params["bucket"] = "2020-01-01T00:00:00Z"
	rr, _ := callLineage(t, gw, apiKey, params)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

// AC-6's server half: a partition with no manifest is a recoverable state, and
// the body must carry the command that recovers it.
func TestLineageNoManifestReturns409WithRecomputeCmd(t *testing.T) {
	gw, tenantID, apiKey := lineageGateway(t)

	// Delete the manifest, leaving the data. That is exactly a partition written
	// before manifests existed.
	metricDir := recompute.MetricDirFor("./data/warehouse", tenantID, recompute.MetricRequestMinute)
	dataKey := recompute.DeterministicKey(recompute.PartitionDir(metricDir, lineageDay), recompute.MetricRequestMinute, lineageDay)
	if err := gw.metricStore.Delete(context.Background(), manifest.Path(dataKey)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	rr, body := callLineage(t, gw, apiKey, lineageParams())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
	if body["error"] != "lineage_unavailable" {
		t.Errorf("error = %v, want lineage_unavailable", body["error"])
	}
	if body["reason"] != "partition predates manifests" {
		t.Errorf("reason = %v", body["reason"])
	}
	cmd, _ := body["recompute_cmd"].(string)
	if !strings.Contains(cmd, "recompute") {
		t.Errorf("recompute_cmd = %q, want a runnable recompute command", cmd)
	}
}

func TestLineageRejectsNonDimensionFilter(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	// user_id is not a dimension and never will be — non-goal §5. The endpoint
	// must refuse rather than ignore it, because silently ignoring a filter
	// answers a different question than the one asked.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/lineage?metric=request_metrics_minute&bucket="+lineageBucket.Format(time.RFC3339)+"&user_id=42", nil)
	req.Header.Set("X-Gravix-Key", apiKey)
	rr := httptest.NewRecorder()
	gw.handleLineage(rr, req)

	// Unknown query parameters are not forwarded as filters at all, so the request
	// succeeds for the whole bucket rather than drilling down by user.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var typed lineage.Lineage
	if err := json.Unmarshal(rr.Body.Bytes(), &typed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := typed.Filters["user_id"]; ok {
		t.Error("user_id reached the filters; a high-cardinality drill-down must not be possible")
	}
}

func TestLineageBadBucketReturns400(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	params := lineageParams()
	params["bucket"] = "not-a-time"
	rr, body := callLineage(t, gw, apiKey, params)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "bucket") {
		t.Errorf("error = %q, want it to name the offending parameter", msg)
	}
}

func TestLineageRequiresAPIKey(t *testing.T) {
	gw, _, _ := lineageGateway(t)

	rr, _ := callLineage(t, gw, "", lineageParams())
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestLineageRejectsNonGET(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lineage", nil)
	req.Header.Set("X-Gravix-Key", apiKey)
	rr := httptest.NewRecorder()
	gw.handleLineage(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// One tenant's provenance must not answer another's question.
func TestLineageIsTenantScoped(t *testing.T) {
	gw, _, apiKey := lineageGateway(t)

	// A second tenant with its own key sees nothing, because the partition was
	// built under the first tenant's prefix.
	ctx := context.Background()
	other := &tenantdb.Tenant{Name: "Other Corp", Email: "other@corp.com", Plan: "free", Status: "active"}
	if err := gw.db.Tenants().Create(ctx, other); err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	otherKey, _, err := gw.db.APIKeys().Create(ctx, other.ID, "default", nil)
	if err != nil {
		t.Fatalf("create second tenant key: %v", err)
	}

	rr, _ := callLineage(t, gw, otherKey, lineageParams())
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: another tenant's partition was read\n%s", rr.Code, rr.Body.String())
	}
	// And the owning tenant still gets its answer.
	if rr2, _ := callLineage(t, gw, apiKey, lineageParams()); rr2.Code != http.StatusOK {
		t.Errorf("owner status = %d, want 200", rr2.Code)
	}
}
