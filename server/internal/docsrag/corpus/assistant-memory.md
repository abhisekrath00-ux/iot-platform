# Explicit private assistant memory (first unit)

Built October 7, 2026. This is an API-first, opt-in private-note store with offline
lexical retrieval. It is not automatic transcript memory, a vector database, or
enterprise-ready long-term memory. No extra service or internet is needed.

## User control

Only an unscoped interactive user session can manage its own notes. API keys,
assistant requests and customer-scoped sessions are refused. Disabled by default.

- `GET /v1/assistant/memory`: your notes and enabled flag; usable as a JSON export.
- `PUT /v1/assistant/memory/settings` with `{"enabled":true}` or `false`.
- `POST /v1/assistant/memory` with `{"title":"Pump label","content":"Pump label is Orchid","retention_days":30}`.
- `DELETE /v1/assistant/memory/{id}`: delete one of your notes.

The Assistant page now has private-memory controls: deliberate provider/privacy
acknowledgement before enabling, explicit save with retention, disable retrieval,
authenticated JSON export and per-note delete with a separate review step.
No chat is harvested. Use an interactive session, not an API key. Do not put passwords, tokens,
other credentials or sensitive third-party data in notes. Notes are explicit text
saved by you, not a model inference or a copy of chat. Storage uses the existing
Postgres database, whose disk encryption/backup access remains the operator's job.

Limits: 50 notes per user/tenant, title 120 bytes, content 2000 bytes, retention 1-90
days (default 30). Owner row locking serializes quota checks across replicas.
Disabling prevents retrieval but keeps notes for export/delete. Expired notes are
excluded and purged on that owner's next management/retrieval access, not by a timed
purger; untouched expired rows and old backups can remain on disk. Deletion removes
the active row, not copies in backups. No completeness claim for backup erasure.

## Retrieval safety

The model has a read-only `search_user_memory` tool in both small/full mode. It accepts
only `query`; it cannot specify another owner, enable/save/delete notes, or harvest chat.
Current database identity, role, disabled status and customer scope are rechecked at
retrieval. Tenant and user filters apply. Notes saved under a different role are not
retrieved after a role change; the owner can still see/delete them in the management API.

BM25 searches at most 50 explicit notes and returns at most three snippets with note ID
and title. Empty/disabled search is empty, not invented memory. Backend failure is an
error and does not fall back to other users' data. Results are marked UNTRUSTED CONTEXT,
explicit provenance and a warning against treating text as instructions, approval or
permission. They are tool results, never a privileged system prompt. The existing
action policy/confirmation/four-eyes gates still enforce effects. This does not prove
that a real model will ignore all injected text or phrase every answer safely.

Audit stores settings/save/delete events with IDs and retention only, not note content.
No cross-user admin memory endpoint, automatic summarization, embedding calls or remote
memory service exists. Existing hosted model selection can transmit retrieved snippets
to that configured provider if the user asks the assistant to use memory. Keep a local
provider for air-gapped/private deployment. No new external fallback is introduced.

## Tests and retrieval eval

Real Postgres API tests: default disabled, explicit enable/save, bounds, API-key refusal,
cross-user/tenant search and deletion isolation, role-change exclusion, customer-scope
refusal, no owner parameters, disable/export/delete, quota and expired-row purge, no
content in audit. Policy tests deny generic management to models. Retrieval primitive
handles invalid result limits without panic.

Product-doc retrieval eval: eight source-recall queries (model chooser, failure boundary,
search, backup, register maps, offline deploy, SAML, fleet), expected source within top
five; unknown term returns no evidence. These are lexical retrieval checks, not answer
accuracy, real-model security tests, representative relevance metrics or a vector-vs-BM25
benchmark. Corpus freshness test compares embedded docs byte-for-byte.

UI tested: React/jsdom consent gate, delete review/cancel, visible backend error and
UTF-8 byte/retention bounds; full web suite and production build. Rendered UI screenshots
use fixture API responses, not a real logged-in deployment.

Still pending: server-managed conversation history, edit/bulk delete, background
expiry and identity-deletion cleanup, encryption-specific design, real-model injection
and no-answer eval, broader source-quality/semantic retrieval evaluation, durable agent
run recovery. No real Windows/Docker/hardware or remote CI claim. Vector store choice
remains unresolved until retrieval measurements justify one within the CPU/RAM target.
