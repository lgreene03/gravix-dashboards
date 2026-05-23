// Package tenantdb provides multi-tenant data access for Gravix.
//
// It defines repository interfaces for tenants, API keys, and users,
// along with a SQLite implementation suitable for single-node deployments.
package tenantdb

import (
	"context"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/referral"
)

// Tenant represents a Gravix customer account.
type Tenant struct {
	ID                   string
	Name                 string
	Email                string
	Plan                 string // free, team, business, scale, enterprise
	StripeCustomerID     string
	StripeSubscriptionID string
	Status               string // active, suspended, churned
	OverageAllowed       bool   // false=reject over limit (free), true=allow+flag (paid)
	ParentTenantID       string     // non-empty for child orgs in multi-org setup
	TrialStartedAt       *time.Time // nil if never on trial
	TrialEndsAt          *time.Time // nil if no trial or trial expired
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// APIKey represents an ingestion API key belonging to a tenant.
type APIKey struct {
	ID         string
	TenantID   string
	KeyPrefix  string // first 8 chars for display (e.g., "grvx_abc1...")
	Name       string
	Scopes     string // comma-separated: ingest:write,traces:write,admin:read,admin:write (empty = all)
	Status     string // active, revoked
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// APIKeyInfo is the minimal info returned during key validation on the
// ingestion hot path. Keep this struct small for performance.
type APIKeyInfo struct {
	TenantID       string
	Plan           string
	Status         string // tenant status (active, suspended)
	OverageAllowed bool   // whether overage is permitted (paid plans)
	Scopes         string // comma-separated scopes (empty = unrestricted)
}

// HasScope returns true if the API key has the given scope (or has no scope restrictions).
func (info *APIKeyInfo) HasScope(scope string) bool {
	if info.Scopes == "" {
		return true // empty = unrestricted
	}
	for _, s := range strings.Split(info.Scopes, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}

// User represents a dashboard user belonging to a tenant.
type User struct {
	ID               string
	TenantID         string
	Email            string
	PasswordHash     string
	Role             string // admin, viewer
	EmailVerified    bool
	TwoFactorEnabled bool
	TwoFactorSecret  string // encrypted TOTP secret (empty if 2FA not set up)
	Status           string // active, deactivated
	LastLoginAt      *time.Time
	CreatedAt        time.Time
}

// PasswordResetToken represents a one-time password reset token.
type PasswordResetToken struct {
	ID        string
	UserID    string
	TokenHash string // SHA-256 hash of the token
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// EmailVerificationToken represents a one-time email verification token.
type EmailVerificationToken struct {
	ID         string
	UserID     string
	TokenHash  string // SHA-256 hash of the token
	ExpiresAt  time.Time
	VerifiedAt *time.Time
	CreatedAt  time.Time
}

// Invitation represents a team invite sent by an admin.
type Invitation struct {
	ID        string
	TenantID  string
	Email     string
	Role      string // editor, viewer
	TokenHash string // SHA-256 hash of invite token
	Status    string // pending, accepted, expired
	InvitedBy string // user ID of inviter
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ConsentRecord tracks user acceptance of legal documents (TOS, privacy policy).
type ConsentRecord struct {
	ID        string
	TenantID  string
	UserID    string
	Type      string // tos, privacy, cookies
	Version   string // e.g., "1.0"
	Accepted  bool
	IPAddress string
	CreatedAt time.Time
}

// DeletionRequest tracks account deletion requests with grace period.
type DeletionRequest struct {
	ID          string
	TenantID    string
	RequestedBy string
	Status      string // pending, cancelled, completed
	RequestedAt time.Time
	ExpiresAt   time.Time // 30 days after request
	CompletedAt *time.Time
}

// TenantRepo manages tenant records.
type TenantRepo interface {
	Create(ctx context.Context, t *Tenant) error
	GetByID(ctx context.Context, id string) (*Tenant, error)
	GetByEmail(ctx context.Context, email string) (*Tenant, error)
	UpdatePlan(ctx context.Context, id, plan string) error
	UpdateStatus(ctx context.Context, id, status string) error
	UpdateStripe(ctx context.Context, id, customerID, subscriptionID string) error
	UpdateTrial(ctx context.Context, id string, trialStart, trialEnd *time.Time) error
	UpdateParentTenant(ctx context.Context, id, parentTenantID string) error
	ListChildren(ctx context.Context, parentTenantID string) ([]*Tenant, error)
	List(ctx context.Context) ([]*Tenant, error)
}

// APIKeyRepo manages per-tenant API keys for ingestion authentication.
type APIKeyRepo interface {
	// Create generates a new API key for the tenant. Returns the plaintext
	// key (only returned once — the stored value is a SHA-256 hash).
	// expiresAt is optional — pass nil for keys that never expire.
	Create(ctx context.Context, tenantID, name string, expiresAt *time.Time) (plainKey string, key *APIKey, err error)

	// ValidateKey checks a raw API key and returns tenant info if valid.
	// This is called on every ingestion request — must be fast.
	// Returns error if the key is expired, revoked, or not found.
	ValidateKey(ctx context.Context, rawKey string) (*APIKeyInfo, error)

	// TouchLastUsed updates the last_used_at timestamp for the key.
	TouchLastUsed(ctx context.Context, keyHash string) error

	// ListByTenant returns all API keys for a tenant (without hashes).
	ListByTenant(ctx context.Context, tenantID string) ([]*APIKey, error)

	// ListExpiringSoon returns active keys expiring within the given number of days.
	ListExpiringSoon(ctx context.Context, tenantID string, withinDays int) ([]*APIKey, error)

	// Revoke marks an API key as revoked.
	Revoke(ctx context.Context, keyID string) error
}

// UserRepo manages dashboard users.
type UserRepo interface {
	Create(ctx context.Context, u *User) error
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	ListByTenant(ctx context.Context, tenantID string) ([]*User, error)
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
	UpdateEmailVerified(ctx context.Context, userID string, verified bool) error
	UpdateLastLogin(ctx context.Context, userID string, t time.Time) error
	UpdateRole(ctx context.Context, userID, role string) error
	UpdateStatus(ctx context.Context, userID, status string) error
	UpdateTwoFactor(ctx context.Context, userID string, enabled bool, secret string) error
	CountByTenant(ctx context.Context, tenantID string) (int, error)
}

// InvitationRepo manages team invitations.
type InvitationRepo interface {
	Create(ctx context.Context, inv *Invitation) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error)
	ListByTenant(ctx context.Context, tenantID string) ([]*Invitation, error)
	MarkAccepted(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context) error
}

// ConsentRecordRepo manages legal consent records.
type ConsentRecordRepo interface {
	Create(ctx context.Context, r *ConsentRecord) error
	ListByUser(ctx context.Context, userID string) ([]*ConsentRecord, error)
	HasAccepted(ctx context.Context, userID, consentType, version string) (bool, error)
}

// DeletionRequestRepo manages account deletion requests.
type DeletionRequestRepo interface {
	Create(ctx context.Context, r *DeletionRequest) error
	GetByTenantID(ctx context.Context, tenantID string) (*DeletionRequest, error)
	Cancel(ctx context.Context, id string) error
	Complete(ctx context.Context, id string) error
	ListPending(ctx context.Context) ([]*DeletionRequest, error)
}

// PasswordResetRepo manages password reset tokens.
type PasswordResetRepo interface {
	// Create stores a new password reset token (hashed). Returns the token ID.
	Create(ctx context.Context, token *PasswordResetToken) error
	// FindByTokenHash looks up a token by its SHA-256 hash.
	FindByTokenHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error)
	// MarkUsed marks a token as used.
	MarkUsed(ctx context.Context, id string) error
	// DeleteExpired removes tokens that have expired.
	DeleteExpired(ctx context.Context) error
}

// EmailVerificationRepo manages email verification tokens.
type EmailVerificationRepo interface {
	// Create stores a new email verification token (hashed).
	Create(ctx context.Context, token *EmailVerificationToken) error
	// FindByTokenHash looks up a token by its SHA-256 hash.
	FindByTokenHash(ctx context.Context, tokenHash string) (*EmailVerificationToken, error)
	// MarkVerified marks a token as verified.
	MarkVerified(ctx context.Context, id string) error
	// DeleteExpired removes tokens that have expired.
	DeleteExpired(ctx context.Context) error
}

// EventCounterRepo tracks per-tenant daily event counts for billing metering.
type EventCounterRepo interface {
	Increment(ctx context.Context, tenantID, day string, delta int64) error
	GetCount(ctx context.Context, tenantID, day string) (int64, error)
}

// NotificationChannel represents a notification delivery target (Slack webhook, generic webhook).
type NotificationChannel struct {
	ID        string
	TenantID  string
	Name      string
	Type      string // slack, webhook
	Config    string // JSON blob: {"webhook_url":"...", "auth_header":"..."}
	Status    string // active
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AlertRule represents a metric threshold alert rule.
type AlertRule struct {
	ID              string
	TenantID        string
	Name            string
	Metric          string  // error_rate, p50_latency, p95_latency, p99_latency, throughput
	Operator        string  // gt, lt
	Threshold       float64
	WindowMinutes   int
	Service         string // empty = all services
	PathTemplate    string // empty = all paths
	ChannelID       string
	CooldownMinutes int
	Status          string // active, paused
	LastTriggeredAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AlertHistoryEntry represents a single alert firing event.
type AlertHistoryEntry struct {
	ID           string
	RuleID       string
	TenantID     string
	Metric       string
	Threshold    float64
	ActualValue  float64
	Service      string
	PathTemplate string
	Status       string // fired, resolved, error
	Message      string
	CreatedAt    time.Time
}

// NotificationChannelRepo manages notification channels.
type NotificationChannelRepo interface {
	Create(ctx context.Context, c *NotificationChannel) error
	GetByID(ctx context.Context, id string) (*NotificationChannel, error)
	Update(ctx context.Context, c *NotificationChannel) error
	Delete(ctx context.Context, id string) error
	ListByTenant(ctx context.Context, tenantID string) ([]*NotificationChannel, error)
}

// AlertRuleRepo manages alert rules.
type AlertRuleRepo interface {
	Create(ctx context.Context, r *AlertRule) error
	GetByID(ctx context.Context, id string) (*AlertRule, error)
	Update(ctx context.Context, r *AlertRule) error
	Delete(ctx context.Context, id string) error
	ListByTenant(ctx context.Context, tenantID string) ([]*AlertRule, error)
	// ListActive returns all active rules for active tenants (used by evaluator).
	ListActive(ctx context.Context) ([]*AlertRule, error)
	UpdateLastTriggered(ctx context.Context, id string, t time.Time) error
}

// AlertHistoryRepo manages alert firing history.
type AlertHistoryRepo interface {
	Create(ctx context.Context, e *AlertHistoryEntry) error
	ListByTenant(ctx context.Context, tenantID string, limit int) ([]*AlertHistoryEntry, error)
	ListByRule(ctx context.Context, ruleID string, limit int) ([]*AlertHistoryEntry, error)
}

// AuditEntry represents an immutable audit log record.
type AuditEntry struct {
	ID         string
	TenantID   string
	UserID     string // actor who performed the action
	Action     string // e.g. "api_key.create", "tenant.register", "data.purge"
	Resource   string // type of resource acted upon
	ResourceID string // ID of the specific resource
	Detail     string // JSON-encoded details (before/after, metadata)
	IPAddress  string // request source IP
	CreatedAt  time.Time
}

// AuditRepo manages immutable audit log entries.
type AuditRepo interface {
	Log(ctx context.Context, entry *AuditEntry) error
	ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*AuditEntry, int, error)
}

// RetentionPolicy represents per-tenant data retention configuration.
// When set, overrides the plan-based default retention for that tenant.
type RetentionPolicy struct {
	TenantID       string
	FactsDays      int  // retention for request facts (0 = use plan default)
	MetricsDays    int  // retention for aggregated metrics (0 = use plan default)
	TracesDays     int  // retention for trace samples (0 = use 7-day default)
	UpdatedAt      time.Time
}

// RetentionPolicyRepo manages per-tenant retention policies.
type RetentionPolicyRepo interface {
	// Upsert creates or updates the retention policy for a tenant.
	Upsert(ctx context.Context, p *RetentionPolicy) error
	// GetByTenantID returns the retention policy for a tenant, or nil if none set.
	GetByTenantID(ctx context.Context, tenantID string) (*RetentionPolicy, error)
}

// MonthlyUsage represents a monthly snapshot of tenant event usage.
type MonthlyUsage struct {
	TenantID  string
	Month     string // YYYY-MM
	Count     int64
	Plan      string
	SnappedAt time.Time
}

// MonthlyUsageRepo manages monthly usage snapshots.
type MonthlyUsageRepo interface {
	Snapshot(ctx context.Context, tenantID, month string, count int64, plan string) error
	GetByTenant(ctx context.Context, tenantID string, limit int) ([]*MonthlyUsage, error)
}

// SSOConfig represents per-tenant SSO configuration.
type SSOConfig struct {
	ID           string
	TenantID     string
	Provider     string // saml, oidc
	Enabled      bool
	EntityID     string // SAML IdP Entity ID
	SSOURL       string // SAML IdP SSO URL
	Certificate  string // SAML IdP X.509 certificate (PEM)
	ClientID     string // OIDC Client ID
	ClientSecret string // OIDC Client Secret (encrypted)
	Issuer       string // OIDC Issuer URL
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SSOConfigRepo manages per-tenant SSO configurations.
type SSOConfigRepo interface {
	Upsert(ctx context.Context, cfg *SSOConfig) error
	GetByTenantID(ctx context.Context, tenantID string) (*SSOConfig, error)
	Delete(ctx context.Context, tenantID string) error
}

// Session represents an active user session for session management.
type Session struct {
	ID        string
	UserID    string
	TenantID  string
	IPAddress string
	UserAgent string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// SessionRepo manages user sessions.
type SessionRepo interface {
	Create(ctx context.Context, s *Session) error
	GetByID(ctx context.Context, id string) (*Session, error)
	ListByUser(ctx context.Context, userID string) ([]*Session, error)
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID string) error
	DeleteExpired(ctx context.Context) error
}

// RecoveryCodeRepo manages hashed 2FA recovery codes.
type RecoveryCodeRepo interface {
	// Store saves a set of hashed recovery codes for a user, replacing any existing ones.
	Store(ctx context.Context, userID string, codeHashes []string) error
	// Validate checks if a code hash is valid (exists + unused), marks it used atomically, returns true if valid.
	Validate(ctx context.Context, userID, codeHash string) (bool, error)
	// DeleteByUser removes all recovery codes for a user.
	DeleteByUser(ctx context.Context, userID string) error
}

// RevokedTokenRepo manages persistent token revocation (replaces in-memory blacklist).
type RevokedTokenRepo interface {
	// Revoke adds a JTI to the revocation list.
	Revoke(ctx context.Context, jti string, expiresAt time.Time) error
	// IsRevoked checks if a JTI has been revoked.
	IsRevoked(ctx context.Context, jti string) bool
	// Cleanup removes expired entries.
	Cleanup(ctx context.Context) error
}

// SSOStateRepo manages CSRF state tokens for SSO login flows.
type SSOStateRepo interface {
	// Store saves a state token with expiry.
	Store(ctx context.Context, state, tenantID string, expiresAt time.Time) error
	// ValidateAndDelete checks a state token, returns the tenant ID, and deletes it atomically.
	ValidateAndDelete(ctx context.Context, state string) (tenantID string, err error)
	// Cleanup removes expired entries.
	Cleanup(ctx context.Context) error
}

// CustomDashboard represents a tenant-saved dashboard layout with custom panels.
type CustomDashboard struct {
	ID          string
	TenantID    string
	Name        string
	Description string
	Config      string    // JSON array of panel configurations
	IsDefault   bool
	SharedWith  string    // private, team
	CreatedBy   string    // user ID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CustomDashboardRepo manages user-saved dashboard configurations.
type CustomDashboardRepo interface {
	Create(ctx context.Context, d *CustomDashboard) error
	GetByID(ctx context.Context, id string) (*CustomDashboard, error)
	Update(ctx context.Context, d *CustomDashboard) error
	Delete(ctx context.Context, id string) error
	ListByTenant(ctx context.Context, tenantID string) ([]*CustomDashboard, error)
	// SetDefault marks a dashboard as the tenant default and clears the flag on any other.
	SetDefault(ctx context.Context, tenantID, id string) error
}

// TenantBranding holds per-tenant visual customization (Enterprise feature).
type TenantBranding struct {
	TenantID     string
	LogoURL      string
	FaviconURL   string
	PrimaryColor string // CSS hex, e.g. "#6366f1"
	AccentColor  string // CSS hex, e.g. "#8b5cf6"
	CompanyName  string
	UpdatedAt    time.Time
}

// TenantBrandingRepo manages per-tenant branding configuration.
type TenantBrandingRepo interface {
	// Get returns the branding for a tenant, or a zero-value struct if none is set.
	Get(ctx context.Context, tenantID string) (*TenantBranding, error)
	// Upsert creates or replaces the branding config for a tenant.
	Upsert(ctx context.Context, b *TenantBranding) error
}

// ScheduledExport represents a recurring data export to a customer's own S3 bucket.
type ScheduledExport struct {
	ID             string
	TenantID       string
	Name           string
	Schedule       string // cron expression (e.g. "0 3 * * *")
	DataType       string // request_facts, service_events
	Format         string // jsonl, csv, parquet
	DestinationURL string // s3://bucket/prefix
	LookbackDays   int
	Status         string // active, paused
	LastRunAt      *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ScheduledExportRepo manages recurring data export configurations.
type ScheduledExportRepo interface {
	Create(ctx context.Context, e *ScheduledExport) error
	GetByID(ctx context.Context, id string) (*ScheduledExport, error)
	Update(ctx context.Context, e *ScheduledExport) error
	Delete(ctx context.Context, id string) error
	ListByTenant(ctx context.Context, tenantID string) ([]*ScheduledExport, error)
	// ListActive returns all active exports across all tenants (used by the export runner).
	ListActive(ctx context.Context) ([]*ScheduledExport, error)
	// UpdateLastRun records the completion time and any error from the last run.
	UpdateLastRun(ctx context.Context, id string, t time.Time, errMsg string) error
}

// DB bundles all repositories. Implementations must provide all repos.
type DB interface {
	Tenants() TenantRepo
	APIKeys() APIKeyRepo
	Users() UserRepo
	EventCounters() EventCounterRepo
	MonthlyUsage() MonthlyUsageRepo
	NotificationChannels() NotificationChannelRepo
	AlertRules() AlertRuleRepo
	AlertHistory() AlertHistoryRepo
	AuditLog() AuditRepo
	RetentionPolicies() RetentionPolicyRepo
	PasswordResets() PasswordResetRepo
	EmailVerifications() EmailVerificationRepo
	Invitations() InvitationRepo
	ConsentRecords() ConsentRecordRepo
	DeletionRequests() DeletionRequestRepo
	SSOConfigs() SSOConfigRepo
	Sessions() SessionRepo
	RecoveryCodes() RecoveryCodeRepo
	RevokedTokens() RevokedTokenRepo
	SSOStates() SSOStateRepo
	Referrals() referral.ReferralRepo
	CustomDashboards() CustomDashboardRepo
	TenantBranding() TenantBrandingRepo
	ScheduledExports() ScheduledExportRepo
	Close() error
}
