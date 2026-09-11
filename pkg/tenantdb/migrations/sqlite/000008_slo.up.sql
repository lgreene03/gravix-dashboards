-- Phase 8: service level objectives and error-budget burn rates (GRVX-811)
--
-- One SLO per service per kind. The UNIQUE constraint is what makes that true
-- rather than aspirational: two availability objectives for one service is two
-- answers to one question, and whichever the dashboard happened to read first
-- would become the real one.

CREATE TABLE IF NOT EXISTS slos (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service       TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('availability','latency')),
    objective     REAL NOT NULL CHECK (objective > 0 AND objective < 1),
    threshold_ms  REAL NOT NULL DEFAULT 0,
    window_days   INTEGER NOT NULL CHECK (window_days IN (7,28,30)),
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (tenant_id, service, kind)
);

CREATE INDEX IF NOT EXISTS idx_slos_tenant_enabled ON slos (tenant_id, enabled);
