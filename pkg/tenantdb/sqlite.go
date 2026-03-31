package tenantdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tenants (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	email       TEXT NOT NULL UNIQUE,
	plan        TEXT NOT NULL DEFAULT 'free',
	stripe_customer_id TEXT DEFAULT '',
	stripe_subscription_id TEXT DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'active',
	overage_allowed INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS api_keys (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	key_hash    TEXT NOT NULL UNIQUE,
	key_prefix  TEXT NOT NULL,
	name        TEXT NOT NULL DEFAULT 'default',
	status      TEXT NOT NULL DEFAULT 'active',
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	last_used_at TEXT,
	expires_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant ON api_keys(tenant_id);

CREATE TABLE IF NOT EXISTS users (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	email       TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role        TEXT NOT NULL DEFAULT 'admin',
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS event_counters (
	tenant_id TEXT NOT NULL,
	day       TEXT NOT NULL,
	count     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (tenant_id, day)
);

CREATE TABLE IF NOT EXISTS notification_channels (
	id         TEXT PRIMARY KEY,
	tenant_id  TEXT NOT NULL REFERENCES tenants(id),
	name       TEXT NOT NULL,
	type       TEXT NOT NULL,
	config     TEXT NOT NULL DEFAULT '{}',
	status     TEXT NOT NULL DEFAULT 'active',
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_notification_channels_tenant ON notification_channels(tenant_id);

CREATE TABLE IF NOT EXISTS alert_rules (
	id                TEXT PRIMARY KEY,
	tenant_id         TEXT NOT NULL REFERENCES tenants(id),
	name              TEXT NOT NULL,
	metric            TEXT NOT NULL,
	operator          TEXT NOT NULL,
	threshold         REAL NOT NULL,
	window_minutes    INTEGER NOT NULL DEFAULT 5,
	service           TEXT NOT NULL DEFAULT '',
	path_template     TEXT NOT NULL DEFAULT '',
	channel_id        TEXT NOT NULL REFERENCES notification_channels(id),
	cooldown_minutes  INTEGER NOT NULL DEFAULT 15,
	status            TEXT NOT NULL DEFAULT 'active',
	last_triggered_at TEXT,
	created_at        TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_alert_rules_tenant ON alert_rules(tenant_id);

CREATE TABLE IF NOT EXISTS alert_history (
	id            TEXT PRIMARY KEY,
	rule_id       TEXT NOT NULL REFERENCES alert_rules(id),
	tenant_id     TEXT NOT NULL REFERENCES tenants(id),
	metric        TEXT NOT NULL,
	threshold     REAL NOT NULL,
	actual_value  REAL NOT NULL,
	service       TEXT NOT NULL DEFAULT '',
	path_template TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL DEFAULT 'fired',
	message       TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_alert_history_tenant ON alert_history(tenant_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_rule ON alert_history(rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_created ON alert_history(created_at);

CREATE TABLE IF NOT EXISTS audit_logs (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	user_id     TEXT NOT NULL,
	action      TEXT NOT NULL,
	resource    TEXT NOT NULL DEFAULT '',
	resource_id TEXT NOT NULL DEFAULT '',
	detail      TEXT NOT NULL DEFAULT '',
	ip_address  TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_tenant_time ON audit_logs(tenant_id, created_at);

CREATE TABLE IF NOT EXISTS monthly_usage (
	tenant_id  TEXT NOT NULL,
	month      TEXT NOT NULL,
	count      INTEGER NOT NULL DEFAULT 0,
	plan       TEXT NOT NULL DEFAULT 'free',
	snapped_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (tenant_id, month)
);

CREATE TABLE IF NOT EXISTS retention_policies (
	tenant_id    TEXT PRIMARY KEY REFERENCES tenants(id),
	facts_days   INTEGER NOT NULL DEFAULT 0,
	metrics_days INTEGER NOT NULL DEFAULT 0,
	traces_days  INTEGER NOT NULL DEFAULT 0,
	updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS leader_locks (
	lock_name    TEXT PRIMARY KEY,
	holder_id    TEXT NOT NULL,
	acquired_at  TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL REFERENCES users(id),
	token_hash  TEXT NOT NULL UNIQUE,
	expires_at  TEXT NOT NULL,
	used_at     TEXT,
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_prt_token ON password_reset_tokens(token_hash);

CREATE TABLE IF NOT EXISTS email_verification_tokens (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL REFERENCES users(id),
	token_hash  TEXT NOT NULL UNIQUE,
	expires_at  TEXT NOT NULL,
	verified_at TEXT,
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_evt_token ON email_verification_tokens(token_hash);

CREATE TABLE IF NOT EXISTS invitations (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	email       TEXT NOT NULL,
	role        TEXT NOT NULL DEFAULT 'viewer',
	token_hash  TEXT NOT NULL UNIQUE,
	status      TEXT NOT NULL DEFAULT 'pending',
	invited_by  TEXT NOT NULL,
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_inv_token ON invitations(token_hash);
CREATE INDEX IF NOT EXISTS idx_inv_tenant ON invitations(tenant_id);

CREATE TABLE IF NOT EXISTS consent_records (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	user_id     TEXT NOT NULL,
	type        TEXT NOT NULL,
	version     TEXT NOT NULL DEFAULT '1.0',
	accepted    INTEGER NOT NULL DEFAULT 1,
	ip_address  TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_consent_user ON consent_records(user_id);

CREATE TABLE IF NOT EXISTS deletion_requests (
	id           TEXT PRIMARY KEY,
	tenant_id    TEXT NOT NULL REFERENCES tenants(id),
	requested_by TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'pending',
	requested_at TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at   TEXT NOT NULL,
	completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_deletion_tenant ON deletion_requests(tenant_id);
`

// SQLiteDB implements DB using a SQLite database.
type SQLiteDB struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at the given path and runs
// schema migrations. The database uses WAL mode for concurrent reads.
func Open(dbPath string) (*SQLiteDB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode and foreign keys
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Enforce WAL mode for better read concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	// Connection pool tuning: SQLite serializes writes, so a single
	// connection avoids SQLITE_BUSY contention while WAL mode still
	// allows concurrent reads from the same connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // no lifetime limit for single-conn pool

	// Run schema via migration framework (handles both fresh and pre-existing DBs)
	if err := RunMigrations(db, "sqlite"); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &SQLiteDB{db: db}, nil
}

func (s *SQLiteDB) Close() error        { return s.db.Close() }
func (s *SQLiteDB) Tenants() TenantRepo { return &sqliteTenantRepo{db: s.db} }
func (s *SQLiteDB) APIKeys() APIKeyRepo { return &sqliteAPIKeyRepo{db: s.db} }
func (s *SQLiteDB) Users() UserRepo     { return &sqliteUserRepo{db: s.db} }
func (s *SQLiteDB) EventCounters() EventCounterRepo {
	return &sqliteEventCounterRepo{db: s.db}
}
func (s *SQLiteDB) NotificationChannels() NotificationChannelRepo {
	return &sqliteNotificationChannelRepo{db: s.db}
}
func (s *SQLiteDB) AlertRules() AlertRuleRepo     { return &sqliteAlertRuleRepo{db: s.db} }
func (s *SQLiteDB) AlertHistory() AlertHistoryRepo { return &sqliteAlertHistoryRepo{db: s.db} }
func (s *SQLiteDB) AuditLog() AuditRepo            { return &sqliteAuditRepo{db: s.db} }
func (s *SQLiteDB) MonthlyUsage() MonthlyUsageRepo  { return &sqliteMonthlyUsageRepo{db: s.db} }
func (s *SQLiteDB) RetentionPolicies() RetentionPolicyRepo {
	return &sqliteRetentionPolicyRepo{db: s.db}
}
func (s *SQLiteDB) PasswordResets() PasswordResetRepo {
	return &sqlitePasswordResetRepo{db: s.db}
}
func (s *SQLiteDB) EmailVerifications() EmailVerificationRepo {
	return &sqliteEmailVerificationRepo{db: s.db}
}
func (s *SQLiteDB) Invitations() InvitationRepo {
	return &sqliteInvitationRepo{db: s.db}
}
func (s *SQLiteDB) ConsentRecords() ConsentRecordRepo {
	return &sqliteConsentRecordRepo{db: s.db}
}
func (s *SQLiteDB) DeletionRequests() DeletionRequestRepo {
	return &sqliteDeletionRequestRepo{db: s.db}
}
func (s *SQLiteDB) SSOConfigs() SSOConfigRepo {
	return &sqliteSSOConfigRepo{db: s.db}
}
func (s *SQLiteDB) Sessions() SessionRepo {
	return &sqliteSessionRepo{db: s.db}
}
func (s *SQLiteDB) RecoveryCodes() RecoveryCodeRepo {
	return &sqliteRecoveryCodeRepo{db: s.db}
}
func (s *SQLiteDB) RevokedTokens() RevokedTokenRepo {
	return &sqliteRevokedTokenRepo{db: s.db}
}
func (s *SQLiteDB) SSOStates() SSOStateRepo {
	return &sqliteSSOStateRepo{db: s.db}
}

// --- Tenant Repo ---

type sqliteTenantRepo struct{ db *sql.DB }

func (r *sqliteTenantRepo) Create(ctx context.Context, t *Tenant) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	overageInt := 0
	if t.OverageAllowed {
		overageInt = 1
	}
	var trialStarted, trialEnds interface{}
	if t.TrialStartedAt != nil {
		trialStarted = t.TrialStartedAt.UTC().Format(time.RFC3339)
	}
	if t.TrialEndsAt != nil {
		trialEnds = t.TrialEndsAt.UTC().Format(time.RFC3339)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tenants (id, name, email, plan, status, overage_allowed, trial_started_at, trial_ends_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Email, t.Plan, t.Status, overageInt, trialStarted, trialEnds, now, now,
	)
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, now)
	t.UpdatedAt = t.CreatedAt
	return nil
}

func (r *sqliteTenantRepo) GetByID(ctx context.Context, id string) (*Tenant, error) {
	return r.scanTenant(r.db.QueryRowContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, overage_allowed, trial_started_at, trial_ends_at, created_at, updated_at FROM tenants WHERE id = ?`, id))
}

func (r *sqliteTenantRepo) GetByEmail(ctx context.Context, email string) (*Tenant, error) {
	return r.scanTenant(r.db.QueryRowContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, overage_allowed, trial_started_at, trial_ends_at, created_at, updated_at FROM tenants WHERE email = ?`, email))
}

func (r *sqliteTenantRepo) UpdatePlan(ctx context.Context, id, plan string) error {
	// Set overage_allowed based on plan: free=0, paid=1
	overageAllowed := 0
	if plan != "free" {
		overageAllowed = 1
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET plan = ?, overage_allowed = ?, updated_at = datetime('now') WHERE id = ?`, plan, overageAllowed, id)
	return err
}

func (r *sqliteTenantRepo) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET status = ?, updated_at = datetime('now') WHERE id = ?`, status, id)
	return err
}

func (r *sqliteTenantRepo) UpdateStripe(ctx context.Context, id, customerID, subscriptionID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET stripe_customer_id = ?, stripe_subscription_id = ?, updated_at = datetime('now') WHERE id = ?`,
		customerID, subscriptionID, id)
	return err
}

func (r *sqliteTenantRepo) UpdateTrial(ctx context.Context, id string, trialStart, trialEnd *time.Time) error {
	var startVal, endVal interface{}
	if trialStart != nil {
		startVal = trialStart.UTC().Format(time.RFC3339)
	}
	if trialEnd != nil {
		endVal = trialEnd.UTC().Format(time.RFC3339)
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET trial_started_at = ?, trial_ends_at = ?, updated_at = datetime('now') WHERE id = ?`,
		startVal, endVal, id)
	return err
}

func (r *sqliteTenantRepo) UpdateParentTenant(ctx context.Context, id, parentTenantID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET parent_tenant_id = ?, updated_at = datetime('now') WHERE id = ?`,
		parentTenantID, id)
	return err
}

func (r *sqliteTenantRepo) ListChildren(ctx context.Context, parentTenantID string) ([]*Tenant, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, overage_allowed, parent_tenant_id, trial_started_at, trial_ends_at, created_at, updated_at
		 FROM tenants WHERE parent_tenant_id = ? ORDER BY created_at`, parentTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenants []*Tenant
	for rows.Next() {
		t := &Tenant{}
		var createdAt, updatedAt string
		var trialStarted, trialEnds, parentID sql.NullString
		var overageInt int
		if err := rows.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
			&t.StripeCustomerID, &t.StripeSubscriptionID,
			&t.Status, &overageInt, &parentID, &trialStarted, &trialEnds, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.OverageAllowed = overageInt != 0
		if parentID.Valid {
			t.ParentTenantID = parentID.String
		}
		if trialStarted.Valid {
			ts, _ := time.Parse(time.RFC3339, trialStarted.String)
			t.TrialStartedAt = &ts
		}
		if trialEnds.Valid {
			te, _ := time.Parse(time.RFC3339, trialEnds.String)
			t.TrialEndsAt = &te
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

func (r *sqliteTenantRepo) List(ctx context.Context) ([]*Tenant, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, overage_allowed, trial_started_at, trial_ends_at, created_at, updated_at FROM tenants ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenants []*Tenant
	for rows.Next() {
		t, err := r.scanTenantRow(rows)
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

func (r *sqliteTenantRepo) scanTenant(row *sql.Row) (*Tenant, error) {
	t := &Tenant{}
	var createdAt, updatedAt string
	var trialStarted, trialEnds sql.NullString
	var overageInt int
	err := row.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
		&t.StripeCustomerID, &t.StripeSubscriptionID,
		&t.Status, &overageInt, &trialStarted, &trialEnds, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	t.OverageAllowed = overageInt != 0
	if trialStarted.Valid {
		ts, _ := time.Parse(time.RFC3339, trialStarted.String)
		t.TrialStartedAt = &ts
	}
	if trialEnds.Valid {
		te, _ := time.Parse(time.RFC3339, trialEnds.String)
		t.TrialEndsAt = &te
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return t, nil
}

type tenantScanner interface {
	Scan(dest ...interface{}) error
}

func (r *sqliteTenantRepo) scanTenantRow(row tenantScanner) (*Tenant, error) {
	t := &Tenant{}
	var createdAt, updatedAt string
	var trialStarted, trialEnds sql.NullString
	var overageInt int
	err := row.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
		&t.StripeCustomerID, &t.StripeSubscriptionID,
		&t.Status, &overageInt, &trialStarted, &trialEnds, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	t.OverageAllowed = overageInt != 0
	if trialStarted.Valid {
		ts, _ := time.Parse(time.RFC3339, trialStarted.String)
		t.TrialStartedAt = &ts
	}
	if trialEnds.Valid {
		te, _ := time.Parse(time.RFC3339, trialEnds.String)
		t.TrialEndsAt = &te
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return t, nil
}

// --- API Key Repo ---

type sqliteAPIKeyRepo struct{ db *sql.DB }

// generateKey creates a cryptographically random API key with the grvx_ prefix.
func generateKey() (plainKey string, keyHash string, keyPrefix string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	plain := "grvx_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plain))
	hashStr := fmt.Sprintf("%x", hash[:])
	prefix := plain[:13] // "grvx_" + 8 chars
	return plain, hashStr, prefix, nil
}

// hashKey computes the SHA-256 hash of a plaintext API key.
func hashKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", h[:])
}

func (r *sqliteAPIKeyRepo) Create(ctx context.Context, tenantID, name string, expiresAt *time.Time) (string, *APIKey, error) {
	plain, hash, prefix, err := generateKey()
	if err != nil {
		return "", nil, err
	}

	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	var expiresStr sql.NullString
	if expiresAt != nil {
		expiresStr = sql.NullString{String: expiresAt.UTC().Format(time.RFC3339), Valid: true}
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, tenant_id, key_hash, key_prefix, name, status, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, 'active', ?, ?)`,
		id, tenantID, hash, prefix, name, now, expiresStr,
	)
	if err != nil {
		return "", nil, fmt.Errorf("create api key: %w", err)
	}

	createdAt, _ := time.Parse(time.RFC3339, now)
	key := &APIKey{
		ID:        id,
		TenantID:  tenantID,
		KeyPrefix: prefix,
		Name:      name,
		Status:    "active",
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}
	return plain, key, nil
}

func (r *sqliteAPIKeyRepo) ValidateKey(ctx context.Context, rawKey string) (*APIKeyInfo, error) {
	hash := hashKey(rawKey)

	var info APIKeyInfo
	var overageInt int
	var scopes sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT t.id, t.plan, t.status, t.overage_allowed, k.scopes
		 FROM api_keys k JOIN tenants t ON k.tenant_id = t.id
		 WHERE k.key_hash = ? AND k.status = 'active'
		   AND (k.expires_at IS NULL OR datetime(k.expires_at) > datetime('now'))`,
		hash,
	).Scan(&info.TenantID, &info.Plan, &info.Status, &overageInt, &scopes)
	info.OverageAllowed = overageInt != 0
	if scopes.Valid {
		info.Scopes = scopes.String
	}
	if err != nil {
		return nil, fmt.Errorf("invalid api key")
	}

	if info.Status != "active" {
		return nil, fmt.Errorf("tenant is %s", info.Status)
	}

	return &info, nil
}

func (r *sqliteAPIKeyRepo) TouchLastUsed(ctx context.Context, keyHash string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = datetime('now') WHERE key_hash = ?`, keyHash)
	return err
}

func (r *sqliteAPIKeyRepo) ListByTenant(ctx context.Context, tenantID string) ([]*APIKey, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, key_prefix, name, status, created_at, last_used_at, expires_at
		 FROM api_keys WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

func (r *sqliteAPIKeyRepo) ListExpiringSoon(ctx context.Context, tenantID string, withinDays int) ([]*APIKey, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, key_prefix, name, status, created_at, last_used_at, expires_at
		 FROM api_keys
		 WHERE tenant_id = ? AND status = 'active'
		   AND expires_at IS NOT NULL
		   AND datetime(expires_at) > datetime('now')
		   AND datetime(expires_at) <= datetime('now', ? || ' days')
		 ORDER BY expires_at`, tenantID, fmt.Sprintf("+%d", withinDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

func scanAPIKeys(rows *sql.Rows) ([]*APIKey, error) {
	var keys []*APIKey
	for rows.Next() {
		k := &APIKey{}
		var createdAt string
		var lastUsed, expiresAt sql.NullString
		if err := rows.Scan(&k.ID, &k.TenantID, &k.KeyPrefix, &k.Name,
			&k.Status, &createdAt, &lastUsed, &expiresAt); err != nil {
			return nil, err
		}
		k.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if lastUsed.Valid {
			t, _ := time.Parse(time.RFC3339, lastUsed.String)
			k.LastUsedAt = &t
		}
		if expiresAt.Valid {
			t, _ := time.Parse(time.RFC3339, expiresAt.String)
			k.ExpiresAt = &t
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *sqliteAPIKeyRepo) Revoke(ctx context.Context, keyID string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET status = 'revoked' WHERE id = ? AND status = 'active'`, keyID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("api key not found or already revoked")
	}
	return nil
}

// --- User Repo ---

type sqliteUserRepo struct{ db *sql.DB }

func (r *sqliteUserRepo) Create(ctx context.Context, u *User) error {
	if u.ID == "" {
		u.ID = uuid.New().String()
	}

	// Hash the password if it's not already hashed (starts with $2a$ or $2b$)
	if len(u.PasswordHash) < 4 || (u.PasswordHash[:4] != "$2a$" && u.PasswordHash[:4] != "$2b$") {
		hash, err := bcrypt.GenerateFromPassword([]byte(u.PasswordHash), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		u.PasswordHash = string(hash)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if u.Status == "" {
		u.Status = "active"
	}
	emailVerified := 0
	if u.EmailVerified {
		emailVerified = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, role, email_verified, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.TenantID, u.Email, u.PasswordHash, u.Role, emailVerified, u.Status, now,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func scanUser(u *User, createdAt string, emailVerified int, lastLoginAt *string) {
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.EmailVerified = emailVerified != 0
	if lastLoginAt != nil && *lastLoginAt != "" {
		t, _ := time.Parse(time.RFC3339, *lastLoginAt)
		u.LastLoginAt = &t
	}
}

func (r *sqliteUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	var createdAt string
	var emailVerified int
	var lastLoginAt *string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, email_verified, status, last_login_at, created_at
		 FROM users WHERE email = ?`, email,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &emailVerified, &u.Status, &lastLoginAt, &createdAt)
	if err != nil {
		return nil, err
	}
	scanUser(u, createdAt, emailVerified, lastLoginAt)
	return u, nil
}

func (r *sqliteUserRepo) GetByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	var createdAt string
	var emailVerified int
	var lastLoginAt *string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, email_verified, status, last_login_at, created_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &emailVerified, &u.Status, &lastLoginAt, &createdAt)
	if err != nil {
		return nil, err
	}
	scanUser(u, createdAt, emailVerified, lastLoginAt)
	return u, nil
}

func (r *sqliteUserRepo) ListByTenant(ctx context.Context, tenantID string) ([]*User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, email_verified, status, last_login_at, created_at
		 FROM users WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		var createdAt string
		var emailVerified int
		var lastLoginAt *string
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &emailVerified, &u.Status, &lastLoginAt, &createdAt); err != nil {
			return nil, err
		}
		scanUser(u, createdAt, emailVerified, lastLoginAt)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *sqliteUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return err
}

func (r *sqliteUserRepo) UpdateEmailVerified(ctx context.Context, userID string, verified bool) error {
	v := 0
	if verified {
		v = 1
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET email_verified = ? WHERE id = ?`, v, userID)
	return err
}

func (r *sqliteUserRepo) UpdateLastLogin(ctx context.Context, userID string, t time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`, t.UTC().Format(time.RFC3339), userID)
	return err
}

func (r *sqliteUserRepo) UpdateRole(ctx context.Context, userID, role string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET role = ? WHERE id = ?`, role, userID)
	return err
}

func (r *sqliteUserRepo) UpdateStatus(ctx context.Context, userID, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET status = ? WHERE id = ?`, status, userID)
	return err
}

func (r *sqliteUserRepo) UpdateTwoFactor(ctx context.Context, userID string, enabled bool, secret string) error {
	ev := 0
	if enabled {
		ev = 1
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET two_factor_enabled = ?, two_factor_secret = ? WHERE id = ?`, ev, secret, userID)
	return err
}

func (r *sqliteUserRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE tenant_id = ? AND status = 'active'`, tenantID).Scan(&count)
	return count, err
}

// --- Invitation Repo ---

type sqliteInvitationRepo struct{ db *sql.DB }

func (r *sqliteInvitationRepo) Create(ctx context.Context, inv *Invitation) error {
	if inv.ID == "" {
		inv.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO invitations (id, tenant_id, email, role, token_hash, status, invited_by, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, inv.TenantID, inv.Email, inv.Role, inv.TokenHash, inv.Status, inv.InvitedBy,
		inv.CreatedAt.UTC().Format(time.RFC3339), inv.ExpiresAt.UTC().Format(time.RFC3339))
	return err
}

func (r *sqliteInvitationRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error) {
	var inv Invitation
	var createdAt, expiresAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, role, token_hash, status, invited_by, created_at, expires_at
		 FROM invitations WHERE token_hash = ?`, tokenHash).Scan(
		&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.TokenHash, &inv.Status, &inv.InvitedBy, &createdAt, &expiresAt)
	if err != nil {
		return nil, err
	}
	inv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	inv.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	return &inv, nil
}

func (r *sqliteInvitationRepo) ListByTenant(ctx context.Context, tenantID string) ([]*Invitation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, email, role, status, invited_by, created_at, expires_at
		 FROM invitations WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invitations []*Invitation
	for rows.Next() {
		var inv Invitation
		var createdAt, expiresAt string
		if err := rows.Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Status, &inv.InvitedBy, &createdAt, &expiresAt); err != nil {
			return nil, err
		}
		inv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		inv.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
		invitations = append(invitations, &inv)
	}
	return invitations, nil
}

func (r *sqliteInvitationRepo) MarkAccepted(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE invitations SET status = 'accepted' WHERE id = ?`, id)
	return err
}

func (r *sqliteInvitationRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM invitations WHERE status = 'pending' AND expires_at < datetime('now')`)
	return err
}

// --- Consent Record Repo ---

type sqliteConsentRecordRepo struct{ db *sql.DB }

func (r *sqliteConsentRecordRepo) Create(ctx context.Context, cr *ConsentRecord) error {
	if cr.ID == "" {
		cr.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO consent_records (id, tenant_id, user_id, type, version, accepted, ip_address, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cr.ID, cr.TenantID, cr.UserID, cr.Type, cr.Version,
		boolToInt(cr.Accepted), cr.IPAddress, cr.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *sqliteConsentRecordRepo) ListByUser(ctx context.Context, userID string) ([]*ConsentRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, user_id, type, version, accepted, ip_address, created_at
		 FROM consent_records WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []*ConsentRecord
	for rows.Next() {
		var cr ConsentRecord
		var accepted int
		var createdAt string
		if err := rows.Scan(&cr.ID, &cr.TenantID, &cr.UserID, &cr.Type, &cr.Version,
			&accepted, &cr.IPAddress, &createdAt); err != nil {
			return nil, err
		}
		cr.Accepted = accepted != 0
		cr.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		records = append(records, &cr)
	}
	return records, nil
}

func (r *sqliteConsentRecordRepo) HasAccepted(ctx context.Context, userID, consentType, version string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM consent_records WHERE user_id = ? AND type = ? AND version = ? AND accepted = 1`,
		userID, consentType, version).Scan(&count)
	return count > 0, err
}

// --- Deletion Request Repo ---

type sqliteDeletionRequestRepo struct{ db *sql.DB }

func (r *sqliteDeletionRequestRepo) Create(ctx context.Context, dr *DeletionRequest) error {
	if dr.ID == "" {
		dr.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO deletion_requests (id, tenant_id, requested_by, status, requested_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		dr.ID, dr.TenantID, dr.RequestedBy, dr.Status,
		dr.RequestedAt.UTC().Format(time.RFC3339), dr.ExpiresAt.UTC().Format(time.RFC3339))
	return err
}

func (r *sqliteDeletionRequestRepo) GetByTenantID(ctx context.Context, tenantID string) (*DeletionRequest, error) {
	var dr DeletionRequest
	var requestedAt, expiresAt string
	var completedAt sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, requested_by, status, requested_at, expires_at, completed_at
		 FROM deletion_requests WHERE tenant_id = ? AND status = 'pending' ORDER BY requested_at DESC LIMIT 1`,
		tenantID).Scan(&dr.ID, &dr.TenantID, &dr.RequestedBy, &dr.Status, &requestedAt, &expiresAt, &completedAt)
	if err != nil {
		return nil, err
	}
	dr.RequestedAt, _ = time.Parse(time.RFC3339, requestedAt)
	dr.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339, completedAt.String)
		dr.CompletedAt = &t
	}
	return &dr, nil
}

func (r *sqliteDeletionRequestRepo) Cancel(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deletion_requests SET status = 'cancelled' WHERE id = ?`, id)
	return err
}

func (r *sqliteDeletionRequestRepo) Complete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deletion_requests SET status = 'completed', completed_at = datetime('now') WHERE id = ?`, id)
	return err
}

func (r *sqliteDeletionRequestRepo) ListPending(ctx context.Context) ([]*DeletionRequest, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, requested_by, status, requested_at, expires_at
		 FROM deletion_requests WHERE status = 'pending' AND expires_at <= datetime('now')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []*DeletionRequest
	for rows.Next() {
		var dr DeletionRequest
		var requestedAt, expiresAt string
		if err := rows.Scan(&dr.ID, &dr.TenantID, &dr.RequestedBy, &dr.Status, &requestedAt, &expiresAt); err != nil {
			return nil, err
		}
		dr.RequestedAt, _ = time.Parse(time.RFC3339, requestedAt)
		dr.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
		requests = append(requests, &dr)
	}
	return requests, nil
}

// --- Event Counter Repo ---

type sqliteEventCounterRepo struct{ db *sql.DB }

func (r *sqliteEventCounterRepo) Increment(ctx context.Context, tenantID, day string, delta int64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO event_counters (tenant_id, day, count) VALUES (?, ?, ?)
		 ON CONFLICT(tenant_id, day) DO UPDATE SET count = count + ?`,
		tenantID, day, delta, delta,
	)
	return err
}

func (r *sqliteEventCounterRepo) GetCount(ctx context.Context, tenantID, day string) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		`SELECT count FROM event_counters WHERE tenant_id = ? AND day = ?`,
		tenantID, day,
	).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}

// --- Notification Channel Repo ---

type sqliteNotificationChannelRepo struct{ db *sql.DB }

func (r *sqliteNotificationChannelRepo) Create(ctx context.Context, c *NotificationChannel) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if c.Status == "" {
		c.Status = "active"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notification_channels (id, tenant_id, name, type, config, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.TenantID, c.Name, c.Type, c.Config, c.Status, now, now,
	)
	if err != nil {
		return fmt.Errorf("create notification channel: %w", err)
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, now)
	c.UpdatedAt = c.CreatedAt
	return nil
}

func (r *sqliteNotificationChannelRepo) GetByID(ctx context.Context, id string) (*NotificationChannel, error) {
	c := &NotificationChannel{}
	var createdAt, updatedAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, type, config, status, created_at, updated_at
		 FROM notification_channels WHERE id = ?`, id,
	).Scan(&c.ID, &c.TenantID, &c.Name, &c.Type, &c.Config, &c.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return c, nil
}

func (r *sqliteNotificationChannelRepo) Update(ctx context.Context, c *NotificationChannel) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx,
		`UPDATE notification_channels SET name = ?, type = ?, config = ?, status = ?, updated_at = ?
		 WHERE id = ?`,
		c.Name, c.Type, c.Config, c.Status, now, c.ID,
	)
	if err != nil {
		return fmt.Errorf("update notification channel: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("notification channel not found")
	}
	c.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *sqliteNotificationChannelRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("notification channel not found")
	}
	return nil
}

func (r *sqliteNotificationChannelRepo) ListByTenant(ctx context.Context, tenantID string) ([]*NotificationChannel, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, name, type, config, status, created_at, updated_at
		 FROM notification_channels WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*NotificationChannel
	for rows.Next() {
		c := &NotificationChannel{}
		var createdAt, updatedAt string
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Type, &c.Config, &c.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

// --- Alert Rule Repo ---

type sqliteAlertRuleRepo struct{ db *sql.DB }

func (r *sqliteAlertRuleRepo) Create(ctx context.Context, ar *AlertRule) error {
	if ar.ID == "" {
		ar.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if ar.Status == "" {
		ar.Status = "active"
	}
	if ar.WindowMinutes == 0 {
		ar.WindowMinutes = 5
	}
	if ar.CooldownMinutes == 0 {
		ar.CooldownMinutes = 15
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO alert_rules (id, tenant_id, name, metric, operator, threshold,
		 window_minutes, service, path_template, channel_id, cooldown_minutes, status,
		 created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ar.ID, ar.TenantID, ar.Name, ar.Metric, ar.Operator, ar.Threshold,
		ar.WindowMinutes, ar.Service, ar.PathTemplate, ar.ChannelID, ar.CooldownMinutes,
		ar.Status, now, now,
	)
	if err != nil {
		return fmt.Errorf("create alert rule: %w", err)
	}
	ar.CreatedAt, _ = time.Parse(time.RFC3339, now)
	ar.UpdatedAt = ar.CreatedAt
	return nil
}

func (r *sqliteAlertRuleRepo) GetByID(ctx context.Context, id string) (*AlertRule, error) {
	ar := &AlertRule{}
	var createdAt, updatedAt string
	var lastTriggered sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, metric, operator, threshold,
		        window_minutes, service, path_template, channel_id, cooldown_minutes,
		        status, last_triggered_at, created_at, updated_at
		 FROM alert_rules WHERE id = ?`, id,
	).Scan(&ar.ID, &ar.TenantID, &ar.Name, &ar.Metric, &ar.Operator, &ar.Threshold,
		&ar.WindowMinutes, &ar.Service, &ar.PathTemplate, &ar.ChannelID, &ar.CooldownMinutes,
		&ar.Status, &lastTriggered, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ar.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ar.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastTriggered.Valid {
		t, _ := time.Parse(time.RFC3339, lastTriggered.String)
		ar.LastTriggeredAt = &t
	}
	return ar, nil
}

func (r *sqliteAlertRuleRepo) Update(ctx context.Context, ar *AlertRule) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx,
		`UPDATE alert_rules SET name = ?, metric = ?, operator = ?, threshold = ?,
		 window_minutes = ?, service = ?, path_template = ?, channel_id = ?,
		 cooldown_minutes = ?, status = ?, updated_at = ?
		 WHERE id = ?`,
		ar.Name, ar.Metric, ar.Operator, ar.Threshold,
		ar.WindowMinutes, ar.Service, ar.PathTemplate, ar.ChannelID,
		ar.CooldownMinutes, ar.Status, now, ar.ID,
	)
	if err != nil {
		return fmt.Errorf("update alert rule: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("alert rule not found")
	}
	ar.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *sqliteAlertRuleRepo) Delete(ctx context.Context, id string) error {
	// Delete associated history first (FK constraint)
	if _, err := r.db.ExecContext(ctx, `DELETE FROM alert_history WHERE rule_id = ?`, id); err != nil {
		return fmt.Errorf("delete alert history for rule: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete alert rule: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("alert rule not found")
	}
	return nil
}

func (r *sqliteAlertRuleRepo) ListByTenant(ctx context.Context, tenantID string) ([]*AlertRule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, name, metric, operator, threshold,
		        window_minutes, service, path_template, channel_id, cooldown_minutes,
		        status, last_triggered_at, created_at, updated_at
		 FROM alert_rules WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAlertRules(rows)
}

func (r *sqliteAlertRuleRepo) ListActive(ctx context.Context) ([]*AlertRule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT ar.id, ar.tenant_id, ar.name, ar.metric, ar.operator, ar.threshold,
		        ar.window_minutes, ar.service, ar.path_template, ar.channel_id, ar.cooldown_minutes,
		        ar.status, ar.last_triggered_at, ar.created_at, ar.updated_at
		 FROM alert_rules ar
		 JOIN tenants t ON ar.tenant_id = t.id
		 WHERE ar.status = 'active' AND t.status = 'active'
		 ORDER BY ar.tenant_id, ar.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAlertRules(rows)
}

func (r *sqliteAlertRuleRepo) UpdateLastTriggered(ctx context.Context, id string, t time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE alert_rules SET last_triggered_at = ? WHERE id = ?`,
		t.UTC().Format(time.RFC3339), id)
	return err
}

func (r *sqliteAlertRuleRepo) scanAlertRules(rows *sql.Rows) ([]*AlertRule, error) {
	var rules []*AlertRule
	for rows.Next() {
		ar := &AlertRule{}
		var createdAt, updatedAt string
		var lastTriggered sql.NullString
		if err := rows.Scan(&ar.ID, &ar.TenantID, &ar.Name, &ar.Metric, &ar.Operator, &ar.Threshold,
			&ar.WindowMinutes, &ar.Service, &ar.PathTemplate, &ar.ChannelID, &ar.CooldownMinutes,
			&ar.Status, &lastTriggered, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		ar.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		ar.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastTriggered.Valid {
			t, _ := time.Parse(time.RFC3339, lastTriggered.String)
			ar.LastTriggeredAt = &t
		}
		rules = append(rules, ar)
	}
	return rules, rows.Err()
}

// --- Alert History Repo ---

type sqliteAlertHistoryRepo struct{ db *sql.DB }

func (r *sqliteAlertHistoryRepo) Create(ctx context.Context, e *AlertHistoryEntry) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO alert_history (id, rule_id, tenant_id, metric, threshold, actual_value,
		 service, path_template, status, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.RuleID, e.TenantID, e.Metric, e.Threshold, e.ActualValue,
		e.Service, e.PathTemplate, e.Status, e.Message, now,
	)
	if err != nil {
		return fmt.Errorf("create alert history: %w", err)
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *sqliteAlertHistoryRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*AlertHistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, rule_id, tenant_id, metric, threshold, actual_value,
		        service, path_template, status, message, created_at
		 FROM alert_history WHERE tenant_id = ?
		 ORDER BY created_at DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAlertHistory(rows)
}

func (r *sqliteAlertHistoryRepo) ListByRule(ctx context.Context, ruleID string, limit int) ([]*AlertHistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, rule_id, tenant_id, metric, threshold, actual_value,
		        service, path_template, status, message, created_at
		 FROM alert_history WHERE rule_id = ?
		 ORDER BY created_at DESC LIMIT ?`, ruleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAlertHistory(rows)
}

func (r *sqliteAlertHistoryRepo) scanAlertHistory(rows *sql.Rows) ([]*AlertHistoryEntry, error) {
	var entries []*AlertHistoryEntry
	for rows.Next() {
		e := &AlertHistoryEntry{}
		var createdAt string
		if err := rows.Scan(&e.ID, &e.RuleID, &e.TenantID, &e.Metric, &e.Threshold,
			&e.ActualValue, &e.Service, &e.PathTemplate, &e.Status, &e.Message, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- Audit Log Repo ---

type sqliteAuditRepo struct{ db *sql.DB }

func (r *sqliteAuditRepo) Log(ctx context.Context, entry *AuditEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_logs (id, tenant_id, user_id, action, resource, resource_id, detail, ip_address, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.TenantID, entry.UserID, entry.Action,
		entry.Resource, entry.ResourceID, entry.Detail, entry.IPAddress, now,
	)
	if err == nil {
		entry.CreatedAt, _ = time.Parse(time.RFC3339, now)
	}
	return err
}

func (r *sqliteAuditRepo) ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*AuditEntry, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// Get total count
	var total int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ?`, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, user_id, action, resource, resource_id, detail, ip_address, created_at
		 FROM audit_logs WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		e := &AuditEntry{}
		var createdAt string
		if err := rows.Scan(&e.ID, &e.TenantID, &e.UserID, &e.Action,
			&e.Resource, &e.ResourceID, &e.Detail, &e.IPAddress, &createdAt); err != nil {
			return nil, 0, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// --- Monthly Usage Repo ---

type sqliteMonthlyUsageRepo struct{ db *sql.DB }

func (r *sqliteMonthlyUsageRepo) Snapshot(ctx context.Context, tenantID, month string, count int64, plan string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO monthly_usage (tenant_id, month, count, plan, snapped_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(tenant_id, month) DO UPDATE SET count = ?, plan = ?, snapped_at = datetime('now')`,
		tenantID, month, count, plan, count, plan)
	return err
}

func (r *sqliteMonthlyUsageRepo) GetByTenant(ctx context.Context, tenantID string, limit int) ([]*MonthlyUsage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT tenant_id, month, count, plan, snapped_at
		 FROM monthly_usage WHERE tenant_id = ? ORDER BY month DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*MonthlyUsage
	for rows.Next() {
		m := &MonthlyUsage{}
		var snappedAt string
		if err := rows.Scan(&m.TenantID, &m.Month, &m.Count, &m.Plan, &snappedAt); err != nil {
			return nil, err
		}
		m.SnappedAt, _ = time.Parse(time.RFC3339, snappedAt)
		result = append(result, m)
	}
	return result, rows.Err()
}

// --- Retention Policy Repo ---

type sqliteRetentionPolicyRepo struct{ db *sql.DB }

func (r *sqliteRetentionPolicyRepo) Upsert(ctx context.Context, p *RetentionPolicy) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO retention_policies (tenant_id, facts_days, metrics_days, traces_days, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id) DO UPDATE SET
		   facts_days = excluded.facts_days,
		   metrics_days = excluded.metrics_days,
		   traces_days = excluded.traces_days,
		   updated_at = excluded.updated_at`,
		p.TenantID, p.FactsDays, p.MetricsDays, p.TracesDays, now,
	)
	if err != nil {
		return fmt.Errorf("upsert retention policy: %w", err)
	}
	p.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *sqliteRetentionPolicyRepo) GetByTenantID(ctx context.Context, tenantID string) (*RetentionPolicy, error) {
	p := &RetentionPolicy{}
	var updatedAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT tenant_id, facts_days, metrics_days, traces_days, updated_at
		 FROM retention_policies WHERE tenant_id = ?`, tenantID,
	).Scan(&p.TenantID, &p.FactsDays, &p.MetricsDays, &p.TracesDays, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get retention policy: %w", err)
	}
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return p, nil
}

// --- Password Reset Token Repo ---

type sqlitePasswordResetRepo struct{ db *sql.DB }

func (r *sqlitePasswordResetRepo) Create(ctx context.Context, token *PasswordResetToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	token.CreatedAt, _ = time.Parse(time.RFC3339, now)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt.UTC().Format(time.RFC3339), now,
	)
	if err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}
	return nil
}

func (r *sqlitePasswordResetRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error) {
	t := &PasswordResetToken{}
	var expiresAt, createdAt string
	var usedAt *string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		 FROM password_reset_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &expiresAt, &usedAt, &createdAt)
	if err != nil {
		return nil, err
	}
	t.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if usedAt != nil && *usedAt != "" {
		u, _ := time.Parse(time.RFC3339, *usedAt)
		t.UsedAt = &u
	}
	return t, nil
}

func (r *sqlitePasswordResetRepo) MarkUsed(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ? WHERE id = ?`, now, id)
	return err
}

func (r *sqlitePasswordResetRepo) DeleteExpired(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE expires_at < ?`, now)
	return err
}

// --- Email Verification Token Repo ---

type sqliteEmailVerificationRepo struct{ db *sql.DB }

func (r *sqliteEmailVerificationRepo) Create(ctx context.Context, token *EmailVerificationToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	token.CreatedAt, _ = time.Parse(time.RFC3339, now)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO email_verification_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt.UTC().Format(time.RFC3339), now,
	)
	if err != nil {
		return fmt.Errorf("create email verification token: %w", err)
	}
	return nil
}

func (r *sqliteEmailVerificationRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*EmailVerificationToken, error) {
	t := &EmailVerificationToken{}
	var expiresAt, createdAt string
	var verifiedAt *string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, verified_at, created_at
		 FROM email_verification_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &expiresAt, &verifiedAt, &createdAt)
	if err != nil {
		return nil, err
	}
	t.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if verifiedAt != nil && *verifiedAt != "" {
		v, _ := time.Parse(time.RFC3339, *verifiedAt)
		t.VerifiedAt = &v
	}
	return t, nil
}

func (r *sqliteEmailVerificationRepo) MarkVerified(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`UPDATE email_verification_tokens SET verified_at = ? WHERE id = ?`, now, id)
	return err
}

func (r *sqliteEmailVerificationRepo) DeleteExpired(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE expires_at < ?`, now)
	return err
}

// --- SSO Config Repo ---

type sqliteSSOConfigRepo struct{ db *sql.DB }

func (r *sqliteSSOConfigRepo) Upsert(ctx context.Context, cfg *SSOConfig) error {
	if cfg.ID == "" {
		cfg.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sso_configs (id, tenant_id, provider, enabled, entity_id, sso_url, certificate, client_id, client_secret, issuer, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id) DO UPDATE SET
			provider = excluded.provider,
			enabled = excluded.enabled,
			entity_id = excluded.entity_id,
			sso_url = excluded.sso_url,
			certificate = excluded.certificate,
			client_id = excluded.client_id,
			client_secret = excluded.client_secret,
			issuer = excluded.issuer,
			updated_at = excluded.updated_at`,
		cfg.ID, cfg.TenantID, cfg.Provider, enabled, cfg.EntityID, cfg.SSOURL, cfg.Certificate,
		cfg.ClientID, cfg.ClientSecret, cfg.Issuer, now, now)
	return err
}

func (r *sqliteSSOConfigRepo) GetByTenantID(ctx context.Context, tenantID string) (*SSOConfig, error) {
	cfg := &SSOConfig{}
	var enabled int
	var createdAt, updatedAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, provider, enabled, entity_id, sso_url, certificate, client_id, client_secret, issuer, created_at, updated_at
		 FROM sso_configs WHERE tenant_id = ?`, tenantID,
	).Scan(&cfg.ID, &cfg.TenantID, &cfg.Provider, &enabled, &cfg.EntityID, &cfg.SSOURL, &cfg.Certificate,
		&cfg.ClientID, &cfg.ClientSecret, &cfg.Issuer, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	cfg.Enabled = enabled != 0
	cfg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	cfg.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return cfg, nil
}

func (r *sqliteSSOConfigRepo) Delete(ctx context.Context, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sso_configs WHERE tenant_id = ?`, tenantID)
	return err
}

// --- Session Repo ---

type sqliteSessionRepo struct{ db *sql.DB }

func (r *sqliteSessionRepo) Create(ctx context.Context, s *Session) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, tenant_id, ip_address, user_agent, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.TenantID, s.IPAddress, s.UserAgent,
		s.CreatedAt.UTC().Format(time.RFC3339), s.ExpiresAt.UTC().Format(time.RFC3339))
	return err
}

func (r *sqliteSessionRepo) GetByID(ctx context.Context, id string) (*Session, error) {
	s := &Session{}
	var createdAt, expiresAt string
	var revokedAt sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, tenant_id, ip_address, user_agent, created_at, expires_at, revoked_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.UserID, &s.TenantID, &s.IPAddress, &s.UserAgent, &createdAt, &expiresAt, &revokedAt)
	if err != nil {
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	s.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	if revokedAt.Valid {
		t, _ := time.Parse(time.RFC3339, revokedAt.String)
		s.RevokedAt = &t
	}
	return s, nil
}

func (r *sqliteSessionRepo) ListByUser(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, tenant_id, ip_address, user_agent, created_at, expires_at, revoked_at
		 FROM sessions WHERE user_id = ? AND revoked_at IS NULL AND datetime(expires_at) > datetime('now')
		 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		s := &Session{}
		var createdAt, expiresAt string
		var revokedAt sql.NullString
		if err := rows.Scan(&s.ID, &s.UserID, &s.TenantID, &s.IPAddress, &s.UserAgent,
			&createdAt, &expiresAt, &revokedAt); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		s.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
		if revokedAt.Valid {
			t, _ := time.Parse(time.RFC3339, revokedAt.String)
			s.RevokedAt = &t
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (r *sqliteSessionRepo) Revoke(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE id = ?`, now, id)
	return err
}

func (r *sqliteSessionRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID)
	return err
}

func (r *sqliteSessionRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE datetime(expires_at) < datetime('now')`)
	return err
}

// --- Recovery Code Repo ---

type sqliteRecoveryCodeRepo struct{ db *sql.DB }

func (r *sqliteRecoveryCodeRepo) Store(ctx context.Context, userID string, codeHashes []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing codes for this user
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}

	for _, hash := range codeHashes {
		id := uuid.New().String()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (id, user_id, code_hash) VALUES (?, ?, ?)`,
			id, userID, hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *sqliteRecoveryCodeRepo) Validate(ctx context.Context, userID, codeHash string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE recovery_codes SET used = 1 WHERE user_id = ? AND code_hash = ? AND used = 0`,
		userID, codeHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *sqliteRecoveryCodeRepo) DeleteByUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID)
	return err
}

// --- Revoked Token Repo ---

type sqliteRevokedTokenRepo struct{ db *sql.DB }

func (r *sqliteRevokedTokenRepo) Revoke(ctx context.Context, jti string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO revoked_tokens (jti, expires_at) VALUES (?, ?)`,
		jti, expiresAt.UTC().Format(time.RFC3339))
	return err
}

func (r *sqliteRevokedTokenRepo) IsRevoked(ctx context.Context, jti string) bool {
	var exp string
	err := r.db.QueryRowContext(ctx,
		`SELECT expires_at FROM revoked_tokens WHERE jti = ?`, jti).Scan(&exp)
	if err != nil {
		return false
	}
	// Check if the token has already expired — if so, it's no longer revoked (it's just dead)
	t, _ := time.Parse(time.RFC3339, exp)
	if !t.IsZero() && time.Now().UTC().After(t) {
		// Opportunistic cleanup
		_, _ = r.db.ExecContext(ctx, `DELETE FROM revoked_tokens WHERE jti = ?`, jti)
		return false
	}
	return true
}

func (r *sqliteRevokedTokenRepo) Cleanup(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM revoked_tokens WHERE datetime(expires_at) < datetime('now')`)
	return err
}

// --- SSO State Repo ---

type sqliteSSOStateRepo struct{ db *sql.DB }

func (r *sqliteSSOStateRepo) Store(ctx context.Context, state, tenantID string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sso_states (state, tenant_id, expires_at) VALUES (?, ?, ?)`,
		state, tenantID, expiresAt.UTC().Format(time.RFC3339))
	return err
}

func (r *sqliteSSOStateRepo) ValidateAndDelete(ctx context.Context, state string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var tenantID, expiresAt string
	err = tx.QueryRowContext(ctx,
		`SELECT tenant_id, expires_at FROM sso_states WHERE state = ?`, state).Scan(&tenantID, &expiresAt)
	if err != nil {
		return "", fmt.Errorf("invalid or expired SSO state")
	}

	// Delete the state (one-time use)
	tx.ExecContext(ctx, `DELETE FROM sso_states WHERE state = ?`, state)

	// Check expiry
	t, _ := time.Parse(time.RFC3339, expiresAt)
	if !t.IsZero() && time.Now().UTC().After(t) {
		tx.Commit()
		return "", fmt.Errorf("SSO state expired")
	}

	return tenantID, tx.Commit()
}

func (r *sqliteSSOStateRepo) Cleanup(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM sso_states WHERE datetime(expires_at) < datetime('now')`)
	return err
}
