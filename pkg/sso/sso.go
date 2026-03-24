// Package sso provides SAML and OIDC single sign-on for enterprise tenants.
package sso

import (
	"context"
	"errors"
	"time"
)

// Provider types.
const (
	ProviderSAML = "saml"
	ProviderOIDC = "oidc"
)

// ErrNotConfigured is returned when SSO is not configured for a tenant.
var ErrNotConfigured = errors.New("sso: not configured for tenant")

// ErrInvalidAssertion is returned when SAML assertion or OIDC token validation fails.
var ErrInvalidAssertion = errors.New("sso: invalid assertion")

// Config represents per-tenant SSO configuration.
type Config struct {
	ID       string
	TenantID string
	Provider string // "saml" or "oidc"
	Enabled  bool

	// SAML fields
	EntityID    string // IdP Entity ID
	SSOURL      string // IdP SSO URL (HTTP-Redirect binding)
	Certificate string // IdP X.509 certificate (PEM)

	// OIDC fields
	ClientID     string // OIDC Client ID
	ClientSecret string // OIDC Client Secret (encrypted at rest)
	Issuer       string // OIDC Issuer URL (e.g., https://accounts.google.com)

	CreatedAt time.Time
	UpdatedAt time.Time
}

// SSOUser represents a user identity from SSO assertion/token.
type SSOUser struct {
	Email     string
	FirstName string
	LastName  string
	Groups    []string // group memberships from IdP
}

// Authenticator validates SSO responses and extracts user identity.
type Authenticator interface {
	// InitiateLogin returns the URL to redirect the user to for SSO login.
	InitiateLogin(cfg *Config, callbackURL string) (redirectURL string, state string, err error)
	// ValidateCallback processes the SSO callback and returns the authenticated user.
	ValidateCallback(ctx context.Context, cfg *Config, params map[string]string) (*SSOUser, error)
}

// ConfigRepo manages per-tenant SSO configurations in the database.
type ConfigRepo interface {
	Upsert(ctx context.Context, cfg *Config) error
	GetByTenantID(ctx context.Context, tenantID string) (*Config, error)
	Delete(ctx context.Context, tenantID string) error
}
