// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
)

// exportHandlers are the handlers behind every export route. GRVX-1107's
// route-level criteria are about these, and SD-029 found they live in
// gateway_platform.go and main.go, not in the file the spec named.
var exportHandlers = []string{"handleExport", "handleScheduledExports", "handleScheduledExportByID",
	"handleOnDemandExport", "handleScheduledExportRuns", "runScheduledExport"}

// TestExportHasNoPlanGate is GRVX-1107 AC-7. Leaving with your data is free on
// every plan (charter §7.3, the data-ownership axis), so no export route may be
// gated by plan: not in its middleware chain, and not inside its handler.
func TestExportHasNoPlanGate(t *testing.T) {
	fset := token.NewFileSet()
	var routes int
	for _, file := range []string{"main.go", "gateway_platform.go", "export_server.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, `mux.HandleFunc("/api/gateway/export`) {
				continue
			}
			routes++
			if strings.Contains(line, "requirePlan") || strings.Contains(line, "planRank") {
				t.Errorf("an export route is plan-gated: %s", strings.TrimSpace(line))
			}
		}

		f, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !contains(exportHandlers, fn.Name.Name) {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if x.Name == "requirePlan" || x.Name == "planRank" || x.Name == "StatusPaymentRequired" {
						t.Errorf("%s references %s; export must not depend on plan", fn.Name.Name, x.Name)
					}
				case *ast.SelectorExpr:
					if x.Sel.Name == "StatusPaymentRequired" {
						t.Errorf("%s can answer 402; export must not depend on plan", fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	if routes < 3 {
		t.Fatalf("found %d export routes; the registration pattern changed and this test is checking nothing", routes)
	}
}

// TestAnyRoleCanExport is GRVX-1107 AC-8: export is how a user leaves with
// data they can already see, so a viewer may export as well as an admin.
func TestAnyRoleCanExport(t *testing.T) {
	gw, store := newTestGatewayWithStore(t)
	// The production constructor makes this map; the shared test gateway does
	// not, because nothing had exercised handleExport in a test before.
	gw.activeExports = map[string]bool{}
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	key := "raw/" + tenant.ID + "/request_facts/2026-05-21/10/batch.jsonl"
	if err := store.Put(context.Background(), key, bytes.NewReader([]byte(`{"service":"checkout"}`+"\n"))); err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin} {
		body := `{"start_date":"2026-05-21","end_date":"2026-05-21","data_type":"request_facts"}`
		req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/archive", strings.NewReader(body))
		claims := &auth.Claims{TenantID: tenant.ID, UserID: "u-" + role, Email: role + "@example.com", Role: role}
		req = req.WithContext(auth.WithClaims(req.Context(), claims))
		rr := httptest.NewRecorder()

		gw.handleExport(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("%s: export answered %d, want 200: %s", role, rr.Code, rr.Body.String())
			continue
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/gzip" {
			t.Errorf("%s: Content-Type %q, want application/gzip", role, ct)
		}
	}
}

// TestExportRoutesAreOneFamily pins SD-029's resolution. A route one character
// from another — "/api/gateway/export" beside "/api/gateway/exports" — sends
// clients to the wrong one with nothing to tell them. Every export route is
// the family's root, "/api/gateway/exports" (GRVX-1107 §5.4's on-demand
// export), or under it, and the singular path is gone.
func TestExportRoutesAreOneFamily(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`mux\.HandleFunc\("(/api/gateway/export[^"]*)"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		if m[1] != "/api/gateway/exports" && !strings.HasPrefix(m[1], "/api/gateway/exports/") {
			t.Errorf("export route %q is outside the /api/gateway/exports/ family (SD-029)", m[1])
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestExistingScheduleValidationIntact is GRVX-1107 AC-12, over every rule
// SD-029 recorded for scheduled exports rather than the three the older tests
// happened to cover. A valid schedule is the baseline; each case changes one
// field and must be refused, and the defaulting rules must still apply.
func TestExistingScheduleValidationIntact(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	admin := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleAdmin}

	valid := func() map[string]interface{} {
		return map[string]interface{}{
			"name": "nightly", "schedule": "0 3 * * *",
		}
	}
	post := func(t *testing.T, fields map[string]interface{}, claims *auth.Claims) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/scheduled", jsonBody(t, fields))
		req = req.WithContext(auth.WithClaims(req.Context(), claims))
		rr := httptest.NewRecorder()
		gw.handleScheduledExports(rr, req)
		return rr
	}

	refused := map[string]func(map[string]interface{}){
		"blank name":         func(f map[string]interface{}) { f["name"] = "   " },
		"not a 5-field cron": func(f map[string]interface{}) { f["schedule"] = "0 3 * *" },
		// No destination is the caller's to choose (DD-041). This refuses
		// everything the old s3://-only rule refused, and s3:// as well.
		"a file destination":    func(f map[string]interface{}) { f["destination_url"] = "file:///tmp/x" },
		"an s3 destination":     func(f map[string]interface{}) { f["destination_url"] = "s3://bucket/prefix/" },
		"an out-of-range cron":  func(f map[string]interface{}) { f["schedule"] = "61 3 * * *" },
		"unknown data_type":     func(f map[string]interface{}) { f["data_type"] = "metrics_minute" },
		"unknown format":        func(f map[string]interface{}) { f["format"] = "xlsx" },
		"lookback over 90 days": func(f map[string]interface{}) { f["lookback_days"] = 91 },
	}
	for name, mutate := range refused {
		fields := valid()
		mutate(fields)
		if rr := post(t, fields, admin); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rr.Code, rr.Body.String())
		}
	}

	// A viewer may not create one, whatever the body.
	viewer := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleViewer}
	if rr := post(t, valid(), viewer); rr.Code != http.StatusForbidden {
		t.Errorf("a viewer created a schedule: status %d, want 403", rr.Code)
	}

	// The defaults still apply: data_type request_facts, format jsonl, and a
	// non-positive lookback becomes 7.
	fields := valid()
	fields["lookback_days"] = 0
	rr := post(t, fields, admin)
	if rr.Code != http.StatusCreated && rr.Code != http.StatusOK {
		t.Fatalf("a valid schedule was refused: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		DataType     string
		Format       string
		LookbackDays int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created schedule: %v: %s", err, rr.Body.String())
	}
	if created.DataType != "request_facts" || created.Format != "jsonl" || created.LookbackDays != 7 {
		t.Errorf("defaults not applied: data_type %q, format %q, lookback %d; want request_facts, jsonl, 7",
			created.DataType, created.Format, created.LookbackDays)
	}
}
