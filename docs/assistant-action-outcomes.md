# Assistant action outcomes: never replay ambiguous effects

Built October 7, 2026. This is an outcome-truth unit, not resumable agent execution.
No new service, internet dependency or automatic recovery write is introduced.

## State boundary

Previously, an action claim was labelled executed before the real handler ran. A crash
or result-storage error could leave a success-looking row with no recorded result.
The existing atomic pending claim already prevented double confirmation; this unit
corrects the outcome label and errors rather than claiming a discovered duplicate effect.

Pending changes now atomically become `executing` before execution. Only the real
handler's returned success plus a stored result makes `executed`. Returned 3xx/4xx
responses are `failed`; 5xx, cancellation during execution or lost result persistence
are `outcome_unknown`. Cancellation before execution records failed without invoking
the handler. This is conservative: 5xx does not prove that no database change committed.

Result recording uses an independent five-second context, so a disconnected browser
cannot by itself cancel that recording. A storage failure is surfaced as unknown,
not success. Claim-storage errors are unavailable, not a false not-found/already-decided.
The web confirmation endpoint returns 503 for unknown/unavailable; channel replies
say to check the target. Low-risk autorun uses the same outcome path. Unknown autorun
terminates the model loop so it cannot issue another turn that re-proposes that effect.
Existing human review, RBAC, default-deny policy and physical-control gates remain.

## Crash residue and operator review

An `executing` row older than ten minutes is presented as `outcome_unknown` in the
owner's action-list API, without replay or a new pending action. The stored claim
remains executing: ten minutes is a conservative display cutoff, not proof a worker
is dead or a distributed lease. Legacy executed rows without result_code are converted
to unknown by the idempotent migration. Never automatically replay either state.
Check the actual alert/comment/device target and audit before proposing a new change.
A lost browser response after a completed write is still ambiguous to that browser;
this unit does not provide response replay or client idempotency keys.

## Verification

Real Postgres tests hold a handler open to assert executing before result, race two
confirms and assert one effect, test success/4xx/5xx, pre-execution cancellation, lost
result-row persistence, stale/crash residue and no replay, DB unavailability and autorun
unknown terminating the model loop. Targeted race tests and the full Go server suite
passed on a newly created local test database. An earlier reused-database full run
failed an unrelated escalation test due to leftover old alert fixtures; fresh-database
run passed without weakening or changing that assertion. No actual process kill,
network partition, distributed deployment, real model, Docker/Windows/hardware or
remote CI green claim.

Still open: durable run identity/checkpoints, proposal deduplication across separate
requests, endpoint-specific idempotency, safe reconciliation UI, shared rate limits,
background crash cleanup and all semantic/model retrieval evaluation.
