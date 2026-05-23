-- Phase 6: Platform — custom dashboards, tenant branding, scheduled exports

CREATE TABLE IF NOT EXISTS custom_dashboards (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    config      TEXT NOT NULL DEFAULT '[]',
    is_default  INTEGER NOT NULL DEFAULT 0,
    shared_with TEXT NOT NULL DEFAULT 'private',
    created_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_custom_dashboards_tenant ON custom_dashboards(tenant_id);

CREATE TABLE IF NOT EXISTS tenant_branding (
    tenant_id     TEXT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    logo_url      TEXT NOT NULL DEFAULT '',
    favicon_url   TEXT NOT NULL DEFAULT '',
    primary_color TEXT NOT NULL DEFAULT '#6366f1',
    accent_color  TEXT NOT NULL DEFAULT '#8b5cf6',
    company_name  TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS scheduled_exports (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    schedule        TEXT NOT NULL,
    data_type       TEXT NOT NULL DEFAULT 'request_facts',
    format          TEXT NOT NULL DEFAULT 'jsonl',
    destination_url TEXT NOT NULL,
    lookback_days   INTEGER NOT NULL DEFAULT 7,
    status          TEXT NOT NULL DEFAULT 'active',
    last_run_at     TEXT,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_scheduled_exports_tenant ON scheduled_exports(tenant_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_exports_active ON scheduled_exports(status) WHERE status = 'active';
