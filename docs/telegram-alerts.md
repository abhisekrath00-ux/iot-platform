# Telegram alerts

An optional notification channel next to email, Slack, Teams, SMS, WhatsApp and webhooks. It works for alert rules, escalation steps, flows and report delivery, and has the same Send test button.

## What you need (from your side, not from us)

- A Telegram bot, created with @BotFather inside Telegram. The bot token is given to the platform operator; never paste it into chat or a ticket.
- The numeric chat id of the destination: a person (such as `123456789`), a group or a channel (negative ids, usually starting with `-100`). The bot must be a member of that chat or Telegram refuses the send. Find the id after sending any message to the bot or group, via the bot's getUpdates or a helper such as @userinfobot.
- Internet access from the API server to `api.telegram.org`. An air-gapped site leaves this unset and nothing is ever sent out; `TELEGRAM_API_BASE` can point at a compatible relay if one exists on the site network.

## Turn it on

The operator sets these in the API environment (`.env`):

| Variable | Meaning |
|---|---|
| `TELEGRAM_BOT_TOKEN` | the bot token from BotFather (kept out of every error message and log line; even API error replies are scrubbed of it) |
| `TELEGRAM_API_BASE` | optional, default `https://api.telegram.org`; for a compatible Bot API relay |

Until the token is set, creating a Telegram channel returns 409. Then an admin adds a channel in Settings: type Telegram, target the numeric chat id. Nothing is sent on save; the Send test button on the channel row is the explicit first send and names its destination.

## Honest status

Tested against a local fake of the Bot API: request shape (chat id, text, preview disabled), chat id validation (numeric, `-100...` groups), token never appears in errors (including Telegram's own error text), admin-only create, Send test through the real channel path, 409 when not configured, loopback relays refused in production, non-2xx replies surfaced with the status. NOT tested: a real bot, delivery to a real chat, Telegram rate limits, delivery receipts (a 2xx means Telegram accepted it, not that anyone read it). Bot membership of the destination chat and Telegram's terms are the operator's responsibility.

Upgrade note: this release extends the channel type constraint with migration 0079.
