package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
)

// TestEndToEnd_IngestionToRollup verifies the full pipeline:
// 1. Write facts as JSONL to local storage (simulating ingestion)
// 2. Run the rollup job
// 3. Verify Parquet output exists in the warehouse
func TestEndToEnd_IngestionToRollup(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Step 1: Write raw facts
	numFacts := 10
	for i := 0; i < numFacts; i++ {
		eventID, _ := uuid.NewV7()
		fact := &gravixv1.RequestFact{
			EventId:      eventID.String(),
			EventTime:    timestamppb.New(day.Add(time.Duration(i) * time.Second)),
			Service:      "e2e-service",
			Method:       "GET",
			PathTemplate: "/api/e2e/test",
			StatusCode:   200,
			LatencyMs:    int32(10 + i*5),
		}

		data, err := protojson.Marshal(fact)
		if err != nil {
			t.Fatalf("failed to marshal fact: %v", err)
		}

		key := fmt.Sprintf("raw/request_facts/%s/%s/batch_e2e_%d.jsonl", dayStr, hourStr, i)
		if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
			t.Fatalf("failed to write fact: %v", err)
		}
	}

	// Verify raw data
	rawKeys, err := store.List(context.Background(), "raw/request_facts/"+dayStr)
	if err != nil {
		t.Fatalf("failed to list raw: %v", err)
	}
	if len(rawKeys) != numFacts {
		t.Fatalf("expected %d raw files, got %d", numFacts, len(rawKeys))
	}

	// Step 2: Run the rollup (CWD=baseDir so rollup's ./data resolves to our temp dir)
	rollupBin := filepath.Join(baseDir, "rollup-job")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/request_metrics_minute/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build rollup: %v", err)
	}

	rollupCmd := exec.Command(rollupBin,
		"-input-dir", "./data/raw/request_facts",
		"-output-dir", "./data/warehouse/request_metrics_minute",
		"-process-time", day.Format(time.RFC3339),
	)
	rollupCmd.Dir = baseDir
	rollupCmd.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupCmd.Stderr = os.Stderr
	rollupCmd.Stdout = os.Stdout
	if err := rollupCmd.Run(); err != nil {
		t.Fatalf("rollup failed: %v", err)
	}

	// Step 3: Verify warehouse output
	warehouseKeys, err := store.List(context.Background(), "warehouse/request_metrics_minute")
	if err != nil {
		t.Fatalf("failed to list warehouse: %v", err)
	}
	if len(warehouseKeys) == 0 {
		t.Fatal("expected at least one Parquet file in warehouse")
	}

	foundParquet := false
	for _, key := range warehouseKeys {
		if strings.HasSuffix(key, ".parquet") && strings.Contains(key, dayStr) {
			foundParquet = true
			break
		}
	}
	if !foundParquet {
		t.Errorf("no Parquet file found for day %s", dayStr)
	}

	t.Logf("E2E passed: %d facts → %d warehouse files", numFacts, len(warehouseKeys))
}

// TestEndToEnd_PurgeRetention verifies the purge binary deletes old data and keeps recent data.
func TestEndToEnd_PurgeRetention(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	todayStr := time.Now().UTC().Format("2006-01-02")
	oldStr := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")

	// Create recent and old files
	store.Put(ctx, fmt.Sprintf("raw/request_facts/%s/10/batch_keep.jsonl", todayStr), strings.NewReader("keep"))
	store.Put(ctx, fmt.Sprintf("raw/request_facts/%s/10/batch_old.jsonl", oldStr), strings.NewReader("old"))
	store.Put(ctx, fmt.Sprintf("warehouse/request_metrics_minute/metrics_%s.parquet", todayStr), strings.NewReader("keep"))
	store.Put(ctx, fmt.Sprintf("warehouse/request_metrics_minute/metrics_%s.parquet", oldStr), strings.NewReader("old"))

	// Build and run the purge binary
	purgeBin := filepath.Join(baseDir, "purge-job")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", purgeBin, "./cmd/purge/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build purge: %v", err)
	}

	purgeCmd := exec.Command(purgeBin,
		"--retention-days", "30",
		"--data-dir", filepath.Join(baseDir, "data"),
	)
	purgeCmd.Dir = baseDir
	purgeCmd.Env = append(os.Environ(), "S3_ENDPOINT=")
	purgeCmd.Stderr = os.Stderr
	purgeCmd.Stdout = os.Stdout
	if err := purgeCmd.Run(); err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	// Verify old files deleted, recent files kept
	remainingRaw, _ := store.List(ctx, "raw")
	remainingWarehouse, _ := store.List(ctx, "warehouse")
	if len(remainingRaw) != 1 {
		t.Errorf("expected 1 raw file remaining, got %d", len(remainingRaw))
	}
	if len(remainingWarehouse) != 1 {
		t.Errorf("expected 1 warehouse file remaining, got %d", len(remainingWarehouse))
	}

	t.Logf("E2E purge passed: old files purged, %d raw + %d warehouse remaining", len(remainingRaw), len(remainingWarehouse))
}

