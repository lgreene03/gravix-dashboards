// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package trinocatalog

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestTenantTableLocationIsTheRollupsDirectory(t *testing.T) {
	// pkg/recompute writes warehouse/<tenant>/<table>/event_day=<day>/…
	// (MetricDirFor, KeyPrefix).
	got := CreateTenantTableSQL("gravix", "c5422c0a-0703-4dcd-a661-888506ed5c8b", Tables()[0])
	for _, want := range []string{
		`gravix.tenants."t_c5422c0a_0703_4dcd_a661_888506ed5c8b_request_metrics_minute"`,
		`external_location = 's3a://gravix/warehouse/c5422c0a-0703-4dcd-a661-888506ed5c8b/request_metrics_minute/'`,
		"tenant_id VARCHAR, bucket_start VARCHAR",
		"format = 'PARQUET'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestViewCarriesEachTenantAsAConstant(t *testing.T) {
	daily := Tables()[1]
	got := ViewSQL(daily, []string{"bbbb", "aaaa"})
	want := "CREATE OR REPLACE VIEW gravix.serving.service_events_daily AS " +
		"SELECT CAST('' AS VARCHAR) AS tenant_id, event_day, service, event_type, event_count FROM gravix.raw.service_events_daily" +
		" UNION ALL SELECT CAST('aaaa' AS VARCHAR) AS tenant_id, event_day, service, event_type, event_count FROM gravix.tenants.\"t_aaaa_service_events_daily\"" +
		" UNION ALL SELECT CAST('bbbb' AS VARCHAR) AS tenant_id, event_day, service, event_type, event_count FROM gravix.tenants.\"t_bbbb_service_events_daily\""
	if got != want {
		t.Errorf("view =\n%s\nwant\n%s", got, want)
	}
	// The detail table's single-tenant rows already carry tenant_id.
	if detail := ViewSQL(Tables()[2], nil); !strings.Contains(detail, "SELECT tenant_id AS tenant_id, event_time") {
		t.Errorf("the detail view should keep gravix.raw's own tenant_id:\n%s", detail)
	}
}

func TestTenantIDsThatCouldEscapeAreRefused(t *testing.T) {
	for _, bad := range []string{"", "a'b", `a"b`, "a b", "a;DROP", "../x", "a/b", "-lead", strings.Repeat("a", 65)} {
		if ValidTenantID(bad) {
			t.Errorf("ValidTenantID(%q) = true", bad)
		}
	}
	for _, good := range []string{"c5422c0a-0703-4dcd-a661-888506ed5c8b", "default", "T1"} {
		if !ValidTenantID(good) {
			t.Errorf("ValidTenantID(%q) = false", good)
		}
	}
}

// TestColumnsMatchTheHiveTables holds Tables() to the gravix.raw tables the
// views union with: a column out of order or missing would fail every view.
func TestColumnsMatchTheHiveTables(t *testing.T) {
	raw, err := os.ReadFile("../../storage/trino/init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, tbl := range Tables() {
		m := regexp.MustCompile(`(?s)CREATE TABLE (?:IF NOT EXISTS )?gravix\.raw\.` + tbl.Name + ` \((.*?)\)\s*WITH`).FindStringSubmatch(sql)
		if m == nil {
			t.Fatalf("no CREATE TABLE for %s in storage/trino/init.sql", tbl.Name)
		}
		var cols []string
		for _, line := range strings.Split(m[1], "\n") {
			f := strings.Fields(strings.TrimSuffix(strings.TrimSpace(line), ","))
			if len(f) >= 2 && f[0] != "tenant_id" && !strings.HasPrefix(f[0], "--") {
				cols = append(cols, f[0]+" "+strings.ToUpper(f[1]))
			}
		}
		var want []string
		for _, c := range tbl.Columns {
			want = append(want, c.Name+" "+c.Type)
		}
		if strings.Join(cols, ", ") != strings.Join(want, ", ") {
			t.Errorf("%s: init.sql has\n  %s\nTables() has\n  %s", tbl.Name, strings.Join(cols, ", "), strings.Join(want, ", "))
		}
	}
}

func TestSyncRefusesABadBucketBeforeTouchingTrino(t *testing.T) {
	if _, err := Sync(context.Background(), nil, "Bad Bucket'", nil); err == nil {
		t.Fatal("Sync accepted a bucket name that would end up inside a string literal")
	}
}
