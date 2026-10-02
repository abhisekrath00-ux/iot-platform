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

## Easier onboarding (enroll string, slave scan)

- **Enroll string.** Creating a claim code now also returns `enroll_string` (`hexmon-enroll:1:...`), shown once in the
  wizard and in Settings > Direct MQTT devices. On the gateway, `edge-agent -enroll '<string>'` redeems it: no server URL,
  code or serial to type. The string contains the one-time code, so treat it as a secret. The server URL comes from
  `API_PUBLIC_URL`, which must be set to the address the edge box can reach. Tested (encode/parse, server output).
- **Modbus slave scan (edge, read-only).** `edge-agent -scan-port COM3 -scan-baud 9600 -scan-from 1 -scan-to 247
  -scan-func 4 -scan-register 0` tries one single-register read per address and lists the ones that answer. It never
  writes. Use a register the meter model really has (for the Selec meters see their device pages); a slave that rejects the
  register is not listed. Tested against a simulated bus only. Not wired into the dashboard; run it on the edge box.
- **Still manual:** the server address in `API_PUBLIC_URL`, DNS or a fixed IP for the server, site CA and TLS certificates,
  and each device's serial settings (port, baud, parity) or IP. Not built: mDNS discovery of the server, LAN discovery of
  IP devices, and a no-code zero-touch enrollment (deliberately not offered: a claim code or QR is the minimum safe step).

## Finding the server and devices on the local network

- `edge-agent -discover` sends one mDNS query for `_hexmon-api._tcp` and prints the addresses that answer. The server
  advertises only when `MDNS_ADVERTISE=true`. It suggests an address; it never enrolls or sends a claim code, because
  anything on the LAN can answer multicast. Confirm the address, then enroll with the claim code as usual.
  Tested with hand-built packets (query, compressed names, truncation, pointer loops); not tested against Avahi/Bonjour
  or across VLANs. Multicast is often blocked between subnets; type `-claim-api` then.
- `edge-agent -scan-lan 192.168.1.0/24` TCP-connects to Modbus TCP (502), OPC UA (4840), IEC 104 (2404), DNP3 (20000),
  102, and MQTT (1883/8883). Private ranges only, /22 or smaller. An open port means something listens, not that it is
  a supported device. `edge-agent -scan-bacnet 192.168.1.255` broadcasts a BACnet Who-Is and lists the I-Am answers (device instance, vendor id); parser tested with hand-built I-Am frames incl. routed ones, not against real devices, and broadcasts do not cross routers or VLANs. `edge-agent -scan-snmp 192.168.1.0/24` reads sysName/sysDescr over SNMPv2c using the community in `$SNMP_SCAN_COMMUNITY` (clear text on the wire, as v2c always is; private ranges only). Only the input guards are unit-tested; the probe itself has not been run against a real or simulated agent. The scan runs on the edge box, because only it can see the
  plant network; the dashboard cannot scan on its behalf. The serial Modbus slave scan is `-scan-port`.

## Onboarding the later network drivers from the dashboard

SNMP, BACnet/IP, IEC 60870-5-104, DNP3, CoAP and IEC 61850 can now be chosen in Sensor profiles (point addressing per
driver: OID, key, IOA, or key + index) and Onboarding (host, port, address, SNMP version). The server validates the
shape and renders the gateway config; the agent re-validates. SNMP secrets are never entered in the UI: you give the
name of an environment variable set on the gateway. Status is unchanged: these drivers are tested against simulators
I wrote, not real devices, and the read-only port test and live preview still only cover Modbus RTU. Not exposed in the
UI: DNP3/IEC 104 unsolicited reporting, BACnet COV, per-driver TLS options.

## Scan, then one-click add (dashboard)

Open **Scan** in the dashboard, pick a gateway and what to scan:

- **Serial Modbus**: the gateway sweeps slave addresses on one serial port (read functions only). Every responder is then
  probed with the first registers of each of your Modbus profiles (up to 20, first 3 points each). A suggestion is
  shown when those registers answered inside the profile's min/max range, e.g. "Selec MFM383A (100% of 3 registers
  plausible)". That is a plausibility score, not proof of the model: two meters with similar ranges can both score
  well, so you confirm the profile. A slave that rejects the probed registers with a Modbus exception is not detected.
- **Network**: private range, Modbus TCP / OPC UA / IEC 104 / DNP3 / IEC 61850-102 / MQTT ports. Pick a profile of the
  matching driver and add. Open port only means "something listens".
- **BACnet/IP**: Who-Is broadcast; list devices, pick a bacnet profile, add.

Nothing is added until you press Add. The server checks that the device you add was in that scan's results, that the
connection is valid for the profile's driver, and that the same port/address (or host) is not already on the gateway.
After adding, download the gateway config again (or push it as a fleet config) so the agent starts polling.

How it works: API validates and publishes a read-only request on `t/<tenant>/g/<gw>/scan`; the agent re-validates, runs
one scan at a time (8 minute limit) and answers on `scan/result`; ingest stores it in `gateway_scans` under the same
topic-identity rule as diagnostics. Scan requests need the admin or operator role.

Limits, honestly:
- Tested with fake serial buses and unit/DB tests; never run against a real RS-485 bus, Selec meter or BACnet device.
  The page is type-checked and built but I have not looked at it in a browser.
- A serial scan fails with a clear error if another device on the gateway is already polling that port (the agent opens
  the port per probe). Stop that device or scan before adding devices to the port.
- Existing gateways need a regenerated broker ACL (the `scan` topics) and an agent upgrade before scans work.
- A wide serial range is slow: up to 5 register probes per address at 400 ms each.
- No SNMP scan from the dashboard (it needs a community secret); use `-scan-snmp` on the box.
