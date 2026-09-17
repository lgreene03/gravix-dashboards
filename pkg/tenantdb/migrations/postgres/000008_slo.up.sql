-- Phase 8: service level objectives and error-budget burn rates (GRVX-811)
--
-- The Postgres counterpart of the SQLite migration, with the same constraints.
-- They are written separately rather than generated because the type mapping is
-- not mechanical — REAL is 4 bytes here and 8 in SQLite, and an objective of
-- 0.999 needs the precision.

CREATE TABLE IF NOT EXISTS slos (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service       TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('availability','latency')),
    objective     DOUBLE PRECISION NOT NULL CHECK (objective > 0 AND objective < 1),
    threshold_ms  DOUBLE PRECISION NOT NULL DEFAULT 0,
    window_days   INTEGER NOT NULL CHECK (window_days IN (7,28,30)),
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, service, kind)
);

CREATE INDEX IF NOT EXISTS idx_slos_tenant_enabled ON slos (tenant_id, enabled);
