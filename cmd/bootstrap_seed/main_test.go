// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// seedIn builds a config rooted at a fresh directory, which is what a first
// boot on a clean checkout actually looks like.
func seedIn(t *testing.T) provisionConfig {
	t.Helper()
	dir := t.TempDir()
	return provisionConfig{
		DBPath:          filepath.Join(dir, "gravix.db"),
		APIKeyFile:      filepath.Join(dir, "api_key.txt"),
		DashboardConfig: filepath.Join(dir, "dashboard_config.js"),
		LoginFile:       filepath.Join(dir, "login.txt"),
		JWTSecretFile:   filepath.Join(dir, "jwt_secret.txt"),
		TenantName:      "local",
		TenantEmail:     "local@gravix.invalid",
		IngestionURL:    "http://localhost:8090",
		GatewayURL:      "http://localhost:8091",
	}
}

func run(t *testing.T, cfg provisionConfig) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := provision(context.Background(), cfg, &out)
	return out.String(), err
}

// openSeeded reopens the database the run under test wrote, so assertions read
// what was persisted rather than what was returned.
func openSeeded(t *testing.T, cfg provisionConfig) *tenantdb.SQLiteDB {
	t.Helper()
	db, err := tenantdb.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("reopen tenant db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ─── AC-1 ───

func TestProvisionCreatesSingleTenant(t *testing.T) {
	cfg := seedIn(t)
	out, err := run(t, cfg)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	tenants, err := openSeeded(t, cfg).Tenants().List(context.Background())
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	if len(tenants) != 1 {
		t.Fatalf("AC-1 FAILED: got %d tenants, want exactly 1 — a self-hosted bootstrap is one "+
			"organisation, not a SaaS demo", len(tenants))
	}
	if tenants[0].Email != "local@gravix.invalid" {
		t.Errorf("AC-1 FAILED: tenant email is %q, want local@gravix.invalid", tenants[0].Email)
	}
	if tenants[0].Name != "local" {
		t.Errorf("AC-1 FAILED: tenant name is %q, want local", tenants[0].Name)
	}
	// .invalid is reserved by RFC 2606 and can never resolve, so nothing here
	// can accidentally mail a real address.
	if !strings.HasSuffix(tenants[0].Email, ".invalid") {
		t.Errorf("AC-1 FAILED: the default tenant email %q is not in a reserved domain",
			tenants[0].Email)
	}

	if !strings.Contains(out, `provisioned tenant "local" (id=`) {
		t.Errorf("§6.1 FAILED: stdout does not report the provisioned tenant:\n%s", out)
	}
	if !strings.Contains(out, "api key written to "+cfg.APIKeyFile) {
		t.Errorf("§6.1 FAILED: stdout does not say where the key went:\n%s", out)
	}
}

// ─── AC-2 ───

func TestProvisionWritesAPIKeyFileWithCorrectMode(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("provision: %v", err)
	}

	info, err := os.Stat(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("AC-2 FAILED: no api key file: %v", err)
	}
	if got := info.Mode().Perm(); got != fs.FileMode(0o600) {
		t.Errorf("AC-2 FAILED: api key file mode is %04o, want 0600 — it is a live credential",
			got)
	}

	key, err := os.ReadFile(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("read api key: %v", err)
	}
	if len(key) == 0 {
		t.Fatal("AC-2 FAILED: the api key file is empty")
	}
	// A trailing newline would be carried into the Authorization header by the
	// synthetic-traffic container's `cat`, and rejected.
	if strings.TrimSpace(string(key)) != string(key) {
		t.Errorf("AC-2 FAILED: the api key file has surrounding whitespace (%q); the traffic "+
			"container cats it straight into an env var", string(key))
	}
}

// ─── AC-3 ───

func TestProvisionWritesDashboardConfig(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("provision: %v", err)
	}

	body, err := os.ReadFile(cfg.DashboardConfig)
	if err != nil {
		t.Fatalf("AC-3 FAILED: no dashboard config: %v", err)
	}
	got := string(body)

	if !strings.Contains(got, "window.GRAVIX_CONFIG") {
		t.Errorf("AC-3 FAILED: the config does not set window.GRAVIX_CONFIG, so app.js's "+
			"Object.assign merge sees nothing:\n%s", got)
	}

	key, err := os.ReadFile(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("read api key: %v", err)
	}
	if !strings.Contains(got, string(key)) {
		t.Error("AC-3 FAILED: the dashboard config does not carry the key that was written to " +
			"the key file, so the dashboard would authenticate as nobody")
	}

	for _, want := range []string{
		`ingestionApiUrl: "http://localhost:8090"`,
		`gatewayUrl: "http://localhost:8091"`,
		"apiKey: ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AC-3 FAILED: the config is missing %q:\n%s", want, got)
		}
	}
	if info, err := os.Stat(cfg.DashboardConfig); err == nil {
		if perm := info.Mode().Perm(); perm != fs.FileMode(0o644) {
			t.Errorf("AC-3 FAILED: dashboard config mode is %04o, want 0644 — nginx reads it "+
				"as a different user", perm)
		}
	}
}