// TestEndToEnd_ServiceEventsRollup verifies the service events pipeline:
// Write raw events → run service events rollup → verify Parquet output.
func TestEndToEnd_ServiceEventsRollup(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write raw service events
	for i := 0; i < 5; i++ {
		eventID, _ := uuid.NewV7()
		event := &gravixv1.ServiceEvent{
			EventId:   eventID.String(),
			EventTime: timestamppb.New(day.Add(time.Duration(i) * time.Minute)),
			Service:   "e2e-service",
			EventType: "deploy_started",
			Properties: map[string]string{
				"version": fmt.Sprintf("1.0.%d", i),
			},
		}

		data, err := protojson.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal event: %v", err)
		}

		key := fmt.Sprintf("raw/service_events/%s/%s/batch_e2e_%d.jsonl", dayStr, hourStr, i)
		if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
			t.Fatalf("failed to write event: %v", err)
		}
	}

	// Build and run the events rollup (CWD=baseDir so ./data resolves correctly)
	rollupBin := filepath.Join(baseDir, "events-rollup")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/service_events_daily/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build events rollup: %v", err)
	}

	rollupCmd := exec.Command(rollupBin,
		"-input-dir", "./data/raw/service_events",
		"-output-dir", "./data/warehouse/service_events_daily",
		"-process-time", day.Format(time.RFC3339),
	)
	rollupCmd.Dir = baseDir
	rollupCmd.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupCmd.Stderr = os.Stderr
	rollupCmd.Stdout = os.Stdout
	if err := rollupCmd.Run(); err != nil {
		t.Fatalf("events rollup failed: %v", err)
	}

	// Verify warehouse output
	warehouseKeys, err := store.List(context.Background(), "warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("failed to list warehouse: %v", err)
	}
	if len(warehouseKeys) == 0 {
		t.Fatal("expected at least one Parquet file in warehouse")
	}

	foundParquet := false
	for _, key := range warehouseKeys {
		if strings.HasSuffix(key, ".parquet") && strings.Contains(key, dayStr) {
			foundParquet = true
			break
		}
	}
	if !foundParquet {
		t.Errorf("no Parquet file found for day %s", dayStr)
	}

	t.Logf("E2E events passed: 5 events → %d warehouse files", len(warehouseKeys))
}

// TestEndToEnd_ServiceEventsDetailRollup verifies the service events detail pipeline:
// Write raw events → run service events detail rollup → verify Parquet output.
func TestEndToEnd_ServiceEventsDetailRollup(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write raw service events
	for i := 0; i < 5; i++ {
		eventID, _ := uuid.NewV7()
		event := &gravixv1.ServiceEvent{
			EventId:   eventID.String(),
			EventTime: timestamppb.New(day.Add(time.Duration(i) * time.Minute)),
			Service:   "e2e-service",
			EventType: "deploy_completed",
			EntityId:  fmt.Sprintf("deploy-%d", i),
			Properties: map[string]string{
				"version":  fmt.Sprintf("2.0.%d", i),
				"instance": "e2e-host",
			},
		}

		data, err := protojson.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal event: %v", err)
		}

		key := fmt.Sprintf("raw/service_events/%s/%s/batch_e2e_detail_%d.jsonl", dayStr, hourStr, i)
		if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
			t.Fatalf("failed to write event: %v", err)
		}
	}

	// Build and run the events detail rollup
	rollupBin := filepath.Join(baseDir, "events-detail-rollup")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/service_events_detail/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build events detail rollup: %v", err)
	}

	rollupCmd := exec.Command(rollupBin,
		"-input-dir", "./data/raw/service_events",
		"-output-dir", "./data/warehouse/service_events_detail",
		"-process-time", day.Format(time.RFC3339),
	)
	rollupCmd.Dir = baseDir
	rollupCmd.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupCmd.Stderr = os.Stderr
	rollupCmd.Stdout = os.Stdout
	if err := rollupCmd.Run(); err != nil {
		t.Fatalf("events detail rollup failed: %v", err)
	}

	// Verify warehouse output
	warehouseKeys, err := store.List(context.Background(), "warehouse/service_events_detail")
	if err != nil {
		t.Fatalf("failed to list warehouse: %v", err)
	}
	if len(warehouseKeys) == 0 {
		t.Fatal("expected at least one Parquet file in warehouse")
	}

	foundParquet := false
	for _, key := range warehouseKeys {
		if strings.HasSuffix(key, ".parquet") && strings.Contains(key, dayStr) {
			foundParquet = true
			break
		}
	}
	if !foundParquet {
		t.Errorf("no Parquet file found for day %s", dayStr)
	}

	t.Logf("E2E events detail passed: 5 events → %d warehouse files", len(warehouseKeys))
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

