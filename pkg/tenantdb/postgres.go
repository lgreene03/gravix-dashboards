//go:build postgres || all

package tenantdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/referral"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema_postgres.sql
var postgresSchema string

// PostgresDB implements the DB interface using PostgreSQL.
type PostgresDB struct {
	db                    *sql.DB
	tenants               *pgTenantRepo
	apiKeys               *pgAPIKeyRepo
	users                 *pgUserRepo
	eventCounters         *pgEventCounterRepo
	monthlyUsage          *pgMonthlyUsageRepo
	notificationChannels  *pgNotificationChannelRepo
	alertRules            *pgAlertRuleRepo
	alertHistory          *pgAlertHistoryRepo
	auditLog              *pgAuditRepo
	retentionPolicies     *pgRetentionPolicyRepo
	passwordResets        *pgPasswordResetRepo
	emailVerifications    *pgEmailVerificationRepo
	invitations           *pgInvitationRepo
	consentRecords        *pgConsentRecordRepo
	deletionRequests      *pgDeletionRequestRepo
	ssoConfigs            *pgSSOConfigRepo
	sessions              *pgSessionRepo
	recoveryCodes         *pgRecoveryCodeRepo
	revokedTokens         *pgRevokedTokenRepo
	ssoStates             *pgSSOStateRepo
	referrals             *pgReferralRepo
}

// OpenPostgres opens a PostgreSQL database and initializes the schema.
func OpenPostgres(connStr string) (*PostgresDB, error) {
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	// Connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// Run schema via migration framework
	if err := RunMigrations(db, "postgres"); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	pdb := &PostgresDB{db: db}
	pdb.tenants = &pgTenantRepo{db: db}
	pdb.apiKeys = &pgAPIKeyRepo{db: db}
	pdb.users = &pgUserRepo{db: db}
	pdb.eventCounters = &pgEventCounterRepo{db: db}
	pdb.monthlyUsage = &pgMonthlyUsageRepo{db: db}
	pdb.notificationChannels = &pgNotificationChannelRepo{db: db}
	pdb.alertRules = &pgAlertRuleRepo{db: db}
	pdb.alertHistory = &pgAlertHistoryRepo{db: db}
	pdb.auditLog = &pgAuditRepo{db: db}
	pdb.retentionPolicies = &pgRetentionPolicyRepo{db: db}
	pdb.passwordResets = &pgPasswordResetRepo{db: db}
	pdb.emailVerifications = &pgEmailVerificationRepo{db: db}
	pdb.invitations = &pgInvitationRepo{db: db}
	pdb.consentRecords = &pgConsentRecordRepo{db: db}
	pdb.deletionRequests = &pgDeletionRequestRepo{db: db}
	pdb.ssoConfigs = &pgSSOConfigRepo{db: db}
	pdb.sessions = &pgSessionRepo{db: db}
	pdb.recoveryCodes = &pgRecoveryCodeRepo{db: db}
	pdb.revokedTokens = &pgRevokedTokenRepo{db: db}
	pdb.ssoStates = &pgSSOStateRepo{db: db}
	pdb.referrals = &pgReferralRepo{db: db}
	return pdb, nil
}

func (p *PostgresDB) Tenants() TenantRepo                       { return p.tenants }
func (p *PostgresDB) APIKeys() APIKeyRepo                       { return p.apiKeys }
func (p *PostgresDB) Users() UserRepo                           { return p.users }
func (p *PostgresDB) EventCounters() EventCounterRepo           { return p.eventCounters }
func (p *PostgresDB) MonthlyUsage() MonthlyUsageRepo            { return p.monthlyUsage }
func (p *PostgresDB) NotificationChannels() NotificationChannelRepo { return p.notificationChannels }
func (p *PostgresDB) AlertRules() AlertRuleRepo                 { return p.alertRules }
func (p *PostgresDB) AlertHistory() AlertHistoryRepo             { return p.alertHistory }
func (p *PostgresDB) AuditLog() AuditRepo                       { return p.auditLog }
func (p *PostgresDB) RetentionPolicies() RetentionPolicyRepo     { return p.retentionPolicies }
func (p *PostgresDB) PasswordResets() PasswordResetRepo          { return p.passwordResets }
func (p *PostgresDB) EmailVerifications() EmailVerificationRepo  { return p.emailVerifications }
func (p *PostgresDB) Invitations() InvitationRepo               { return p.invitations }
func (p *PostgresDB) ConsentRecords() ConsentRecordRepo         { return p.consentRecords }
func (p *PostgresDB) DeletionRequests() DeletionRequestRepo     { return p.deletionRequests }
func (p *PostgresDB) SSOConfigs() SSOConfigRepo                 { return p.ssoConfigs }
func (p *PostgresDB) Sessions() SessionRepo                     { return p.sessions }
func (p *PostgresDB) RecoveryCodes() RecoveryCodeRepo            { return p.recoveryCodes }
func (p *PostgresDB) RevokedTokens() RevokedTokenRepo            { return p.revokedTokens }
func (p *PostgresDB) SSOStates() SSOStateRepo                    { return p.ssoStates }
func (p *PostgresDB) Referrals() referral.ReferralRepo            { return p.referrals }
func (p *PostgresDB) Close() error                              { return p.db.Close() }

