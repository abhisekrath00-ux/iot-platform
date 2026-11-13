CREATE TABLE IF NOT EXISTS edge_identity_migrations (
 id text PRIMARY KEY,
 tenant_id text NOT NULL REFERENCES tenants(id),
 gateway_id text NOT NULL REFERENCES gateways(id),
 serial text NOT NULL,
 csr_pem text NOT NULL,
 csr_sha256 text NOT NULL,
 key_sha256 text NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','issued','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '15 minutes',
 approved_by text,
 cert_pem text,
 cert_fingerprint text,
 UNIQUE(gateway_id,csr_sha256)
);
CREATE INDEX IF NOT EXISTS edge_identity_migrations_pending ON edge_identity_migrations(tenant_id,created_at) WHERE state='pending';
