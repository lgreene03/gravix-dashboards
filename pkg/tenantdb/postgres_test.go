//go:build postgres || all

package tenantdb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// newPostgresTestDB creates a PostgresDB connected to a test container.
// Skips the test if Docker is unavailable or DATABASE_TEST_URL is not set.
func newPostgresTestDB(t *testing.T) *PostgresDB {
	t.Helper()

	// Allow manual override via env var (e.g., CI pipeline).
	connStr := os.Getenv("DATABASE_TEST_URL")
	if connStr == "" {
		// Check if Docker is available.
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("Docker not available, skipping PostgreSQL tests")
		}

		// Start an ephemeral postgres container.
		name := fmt.Sprintf("gravix-pg-test-%d", time.Now().UnixNano()%100000)
		port := fmt.Sprintf("%d", 15432+time.Now().UnixNano()%1000)

		cmd := exec.Command("docker", "run", "-d", "--rm",
			"--name", name,
			"-e", "POSTGRES_PASSWORD=testpass",
			"-e", "POSTGRES_DB=gravix_test",
			"-p", port+":5432",
			"postgres:16-alpine",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("Failed to start postgres container: %v\n%s", err, out)
		}

		t.Cleanup(func() {
			exec.Command("docker", "stop", name).Run()
		})

		connStr = fmt.Sprintf("postgres://postgres:testpass@localhost:%s/gravix_test?sslmode=disable", port)

		// Wait for postgres to be ready (up to 15s).
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			db, err := OpenPostgres(connStr)
			if err == nil {
				db.Close()
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	db, err := OpenPostgres(connStr)
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Truncate all tables to ensure clean state between test runs.
	tables := []string{
		"retention_policies", "audit_log", "alert_history", "alert_rules",
		"notification_channels", "monthly_usage", "event_counters",
		"api_keys", "users", "leader_locks", "tenants",
	}
	for _, tbl := range tables {
		db.db.ExecContext(context.Background(), "DELETE FROM "+tbl)
	}

	return db
}

func createPgTestTenant(t *testing.T, db *PostgresDB, name, email string) *Tenant {
	t.Helper()
	tenant := &Tenant{Name: name, Email: email, Plan: "free", Status: "active"}
	if err := db.Tenants().Create(context.Background(), tenant); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}
	return tenant
}

// --- Open / Close ---

func TestPostgresOpen(t *testing.T) {
	db := newPostgresTestDB(t)

	if db.Tenants() == nil {
		t.Error("Tenants() returned nil")
	}
	if db.APIKeys() == nil {
		t.Error("APIKeys() returned nil")
	}
	if db.Users() == nil {
		t.Error("Users() returned nil")
	}
	if db.EventCounters() == nil {
		t.Error("EventCounters() returned nil")
	}
	if db.MonthlyUsage() == nil {
		t.Error("MonthlyUsage() returned nil")
	}
	if db.NotificationChannels() == nil {
		t.Error("NotificationChannels() returned nil")
	}
	if db.AlertRules() == nil {
		t.Error("AlertRules() returned nil")
	}
	if db.AlertHistory() == nil {
		t.Error("AlertHistory() returned nil")
	}
	if db.AuditLog() == nil {
		t.Error("AuditLog() returned nil")
	}
	if db.RetentionPolicies() == nil {
		t.Error("RetentionPolicies() returned nil")
	}
}

// --- Tenant Repo ---

