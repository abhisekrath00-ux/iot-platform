# Search reliability and memory direction

October 7, 2026. Search is several separate paths, not one vector system:

- Product documentation: embedded heading chunks ranked by BM25 (`internal/docsrag`),
  no network, no embedding model or vector database. Sources include document/heading.
- Entity global search: optional Elasticsearch client (`internal/search`) for devices
  and alerts, tenant filter. Missing backend returns 503, not a Postgres search fallback.
  Users can use normal device/alert lists. Customer-scoped users cannot use global
  search: customer middleware already denies it, now pinned by a regression and handler guard.
- Telemetry index sink: optional outbound Elasticsearch/OpenSearch bulk delivery,
  separate from entity search. Source of truth remains Postgres; see search-sink.md.
  Retention/deletion sync and real-cluster tests remain open.
- Chat: request-provided web history and bounded linked-channel context, not a durable
  tenant memory service. Durable user-approved memory retrieval remains future work.

## Entity search hardening in this unit

No redirects, 8-second timeout unchanged, index name/query/tenant checks, escaped
indexed document IDs, checked marshal/request errors, require status 200, response
limited to 1 MiB, require hits shape and at most 25 hits. Every returned document
must declare the requested tenant; any mismatch fails the whole result rather than
passing partial potentially unsafe data. API hides backend diagnostics and suggests
normal lists when unavailable. No new outside service, dependency or spending.

Tests use a fake HTTP endpoint: tenant query filter, valid/empty hits, error status,
malformed shape/JSON, oversized response, wrong/missing tenant, invalid query/index,
redirect refusal, malformed URL, marshal error. API pins customer allowlist and
handler refusal plus invalid-query/backend-unavailable handling. Full local server
suite with real Postgres and targeted race tests run; no real Elasticsearch/OpenSearch
or distributed failure test. This is failure-first hardening, not proof of complete search.

## Next memory/retrieval unit

Before choosing a vector database or embeddings, define a retrieval eval covering
correct source, stale docs, no-answer, tenant/customer isolation, injection attempts,
retention/deletion and offline fallback. Audit all existing channel history paths.
Tenant memory must be opt-in with scope/retention/delete/export and explicit distinction
between user instruction and retrieved untrusted content. It must not turn stored
conversation into auto-approval or allow cross-user/tenant disclosures. Prefer existing
Postgres and offline retrieval within the 4-5 GB CPU target until measured retrieval
gaps justify another service. "elastic search, vector dB whatever in your plan" is
user direction to investigate and improve, not a settled architecture selection.

Still open: real-cluster integration, index mappings, entity delete/update freshness,
customer-filtered global search, memory storage/retrieval, semantic retrieval benchmarks,
durable agent runs, edge installation/templates and report/UI work. No enterprise-GA
or exhaustive failure coverage claim. Remote CI still unavailable (Actions minutes).
