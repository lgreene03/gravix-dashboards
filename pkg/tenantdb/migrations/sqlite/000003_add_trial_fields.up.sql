-- Add trial tracking fields to tenants
ALTER TABLE tenants ADD COLUMN trial_started_at TEXT;
ALTER TABLE tenants ADD COLUMN trial_ends_at TEXT;
