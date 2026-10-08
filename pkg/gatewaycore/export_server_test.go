// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// exportGateway is a gateway whose export store holds one day of a tenant's
// facts, in the keys ingestion writes.
func exportGateway(t *testing.T, day time.Time, facts int) (*gateway, storage.ObjectStore, *tenantdb.Tenant) {
	t.Helper()
	gw := newTestGateway(t)
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gw.metricStore = store
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	var buf bytes.Buffer
	for i := 0; i < facts; i++ {
		fmt.Fprintf(&buf, `{"event_id":"e%d","event_time":"%s","service":"checkout","method":"GET","path_template":"/orders/{id}","status_code":200,"latency_ms":%d,"user_agent_family":"curl","tenant_id":"%s"}`+"\n",
			i, day.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), 10+i, tenant.ID)
	}
	key := fmt.Sprintf("raw/%s/request_facts/%s/10/batch.jsonl", tenant.ID, day.Format("2006-01-02"))
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	return gw, store, tenant
}

func exportAs(gw *gateway, tenantID, role, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports", strings.NewReader(body))
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, UserID: "u", Email: "u@example.com", Role: role}))
	rr := httptest.NewRecorder()
	gw.handleOnDemandExport(rr, req)
	return rr
}

var exportDay = time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)

// GRVX-1107 AC-8 for the on-demand endpoint: a viewer leaves with the data
// they can see, and the export is the response.
func TestOnDemandExportStreamsToTheCaller(t *testing.T) {
	gw, _, tenant := exportGateway(t, exportDay, 3)

	for _, role := range []string{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin} {
		rr := exportAs(gw, tenant.ID, role, `{"dataset":"facts","format":"jsonl","from":"2026-05-21","to":"2026-05-22"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", role, rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
			t.Errorf("%s: Content-Type %q", role, ct)
		}
		if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "facts_20260521_20260522.jsonl") {
			t.Errorf("%s: Content-Disposition %q", role, cd)
		}
		if n := strings.Count(rr.Body.String(), "\n"); n != 3 {
			t.Errorf("%s: %d rows, want 3:\n%s", role, n, rr.Body.String())
		}
	}
}

// DD-041. Any role may export, so no caller may choose where an export is
// written: not a path on the gateway's disk, and not a bucket the gateway
// can write to.
func TestOnDemandExportRefusesADestination(t *testing.T) {
	gw, store, tenant := exportGateway(t, exportDay, 1)
	for _, dest := range []string{`"file:///tmp/gravix-pwned"`, `"s3://someone-elses-bucket/x"`, `"-"`, `null`} {
		rr := exportAs(gw, tenant.ID, auth.RoleViewer,
			`{"dataset":"facts","format":"jsonl","from":"2026-05-21","to":"2026-05-22","destination":`+dest+`}`)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `invalid field \"destination\"`) {
			t.Errorf("destination %s: status %d, body %s; want 400 naming the field", dest, rr.Code, rr.Body.String())
		}
	}
	keys, _ := store.List(context.Background(), "")
	for _, k := range keys {
		if !strings.HasPrefix(k, "raw/") {
			t.Errorf("an export wrote %s", k)
		}
	}
}

// GRVX-1107 AC-11 at the endpoint: a filter or a query is refused by name,
// not silently ignored into a whole-range export.
func TestOnDemandExportRefusesAQuery(t *testing.T) {
	gw, _, tenant := exportGateway(t, exportDay, 1)
	for _, field := range []string{"filter", "where", "query", "sql"} {
		rr := exportAs(gw, tenant.ID, auth.RoleViewer,
			`{"dataset":"facts","from":"2026-05-21","to":"2026-05-22","`+field+`":"service = 'checkout'"}`)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `invalid field \"`+field+`\"`) {
			t.Errorf("%s: status %d, body %s; want 400 naming it", field, rr.Code, rr.Body.String())
		}
	}
}

// GRVX-1107 §6.1's failure modes, with their exact messages.
func TestOnDemandExportFailureModes(t *testing.T) {
	gw, _, tenant := exportGateway(t, exportDay, 1)
	for name, tc := range map[string]struct {
		body string
		code int
		msg  string
	}{
		"unknown dataset": {`{"dataset":"traces","from":"2026-05-21","to":"2026-05-22"}`, 400, `invalid field \"dataset\": must be facts, metrics, or events`},
		"unknown format":  {`{"dataset":"facts","format":"xlsx","from":"2026-05-21","to":"2026-05-22"}`, 400, `invalid field \"format\": must be parquet, csv, or jsonl`},
		"to before from":  {`{"dataset":"facts","from":"2026-05-22","to":"2026-05-21"}`, 400, `invalid field \"to\": must be after from`},
		"bad from":        {`{"dataset":"facts","from":"yesterday","to":"2026-05-21"}`, 400, `invalid field \"from\"`},
		"no data":         {`{"dataset":"facts","from":"2026-01-01","to":"2026-01-02"}`, 422, "no data in range 2026-01-01T00:00:00Z .. 2026-01-02T00:00:00Z"},
	} {
		rr := exportAs(gw, tenant.ID, auth.RoleViewer, tc.body)
		if rr.Code != tc.code || !strings.Contains(rr.Body.String(), tc.msg) {
			t.Errorf("%s: status %d, body %s; want %d and %q", name, rr.Code, rr.Body.String(), tc.code, tc.msg)
		}
	}
}