// --- Tenant Repo ---

type pgTenantRepo struct{ db *sql.DB }

func (r *pgTenantRepo) Create(ctx context.Context, t *Tenant) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tenants (id, name, email, plan, stripe_customer_id, stripe_subscription_id, status, overage_allowed, trial_started_at, trial_ends_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		t.ID, t.Name, t.Email, t.Plan, t.StripeCustomerID, t.StripeSubscriptionID, t.Status, t.OverageAllowed, t.TrialStartedAt, t.TrialEndsAt, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}
	return nil
}

func (r *pgTenantRepo) GetByID(ctx context.Context, id string) (*Tenant, error) {
	t := &Tenant{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, email, plan, COALESCE(stripe_customer_id,''), COALESCE(stripe_subscription_id,''), status, overage_allowed, COALESCE(parent_tenant_id,''), trial_started_at, trial_ends_at, created_at, updated_at FROM tenants WHERE id = $1`, id).
		Scan(&t.ID, &t.Name, &t.Email, &t.Plan, &t.StripeCustomerID, &t.StripeSubscriptionID, &t.Status, &t.OverageAllowed, &t.ParentTenantID, &t.TrialStartedAt, &t.TrialEndsAt, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tenant not found: %s", id)
	}
	return t, err
}

func (r *pgTenantRepo) GetByEmail(ctx context.Context, email string) (*Tenant, error) {
	t := &Tenant{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, email, plan, COALESCE(stripe_customer_id,''), COALESCE(stripe_subscription_id,''), status, overage_allowed, COALESCE(parent_tenant_id,''), trial_started_at, trial_ends_at, created_at, updated_at FROM tenants WHERE email = $1`, email).
		Scan(&t.ID, &t.Name, &t.Email, &t.Plan, &t.StripeCustomerID, &t.StripeSubscriptionID, &t.Status, &t.OverageAllowed, &t.ParentTenantID, &t.TrialStartedAt, &t.TrialEndsAt, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tenant not found: %s", email)
	}
	return t, err
}

func (r *pgTenantRepo) UpdatePlan(ctx context.Context, id, plan string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE tenants SET plan = $1, updated_at = NOW() WHERE id = $2`, plan, id)
	return err
}

func (r *pgTenantRepo) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE tenants SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	return err
}

func (r *pgTenantRepo) UpdateStripe(ctx context.Context, id, customerID, subscriptionID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET stripe_customer_id = $1, stripe_subscription_id = $2, updated_at = NOW() WHERE id = $3`,
		customerID, subscriptionID, id)
	return err
}

func (r *pgTenantRepo) UpdateTrial(ctx context.Context, id string, trialStart, trialEnd *time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET trial_started_at = $1, trial_ends_at = $2, updated_at = NOW() WHERE id = $3`,
		trialStart, trialEnd, id)
	return err
}

func (r *pgTenantRepo) UpdateParentTenant(ctx context.Context, id, parentTenantID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tenants SET parent_tenant_id = $1, updated_at = NOW() WHERE id = $2`,
		parentTenantID, id)
	return err
}

func (r *pgTenantRepo) ListChildren(ctx context.Context, parentTenantID string) ([]*Tenant, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, email, plan, COALESCE(stripe_customer_id,''), COALESCE(stripe_subscription_id,''), status, overage_allowed, COALESCE(parent_tenant_id,''), trial_started_at, trial_ends_at, created_at, updated_at FROM tenants WHERE parent_tenant_id = $1 ORDER BY created_at`, parentTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Tenant
	for rows.Next() {
		t := &Tenant{}
		if err := rows.Scan(&t.ID, &t.Name, &t.Email, &t.Plan, &t.StripeCustomerID, &t.StripeSubscriptionID, &t.Status, &t.OverageAllowed, &t.ParentTenantID, &t.TrialStartedAt, &t.TrialEndsAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *pgTenantRepo) List(ctx context.Context) ([]*Tenant, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, email, plan, COALESCE(stripe_customer_id,''), COALESCE(stripe_subscription_id,''), status, overage_allowed, COALESCE(parent_tenant_id,''), trial_started_at, trial_ends_at, created_at, updated_at FROM tenants ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Tenant
	for rows.Next() {
		t := &Tenant{}
		if err := rows.Scan(&t.ID, &t.Name, &t.Email, &t.Plan, &t.StripeCustomerID, &t.StripeSubscriptionID, &t.Status, &t.OverageAllowed, &t.ParentTenantID, &t.TrialStartedAt, &t.TrialEndsAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// --- API Key Repo ---

type pgAPIKeyRepo struct{ db *sql.DB }

func (r *pgAPIKeyRepo) Create(ctx context.Context, tenantID, name string, expiresAt *time.Time) (string, *APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	plainKey := "grvx_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(hash[:])
	keyPrefix := plainKey[:13]
	id := uuid.New().String()
	now := time.Now().UTC()

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, tenant_id, key_hash, key_prefix, name, status, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, 'active', $6, $7)`,
		id, tenantID, keyHash, keyPrefix, name, now, expiresAt)
	if err != nil {
		return "", nil, err
	}

	key := &APIKey{ID: id, TenantID: tenantID, KeyPrefix: keyPrefix, Name: name, Status: "active", CreatedAt: now, ExpiresAt: expiresAt}
	return plainKey, key, nil
}

