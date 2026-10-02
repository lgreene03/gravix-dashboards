//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestCubeAnswersOnTheFullStack asks the full stack's Cube, which reads Trino,
// for the fixture rows docker-smoke inserts: 33 requests and 1 error for the
// service ci-fixture.
//
// Nothing asked that Cube a question before, and it had two faults. It refused
// every query, because in production mode Cube's cache driver defaults to Cube
// Store, which no stack runs (F-074; the bootstrap stack fixed the same thing
// in F-035). Behind that, errorRate divided two BIGINT sums, which Trino does
// as integers, so every error rate under 100% was 0 (CD-007).
//
// It signs its token with the stack's JWT_SECRET, as the gateway does, and
// without a tenant: the full stack's tables have no tenant column (F-070).
func TestCubeAnswersOnTheFullStack(t *testing.T) {
	requireLiveStack(t)
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		t.Fatal("JWT_SECRET is not set; source the stack's .env before running this test")
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}

	const r = "RequestMetricsMinute"
	query, _ := json.Marshal(map[string]any{"query": map[string]any{
		"measures": []string{r + ".requestCount", r + ".errorCount", r + ".errorRate"},
		"filters": []map[string]any{{
			"member": r + ".service", "operator": "equals", "values": []string{"ci-fixture"},
		}},
	}})

	var answer struct {
		Error string           `json:"error"`
		Data  []map[string]any `json:"data"`
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		req, err := http.NewRequest(http.MethodPost, "http://localhost:4000/cubejs-api/v1/load", bytes.NewReader(query))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("Cube at localhost:4000 did not answer: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		answer.Error, answer.Data = "", nil
		if err := json.Unmarshal(body, &answer); err != nil {
			t.Fatalf("Cube answered %s with something other than JSON: %s", resp.Status, body)
		}
		if answer.Error == "Continue wait" && time.Now().Before(deadline) {
			time.Sleep(time.Second)
			continue
		}
		if answer.Error != "" {
			t.Fatalf("Cube refused the query (%s): %s", resp.Status, answer.Error)
		}
		break
	}
	if len(answer.Data) != 1 {
		t.Fatalf("want one row, got %d: %v", len(answer.Data), answer.Data)
	}

	row := answer.Data[0]
	num := func(m string) float64 {
		t.Helper()
		switch v := row[r+"."+m].(type) {
		case float64:
			return v
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err == nil {
				return f
			}
		}
		t.Fatalf("%s is %v (%T), not a number", m, row[r+"."+m], row[r+"."+m])
		return 0
	}
	if got := num("requestCount"); got != 33 {
		t.Errorf("requestCount = %v, want 33", got)
	}
	if got := num("errorCount"); got != 1 {
		t.Errorf("errorCount = %v, want 1", got)
	}
	if got := num("errorRate"); math.Abs(got-1.0/33) > 1e-9 {
		t.Errorf("errorRate = %v, want 1/33 = %v; 0 means the division was done in integers (CD-007)", got, 1.0/33)
	}
}