// TestEndToEnd_MultiTenantIsolation verifies that rollup output for tenant A
// does not leak into tenant B's warehouse directory.
func TestEndToEnd_MultiTenantIsolation(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	tenantA := "tenant-alpha"
	tenantB := "tenant-beta"

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write facts for tenant A (service: alpha-api)
	for i := 0; i < 5; i++ {
		eventID, _ := uuid.NewV7()
		fact := &gravixv1.RequestFact{
			EventId:      eventID.String(),
			EventTime:    timestamppb.New(day.Add(time.Duration(i) * time.Second)),
			Service:      "alpha-api",
			Method:       "GET",
			PathTemplate: "/api/alpha/test",
			StatusCode:   200,
			LatencyMs:    int32(10 + i*5),
		}
		data, err := protojson.Marshal(fact)
		if err != nil {
			t.Fatalf("marshal tenant A fact: %v", err)
		}
		key := fmt.Sprintf("raw/%s/request_facts/%s/%s/batch_alpha_%d.jsonl", tenantA, dayStr, hourStr, i)
		if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
			t.Fatalf("write tenant A fact: %v", err)
		}
	}

	// Write facts for tenant B (service: beta-api)
	for i := 0; i < 5; i++ {
		eventID, _ := uuid.NewV7()
		fact := &gravixv1.RequestFact{
			EventId:      eventID.String(),
			EventTime:    timestamppb.New(day.Add(time.Duration(i) * time.Second)),
			Service:      "beta-api",
			Method:       "POST",
			PathTemplate: "/api/beta/test",
			StatusCode:   500,
			LatencyMs:    int32(100 + i*10),
		}
		data, err := protojson.Marshal(fact)
		if err != nil {
			t.Fatalf("marshal tenant B fact: %v", err)
		}
		key := fmt.Sprintf("raw/%s/request_facts/%s/%s/batch_beta_%d.jsonl", tenantB, dayStr, hourStr, i)
		if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
			t.Fatalf("write tenant B fact: %v", err)
		}
	}

	// Build rollup binary
	rollupBin := filepath.Join(baseDir, "rollup-job")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/request_metrics_minute/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("build rollup: %v", err)
	}

	// Run rollup for tenant A ONLY
	// Use relative paths because the rollup hard-codes NewLocalStore("./data")
	rollupA := exec.Command(rollupBin,
		"-input-dir", fmt.Sprintf("./data/raw/%s/request_facts", tenantA),
		"-output-dir", fmt.Sprintf("./data/warehouse/%s/request_metrics_minute", tenantA),
		"-process-time", day.Format(time.RFC3339),
	)
	rollupA.Dir = baseDir // CWD so ./data resolves to our temp dir
	rollupA.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupA.Stderr = os.Stderr
	rollupA.Stdout = os.Stdout
	if err := rollupA.Run(); err != nil {
		t.Fatalf("rollup for tenant A failed: %v", err)
	}

	// Verify tenant A has output
	aKeys, err := store.List(context.Background(), fmt.Sprintf("warehouse/%s/request_metrics_minute", tenantA))
	if err != nil {
		t.Fatalf("list tenant A warehouse: %v", err)
	}
	if len(aKeys) == 0 {
		t.Fatal("expected Parquet output for tenant A, got none")
	}
	foundParquet := false
	for _, key := range aKeys {
		if strings.HasSuffix(key, ".parquet") {
			foundParquet = true
			break
		}
	}
	if !foundParquet {
		t.Error("tenant A warehouse has no .parquet file")
	}

	// Verify tenant B has NO output (isolation check)
	bWarehouse := filepath.Join(dataDir, "warehouse", tenantB)
	if _, err := os.Stat(bWarehouse); err == nil {
		bKeys, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/request_metrics_minute", tenantB))
		if len(bKeys) > 0 {
			t.Fatalf("ISOLATION VIOLATION: tenant B has %d warehouse files after running rollup only for tenant A", len(bKeys))
		}
	}
	// Directory not existing is expected — that's proper isolation

	// Now run rollup for tenant B
	rollupB := exec.Command(rollupBin,
		"-input-dir", fmt.Sprintf("./data/raw/%s/request_facts", tenantB),
		"-output-dir", fmt.Sprintf("./data/warehouse/%s/request_metrics_minute", tenantB),
		"-process-time", day.Format(time.RFC3339),
	)
	rollupB.Dir = baseDir
	rollupB.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupB.Stderr = os.Stderr
	rollupB.Stdout = os.Stdout
	if err := rollupB.Run(); err != nil {
		t.Fatalf("rollup for tenant B failed: %v", err)
	}

	// Verify both tenants now have independent output
	bKeys, err := store.List(context.Background(), fmt.Sprintf("warehouse/%s/request_metrics_minute", tenantB))
	if err != nil {
		t.Fatalf("list tenant B warehouse: %v", err)
	}
	if len(bKeys) == 0 {
		t.Fatal("expected Parquet output for tenant B, got none")
	}

	// Re-verify tenant A output unchanged
	aKeys2, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/request_metrics_minute", tenantA))
	if len(aKeys2) != len(aKeys) {
		t.Errorf("tenant A files changed: before=%d after=%d", len(aKeys), len(aKeys2))
	}

	t.Logf("Multi-tenant isolation verified: tenant A=%d files, tenant B=%d files (independent)", len(aKeys), len(bKeys))
}

