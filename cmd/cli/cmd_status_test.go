package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ─── probe tests ───

func TestProbeSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	}))
	defer ts.Close()

	client := ts.Client()
	status, body := probe(client, ts.URL+"/live")
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if body != "up" {
		t.Errorf("body = %q, want up", body)
	}
}

func TestProbeServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready"))
	}))
	defer ts.Close()

	client := ts.Client()
	status, body := probe(client, ts.URL+"/ready")
	if status != 503 {
		t.Errorf("status = %d, want 503", status)
	}
	if body != "not ready" {
		t.Errorf("body = %q, want 'not ready'", body)
	}
}

func TestProbeUnreachable(t *testing.T) {
	client := &http.Client{}
	status, body := probe(client, "http://127.0.0.1:1/unreachable")
	if status != 0 {
		t.Errorf("status = %d, want 0 for unreachable", status)
	}
	if body == "" {
		t.Error("expected non-empty error body for unreachable host")
	}
}

func TestProbeBodyTrimming(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("  healthy  \n"))
	}))
	defer ts.Close()

	client := ts.Client()
	_, body := probe(client, ts.URL+"/live")
	if body != "healthy" {
		t.Errorf("body = %q, want trimmed 'healthy'", body)
	}
}
