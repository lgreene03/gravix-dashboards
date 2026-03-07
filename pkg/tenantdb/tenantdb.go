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
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// APIKey represents an ingestion API key belonging to a tenant.
type APIKey struct {
	ID        string
	TenantID  string
	KeyPrefix string // first 8 chars for display (e.g., "grvx_abc1...")
	Name      string
	Status    string // active, revoked
	CreatedAt time.Time
	LastUsedAt *time.Time
}

// APIKeyInfo is the minimal info returned during key validation on the
// ingestion hot path. Keep this struct small for performance.
type APIKeyInfo struct {
	TenantID string
	Plan     string
	Status   string // tenant status (active, suspended)
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
	Create(ctx context.Context, tenantID, name string) (plainKey string, key *APIKey, err error)

	// ValidateKey checks a raw API key and returns tenant info if valid.
	// This is called on every ingestion request — must be fast.
	ValidateKey(ctx context.Context, rawKey string) (*APIKeyInfo, error)

	// TouchLastUsed updates the last_used_at timestamp for the key.
	TouchLastUsed(ctx context.Context, keyHash string) error

	// ListByTenant returns all API keys for a tenant (without hashes).
	ListByTenant(ctx context.Context, tenantID string) ([]*APIKey, error)

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

// DB bundles all repositories. Implementations must provide all repos.
type DB interface {
	Tenants() TenantRepo
	APIKeys() APIKeyRepo
	Users() UserRepo
	EventCounters() EventCounterRepo
	Close() error
}