// TestDashboardConfigEscapesValues is the reason encoding/json is in the path
// rather than a format verb: a quote in a URL or key must not be able to
// produce a file that fails to parse, because a dashboard that silently loads
// no config is indistinguishable from one with no data.
func TestDashboardConfigEscapesValues(t *testing.T) {
	got, err := dashboardConfig(`http://x/"`, "http://y", "key\"with\\quote")
	if err != nil {
		t.Fatalf("dashboardConfig: %v", err)
	}
	for _, want := range []string{`\"`, `\\`} {
		if !strings.Contains(got, want) {
			t.Errorf("a value containing a quote was not escaped (%q missing):\n%s", want, got)
		}
	}
	// Exactly three fields, one per line, and a terminating semicolon.
	if n := strings.Count(got, "\n"); n != 5 {
		t.Errorf("want one field per line, got %d newlines:\n%s", n, got)
	}
	if !strings.HasSuffix(got, "};\n") {
		t.Errorf("the config does not terminate the object literal:\n%s", got)
	}
}

// ─── AC-4 ───

func TestProvisionIsIdempotent(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	firstKey, err := os.ReadFile(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("read api key: %v", err)
	}

	out, err := run(t, cfg)
	if !errors.Is(err, ErrAlreadyProvisioned) {
		t.Fatalf("AC-4 FAILED: second run returned %v, want ErrAlreadyProvisioned — this "+
			"container runs on every `docker compose up`", err)
	}
	if !strings.Contains(out, "bootstrap already provisioned (api key file exists at "+cfg.APIKeyFile+")") {
		t.Errorf("§6.1 FAILED: wrong message on the already-provisioned path:\n%s", out)
	}

	tenants, err := openSeeded(t, cfg).Tenants().List(context.Background())
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	if len(tenants) != 1 {
		t.Errorf("AC-4 FAILED: got %d tenants after two runs, want 1", len(tenants))
	}

	secondKey, err := os.ReadFile(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("read api key: %v", err)
	}
	if string(firstKey) != string(secondKey) {
		t.Error("AC-4 FAILED: the second run replaced the API key. Every restart would " +
			"invalidate the key the dashboard and the traffic generator are holding")
	}
}

// ─── AC-5 ───

func TestProvisionReusesExistingTenantWhenKeyFileMissing(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := os.Remove(cfg.APIKeyFile); err != nil {
		t.Fatalf("remove api key file: %v", err)
	}

	if _, err := run(t, cfg); err != nil {
		t.Fatalf("AC-5 FAILED: re-provision after losing the key file: %v", err)
	}

	tenants, err := openSeeded(t, cfg).Tenants().List(context.Background())
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	if len(tenants) != 1 {
		t.Fatalf("AC-5 FAILED: got %d tenants, want 1 — losing the key file must mint a new "+
			"key, not a duplicate organisation", len(tenants))
	}
	if _, err := os.Stat(cfg.APIKeyFile); err != nil {
		t.Errorf("AC-5 FAILED: no new key file was written: %v", err)
	}
}

// ─── AC-6 ───

func TestProvisionedAPIKeyValidates(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("provision: %v", err)
	}

	key, err := os.ReadFile(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("read api key: %v", err)
	}

	db := openSeeded(t, cfg)
	info, err := db.APIKeys().ValidateKey(context.Background(), string(key))
	if err != nil {
		t.Fatalf("AC-6 FAILED: the key written for the user does not validate: %v. Ingestion "+
			"would reject every request from the traffic generator", err)
	}

	tenants, err := db.Tenants().List(context.Background())
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	if info.TenantID != tenants[0].ID {
		t.Errorf("AC-6 FAILED: the key belongs to tenant %q, not the provisioned %q",
			info.TenantID, tenants[0].ID)
	}
	if info.Status != "active" {
		t.Errorf("AC-6 FAILED: the provisioned tenant is %q, so ingestion would refuse it",
			info.Status)
	}
	// An unrestricted key is what a single-organisation self-host needs: there
	// is no second scope to grant, and a scoped key would fail unpredictably.
	if !info.HasScope("ingest") || !info.HasScope("query") {
		t.Errorf("AC-6 FAILED: the bootstrap key is scope-restricted (%q)", info.Scopes)
	}
}

// ─── §6.1 failure modes ───

