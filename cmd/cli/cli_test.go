package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ── resolveStr ────────────────────────────────────────────────────────────────

func TestResolveStr_FlagWins(t *testing.T) {
	got := resolveStr("from-flag", "fallback")
	if got != "from-flag" {
		t.Errorf("want %q, got %q", "from-flag", got)
	}
}

func TestResolveStr_FallbackWhenEmpty(t *testing.T) {
	got := resolveStr("", "fallback")
	if got != "fallback" {
		t.Errorf("want %q, got %q", "fallback", got)
	}
}

// ── propertyFlags ─────────────────────────────────────────────────────────────

func TestPropertyFlags_SetAndGet(t *testing.T) {
	var pf propertyFlags
	if err := pf.Set("key=value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pf.Set("env=prod"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pf.pairs["key"] != "value" {
		t.Errorf("want pairs[key]=%q, got %q", "value", pf.pairs["key"])
	}
	if pf.pairs["env"] != "prod" {
		t.Errorf("want pairs[env]=%q, got %q", "prod", pf.pairs["env"])
	}
}

func TestPropertyFlags_ValueWithEquals(t *testing.T) {
	var pf propertyFlags
	if err := pf.Set("url=https://example.com?a=1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pf.pairs["url"] != "https://example.com?a=1" {
		t.Errorf("want full URL value, got %q", pf.pairs["url"])
	}
}

func TestPropertyFlags_MissingEquals(t *testing.T) {
	var pf propertyFlags
	if err := pf.Set("no-equals"); err == nil {
		t.Error("expected error for missing '='")
	}
}

func TestPropertyFlags_String_Empty(t *testing.T) {
	var pf propertyFlags
	// String() should not panic on nil map.
	_ = pf.String()
}

// ── truncate ─────────────────────────────────────────────────────────────────

func TestTruncate_ShortString(t *testing.T) {
	got := truncate("hello", 10)
	if got != "hello" {
		t.Errorf("want %q, got %q", "hello", got)
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	got := truncate("hello", 5)
	if got != "hello" {
		t.Errorf("want %q, got %q", "hello", got)
	}
}

func TestTruncate_LongString(t *testing.T) {
	got := truncate("abcdefghij", 7)
	if got != "abcd..." {
		t.Errorf("want %q, got %q", "abcd...", got)
	}
}

// ── probe (status command) ────────────────────────────────────────────────────

func TestProbe_200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	client := &http.Client{}
	status, body := probe(client, srv.URL+"/live")
	if status != 200 {
		t.Errorf("want 200, got %d", status)
	}
	if body != "ok" {
		t.Errorf("want %q, got %q", "ok", body)
	}
}

func TestProbe_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := &http.Client{}
	status, _ := probe(client, srv.URL+"/ready")
	if status != 503 {
		t.Errorf("want 503, got %d", status)
	}
}

func TestProbe_Unreachable(t *testing.T) {
	client := &http.Client{}
	status, body := probe(client, "http://127.0.0.1:19998/live")
	if status != 0 {
		t.Errorf("want 0 for unreachable, got %d", status)
	}
	if !strings.Contains(body, "error") {
		t.Errorf("want error string in body, got %q", body)
	}
}

// ── fetchDLQEntries ───────────────────────────────────────────────────────────

func TestFetchDLQEntries_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		payload := dlqEntryResponse{Total: 1}
		payload.Entries = append(payload.Entries, struct {
			Timestamp string          `json:"timestamp"`
			TenantID  string          `json:"tenant_id"`
			FactType  string          `json:"fact_type"`
			Error     string          `json:"error"`
			RawJSON   json.RawMessage `json:"raw_json"`
		}{
			Timestamp: "2026-05-22T10:00:00Z",
			TenantID:  "tenant1",
			FactType:  "request_fact",
			Error:     "invalid path",
			RawJSON:   json.RawMessage(`{}`),
		})
		json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	result, err := fetchDLQEntries(srv.URL, "test-key", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("want Total=1, got %d", result.Total)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].Error != "invalid path" {
		t.Errorf("want error=%q, got %q", "invalid path", result.Entries[0].Error)
	}
}

func TestFetchDLQEntries_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchDLQEntries(srv.URL, "key", 10)
	if err == nil {
		t.Error("expected error on non-200 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestFetchDLQEntries_Unreachable(t *testing.T) {
	_, err := fetchDLQEntries("http://127.0.0.1:19998", "key", 10)
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}

func TestFetchDLQEntries_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	_, err := fetchDLQEntries(srv.URL, "key", 10)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── getEndpoint / getAPIKey ───────────────────────────────────────────────────

func TestGetEndpoint_Default(t *testing.T) {
	t.Setenv("GRAVIX_ENDPOINT", "")
	if got := getEndpoint(); got != defaultEndpoint {
		t.Errorf("want %q, got %q", defaultEndpoint, got)
	}
}

func TestGetEndpoint_FromEnv(t *testing.T) {
	t.Setenv("GRAVIX_ENDPOINT", "https://custom.example.com")
	if got := getEndpoint(); got != "https://custom.example.com" {
		t.Errorf("want custom endpoint, got %q", got)
	}
}

func TestGetAPIKey_Empty(t *testing.T) {
	t.Setenv("GRAVIX_API_KEY", "")
	if got := getAPIKey(); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestGetAPIKey_FromEnv(t *testing.T) {
	t.Setenv("GRAVIX_API_KEY", "secret-key")
	if got := getAPIKey(); got != "secret-key" {
		t.Errorf("want %q, got %q", "secret-key", got)
	}
}

// ── binary smoke tests ────────────────────────────────────────────────────────
// These build and invoke the binary to cover main() paths without test harness tricks.

func TestBinary_Version(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if !strings.Contains(string(out), "gravix v") {
		t.Errorf("unexpected output: %q", string(out))
	}
}

func TestBinary_Help(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "help")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Usage:") {
		t.Errorf("help output missing Usage, got: %q", string(out))
	}
}

func TestBinary_UnknownCommand(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "bogus")
	cmd.Run()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Error("expected non-zero exit for unknown command")
	}
}

func TestBinary_SendFactMissingKey(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "send", "fact",
		"--service=svc", "--method=GET", "--path=/", "--status=200", "--latency=10")
	cmd.Env = append(os.Environ(), "GRAVIX_API_KEY=")
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Error("expected non-zero exit when GRAVIX_API_KEY is unset")
	}
	if !strings.Contains(string(out), "GRAVIX_API_KEY") {
		t.Errorf("expected error mentioning GRAVIX_API_KEY, got: %q", string(out))
	}
}

func TestBinary_StatusUnreachable(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "status", "--endpoint=http://127.0.0.1:19997")
	cmd.Run()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Error("expected non-zero exit when service is unreachable")
	}
}

// buildBinary compiles the CLI and returns the path to the binary.
// Results are cached per test run via t.TempDir.
func buildBinary(t *testing.T) string {
	t.Helper()
	out := t.TempDir() + "/gravix"
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "." // cmd/cli
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, b)
	}
	return out
}