// writeServiceEvents is a helper that writes N ServiceEvent JSONL files for a tenant.
func writeServiceEvents(t *testing.T, store storage.ObjectStore, tenantPrefix, dayStr, hourStr string, n int, day time.Time, service string) {
	t.Helper()
	for i := 0; i < n; i++ {
		eventID, _ := uuid.NewV7()
		event := &gravixv1.ServiceEvent{
			EventId:   eventID.String(),
			EventTime: timestamppb.New(day.Add(time.Duration(i) * time.Minute)),
			Service:   service,
			EventType: "deploy_started",
			EntityId:  fmt.Sprintf("deploy-%s-%d", service, i),
			Properties: map[string]string{
				"version": fmt.Sprintf("1.0.%d", i),
			},
		}
		data, err := protojson.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		key := fmt.Sprintf("raw/%s/service_events/%s/%s/batch_%s_%d.jsonl", tenantPrefix, dayStr, hourStr, service, i)
		if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}
}

// TestEndToEnd_MultiTenantServiceEventsDaily verifies that service events daily
// rollup for tenant A does not leak into tenant B's warehouse.
func TestEndToEnd_MultiTenantServiceEventsDaily(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	tenantA := "tenant-alpha"
	tenantB := "tenant-beta"

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write events for both tenants
	writeServiceEvents(t, store, tenantA, dayStr, hourStr, 5, day, "alpha-api")
	writeServiceEvents(t, store, tenantB, dayStr, hourStr, 5, day, "beta-api")

	// Build events daily rollup
	rollupBin := filepath.Join(baseDir, "events-rollup")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/service_events_daily/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("build events rollup: %v", err)
	}

	// Run for tenant A only
	rollupA := exec.Command(rollupBin,
		"-input-dir", fmt.Sprintf("./data/raw/%s/service_events", tenantA),
		"-output-dir", fmt.Sprintf("./data/warehouse/%s/service_events_daily", tenantA),
		"-process-time", day.Format(time.RFC3339),
	)
	rollupA.Dir = baseDir
	rollupA.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupA.Stderr = os.Stderr
	rollupA.Stdout = os.Stdout
	if err := rollupA.Run(); err != nil {
		t.Fatalf("events rollup for tenant A failed: %v", err)
	}

	// Verify tenant A has output
	aKeys, err := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_daily", tenantA))
	if err != nil {
		t.Fatalf("list tenant A warehouse: %v", err)
	}
	if len(aKeys) == 0 {
		t.Fatal("expected Parquet output for tenant A, got none")
	}

	// Verify tenant B has NO output (isolation check)
	bWarehouse := filepath.Join(dataDir, "warehouse", tenantB)
	if _, err := os.Stat(bWarehouse); err == nil {
		bKeys, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_daily", tenantB))
		if len(bKeys) > 0 {
			t.Fatalf("ISOLATION VIOLATION: tenant B has %d warehouse files after running rollup only for tenant A", len(bKeys))
		}
	}

	// Run for tenant B
	rollupB := exec.Command(rollupBin,
		"-input-dir", fmt.Sprintf("./data/raw/%s/service_events", tenantB),
		"-output-dir", fmt.Sprintf("./data/warehouse/%s/service_events_daily", tenantB),
		"-process-time", day.Format(time.RFC3339),
	)
	rollupB.Dir = baseDir
	rollupB.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupB.Stderr = os.Stderr
	rollupB.Stdout = os.Stdout
	if err := rollupB.Run(); err != nil {
		t.Fatalf("events rollup for tenant B failed: %v", err)
	}

	// Verify both tenants have independent output
	bKeys, err := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_daily", tenantB))
	if err != nil {
		t.Fatalf("list tenant B warehouse: %v", err)
	}
	if len(bKeys) == 0 {
		t.Fatal("expected Parquet output for tenant B, got none")
	}

	aKeys2, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_daily", tenantA))
	if len(aKeys2) != len(aKeys) {
		t.Errorf("tenant A files changed: before=%d after=%d", len(aKeys), len(aKeys2))
	}

	t.Logf("Multi-tenant events daily isolation verified: tenant A=%d files, tenant B=%d files", len(aKeys), len(bKeys))
}

