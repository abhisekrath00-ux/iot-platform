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
