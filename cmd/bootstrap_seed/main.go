// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command bootstrap_seed idempotently provisions the single local tenant, API
// key and login needed for zero-config self-hosted boot of
// docker-compose.bootstrap.yml.
//
// It exists because ingestion and gateway both run in multi-tenant auth mode in
// the bootstrap stack — TENANT_DB_PATH takes precedence over API_KEY — so
// without a tenant and a key in that database every request is rejected and the
// dashboard charts stay empty forever. Nothing about that is discoverable from
// a blank screen, which is the whole problem this spec addresses.
//
// Usage:
//
//	go run ./cmd/bootstrap_seed/ \
//	  -db ./data/gravix.db \
//	  -api-key-file ./data/api_key.txt \
//	  -dashboard-config ./data/dashboard_config.js \
//	  -login-file ./data/login.txt
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ErrAlreadyProvisioned reports that the API key file exists, so a previous run
// already did the work. It is a success, not a failure: the container runs on
// every `docker compose up` and must not mint a second key each time.
var ErrAlreadyProvisioned = errors.New("bootstrap_seed: api key file already exists, nothing to do")

type provisionConfig struct {
	DBPath          string
	APIKeyFile      string
	DashboardConfig string
	TenantName      string
	TenantEmail     string
	IngestionURL    string
	GatewayURL      string
	LoginFile       string
}

