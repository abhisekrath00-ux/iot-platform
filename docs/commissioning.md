# Guided commissioning

The installer wizard takes a new sensor from box to live data in five steps.
Everything runs on-prem; no step needs internet access (air-gap safe).

## Steps

1. **Site & serial** - the operator picks the site and types the gateway
   serial. `POST /v1/commissioning/sessions` creates the gateway (pending),
   a 72h one-time claim code, and the tracking session. The dashboard shows
   the claim code once plus a QR code (`hexmon://claim?api=..&serial=..&code=..`).
2. **Claim** - the installer scans the QR on the gateway (or types the code);
   the edge agent redeems it via `POST /v1/enrollment/claim` and receives its
   ingest token and optional mTLS client certificate. Session state: `claimed`.
3. **Profile** - the operator assigns a device profile (sensor template)
   with `POST /v1/commissioning/sessions/{id}/profile`; the device and its
   validated points are created from the template. State: `profiled`.
4. **Port test** - `POST /v1/commissioning/sessions/{id}/port-test` sends a
   **read-only** Modbus probe (port, baud, address, function 1-4, register,
   count, type) to the gateway over `t/<tenant>/g/<gw>/diag`. The edge runs
   one poll and answers on `.../diag/result`; ingest records the outcome.
   State: `tested` or `failed` (with the exact error: wiring, address, baud).
5. **Live preview** - `GET /v1/commissioning/sessions/{id}/preview` returns
   the latest reading per point. First telemetry flips the derived state to
   `live` - commissioning is done.

## Safety properties

- The probe is read-only by construction: write functions (6, 15, 16) are
  rejected by the API and re-rejected on the edge, so the four-eyes
  actuation gate in docs/security.md is not invoked and cannot be bypassed.
- Results only land on sessions matching the broker-authenticated topic
  identity (tenant + gateway), same enforcement as telemetry ingest.
- Claim codes are single-use, expiry-checked, and stored as hashes.
- Every step writes an audit row (commission.start / .profile / .port_test).

`GET /v1/commissioning/sessions/{id}` derives the authoritative state from
evidence (gateway status, device linkage, test result, first telemetry), so
a skipped port test still ends `live` once data flows.
