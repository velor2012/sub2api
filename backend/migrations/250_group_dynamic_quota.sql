ALTER TABLE groups ADD COLUMN IF NOT EXISTS dynamic_quota_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS dynamic_quota_seats integer NOT NULL DEFAULT 1 CHECK (dynamic_quota_seats >= 1);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS dynamic_quota_limit_usd double precision;
