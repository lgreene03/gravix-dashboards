-- Add trial tracking fields to tenants
ALTER TABLE tenants ADD COLUMN trial_started_at TIMESTAMPTZ;
ALTER TABLE tenants ADD COLUMN trial_ends_at TIMESTAMPTZ;
