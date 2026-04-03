package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear any env vars that might be set
	for _, k := range []string{"GATEWAY_ADDR", "DB_DRIVER", "S3_REGION", "S3_BUCKET", "LOG_LEVEL"} {
		os.Unsetenv(k)
	}

	cfg := Load()

	if cfg.Gateway.Addr != ":8091" {
		t.Errorf("Gateway.Addr = %q, want %q", cfg.Gateway.Addr, ":8091")
	}
	if cfg.DB.Driver != "sqlite" {
		t.Errorf("DB.Driver = %q, want %q", cfg.DB.Driver, "sqlite")
	}
	if cfg.Storage.Region != "us-east-1" {
		t.Errorf("Storage.Region = %q, want %q", cfg.Storage.Region, "us-east-1")
	}
	if cfg.Storage.Bucket != "gravix" {
		t.Errorf("Storage.Bucket = %q, want %q", cfg.Storage.Bucket, "gravix")
	}
	if cfg.Email.SMTPPort != 587 {
		t.Errorf("Email.SMTPPort = %d, want %d", cfg.Email.SMTPPort, 587)
	}
	if cfg.Observability.LogLevel != "info" {
		t.Errorf("Observability.LogLevel = %q, want %q", cfg.Observability.LogLevel, "info")
	}
	if cfg.Observability.TraceSampleRate != 0.01 {
		t.Errorf("TraceSampleRate = %f, want %f", cfg.Observability.TraceSampleRate, 0.01)
	}
	if cfg.Gateway.MaxBodyBytes != 1<<20 {
		t.Errorf("MaxBodyBytes = %d, want %d", cfg.Gateway.MaxBodyBytes, int64(1<<20))
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("GATEWAY_ADDR", ":9999")
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-characters-long")
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.gravix.io, https://staging.gravix.io")
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_123")
	t.Setenv("TRACE_SAMPLE_RATE", "0.5")
	t.Setenv("GATEWAY_MAX_BODY_BYTES", "2097152")

	cfg := Load()

	if cfg.Gateway.Addr != ":9999" {
		t.Errorf("Gateway.Addr = %q, want %q", cfg.Gateway.Addr, ":9999")
	}
	if cfg.Gateway.JWTSecret != "test-secret-that-is-at-least-32-characters-long" {
		t.Error("JWTSecret not set from env")
	}
	if cfg.DB.Driver != "postgres" {
		t.Errorf("DB.Driver = %q, want %q", cfg.DB.Driver, "postgres")
	}
	if cfg.DB.PostgresURL != "postgres://localhost/test" {
		t.Error("PostgresURL not set")
	}
	if len(cfg.Gateway.CORSAllowedOrigins) != 2 {
		t.Errorf("CORSAllowedOrigins length = %d, want 2", len(cfg.Gateway.CORSAllowedOrigins))
	} else {
		if cfg.Gateway.CORSAllowedOrigins[0] != "https://app.gravix.io" {
			t.Errorf("CORSAllowedOrigins[0] = %q", cfg.Gateway.CORSAllowedOrigins[0])
		}
		if cfg.Gateway.CORSAllowedOrigins[1] != "https://staging.gravix.io" {
			t.Errorf("CORSAllowedOrigins[1] = %q", cfg.Gateway.CORSAllowedOrigins[1])
		}
	}
	if !cfg.Stripe.Enabled() {
		t.Error("Stripe should be enabled when secret key is set")
	}
	if cfg.Observability.TraceSampleRate != 0.5 {
		t.Errorf("TraceSampleRate = %f, want 0.5", cfg.Observability.TraceSampleRate)
	}
	if cfg.Gateway.MaxBodyBytes != 2097152 {
		t.Errorf("MaxBodyBytes = %d, want 2097152", cfg.Gateway.MaxBodyBytes)
	}
}

func TestStripeEnabledDisabled(t *testing.T) {
	cfg := StripeConfig{}
	if cfg.Enabled() {
		t.Error("empty StripeConfig should not be enabled")
	}

	cfg.SecretKey = "sk_test_123"
	if !cfg.Enabled() {
		t.Error("StripeConfig with secret key should be enabled")
	}
}

func TestValidate(t *testing.T) {
	cfg := Load()
	cfg.Gateway.JWTSecret = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing JWT_SECRET")
	}

	cfg.Gateway.JWTSecret = "short"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for short JWT_SECRET")
	}

	cfg.Gateway.JWTSecret = "this-is-a-jwt-secret-that-is-at-least-32-chars"
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateIngestion(t *testing.T) {
	cfg := Load()
	cfg.Ingestion.APIKey = ""
	if err := cfg.ValidateIngestion(); err == nil {
		t.Error("expected error for missing API_KEY")
	}

	cfg.Ingestion.APIKey = "test-key"
	if err := cfg.ValidateIngestion(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnvSplitEmpty(t *testing.T) {
	os.Unsetenv("TEST_SPLIT_VAR")
	result := envSplit("TEST_SPLIT_VAR", ",")
	if result != nil {
		t.Errorf("expected nil for unset env var, got %v", result)
	}
}

func TestEnvDurationInvalid(t *testing.T) {
	t.Setenv("TEST_DURATION", "not-a-duration")
	d := envDuration("TEST_DURATION", 5*60*1000000000) // 5m as nanoseconds
	if d != 5*60*1000000000 {
		t.Errorf("expected fallback duration for invalid value")
	}
}

func TestEnvFloat64Invalid(t *testing.T) {
	t.Setenv("TEST_FLOAT", "not-a-float")
	f := envFloat64("TEST_FLOAT", 0.42)
	if f != 0.42 {
		t.Errorf("expected fallback 0.42, got %f", f)
	}
}
