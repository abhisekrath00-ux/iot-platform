# Apply readiness batch, local work and proposed validation

This record is not activation, migration, merge, release or runner approval.
Remote review acceptance remains 58fdeb91afa665a8860fd221ec38fc1fe7b8e3c7.
The image claim audit controls the narrower existing replacement evidence.

## Work under way

A conservative new-install preflight now checks the exact candidate SQL guard,
a separately bound retained-store ID, only its identity marker in the store,
and zero database Apply history. It refuses every existing DB state, including
terminal receipts. It does not silently adopt legacy history. It does not install
SQL, provision storage, change defaults, publish MQTT or clear records. This is
a read-only observation, not a lease. Writer quiescence and a final guarded
admission check remain necessary. The directory check does not serialize another
writer or prove mount durability, and the identity must not be learned from the
store being checked. No product entrypoint calls this preflight yet.

13 local cases cover empty, missing guard, unbound/missing-marker stores, retained
intent, unknown files and all seven legacy states. Three race repeats with the
policy/read-status suites pass 4.423s; store primitive three race repeats pass
4.123s; vet passes. Fresh disposable DB full API passes 35.604s. An earlier full
API attempt on the reused sandbox DB failed with fixed-ID duplicates and stale
fixture state. That failure is retained, not counted as acceptance. The fresh DB
was explicitly created and dropped for the successful run.

The one-connection bounded Apply publisher already exists in the disabled
candidate. The OPEN PUBACK resend gap is in the legacy shared publisher. Do not
mistake another characterization pass for correction or change shared retained
manifest/diagnostic behavior as a side effect of choosing Apply transport.

## Next autonomous implementation and acceptance work

- Explicit offline preparation with independently retained expected ID. Refuse
  occupied stores; no startup provisioning and no reset/clear workflow.
- Disposable deployment binding and restart test: same independent ID and store
  across API replacement; missing/wrong/replaced store fail closed. Compare empty
  new-install vs existing legacy history and require reconciliation for the latter.
- Preserve separate retained history through DB restore, validate latest chain
  against DB and block stale or missing history before any broker effect.
- Complete isolated enabled-candidate end-to-end entrypoint/image acceptance.
  Use the bounded one-connection publisher and real broker/compiled ingest,
  inspect a nonempty configuration, independently verify envelope fields/signature,
  test PUBACK loss, terminal receipt progression and the exact retained attempt.
  Physical device execution remains a distinct untested boundary.
- Implement and test secure selected-deployment updater/rollback separately.
  Its current refusal is intentional. Existing four image cases are sequential
  API replacement on the same running DB, not native updater or archive restore.
- Keep single-host/local-filesystem scope explicit. Multi-replica/NFS acceptance
  requires work, not an inference from primitive locking tests.

## Publication and runner proposal, not executed

Finish the substantive batch and record fixed source hashes, all local results,
skips and actual-pixel evidence. Review assertion-to-claim mapping together.
Request review-only publication of one coherent tree, verify every remote blob,
then request only the necessary standard-runner runs after checking current free
eligibility. Existing strengthened image assertions and the new enabled-candidate
matrix have separate claims. No additional dispatch is currently authorized.
Do not create a sequence of one-failure dispatch patches.

## Owner-dependent acceptance

Actual selected-server topology, retained mount/permissions and legacy history
inventory; approval to quiesce writers and adopt/deploy; physical Windows fresh
installer/UAC/reboot/COM meter and real readings; joint tested server/installer
handoff. These are not needed for isolated implementation now. Native secure
updater, provisioning UX and deployment binding are implementation gaps first,
not reasons to offload unfinished engineering onto the owner.

No owner environment changes, broader token scope or spending are authorized by
this document. Trello editor retry remains held absent a new signal.


## Updated local checkpoint, November 24

Offline preparation/binding library, independent envelope verification in the
real mTLS/compiled-ingest fixture, and separate-process schema-archive refusal
are implemented and tested in separate fixtures. Exact limits and hashes are in
apply-deployment-binding-local.json and apply-process-binding-local.json.
None is installed in a product constructor or activation UI. Existing-history
reconciliation, physical mount lifecycle, operator preparation GUI, true shipped
API/image enabled-candidate matrix and secure native updater remain unfinished.

The coherent unpublished batch is now suitable for a scoped review publication
proposal, not owner readiness. Proposed checks before publication: freeze source
hashes; rerun final full local web/server/edge/build and applicable isolated race,
archive and mTLS cases; preserve skips and older failures; verify exact93 and
updater guards unchanged. Then request one review-only remote batch and normal
free standard-runner CI. Existing optional image matrix can validate its stronger
assertions only if separately authorized; it still cannot validate enabled-candidate
product deployment. No workflow expansion or dispatch is authorized here.