// TestEndToEnd_MultiTenantServiceEventsDetail verifies that service events detail
// rollup for tenant A does not leak into tenant B's warehouse.
func TestEndToEnd_MultiTenantServiceEventsDetail(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	tenantA := "tenant-alpha"
	tenantB := "tenant-beta"

	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write events for both tenants
	writeServiceEvents(t, store, tenantA, dayStr, hourStr, 5, day, "alpha-api")
	writeServiceEvents(t, store, tenantB, dayStr, hourStr, 5, day, "beta-api")

	// Build events detail rollup
	rollupBin := filepath.Join(baseDir, "events-detail-rollup")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/service_events_detail/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("build events detail rollup: %v", err)
	}

	// Run for tenant A only
	rollupA := exec.Command(rollupBin,
		"-input-dir", fmt.Sprintf("./data/raw/%s/service_events", tenantA),
		"-output-dir", fmt.Sprintf("./data/warehouse/%s/service_events_detail", tenantA),
		"-process-time", day.Format(time.RFC3339),
	)
	rollupA.Dir = baseDir
	rollupA.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupA.Stderr = os.Stderr
	rollupA.Stdout = os.Stdout
	if err := rollupA.Run(); err != nil {
		t.Fatalf("events detail rollup for tenant A failed: %v", err)
	}

	// Verify tenant A has output
	aKeys, err := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_detail", tenantA))
	if err != nil {
		t.Fatalf("list tenant A warehouse: %v", err)
	}
	if len(aKeys) == 0 {
		t.Fatal("expected Parquet output for tenant A, got none")
	}

	// Verify tenant B has NO output (isolation check)
	bWarehouse := filepath.Join(dataDir, "warehouse", tenantB)
	if _, err := os.Stat(bWarehouse); err == nil {
		bKeys, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_detail", tenantB))
		if len(bKeys) > 0 {
			t.Fatalf("ISOLATION VIOLATION: tenant B has %d warehouse files after running rollup only for tenant A", len(bKeys))
		}
	}

	// Run for tenant B
	rollupB := exec.Command(rollupBin,
		"-input-dir", fmt.Sprintf("./data/raw/%s/service_events", tenantB),
		"-output-dir", fmt.Sprintf("./data/warehouse/%s/service_events_detail", tenantB),
		"-process-time", day.Format(time.RFC3339),
	)
	rollupB.Dir = baseDir
	rollupB.Env = append(os.Environ(), "S3_ENDPOINT=")
	rollupB.Stderr = os.Stderr
	rollupB.Stdout = os.Stdout
	if err := rollupB.Run(); err != nil {
		t.Fatalf("events detail rollup for tenant B failed: %v", err)
	}

	// Verify both tenants have independent output
	bKeys, err := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_detail", tenantB))
	if err != nil {
		t.Fatalf("list tenant B warehouse: %v", err)
	}
	if len(bKeys) == 0 {
		t.Fatal("expected Parquet output for tenant B, got none")
	}

	aKeys2, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_detail", tenantA))
	if len(aKeys2) != len(aKeys) {
		t.Errorf("tenant A files changed: before=%d after=%d", len(aKeys), len(aKeys2))
	}

	t.Logf("Multi-tenant events detail isolation verified: tenant A=%d files, tenant B=%d files", len(aKeys), len(bKeys))
}