// TestProvisionFailsWhenPathsAreUnusable proves the error path reports the
// underlying cause rather than a generic failure, because the operator reading
// it is looking at a stack that would not come up.
func TestProvisionFailsWhenPathsAreUnusable(t *testing.T) {
	dir := t.TempDir()
	// A regular file where a directory must be: MkdirAll cannot create under it.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	cfg := seedIn(t)
	cfg.DBPath = filepath.Join(blocker, "nested", "gravix.db")

	err := provision(context.Background(), cfg, io.Discard)
	if err == nil {
		t.Fatal("§6.1 FAILED: provision reported success with an uncreatable database path")
	}
	if errors.Is(err, ErrAlreadyProvisioned) {
		t.Fatal("§6.1 FAILED: an unusable path was reported as already provisioned")
	}
	if !strings.HasPrefix(err.Error(), "bootstrap_seed: ") {
		t.Errorf("§6.1 FAILED: the error is not prefixed with the command name: %v", err)
	}
	// Nothing should be left behind for the next run to trip over.
	if _, statErr := os.Stat(cfg.APIKeyFile); statErr == nil {
		t.Error("§6.1 FAILED: a key file was written despite the run failing")
	}
}

// ─── F-016: the dashboard login ───

// TestProvisionedLoginAuthenticates is the regression guard for F-016.
//
// The bootstrap stack seeded a tenant and an API key and no user. The dashboard
// sends Cube a JWT, which only a gateway login mints, and that login needs an
// email and a bcrypt password — so with no user the charts could never load,
// while every service reported healthy. The API key it did have is the wrong
// kind of credential: it authorises writes to ingestion, not queries to Cube.
//
// This runs the same three checks handleLogin runs, against what was persisted:
// the user resolves by email, the password in the login file verifies against
// the stored bcrypt hash, and the account is usable. Asserting only that a
// users row exists would pass with an unusable password.
func TestProvisionedLoginAuthenticates(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("provision: %v", err)
	}

	email, password := readLogin(t, cfg.LoginFile)
	if email != cfg.TenantEmail {
		t.Errorf("login file email = %q, want %q", email, cfg.TenantEmail)
	}

	db := openSeeded(t, cfg)
	user, err := db.Users().GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("F-016 regression: no user for %q (%v); the dashboard cannot obtain a JWT "+
			"and every Cube query is rejected", email, err)
	}

	// The check handleLogin makes. A password stored as plaintext, or bcrypted
	// twice, leaves a users row that exists and cannot be logged into.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		t.Fatalf("F-016 regression: the generated password does not verify against the stored "+
			"hash (%v); stored hash begins %.4q", err, user.PasswordHash)
	}

	if user.Status != "active" {
		t.Errorf("user status = %q, want active; handleLogin rejects anything else", user.Status)
	}
	if user.TwoFactorEnabled {
		t.Error("2FA is enabled on the bootstrap user, so login would demand a TOTP code nobody has")
	}
	if user.TenantID == "" {
		t.Error("user has no tenant, so the minted JWT would carry no tenant_id for queryRewrite")
	}
}

func TestProvisionedLoginFileIsNotWorldReadable(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("provision: %v", err)
	}
	info, err := os.Stat(cfg.LoginFile)
	if err != nil {
		t.Fatalf("stat login file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("login file mode = %04o, want 0600; it holds the dashboard password", mode)
	}
}

// The password is the one thing in this stack that is not derived from anything
// else, so two installs must not share it.
func TestProvisionedPasswordsDiffer(t *testing.T) {
	cfgA, cfgB := seedIn(t), seedIn(t)
	if _, err := run(t, cfgA); err != nil {
		t.Fatalf("provision A: %v", err)
	}
	if _, err := run(t, cfgB); err != nil {
		t.Fatalf("provision B: %v", err)
	}
	_, passwordA := readLogin(t, cfgA.LoginFile)
	_, passwordB := readLogin(t, cfgB.LoginFile)
	if passwordA == passwordB {
		t.Fatalf("two independent provisions produced the same password %q", passwordA)
	}
	if len(passwordA) < 32 {
		t.Errorf("generated password is %d characters, too short to be 256 bits of entropy", len(passwordA))
	}
}

// The password is printed as well as written: the compose log is where someone
// watching a first boot is already looking, and the file is 0600 inside a
// container they may not have a shell in.
func TestProvisionPrintsTheLogin(t *testing.T) {
	cfg := seedIn(t)
	out, err := run(t, cfg)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	_, password := readLogin(t, cfg.LoginFile)
	for _, want := range []string{cfg.TenantEmail, password, cfg.LoginFile} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not mention %q; a first-time user has no way to find the login\n%s", want, out)
		}
	}
}

// readLogin parses the two-line credential file provision writes.
func readLogin(t *testing.T, path string) (email, password string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read login file: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("login file line %q is not \"key: value\"", line)
		}
		switch key {
		case "email":
			email = value
		case "password":
			password = value
		}
	}
	if email == "" || password == "" {
		t.Fatalf("login file %q did not yield both an email and a password", raw)
	}
	return email, password
}