// One tenant's export never contains another's facts.
func TestOnDemandExportIsTheCallersTenant(t *testing.T) {
	gw, _, _ := exportGateway(t, exportDay, 2)
	rr := exportAs(gw, "some-other-tenant", auth.RoleAdmin, `{"dataset":"facts","format":"jsonl","from":"2026-05-21","to":"2026-05-22"}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("another tenant's export: status %d, body %s; want 422, no data", rr.Code, rr.Body.String())
	}
}

// createSchedule stores a schedule the way the API does and returns it.
func createSchedule(t *testing.T, gw *gateway, tenantID, cron string) *tenantdb.ScheduledExport {
	t.Helper()
	e := &tenantdb.ScheduledExport{TenantID: tenantID, Name: "nightly", Schedule: cron, DataType: "request_facts",
		Format: "jsonl", LookbackDays: 1, Status: "active"}
	if err := gw.db.ScheduledExports().Create(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	got, err := gw.db.ScheduledExports().GetByID(context.Background(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// GRVX-1107 AC-10. A scheduled run and an on-demand export of the same range
// are the same rows, because they are the same engine.
func TestScheduledAndOnDemandAgree(t *testing.T) {
	gw, store, tenant := exportGateway(t, exportDay, 4)
	s := createSchedule(t, gw, tenant.ID, "0 3 * * *")

	due := exportDay.AddDate(0, 0, 1).Add(3 * time.Hour)
	if _, err := gw.runScheduledExport(context.Background(), s, due); err != nil {
		t.Fatalf("scheduled run: %v", err)
	}
	key := path.Join(scheduledExportPrefix(tenant.ID, s.ID), due.Format(runLayout), "facts_20260521.jsonl")
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("the scheduled run wrote no %s: %v", key, err)
	}
	var scheduled bytes.Buffer
	_, _ = scheduled.ReadFrom(rc)
	rc.Close()

	rr := exportAs(gw, tenant.ID, auth.RoleViewer, `{"dataset":"facts","format":"jsonl","from":"2026-05-21","to":"2026-05-22"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("on-demand: %d %s", rr.Code, rr.Body.String())
	}
	if scheduled.String() != rr.Body.String() || strings.Count(scheduled.String(), "\n") != 4 {
		t.Errorf("scheduled and on-demand differ:\nscheduled:\n%s\non demand:\n%s", scheduled.String(), rr.Body.String())
	}
}

// F-054. Schedules were stored and listed and never run. The runner runs one
// when it comes due, records the run, and does not run the same minute twice.
func TestScheduledExportRunsWhenDue(t *testing.T) {
	// Ten seconds into a minute after the schedule exists, so the second call
	// below is in the same minute whatever the wall clock says, and the data
	// is the day before that minute's day.
	later := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Minute).Add(10 * time.Second)
	yesterday := later.Truncate(24*time.Hour).AddDate(0, 0, -1)
	gw, store, tenant := exportGateway(t, yesterday, 2)
	s := createSchedule(t, gw, tenant.ID, "* * * * *")

	gw.runScheduledExports(context.Background(), later)

	got, err := gw.db.ScheduledExports().GetByID(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(later.Truncate(time.Minute)) || got.LastError != "" {
		t.Fatalf("after a due run: LastRunAt %v, LastError %q; want %v and none", got.LastRunAt, got.LastError, later.Truncate(time.Minute))
	}
	runs, _ := store.List(context.Background(), scheduledExportPrefix(tenant.ID, s.ID))
	if len(runs) != 2 { // the day's file and its manifest
		t.Fatalf("the run wrote %v; want one data file and a manifest", runs)
	}

	// Again within the same minute: nothing is due.
	gw.runScheduledExports(context.Background(), later.Add(30*time.Second))
	again, _ := store.List(context.Background(), scheduledExportPrefix(tenant.ID, s.ID))
	if len(again) != len(runs) {
		t.Errorf("the same minute ran twice: %v", again)
	}

	// A paused schedule does not run.
	got.Status = "paused"
	if err := gw.db.ScheduledExports().Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	gw.runScheduledExports(context.Background(), later.Add(5*time.Minute))
	paused, _ := store.List(context.Background(), scheduledExportPrefix(tenant.ID, s.ID))
	if len(paused) != len(runs) {
		t.Errorf("a paused schedule ran: %v", paused)
	}
}

// A run with nothing to export says so where the tenant reads it.
func TestScheduledExportRecordsAnEmptyRange(t *testing.T) {
	gw, _, tenant := exportGateway(t, exportDay, 1) // data from May; the run covers yesterday
	s := createSchedule(t, gw, tenant.ID, "* * * * *")
	gw.runScheduledExports(context.Background(), time.Now().Add(2*time.Minute))
	got, _ := gw.db.ScheduledExports().GetByID(context.Background(), s.ID)
	if got.LastRunAt == nil || !strings.Contains(got.LastError, "no data in range") {
		t.Errorf("LastRunAt %v, LastError %q; want the run recorded with its reason", got.LastRunAt, got.LastError)
	}
}

// Runs are listed and downloaded by any role of the schedule's tenant, and by
// no one else; a file name cannot reach outside the run.
func TestScheduledExportRunsAreReadable(t *testing.T) {
	gw, _, tenant := exportGateway(t, exportDay, 3)
	s := createSchedule(t, gw, tenant.ID, "0 3 * * *")
	due := exportDay.AddDate(0, 0, 1).Add(3 * time.Hour)
	if _, err := gw.runScheduledExport(context.Background(), s, due); err != nil {
		t.Fatal(err)
	}
	get := func(tenantID, p string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/gateway/exports/scheduled/"+s.ID+p, nil)
		req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, Role: auth.RoleViewer}))
		rr := httptest.NewRecorder()
		gw.handleScheduledExportByID(rr, req)
		return rr
	}

	rr := get(tenant.ID, "/runs")
	var listing struct {
		Runs []struct {
			Run   string   `json:"run"`
			Files []string `json:"files"`
		} `json:"runs"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &listing) != nil || len(listing.Runs) != 1 {
		t.Fatalf("listing: %d %s", rr.Code, rr.Body.String())
	}
	run := listing.Runs[0]
	if run.Run != "20260522T0300Z" || strings.Join(run.Files, ",") != "facts_20260521.jsonl,manifest.json" {
		t.Errorf("run = %+v", run)
	}

	rr = get(tenant.ID, "/runs/"+run.Run+"/facts_20260521.jsonl")
	if rr.Code != http.StatusOK || strings.Count(rr.Body.String(), "\n") != 3 {
		t.Errorf("download: %d %s", rr.Code, rr.Body.String())
	}
	if rr := get("another-tenant", "/runs"); rr.Code != http.StatusNotFound {
		t.Errorf("another tenant listed the runs: %d", rr.Code)
	}
	for _, p := range []string{"/runs/" + run.Run + "/../../../x", "/runs/../" + run.Run, "/runs/" + run.Run + "/secrets.txt", "/other"} {
		if rr := get(tenant.ID, p); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d; want 404", p, rr.Code)
		}
	}
}

func TestCronSpec(t *testing.T) {
	at := func(s string) time.Time { tm, _ := time.Parse("2006-01-02 15:04", s); return tm }
	for _, tc := range []struct {
		expr string
		t    string
		want bool
	}{
		{"0 3 * * *", "2026-05-21 03:00", true},
		{"0 3 * * *", "2026-05-21 03:01", false},
		{"*/15 * * * *", "2026-05-21 10:45", true},
		{"*/15 * * * *", "2026-05-21 10:44", false},
		{"0 0 1 * *", "2026-06-01 00:00", true},
		{"0 0 * * 0", "2026-05-24 00:00", true}, // a Sunday
		{"0 0 * * 7", "2026-05-24 00:00", true}, // Sunday as 7
		{"0 0 * * 1", "2026-05-24 00:00", false},
		{"0 0 1 * 1", "2026-05-25 00:00", true}, // both day fields set: either matches
		{"0 0 1 * 1", "2026-05-26 00:00", false},
		{"0 0 * 2 *", "2026-05-01 00:00", false},
	} {
		c, err := parseCron(tc.expr)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		if got := c.matches(at(tc.t)); got != tc.want {
			t.Errorf("%q at %s = %v; want %v", tc.expr, tc.t, got, tc.want)
		}
	}
	for _, bad := range []string{"60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "0 3 * *"} {
		if _, err := parseCron(bad); err == nil {
			t.Errorf("parseCron(%q) accepted it", bad)
		}
	}

	c, _ := parseCron("0 3 * * *")
	if due, ok := c.lastMatch(at("2026-05-20 03:00"), at("2026-05-22 10:00")); !ok || !due.Equal(at("2026-05-22 03:00")) {
		t.Errorf("two missed runs: due %v, %v; want the latest only, 2026-05-22 03:00", due, ok)
	}
	if _, ok := c.lastMatch(at("2026-05-22 03:00"), at("2026-05-22 10:00")); ok {
		t.Error("a run already done was due again")
	}
}
