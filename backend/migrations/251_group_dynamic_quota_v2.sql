ALTER TABLE groups ADD COLUMN IF NOT EXISTS dynamic_quota_positions bigint;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS dynamic_quota_abnormal boolean NOT NULL DEFAULT false;
ALTER TABLE groups DROP COLUMN IF EXISTS dynamic_quota_seats;
UPDATE groups SET dynamic_quota_limit_usd = NULL WHERE dynamic_quota_limit_usd IS NOT NULL;
