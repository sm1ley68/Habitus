DROP TABLE IF EXISTS partner_admin_log;
ALTER TABLE partner_api_keys DROP COLUMN IF EXISTS allowed_ips;
ALTER TABLE partners ALTER COLUMN status SET DEFAULT 'active';
ALTER TABLE partners DROP CONSTRAINT IF EXISTS partners_status_check;
ALTER TABLE partners ADD CONSTRAINT partners_status_check
    CHECK (status IN ('active', 'suspended'));
