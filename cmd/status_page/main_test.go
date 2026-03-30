package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewStatusPage(t *testing.T) {
	sp := newStatusPage()
	if sp == nil {
		t.Fatal("newStatusPage returned nil")
	}
	if sp.services == nil {
		t.Fatal("services map not initialized")
	}
	if len(sp.services) != 0 {
		t.Fatalf("expected empty services map, got %d entries", len(sp.services))
	}
	if sp.client == nil {
		t.Fatal("HTTP client not initialized")
	}
	if sp.client.Timeout != 10*time.Second {
		t.Fatalf("expected 10s timeout, got %s", sp.client.Timeout)
	}
}

func TestAddService(t *testing.T) {
	sp := newStatusPage()
	sp.addService("Ingestion", "http://localhost:8090/live")
	sp.addService("Gateway", "http://localhost:8091/live")

	if len(sp.services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(sp.services))
	}

	svc, ok := sp.services["Ingestion"]
	if !ok {
		t.Fatal("Ingestion service not found")
	}
	if svc.name != "Ingestion" {
		t.Errorf("expected name Ingestion, got %s", svc.name)
	}
	if svc.url != "http://localhost:8090/live" {
		t.Errorf("expected url http://localhost:8090/live, got %s", svc.url)
	}
	if svc.status != "unknown" {
		t.Errorf("expected initial status unknown, got %s", svc.status)
	}
	if svc.dailyChecks == nil {
		t.Error("dailyChecks map not initialized")
	}
	if svc.dailyFailures == nil {
		t.Error("dailyFailures map not initialized")
	}
}

func TestAddServiceOverwrite(t *testing.T) {
	sp := newStatusPage()
	sp.addService("Svc", "http://old-url")
	sp.addService("Svc", "http://new-url")

	if len(sp.services) != 1 {
		t.Fatalf("expected 1 service after overwrite, got %d", len(sp.services))
	}
	if sp.services["Svc"].url != "http://new-url" {
		t.Errorf("expected overwritten url, got %s", sp.services["Svc"].url)
	}
}

func TestPollOperational(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sp := newStatusPage()
	sp.addService("TestService", srv.URL)
	sp.poll(context.Background())

	svc := sp.services["TestService"]
	if svc.status != "operational" {
		t.Errorf("expected operational, got %s", svc.status)
	}
	if svc.lastCheck.IsZero() {
		t.Error("lastCheck not set after poll")
	}
	if svc.latency <= 0 {
		t.Error("latency should be positive after poll")
	}

	today := time.Now().UTC().Format("2006-01-02")
	if svc.dailyChecks[today] != 1 {
		t.Errorf("expected 1 daily check, got %d", svc.dailyChecks[today])
	}
	if svc.dailyFailures[today] != 0 {
		t.Errorf("expected 0 daily failures, got %d", svc.dailyFailures[today])
	}
}

func TestPollDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sp := newStatusPage()
	sp.addService("DownService", srv.URL)
	sp.poll(context.Background())

	svc := sp.services["DownService"]
	if svc.status != "down" {
		t.Errorf("expected down for 500, got %s", svc.status)
	}

	today := time.Now().UTC().Format("2006-01-02")
	if svc.dailyFailures[today] != 1 {
		t.Errorf("expected 1 failure, got %d", svc.dailyFailures[today])
	}
}

func TestPollDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	sp := newStatusPage()
	sp.addService("DegradedService", srv.URL)
	sp.poll(context.Background())

	svc := sp.services["DegradedService"]
	if svc.status != "degraded" {
		t.Errorf("expected degraded for 404, got %s", svc.status)
	}
}

func TestPollUnreachable(t *testing.T) {
	sp := newStatusPage()
	sp.addService("BadService", "http://127.0.0.1:1") // port 1 should be unreachable
	sp.poll(context.Background())

	svc := sp.services["BadService"]
	if svc.status != "down" {
		t.Errorf("expected down for unreachable host, got %s", svc.status)
	}

	today := time.Now().UTC().Format("2006-01-02")
	if svc.dailyFailures[today] != 1 {
		t.Errorf("expected 1 failure for unreachable, got %d", svc.dailyFailures[today])
	}
}

func TestHandleStatusHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sp := newStatusPage()
	sp.addService("Web", srv.URL)
	sp.poll(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	sp.handleStatus(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content-type, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("response should contain HTML doctype")
	}
	if !strings.Contains(body, "Gravix Status") {
		t.Error("response should contain page title")
	}
	if !strings.Contains(body, "Web") {
		t.Error("response should contain service name")
	}
}

func TestHandleStatusJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sp := newStatusPage()
	sp.addService("API", srv.URL)
	sp.poll(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	sp.handleStatus(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if _, ok := result["services"]; !ok {
		t.Error("JSON response missing services key")
	}
	if _, ok := result["updated_at"]; !ok {
		t.Error("JSON response missing updated_at key")
	}
}

func TestHandleStatusJSONQueryParam(t *testing.T) {
	sp := newStatusPage()

	req := httptest.NewRequest(http.MethodGet, "/?format=json", nil)
	w := httptest.NewRecorder()
	sp.handleStatus(w, req)

	ct := w.Result().Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json via query param, got %s", ct)
	}
}

func TestHandleStatusMethodNotAllowed(t *testing.T) {
	sp := newStatusPage()

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	sp.handleStatus(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestRenderStatusHTML(t *testing.T) {
	statuses := []ServiceStatus{
		{
			Name:      "Ingestion",
			Status:    "operational",
			UptimePct: 99.95,
			History: []DayStatus{
				{Date: "2026-03-28", UptimePct: 100.0, Checks: 48, Failures: 0},
				{Date: "2026-03-29", UptimePct: 99.5, Checks: 48, Failures: 1},
			},
		},
		{
			Name:      "Gateway",
			Status:    "down",
			UptimePct: 95.0,
			History: []DayStatus{
				{Date: "2026-03-28", UptimePct: 90.0, Checks: 48, Failures: 5},
			},
		},
	}

	html := renderStatusHTML(statuses)

	checks := []string{
		"<!DOCTYPE html>",
		"<html lang=\"en\">",
		"Gravix Status",
		"Ingestion",
		"Gateway",
		"99.95% uptime",
		"95.00% uptime",
		"Some Systems Experiencing Issues",
	}
	for _, want := range checks {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing expected content: %s", want)
		}
	}
}

func TestRenderStatusHTMLAllOperational(t *testing.T) {
	statuses := []ServiceStatus{
		{Name: "Svc", Status: "operational", UptimePct: 100.0},
	}
	html := renderStatusHTML(statuses)
	if !strings.Contains(html, "All Systems Operational") {
		t.Error("expected All Systems Operational banner")
	}
	if strings.Contains(html, "Some Systems Experiencing Issues") {
		t.Error("should not show degraded banner when all operational")
	}
}

func TestRenderStatusHTMLNoData(t *testing.T) {
	statuses := []ServiceStatus{
		{
			Name:   "Empty",
			Status: "unknown",
			History: []DayStatus{
				{Date: "2026-03-29", UptimePct: 100.0, Checks: 0, Failures: 0},
			},
		},
	}
	html := renderStatusHTML(statuses)
	if !strings.Contains(html, "no-data") {
		t.Error("expected no-data class for days with zero checks")
	}
}

func TestGetStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sp := newStatusPage()
	sp.addService("Alpha", srv.URL)
	sp.addService("Beta", srv.URL)
	sp.poll(context.Background())

	statuses := sp.getStatuses()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	found := make(map[string]bool)
	for _, s := range statuses {
		found[s.Name] = true
		if s.Status != "operational" {
			t.Errorf("expected operational for %s, got %s", s.Name, s.Status)
		}
		if s.UptimePct != 100.0 {
			t.Errorf("expected 100%% uptime for %s, got %.2f", s.Name, s.UptimePct)
		}
		if len(s.History) != 90 {
			t.Errorf("expected 90 days of history for %s, got %d", s.Name, len(s.History))
		}
		if s.URL != srv.URL {
			t.Errorf("expected URL %s, got %s", srv.URL, s.URL)
		}
	}
	if !found["Alpha"] || !found["Beta"] {
		t.Error("missing expected service names in statuses")
	}
}

func TestGetStatusesBeforePoll(t *testing.T) {
	sp := newStatusPage()
	sp.addService("NoPoll", "http://localhost:9999")

	statuses := sp.getStatuses()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "unknown" {
		t.Errorf("expected unknown before polling, got %s", statuses[0].Status)
	}
	if statuses[0].UptimePct != 100.0 {
		t.Errorf("expected 100%% default uptime, got %.2f", statuses[0].UptimePct)
	}
}

func TestGetStatusesEmpty(t *testing.T) {
	sp := newStatusPage()
	statuses := sp.getStatuses()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses for empty page, got %d", len(statuses))
	}
}

func TestPollMultipleServices(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failSrv.Close()

	sp := newStatusPage()
	sp.addService("Healthy", okSrv.URL)
	sp.addService("Unhealthy", failSrv.URL)
	sp.poll(context.Background())

	if sp.services["Healthy"].status != "operational" {
		t.Errorf("expected operational, got %s", sp.services["Healthy"].status)
	}
	if sp.services["Unhealthy"].status != "down" {
		t.Errorf("expected down, got %s", sp.services["Unhealthy"].status)
	}
}
