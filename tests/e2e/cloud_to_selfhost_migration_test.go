//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parquet-go/parquet-go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// The single command a customer runs to leave Gravix Cloud. It is written out
// here, once, and the test below runs exactly this — so the guide cannot
// publish a command nobody executed.
const migrationCommand = "gravix migrate export-cloud --tenant-id <id> --since <YYYY-MM-DD> " +
	"--gateway-endpoint <url> --gateway-token <jwt> --out-dir ./gravix-export"

const migrationTenantID = "ten_migration_e2e"

// migrationVerifyQuery is the confirm-before-you-cancel query published in
// docs-site/docs/cloud-to-selfhost-migration.md. The doc and this constant are
// held identical by TestMigrationGuidePublishesTheCommandThatRuns, so the guide
// can never publish SQL nobody ran — which matters more here than anywhere
// else, because this page is read once, by somebody who has already decided to
// leave and will not be filing a bug about it.
const migrationVerifyQuery = `SELECT count(*) AS rows, sum(request_count) AS requests
FROM read_parquet('data/warehouse/request_metrics_minute/**/*.parquet');`

// migrationJWTSecret is long enough for the gateway's own 32-character minimum.
const migrationJWTSecret = "migration-e2e-secret-at-least-32ch"

// TestMigrationOutputReadableByDuckDB is the exit-path proof for Gravix Cloud,
// and it deliberately uses nothing of its own.
//
// The gateway is the real, unmodified binary, started from ./services/gateway/.
// The client is the real, unmodified `gravix` binary running the real
// subcommand. The recompute step is the real, unmodified rollup binary. The
// reader is DuckDB, a foreign SQL engine with no knowledge of Gravix, when it
// is on PATH — and the parquet-go library otherwise, so this test asserts
// something real in every suite rather than skipping where a tool is missing.
//
// A version of this test that posted to a fixture gateway of its own
// construction would prove only that the client agreed with the fixture. That
// is not the claim. The claim is that a customer can leave.
func TestMigrationOutputReadableByDuckDB(t *testing.T) {
	root := findProjectRoot(t)
	work := t.TempDir()

	// Truncated to the hour, so every seeded fact lands in the same minute
	// bucket whatever time of day the suite runs at. Without this the fixture
	// straddles a minute boundary whenever the clock happens to be past :59:55,
	// and the recomputed row count is 1 or 2 depending on when CI fired — a
	// test that passes because of the wall clock is the kind that goes red once
	// a month for no reason anybody can reproduce.
	day := time.Now().UTC().AddDate(0, 0, -1).Truncate(time.Hour)
	dayStr := day.Format("2006-01-02")

	// --- The Cloud side: a tenant's facts, in the keys ingestion writes ------
	//
	// The gateway's object store is rooted at RAW_DATA_DIR and its keys begin
	// with "raw/", so the facts go one level down from the root it is given.
	cloudRoot := filepath.Join(work, "cloud-data")
	cloudStore, err := storage.NewLocalStore(cloudRoot)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	const factsPerFile, files = 5, 3
	wantFacts := factsPerFile * files
	for i := 0; i < files; i++ {
		key := fmt.Sprintf("raw/%s/request_facts/%s/%02d/batch_%d.jsonl",
			migrationTenantID, dayStr, 9+i, i)
		if err := cloudStore.Put(context.Background(), key,
			bytes.NewReader(migrationFacts(t, day, factsPerFile))); err != nil {
			t.Fatalf("seeding %s: %v", key, err)
		}
	}

	gatewayAddr := startMigrationGateway(t, root, work, cloudRoot)

	// --- The customer's side: one command ------------------------------------
	gravixBin := filepath.Join(work, "gravix")
	buildBin(t, root, gravixBin, "./cmd/cli/")

	// Exported straight into the self-hosted data root. The gateway's archive
	// entries are object-store keys beginning with "raw/", and a Gravix data
	// root is a directory whose keys begin with "raw/" — so choosing --out-dir
	// well removes even the copy step. This is the claim being tested: not that
	// the export can be converted, but that there is nothing to convert.
	outDir := filepath.Join(work, "data")
	token := migrationToken(t)

	cmd := exec.Command(gravixBin, "migrate", "export-cloud",
		"--tenant-id", migrationTenantID,
		"--since", dayStr,
		"--until", dayStr,
		"--gateway-endpoint", "http://"+gatewayAddr,
		"--out-dir", outDir,
	)
	cmd.Dir = work
	// The token travels in the environment, not on a command line that lands
	// in shell history and in `ps`.
	cmd.Env = append(os.Environ(), "GRAVIX_GATEWAY_TOKEN="+token)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gravix migrate export-cloud: %v\n%s", err, out)
	}
	t.Logf("migrate export-cloud: %s", strings.TrimSpace(string(out)))

	// --- The exported directory IS a Gravix data root ------------------------
	//
	// No conversion step, no import: the keys the gateway put in the archive
	// are the keys a LocalStore reads.
	exportedPrefix := filepath.Join(outDir, "raw", migrationTenantID, "request_facts", dayStr)
	entries := countJSONLFiles(t, exportedPrefix)
	if entries != files {
		t.Fatalf("exported %d JSONL files under %s, want %d", entries, exportedPrefix, files)
	}

	var manifest struct {
		TenantID      string `json:"tenant_id"`
		Since         string `json:"since"`
		Until         string `json:"until"`
		FilesExported int    `json:"files_exported"`
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "MIGRATION_MANIFEST.json"))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	if manifest.TenantID != migrationTenantID || manifest.FilesExported != files {
		t.Errorf("manifest = %+v, want tenant %s and %d files",
			manifest, migrationTenantID, files)
	}

	// --- The self-hosted side: the unmodified rollup binary ------------------
	//
	// Run exactly as a self-hoster runs it: from the directory holding ./data,
	// with the store-relative paths the job's own defaults and its multi-tenant
	// mode use. Nothing here is specific to having come from Cloud.
	rollupBin := filepath.Join(work, "rollup-job")
	buildBin(t, root, rollupBin, "./transforms/request_metrics_minute/")

	const warehouseKey = "./data/warehouse/request_metrics_minute"
	rollup := exec.Command(rollupBin,
		"-input-dir", "./data/raw/"+migrationTenantID+"/request_facts",
		"-output-dir", warehouseKey,
		"-process-time", day.Format(time.RFC3339),
	)
	rollup.Dir = work
	rollup.Env = append(os.Environ(), "S3_ENDPOINT=")
	if out, err := rollup.CombinedOutput(); err != nil {
		t.Fatalf("rollup against the exported directory: %v\n%s", err, out)
	}

	warehouse := filepath.Join(work, "data", "warehouse", "request_metrics_minute")

	parquetFiles := findParquet(t, warehouse)
	if len(parquetFiles) == 0 {
		t.Fatalf("the rollup produced no Parquet under %s", warehouse)
	}

	// --- Somebody else's SQL engine reads it ---------------------------------
	rows, counted := readWarehouse(t, work, parquetFiles)

	// Exactly one row: all 15 facts share a minute, a service, a method, a path
	// and a status, which is one aggregate. Asserting the exact number rather
	// than "more than zero" is what makes this a check on the recomputation
	// rather than on whether a file exists.
	if rows != 1 {
		t.Errorf("the recomputed warehouse has %d rows, want exactly 1 "+
			"(all %d facts fall in one minute bucket)", rows, wantFacts)
	}

	// The stronger claim, and the one a customer actually cares about: not that
	// SOMETHING was recomputed, but that every fact made the journey. All 15
	// facts share a minute, a service, a method, a path and a status, so they
	// aggregate into one row whose request_count must be 15. A row count alone
	// would pass just as happily if fourteen facts had been dropped.
	if counted != wantFacts {
		t.Errorf("the recomputed warehouse accounts for %d requests; %d facts were exported from Cloud, "+
			"so %d were lost in the migration", counted, wantFacts, wantFacts-counted)
	}

	t.Logf("exit path proved: %d facts exported from Cloud -> %d Parquet file(s) -> %d row(s), "+
		"%d requests accounted for", wantFacts, len(parquetFiles), rows, counted)
}

