// Package tenantdb provides multi-tenant data access for Gravix.
//
// It defines repository interfaces for tenants, API keys, and users,
// along with a SQLite implementation suitable for single-node deployments.
package tenantdb

import (
	"context"
	"time"
)

// Tenant represents a Gravix customer account.
type Tenant struct {
	ID                   string
	Name                 string
	Email                string
	Plan                 string // free, starter, pro, business, enterprise
	StripeCustomerID     string
	StripeSubscriptionID string
	Status               string // active, suspended, churned
	OverageAllowed       bool   // false=reject over limit (free), true=allow+flag (paid)
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// APIKey represents an ingestion API key belonging to a tenant.
type APIKey struct {
	ID         string
	TenantID   string
	KeyPrefix  string // first 8 chars for display (e.g., "grvx_abc1...")
	Name       string
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
}

// User represents a dashboard user belonging to a tenant.
type User struct {
	ID           string
	TenantID     string
	Email        string
	PasswordHash string
	Role         string // admin, viewer
	CreatedAt    time.Time
}

// TenantRepo manages tenant records.
type TenantRepo interface {
	Create(ctx context.Context, t *Tenant) error
	GetByID(ctx context.Context, id string) (*Tenant, error)
	GetByEmail(ctx context.Context, email string) (*Tenant, error)
	UpdatePlan(ctx context.Context, id, plan string) error
	UpdateStatus(ctx context.Context, id, status string) error
	UpdateStripe(ctx context.Context, id, customerID, subscriptionID string) error
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
	Close() error
}
