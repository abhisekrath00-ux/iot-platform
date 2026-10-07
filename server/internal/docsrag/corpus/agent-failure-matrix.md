# Agent reliability: execution boundary and failure matrix

October 7, 2026. This is a bounded hardening unit, not an enterprise-readiness claim.

## Implemented in this unit

- Only tools actually advertised this turn may run, for native and structured-text calls.
  A typed-registry name is not callable merely because it exists elsewhere in the registry.
  Full mode retains its complete generic tool menu; small mode retains a short selected menu.
- Calls require JSON object arguments, at most 16 KiB, supported function type and bounded ID.
  Typed tools still apply their stricter schema. Existing real-handler RBAC and write policy remain.
- Repeated-call signatures canonicalize JSON key order/whitespace. Third identical call is refused.
- A batch larger than the remaining 25-call budget is rejected before any call in that batch.
  Existing 12-model-call, run-timeout and rate limits remain. Cancellation is checked before
  provider requests and before each execution.
- Catalog IDs for larger local models use full mode automatically instead of inheriting small
  workarounds just because the endpoint is local. Explicit admin small/full choice still wins.
- MCP HTTP request body capped at 64 KiB, JSON-RPC 2.0 required, tool-call envelope rejects
  unknown fields, missing name and non-object/missing arguments. 30-second request context.

## Failure cases

| Case | Current response | Evidence / remaining gap |
| --- | --- | --- |
| Native call not advertised | Refused and traced, never reaches tool executor | Scripted-provider test |
| Text fallback names unadvertised tool | Parser will not convert it to execution | Parser unit tests; no real-model accuracy guarantee |
| Invalid/large arguments | Refused before tool execution | Guard + scripted-provider tests |
| Huge tool-call batch | Whole batch refused, run returns error; no partial execution | Scripted-provider test |
| Repeats with reordered JSON | Same loop signature; third call refused | Canonical signature test |
| Cancelled run | No new provider request/tool execution after check | Cancelled-context test; UI Stop already aborts stream |
| Malformed/oversized MCP envelope | Bad request / JSON-RPC error | HTTP tests without database |
| Model/context/network failure | Existing timeout/error handling; context overflow trims history and retries once | Existing provider/stream tests, not chaos-tested deployment |
| Model unavailable | Core platform remains independent; deterministic /v1/ask available | Existing architecture, no new failover service |
| Proposed write / physical action | Existing confirmation, role and four-eyes gates unchanged | Policy security eval; no new auto-approval |
| Memory/search unavailable | Not addressed by this unit | Next research/build unit; no vector DB selected |
| Crash between model response and pending-write creation | Not claimed covered | Durable run ID/idempotency/recovery design pending |
| Multi-instance run rate limits | Existing in-process limit only | Shared coordination pending |
| Provider quota/rate limit | Existing configured fallback only | Multi-key design pending; never bypass provider rules |

## Latest requested work, still open

Enterprise-grade MCP/agentic memory and search, including "elastic search, vector dB whatever
in your plan", remains a goal, not a settled requirement to deploy a new database. Audit the
existing Postgres docs retrieval and optional search sink first; measure retrieval, tenant
isolation, deletion/retention, prompt-injection handling and CPU/RAM before deciding whether
embeddings or a vector store improve it. Preserve an offline deterministic fallback.

The same failure-first work continues for lightweight edge installation, sensor/product
connection and templates, rich report charts/views, multi-key settings and dashboard/simulation.
No claim of exhaustive scenarios, benchmarked 4-5 GB operation or enterprise GA is made.

## Verification limits

Go tests cover guards, scripted providers, protocol envelopes and existing security policy.
API and MCP suites also passed against a fresh real Postgres database, including existing
approval/RBAC/tenant-isolation tests. The broad no-DB server run skips other DB tests. No real-model eval, distributed deployment,
real hardware, real Windows or Docker stack was run for this unit. Remote CI unavailable
because Actions minutes are exhausted. No spending, billing change or retrigger push.
