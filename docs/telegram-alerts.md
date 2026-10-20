# Telegram alerts

An optional notification channel next to email, Slack, Teams, SMS, WhatsApp and webhooks. It works for alert rules, escalation steps, flows and report delivery, and has the same Send test button.

## What you need (from your side, not from us)

- A Telegram bot, created with @BotFather inside Telegram. The bot token is given to the platform operator; never paste it into chat or a ticket.
- A chat paired to your tenant. The bot token is shared by the whole deployment, so seeing a chat or knowing its id proves nothing: in Settings press Get pairing code, send `/pair CODE` to the bot inside your chat (10 minutes, single use), then press Find chats. Only chats paired this way can be listed, picked or typed in as a channel - another tenant cannot see or claim your chats, and you cannot claim theirs. The bot must be a member of the chat or Telegram refuses the send.
- Internet access from the API server to `api.telegram.org`. An air-gapped site leaves this unset and nothing is ever sent out; `TELEGRAM_API_BASE` can point at a compatible relay if one exists on the site network.

## Turn it on

The operator sets these in the API environment (`.env`):

| Variable | Meaning |
|---|---|
| `TELEGRAM_BOT_TOKEN` | the bot token from BotFather (kept out of every error message and log line; even API error replies are scrubbed of it) |
| `TELEGRAM_API_BASE` | optional, default `https://api.telegram.org`; for a compatible Bot API relay |

Until the token is set, creating a Telegram channel returns 409. Then an admin adds a channel in Settings: type Telegram, target the paired chat (picked with Find chats). New channels of every type are staged disabled: no rule or escalation delivers to a channel until an admin presses Enable on its row. Send test works while a channel is off, so the destination can be verified before anything real flows.

Note for the operator: pairing reads the bot's getUpdates feed. Telegram forbids getUpdates while a webhook is set, so the Telegram inbound agent below shares this same stream (poller); the bot never sets a webhook.

## Inbound: the bot answers /alerts and /help (optional, off by default)

Set `TELEGRAM_INBOUND=1` next to the bot token and the deployment runs a small inbound agent: one leader-elected poller long-polls the bot's getUpdates feed, stores what it reads (migration 0082), and answers two read-only commands in paired chats only:

- `/alerts` - the site's current open alerts (up to 5, newest first)
- `/help` or `/start` - what the bot does and where the rest lives (the web UI)

That is the whole surface, on purpose. Only chats paired to a tenant get answers - an unpaired chat gets silence, so the bot never confirms whom it serves, never echoes a pairing code back, and tenant alerts never leave a paired chat. One chat pairs to one tenant only: speaking another tenant's code into an already-paired chat is refused, and a chat that somehow ends up paired to two tenants gets a refusal instead of a guess. Unpairing stops replies at once (the tenant lookup happens per message). Bot-authored messages are dropped at ingest, so no bot-to-bot loops. One reply per chat per 5 seconds. Acknowledging, resolving and changing anything stays in the web UI or the signed Slack/email assistant channels. When inbound is on, pairing reads the poller's inbox instead of making its own getUpdates calls, so the two never race. Inbox rows are kept 7 days, then deleted.

A paired group chat is a disclosure decision: anyone in that group, including people who join later, can read its /alerts answers. Pair only groups that may see the site's alerts; a private chat shows alerts to that one person.

Crash safety, honestly: exactly-once replies do not exist (Telegram has no idempotent send). The inbox row and the read cursor commit before any reply; a crash in that window is re-sent once by the startup recovery scan, which reprocesses stored messages younger than 10 minutes that have no recorded answer. The residual risk is the mirror window: if Telegram accepted the send but the process died before recording it, the recovery scan re-sends it - one duplicate per such crash, and repeated crash-after-send cycles can each produce one. A node that stays dead longer than the 10-minute window loses the reply. Losing a /alerts answer is harmless (ask again); a duplicate is cosmetic.

Honest status: tested against a local fake of the Bot API (poll-store-reply, cursor advance, inbox exactly-once on replay, both crash windows above including the single-duplicate case, paired vs unpaired, cooldown, tenant isolation of /alerts, unknown commands ignored, bot-authored dropped, second-tenant claim refused, ambiguous chat refusal, unpair stops replies, pairing through the inbox) and end to end on the demo stack with the same fake. NOT tested: a real bot, real chats, Telegram rate limits.

## Honest status

Tested against a local fake of the Bot API: request shape (chat id, text, preview disabled), chat id validation (numeric, `-100...` groups), token never appears in errors (including Telegram's own error text), admin-only create, Send test through the real channel path, 409 when not configured, loopback relays refused in production, non-2xx replies surfaced with the status. NOT tested: a real bot, delivery to a real chat, Telegram rate limits, delivery receipts (a 2xx means Telegram accepted it, not that anyone read it). Bot membership of the destination chat and Telegram's terms are the operator's responsibility.

Upgrade note: this release extends the channel type constraint with migration 0079; the inbound agent adds migration 0082.