func (r *pgAPIKeyRepo) ValidateKey(ctx context.Context, rawKey string) (*APIKeyInfo, error) {
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	var info APIKeyInfo
	var keyStatus string
	var expiresAt sql.NullTime
	var scopes sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT k.status, k.expires_at, k.scopes, t.id, t.plan, t.status, t.overage_allowed
		 FROM api_keys k JOIN tenants t ON k.tenant_id = t.id
		 WHERE k.key_hash = $1`, keyHash).
		Scan(&keyStatus, &expiresAt, &scopes, &info.TenantID, &info.Plan, &info.Status, &info.OverageAllowed)
	if scopes.Valid {
		info.Scopes = scopes.String
	}
	if err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	if keyStatus != "active" {
		return nil, fmt.Errorf("API key is revoked")
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return nil, fmt.Errorf("API key has expired")
	}
	if info.Status != "active" {
		return nil, fmt.Errorf("tenant is %s", info.Status)
	}

	go r.TouchLastUsed(context.Background(), keyHash)
	return &info, nil
}

func (r *pgAPIKeyRepo) TouchLastUsed(ctx context.Context, keyHash string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE key_hash = $1`, keyHash)
	return err
}

func (r *pgAPIKeyRepo) ListByTenant(ctx context.Context, tenantID string) ([]*APIKey, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, key_prefix, name, status, created_at, last_used_at, expires_at FROM api_keys WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []*APIKey
	for rows.Next() {
		k := &APIKey{}
		var lastUsed, expires sql.NullTime
		if err := rows.Scan(&k.ID, &k.TenantID, &k.KeyPrefix, &k.Name, &k.Status, &k.CreatedAt, &lastUsed, &expires); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			k.LastUsedAt = &lastUsed.Time
		}
		if expires.Valid {
			k.ExpiresAt = &expires.Time
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *pgAPIKeyRepo) ListExpiringSoon(ctx context.Context, tenantID string, withinDays int) ([]*APIKey, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, key_prefix, name, status, created_at, last_used_at, expires_at
		 FROM api_keys WHERE tenant_id = $1 AND status = 'active' AND expires_at IS NOT NULL AND expires_at <= NOW() + $2 * INTERVAL '1 day'
		 ORDER BY expires_at`, tenantID, withinDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []*APIKey
	for rows.Next() {
		k := &APIKey{}
		var lastUsed, expires sql.NullTime
		if err := rows.Scan(&k.ID, &k.TenantID, &k.KeyPrefix, &k.Name, &k.Status, &k.CreatedAt, &lastUsed, &expires); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			k.LastUsedAt = &lastUsed.Time
		}
		if expires.Valid {
			k.ExpiresAt = &expires.Time
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *pgAPIKeyRepo) Revoke(ctx context.Context, keyID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE api_keys SET status = 'revoked' WHERE id = $1`, keyID)
	return err
}

// --- User Repo ---

type pgUserRepo struct{ db *sql.DB }

func (r *pgUserRepo) Create(ctx context.Context, u *User) error {
	if u.ID == "" {
		u.ID = uuid.New().String()
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	if u.Status == "" {
		u.Status = "active"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, role, email_verified, status, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		u.ID, u.TenantID, u.Email, u.PasswordHash, u.Role, u.EmailVerified, u.Status, u.CreatedAt)
	return err
}

func (r *pgUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, email_verified, status, last_login_at, created_at
		 FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.Status, &u.LastLoginAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	return u, err
}

func (r *pgUserRepo) GetByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, email_verified, status, last_login_at, created_at
		 FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.Status, &u.LastLoginAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	return u, err
}

func (r *pgUserRepo) ListByTenant(ctx context.Context, tenantID string) ([]*User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, email, password_hash, role, email_verified, status, last_login_at, created_at
		 FROM users WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.Status, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *pgUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, userID)
	return err
}

func (r *pgUserRepo) UpdateEmailVerified(ctx context.Context, userID string, verified bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET email_verified = $1 WHERE id = $2`, verified, userID)
	return err
}

func (r *pgUserRepo) UpdateLastLogin(ctx context.Context, userID string, t time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = $1 WHERE id = $2`, t.UTC(), userID)
	return err
}

