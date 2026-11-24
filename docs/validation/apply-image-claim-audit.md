# Image matrix assertion-to-claim audit

Audited 2026-11-23 against accepted source58fdeb91afa665a8860fd221ec38fc1fe7b8e3c7 and run https://github.com/abhisekrath00-ux/iot-platform/actions/runs/45178581694 . This record is local, not additional image acceptance.

## What actually ran

- Complete historical0574e75b8a25de5e7589d5933aca0266e10fc32b and current58fdeb9 source checkouts were built with their server Dockerfiles. Runtime required exact local image IDs and --pull=never for application containers. The source revisions were fixed, but base/dependency tags were not all immutable, so this is not a reproducible-build attestation.
- Four separate databases/volumes: old-to-new and new-to-old, each baseline and SQL-guarded. Each case starts one API, creates synthetic tenant/user/site/active gateway with no device/profile fixture, publishes once, manually ages created_at by one day, removes API, starts other API against the same running PostgreSQL container and retained volume, then GET and one POST.
- This is sequential API image replacement against a retained running DB. It is not a DB container restart, backup/restore, native updater, service manager, crash during publication, concurrent mixed writer, retained independent-store adoption, or owner deployment test. Embedded edge package build success is not Windows install/runtime acceptance.

## Accepted assertions and limits

| Assertion | Supported claim | Limit |
| --- | --- | --- |
| Exact image ID inspected and run | Those built local application images executed | No owner image or deployed provenance attestation |
| Only internal network membership, no gateway, no host bindings | Fixture used internal-only Docker topology and private direct host IPs | No adversarial egress or host-firewall penetration test; build/pull phase needed network |
| Authenticated TCP SELECT1, broker CONNACK, API health200 | Those readiness paths worked before Apply | Health is DB ping, not all background services |
| First POST202 and one observed config packet | Synthetic config publication accepted and reached observer | Empty device list, no executor, no physical side effect |
| Observer tenant, expected topic, nonempty signature, distinct push IDs | Distinct signature-bearing configuration envelopes observed | No independent Ed25519 verification, serial/version/digest/content/expiry verification by observer, or device acceptance |
| Manual age mutation then GET status expired and second POST202, two distinct IDs | Baseline permits another publication after aged sent history | This is an OPEN rearm control, not a safety success |
| Guarded GET sent, second POST409, exact row JSON equal | Guarded fixture retained the complete row through that sequential replacement and did not admit that POST | Manual age change precedes row snapshot; no proof for all other rows/tables or all operations |
| No second MQTT packet in0.5s guarded observation | No extra packet observed in that bounded interval | Not proof of no delayed delivery ever |
| Function exists, named trigger enabled, unique index valid | Named catalog objects and flags remained present | Accepted run did not compare full function/trigger/index definitions, language, trigger binding/type or predicate |
| schema_migrations count93 | Ledger still had93 rows | Accepted run did not compare filename set, applied times or checksums |
| Four result rows and successful removal commands | All four cases completed and disposable resources cleanup commands succeeded | No owner resources touched; no native rollback/restore proof |
| Secure-selection legacy updater refusal self-test | Actual unchanged guard refused before temp fixture file changes | Not successful updater execution |

Normal CI's real mTLS/compiled-ingest/compiled-edge tests are separate evidence. They do not add cryptographic verification, mTLS or physical acceptance to this image matrix.

## Local stronger assertions, not yet executed in images

The unpublished harness now validates reviewed SQL function body, language/security/result/config; trigger table/function binding, type/args/qualification/internal/enabled; index columns/predicate/uniqueness/readiness/validity; full before/after definitions; and all93 migration filenames. Real PG14 temporary-schema transaction exercised the catalog query and rejected22 changed/missing fields, then rolled back. Python compile/self-test and docs tests passed. These do not establish the stronger assertions on PostgreSQL16 replacement images until that actual matrix executes.

## Claim corrections required

Use: four actual sequential API image/retained-running-DB controls, exact guarded row, catalog presence/flags, ledger count93, signature-bearing observed envelopes, bounded0.5s guarded silence.

Do not use for the accepted run: exact catalog identity, exact migration filename set, independently verified signed envelopes, complete config retention, successful native upgrade/rollback/restore, enabled-policy/store deployment acceptance, or hardware acceptance.

Remote tip remains58fdeb9, all5 jobs green. The limitations narrow the interpretation, not the observed job result. No additional dispatch is authorized by this audit.
