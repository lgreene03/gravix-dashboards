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

	// Connection pool tuning: SQLite serializes writes, so a single
	// connection avoids SQLITE_BUSY contention while WAL mode still
	// allows concurrent reads from the same connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // no lifetime limit for single-conn pool

	// Run schema migrations (idempotent)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("run schema: %w", err)
	}

	// Incremental migrations for existing databases
	migrations := []string{
		`ALTER TABLE api_keys ADD COLUMN expires_at TEXT`,
	}
	for _, m := range migrations {
		// Ignore "duplicate column" errors from re-running migrations
		db.Exec(m)
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
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tenants (id, name, email, plan, status, overage_allowed, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Email, t.Plan, t.Status, overageInt, now, now,
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
		        status, overage_allowed, created_at, updated_at FROM tenants WHERE id = ?`, id))
}

func (r *sqliteTenantRepo) GetByEmail(ctx context.Context, email string) (*Tenant, error) {
	return r.scanTenant(r.db.QueryRowContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, overage_allowed, created_at, updated_at FROM tenants WHERE email = ?`, email))
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

func (r *sqliteTenantRepo) List(ctx context.Context) ([]*Tenant, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, overage_allowed, created_at, updated_at FROM tenants ORDER BY created_at`)
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
	var overageInt int
	err := row.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
		&t.StripeCustomerID, &t.StripeSubscriptionID,
		&t.Status, &overageInt, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	t.OverageAllowed = overageInt != 0
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
	var overageInt int
	err := row.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
		&t.StripeCustomerID, &t.StripeSubscriptionID,
		&t.Status, &overageInt, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	t.OverageAllowed = overageInt != 0
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
	err := r.db.QueryRowContext(ctx,
		`SELECT t.id, t.plan, t.status, t.overage_allowed
		 FROM api_keys k JOIN tenants t ON k.tenant_id = t.id
		 WHERE k.key_hash = ? AND k.status = 'active'
		   AND (k.expires_at IS NULL OR datetime(k.expires_at) > datetime('now'))`,
		hash,
	).Scan(&info.TenantID, &info.Plan, &info.Status, &overageInt)
	info.OverageAllowed = overageInt != 0
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
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, role, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.TenantID, u.Email, u.PasswordHash, u.Role, now,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *sqliteUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	var createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, created_at
		 FROM users WHERE email = ?`, email,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return u, nil
}

func (r *sqliteUserRepo) GetByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	var createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, created_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return u, nil
}

func (r *sqliteUserRepo) ListByTenant(ctx context.Context, tenantID string) ([]*User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, created_at
		 FROM users WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		var createdAt string
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &createdAt); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		users = append(users, u)
	}
	return users, rows.Err()
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
