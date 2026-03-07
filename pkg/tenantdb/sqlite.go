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
	last_used_at TEXT
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

	// Run schema migrations (idempotent)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("run schema: %w", err)
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

// --- Tenant Repo ---

type sqliteTenantRepo struct{ db *sql.DB }

func (r *sqliteTenantRepo) Create(ctx context.Context, t *Tenant) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tenants (id, name, email, plan, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Email, t.Plan, t.Status, now, now,
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
		        status, created_at, updated_at FROM tenants WHERE id = ?`, id))
}

func (r *sqliteTenantRepo) GetByEmail(ctx context.Context, email string) (*Tenant, error) {
	return r.scanTenant(r.db.QueryRowContext(ctx,
		`SELECT id, name, email, plan, stripe_customer_id, stripe_subscription_id,
		        status, created_at, updated_at FROM tenants WHERE email = ?`, email))
}

func (r *sqliteTenantRepo) UpdatePlan(ctx context.Context, id, plan string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET plan = ?, updated_at = datetime('now') WHERE id = ?`, plan, id)
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
		        status, created_at, updated_at FROM tenants ORDER BY created_at`)
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
	err := row.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
		&t.StripeCustomerID, &t.StripeSubscriptionID,
		&t.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
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
	err := row.Scan(&t.ID, &t.Name, &t.Email, &t.Plan,
		&t.StripeCustomerID, &t.StripeSubscriptionID,
		&t.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
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

func (r *sqliteAPIKeyRepo) Create(ctx context.Context, tenantID, name string) (string, *APIKey, error) {
	plain, hash, prefix, err := generateKey()
	if err != nil {
		return "", nil, err
	}

	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, tenant_id, key_hash, key_prefix, name, status, created_at)
		 VALUES (?, ?, ?, ?, ?, 'active', ?)`,
		id, tenantID, hash, prefix, name, now,
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
	}
	return plain, key, nil
}

func (r *sqliteAPIKeyRepo) ValidateKey(ctx context.Context, rawKey string) (*APIKeyInfo, error) {
	hash := hashKey(rawKey)

	var info APIKeyInfo
	err := r.db.QueryRowContext(ctx,
		`SELECT t.id, t.plan, t.status
		 FROM api_keys k JOIN tenants t ON k.tenant_id = t.id
		 WHERE k.key_hash = ? AND k.status = 'active'`,
		hash,
	).Scan(&info.TenantID, &info.Plan, &info.Status)
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
		`SELECT id, tenant_id, key_prefix, name, status, created_at, last_used_at
		 FROM api_keys WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		k := &APIKey{}
		var createdAt string
		var lastUsed sql.NullString
		if err := rows.Scan(&k.ID, &k.TenantID, &k.KeyPrefix, &k.Name,
			&k.Status, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		k.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if lastUsed.Valid {
			t, _ := time.Parse(time.RFC3339, lastUsed.String)
			k.LastUsedAt = &t
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