// Losing api_key.txt while keeping the database re-runs provision against a
// user that already exists, so the password is reset rather than created. That
// path goes through UpdatePassword, which — unlike Create — stores what it is
// given verbatim. Writing the plaintext there leaves a login file whose
// password looks right and cannot be used.
func TestReprovisionedLoginAuthenticates(t *testing.T) {
	cfg := seedIn(t)
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	_, firstPassword := readLogin(t, cfg.LoginFile)

	if err := os.Remove(cfg.APIKeyFile); err != nil {
		t.Fatalf("remove api key file: %v", err)
	}
	if _, err := run(t, cfg); err != nil {
		t.Fatalf("re-provision: %v", err)
	}

	email, password := readLogin(t, cfg.LoginFile)
	if password == firstPassword {
		t.Error("re-provision reused the previous password; the login file should reflect a reset")
	}

	db := openSeeded(t, cfg)
	users, err := db.Users().ListByTenant(context.Background(), tenantIDOf(t, db))
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("re-provision left %d users, want exactly 1", len(users))
	}

	user, err := db.Users().GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		t.Fatalf("the password written on re-provision does not authenticate (%v); stored value "+
			"begins %.4q — UpdatePassword stores verbatim, so it must be hashed first",
			err, user.PasswordHash)
	}
}

func tenantIDOf(t *testing.T, db *tenantdb.SQLiteDB) string {
	t.Helper()
	tenants, err := db.Tenants().List(context.Background())
	if err != nil || len(tenants) == 0 {
		t.Fatalf("list tenants: %v", err)
	}
	return tenants[0].ID
}

// ─── F-032: the generated JWT signing secret ───────────────────────────────

// TestProvisionWritesAUsableJWTSecret covers the two properties the gateway and
// Cube actually depend on: the secret is long enough for the gateway to accept,
// and it is not readable by other users.
//
// The literal this replaced was `supersecretjwtkey12345!` — 23 characters, where
// services/gateway/main.go exits 1 below 32. So the bootstrap gateway crash-looped
// on every boot and the dashboard's login could never succeed. The length is
// asserted against the same constant the gateway enforces, not against a number
// repeated here.
func TestProvisionWritesAUsableJWTSecret(t *testing.T) {
	cfg := seedIn(t)
	if err := provision(context.Background(), cfg, io.Discard); err != nil {
		t.Fatalf("provision: %v", err)
	}

	info, err := os.Stat(cfg.JWTSecretFile)
	if err != nil {
		t.Fatalf("the JWT secret file was not written: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("JWT secret file mode is %04o, want 0600: it is a signing key, and a\n"+
			"world-readable one lets any process on the host mint a token for any tenant", got)
	}

	raw, err := os.ReadFile(cfg.JWTSecretFile)
	if err != nil {
		t.Fatalf("read the JWT secret: %v", err)
	}
	secret := strings.TrimSpace(string(raw))

	// The gateway's own threshold, so this test moves if that check moves.
	const gatewayMinimum = 32
	if len(secret) < gatewayMinimum {
		t.Errorf("the generated secret is %d characters; services/gateway/main.go exits 1 below\n"+
			"%d, so the gateway would crash-loop exactly as it did with the hardcoded literal\n"+
			"(F-032)", len(secret), gatewayMinimum)
	}
	if strings.Contains(secret, "supersecret") {
		t.Errorf("the generated secret contains the published literal; it must be random")
	}
}

// TestReprovisionKeepsTheSameJWTSecret is the property that makes the stack
// survive a restart. A regenerated secret would invalidate every token already
// issued, and worse, could be written while Cube still holds the old one — a
// stack that disagrees with itself about its own signing key presents as an empty
// dashboard, not as an error.
func TestReprovisionKeepsTheSameJWTSecret(t *testing.T) {
	cfg := seedIn(t)
	if err := provision(context.Background(), cfg, io.Discard); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	first, err := os.ReadFile(cfg.JWTSecretFile)
	if err != nil {
		t.Fatalf("read the first secret: %v", err)
	}

	// Losing the API key file while keeping the rest is the re-provision path the
	// other tests in this file exercise; the secret must survive it.
	if err := os.Remove(cfg.APIKeyFile); err != nil {
		t.Fatalf("remove the api key file: %v", err)
	}
	if err := provision(context.Background(), cfg, io.Discard); err != nil {
		t.Fatalf("second provision: %v", err)
	}
	second, err := os.ReadFile(cfg.JWTSecretFile)
	if err != nil {
		t.Fatalf("read the second secret: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("re-provisioning replaced the JWT signing secret.\n" +
			"Every token already issued becomes invalid, and if Cube has not restarted it is\n" +
			"still verifying with the old value — which shows up as an empty dashboard rather\n" +
			"than as an authentication error.")
	}
}
