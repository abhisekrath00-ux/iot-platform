# WhatsApp alerts

An optional notification channel next to email, Slack, Teams, SMS and webhooks. It works for alert rules, escalation steps, flows and report delivery, and has the same Send test button.

## What you need (from your side, not from us)

- A Meta WhatsApp Business account with the Cloud API enabled, a phone number ID and an access token. Meta charges for business-started conversations; that is between you and Meta and HexThings does not spend anything.
- For alerts that start a conversation, an approved message template whose body has one variable (`{{1}}`). WhatsApp refuses business-started plain text. Without a template, a plain text message is only delivered to people who wrote to your business number in the last 24 hours.
- Internet access from the API server to `graph.facebook.com`. An air-gapped site leaves this unset and nothing is ever sent out.

## Turn it on

The operator sets these in the API environment (`.env`). Put the token in the file yourself; never paste it into chat.

| Variable | Meaning |
|---|---|
| `WHATSAPP_PHONE_NUMBER_ID` | the sending number's ID from Meta |
| `WHATSAPP_TOKEN` | the access token (kept out of every error message and log line) |
| `WHATSAPP_TEMPLATE` | template name; optional, see above |
| `WHATSAPP_TEMPLATE_LANG` | template language code, default `en` |
| `WHATSAPP_API_BASE` | optional, default `https://graph.facebook.com/v20.0`; for a compatible gateway |

Until the first two are set, creating a WhatsApp channel returns 409. Then an admin adds a channel in Settings: type WhatsApp, number in international form such as `+919812345678`.

## Honest status

Tested against a local fake of the Cloud API: request shape (text and template), bearer token, number format, token never appears in errors, admin-only create, Send test through the real channel path, 409 when not configured, loopback gateways refused in production. NOT tested: a real Meta account, a real template approval, delivery to a real phone, Meta rate limits, delivery receipts (not tracked; a 2xx means Meta accepted it, not that it was read). Templates, opt-in handling and Meta's business policies are the operator's responsibility.

Upgrade note: this release extends the channel type constraint in migrations 0060 and 0070.
