# Direct MQTT devices (certificate and password auth built, telemetry-only)

Status: **proposal**. Today every device reaches the platform through an edge
agent (serial, Modbus, OPC UA). Network-capable microcontrollers (ESP32,
STM32 with an Ethernet or Wi-Fi stack) could publish straight to the broker.
This note records the design and what must be decided first. The certificate path is now built (see below); the rest is still design.

## Built and tested (certificate path)

- `POST /v1/enrollment/tokens` accepts `"kind":"direct"`; the gateway row stores `kind` (migration 0025, default `edge`).
  The claim flow is the same as for gateways (claim code + CSR, CN = serial).
- The broker ACL for a direct device allows only `write t/<tenant>/g/<gateway>/telemetry`: no reads, no cmd, diag or fleet topics.
  Unit-tested, including that edge gateways keep their command topics.
- Ingest already takes tenant and gateway from the broker-enforced topic and rejects a payload that names a different one (tested).
  A payload with no `observed_at` is now stamped with receive time and quality `estimated` (tested).
- No command path exists for direct devices.
- **Password option (owner decision: offer both, admin chooses).** Off by default per tenant. An admin turns it on in
  Settings > Direct MQTT devices (with a security warning shown), then can create a device with username = serial and a
  random secret shown once. Only a PBKDF2-SHA512 hash (mosquitto `$7$` format) is stored. Rotate and revoke are in the UI;
  turning the policy off removes the tenant's password devices from the broker files. Same telemetry-only ACL as the
  certificate path. Endpoints: `GET/PUT /v1/direct-auth/policy`, `POST /v1/direct-devices/password`,
  `POST /v1/direct-devices/{id}/rotate|revoke`, `GET /v1/direct-devices`. Server env `BROKER_DIRECT_PASSWD_FILE` is the
  generated password file; it must be loaded by the broker (mosquitto allows one password file per listener, so merge it
  with the service-user file in your deploy hook, then SIGHUP). The hash format follows mosquitto's documented `$7$`
  layout but was NOT verified against a running mosquitto here. Use TLS on the listener: the secret is otherwise sent in clear.

## Not built

- Per-client publish rate and payload size limits at the broker (set them in the broker config; not generated or tested here).
- Any test against a real ESP32/STM32 or a real broker with TLS: UNTESTED on hardware.

## Why it needed a design first

The broker ACL today is generated per claimed gateway from the certificate CN
(`server/internal/brokeracl`). A direct device is a new kind of broker
identity, and a weak one: small MCUs often cannot hold a private key safely,
cannot rotate certificates, and cannot verify long certificate chains cheaply.
Security is first-class here, so the identity model comes before any code.

## Proposed model

1. **One virtual gateway per direct device.** The device gets its own gateway
   record (kind `direct`), so it reuses `t/<tenant>/g/<gateway>/telemetry`,
   the existing ingest path, tenant isolation and audit, with no new topic
   shape.
2. **Identity.** Preferred: per-device client certificate issued by the site
   CA (`scripts/gen-ca.sh`), CN = serial, same ACL generator as gateways.
   Fallback for MCUs that cannot hold a key: username + per-device random
   secret over TLS (server-authenticated), stored hashed in the password
   file, revocable per device. The fallback is a weaker tier and should be
   off by default and labelled in the UI.
3. **ACL.** Publish only to its own `.../telemetry`; read only its own
   `.../cmd`. Direct devices get **no** command execution until the control
   path for them has its own hazard analysis (`docs/hazard-analysis.md`);
   start telemetry-only.
4. **Payload.** Same JSON envelope the edge agent sends (`event_id`,
   `device_id`, `point_id`, `observed_at`, `value`, `unit`, `quality`,
   `schema_version`). MCUs without a reliable clock send no `observed_at`;
   ingest then stamps receive time and marks quality `estimated`.
5. **Enrollment.** Same one-time claim-code flow as gateways, surfaced in the
   onboarding UI as "Network device", producing either a certificate bundle
   or a credential file to flash.
6. **Limits.** Per-client publish rate limit and max payload size at the
   broker; ingest already validates envelopes and must also check that the
   payload's gateway and tenant match the topic (to verify before building).

## Open decisions (need the owner)

- (Decided: both are offered, admin chooses.)
- Which MCU families and TLS stacks are in scope (mbedTLS on STM32, ESP-IDF)?
- Time source: NTP on site, or accept receive-time stamps?

## Test plan before it counts as done

Broker ACL unit tests for the direct identity (cannot cross tenants or
gateways), an ingest test that rejects a payload whose tenant or gateway
differs from its topic, a rate-limit test, and one real ESP32 or STM32 soak
test against a lab broker.
