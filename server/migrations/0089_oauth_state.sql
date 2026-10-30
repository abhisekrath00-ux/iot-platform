-- Short-lived OAuth state shared by API replicas. Raw state tokens are never stored.
CREATE TABLE IF NOT EXISTS oauth_states (
    state_hash TEXT PRIMARY KEY,
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS oauth_states_expiry ON oauth_states(expires_at);