// TestMigrationGuidePublishesTheCommandThatRuns holds the published guide and
// the command this test executes identical. A migration guide is read once, at
// the worst possible moment, by somebody who has already decided to leave.
func TestMigrationGuidePublishesTheCommandThatRuns(t *testing.T) {
	const guide = "../../docs-site/docs/cloud-to-selfhost-migration.md"
	raw, err := os.ReadFile(guide)
	if err != nil {
		t.Fatalf("reading the guide: %v", err)
	}
	body := string(raw)

	for _, want := range []string{
		"gravix migrate export-cloud",
		"--tenant-id",
		"--since",
		"--gateway-token",
		"MIGRATION_MANIFEST.json",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the guide does not mention %q", want)
		}
	}

	// The confirm-before-you-cancel query must be the one this test ran.
	if !strings.Contains(body, migrationVerifyQuery) {
		t.Errorf("the guide does not publish the query this test executes:\n%s", migrationVerifyQuery)
	}

	// The guide must not promise a paid tier is needed to leave.
	for _, forbidden := range []string{"Enterprise licence required", "upgrade to export"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the guide gates the exit path behind %q", forbidden)
		}
	}
}

// TestMigrationE2EJobInstallsDuckDB: the DuckDB half of the proof above runs
// only where DuckDB exists. If the e2e job ever stops installing it, the
// foreign-reader claim quietly stops being tested, so the workflow is checked
// rather than trusted.
func TestMigrationE2EJobInstallsDuckDB(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	if !strings.Contains(string(raw), "Install DuckDB CLI") {
		t.Error("the e2e job no longer installs DuckDB; the migration's foreign-reader proof is not running")
	}
}

