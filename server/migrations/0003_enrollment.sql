-- Gateway enrollment: one-time claim codes bound to tenant + serial.
CREATE TABLE IF NOT EXISTS enrollment_tokens (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  site_id TEXT NOT NULL REFERENCES sites(id),
  gateway_id TEXT NOT NULL REFERENCES gateways(id),
  serial TEXT NOT NULL,
  code_hash BYTEA NOT NULL,           -- SHA-256 of the claim code; the code itself is never stored
  expires_at TIMESTAMPTZ NOT NULL,
  claimed_at TIMESTAMPTZ,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS enrollment_tokens_code_hash ON enrollment_tokens(code_hash);