func TestPostgresTenantCreate(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()

	tenant := &Tenant{Name: "Acme", Email: "acme@pg.test", Plan: "starter", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tenant.ID == "" {
		t.Error("ID should be auto-generated")
	}
	if tenant.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestPostgresTenantGetByID(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "GetByID", "getbyid@pg.test")

	got, err := db.Tenants().GetByID(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "GetByID" {
		t.Errorf("Name = %q, want GetByID", got.Name)
	}
}

func TestPostgresTenantGetByEmail(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	createPgTestTenant(t, db, "ByEmail", "byemail@pg.test")

	got, err := db.Tenants().GetByEmail(ctx, "byemail@pg.test")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Name != "ByEmail" {
		t.Errorf("Name = %q, want ByEmail", got.Name)
	}
}

func TestPostgresTenantUpdatePlan(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "PlanUp", "planup@pg.test")

	if err := db.Tenants().UpdatePlan(ctx, tenant.ID, "pro"); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	got, _ := db.Tenants().GetByID(ctx, tenant.ID)
	if got.Plan != "pro" {
		t.Errorf("Plan = %q, want pro", got.Plan)
	}
}

func TestPostgresTenantList(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	createPgTestTenant(t, db, "List1", "list1@pg.test")
	createPgTestTenant(t, db, "List2", "list2@pg.test")

	list, err := db.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) < 2 {
		t.Errorf("List returned %d tenants, want >= 2", len(list))
	}
}

// --- API Key Repo ---

func TestPostgresAPIKeyCreateAndValidate(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "KeyTest", "keytest@pg.test")

	plainKey, key, err := db.APIKeys().Create(ctx, tenant.ID, "test-key", nil)
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}
	if plainKey == "" || key == nil {
		t.Fatal("expected non-empty key")
	}
	if key.TenantID != tenant.ID {
		t.Errorf("TenantID = %q, want %q", key.TenantID, tenant.ID)
	}

	info, err := db.APIKeys().ValidateKey(ctx, plainKey)
	if err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	if info.TenantID != tenant.ID {
		t.Errorf("info.TenantID = %q, want %q", info.TenantID, tenant.ID)
	}
}

func TestPostgresAPIKeyRevoke(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Revoke", "revoke@pg.test")

	plainKey, key, _ := db.APIKeys().Create(ctx, tenant.ID, "revokable", nil)
	if err := db.APIKeys().Revoke(ctx, key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, err := db.APIKeys().ValidateKey(ctx, plainKey)
	if err == nil {
		t.Error("expected error validating revoked key")
	}
}

// --- User Repo ---

func TestPostgresUserCreateAndGet(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "UserTest", "usertest@pg.test")

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	user := &User{
		TenantID:     tenant.ID,
		Email:        "user@pg.test",
		PasswordHash: string(hash),
		Role:         "admin",
	}
	if err := db.Users().Create(ctx, user); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if user.ID == "" {
		t.Error("User ID should be auto-generated")
	}

	got, err := db.Users().GetByEmail(ctx, "user@pg.test")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Role != "admin" {
		t.Errorf("Role = %q, want admin", got.Role)
	}
}

// --- Event Counter Repo ---

func TestPostgresEventCounter(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Counter", "counter@pg.test")

	if err := db.EventCounters().Increment(ctx, tenant.ID, "2026-03-21", 100); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	if err := db.EventCounters().Increment(ctx, tenant.ID, "2026-03-21", 50); err != nil {
		t.Fatalf("Increment 2: %v", err)
	}

	count, err := db.EventCounters().GetCount(ctx, tenant.ID, "2026-03-21")
	if err != nil {
		t.Fatalf("GetCount: %v", err)
	}
	if count != 150 {
		t.Errorf("count = %d, want 150", count)
	}
}

// --- Monthly Usage Repo ---

func TestPostgresMonthlyUsage(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Usage", "usage@pg.test")

	if err := db.MonthlyUsage().Snapshot(ctx, tenant.ID, "2026-03", 5000, "starter"); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	list, err := db.MonthlyUsage().GetByTenant(ctx, tenant.ID, 10)
	if err != nil {
		t.Fatalf("GetByTenant: %v", err)
	}
	if len(list) != 1 || list[0].Count != 5000 {
		t.Errorf("unexpected usage: %+v", list)
	}
}

// --- Notification Channel Repo ---