func main() {
	var cfg provisionConfig
	flag.StringVar(&cfg.DBPath, "db", "./data/gravix.db", "Path to the tenant SQLite database")
	flag.StringVar(&cfg.APIKeyFile, "api-key-file", "./data/api_key.txt", "Path to write the plaintext API key (mode 0600)")
	flag.StringVar(&cfg.DashboardConfig, "dashboard-config", "./data/dashboard_config.js", "Path to write the dashboard's window.GRAVIX_CONFIG file")
	flag.StringVar(&cfg.TenantName, "tenant-name", "local", "Name of the single bootstrap tenant")
	flag.StringVar(&cfg.TenantEmail, "tenant-email", "local@gravix.invalid", "Email of the single bootstrap tenant")
	flag.StringVar(&cfg.IngestionURL, "ingestion-url", "http://localhost:8090", "Value written into dashboard_config.js as ingestionApiUrl")
	flag.StringVar(&cfg.GatewayURL, "gateway-url", "http://localhost:8091", "Value written into dashboard_config.js as gatewayUrl")
	flag.StringVar(&cfg.LoginFile, "login-file", "./data/login.txt", "Path to write the generated dashboard login (mode 0600)")
	flag.Parse()

	if err := provision(context.Background(), cfg, os.Stdout); err != nil {
		if errors.Is(err, ErrAlreadyProvisioned) {
			// Already done is the common case, not an error: this runs on every
			// boot and the stack's other services wait on it exiting 0.
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// provision performs the idempotent seed. It is the unit under test; main()
// parses flags and calls it.
func provision(ctx context.Context, cfg provisionConfig, stdout io.Writer) error {
	// The API key file is the marker for "already provisioned" rather than the
	// database, because the database exists the moment anything opens it and
	// would make every subsequent run a no-op before a key was ever written.
	if _, err := os.Stat(cfg.APIKeyFile); err == nil {
		fmt.Fprintf(stdout, "bootstrap already provisioned (api key file exists at %s)\n", cfg.APIKeyFile)
		return ErrAlreadyProvisioned
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("bootstrap_seed: %w", err)
	}

	for _, path := range []string{cfg.DBPath, cfg.APIKeyFile, cfg.DashboardConfig, cfg.LoginFile} {
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("bootstrap_seed: %w", err)
			}
		}
	}

	db, err := tenantdb.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("bootstrap_seed: open tenant database: %w", err)
	}
	defer db.Close()

	// A missing tenant reports an error rather than a nil tenant, and the two
	// are not worth distinguishing here: anything other than a usable tenant
	// means create one, and a real database failure surfaces on the next call.
	tenant, _ := db.Tenants().GetByEmail(ctx, cfg.TenantEmail)
	if tenant == nil {
		tenant = &tenantdb.Tenant{
			Name:   cfg.TenantName,
			Email:  cfg.TenantEmail,
			Plan:   "free",
			Status: "active",
		}
		if err := db.Tenants().Create(ctx, tenant); err != nil {
			return fmt.Errorf("bootstrap_seed: create tenant: %w", err)
		}
	}

	plainKey, _, err := db.APIKeys().Create(ctx, tenant.ID, "bootstrap", nil)
	if err != nil {
		return fmt.Errorf("bootstrap_seed: create api key: %w", err)
	}

	// 0600: this is a live credential, and the synthetic-traffic container
	// reads it as the same user rather than through a widened mode.
	if err := os.WriteFile(cfg.APIKeyFile, []byte(plainKey), 0o600); err != nil {
		return fmt.Errorf("bootstrap_seed: %w", err)
	}

	// The dashboard sends Cube a JWT, not this API key, and the only thing that
	// mints one is a gateway login against a user with a bcrypt password. A
	// tenant with no user therefore boots a stack whose charts can never load,
	// with every service reporting healthy — see F-016. The user is created
	// here so the zero-config path stays authenticated rather than reaching the
	// dashboard by switching authentication off.
	password, err := generatePassword()
	if err != nil {
		return fmt.Errorf("bootstrap_seed: %w", err)
	}
	//
	// The recovery path matters as much as the first boot: losing api_key.txt
	// while keeping the database re-runs this function against a tenant, and a
	// user, that already exist. Creating blindly fails the UNIQUE constraint on
	// users.email and leaves the stack unprovisioned.
	if existing, err := db.Users().GetByEmail(ctx, cfg.TenantEmail); err == nil && existing != nil {
		// UpdatePassword stores what it is given verbatim — unlike Create,
		// which bcrypts anything that is not already a hash. Hashing here is
		// not belt and braces; skipping it writes the password in plaintext.
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("bootstrap_seed: hash password: %w", err)
		}
		if err := db.Users().UpdatePassword(ctx, existing.ID, string(hash)); err != nil {
			return fmt.Errorf("bootstrap_seed: reset user password: %w", err)
		}
	} else {
		user := &tenantdb.User{
			TenantID: tenant.ID,
			Email:    cfg.TenantEmail,
			// Plaintext here: Create bcrypts anything that is not already a
			// bcrypt hash, so passing a hash would store it double-hashed.
			PasswordHash:  password,
			Role:          "admin",
			EmailVerified: true,
			Status:        "active",
		}
		if err := db.Users().Create(ctx, user); err != nil {
			return fmt.Errorf("bootstrap_seed: create user: %w", err)
		}
	}

	// 0600, like the API key: this is the credential to the dashboard.
	login := fmt.Sprintf("email: %s\npassword: %s\n", cfg.TenantEmail, password)
	if err := os.WriteFile(cfg.LoginFile, []byte(login), 0o600); err != nil {
		return fmt.Errorf("bootstrap_seed: %w", err)
	}

	config, err := dashboardConfig(cfg.IngestionURL, cfg.GatewayURL, plainKey)
	if err != nil {
		return fmt.Errorf("bootstrap_seed: %w", err)
	}
	if err := os.WriteFile(cfg.DashboardConfig, []byte(config), 0o644); err != nil {
		return fmt.Errorf("bootstrap_seed: %w", err)
	}

	fmt.Fprintf(stdout, "provisioned tenant %q (id=%s)\n", tenant.Name, tenant.ID)
	fmt.Fprintf(stdout, "api key written to %s\n", cfg.APIKeyFile)
	// Printed as well as written, because this is the one moment the password
	// exists in plaintext anywhere other than that file, and the log is where a
	// first-time user is already looking.
	fmt.Fprintf(stdout, "dashboard login written to %s\n", cfg.LoginFile)
	fmt.Fprintf(stdout, "  email:    %s\n", cfg.TenantEmail)
	fmt.Fprintf(stdout, "  password: %s\n", password)
	return nil
}

// generatePassword returns a 256-bit random password, URL-safe so it survives
// being copied out of a terminal or a compose log without escaping.
func generatePassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// dashboardConfig renders the script the dashboard loads before app.js, whose
// Object.assign merge lets these fields override the built-in defaults.
//
// Values go through encoding/json rather than %q: a key or URL containing a
// quote would otherwise produce a file that does not parse, and a dashboard
// that fails to load is harder to diagnose than one that shows no data.
func dashboardConfig(ingestionURL, gatewayURL, apiKey string) (string, error) {
	fields := make([]string, 0, 3)
	for _, f := range []struct{ name, value string }{
		{"ingestionApiUrl", ingestionURL},
		{"gatewayUrl", gatewayURL},
		{"apiKey", apiKey},
	} {
		encoded, err := json.Marshal(f.value)
		if err != nil {
			return "", fmt.Errorf("encode %s: %w", f.name, err)
		}
		fields = append(fields, fmt.Sprintf("  %s: %s", f.name, encoded))
	}
	return "window.GRAVIX_CONFIG = {\n" + fields[0] + ",\n" + fields[1] + ",\n" + fields[2] + "\n};\n", nil
}
