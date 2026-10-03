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
Tested only against a scripted fake OpenAI-compatible server (`assistant_test.go`, `llm_test.go`): plan, read, propose, confirm, role and tenant checks, refusal of approve and secrets. No real model has been tried, so how well any model plans or picks endpoints is unproven. Models can be wrong; the confirm step is the safeguard. Slack and email channels, per-user auto-run of low-risk changes, deeper analysis and richer reports are not built yet.
