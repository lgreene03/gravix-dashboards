package main

import (
	"os"
	"testing"
)

// ─── getEndpoint tests ───

func TestGetEndpointDefault(t *testing.T) {
	os.Unsetenv("GRAVIX_ENDPOINT")
	got := getEndpoint()
	if got != defaultEndpoint {
		t.Errorf("getEndpoint() = %q, want %q", got, defaultEndpoint)
	}
}

func TestGetEndpointFromEnv(t *testing.T) {
	t.Setenv("GRAVIX_ENDPOINT", "http://custom:9999")
	got := getEndpoint()
	if got != "http://custom:9999" {
		t.Errorf("getEndpoint() = %q, want http://custom:9999", got)
	}
}

// ─── getAPIKey tests ───

func TestGetAPIKeyEmpty(t *testing.T) {
	os.Unsetenv("GRAVIX_API_KEY")
	got := getAPIKey()
	if got != "" {
		t.Errorf("getAPIKey() = %q, want empty string", got)
	}
}

func TestGetAPIKeyFromEnv(t *testing.T) {
	t.Setenv("GRAVIX_API_KEY", "test-key-abc")
	got := getAPIKey()
	if got != "test-key-abc" {
		t.Errorf("getAPIKey() = %q, want test-key-abc", got)
	}
}
