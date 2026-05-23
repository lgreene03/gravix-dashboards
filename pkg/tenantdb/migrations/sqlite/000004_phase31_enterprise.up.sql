-- Phase 31: Enterprise features
-- SSO configs, sessions, API key scopes, 2FA, multi-org

CREATE TABLE IF NOT EXISTS sso_configs (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL UNIQUE REFERENCES tenants(id),
    provider      TEXT NOT NULL DEFAULT 'oidc',
    enabled       INTEGER NOT NULL DEFAULT 0,
    entity_id     TEXT NOT NULL DEFAULT '',
    sso_url       TEXT NOT NULL DEFAULT '',
    certificate   TEXT NOT NULL DEFAULT '',
    client_id     TEXT NOT NULL DEFAULT '',
    client_secret TEXT NOT NULL DEFAULT '',
    issuer        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_sso_configs_tenant ON sso_configs(tenant_id);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id),
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_tenant ON sessions(tenant_id);

-- Add API key scopes column
ALTER TABLE api_keys ADD COLUMN scopes TEXT NOT NULL DEFAULT '';

-- Add 2FA columns to users
ALTER TABLE users ADD COLUMN two_factor_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_secret TEXT NOT NULL DEFAULT '';

-- Add parent_tenant_id for multi-org
ALTER TABLE tenants ADD COLUMN parent_tenant_id TEXT DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_tenants_parent ON tenants(parent_tenant_id);
