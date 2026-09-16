// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// GRVX-1104. The five criteria provable without a Grafana or a Trino; AC-4
// needs a live Trino and is in tests/e2e alongside AC-7.
//
// The field allowlist gets the most attention here because it is the only
// place where anything derived from user input reaches the SQL text rather
// than a bound parameter. Everything else in the statement is a `?`.

// -------------------------------------------------------------------- AC-1 --

func TestFieldColumnSupportedValues(t *testing.T) {
	// The six §5.2 names, spelled out rather than ranged over fieldColumns,
	// so that adding a column to the map without deciding it belongs in the
	// public contract fails here.
	want := map[string]string{
		"request_count":  "request_count",
		"error_count":    "error_count",
		"error_rate":     "error_rate",
		"p50_latency_ms": "p50_latency_ms",
		"p95_latency_ms": "p95_latency_ms",
		"p99_latency_ms": "p99_latency_ms",
	}

	for field, col := range want {
		got, err := fieldColumn(field)
		if err != nil {
			t.Errorf("fieldColumn(%q) returned %v", field, err)
			continue
		}
		if got != col {
			t.Errorf("fieldColumn(%q) = %q, want %q", field, got, col)
		}
	}

	if len(fieldColumns) != len(want) {
		t.Errorf("fieldColumns has %d entries, the contract names %d; a column was added to the "+
			"allowlist without being added to the documented set", len(fieldColumns), len(want))
	}
}

// -------------------------------------------------------------------- AC-2 --

func TestFieldColumnRejectsUnsupported(t *testing.T) {
	for _, field := range []string{
		"", "tenant_id", "service", "bucket_start",
		"request_count; DROP TABLE x",
		"request_count, (SELECT 1)",
		"REQUEST_COUNT",
		"*",
	} {
		got, err := fieldColumn(field)
		if !errors.Is(err, ErrUnsupportedField) {
			t.Errorf("fieldColumn(%q) = (%q, %v), want ErrUnsupportedField", field, got, err)
		}
		if got != "" {
			t.Errorf("fieldColumn(%q) returned a column %q alongside its error; a caller that "+
				"checked the value before the error would interpolate it", field, got)
		}
	}
}

// TestOnlyAllowlistedColumnsReachTheStatement is the property the two tests
// above exist to support, asserted directly.
//
// fmt.Sprintf into SQL is a smell worth earning: it is safe here only because
// its one verb is fed exclusively from fieldColumns. If that ever stops being
// true this test is what notices.
func TestOnlyAllowlistedColumnsReachTheStatement(t *testing.T) {
	if strings.Count(querySQL, "%s") != 1 {
		t.Fatalf("querySQL has %d format verbs; exactly one (the column) is intended",
			strings.Count(querySQL, "%s"))
	}
	for _, col := range fieldColumns {
		if strings.ContainsAny(col, " ;'\"()*,") {
			t.Errorf("allowlisted column %q is not a bare identifier", col)
		}
	}
	// Every other variable in the statement is bound.
	if want := 7; strings.Count(querySQL, "?") != want {
		t.Errorf("querySQL binds %d parameters, want %d — a value moved out of a placeholder",
			strings.Count(querySQL, "?"), want)
	}
}

// -------------------------------------------------------------------- AC-3 --

func TestQueryDataRequiresService(t *testing.T) {
	// No database: an empty Service must be refused before anything is
	// executed, and a nil *sql.DB makes that unmissable.
	ds := &Datasource{}

	body, err := json.Marshal(QueryModel{Field: "request_count"})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}

	resp, err := ds.QueryData(context.Background(), &backend.QueryDataRequest{
		Queries: []backend.DataQuery{{RefID: "A", JSON: body}},
	})
	if err != nil {
		t.Fatalf("QueryData returned a transport error: %v", err)
	}

	got := resp.Responses["A"]
	if got.Error == nil {
		t.Fatal("a query with no service succeeded")
	}
	if got.Error.Error() != "service is required" {
		t.Errorf("error = %q, want %q (§6.1 fixes the text)", got.Error, "service is required")
	}
}

