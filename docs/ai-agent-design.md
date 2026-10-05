# Local AI agent layer: assessment and plan

Written 2026-10-05 from the code in this repo. It answers the "Enterprise Local AI Agent Layer" brief: what already
exists, what is missing, where each piece goes, and in what order it is built. Status labels follow status.md:
nothing here counts as done until it is built, tested and labelled.

## 1. What already exists (reuse, do not rebuild)

| Brief section | Existing piece | Where |
|---|---|---|
| Agent loop with limits | Bounded loop: 12 model calls, 25 tool calls, 12 KB per result, 3 min, 30 runs per hour per user | `cmd/api/assistant.go` |
| Policy engine (default deny) | Read allow-list with credential paths denied; an explicit list of changes that need a confirm; everything else refused | `internal/assistant/policy.go` |
| RBAC as the user | The agent calls the real `/v1` handler chain as the signed-in user, so role checks, tenant filters and customer scoping apply | `assistant.go` (`inner` chain) |
| Approval | Pending action with exact method, path, body; same user confirms within 30 min; viewer confirm fails the role check; control approvals can never come from the AI | `assistant.go` |
| Audit | `assistant.confirm`, `assistant.autorun` with `ai_initiated` | `audit_log`, Audit page |
| Model connection | Any OpenAI-compatible endpoint with tool calling, local hosts allowed, key in the encrypted store, redirects refused, responses capped | `internal/llm`, Settings AI card |
| Chat channels | Slack and signed email with identity linking | `assistant_channels.go` |
| Analysis | Anomalies, forecast, root-cause hints, correlation, KPIs | `internal/anomaly`, `forecast`, `rules` |
| Reports | CSV, HTML, PDF, XLSX, schedules | `internal/report` |
| Docs corpus | about 60 markdown files | `docs/` |
| Air-gapped bundle, compose, MCP service | `scripts/airgap-*.sh`, `docker-compose.yml` | repo root |

## 2. Gaps against the brief

1. **No local model runtime.** Nothing runs llama.cpp or ships a model. The assistant only talks to an endpoint someone else runs.
2. **Tools are generic.** The model has one `api_request` tool and a path regex policy. The brief wants a typed registry (name, schema, risk class) with argument validation.
3. **Risk classes are two, not four.** Today: read, confirm, denied. Needed: READ, LOW_RISK_WRITE, HIGH_RISK_WRITE, DESTRUCTIVE, with per-class admin switches and an auto-remediation policy (off by default).
4. **Chat is a page, not a side panel.** No streaming, no page context ("why is it offline?" cannot resolve to the device on screen), no stored conversation history.
5. **No deterministic workflows.** Joins and arithmetic (offline devices vs their gateways) are left to the model.
6. **No knowledge retrieval.** The docs are not indexed for the assistant.
7. **No dedicated AI audit trail.** Only confirmed changes are audited. The brief wants every tool call (arguments, risk, authorization, duration, error) and an admin activity page.
8. **No AI health or metrics**, no eval harness, no screenshots.

## 3. Capabilities the brief names that do not exist in the platform

The brief says to expose only tools that really exist. These do not, so they are **not offered** until a real, safe path exists:

- **Restart MQTT, restart a gateway, restart or disable a device.** The API has no such handlers, and the API container has no authority over the broker or Docker (and must not be given the Docker socket). Control today is the approved, four-eyes command path with a simulated actuator. V1 will diagnose and recommend a restart; it will not perform one. A restart tool needs its own design and hazard review.
- **Search service logs.** There is no queryable application log store (process logs go to stdout). V1 log tools cover what is stored: audit log, alert history and comments, command results, gateway last-seen and health, ingest rejects if recorded. Free-text search over service logs is a later feature.
- **Create administrator, change roles.** Deliberately blocked for the AI today (users, roles, keys, secrets stay human-only). The example "create user with monitoring-only permissions" would need a policy change plus four-eyes; proposed as HIGH_RISK with an approval by a different admin, not built in V1.
- **Update device or system configuration** beyond the allow-listed device attributes, tags, groups and rules.

## 4. Architecture decision

The brief sketches a separate `ai-runtime/` service holding agent, tools, security and knowledge. Here:

- **`ai-runtime` container** = llama.cpp's `llama-server` (OpenAI-compatible, CPU only) plus a small health and metrics endpoint (`/health /ready /version /model /metrics`). Model file mounted read-only, sha256 checked at start, internal network only, no internet.
- **Orchestrator, typed tool registry, policy engine, approval, RAG and audit stay in the Go API** (`internal/aiagent/...`). Reason: these must run tools through the existing handlers as the signed-in user. A separate agent service would have to re-implement authentication and RBAC or hold privileged credentials, which is the opposite of the brief's security principle.
- The model provider stays replaceable: the runtime is just an OpenAI-compatible URL, so another GGUF, Ollama or vLLM works by configuration.
- If the runtime is down, the API reports "AI unavailable" and everything else keeps working (the rule-based `/v1/ask` stays as the fallback).

