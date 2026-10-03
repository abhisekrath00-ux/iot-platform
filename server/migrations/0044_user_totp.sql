-- Optional second factor (TOTP) for approving control commands. Idempotent.
CREATE TABLE IF NOT EXISTS user_totp (
  user_id     TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   TEXT NOT NULL,
  secret      BYTEA NOT NULL,           -- AES-GCM sealed with SECRETS_KEY, bound to tenant and user
  confirmed   BOOLEAN NOT NULL DEFAULT false,
  last_step   BIGINT NOT NULL DEFAULT 0, -- newest accepted time step: a code cannot be used twice
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