// TestQueryDataRejectsUnsupportedFieldBeforeQuerying — same shape, second gate.
func TestQueryDataRejectsUnsupportedFieldBeforeQuerying(t *testing.T) {
	ds := &Datasource{}

	body, err := json.Marshal(QueryModel{Service: "checkout", Field: "tenant_id"})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}

	resp, err := ds.QueryData(context.Background(), &backend.QueryDataRequest{
		Queries: []backend.DataQuery{{RefID: "A", JSON: body}},
	})
	if err != nil {
		t.Fatalf("QueryData returned a transport error: %v", err)
	}

	got := resp.Responses["A"]
	if got.Error == nil {
		t.Fatal("a query naming an unlisted column succeeded")
	}
	if got.Error.Error() != ErrUnsupportedField.Error() {
		t.Errorf("error = %q, want %q", got.Error, ErrUnsupportedField)
	}
}

// -------------------------------------------------------------------- AC-5 --

func TestCheckHealthUnreachable(t *testing.T) {
	// A port that was open long enough to be allocated and is now closed, so
	// the connection is refused rather than hanging.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	settings := backend.DataSourceInstanceSettings{
		JSONData: []byte(`{"trinoHost":"127.0.0.1","trinoPort":` + itoa(port) + `}`),
	}
	inst, err := NewDatasource(context.Background(), settings)
	if err != nil {
		t.Fatalf("NewDatasource: %v", err)
	}
	ds := inst.(*Datasource)
	defer ds.Dispose()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := ds.CheckHealth(ctx, &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("CheckHealth returned a transport error: %v", err)
	}
	if res.Status != backend.HealthStatusError {
		t.Errorf("status = %v, want HealthStatusError", res.Status)
	}
	if !strings.HasPrefix(res.Message, "gravix: trino unreachable: ") {
		t.Errorf("message = %q, want the §6.1 prefix %q", res.Message, "gravix: trino unreachable: ")
	}
}

// TestNewDatasourceDefaults — the settings fallback §5.2 specifies.
func TestNewDatasourceDefaults(t *testing.T) {
	for name, jsonData := range map[string]string{
		"empty":     "",
		"null":      "null",
		"malformed": "{not json",
		"partial":   `{"trinoHost":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			inst, err := NewDatasource(context.Background(),
				backend.DataSourceInstanceSettings{JSONData: []byte(jsonData)})
			if err != nil {
				t.Fatalf("NewDatasource: %v", err)
			}
			ds := inst.(*Datasource)
			defer ds.Dispose()

			if ds.trinoHost != "localhost" || ds.trinoPort != 8081 {
				t.Errorf("defaults = %s:%d, want localhost:8081", ds.trinoHost, ds.trinoPort)
			}
		})
	}
}

// -------------------------------------------------------------------- AC-6 --

// TestBuildIsCGOFree — Grafana ships the backend binary to whatever machine
// runs Grafana. A cgo dependency would make it built-here-only, which for a
// plugin is the same as broken.
func TestBuildIsCGOFree(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving the module root: %v", err)
	}

	// ./cmd, not ./..., because that is the one package that becomes the
	// binary Grafana ships, and `go build -o <file>` refuses more than one.
	cmd := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "gpx"), "./cmd")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("CGO_ENABLED=0 go build failed, so this plugin cannot be cross-built for the "+
			"machine that runs Grafana:\n%v\n%s", err, out)
	}
}

// -------------------------------------------------------------------- AC-4 --

// TestCheckHealthOK needs a live Trino, which is the whole point: the
// unreachable case above proves the error path, and only a real connection
// proves the success one. A mock of Trino would have been written by whoever
// wrote the assumption being tested.
func TestCheckHealthOK(t *testing.T) {
	if !trinoReachable() {
		t.Skip("Trino is not reachable at localhost:8081; run docker-compose up -d to run this test")
	}

	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{})
	if err != nil {
		t.Fatalf("NewDatasource: %v", err)
	}
	ds := inst.(*Datasource)
	defer ds.Dispose()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := ds.CheckHealth(ctx, &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("CheckHealth returned a transport error: %v", err)
	}
	if res.Status != backend.HealthStatusOk {
		t.Fatalf("status = %v with message %q, want HealthStatusOk", res.Status, res.Message)
	}
	if res.Message != "gravix: trino reachable" {
		t.Errorf("message = %q, want %q (§6 step 5 fixes the text)", res.Message, "gravix: trino reachable")
	}
}

func trinoReachable() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:8081/v1/info")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
