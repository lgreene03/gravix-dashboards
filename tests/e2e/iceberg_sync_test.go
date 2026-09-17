//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/trinodb/trino-go-client/trino"
)

// GRVX-1106 AC-4, AC-5, AC-6. These need the docker-compose stack; the unit
// tests in transforms/iceberg_sync cover the statements themselves.
//
// What these prove that the unit tests cannot: that the SQL those functions
// build is SQL Trino accepts, against a real Iceberg catalog, and that running
// the job twice does not double the data. Both are properties of the
// interaction, and a mock of Trino would have been written by the same person
// who wrote the assumption being tested.

const icebergDSN = "http://gravix@localhost:8081?catalog=gravix_iceberg&schema=raw"
const hiveDSN = "http://gravix@localhost:8081?catalog=gravix&schema=raw"

// buildIcebergSync builds the job and returns its path.
func buildIcebergSync(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "iceberg-sync")
	cmd := exec.Command("go", "build", "-o", bin, "./transforms/iceberg_sync/")
	cmd.Dir = repoRootFromE2E(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building iceberg-sync: %v\n%s", err, out)
	}
	return bin
}

func repoRootFromE2E(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	return root
}

// today is the partition the sync job writes on a -days 1 run.
func today() string { return time.Now().UTC().Format("2006-01-02") }

// countRows returns the row count for one day from one catalog.
func countRows(t *testing.T, dsn, table, day string) int64 {
	t.Helper()

	db, err := sql.Open("trino", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	defer db.Close()

	var n int64
	q := "SELECT COUNT(*) FROM " + table + " WHERE event_day = '" + day + "'"
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("counting rows (%s): %v", q, err)
	}
	return n
}

func runSync(t *testing.T, bin string, days string) {
	t.Helper()

	cmd := exec.Command(bin, "-iceberg-dsn", icebergDSN, "-days", days)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("iceberg-sync -days %s: %v\n%s", days, err, out)
	}
}

// -------------------------------------------------------------------- AC-4 --

// TestIcebergSyncPreservesRowCount — the Iceberg table holds exactly what the
// Hive table holds.
//
// Row count rather than a checksum on purpose: a mismatch here means the copy
// lost or duplicated data, which is the failure that matters. Equality of
// values is what the explicit column list in insertDaySQL protects, and that is
// unit-tested.
func TestIcebergSyncPreservesRowCount(t *testing.T) {
	requireLiveStack(t)

	bin := buildIcebergSync(t)
	runSync(t, bin, "1")

	day := today()
	hive := countRows(t, hiveDSN, "gravix.raw.request_metrics_minute", day)
	iceberg := countRows(t, icebergDSN, "gravix_iceberg.raw.request_metrics_minute", day)

	if hive != iceberg {
		t.Errorf("row count for %s: Hive %d, Iceberg %d — the copy is not a copy", day, hive, iceberg)
	}
	if hive == 0 {
		t.Log("note: the Hive table has no rows for today, so this run proved equality of two " +
			"empty sets. Generate load and re-run for a real comparison.")
	}
}

// -------------------------------------------------------------------- AC-5 --

// TestIcebergSyncIsIdempotent — running twice does not double the data.
//
// This is the failure the DELETE exists to prevent, and it is the one most
// likely to reach production unnoticed: every number stays plausible and is
// exactly twice what it should be. A cron sidecar runs this job every five
// minutes, so "additive instead of idempotent" would be wrong within ten.
func TestIcebergSyncIsIdempotent(t *testing.T) {
	requireLiveStack(t)

	bin := buildIcebergSync(t)
	day := today()

	runSync(t, bin, "1")
	first := countRows(t, icebergDSN, "gravix_iceberg.raw.request_metrics_minute", day)

	runSync(t, bin, "1")
	second := countRows(t, icebergDSN, "gravix_iceberg.raw.request_metrics_minute", day)

	if first != second {
		t.Errorf("row count after one sync %d, after two %d — the sync is additive, not idempotent",
			first, second)
	}
}

// -------------------------------------------------------------------- AC-6 --

// TestVerifySparkIcebergRead — an engine that has never heard of Gravix reads
// Gravix's warehouse.
//
// Trino reading back what Trino wrote proves only that Trino is
// self-consistent. The interoperability claim needs a second implementation of
// the table format, which is the entire reason this spec exists.
func TestVerifySparkIcebergRead(t *testing.T) {
	requireLiveStack(t)

	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is not on PATH, but the stack this test just probed is running — " +
			"something is inconsistent about this environment")
	}

	root := repoRootFromE2E(t)
	cmd := exec.Command(filepath.Join(root, "scripts", "verify_spark_iceberg_read.sh"))
	cmd.Dir = root
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running verify_spark_iceberg_read.sh: %v\n%s", err, out)
	}

	if code == 2 {
		t.Fatalf("the script reports Trino or MinIO unreachable, but requireLiveStack "+
			"just reached Trino:\n%s", out)
	}
	if code != 0 {
		t.Errorf("Spark could not read the Iceberg tables (exit %d):\n%s", code, out)
	}
	if !strings.Contains(string(out), "row(s)") {
		t.Errorf("the script succeeded without reporting a row count:\n%s", out)
	}
}