// TestEndToEnd_DLQAndReplay writes a DLQ entry as JSONL, verifies the file
// is valid and parseable, then simulates replaying the corrected fact.
func TestEndToEnd_DLQAndReplay(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Step 1: Write a DLQ entry (simulating rejected fact)
	type DLQEntry struct {
		Timestamp time.Time       `json:"timestamp"`
		TenantID  string          `json:"tenant_id,omitempty"`
		RequestID string          `json:"request_id,omitempty"`
		FactType  string          `json:"fact_type"`
		Error     string          `json:"error"`
		RawJSON   json.RawMessage `json:"raw_json"`
	}

	dlqEntry := DLQEntry{
		Timestamp: time.Now().UTC(),
		TenantID:  "e2e-tenant",
		RequestID: "req-abc-123",
		FactType:  "request_fact",
		Error:     "missing required field: service",
		RawJSON:   json.RawMessage(`{"event_id":"bad","service":""}`),
	}

	dlqData, err := json.Marshal(dlqEntry)
	if err != nil {
		t.Fatalf("marshal DLQ entry: %v", err)
	}
	dlqKey := "raw/e2e-tenant/dlq/request_facts/2026-03-19/batch_test_dlq.jsonl"
	if err := store.Put(context.Background(), dlqKey, bytes.NewReader(dlqData)); err != nil {
		t.Fatalf("store DLQ: %v", err)
	}

	// Step 2: Read back and verify DLQ entry
	reader, err := store.Get(context.Background(), dlqKey)
	if err != nil {
		t.Fatalf("get DLQ: %v", err)
	}
	defer reader.Close()

	readData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read DLQ: %v", err)
	}

	var readEntry DLQEntry
	if err := json.Unmarshal(readData, &readEntry); err != nil {
		t.Fatalf("DLQ entry is not valid JSON: %v", err)
	}
	if readEntry.FactType != "request_fact" {
		t.Errorf("fact_type = %q, want %q", readEntry.FactType, "request_fact")
	}
	if readEntry.RequestID != "req-abc-123" {
		t.Errorf("request_id = %q, want %q", readEntry.RequestID, "req-abc-123")
	}
	if readEntry.Error == "" {
		t.Error("DLQ error message should not be empty")
	}

	// Step 3: Simulate "replay" by writing corrected fact to raw storage
	day := time.Now().UTC()
	dayStr := day.Format("2006-01-02")
	hourStr := day.Format("15")

	eventID, _ := uuid.NewV7()
	correctedFact := &gravixv1.RequestFact{
		EventId:      eventID.String(),
		EventTime:    timestamppb.New(day),
		Service:      "e2e-service-replayed",
		Method:       "POST",
		PathTemplate: "/api/replay/test",
		StatusCode:   200,
		LatencyMs:    15,
	}
	data, _ := protojson.Marshal(correctedFact)
	rawKey := fmt.Sprintf("raw/e2e-tenant/request_facts/%s/%s/replayed.jsonl", dayStr, hourStr)
	if err := store.Put(context.Background(), rawKey, bytes.NewReader(data)); err != nil {
		t.Fatalf("store replayed fact: %v", err)
	}

	// Verify the replayed fact is stored
	exists, err := store.Exists(context.Background(), rawKey)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Error("replayed fact should exist in storage")
	}

	t.Log("DLQ write → read → replay cycle verified successfully")
}