Mapping of the brief's modules to code: `inference/` = the runtime container and `internal/llm`; `agent/` = `internal/aiagent/orchestrator`, `workflows`; `tools/` = `internal/aiagent/registry` over existing handlers; `security/` = `internal/assistant` policy, approval and `internal/aiagent/audit`; `knowledge/` = `internal/aiagent/rag`; `evaluation/` = `internal/aiagent/eval` plus a cases file.

## 5. Decisions taken for V1 (override any of them)

- **Model.** There is no "Qwen3 1.5B". The Qwen3 sizes are 0.6B, 1.7B and 4B; the 1.5B is Qwen2.5. Default: **Qwen3-1.7B Instruct, Q4_K_M GGUF**. Qwen2.5-1.5B-Instruct and Qwen3-0.6B (smoke tests) are alternatives by config.
- **RAG without embeddings.** Chunk the docs into Postgres full-text search with ranking. No vector database, no embedding model, nothing new to run. Embeddings can come later if retrieval quality measurements say they are needed.
- **Fine-tuning:** not in V1, as the brief says. We collect the eval dataset first.
- **Screenshots:** rendered by an isolated headless-browser worker that can only open the platform's own origin with a short-lived, read-only, user-scoped token. Needs a browser in the image (adds size). Phase 8.

## 6. Resource reality check

This development environment has 2 GB RAM, 2 CPUs and no Docker. The brief's 4-5 GB target cannot be validated here at full size. Plan: build llama.cpp from source here, run the largest model that fits, and report measured RSS, KV-cache growth, tokens per second and latency as measurements on this machine, not as claims about the target hardware. Anything not measurable here is labelled so.

## 7. Phases (the brief's order)

1. Runtime and chat: llama.cpp build, `ai-runtime` container and health API, model verification, admin status, side-panel chat with streaming and page context. Real model smoke test and measurements.
2. Typed tool registry and structured tool calls with schema validation, over existing handlers (read tools first).
3. Risk classes, per-class admin switches, AI audit table and admin activity page, secret scrubbing, rate limits.
4. Device, alert, gateway, audit-log and system-health tools that map to real endpoints.
5. RAG over the docs.
6. Deterministic multi-step workflows (offline devices vs gateways, site investigation) with the model doing intent and narration only.
7. Approval UI with impact text, remediation policy (AUTO / APPROVAL / NEVER), off by default. No service-restart tools (see section 3).
8. Reports through the existing generator with model-supplied definitions; screenshots worker.
9. Performance: warm model, context and thread limits, streaming, cancellation, measured.
10. Packaging: compose service, air-gapped bundle including the model, installer steps, `-check-config` additions.

Eval harness (600+ cases across normal, ambiguous, invalid, security, tool-selection, multi-step) is built alongside phases 2-6. Unauthorized-action rate is tested deterministically against the policy engine (it must be zero regardless of model output). Intent and tool-selection accuracy are measured with the real model where it fits, and labelled as measured on this machine.

## 8. Honest status today

Phase 1 (runtime, model, chat UI) is built; phases 2-10 are not. Detail in docs/ai-runtime.md.

| Item | State |
|---|---|
| llama.cpp built (CPU, portable flags) and Qwen3-1.7B Q4_K_M run on the 2 GB / 2 CPU dev machine | Measured, this machine only |
| Model emits a correct structured tool call for a simple typed tool | Observed once with a hand-written tool schema (not a test, not the registry) |
| `airuntime` front: /health /ready /version /model /metrics, keyed and allow-listed model API | Built, unit-tested against a fake llama-server; also run in front of the real one |
| Streaming API (`POST /v1/assistant/chat?stream=1`) and `GET /v1/ai/status` | Built, unit-tested; run against the real model |
| Chat side panel (streaming, markdown, steps, confirm cards, stop, page context, status dot) | Built, unit-tested helpers, screenshot-checked |
| ai-runtime container image and compose profile | Written, **never built or run** (no Docker on the dev machine) |
| Typed tool registry (`internal/aitools`; select with `AI_TOOL_MODE=typed|generic|auto`): 15 tools (13 READ, 2 LOW_RISK_WRITE), strict argument validation, risk fixed per tool, agrees with the policy by test | Built, unit-tested (bad-argument table, policy agreement, no path to approval/users/secrets). Not covered by a DB-backed integration test yet (the proposal-to-confirm path is covered for the generic tool). Shown to local runtimes instead of `api_request`; hosted models keep `api_request` until measured |
| Real model with typed tools: "How many alerts are open right now?" | Run once on the dev machine: called `list_alerts`, answered correctly (1 open), 78 s end to end on this CPU. One question, not an eval. |
| Existing generic `api_request` tool with a 1.7B model | Observed to build a malformed path on a simple question; the policy refused it. This is the reason for phase 2 (typed tools). |
