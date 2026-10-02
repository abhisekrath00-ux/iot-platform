# Connectors: STM32, PLCs, SCADA and field devices

The edge agent reads devices through drivers selected by `profile` in its config.
Every driver is config-driven: new sensor models are added as profiles in the
dashboard (Profiles page), not in code. All drivers validate each value against
the point's `min`/`max` and drop out-of-range samples as errors.

| Profile | Transport | Use for | Status |
|---|---|---|---|
| `modbus-generic` | Modbus RTU, RS-485/RS-232 serial | Meters, sensors, drives | Unit + round-trip tests. Needs hardware pilot. |
| `modbus-tcp` | Modbus TCP | PLCs, meters with Ethernet, serial-to-Ethernet bridges, SCADA RTUs | Tested against an in-process Modbus TCP server (reads, u32 decode, redial). |
| `opcua` | OPC UA client (`opc.tcp://`) | SCADA servers and PLCs: Kepware, Ignition, Siemens S7-1500, Beckhoff, open62541 | Tested against an in-process OPC UA server (no security). Secure modes (sign, sign-and-encrypt) require a pinned `server_cert` (PEM or DER; expired, not-yet-valid or unreadable certs are refused, and there is no trust-on-first-use): unit-tested option handling only, not yet against a secured server. The channel is encrypted to the pinned key, but this relies on the gopcua library's handshake; verify against your real server during the pilot. |
| `snmp` | SNMP v2c / v3 authPriv (UDP 161), read-only GET | UPS, PDUs, switches, HVAC, power meters with SNMP | Tested against an in-process v2c agent (numeric types, text numbers, scale, range check, wrong community, missing object) and config refusals (v3 md5/des refused, secrets only via env vars). v3 handshake is NOT tested against any agent; no real device tried. No SET, no traps, no walk/table support, numeric OIDs only (no MIB names). |
| `iec104` | IEC 60870-5-104 client over TCP (default 2404), read-only | Substation RTUs, protection relays, SCADA outstations | Tested against an in-process outstation: STARTDT, general interrogation, types 1/3/9/11/13 and time-tagged 30/31/34/35/36, sequence addressing, IV/NT/OV quality flags rejected, other station's data ignored, negative confirm, range checks. Not tested on a real RTU or relay. No commands (no C_SC/C_SE), no spontaneous/event reporting, no counters (M_IT), no TLS (IEC 62351), no redundancy groups: reads by polling interrogation only. Config: `address` = common address, points use `ioa`. |
| `bacnet` | BACnet/IP unicast ReadProperty(present-value), UDP 47808, read-only | HVAC controllers, VAVs, chillers, building meters | Tested against an in-process BACnet simulator that I wrote from the standard's encoding (real, unsigned, signed, boolean, enumerated; object types ai/ao/av/bi/bo/bv/mi/mo/mv; error PDUs; replies with a routed source specifier; garbage input). Because the simulator and the client share one author's reading of the spec, treat real-device compatibility as UNTESTED. No Who-Is/discovery, no BBMD/foreign device, no routed-network addressing, no segmentation, no COV, no WriteProperty, no BACnet/SC. Point `key` is `<type>:<instance>`, e.g. `ai:12`; `host` is the device's IP. |
| `dnp3` | DNP3 (IEEE 1815) master over TCP (default 20000), read-only class 0 integrity poll | Utility RTUs, SCADA outstations, water/power telemetry | Tested against an in-process outstation that I wrote from the spec: link-layer framing with CRC (header CRC checked against a published vector), transport reassembly, multi-fragment responses with application confirm, g1v2 binary, g20 counters, g30 analog (32/16-bit and float), index ranges, IIN error bits, offline-flag rejection, range checks, corrupt-CRC rejection. The simulator shares one author's reading of the spec, so real-outstation compatibility is UNTESTED. No SELECT/OPERATE (cannot control equipment), no unsolicited responses, no event classes 1-3, no time sync, no secure authentication (SAv5), no serial/UDP. Config: `address` = outstation link address (master is 1); point `key` is `ai`/`bi`/`ctr`/`bo` and `register` is the point index. |
| `coap` | CoAP (RFC 7252) client over UDP (default 5683), confirmable GET only | Constrained sensors and gateways exposing resources | Tested against an in-process CoAP server I wrote: piggybacked and separate responses, retransmission after a dropped request, token matching, 4.xx errors, plain-number and JSON (dotted field) bodies, range checks, malformed packets. Real-device compatibility UNTESTED. No DTLS (so use only on trusted networks), no Observe, no block-wise transfer (payload must fit one datagram), no CBOR/SenML, no writes (no PUT/POST), no CoAP server ingest for devices that push. Point `key` is `/path` or `/path#field`. |
| `serial-json` | UART / USB-CDC text lines | STM32, Arduino, ESP32, nRF, any firmware that prints readings | Line parser unit-tested. Needs a board in the loop for a full pilot. |
| `modbus-energy-meter`, `door-contact` | Legacy serial profiles | Kept for existing gateways | Unit tests. |

