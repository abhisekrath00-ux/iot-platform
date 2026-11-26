-- Isolated opt-in candidate only. Not in migration ledger or production routes.
CREATE TABLE IF NOT EXISTS enrollment_attempt_candidate (
 attempt_id text PRIMARY KEY,
 token_id text NOT NULL UNIQUE,
 tenant_id text NOT NULL,
 gateway_id text NOT NULL,
 serial text NOT NULL,
 csr_pem text NOT NULL,
 csr_sha256 text NOT NULL,
 fingerprint text NOT NULL,
 response bytea NOT NULL,
 expires_at timestamptz NOT NULL,
 revoked boolean NOT NULL DEFAULT false
);
