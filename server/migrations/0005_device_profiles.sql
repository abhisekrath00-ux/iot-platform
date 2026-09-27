-- Device profiles: tenant-managed sensor templates. A profile names a driver
-- profile on the edge agent plus its points (register, func, type, word
-- order, scale, unit, range), so new sensor models are onboarded from the
-- dashboard with no code change and no edge reflash beyond config sync.
CREATE TABLE IF NOT EXISTS device_profiles (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,            -- e.g. "Acme EM-300 power meter"
  driver_profile TEXT NOT NULL,  -- edge driver: modbus-generic, door-contact, ...
  points JSONB NOT NULL,         -- [{id, register, func, type, word_order, scale, unit, min, max}]
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
