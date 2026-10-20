-- Idempotency-Key records for wizard-driven POSTs: a retried request replays the first response
-- instead of writing twice (or sending a second invitation email). Keys are scoped to
-- (tenant, user, action, key); a reused key with a different body is a 409.
CREATE TABLE IF NOT EXISTS idempotency_keys(
  tenant_id     text NOT NULL,
  user_id       text NOT NULL,
  action        text NOT NULL,
  key           text NOT NULL,
  payload_hash  text NOT NULL,
  status        text NOT NULL DEFAULT 'in_flight',
  response_code int,
  response_body text,
  response_type text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(tenant_id, user_id, action, key)
);
CREATE INDEX IF NOT EXISTS idempotency_keys_created ON idempotency_keys(created_at);
