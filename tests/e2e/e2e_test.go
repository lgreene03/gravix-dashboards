package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
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

	dataDir := t.TempDir()
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

	// Step 2: Run the rollup
	rollupBin := filepath.Join(dataDir, "rollup-job")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/request_metrics_minute/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build rollup: %v", err)
	}

	rollupCmd := exec.Command(rollupBin,
		"-input-dir", filepath.Join(dataDir, "raw", "request_facts"),
		"-output-dir", filepath.Join(dataDir, "warehouse", "request_metrics_minute"),
		"-process-time", day.Format(time.RFC3339),
	)
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

// TestEndToEnd_PurgeRetention verifies the purge logic deletes old data and keeps recent data.
func TestEndToEnd_PurgeRetention(t *testing.T) {
	dataDir := t.TempDir()
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

	// Purge files older than 30 days
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	allRaw, _ := store.List(ctx, "raw")
	allWarehouse, _ := store.List(ctx, "warehouse")

	deleted := 0
	for _, key := range append(allRaw, allWarehouse...) {
		dateStr := extractDateFromKey(key)
		if dateStr != "" && dateStr < cutoff {
			store.Delete(ctx, key)
			deleted++
		}
	}

	if deleted != 2 {
		t.Errorf("expected 2 files deleted, got %d", deleted)
	}

	remainingRaw, _ := store.List(ctx, "raw")
	remainingWarehouse, _ := store.List(ctx, "warehouse")
	if len(remainingRaw) != 1 {
		t.Errorf("expected 1 raw file remaining, got %d", len(remainingRaw))
	}
	if len(remainingWarehouse) != 1 {
		t.Errorf("expected 1 warehouse file remaining, got %d", len(remainingWarehouse))
	}
}

// TestEndToEnd_ServiceEventsRollup verifies the service events pipeline:
// Write raw events → run service events rollup → verify Parquet output.
func TestEndToEnd_ServiceEventsRollup(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.Skip("Set E2E_TEST=1 to run end-to-end tests")
	}

	dataDir := t.TempDir()
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

	// Build and run the events rollup
	rollupBin := filepath.Join(dataDir, "events-rollup")
	projectRoot := findProjectRoot(t)
	buildCmd := exec.Command("go", "build", "-o", rollupBin, "./transforms/service_events_daily/")
	buildCmd.Dir = projectRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build events rollup: %v", err)
	}

	rollupCmd := exec.Command(rollupBin,
		"-input-dir", filepath.Join(dataDir, "raw", "service_events"),
		"-output-dir", filepath.Join(dataDir, "warehouse", "service_events_daily"),
		"-process-time", day.Format(time.RFC3339),
	)
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

// extractDateFromKey finds the first YYYY-MM-DD pattern anywhere in a key.
// It checks both path segments and substrings within segments (e.g., metrics_2025-01-15.parquet).
func extractDateFromKey(key string) string {
	// Search the entire key for a YYYY-MM-DD pattern
	for i := 0; i <= len(key)-10; i++ {
		candidate := key[i : i+10]
		if candidate[4] == '-' && candidate[7] == '-' {
			if _, err := time.Parse("2006-01-02", candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
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
