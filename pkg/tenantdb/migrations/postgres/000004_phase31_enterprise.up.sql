-- Phase 31: Enterprise features
-- SSO configs, sessions, API key scopes, 2FA, multi-org

CREATE TABLE IF NOT EXISTS sso_configs (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL UNIQUE REFERENCES tenants(id),
    provider      TEXT NOT NULL DEFAULT 'oidc',
    enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    entity_id     TEXT NOT NULL DEFAULT '',
    sso_url       TEXT NOT NULL DEFAULT '',
    certificate   TEXT NOT NULL DEFAULT '',
    client_id     TEXT NOT NULL DEFAULT '',
    client_secret TEXT NOT NULL DEFAULT '',
    issuer        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sso_configs_tenant ON sso_configs(tenant_id);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id),
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_tenant ON sessions(tenant_id);

-- Add API key scopes column
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS scopes TEXT NOT NULL DEFAULT '';

-- Add 2FA columns to users
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_secret TEXT NOT NULL DEFAULT '';

-- Add parent_tenant_id for multi-org
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS parent_tenant_id TEXT DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_tenants_parent ON tenants(parent_tenant_id);
