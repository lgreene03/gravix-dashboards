//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lgreene/gravix-dashboards/pkg/trinocatalog"

	_ "github.com/trinodb/trino-go-client/trino"
)

// TestTrinoServesEachTenantThroughCube is F-070. The full stack's rollups write
// warehouse/<tenant>/<table>/, and Trino's tables read warehouse/<table>/, so a
// signed-in user's dashboard read nothing. pkg/trinocatalog registers each
// tenant's directory as its own table and serves them through one view per
// table, which Cube reads.
//
// It registers two tenants, writes rows into each one's own table through
// Trino, so the files land where a rollup puts them, and asks Cube as each
// tenant and as a third that is not registered. Each must see exactly its own
// rows.
func TestTrinoServesEachTenantThroughCube(t *testing.T) {
	requireLiveStack(t)
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		t.Fatal("JWT_SECRET is not set; source the stack's .env before running this test")
	}
	db, err := sql.Open("trino", hiveDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const a, b, absent = "f0700000-0000-4000-8000-00000000000a", "f0700000-0000-4000-8000-00000000000b",
		"f0700000-0000-4000-8000-0000000000ff"
	if skipped, err := trinocatalog.Sync(ctx, db, "gravix", []string{a, b}); err != nil || len(skipped) > 0 {
		t.Fatalf("Sync: skipped %v, err %v", skipped, err)
	}

	// A service name of its own, so a rerun against the same stack counts only
	// this run's rows.
	service := fmt.Sprintf("e2e-f070-%d", time.Now().UnixNano())
	day := time.Now().UTC().Format("2006-01-02")
	for tenant, requests := range map[string]int{a: 7, b: 5} {
		insert := fmt.Sprintf(`INSERT INTO gravix.tenants.%q (tenant_id, bucket_start, service, method,
			path_template, request_count, error_count, error_rate, p50_latency_ms, p95_latency_ms,
			p99_latency_ms, event_day) VALUES ('%s', '%s 00:00:00', '%s', 'GET', '/f070', %d, 1, 0.1,
			1.0, 2.0, 3.0, '%s')`,
			trinocatalog.TableName(tenant, "request_metrics_minute"), tenant, day, service, requests, day)
		if _, err := db.ExecContext(ctx, insert); err != nil {
			t.Fatalf("writing tenant %s's rows: %v", tenant, err)
		}
	}

	ask := func(tenant string) float64 {
		t.Helper()
		tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"tenant_id": tenant, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		rows := askCube(t, tok, map[string]any{
			"measures": []string{"RequestMetricsMinute.requestCount"},
			"filters": []map[string]any{{
				"member": "RequestMetricsMinute.service", "operator": "equals", "values": []string{service},
			}},
		})
		if len(rows) == 0 {
			return 0
		}
		v, _ := strconv.ParseFloat(fmt.Sprint(rows[0]["RequestMetricsMinute.requestCount"]), 64)
		return v
	}
	for tenant, want := range map[string]float64{a: 7, b: 5, absent: 0} {
		if got := ask(tenant); got != want {
			t.Errorf("tenant %s read %v requests through Cube on Trino, want %v", tenant, got, want)
		}
	}
}
