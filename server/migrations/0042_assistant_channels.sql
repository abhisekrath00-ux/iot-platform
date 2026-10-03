-- Slack and email access to the assistant. Idempotent: migrations run on every API start.
ALTER TABLE assistant_actions ADD COLUMN IF NOT EXISTS code TEXT NOT NULL DEFAULT '';
ALTER TABLE assistant_actions ADD COLUMN IF NOT EXISTS via TEXT NOT NULL DEFAULT 'web';

CREATE TABLE IF NOT EXISTS assistant_channel_settings (
  tenant_id     TEXT PRIMARY KEY REFERENCES tenants(id),
  slack_enabled BOOLEAN NOT NULL DEFAULT false,
  email_enabled BOOLEAN NOT NULL DEFAULT false,
  slack_secret  TEXT NOT NULL DEFAULT '',  -- name in the encrypted secrets store
  email_secret  TEXT NOT NULL DEFAULT '',
  updated_by    TEXT NOT NULL DEFAULT '',
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A Slack member id or an email address tied to one platform user. Pending until the owner proves
-- control by sending the one-time code from that identity.
CREATE TABLE IF NOT EXISTS assistant_channel_links (
  id               TEXT PRIMARY KEY,
  tenant_id        TEXT NOT NULL REFERENCES tenants(id),
  user_id          TEXT NOT NULL REFERENCES users(id),
  kind             TEXT NOT NULL CHECK (kind IN ('slack','email')),
  address          TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified')),
  code_hash        TEXT NOT NULL DEFAULT '',
  autorun_low_risk BOOLEAN NOT NULL DEFAULT false, -- set by an admin only
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  verified_at      TIMESTAMPTZ,
  UNIQUE (tenant_id, kind, address)
);

-- replay protection for inbound messages
CREATE TABLE IF NOT EXISTS assistant_inbound_seen (
  tenant_id TEXT NOT NULL,
  msg_id    TEXT NOT NULL,
  seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, msg_id)
);