func (r *pgUserRepo) UpdateRole(ctx context.Context, userID, role string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET role = $1 WHERE id = $2`, role, userID)
	return err
}

func (r *pgUserRepo) UpdateStatus(ctx context.Context, userID, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET status = $1 WHERE id = $2`, status, userID)
	return err
}

func (r *pgUserRepo) UpdateTwoFactor(ctx context.Context, userID string, enabled bool, secret string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET two_factor_enabled = $1, two_factor_secret = $2 WHERE id = $3`, enabled, secret, userID)
	return err
}

func (r *pgUserRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND status = 'active'`, tenantID).Scan(&count)
	return count, err
}

// --- Invitation Repo ---

type pgInvitationRepo struct{ db *sql.DB }

func (r *pgInvitationRepo) Create(ctx context.Context, inv *Invitation) error {
	if inv.ID == "" {
		inv.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO invitations (id, tenant_id, email, role, token_hash, status, invited_by, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		inv.ID, inv.TenantID, inv.Email, inv.Role, inv.TokenHash, inv.Status, inv.InvitedBy,
		inv.CreatedAt.UTC(), inv.ExpiresAt.UTC())
	return err
}

func (r *pgInvitationRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error) {
	var inv Invitation
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, role, token_hash, status, invited_by, created_at, expires_at
		 FROM invitations WHERE token_hash = $1`, tokenHash).Scan(
		&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.TokenHash, &inv.Status, &inv.InvitedBy,
		&inv.CreatedAt, &inv.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *pgInvitationRepo) ListByTenant(ctx context.Context, tenantID string) ([]*Invitation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, email, role, status, invited_by, created_at, expires_at
		 FROM invitations WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invitations []*Invitation
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Status, &inv.InvitedBy,
			&inv.CreatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		invitations = append(invitations, &inv)
	}
	return invitations, nil
}

func (r *pgInvitationRepo) MarkAccepted(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE invitations SET status = 'accepted' WHERE id = $1`, id)
	return err
}

func (r *pgInvitationRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM invitations WHERE status = 'pending' AND expires_at < NOW()`)
	return err
}

// --- Consent Record Repo ---

type pgConsentRecordRepo struct{ db *sql.DB }

func (r *pgConsentRecordRepo) Create(ctx context.Context, cr *ConsentRecord) error {
	if cr.ID == "" {
		cr.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO consent_records (id, tenant_id, user_id, type, version, accepted, ip_address, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		cr.ID, cr.TenantID, cr.UserID, cr.Type, cr.Version,
		cr.Accepted, cr.IPAddress, cr.CreatedAt.UTC())
	return err
}

func (r *pgConsentRecordRepo) ListByUser(ctx context.Context, userID string) ([]*ConsentRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, user_id, type, version, accepted, ip_address, created_at
		 FROM consent_records WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []*ConsentRecord
	for rows.Next() {
		var cr ConsentRecord
		if err := rows.Scan(&cr.ID, &cr.TenantID, &cr.UserID, &cr.Type, &cr.Version,
			&cr.Accepted, &cr.IPAddress, &cr.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, &cr)
	}
	return records, nil
}

func (r *pgConsentRecordRepo) HasAccepted(ctx context.Context, userID, consentType, version string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM consent_records WHERE user_id = $1 AND type = $2 AND version = $3 AND accepted = true`,
		userID, consentType, version).Scan(&count)
	return count > 0, err
}

// --- Deletion Request Repo ---

type pgDeletionRequestRepo struct{ db *sql.DB }

func (r *pgDeletionRequestRepo) Create(ctx context.Context, dr *DeletionRequest) error {
	if dr.ID == "" {
		dr.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO deletion_requests (id, tenant_id, requested_by, status, requested_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		dr.ID, dr.TenantID, dr.RequestedBy, dr.Status, dr.RequestedAt.UTC(), dr.ExpiresAt.UTC())
	return err
}

func (r *pgDeletionRequestRepo) GetByTenantID(ctx context.Context, tenantID string) (*DeletionRequest, error) {
	var dr DeletionRequest
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, requested_by, status, requested_at, expires_at, completed_at
		 FROM deletion_requests WHERE tenant_id = $1 AND status = 'pending' ORDER BY requested_at DESC LIMIT 1`,
		tenantID).Scan(&dr.ID, &dr.TenantID, &dr.RequestedBy, &dr.Status, &dr.RequestedAt, &dr.ExpiresAt, &dr.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &dr, nil
}

func (r *pgDeletionRequestRepo) Cancel(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deletion_requests SET status = 'cancelled' WHERE id = $1`, id)
	return err
}

func (r *pgDeletionRequestRepo) Complete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deletion_requests SET status = 'completed', completed_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *pgDeletionRequestRepo) ListPending(ctx context.Context) ([]*DeletionRequest, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, requested_by, status, requested_at, expires_at
		 FROM deletion_requests WHERE status = 'pending' AND expires_at <= NOW()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []*DeletionRequest
	for rows.Next() {
		var dr DeletionRequest
		if err := rows.Scan(&dr.ID, &dr.TenantID, &dr.RequestedBy, &dr.Status, &dr.RequestedAt, &dr.ExpiresAt); err != nil {
			return nil, err
		}
		requests = append(requests, &dr)
	}
	return requests, nil
}

