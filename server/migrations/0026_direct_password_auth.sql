-- Optional username+secret broker auth for direct devices that cannot hold a
-- client certificate. Off by default; an admin enables it per tenant.
ALTER TABLE gateways ADD COLUMN IF NOT EXISTS auth_mode TEXT NOT NULL DEFAULT 'cert' CHECK (auth_mode IN ('cert','password'));
ALTER TABLE gateways ADD COLUMN IF NOT EXISTS broker_pw_hash TEXT;
CREATE TABLE IF NOT EXISTS tenant_direct_auth (
  tenant_id TEXT PRIMARY KEY REFERENCES tenants(id),
  password_enabled BOOLEAN NOT NULL DEFAULT false,
  updated_by TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
