-- Enter/exit alerts for geofences. Zones stay opt-in: a fence raises alerts
-- only when its creator asks for them. geofence_state remembers which devices
-- were inside which fence so the evaluator can alert on transitions instead
-- of re-alerting every pass. The first observation of a device/fence pair is
-- a silent baseline - enabling alerts on a zone with devices already inside
-- does not raise a storm of "entered" alerts.
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS alert_on_enter BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS alert_on_exit BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS alert_severity TEXT NOT NULL DEFAULT 'warning';
CREATE TABLE IF NOT EXISTS geofence_state (
  tenant_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  geofence_id TEXT NOT NULL REFERENCES geofences(id) ON DELETE CASCADE,
  inside BOOLEAN NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, geofence_id)
);
CREATE INDEX IF NOT EXISTS geofence_state_tenant ON geofence_state (tenant_id);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS geofence_id TEXT REFERENCES geofences(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS alerts_open_geofence_device ON alerts (geofence_id, device_id) WHERE status = 'open';
