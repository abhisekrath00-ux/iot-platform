# Setting up the assistant in Slack and email

This is the operator/admin setup guide for messaging the assistant from Slack or email.
The security model (signatures, freshness, one-time linking, per-user roles) is in
docs/assistant.md and is not repeated here.

Honest status: the channels are tested with hand-built signed payloads and a recording
notifier, not against a real Slack workspace or mail gateway. Treat the first real setup
as a pilot and check each step's effect before moving on.

## Slack

Slack needs internet reachability both ways: Slack's servers call your server, and your
server calls Slack to reply. It is not available on an air-gapped install; use email with
a local gateway there instead.

1. Create the app at https://api.slack.com/apps (Create New App, from scratch). Any name,
   your workspace.
2. On Basic Information, copy the **Signing Secret**. In HexThings: Settings > Assistant in
   Slack and email, paste it into "Slack app signing secret", tick Slack, Save channels.
   (Secrets need SECRETS_KEY set on the server; the card says when storage is unavailable.)
3. Under OAuth & Permissions add the bot scopes **chat:write, im:history, im:read,
   im:write**.
4. Under Event Subscriptions, switch events on and set the Request URL to
   `https://<your server's public address>` plus the path shown on the card
   (`/v1/assistant/inbound/slack/<tenant>`). Slack verifies the URL with a signed challenge,
   which the server answers only when the signature matches the secret from step 2, so do
   step 2 first. Subscribe to the bot event **message.im**. Direct messages only: the
   server accepts only direct-message events (channel_type "im"); anything posted to a
   channel is ignored without an answer.
5. Install the app to the workspace and copy the **Bot User OAuth Token** (xoxb-...). Give
   it to the platform operator; they set SLACK_BOT_TOKEN on the server. Without it the
   assistant reads but cannot reply.
6. Each person links themselves: in Settings > Assistant in Slack and email, choose Slack,
   enter their Slack member id (their Slack profile > ... > Copy member ID, U...), press
   Link me, then send the shown code as a direct message to the bot within 15 minutes.
   After that they can message the assistant; changes wait for their YES, and control
   commands are never approvable from chat.

## Email

Email needs a mail gateway you run: something that receives the inbound mail, checks DKIM
and SPF, and POSTs each message as signed JSON to
`https://<your server>/v1/assistant/inbound/email/<tenant>` (the path is on the card).
The gateway is not part of the install - a common shape is a small script on the mail
server (a Postfix pipe or a hosted inbound-parse webhook) that does the signing.

Contract for each message:

- JSON body: `{"id": "<unique per message>", "from": "sender@example.com", "to": ["assistant@yourdomain"], "subject": "...", "text": "plain text body", "dkim": "pass", "spf": "pass", "ts": <unix seconds>}`
- Headers: `X-Timestamp: <unix seconds>` and
  `X-Signature: hex(HMAC-SHA256(secret, ts + "." + body))` where ts is the X-Timestamp
  value and body is the exact raw request body.
- The message is dropped unless the signature matches, the timestamp is within 5 minutes,
  the id was not seen before, and both dkim and spf are "pass". A bare From line proves
  nothing, by design.

Setup: choose a shared secret with the gateway, paste it into "Shared secret for the
gateway HMAC" on the card, tick Email, Save. Each person links their own mailbox: choose
Email on the card, enter the address, press Link me, and send the shown code FROM that
mailbox to the assistant's address within 15 minutes. Replies go out through the normal
notification email path (SMTP_* on the server), so outbound mail must work for replies.

## After setup

Ask the in-product assistant (it has this guide in its corpus) or check
docs/assistant.md for what the linked person can do. Everything stays off until you tick a
channel, and nothing about notification channels (alerts) changes: those are configured
separately on the same Settings page.
