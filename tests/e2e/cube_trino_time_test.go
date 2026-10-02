//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/trinodb/trino-go-client/trino"
)

// TestCubeTimeColumnsCastOnTrino runs the time expressions the Cube models give
// Trino on the live stack's Trino, against values in the formats the rollups
// write. Every time dimension goes through one of them, so one Trino cannot
// evaluate fails every query on that cube. ServiceEvents.eventTime did (F-073):
// Trino's CAST refuses RFC 3339 text, and nothing had run the expression on a
// Trino with an event in the table.
func TestCubeTimeColumnsCastOnTrino(t *testing.T) {
	requireLiveStack(t)

	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required to evaluate cube/model_flags.js: %v", err)
	}
	cmd := exec.Command(node, "-e", `const f = require(process.argv[1]);
console.log(JSON.stringify({
  bucket: f.timestampSql('v'),
  event: f.isoTimestampSql('v'),
}));`, filepath.Join(repoRootFromE2E(t), "cube", "model_flags.js"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "CUBEJS_DB_TYPE=trino"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("evaluate model_flags.js: %v", err)
	}
	var exprs struct{ Bucket, Event string }
	if err := json.Unmarshal(out, &exprs); err != nil {
		t.Fatalf("model_flags.js output: %v\n%s", err, out)
	}

	db, err := sql.Open("trino", hiveDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := time.Date(2026, 10, 2, 10, 30, 0, 0, time.UTC)
	for _, c := range []struct{ name, expr, value string }{
		// pkg/recompute writes bucket_start like this.
		{"RequestMetricsMinute.bucketStart", exprs.Bucket, "2026-10-02 10:30:00"},
		// transforms/service_events_detail writes event_time with time.RFC3339.
		{"ServiceEvents.eventTime", exprs.Event, want.Format(time.RFC3339)},
	} {
		q := "SELECT format_datetime(" + c.expr + ", 'yyyy-MM-dd HH:mm:ss') FROM (VALUES '" + c.value + "') AS t(v)"
		var got string
		if err := db.QueryRow(q).Scan(&got); err != nil {
			t.Errorf("%s: Trino cannot evaluate %s on %q: %v", c.name, c.expr, c.value, err)
			continue
		}
		if got != want.Format("2006-01-02 15:04:05") {
			t.Errorf("%s: %s on %q = %s, want the UTC wall time %s",
				c.name, c.expr, c.value, got, want.Format("2006-01-02 15:04:05"))
		}
	}
}
