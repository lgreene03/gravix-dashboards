-- Migrate legacy plan names to 5-tier naming
UPDATE tenants SET plan = 'team' WHERE plan = 'starter';
UPDATE tenants SET plan = 'business' WHERE plan = 'pro';
