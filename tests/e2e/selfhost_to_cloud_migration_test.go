//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

const importTenantID = "ten_import_e2e"

// TestSelfHostToCloudImportPreservesFactCount proves the import direction with
// two real, unmodified ingestion binaries and the real gravix binary between
// them.
//
// The source instance is a self-hosted install: facts are POSTed to it, it
// writes them to its own disk in its own layout, and it is shut down so the
// sink flushes. The destination instance stands in for Gravix Cloud. Nothing
// in the middle knows anything about migration — the importer replays through
// POST /api/v1/facts/batch, the same public endpoint every SDK calls, because
// ValidateRequestFact has never had a staleness check on EventTime.
//
// The number that matters is that every fact arrives. A migration that moves
// "most of" a customer's history is not a migration.
func TestSelfHostToCloudImportPreservesFactCount(t *testing.T) {
	root := findProjectRoot(t)
	work := t.TempDir()

	const factCount = 40
	day := time.Now().UTC().Add(-36 * time.Hour)

	// --- The self-hosted install the customer is leaving --------------------
	srcDir := filepath.Join(work, "selfhost")
	srcKey := seedTenantDB(t, filepath.Join(srcDir, "tenants.db"))

	ingestionBin := filepath.Join(work, "ingestion-service")
	buildBin(t, root, ingestionBin, "./services/ingestion/")

	srcAddr, stopSrc := startIngestion(t, ingestionBin, srcDir, work, "source")
	for i := 0; i < factCount; i++ {
		postFactTo(t, srcAddr, srcKey, importFact(t, day, i))
	}

	// Shut it down so the sink rotates and uploads. The facts are only on disk
	// in their final layout once Close has run.
	stopSrc()
	srcFacts := filepath.Join(srcDir, "raw", importTenantID, "request_facts")
	onDisk := countJSONLLines(t, srcFacts)
	if onDisk != factCount {
		t.Fatalf("the source install holds %d facts on disk, want %d — the migration has not "+
			"been given the data it is supposed to move", onDisk, factCount)
	}

	// --- The destination, standing in for Gravix Cloud ----------------------
	dstDir := filepath.Join(work, "cloud")
	dstKey := seedTenantDB(t, filepath.Join(dstDir, "tenants.db"))
	dstAddr, stopDst := startIngestion(t, ingestionBin, dstDir, work, "destination")

	// --- One command --------------------------------------------------------
	gravixBin := filepath.Join(work, "gravix")
	buildBin(t, root, gravixBin, "./cmd/cli/")

	cmd := exec.Command(gravixBin, "migrate", "import-cloud",
		"--data-dir", srcDir,
		"--tenant-dir-name", importTenantID,
		"--ingestion-endpoint", "http://"+dstAddr,
	)
	cmd.Dir = work
	// The key travels in the environment, not on a command line that lands in
	// shell history and in `ps`.
	cmd.Env = append(os.Environ(), "GRAVIX_API_KEY="+dstKey)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gravix migrate import-cloud: %v\n%s", err, out)
	}

	var summary struct {
		FilesRead      int `json:"files_read"`
		FactsAccepted  int `json:"facts_accepted"`
		FactsRejected  int `json:"facts_rejected"`
		EventsAccepted int `json:"events_accepted"`
		EventsFailed   int `json:"events_failed"`
	}
	if err := json.Unmarshal(out, &summary); err != nil {
		t.Fatalf("the summary is not valid JSON: %v\n%s", err, out)
	}

	// AC-7: the destination accepted exactly what the source held.
	if summary.FactsAccepted != factCount {
		t.Errorf("facts_accepted = %d, want %d — %d facts did not survive the migration",
			summary.FactsAccepted, factCount, factCount-summary.FactsAccepted)
	}
	if summary.FactsRejected != 0 {
		t.Errorf("facts_rejected = %d, want 0; a fact the source stored was refused on replay, "+
			"which means the two installs disagree about what is valid", summary.FactsRejected)
	}
	if summary.FilesRead == 0 {
		t.Error("files_read = 0; the importer found nothing to read")
	}

	// And the destination really has them on its own disk, not merely counted
	// them in a response body.
	stopDst()
	dstFacts := filepath.Join(dstDir, "raw", importTenantID, "request_facts")
	if got := countJSONLLines(t, dstFacts); got != factCount {
		t.Errorf("the destination holds %d facts on disk, want %d", got, factCount)
	}

	t.Logf("import proved: %d facts on the self-hosted install -> %d accepted by the destination "+
		"-> %d durable on its disk", onDisk, summary.FactsAccepted, factCount)
}