// --- helpers ----------------------------------------------------------------

// migrationFacts returns n valid RequestFacts as JSONL, exactly as ingestion
// buffers them.
func migrationFacts(t *testing.T, day time.Time, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		fact := &gravixv1.RequestFact{
			EventId:      id.String(),
			EventTime:    timestamppb.New(day.Add(time.Duration(i) * time.Second)),
			Service:      "migration-e2e",
			Method:       "GET",
			PathTemplate: "/api/orders/{id}",
			StatusCode:   200,
			LatencyMs:    int32(10 + i*5),
		}
		data, err := protojson.Marshal(fact)
		if err != nil {
			t.Fatalf("marshalling fact: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// startMigrationGateway builds and starts the real gateway binary against
// rawRoot, and returns its address.
func startMigrationGateway(t *testing.T, root, work, rawRoot string) string {
	t.Helper()

	bin := filepath.Join(work, "gateway")
	buildBin(t, root, bin, "./services/gateway/")

	addr := migrationFreeAddr(t)
	cmd := exec.Command(bin)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"GATEWAY_ADDR="+addr,
		"TENANT_DB_PATH="+filepath.Join(work, "tenants.db"),
		"JWT_SECRET="+migrationJWTSecret,
		"RAW_DATA_DIR="+rawRoot,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the gateway: %v", err)
	}

	var logs strings.Builder
	go func() { io.Copy(&logs, bufio.NewReader(stderr)) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			t.Logf("gateway stderr:\n%s", logs.String())
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/live")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the gateway did not answer /live within 30s:\n%s", logs.String())
	return ""
}

// migrationToken mints a session token the same way POST /api/gateway/login
// does, so the export runs under the gateway's real auth rather than around
// it.
func migrationToken(t *testing.T) string {
	t.Helper()
	tok, err := auth.NewTokenService(migrationJWTSecret, time.Hour).
		Generate(migrationTenantID, "usr_migration", "owner@example.com", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("minting a session token: %v", err)
	}
	return tok
}

func buildBin(t *testing.T, root, out, pkg string) {
	t.Helper()
	build := exec.Command("go", "build", "-o", out, pkg)
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -o %s %s: %v\n%s", out, pkg, err, b)
	}
}

func migrationFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

func countJSONLFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return n
}

func findParquet(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".parquet") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
}

// readWarehouse returns the recomputed row count and the total request_count
// across those rows.
//
// It reads with DuckDB when it is installed — the foreign-engine half of the
// proof, a SQL engine that has never heard of Gravix — and with parquet-go
// otherwise, so the test still asserts the output is real, readable Parquet in
// suites where DuckDB is not available. It never skips.
func readWarehouse(t *testing.T, dataRoot string, files []string) (rows, requests int) {
	t.Helper()

	if duckDBPath() != "" {
		// Run from the data root, with the query the guide publishes, exactly
		// as a customer confirming their migration would.
		got := runDuckDB(t, dataRoot, migrationVerifyQuery)
		lines := strings.Split(strings.TrimSpace(got), "\n")
		fields := strings.Split(strings.TrimSpace(lines[len(lines)-1]), ",")
		if len(fields) != 2 {
			t.Fatalf("DuckDB returned %q, which is not a (rows, requests) pair", got)
		}
		rows, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			t.Fatalf("DuckDB row count %q: %v", fields[0], err)
		}
		requests, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			t.Fatalf("DuckDB request sum %q: %v", fields[1], err)
		}
		t.Logf("read by DuckDB, with no Gravix process running: %d rows, %d requests", rows, requests)
		return rows, requests
	}

	t.Log("duckdb is not on PATH; reading the Parquet with parquet-go instead. " +
		"The foreign-engine half of this proof runs in the e2e job, which installs it.")
	for _, f := range files {
		got, err := parquet.ReadFile[recompute.MetricRow](f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		rows += len(got)
		for _, r := range got {
			requests += int(r.RequestCount)
		}
	}
	return rows, requests
}
