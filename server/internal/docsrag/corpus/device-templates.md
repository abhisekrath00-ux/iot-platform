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
3. Keeps the previous list, writes `managed-devices.yaml` atomically, and restarts (needs a service manager, as for remote restart).
4. Probation, 3 minutes: once the agent is connected to the broker the push is confirmed. If the file is unusable at start, or the agent never connects within probation, the previous list is restored and the agent restarts. A rolled-back version is never re-applied; a newer push is needed.

Managed devices replace local devices with the same id and add the rest.

## Footprint (measured, Linux amd64, no devices, no broker)
Stripped binary 13.3 MB (linux/arm 12.8 MB, windows/amd64 13.7 MB); idle RSS 12 MB, 7 threads. The push code adds no new dependency.

## Tested / not tested
Tested: signature, tenant, gateway, expiry, lifetime, checksum, unknown fields, bad YAML, bad fields, duplicate ids, ranges, version order, replay, opt-in, no key, probation confirm, rollback, first-push rollback, broken file at start, server push against Postgres with a fake broker (admin gate, one at a time, signature verifies, only devices travel), fixed signing vector on both sides.
NOT tested: a real broker and broker ACL, a real box restarting under systemd or a Windows service, real Modbus devices after a push, a push to a box with devices actively polling. UI: Fleet updates > Device templates card, seeded rows only (no real push).
