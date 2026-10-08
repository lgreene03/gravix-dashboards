//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// TestOnDemandExportOnTheFullStack is DD-041 and F-081 on the running stack.
// Ingestion writes a tenant's facts to MinIO, and the gateway read its own
// local disk, so every endpoint that reads the store answered with nothing.
// It writes facts where ingestion does, for a tenant of its own, and exports
// them through the gateway as that tenant's viewer: the rows must come back,
// and a destination chosen by the caller must be refused.
func TestOnDemandExportOnTheFullStack(t *testing.T) {
	requireLiveStack(t)
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		t.Fatal("JWT_SECRET is not set; source the stack's .env before running this test")
	}
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		bucket = "gravix"
	}
	region := os.Getenv("S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	ctx := context.Background()
	store, err := storage.NewS3Store(ctx, "http://127.0.0.1:9000", region, bucket,
		os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"))
	if err != nil {
		t.Fatalf("MinIO: %v", err)
	}

	tenant := uuid.NewString()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	const facts = 4
	var buf bytes.Buffer
	for i := 0; i < facts; i++ {
		fmt.Fprintf(&buf, `{"event_id":"%s","event_time":"%s","service":"e2e-export","method":"GET","path_template":"/x/{id}","status_code":200,"latency_ms":%d,"user_agent_family":"curl","tenant_id":"%s"}`+"\n",
			uuid.NewString(), day.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), 5+i, tenant)
	}
	key := fmt.Sprintf("raw/%s/request_facts/%s/10/e2e-export.jsonl", tenant, day.Format("2006-01-02"))
	if err := store.Put(ctx, key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("writing %s: %v", key, err)
	}
	t.Cleanup(func() { _ = store.Delete(ctx, key) })

	tok, err := auth.NewTokenService(secret, time.Hour).Generate(tenant, "e2e-viewer", "viewer@example.com", auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, "http://localhost:8091/api/gateway/exports", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("POST /api/gateway/exports: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	rangeJSON := fmt.Sprintf(`"from":"%s","to":"%s"`, day.Format("2006-01-02"), day.AddDate(0, 0, 1).Format("2006-01-02"))
	code, body := post(`{"dataset":"facts","format":"jsonl",` + rangeJSON + `}`)
	if code != http.StatusOK {
		t.Fatalf("export answered %d: %s", code, body)
	}
	if got := strings.Count(body, `"service":"e2e-export"`); got != facts {
		t.Errorf("export returned %d of the tenant's %d facts:\n%s", got, facts, body)
	}

	code, body = post(`{"dataset":"facts","format":"jsonl",` + rangeJSON + `,"destination":"file:///app/data/x"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "destination") {
		t.Errorf("a caller's destination was not refused: %d %s", code, body)
	}
}