// TestImportGuidePublishesTheDeduplicationLimitation: re-running the import
// over a range that already landed produces duplicates, and GRVX-1404 §9
// requires that to be said where a customer will read it rather than only in
// the spec.
func TestImportGuidePublishesTheDeduplicationLimitation(t *testing.T) {
	const guide = "../../docs-site/docs/selfhost-to-cloud-migration.md"
	raw, err := os.ReadFile(guide)
	if err != nil {
		t.Fatalf("reading the guide: %v", err)
	}
	body := strings.ToLower(string(raw))

	for _, want := range []string{"gravix migrate import-cloud", "--tenant-dir-name", "--dry-run"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the guide does not mention %q", want)
		}
	}
	// The limitation, in words a customer would search for.
	if !strings.Contains(body, "duplicate") {
		t.Error("the guide never says that re-running produces duplicates; a customer re-running " +
			"after a partial import would double their history without warning")
	}
	if !strings.Contains(body, "does not de-duplicate") && !strings.Contains(body, "no de-duplication") {
		t.Error("the guide does not state plainly that the importer performs no de-duplication")
	}
}

// --- helpers ----------------------------------------------------------------

// seedTenantDB creates the tenant and one API key, and returns the key.
func seedTenantDB(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tdb, err := tenantdb.Open(path)
	if err != nil {
		t.Fatalf("tenantdb.Open(%s): %v", path, err)
	}
	defer tdb.Close()

	ctx := context.Background()
	if err := tdb.Tenants().Create(ctx, &tenantdb.Tenant{
		ID: importTenantID, Name: "Import E2E", Email: "ops@example.com",
		Plan: "business", Status: "active", OverageAllowed: true,
	}); err != nil {
		t.Fatalf("creating the tenant: %v", err)
	}
	key, _, err := tdb.APIKeys().Create(ctx, importTenantID, "migration-e2e", nil)
	if err != nil {
		t.Fatalf("creating an API key: %v", err)
	}
	return key
}

// startIngestion builds nothing — it starts the already-built, unmodified
// binary — and returns its address plus a function that shuts it down
// gracefully so the sink flushes.
func startIngestion(t *testing.T, bin, baseDir, work, label string) (addr string, stop func()) {
	t.Helper()

	addr = migrationFreeAddr(t)
	_, port, _ := strings.Cut(addr, ":")

	cmd := exec.Command(bin, "-port", port, "-base-dir", baseDir)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"TENANT_DB_PATH="+filepath.Join(baseDir, "tenants.db"),
		"S3_ENDPOINT=",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("%s: stderr pipe: %v", label, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: start: %v", label, err)
	}

	var logs strings.Builder
	go func() { io.Copy(&logs, bufio.NewReader(stderr)) }()

	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		// SIGTERM, not Kill: main's `defer sink.Close()` is what rotates the
		// buffer and uploads it, and a killed process leaves the facts in
		// current.jsonl where nothing will ever read them.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			t.Errorf("%s did not shut down within 30s:\n%s", label, logs.String())
		}
	}
	t.Cleanup(func() {
		stop()
		if t.Failed() {
			t.Logf("%s stderr:\n%s", label, logs.String())
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/live")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr, stop
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not answer /live within 30s:\n%s", label, logs.String())
	return "", stop
}

// importFact returns one valid RequestFact as JSON.
func importFact(t *testing.T, day time.Time, i int) []byte {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	fact := &gravixv1.RequestFact{
		EventId:      id.String(),
		EventTime:    timestamppb.New(day.Add(time.Duration(i) * time.Second)),
		Service:      "import-e2e",
		Method:       "GET",
		PathTemplate: "/api/orders/{id}",
		StatusCode:   200,
		LatencyMs:    int32(10 + i),
	}
	data, err := protojson.Marshal(fact)
	if err != nil {
		t.Fatalf("marshalling fact: %v", err)
	}
	return data
}

func postFactTo(t *testing.T, addr, apiKey string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/facts", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/facts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		got, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/v1/facts returned %d: %s", resp.StatusCode, got)
	}
}

// countJSONLLines counts non-empty lines across every .jsonl file under dir,
// ignoring current.jsonl — a file the sink has not rotated is not data any
// reader will find.
func countJSONLLines(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") ||
			filepath.Base(path) == "current.jsonl" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				n++
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return n
}
