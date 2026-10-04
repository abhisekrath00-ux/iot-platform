-- One-time password reset links (only issued when outgoing mail is configured). Only the hash is stored.
CREATE TABLE IF NOT EXISTS password_resets (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id TEXT NOT NULL,
  token_hash BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS password_resets_hash ON password_resets (token_hash);
CREATE INDEX IF NOT EXISTS password_resets_user ON password_resets (user_id);
