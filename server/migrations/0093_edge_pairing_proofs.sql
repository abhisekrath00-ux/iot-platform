-- One short-lived diagnostic proof per gateway. This is not telemetry,
-- a device command, a new identity or an authorization grant.
CREATE TABLE IF NOT EXISTS edge_pairing_proofs (
 id text PRIMARY KEY,
 migration_id text NOT NULL REFERENCES edge_identity_migrations(id),
 tenant_id text NOT NULL REFERENCES tenants(id),
 gateway_id text NOT NULL UNIQUE REFERENCES gateways(id),
 cert_fingerprint text NOT NULL,
 nonce_sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '60 seconds',
 observed_at timestamptz
);
