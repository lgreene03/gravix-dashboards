-- Phase 34: GA Security — recovery codes, token revocation, SSO state
-- recovery_codes: stores hashed recovery codes for 2FA backup
CREATE TABLE IF NOT EXISTS recovery_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id),
    code_hash  TEXT NOT NULL,
    used       INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_recovery_codes_user ON recovery_codes(user_id);

-- revoked_tokens: persistent token blacklist (replaces in-memory sync.Map)
CREATE TABLE IF NOT EXISTS revoked_tokens (
    jti        TEXT PRIMARY KEY,
    expires_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- sso_states: CSRF protection for SSO login flow
CREATE TABLE IF NOT EXISTS sso_states (
    state      TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL
);
