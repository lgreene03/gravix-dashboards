-- Initial schema baseline: captures all tables as they exist at Phase 26.
-- For existing databases this migration is force-applied (version 1) without re-executing.

CREATE TABLE IF NOT EXISTS tenants (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	email       TEXT NOT NULL UNIQUE,
	plan        TEXT NOT NULL DEFAULT 'free',
	stripe_customer_id TEXT DEFAULT '',
	stripe_subscription_id TEXT DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'active',
	overage_allowed INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS api_keys (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	key_hash    TEXT NOT NULL UNIQUE,
	key_prefix  TEXT NOT NULL,
	name        TEXT NOT NULL DEFAULT 'default',
	status      TEXT NOT NULL DEFAULT 'active',
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	last_used_at TEXT,
	expires_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant ON api_keys(tenant_id);

CREATE TABLE IF NOT EXISTS users (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	email       TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role        TEXT NOT NULL DEFAULT 'admin',
	email_verified INTEGER NOT NULL DEFAULT 0,
	status      TEXT NOT NULL DEFAULT 'active',
	last_login_at TEXT,
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS event_counters (
	tenant_id TEXT NOT NULL,
	day       TEXT NOT NULL,
	count     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (tenant_id, day)
);

CREATE TABLE IF NOT EXISTS notification_channels (
	id         TEXT PRIMARY KEY,
	tenant_id  TEXT NOT NULL REFERENCES tenants(id),
	name       TEXT NOT NULL,
	type       TEXT NOT NULL,
	config     TEXT NOT NULL DEFAULT '{}',
	status     TEXT NOT NULL DEFAULT 'active',
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_notification_channels_tenant ON notification_channels(tenant_id);

CREATE TABLE IF NOT EXISTS alert_rules (
	id                TEXT PRIMARY KEY,
	tenant_id         TEXT NOT NULL REFERENCES tenants(id),
	name              TEXT NOT NULL,
	metric            TEXT NOT NULL,
	operator          TEXT NOT NULL,
	threshold         REAL NOT NULL,
	window_minutes    INTEGER NOT NULL DEFAULT 5,
	service           TEXT NOT NULL DEFAULT '',
	path_template     TEXT NOT NULL DEFAULT '',
	channel_id        TEXT NOT NULL REFERENCES notification_channels(id),
	cooldown_minutes  INTEGER NOT NULL DEFAULT 15,
	status            TEXT NOT NULL DEFAULT 'active',
	last_triggered_at TEXT,
	created_at        TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_alert_rules_tenant ON alert_rules(tenant_id);

CREATE TABLE IF NOT EXISTS alert_history (
	id            TEXT PRIMARY KEY,
	rule_id       TEXT NOT NULL REFERENCES alert_rules(id),
	tenant_id     TEXT NOT NULL REFERENCES tenants(id),
	metric        TEXT NOT NULL,
	threshold     REAL NOT NULL,
	actual_value  REAL NOT NULL,
	service       TEXT NOT NULL DEFAULT '',
	path_template TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL DEFAULT 'fired',
	message       TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_alert_history_tenant ON alert_history(tenant_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_rule ON alert_history(rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_created ON alert_history(created_at);

CREATE TABLE IF NOT EXISTS audit_logs (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	user_id     TEXT NOT NULL,
	action      TEXT NOT NULL,
	resource    TEXT NOT NULL DEFAULT '',
	resource_id TEXT NOT NULL DEFAULT '',
	detail      TEXT NOT NULL DEFAULT '',
	ip_address  TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_tenant_time ON audit_logs(tenant_id, created_at);

CREATE TABLE IF NOT EXISTS monthly_usage (
	tenant_id  TEXT NOT NULL,
	month      TEXT NOT NULL,
	count      INTEGER NOT NULL DEFAULT 0,
	plan       TEXT NOT NULL DEFAULT 'free',
	snapped_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (tenant_id, month)
);

CREATE TABLE IF NOT EXISTS retention_policies (
	tenant_id    TEXT PRIMARY KEY REFERENCES tenants(id),
	facts_days   INTEGER NOT NULL DEFAULT 0,
	metrics_days INTEGER NOT NULL DEFAULT 0,
	traces_days  INTEGER NOT NULL DEFAULT 0,
	updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS leader_locks (
	lock_name    TEXT PRIMARY KEY,
	holder_id    TEXT NOT NULL,
	acquired_at  TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL REFERENCES users(id),
	token_hash  TEXT NOT NULL UNIQUE,
	expires_at  TEXT NOT NULL,
	used_at     TEXT,
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_prt_token ON password_reset_tokens(token_hash);

CREATE TABLE IF NOT EXISTS email_verification_tokens (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL REFERENCES users(id),
	token_hash  TEXT NOT NULL UNIQUE,
	expires_at  TEXT NOT NULL,
	verified_at TEXT,
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_evt_token ON email_verification_tokens(token_hash);

CREATE TABLE IF NOT EXISTS invitations (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	email       TEXT NOT NULL,
	role        TEXT NOT NULL DEFAULT 'viewer',
	token_hash  TEXT NOT NULL UNIQUE,
	status      TEXT NOT NULL DEFAULT 'pending',
	invited_by  TEXT NOT NULL,
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_inv_token ON invitations(token_hash);
CREATE INDEX IF NOT EXISTS idx_inv_tenant ON invitations(tenant_id);

CREATE TABLE IF NOT EXISTS consent_records (
	id          TEXT PRIMARY KEY,
	tenant_id   TEXT NOT NULL REFERENCES tenants(id),
	user_id     TEXT NOT NULL,
	type        TEXT NOT NULL,
	version     TEXT NOT NULL DEFAULT '1.0',
	accepted    INTEGER NOT NULL DEFAULT 1,
	ip_address  TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_consent_user ON consent_records(user_id);

CREATE TABLE IF NOT EXISTS deletion_requests (
	id           TEXT PRIMARY KEY,
	tenant_id    TEXT NOT NULL REFERENCES tenants(id),
	requested_by TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'pending',
	requested_at TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at   TEXT NOT NULL,
	completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_deletion_tenant ON deletion_requests(tenant_id);
