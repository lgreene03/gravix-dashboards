// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

const seededSecret = "grvx_sk_live_9f3a2b1c8d7e6f5a4b3c2d1e"

// seedConfigDB builds a tenant database holding one of everything the exit
// path exports, including material that must never leave.
func seedConfigDB(t *testing.T) (tenantdb.DB, string) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "gravix.db")
	db, err := tenantdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open tenantdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	tenant := &tenantdb.Tenant{ID: "acme", Name: "Acme", Email: "owner@example.com", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	if _, _, err := db.APIKeys().Create(ctx, tenant.ID, "ingest key", nil); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if err := db.Users().Create(ctx, &tenantdb.User{
		ID:              "user-1",
		TenantID:        tenant.ID,
		Email:           "owner@example.com",
		PasswordHash:    "hash-that-must-not-leak",
		Role:            "admin",
		Status:          "active",
		TwoFactorSecret: seededSecret,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	channel := &tenantdb.NotificationChannel{
		ID:       "chan-1",
		TenantID: tenant.ID,
		Name:     "ops slack",
		Type:     "webhook",
		Config:   `{"webhook_url":"https://hooks.example.com/x","auth_header":"Bearer super-secret-token"}`,
		Status:   "active",
	}
	if err := db.NotificationChannels().Create(ctx, channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if err := db.AlertRules().Create(ctx, &tenantdb.AlertRule{
		ID:            "rule-1",
		ChannelID:     channel.ID,
		TenantID:      tenant.ID,
		Name:          "checkout error rate",
		Metric:        "error_rate",
		Operator:      "gt",
		Threshold:     0.05,
		WindowMinutes: 5,
		Status:        "active",
	}); err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	if err := db.ScheduledExports().Create(ctx, &tenantdb.ScheduledExport{
		TenantID:       tenant.ID,
		Name:           "nightly",
		Schedule:       "0 3 * * *",
		DataType:       "request_facts",
		Format:         "parquet",
		DestinationURL: "s3://backups/gravix",
		LookbackDays:   7,
		Status:         "active",
	}); err != nil {
		t.Fatalf("create scheduled export: %v", err)
	}

	return db, tenant.ID
}

func TestExportConfigWritesEveryDocument(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	outDir := filepath.Join(t.TempDir(), "config")

	if err := ExportConfig(context.Background(), db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	for _, name := range []string{
		"alert_rules.json", "dashboards.json", "slos.json",
		"api_keys.json", "team.json", "scheduled_exports.json",
	} {
		path := filepath.Join(outDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		var generic any
		if err := json.Unmarshal(data, &generic); err != nil {
			t.Errorf("%s is not valid JSON: %v", name, err)
		}
	}
}

// AC-7: the configuration a user needs to rebuild elsewhere must be complete,
// not merely present.
func TestConfigExportComplete(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	outDir := filepath.Join(t.TempDir(), "config")

	if err := ExportConfig(context.Background(), db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	rules := readJSONArray(t, filepath.Join(outDir, "alert_rules.json"))
	if len(rules) != 1 {
		t.Fatalf("exported %d alert rules, want 1", len(rules))
	}
	rule := rules[0]
	for field, want := range map[string]any{
		"Name":          "checkout error rate",
		"Metric":        "error_rate",
		"Operator":      "gt",
		"Threshold":     0.05,
		"WindowMinutes": float64(5),
		"Status":        "active",
	} {
		if got := rule[field]; got != want {
			t.Errorf("alert rule %s = %v, want %v", field, got, want)
		}
	}

	schedules := readJSONArray(t, filepath.Join(outDir, "scheduled_exports.json"))
	if len(schedules) != 1 {
		t.Fatalf("exported %d schedules, want 1", len(schedules))
	}
	if schedules[0]["Schedule"] != "0 3 * * *" {
		t.Errorf("schedule = %v", schedules[0]["Schedule"])
	}

	team := readJSONArray(t, filepath.Join(outDir, "team.json"))
	if len(team) != 1 {
		t.Fatalf("exported %d users, want 1", len(team))
	}
	if team[0]["Email"] != "owner@example.com" {
		t.Errorf("user email = %v; an export that loses who had access is not complete", team[0]["Email"])
	}
	if team[0]["Role"] != "admin" {
		t.Errorf("user role = %v", team[0]["Role"])
	}
}

// AC-5: every redacted field keeps its name and loses its value.
func TestAllSecretFieldsRedacted(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	outDir := filepath.Join(t.TempDir(), "config")

	if err := ExportConfig(context.Background(), db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	team := readJSONArray(t, filepath.Join(outDir, "team.json"))[0]

	// The field is retained, so a reader knows it existed...
	if _, present := team["PasswordHash"]; !present {
		t.Error("PasswordHash was dropped entirely; the field must be retained so a reader knows it existed")
	}
	// ...and its value is gone.
	if got := team["PasswordHash"]; got != RedactedPlaceholder {
		t.Errorf("PasswordHash = %v, want %q", got, RedactedPlaceholder)
	}
	if got := team["TwoFactorSecret"]; got != RedactedPlaceholder && got != "" {
		t.Errorf("TwoFactorSecret = %v, want it redacted", got)
	}

	// Non-secret fields must survive, or the export is useless.
	if team["Email"] == RedactedPlaceholder {
		t.Error("Email was redacted; over-redaction makes the export useless")
	}
}

// AC-4: the seeded key's value must not appear anywhere in the output.
func TestNoSecretInExitPath(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	outDir := filepath.Join(t.TempDir(), "config")

	// The plaintext key is returned once at creation and never stored, so the
	// strongest check available is that neither it nor the password hash we
	// seeded appears in any exported byte.
	ctx := context.Background()
	plain, _, err := db.APIKeys().Create(ctx, tenantID, "second key", nil)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if err := ExportConfig(ctx, db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read %s: %v", outDir, err)
	}

	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		text := string(data)
		for _, secret := range []string{plain, "hash-that-must-not-leak", seededSecret} {
			if strings.Contains(text, secret) {
				t.Errorf("%s contains a secret value", e.Name())
			}
		}
	}
}

// AC-6: a secret reaching output aborts and deletes the file.
//
// This drives the post-write scan directly, because the whole point of the
// scan is to catch what field selection missed — a case that by definition
// cannot be produced through the normal path.
func TestSecretLeakAborts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leaky.json")

	leaky := `[{"Name":"a key","client_secret":"sk_live_abcdefghijklmnop"}]`
	if err := os.WriteFile(path, []byte(leaky), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	field, err := scanForSecrets(path)
	if !errors.Is(err, ErrSecretLeak) {
		t.Fatalf("err = %v, want ErrSecretLeak", err)
	}
	if field != "client_secret" {
		t.Errorf("leaked field = %q, want client_secret", field)
	}
}

// The scan must see a secret nested inside an object or an array, not only at
// the top level.
func TestSecretScanFindsNestedLeaks(t *testing.T) {
	cases := map[string]string{
		"nested object": `[{"Name":"a","Meta":{"api_key":"grvx_live_xyz"}}]`,
		"nested array":  `{"items":[{"inner":[{"token":"t-abc123"}]}]}`,
		"top level":     `{"private_key":"-----BEGIN"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "doc.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := scanForSecrets(path); !errors.Is(err, ErrSecretLeak) {
				t.Fatalf("a %s leak was not detected", name)
			}
		})
	}
}

// An empty or absent value is not a leak: the field exists and holds nothing,
// which is the truth rather than a secret.
func TestSecretScanAcceptsEmptyAndRedactedValues(t *testing.T) {
	body := `[{"PasswordHash":"[redacted]","TwoFactorSecret":"","ClientSecret":null}]`
	path := filepath.Join(t.TempDir(), "clean.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if field, err := scanForSecrets(path); err != nil {
		t.Fatalf("clean document flagged %q: %v", field, err)
	}
}

// The redaction rules must match the Go field names that actually appear in
// the output. pkg/tenantdb's structs carry no json tags, so the key is the Go
// name — a rule list written in snake_case would match nothing at all without
// normalisation.
func TestRedactionMatchesGoFieldNames(t *testing.T) {
	redacted := []string{
		"PasswordHash", "password_hash",
		"TwoFactorSecret", "ClientSecret", "client_secret",
		"APIKey", "api_key", "KeyMaterial",
		"Token", "RefreshToken", "PrivateKey",
		"WebhookAuthHeader", "StripeKey",
	}
	for _, name := range redacted {
		if !IsRedactedField(name) {
			t.Errorf("IsRedactedField(%q) = false; it carries secret material", name)
		}
	}

	kept := []string{"Email", "Role", "Name", "TenantID", "CreatedAt", "Status", "Schedule", "KeyPrefix", "Threshold"}
	for _, name := range kept {
		if IsRedactedField(name) {
			t.Errorf("IsRedactedField(%q) = true; over-redaction makes the export useless", name)
		}
	}
}

// Every rule in RedactedFields must actually redact something named after it.
// A rule that matches nothing is a rule someone will trust and that does not
// work.
func TestEveryRedactionRuleIsLive(t *testing.T) {
	for _, rule := range RedactedFields {
		if !IsRedactedField(rule) {
			t.Errorf("rule %q does not match its own name", rule)
		}
	}
}

func TestExportConfigCreatesTheOutputDirectory(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	outDir := filepath.Join(t.TempDir(), "deep", "nested", "config")

	if err := ExportConfig(context.Background(), db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "team.json")); err != nil {
		t.Fatalf("expected output under a created directory: %v", err)
	}
}

// Another tenant's configuration must not appear in this tenant's export.
func TestExportConfigIsTenantScoped(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	ctx := context.Background()

	other := &tenantdb.Tenant{ID: "other-co", Name: "Other", Email: "them@example.com", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(ctx, other); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	otherChannel := &tenantdb.NotificationChannel{
		ID: "chan-other", TenantID: other.ID, Name: "theirs", Type: "webhook",
		Config: `{"webhook_url":"https://hooks.example.com/y"}`, Status: "active",
	}
	if err := db.NotificationChannels().Create(ctx, otherChannel); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.AlertRules().Create(ctx, &tenantdb.AlertRule{
		ID:            "rule-other",
		ChannelID:     otherChannel.ID,
		TenantID:      other.ID,
		Name:          "not-yours",
		Metric:        "error_rate",
		Operator:      "gt",
		WindowMinutes: 5,
		Status:        "active",
	}); err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	outDir := filepath.Join(t.TempDir(), "config")
	if err := ExportConfig(ctx, db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "alert_rules.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "not-yours") {
		t.Error("another tenant's alert rule appeared in this tenant's export")
	}
}

// Config files hold names, emails and roles, so they are written for the
// owner only rather than world-readable.
func TestExportedConfigIsNotWorldReadable(t *testing.T) {
	db, tenantID := seedConfigDB(t)
	outDir := filepath.Join(t.TempDir(), "config")

	if err := ExportConfig(context.Background(), db, tenantID, outDir); err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	info, err := os.Stat(filepath.Join(outDir, "team.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("team.json mode = %o, want no group or other access", perm)
	}
}

func readJSONArray(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// A secret can hide inside a string that holds JSON. pkg/tenantdb stores
// notification channel settings that way — a webhook auth header lives inside
// a blob in a field called Config — and a scan that only walked decoded
// structure would step straight past it.
func TestSecretScanDescendsIntoJSONHeldInAString(t *testing.T) {
	body := `[{"Name":"ops slack","Config":"{\"webhook_url\":\"https://hooks.example.com/x\",\"auth_header\":\"Bearer super-secret-token\"}"}]`
	path := filepath.Join(t.TempDir(), "channels.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	field, err := scanForSecrets(path)
	if !errors.Is(err, ErrSecretLeak) {
		t.Fatalf("a secret nested inside a JSON string was not detected (err = %v)", err)
	}
	if field != "auth_header" {
		t.Errorf("leaked field = %q, want auth_header", field)
	}
}

// Redaction must reach into the same place, replacing the nested value while
// leaving the surrounding blob intact and still valid JSON.
func TestRedactionReachesIntoJSONHeldInAString(t *testing.T) {
	type channel struct {
		Name   string
		Config string
	}
	records := []channel{{
		Name:   "ops slack",
		Config: `{"webhook_url":"https://hooks.example.com/x","auth_header":"Bearer super-secret-token"}`,
	}}

	data, err := redactedJSON(records)
	if err != nil {
		t.Fatalf("redactedJSON: %v", err)
	}

	if strings.Contains(string(data), "super-secret-token") {
		t.Fatalf("the nested secret survived redaction:\n%s", data)
	}
	// The non-secret part of the blob must survive, or the export loses the
	// configuration it exists to preserve.
	if !strings.Contains(string(data), "hooks.example.com") {
		t.Errorf("redaction discarded the whole blob:\n%s", data)
	}

	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(out[0]["Config"].(string)), &inner); err != nil {
		t.Fatalf("the nested blob is no longer valid JSON: %v", err)
	}
	if inner["auth_header"] != RedactedPlaceholder {
		t.Errorf("auth_header = %v, want %q", inner["auth_header"], RedactedPlaceholder)
	}
}

// A string that merely starts with a brace is not JSON, and must be left alone
// rather than mangled.
func TestRedactionLeavesNonJSONStringsAlone(t *testing.T) {
	records := []map[string]string{{"Name": "weird", "Note": "{not json at all"}}

	data, err := redactedJSON(records)
	if err != nil {
		t.Fatalf("redactedJSON: %v", err)
	}
	if !strings.Contains(string(data), "{not json at all") {
		t.Errorf("a non-JSON string was altered:\n%s", data)
	}
}

// The rule list is matched in both directions, and the length floor is what
// keeps that from over-redacting. Both halves need pinning: a regression in
// either direction is a leak or a useless export.
func TestRedactionMatchesRulesInBothDirections(t *testing.T) {
	// More specific than the rule.
	for _, name := range []string{"TwoFactorSecret", "RefreshToken", "StripeSecretKey"} {
		if !IsRedactedField(name) {
			t.Errorf("IsRedactedField(%q) = false; it contains a rule", name)
		}
	}

	// Less specific than the rule: auth_header is how the webhook secret is
	// actually named inside a notification channel's config blob.
	for _, name := range []string{"auth_header", "AuthHeader"} {
		if !IsRedactedField(name) {
			t.Errorf("IsRedactedField(%q) = false; the rule webhook_auth_header must catch it", name)
		}
	}

	// A short name must not match a long rule by accident.
	for _, name := range []string{"key", "id", "type", "Name", "URL"} {
		if IsRedactedField(name) {
			t.Errorf("IsRedactedField(%q) = true; a short name matched a long rule by accident", name)
		}
	}
}