## STM32 and other microcontrollers

Print one line per sample from your firmware. Three formats are auto-detected:

```
{"temp":23.4,"hum":51,"door":true}     JSON object (point key = field name)
temp=23.4,hum=51                        key=value
23.4,51,1013                            CSV (point key = column index: "0", "1", ...)
```

STM32 HAL example (USART2 or USB CDC):

```c
char buf[64];
int n = snprintf(buf, sizeof buf, "{\"temp\":%.2f,\"hum\":%.1f}\n", temp_c, rh);
HAL_UART_Transmit(&huart2, (uint8_t*)buf, n, 100);
```

Agent config (COM3 on Windows, `/dev/ttyACM0` or `/dev/ttyUSB0` on Linux):

```yaml
- id: stm32-1
  profile: serial-json
  port: /dev/ttyACM0
  baud: 115200
  data_bits: 8
  interval: 5s
  points:
    - {id: temp, key: temp, unit: C, min: -40, max: 125}
```

The agent drains the port each poll and keeps the latest value per point, so the
firmware can stream at its own rate. For devices without a serial link (ESP32
with Wi-Fi, STM32 with Ethernet) use Modbus TCP or OPC UA on the firmware side,
or publish to the gateway's local Modbus TCP server; a direct MQTT publisher
path is on the roadmap.

## Any HTTP device: push readings

Any device or bridge that can make an HTTPS request can push readings for a
device that was onboarded in the UI (Add device, or Profiles for the point map).
Create an API key with the `telemetry` scope and operator role (Settings), then:

```bash
curl -X POST https://HOST/api/v1/telemetry/ingest \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"device_id":"esp32-1","readings":[{"point":"temp","value":23.4,"event_id":"esp32-1-000123"}]}'
```

Rules: the device must belong to the key's tenant, every point must be
registered on the device, values are range-checked against the point's min/max,
`ts` (RFC 3339, optional, max 30 days old, max 5 minutes ahead) defaults to the
server time, and a repeated `event_id` is stored once, so retries are safe.
Batches over 500 readings get 413. The response lists accepted count and the
index and reason of each rejected reading. Alert rules and flows run on these
readings exactly as for MQTT. Tested by an integration test (tenant isolation,
range, duplicate, role, size limit); not yet load-tested.

## PLCs and SCADA (southbound: reading from them)

Modbus TCP:

```yaml
- {id: plc-1, profile: modbus-tcp, host: 192.168.1.50, address: 1, interval: 10s,
   points: [{id: temperature, register: 100, func: 3, type: i16, scale: 0.1, unit: C, min: -40, max: 150}]}
```

OPC UA (all points are read in one batched request; the session redials after errors):

```yaml
- id: scada-1
  profile: opcua
  endpoint: opc.tcp://192.168.1.60:4840
  security: sign-and-encrypt          # none | sign | sign-and-encrypt (Basic256Sha256)
  client_cert: /etc/hexmon/opcua-client.pem
  client_key: /etc/hexmon/opcua-client.key
  server_cert: /etc/hexmon/opcua-server.pem   # required for sign / sign-and-encrypt: pin the server's certificate (PEM or DER)
  username: hexmon
  password_env: OPCUA_PASSWORD        # password comes from the environment, never the file
  interval: 10s
  points:
    - {id: boiler_temp, node_id: "ns=2;s=Boiler.Temp", unit: C, min: 0, max: 400}
```

The agent refuses to start a device with `sign`/`sign-and-encrypt` and no client
certificate, or a username without a password source; it never silently falls
back to an insecure mode. Default is `none`, which is acceptable only on an
isolated plant network. Server certificate pinning/trust lists are not
implemented yet.

The agent is read-only toward PLCs and SCADA. Writes (actuation) go through the
command path with approval and four-eyes gating described in `security.md`.

## Feeding SCADA, historians and BI (northbound: reading from Hexmon)

- `GET /v1/export/telemetry.csv?device_id=...&point_id=...&hours=24` streams raw
  samples (bounded: 720 h, 200k rows, audited, spreadsheet-formula-safe).
  Works with Excel/Power Query, Grafana CSV, Ignition and historian importers.
- `GET /v1/telemetry/latest`, `/v1/telemetry/series` for JSON polling.
- Report CSV/HTML download: `GET /v1/reports/{id}/download?format=csv|html`.
- Flows can forward to webhooks; MQTT topics (`t/<tenant>/g/<gateway>/telemetry`) can be
  bridged by the broker to a plant MQTT/Sparkplug consumer. A native OPC UA server
  and Sparkplug B publisher are not built.

## Onboarding flow

1. Profiles page: create a profile for the protocol (registers, OPC UA node IDs or STM32 field names, ranges).
2. Add device wizard: claim the gateway, choose the profile, enter the connection (COM/port/host/endpoint).
3. Download `edge-agent.yaml` (`GET /v1/gateways/{id}/edge-config`), or push it as a fleet config release.
4. The live preview shows readings as soon as the gateway polls.