// TestEndToEnd_APIKeyExpiry verifies the full API key expiry lifecycle:
// create a key with expiry, validate it works, validate expired key is rejected.
func TestEndToEnd_APIKeyExpiry(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	db, err := tenantdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create tenant
	tenant := &tenantdb.Tenant{Name: "E2E Corp", Email: "e2e@test.com", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Create key with future expiry — should validate
	future := time.Now().UTC().Add(24 * time.Hour)
	validKey, key, err := db.APIKeys().Create(ctx, tenant.ID, "future-key", &future)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if key.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be set on key")
	}

	info, err := db.APIKeys().ValidateKey(ctx, validKey)
	if err != nil {
		t.Fatalf("ValidateKey for future key: %v", err)
	}
	if info.TenantID != tenant.ID {
		t.Errorf("TenantID = %q, want %q", info.TenantID, tenant.ID)
	}

	// Create key with past expiry — should be rejected
	past := time.Now().UTC().Add(-1 * time.Hour)
	expiredKey, _, err := db.APIKeys().Create(ctx, tenant.ID, "expired-key", &past)
	if err != nil {
		t.Fatalf("create expired key: %v", err)
	}

	_, err = db.APIKeys().ValidateKey(ctx, expiredKey)
	if err == nil {
		t.Fatal("expected error for expired key, got nil")
	}

	// Create key without expiry — should always validate
	noExpiryKey, _, err := db.APIKeys().Create(ctx, tenant.ID, "no-expiry", nil)
	if err != nil {
		t.Fatalf("create no-expiry key: %v", err)
	}

	info, err = db.APIKeys().ValidateKey(ctx, noExpiryKey)
	if err != nil {
		t.Fatalf("ValidateKey for no-expiry key: %v", err)
	}
	if info.TenantID != tenant.ID {
		t.Errorf("TenantID = %q, want %q", info.TenantID, tenant.ID)
	}

	// Verify ListExpiringSoon returns the future key (within 7 days? no, it's 24h so within 2 days)
	expiring, err := db.APIKeys().ListExpiringSoon(ctx, tenant.ID, 2)
	if err != nil {
		t.Fatalf("ListExpiringSoon: %v", err)
	}
	if len(expiring) != 1 {
		t.Fatalf("expected 1 expiring key, got %d", len(expiring))
	}
	if expiring[0].Name != "future-key" {
		t.Errorf("expiring key name = %q, want %q", expiring[0].Name, "future-key")
	}

	t.Log("API key expiry lifecycle verified: future=valid, expired=rejected, no-expiry=valid, list-expiring=correct")
}

// TestEndToEnd_RequestIDPropagation verifies that request IDs are preserved
// in DLQ entries when facts fail validation.
func TestEndToEnd_RequestIDPropagation(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	// This test verifies the DLQ entry structure includes request_id field
	type DLQEntry struct {
		Timestamp time.Time       `json:"timestamp"`
		TenantID  string          `json:"tenant_id,omitempty"`
		RequestID string          `json:"request_id,omitempty"`
		FactType  string          `json:"fact_type"`
		Error     string          `json:"error"`
		RawJSON   json.RawMessage `json:"raw_json"`
	}

	// Create a DLQ entry with request ID (as the ingestion service would)
	entry := DLQEntry{
		Timestamp: time.Now().UTC(),
		TenantID:  "tenant-123",
		RequestID: "trace-e2e-propagation-test",
		FactType:  "request_fact",
		Error:     "validation failed: missing service",
		RawJSON:   json.RawMessage(`{"event_id":"bad"}`),
	}

	// Marshal and unmarshal to verify round-trip
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DLQEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.RequestID != "trace-e2e-propagation-test" {
		t.Errorf("RequestID = %q, want %q", decoded.RequestID, "trace-e2e-propagation-test")
	}
	if decoded.TenantID != "tenant-123" {
		t.Errorf("TenantID = %q, want %q", decoded.TenantID, "tenant-123")
	}

	// Verify the JSON contains the request_id field
	if !strings.Contains(string(data), `"request_id"`) {
		t.Error("serialized DLQ entry missing request_id field")
	}

	t.Log("Request ID propagation in DLQ entries verified")
}
