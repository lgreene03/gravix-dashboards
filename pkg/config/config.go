// Package config provides typed, centralized configuration loaded from
// environment variables. All services read config through this package
// instead of calling os.Getenv directly.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all Gravix configuration, loaded from environment variables.
type Config struct {
	// Gateway
	Gateway GatewayConfig

	// Ingestion
	Ingestion IngestionConfig

	// Database
	DB DBConfig

	// Storage (S3/MinIO)
	Storage StorageConfig

	// Stripe Billing
	Stripe StripeConfig

	// Email
	Email EmailConfig

	// Observability
	Observability ObservabilityConfig
}

// GatewayConfig holds gateway service configuration.
type GatewayConfig struct {
	Addr               string        // GATEWAY_ADDR — listen address (default ":8091")
	JWTSecret          string        // JWT_SECRET — signing key for JWTs
	TOTPEncryptionKey  string        // TOTP_ENCRYPTION_KEY — encryption key for TOTP secrets
	BaseURL            string        // BASE_URL — for email links (default "http://localhost:8000")
	CORSAllowedOrigins []string      // CORS_ALLOWED_ORIGINS — comma-separated origins
	CubeAPIURL         string        // CUBE_API_URL — Cube.js API endpoint
	MaxBodyBytes       int64         // GATEWAY_MAX_BODY_BYTES — max request body size (default 1MB)
	CaptchaSecretKey   string        // CAPTCHA_SECRET_KEY — reCAPTCHA secret
	TokenExpiry        time.Duration // TOKEN_EXPIRY — JWT token lifetime (default 24h)
}

// IngestionConfig holds ingestion service configuration.
type IngestionConfig struct {
	Addr         string // INGESTION_ADDR — listen address (default ":8090")
	APIKey       string // API_KEY — shared ingestion API key
	IngestionURL string // INGESTION_URL — URL for gateway to reach ingestion
	BufferDir    string // BUFFER_DIR — local buffer directory (default "./data/raw")
}

// DBConfig holds database configuration.
type DBConfig struct {
	Driver      string // DB_DRIVER — "sqlite" or "postgres" (default "sqlite")
	SQLitePath  string // TENANT_DB_PATH — SQLite file path (default "./data/gravix.db")
	PostgresURL string // DATABASE_URL — PostgreSQL connection string
}

// StorageConfig holds S3/MinIO storage configuration.
type StorageConfig struct {
	Endpoint  string // S3_ENDPOINT
	Region    string // S3_REGION (default "us-east-1")
	Bucket    string // S3_BUCKET (default "gravix")
	AccessKey string // S3_ACCESS_KEY
	SecretKey string // S3_SECRET_KEY
}

// StripeConfig holds Stripe billing configuration.
type StripeConfig struct {
	SecretKey     string // STRIPE_SECRET_KEY
	WebhookSecret string // STRIPE_WEBHOOK_SECRET

	// Monthly price IDs
	PriceFree       string // STRIPE_PRICE_FREE
	PriceTeam       string // STRIPE_PRICE_TEAM
	PriceBusiness   string // STRIPE_PRICE_BUSINESS
	PriceScale      string // STRIPE_PRICE_SCALE
	PriceEnterprise string // STRIPE_PRICE_ENTERPRISE

	// Annual price IDs
	PriceTeamAnnual       string // STRIPE_PRICE_TEAM_ANNUAL
	PriceBusinessAnnual   string // STRIPE_PRICE_BUSINESS_ANNUAL
	PriceScaleAnnual      string // STRIPE_PRICE_SCALE_ANNUAL
	PriceEnterpriseAnnual string // STRIPE_PRICE_ENTERPRISE_ANNUAL
}

// Enabled returns true if Stripe billing is configured.
func (s StripeConfig) Enabled() bool {
	return s.SecretKey != ""
}

// EmailConfig holds email sending configuration.
type EmailConfig struct {
	Provider    string // EMAIL_PROVIDER — "smtp" or "noop" (default "noop")
	SMTPHost    string // SMTP_HOST
	SMTPPort    int    // SMTP_PORT (default 587)
	SMTPUser    string // SMTP_USER
	SMTPPass    string // SMTP_PASS
	FromAddress string // EMAIL_FROM_ADDRESS (default "noreply@gravix.io")
	FromName    string // EMAIL_FROM_NAME (default "Gravix")
}