func TestPostgresNotificationChannel(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Notify", "notify@pg.test")

	ch := &NotificationChannel{
		TenantID: tenant.ID,
		Name:     "slack-ops",
		Type:     "slack",
		Config:   `{"webhook_url":"https://hooks.slack.com/test"}`,
		Status:   "active",
	}
	if err := db.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatalf("Create channel: %v", err)
	}
	if ch.ID == "" {
		t.Error("Channel ID should be auto-generated")
	}

	got, err := db.NotificationChannels().GetByID(ctx, ch.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "slack-ops" {
		t.Errorf("Name = %q, want slack-ops", got.Name)
	}
}

// --- Alert Rule Repo ---

func TestPostgresAlertRule(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Alert", "alert@pg.test")

	ch := &NotificationChannel{
		TenantID: tenant.ID, Name: "ch", Type: "webhook",
		Config: `{"webhook_url":"http://localhost"}`, Status: "active",
	}
	db.NotificationChannels().Create(ctx, ch)

	rule := &AlertRule{
		TenantID:        tenant.ID,
		Name:            "High Error Rate",
		Metric:          "error_rate",
		Operator:        "gt",
		Threshold:       5.0,
		WindowMinutes:   5,
		ChannelID:       ch.ID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := db.AlertRules().Create(ctx, rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}
	if rule.ID == "" {
		t.Error("Rule ID should be auto-generated")
	}

	active, err := db.AlertRules().ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	found := false
	for _, r := range active {
		if r.ID == rule.ID {
			found = true
		}
	}
	if !found {
		t.Error("rule not in ListActive results")
	}
}

// --- Alert History Repo ---

func TestPostgresAlertHistory(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "History", "history@pg.test")

	entry := &AlertHistoryEntry{
		RuleID:      "rule-123",
		TenantID:    tenant.ID,
		Metric:      "error_rate",
		Threshold:   5.0,
		ActualValue: 7.5,
		Status:      "fired",
		Message:     "error rate above threshold",
	}
	if err := db.AlertHistory().Create(ctx, entry); err != nil {
		t.Fatalf("Create history: %v", err)
	}

	list, err := db.AlertHistory().ListByTenant(ctx, tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 entry, got %d", len(list))
	}
}

// --- Audit Repo ---

func TestPostgresAuditLog(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Audit", "audit@pg.test")

	entry := &AuditEntry{
		TenantID:   tenant.ID,
		UserID:     "user-1",
		Action:     "api_key.create",
		Resource:   "api_key",
		ResourceID: "key-1",
		Detail:     `{"name":"test"}`,
		IPAddress:  "127.0.0.1",
	}
	if err := db.AuditLog().Log(ctx, entry); err != nil {
		t.Fatalf("Log: %v", err)
	}

	list, total, err := db.AuditLog().ListByTenant(ctx, tenant.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Errorf("expected 1 entry, got %d (total %d)", len(list), total)
	}
}

// --- Retention Policy Repo ---

func TestPostgresRetentionPolicy(t *testing.T) {
	db := newPostgresTestDB(t)
	ctx := context.Background()
	tenant := createPgTestTenant(t, db, "Retention", "retention@pg.test")

	policy := &RetentionPolicy{
		TenantID:  tenant.ID,
		FactsDays: 60,
		MetricsDays: 90,
		TracesDays:  14,
	}
	if err := db.RetentionPolicies().Upsert(ctx, policy); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := db.RetentionPolicies().GetByTenantID(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("GetByTenantID: %v", err)
	}
	if got.FactsDays != 60 {
		t.Errorf("FactsDays = %d, want 60", got.FactsDays)
	}

	// Update
	policy.FactsDays = 45
	if err := db.RetentionPolicies().Upsert(ctx, policy); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	got, _ = db.RetentionPolicies().GetByTenantID(ctx, tenant.ID)
	if got.FactsDays != 45 {
		t.Errorf("FactsDays after update = %d, want 45", got.FactsDays)
	}
}
