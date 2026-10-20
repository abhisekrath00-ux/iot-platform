-- Telegram bot tokens are operator-global while channels are per-tenant. A chat must prove it
-- belongs to a tenant (pairing code spoken to the bot) before that tenant can alert into it or
-- even see it listed.
CREATE TABLE IF NOT EXISTS telegram_pair_codes (
  tenant_id TEXT PRIMARY KEY REFERENCES tenants(id),
  code TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS telegram_chats (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  chat_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  chat_type TEXT NOT NULL DEFAULT '',
  paired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, chat_id)
);
