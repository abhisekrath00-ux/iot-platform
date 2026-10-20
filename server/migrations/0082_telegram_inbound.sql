-- Inbound Telegram agent (optional, off by default: TELEGRAM_INBOUND=1). A leader-elected poller
-- long-polls the bot's getUpdates feed, stores every consumed update in telegram_inbox and advances
-- telegram_cursor in the same transaction, so a crash or failover never answers the same message
-- twice and never skips one. Pairing (migration 0080) and the /alerts,/help replies read this
-- inbox; the bot never sets a webhook (Telegram forbids getUpdates while a webhook is set).
CREATE TABLE IF NOT EXISTS telegram_cursor (
  id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  last_update_id BIGINT NOT NULL DEFAULT 0
);
INSERT INTO telegram_cursor(id, last_update_id) VALUES (1, 0) ON CONFLICT (id) DO NOTHING;
CREATE TABLE IF NOT EXISTS telegram_inbox (
  update_id BIGINT PRIMARY KEY,
  chat_id TEXT NOT NULL,
  chat_type TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  text TEXT NOT NULL DEFAULT '',
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  answered_at TIMESTAMPTZ -- set once the poller finished with this message (reply sent or deliberately none); NULL after a crash means the startup recovery scan reprocesses it
);
CREATE INDEX IF NOT EXISTS telegram_inbox_received ON telegram_inbox(received_at DESC);