## Direct HTTPS devices (per-device token)

Devices that can make HTTPS calls (ESP32, cellular modems, third-party bridges) can post readings without an edge agent.
An admin mints a token for one device (`POST /v1/devices/{id}/tokens`, shown once, 1-730 days, revocable with
`DELETE /v1/devices/{id}/tokens/{tid}`); the device sends:

```
POST /v1/device/ingest
Authorization: Bearer hxd_<id>.<secret>
{"readings":[{"point":"temp","value":21.5,"event_id":"abc-1","ts":"2026-10-02T09:00:00Z"}]}
```

The token fixes tenant and device (a body naming another device is ignored), can do nothing else, and only its
sha256 is stored. Same validation as the MQTT path: registered points only, min/max range check, idempotent event ids,
max 500 readings per request. Status: integration-tested (mint, ingest, cross-device, revoke, expiry, no secret in
list or storage). Not tested: real devices, TLS termination (put it behind the platform's TLS proxy), and there is no
per-token rate limit yet, so rely on the proxy for that.

## Modbus writes (off by default, safety-gated)

A gateway writes to a device only when ALL of these hold: the command was requested by one person and approved by a
different person in an interactive session (API keys can never approve); the envelope is unexpired (5 min), not replayed,
and `modbus.write` is in the gateway's `allowed_commands`; the agent runs with `command_mode: modbus`; and the
register is listed under that device's `writes:` with a finite `min < max` (values outside it are refused before any
frame is built). Supported: coil (func 5), single register (6), multiple registers (16), u16/i16/u32/i32/f32 with word
order and scale. The device's echo is verified; an exception or mismatched echo is reported as failed.

```yaml
command_mode: modbus
allowed_commands: [modbus.write]
devices:
  - id: vfd1
    profile: modbus-tcp
    host: 10.0.0.20
    address: 1
    writes:
      - {id: speed_setpoint, register: 100, func: 6, type: u16, scale: 0.1, unit: Hz, min: 0, max: 50}
```

Request: `POST /v1/commands {"device_id":"vfd1","gateway_id":"...","action":"modbus.write","parameters":{"point":"speed_setpoint","value":42.5}}`,
then approve by a second user. Status: tested with fake serial and TCP peers and the real gate (allowlist, replay,
expiry, malformed parameters, refusals). NOT tested on real equipment; no read-back verification beyond the echo;
the maintenance interlock and measured-outcome items in docs/hazard-analysis.md are not built. Do not use on
safety-relevant equipment yet.

## Sparkplug B (MQTT, ingest side)

The ingest service can subscribe to Sparkplug B (`spBv1.0/...`) NBIRTH/NDATA/NDEATH/DBIRTH/DDATA/DDEATH and store numeric
and boolean metrics. It is OFF unless `SPARKPLUG_TENANT=<tenant id>` is set on the ingest service.

- Mapping: platform device id = `<edge_node>` for node metrics, `<edge_node>:<device>` for device metrics; point id =
  the metric name. The device and point must already be onboarded in the UI; unknown ones are dropped, values are
  range-checked, event ids are deterministic so duplicates are harmless.
- Aliases from BIRTH messages are remembered per edge node/device (in memory; a BIRTH replaces the table, DEATH
  clears it). After an ingest restart, alias-only DATA is dropped until the next BIRTH. Not a shared subscription.
- Security: Sparkplug topics carry no tenant, so one ingest bridge feeds one tenant, and the broker ACL must allow only
  your Sparkplug nodes to publish under `spBv1.0/<group>/#`. Without that ACL any broker client could write readings.
- Not built: publishing anything (no NCMD/DCMD, no rebirth request, no STATE/primary-host handling), datasets,
  templates, bytes/strings (skipped), historical metrics (skipped), Sparkplug on the edge agent.
- Status: decoder and mapping tested with a protobuf encoder written for the tests (types Int8-64/UInt8-64/Float/
  Double/Boolean, aliases, truncation fuzz, timestamp plausibility). NOT tested against a real Sparkplug node or
  Ignition/Cirrus Link; the encoder and decoder share one author's reading of the spec.

## LoRaWAN (via your network server's HTTP integration)

The platform does not run a LoRaWAN network server and does not touch radio frames. Use ChirpStack or The Things Stack
(or any server with an HTTP uplink webhook) to join, decrypt and decode the payload, then point its HTTP integration at
`POST /v1/lorawan/uplink` with the header `Authorization: Bearer hxd_...` (one per-device token, minted as in the
direct HTTPS section). The decoded fields (ChirpStack v4 `object`, TTS v3 `uplink_message.decoded_payload`) become
readings; nested objects flatten with `_`, booleans map to 0/1, strings and arrays are ignored, and only fields that
exist as registered points for that device are stored. The network server's timestamp is used when valid and plausible.
Status: integration-tested with both JSON shapes, bad/missing tokens and payloads with no numeric fields. Not tested
against a real ChirpStack/TTS instance or real sensors. Downlinks (commands to LoRaWAN devices) are not built.
