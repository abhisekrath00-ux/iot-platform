# Device templates pushed to edge boxes

Chosen design: **server-managed, versioned, signed push with edge-side verify, probation and rollback.** This is the usual gateway pattern (desired state held centrally, a small agent that applies it and can undo it) and it works when the box is offline, because the agent keeps running the last good list on its own.

## What a template is
A device template is a device profile (Settings > Profiles: driver, register map, scale, unit, range) plus the device's own connection settings (port, address, host, interval). Create or change them on the server as before.

## Pushing
`POST /v1/gateways/{id}/config-push` (admin, interactive session only; not API keys, not the assistant, not customer-scoped users). The server renders the gateway's device list, signs it with the fleet key (`FLEET_SIGNING_KEY`, same key as release manifests) and sends it on `t/<tenant>/g/<gw>/config`. `GET` on the same path lists pushes with status: sent, applied, confirmed, rolled_back, rejected, failed, expired. One push at a time per gateway; versions only go up.

Only the device list travels. Broker, TLS, identity, outputs, rules and security settings stay in the box's own file and cannot be pushed.

## On the box
Opt-in: `managed_devices: true` in the box's config, plus `fleet_public_key`. The agent then:
1. Checks the signature, tenant, serial, expiry (max 24 h life), one use per push id, version newer than the applied one, checksum, size (256 KB, 200 devices).
2. Validates the list like a hand-written config (known fields only, unique ids, intervals, ranges, lint errors).
3. Keeps the previous list, persists a prepared state before replacing `managed-devices.yaml`, then reloads the polling supervisor in the same process. Startup restores an interrupted prepared write before loading the file. A malformed state file blocks startup rather than resetting version history.
4. Probation, 3 minutes: once the agent is connected to the broker the push is confirmed. If the file is unusable at start, or the agent never connects within probation, the previous list is restored and the polling supervisor reloads it. Failed file restoration is not reported as a successful rollback. A rolled-back version is never re-applied; a newer push is needed.

Managed devices replace local devices with the same id and add the rest.

## Footprint (measured, Linux amd64, no devices, no broker)
Stripped binary 13.3 MB (linux/arm 12.8 MB, windows/amd64 13.7 MB); idle RSS 12 MB, 7 threads. The push code adds no new dependency.

## Tested / not tested
Tested: signature, tenant, gateway, expiry, lifetime, checksum, unknown fields, bad YAML, bad fields, duplicate ids, ranges, version order, replay, opt-in, no key, probation confirm, rollback, first-push rollback, broken file at start, server push against Postgres with a fake broker (admin gate, one at a time, signature verifies, only devices travel), fixed signing vector on both sides.
NOT tested: a real broker and broker ACL, a real box restarting under systemd or a Windows service, real Modbus devices after a push, a push to a box with devices actively polling. UI: Fleet updates > Device templates card, seeded rows only (no real push).


## Source-setup development checkpoint (not released, 12 November 2026)

The new source Apply card is separate from saving a source and from seeing a
reading. It requires an interactive workspace admin, a review checkbox and the
current desired SHA256. A changed fingerprint returns 409 instead of sending a
stale review. Server readiness does not prove the edge opted in or is online.
Unknown send outcome blocks another send until saved status is recovered.

Applied/final results are retained in order on disk until the MQTT publish
succeeds. This is broker acknowledgement, not an end-to-end database receipt.
An interrupted write is rejected after restoring the previous list; it is not
called applied. Replayed/old versions are not accepted after rollback.

Current local compiled test uses a disposable loopback Mosquitto, the real edge
executable, real ingest executable and PostgreSQL, plus a synthetic Modbus TCP
server. It passed signed push -> same running process reload -> applied ->
confirmed -> measured value 110 and matching scoped HTTP status; stale reviewed
fingerprints were refused. Playwright fixtures passed review/send-once/status
and stale-reading refusal at 1440/390 px. Full local server/edge suites and 346
web tests passed before the latest recovery-only additions; focused recovery,
race and apply tests passed afterwards. This supersedes the old fake-broker-only
boundary for that synthetic pipeline, not for hardware or production deployment.

Not yet verified: this path on Windows, a physical RS-485/USB source, secured
production broker/ACL deployment, power-loss/fsync durability, or a database
outage after a successful broker publish. Confirmation means the agent loaded
settings while connected, not that a meter returned a correct/calibrated value.
