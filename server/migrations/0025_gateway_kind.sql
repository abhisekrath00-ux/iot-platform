-- Direct network devices (MCUs that publish straight to the broker) are
-- virtual gateways of kind 'direct': telemetry-only broker ACL.
ALTER TABLE gateways ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'edge' CHECK (kind IN ('edge','direct'));