// ObservabilityConfig holds observability settings.
type ObservabilityConfig struct {
	LogLevel        string  // LOG_LEVEL — "debug", "info", "warn", "error" (default "info")
	TraceSampleRate float64 // TRACE_SAMPLE_RATE — 0.0 to 1.0 (default 0.01)
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Gateway: GatewayConfig{
			Addr:               envOr("GATEWAY_ADDR", ":8091"),
			JWTSecret:          os.Getenv("JWT_SECRET"),
			TOTPEncryptionKey:  os.Getenv("TOTP_ENCRYPTION_KEY"),
			BaseURL:            envOr("BASE_URL", "http://localhost:8000"),
			CORSAllowedOrigins: envSplit("CORS_ALLOWED_ORIGINS", ","),
			CubeAPIURL:         envOr("CUBE_API_URL", "http://localhost:4000/cubejs-api/v1/load"),
			MaxBodyBytes:       envInt64("GATEWAY_MAX_BODY_BYTES", 1<<20),
			CaptchaSecretKey:   os.Getenv("CAPTCHA_SECRET_KEY"),
			TokenExpiry:        envDuration("TOKEN_EXPIRY", 24*time.Hour),
		},
		Ingestion: IngestionConfig{
			Addr:         envOr("INGESTION_ADDR", ":8090"),
			APIKey:       os.Getenv("API_KEY"),
			IngestionURL: envOr("INGESTION_URL", "http://localhost:8090"),
			BufferDir:    envOr("BUFFER_DIR", "./data/raw"),
		},
		DB: DBConfig{
			Driver:      envOr("DB_DRIVER", "sqlite"),
			SQLitePath:  envOr("TENANT_DB_PATH", "./data/gravix.db"),
			PostgresURL: os.Getenv("DATABASE_URL"),
		},
		Storage: StorageConfig{
			Endpoint:  os.Getenv("S3_ENDPOINT"),
			Region:    envOr("S3_REGION", "us-east-1"),
			Bucket:    envOr("S3_BUCKET", "gravix"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"),
			SecretKey: os.Getenv("S3_SECRET_KEY"),
		},
		Stripe: StripeConfig{
			SecretKey:             os.Getenv("STRIPE_SECRET_KEY"),
			WebhookSecret:        os.Getenv("STRIPE_WEBHOOK_SECRET"),
			PriceFree:            os.Getenv("STRIPE_PRICE_FREE"),
			PriceTeam:            os.Getenv("STRIPE_PRICE_TEAM"),
			PriceBusiness:        os.Getenv("STRIPE_PRICE_BUSINESS"),
			PriceScale:           os.Getenv("STRIPE_PRICE_SCALE"),
			PriceEnterprise:      os.Getenv("STRIPE_PRICE_ENTERPRISE"),
			PriceTeamAnnual:      os.Getenv("STRIPE_PRICE_TEAM_ANNUAL"),
			PriceBusinessAnnual:  os.Getenv("STRIPE_PRICE_BUSINESS_ANNUAL"),
			PriceScaleAnnual:     os.Getenv("STRIPE_PRICE_SCALE_ANNUAL"),
			PriceEnterpriseAnnual: os.Getenv("STRIPE_PRICE_ENTERPRISE_ANNUAL"),
		},
		Email: EmailConfig{
			Provider:    envOr("EMAIL_PROVIDER", "noop"),
			SMTPHost:    os.Getenv("SMTP_HOST"),
			SMTPPort:    int(envInt64("SMTP_PORT", 587)),
			SMTPUser:    os.Getenv("SMTP_USER"),
			SMTPPass:    os.Getenv("SMTP_PASS"),
			FromAddress: envOr("EMAIL_FROM_ADDRESS", "noreply@gravix.io"),
			FromName:    envOr("EMAIL_FROM_NAME", "Gravix"),
		},
		Observability: ObservabilityConfig{
			LogLevel:        envOr("LOG_LEVEL", "info"),
			TraceSampleRate: envFloat64("TRACE_SAMPLE_RATE", 0.01),
		},
	}
}

// Validate checks that required configuration is present.
// Returns an error listing all missing required fields.
func (c *Config) Validate() error {
	var missing []string

	if c.Gateway.JWTSecret == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if len(c.Gateway.JWTSecret) < 32 {
		missing = append(missing, "JWT_SECRET (must be >= 32 characters)")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateIngestion checks ingestion-specific required fields.
func (c *Config) ValidateIngestion() error {
	var missing []string
	if c.Ingestion.APIKey == "" {
		missing = append(missing, "API_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

// --- helpers ---

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envSplit(key, sep string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func envInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat64(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
