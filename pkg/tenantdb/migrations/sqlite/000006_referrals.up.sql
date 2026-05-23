-- Phase 35: Referral persistence
CREATE TABLE IF NOT EXISTS referrals (
    id              TEXT PRIMARY KEY,
    code            TEXT NOT NULL UNIQUE,
    referrer_tenant TEXT NOT NULL REFERENCES tenants(id),
    referee_tenant  TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active',
    coupon_id       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    used_at         TEXT,
    expires_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_referrals_code ON referrals(code);
CREATE INDEX IF NOT EXISTS idx_referrals_referrer ON referrals(referrer_tenant);
