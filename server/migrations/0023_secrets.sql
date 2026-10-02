-- Encrypted per-tenant secrets (AES-256-GCM, key from SECRETS_KEY, never stored).
-- Idempotent: migrations re-run on start.
CREATE TABLE IF NOT EXISTS secrets (
  tenant_id  TEXT NOT NULL REFERENCES tenants(id),
  name       TEXT NOT NULL,
  ciphertext BYTEA NOT NULL,
  key_id     TEXT NOT NULL DEFAULT 'k1',
  created_by TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, name)
);
