# AI assistant

An agentic assistant that plans, calls the platform API as the signed-in user, and reports back.

## Model connection (admin, Settings)
Any OpenAI-compatible chat endpoint with tool calling: a hosted API, or a local server (Ollama `http://host:11434/v1`, llama.cpp, vLLM). Off by default. The API key is write-only and stored in the encrypted secrets store (needs `SECRETS_KEY`); local servers need no key. The base URL may be a private or loopback host (local models are the point) but never link-local (cloud metadata) addresses; redirects are not followed, responses are capped at 1 MB and the key is scrubbed from errors. Settings are tenant-scoped.

## What it can do
Two tools: `set_plan` (a visible list of steps) and `api_request`, which calls the platform's own `/v1` API through the normal handler chain, so every role check, tenant filter and validation applies as for that user.
- **Reads** (GET) run immediately.
- **Changes** become a pending action showing the exact method, path and body. Nothing runs until the same user confirms it in an interactive session within 30 minutes. A viewer's confirm fails the role check. Confirmed changes are audited `assistant.confirm` with `ai_initiated: true`.
- **Never available to it**, even with a confirm: approving control commands (it runs like an API key session, which cannot approve), users/roles, API keys, secrets, SSO, feature switches, switching control targets to automatic, device tokens, enrollment, and its own settings. The policy is default-deny (`internal/assistant/policy.go`).
- Tool output is treated as data, never instructions.

Limits per run: 12 model calls, 25 tool calls, 12 KB per tool result, 3 minutes. 30 runs per hour per user. The rule-based question box (`/v1/ask`) stays as the fallback when no model is connected.

## Honest status
Tested only against a scripted fake OpenAI-compatible server (`assistant_test.go`, `llm_test.go`): plan, read, propose, confirm, role and tenant checks, refusal of approve and secrets. No real model has been tried, so how well any model plans or picks endpoints is unproven. Models can be wrong; the confirm step is the safeguard. Deeper analysis and richer reports are not built yet.

## Slack and email
Admins switch each channel on in Settings and store a secret (encrypted). People link their own identity. Code: `cmd/api/assistant_channels.go`.
- **Who is speaking.** Every inbound message must be signed and fresh (5 minutes), and each message id is accepted once. Slack: the app signing secret (`X-Slack-Signature`), direct messages only so answers never reach a shared channel. Email: the admin's mail gateway posts JSON to `/v1/assistant/inbound/email/{tenant}` with `X-Timestamp` and `X-Signature` = HMAC-SHA256(secret, `ts.body`) and must report `dkim` and `spf` as `pass`; otherwise the message is dropped silently and audited. A bare From line proves nothing.
- **Linking.** A signed-in user enters their Slack member id or email in the web app and gets a one-time code (15 minutes, shown once, stored hashed). Sending `link <code>` from that identity verifies it. Unlinked senders are refused (Slack gets a short notice; email is never answered, to avoid backscatter to forged addresses). Roles are read live from the user record on every message.
- **Doing the task.** The message runs the same agent loop as the web chat, as that user. Changes are proposed with a six-character code; reply `YES <code>` (or a plain `YES` when exactly one is open from that channel) or `NO`. A Slack yes cannot confirm an email request and vice versa. Confirmed changes are audited `assistant.confirm` with `via` and `ai_initiated`.
- **Low-risk auto-run.** Off by default. An admin can switch it on per linked identity; it covers only acknowledging or commenting on an alert. It is audited as `assistant.autorun`. Everything else still waits for YES.
- **Never from chat.** Approving control commands, and every item on the excluded list above.
- **Not built:** Slack buttons or confirm links, conversation memory between chat messages (each message stands alone), inbound mail receiving itself (a gateway must deliver it).
- **Honest status.** Tested with hand-built signed payloads and a recording notifier (`assistant_channels_test.go`). No real Slack workspace, SMTP or mail gateway has been used; real payload formats may differ in details.