// --- Event Counter Repo ---

type pgEventCounterRepo struct{ db *sql.DB }

func (r *pgEventCounterRepo) Increment(ctx context.Context, tenantID, day string, delta int64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO event_counters (tenant_id, day, count) VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, day) DO UPDATE SET count = event_counters.count + EXCLUDED.count`,
		tenantID, day, delta)
	return err
}

func (r *pgEventCounterRepo) GetCount(ctx context.Context, tenantID, day string) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		`SELECT count FROM event_counters WHERE tenant_id = $1 AND day = $2`, tenantID, day).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}

// --- Monthly Usage Repo ---

type pgMonthlyUsageRepo struct{ db *sql.DB }

func (r *pgMonthlyUsageRepo) Snapshot(ctx context.Context, tenantID, month string, count int64, plan string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO monthly_usage (tenant_id, month, count, plan, snapped_at) VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (tenant_id, month) DO UPDATE SET count = EXCLUDED.count, plan = EXCLUDED.plan, snapped_at = NOW()`,
		tenantID, month, count, plan)
	return err
}

func (r *pgMonthlyUsageRepo) GetByTenant(ctx context.Context, tenantID string, limit int) ([]*MonthlyUsage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT tenant_id, month, count, plan, snapped_at FROM monthly_usage WHERE tenant_id = $1 ORDER BY month DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*MonthlyUsage
	for rows.Next() {
		m := &MonthlyUsage{}
		if err := rows.Scan(&m.TenantID, &m.Month, &m.Count, &m.Plan, &m.SnappedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// --- Notification Channel Repo ---

type pgNotificationChannelRepo struct{ db *sql.DB }

func (r *pgNotificationChannelRepo) Create(ctx context.Context, c *NotificationChannel) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notification_channels (id, tenant_id, name, type, config, status, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, c.TenantID, c.Name, c.Type, c.Config, c.Status, c.CreatedAt, c.UpdatedAt)
	return err
}

func (r *pgNotificationChannelRepo) GetByID(ctx context.Context, id string) (*NotificationChannel, error) {
	c := &NotificationChannel{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, type, config, status, created_at, updated_at FROM notification_channels WHERE id = $1`, id).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Type, &c.Config, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("channel not found")
	}
	return c, err
}

func (r *pgNotificationChannelRepo) Update(ctx context.Context, c *NotificationChannel) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE notification_channels SET name = $1, type = $2, config = $3, status = $4, updated_at = NOW() WHERE id = $5`,
		c.Name, c.Type, c.Config, c.Status, c.ID)
	return err
}

func (r *pgNotificationChannelRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	return err
}

func (r *pgNotificationChannelRepo) ListByTenant(ctx context.Context, tenantID string) ([]*NotificationChannel, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, name, type, config, status, created_at, updated_at FROM notification_channels WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*NotificationChannel
	for rows.Next() {
		c := &NotificationChannel{}
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Type, &c.Config, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// --- Alert Rule Repo ---

type pgAlertRuleRepo struct{ db *sql.DB }

func (r *pgAlertRuleRepo) Create(ctx context.Context, rule *AlertRule) error {
	if rule.ID == "" {
		rule.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO alert_rules (id, tenant_id, name, metric, operator, threshold, window_minutes, service, path_template, channel_id, cooldown_minutes, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		rule.ID, rule.TenantID, rule.Name, rule.Metric, rule.Operator, rule.Threshold, rule.WindowMinutes,
		rule.Service, rule.PathTemplate, rule.ChannelID, rule.CooldownMinutes, rule.Status, rule.CreatedAt, rule.UpdatedAt)
	return err
}

func (r *pgAlertRuleRepo) GetByID(ctx context.Context, id string) (*AlertRule, error) {
	rule := &AlertRule{}
	var triggered sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, metric, operator, threshold, window_minutes, service, path_template, channel_id, cooldown_minutes, status, last_triggered_at, created_at, updated_at
		 FROM alert_rules WHERE id = $1`, id).
		Scan(&rule.ID, &rule.TenantID, &rule.Name, &rule.Metric, &rule.Operator, &rule.Threshold, &rule.WindowMinutes,
			&rule.Service, &rule.PathTemplate, &rule.ChannelID, &rule.CooldownMinutes, &rule.Status, &triggered, &rule.CreatedAt, &rule.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("alert rule not found")
	}
	if triggered.Valid {
		rule.LastTriggeredAt = &triggered.Time
	}
	return rule, err
}

func (r *pgAlertRuleRepo) Update(ctx context.Context, rule *AlertRule) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE alert_rules SET name=$1, metric=$2, operator=$3, threshold=$4, window_minutes=$5, service=$6, path_template=$7, channel_id=$8, cooldown_minutes=$9, status=$10, updated_at=NOW() WHERE id=$11`,
		rule.Name, rule.Metric, rule.Operator, rule.Threshold, rule.WindowMinutes, rule.Service, rule.PathTemplate, rule.ChannelID, rule.CooldownMinutes, rule.Status, rule.ID)
	return err
}

func (r *pgAlertRuleRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	return err
}

func (r *pgAlertRuleRepo) ListByTenant(ctx context.Context, tenantID string) ([]*AlertRule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, name, metric, operator, threshold, window_minutes, service, path_template, channel_id, cooldown_minutes, status, last_triggered_at, created_at, updated_at
		 FROM alert_rules WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertRules(rows)
}

func (r *pgAlertRuleRepo) ListActive(ctx context.Context) ([]*AlertRule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT ar.id, ar.tenant_id, ar.name, ar.metric, ar.operator, ar.threshold, ar.window_minutes, ar.service, ar.path_template, ar.channel_id, ar.cooldown_minutes, ar.status, ar.last_triggered_at, ar.created_at, ar.updated_at
		 FROM alert_rules ar JOIN tenants t ON ar.tenant_id = t.id
		 WHERE ar.status = 'active' AND t.status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertRules(rows)
}

func (r *pgAlertRuleRepo) UpdateLastTriggered(ctx context.Context, id string, t time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE alert_rules SET last_triggered_at = $1 WHERE id = $2`, t, id)
	return err
}

func scanAlertRules(rows *sql.Rows) ([]*AlertRule, error) {
	var list []*AlertRule
	for rows.Next() {
		rule := &AlertRule{}
		var triggered sql.NullTime
		if err := rows.Scan(&rule.ID, &rule.TenantID, &rule.Name, &rule.Metric, &rule.Operator, &rule.Threshold, &rule.WindowMinutes,
			&rule.Service, &rule.PathTemplate, &rule.ChannelID, &rule.CooldownMinutes, &rule.Status, &triggered, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		if triggered.Valid {
			rule.LastTriggeredAt = &triggered.Time
		}
		list = append(list, rule)
	}
	return list, rows.Err()
}

// --- Alert History Repo ---

type pgAlertHistoryRepo struct{ db *sql.DB }

func (r *pgAlertHistoryRepo) Create(ctx context.Context, e *AlertHistoryEntry) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO alert_history (id, rule_id, tenant_id, metric, threshold, actual_value, service, path_template, status, message, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		e.ID, e.RuleID, e.TenantID, e.Metric, e.Threshold, e.ActualValue, e.Service, e.PathTemplate, e.Status, e.Message, e.CreatedAt)
	return err
}

func (r *pgAlertHistoryRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*AlertHistoryEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, rule_id, tenant_id, metric, threshold, actual_value, service, path_template, status, message, created_at
		 FROM alert_history WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertHistory(rows)
}

func (r *pgAlertHistoryRepo) ListByRule(ctx context.Context, ruleID string, limit int) ([]*AlertHistoryEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, rule_id, tenant_id, metric, threshold, actual_value, service, path_template, status, message, created_at
		 FROM alert_history WHERE rule_id = $1 ORDER BY created_at DESC LIMIT $2`, ruleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertHistory(rows)
}

func scanAlertHistory(rows *sql.Rows) ([]*AlertHistoryEntry, error) {
	var list []*AlertHistoryEntry
	for rows.Next() {
		e := &AlertHistoryEntry{}
		if err := rows.Scan(&e.ID, &e.RuleID, &e.TenantID, &e.Metric, &e.Threshold, &e.ActualValue, &e.Service, &e.PathTemplate, &e.Status, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// --- Audit Repo ---

type pgAuditRepo struct{ db *sql.DB }

func (r *pgAuditRepo) Log(ctx context.Context, entry *AuditEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_log (id, tenant_id, user_id, action, resource, resource_id, detail, ip_address, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		entry.ID, entry.TenantID, entry.UserID, entry.Action, entry.Resource, entry.ResourceID, entry.Detail, entry.IPAddress, entry.CreatedAt)
	return err
}

func (r *pgAuditRepo) ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*AuditEntry, int, error) {
	var total int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE tenant_id = $1`, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, user_id, action, resource, resource_id, detail, ip_address, created_at
		 FROM audit_log WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []*AuditEntry
	for rows.Next() {
		e := &AuditEntry{}
		if err := rows.Scan(&e.ID, &e.TenantID, &e.UserID, &e.Action, &e.Resource, &e.ResourceID, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		list = append(list, e)
	}
	return list, total, rows.Err()
}

// --- Retention Policy Repo ---

type pgRetentionPolicyRepo struct{ db *sql.DB }

func (r *pgRetentionPolicyRepo) Upsert(ctx context.Context, p *RetentionPolicy) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO retention_policies (tenant_id, facts_days, metrics_days, traces_days, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (tenant_id) DO UPDATE SET facts_days = EXCLUDED.facts_days, metrics_days = EXCLUDED.metrics_days, traces_days = EXCLUDED.traces_days, updated_at = NOW()`,
		p.TenantID, p.FactsDays, p.MetricsDays, p.TracesDays)
	return err
}

func (r *pgRetentionPolicyRepo) GetByTenantID(ctx context.Context, tenantID string) (*RetentionPolicy, error) {
	p := &RetentionPolicy{}
	err := r.db.QueryRowContext(ctx,
		`SELECT tenant_id, facts_days, metrics_days, traces_days, updated_at FROM retention_policies WHERE tenant_id = $1`, tenantID).
		Scan(&p.TenantID, &p.FactsDays, &p.MetricsDays, &p.TracesDays, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// --- Password Reset Token Repo ---

type pgPasswordResetRepo struct{ db *sql.DB }

func (r *pgPasswordResetRepo) Create(ctx context.Context, token *PasswordResetToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	token.CreatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt.UTC(), token.CreatedAt)
	return err
}

func (r *pgPasswordResetRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error) {
	t := &PasswordResetToken{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		 FROM password_reset_tokens WHERE token_hash = $1`, tokenHash).
		Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *pgPasswordResetRepo) MarkUsed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *pgPasswordResetRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE expires_at < NOW()`)
	return err
}

// --- Email Verification Token Repo ---

type pgEmailVerificationRepo struct{ db *sql.DB }

func (r *pgEmailVerificationRepo) Create(ctx context.Context, token *EmailVerificationToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	token.CreatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO email_verification_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt.UTC(), token.CreatedAt)
	return err
}

func (r *pgEmailVerificationRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*EmailVerificationToken, error) {
	t := &EmailVerificationToken{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, verified_at, created_at
		 FROM email_verification_tokens WHERE token_hash = $1`, tokenHash).
		Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.VerifiedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *pgEmailVerificationRepo) MarkVerified(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE email_verification_tokens SET verified_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *pgEmailVerificationRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE expires_at < NOW()`)
	return err
}

// --- SSO Config Repo ---

type pgSSOConfigRepo struct{ db *sql.DB }

func (r *pgSSOConfigRepo) Upsert(ctx context.Context, cfg *SSOConfig) error {
	if cfg.ID == "" {
		cfg.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sso_configs (id, tenant_id, provider, enabled, entity_id, sso_url, certificate, client_id, client_secret, issuer, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		 ON CONFLICT(tenant_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			enabled = EXCLUDED.enabled,
			entity_id = EXCLUDED.entity_id,
			sso_url = EXCLUDED.sso_url,
			certificate = EXCLUDED.certificate,
			client_id = EXCLUDED.client_id,
			client_secret = EXCLUDED.client_secret,
			issuer = EXCLUDED.issuer,
			updated_at = NOW()`,
		cfg.ID, cfg.TenantID, cfg.Provider, cfg.Enabled, cfg.EntityID, cfg.SSOURL, cfg.Certificate,
		cfg.ClientID, cfg.ClientSecret, cfg.Issuer)
	return err
}

func (r *pgSSOConfigRepo) GetByTenantID(ctx context.Context, tenantID string) (*SSOConfig, error) {
	cfg := &SSOConfig{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, provider, enabled, entity_id, sso_url, certificate, client_id, client_secret, issuer, created_at, updated_at
		 FROM sso_configs WHERE tenant_id = $1`, tenantID,
	).Scan(&cfg.ID, &cfg.TenantID, &cfg.Provider, &cfg.Enabled, &cfg.EntityID, &cfg.SSOURL, &cfg.Certificate,
		&cfg.ClientID, &cfg.ClientSecret, &cfg.Issuer, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (r *pgSSOConfigRepo) Delete(ctx context.Context, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sso_configs WHERE tenant_id = $1`, tenantID)
	return err
}

// --- Session Repo ---

type pgSessionRepo struct{ db *sql.DB }

func (r *pgSessionRepo) Create(ctx context.Context, s *Session) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, tenant_id, ip_address, user_agent, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		s.ID, s.UserID, s.TenantID, s.IPAddress, s.UserAgent, s.CreatedAt.UTC(), s.ExpiresAt.UTC())
	return err
}

func (r *pgSessionRepo) GetByID(ctx context.Context, id string) (*Session, error) {
	s := &Session{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, tenant_id, ip_address, user_agent, created_at, expires_at, revoked_at
		 FROM sessions WHERE id = $1`, id,
	).Scan(&s.ID, &s.UserID, &s.TenantID, &s.IPAddress, &s.UserAgent, &s.CreatedAt, &s.ExpiresAt, &s.RevokedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (r *pgSessionRepo) ListByUser(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, tenant_id, ip_address, user_agent, created_at, expires_at, revoked_at
		 FROM sessions WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Session
	for rows.Next() {
		s := &Session{}
		if err := rows.Scan(&s.ID, &s.UserID, &s.TenantID, &s.IPAddress, &s.UserAgent, &s.CreatedAt, &s.ExpiresAt, &s.RevokedAt); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (r *pgSessionRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *pgSessionRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

func (r *pgSessionRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < NOW()`)
	return err
}

// --- Recovery Code Repo ---

type pgRecoveryCodeRepo struct{ db *sql.DB }

func (r *pgRecoveryCodeRepo) Store(ctx context.Context, userID string, codeHashes []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}

	for _, hash := range codeHashes {
		id := uuid.New().String()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
			id, userID, hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *pgRecoveryCodeRepo) Validate(ctx context.Context, userID, codeHash string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE recovery_codes SET used = TRUE WHERE user_id = $1 AND code_hash = $2 AND used = FALSE`,
		userID, codeHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *pgRecoveryCodeRepo) DeleteByUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID)
	return err
}

// --- Revoked Token Repo ---

type pgRevokedTokenRepo struct{ db *sql.DB }

func (r *pgRevokedTokenRepo) Revoke(ctx context.Context, jti string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO revoked_tokens (jti, expires_at) VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`,
		jti, expiresAt.UTC())
	return err
}

func (r *pgRevokedTokenRepo) IsRevoked(ctx context.Context, jti string) bool {
	var expiresAt time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT expires_at FROM revoked_tokens WHERE jti = $1`, jti).Scan(&expiresAt)
	if err != nil {
		return false
	}
	if time.Now().UTC().After(expiresAt) {
		_, _ = r.db.ExecContext(ctx, `DELETE FROM revoked_tokens WHERE jti = $1`, jti)
		return false
	}
	return true
}

func (r *pgRevokedTokenRepo) Cleanup(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM revoked_tokens WHERE expires_at < NOW()`)
	return err
}

// --- SSO State Repo ---

type pgSSOStateRepo struct{ db *sql.DB }

func (r *pgSSOStateRepo) Store(ctx context.Context, state, tenantID string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sso_states (state, tenant_id, expires_at) VALUES ($1, $2, $3)`,
		state, tenantID, expiresAt.UTC())
	return err
}

func (r *pgSSOStateRepo) ValidateAndDelete(ctx context.Context, state string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var tenantID string
	var expiresAt time.Time
	err = tx.QueryRowContext(ctx,
		`SELECT tenant_id, expires_at FROM sso_states WHERE state = $1`, state).Scan(&tenantID, &expiresAt)
	if err != nil {
		return "", fmt.Errorf("invalid or expired SSO state")
	}

	tx.ExecContext(ctx, `DELETE FROM sso_states WHERE state = $1`, state)

	if time.Now().UTC().After(expiresAt) {
		tx.Commit()
		return "", fmt.Errorf("SSO state expired")
	}

	return tenantID, tx.Commit()
}

func (r *pgSSOStateRepo) Cleanup(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM sso_states WHERE expires_at < NOW()`)
	return err
}

// --- Referral Repo ---

type pgReferralRepo struct{ db *sql.DB }

func (r *pgReferralRepo) Create(ctx context.Context, ref *referral.Referral) error {
	if ref.ID == "" {
		ref.ID = uuid.New().String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO referrals (id, code, referrer_tenant, status, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		ref.ID, ref.Code, ref.ReferrerTenant, ref.Status, ref.CreatedAt.UTC(), ref.ExpiresAt.UTC())
	return err
}

func (r *pgReferralRepo) GetByCode(ctx context.Context, code string) (*referral.Referral, error) {
	ref := &referral.Referral{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, code, referrer_tenant, referee_tenant, status, coupon_id, created_at, used_at, expires_at FROM referrals WHERE code = $1`,
		code).Scan(&ref.ID, &ref.Code, &ref.ReferrerTenant, &ref.RefereeTenant, &ref.Status, &ref.CouponID, &ref.CreatedAt, &ref.UsedAt, &ref.ExpiresAt)
	return ref, err
}

func (r *pgReferralRepo) MarkUsed(ctx context.Context, code, refereeTenant, couponID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE referrals SET referee_tenant = $1, status = 'used', coupon_id = $2, used_at = NOW() WHERE code = $3 AND status = 'active'`,
		refereeTenant, couponID, code)
	return err
}

func (r *pgReferralRepo) ListByTenant(ctx context.Context, tenantID string) ([]*referral.Referral, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, code, referrer_tenant, referee_tenant, status, coupon_id, created_at, used_at, expires_at FROM referrals WHERE referrer_tenant = $1 ORDER BY created_at DESC`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []*referral.Referral
	for rows.Next() {
		ref := &referral.Referral{}
		if err := rows.Scan(&ref.ID, &ref.Code, &ref.ReferrerTenant, &ref.RefereeTenant, &ref.Status, &ref.CouponID, &ref.CreatedAt, &ref.UsedAt, &ref.ExpiresAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (r *pgReferralRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM referrals WHERE referrer_tenant = $1 AND status = 'active'`, tenantID).Scan(&count)
	return count, err
}
