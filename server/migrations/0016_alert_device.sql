-- Alerts record the device that raised them so one rule can cover many devices
-- (profile-wide rules) with one open alert per rule and device.
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS device_id TEXT;
CREATE INDEX IF NOT EXISTS alerts_open_rule_device ON alerts (rule_id, device_id) WHERE status = 'open';
