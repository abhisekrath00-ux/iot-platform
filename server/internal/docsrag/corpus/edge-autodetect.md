# Edge auto-detect

The edge agent finds devices by itself. The server does not have to ask. Everything it does is read-only, and it
works with no server, no broker and no internet (air-gapped).

## What it does

On first start (about 45 seconds after the agent is up, so polling and the broker come first) and then every
`autodetect.interval` (default 6h, minimum 15m), the agent runs one pass:

| Pass | How | Reads or sends |
|---|---|---|
| Serial Modbus RTU | Every serial port the OS lists (plus `serial_ports`), skipping ports a configured device polls. Tries 9600/19200/38400/4800/115200 baud with none/even/odd parity (8 settings, most common first), sweeps slave addresses `serial_from`..`serial_to` (default 1-32), stops at the first setting where a slave answers. | Modbus read functions 3 and 4 only |
| Profile matching | Each responding slave is scored against the profiles this gateway knows: it reads the first 3 registers of each profile and checks they fall inside that profile's plausible ranges. | Reads only |
| Local network | Private IPv4 subnets of this machine (never wider than a /24, container bridges skipped), or `lan_cidrs`. TCP connect to Modbus TCP, OPC UA, IEC 104, DNP3, IEC 61850/S7, MQTT ports. | TCP connects, no payload |
| BACnet | Who-Is broadcast to each local subnet and 255.255.255.255. | One broadcast packet |

Profiles known offline: two built in (Selec MX300 and Selec MFM383A-C, from `docs/devices/`), any `*.yaml` in
`<data>/profiles.d/` (one profile per file: `id`, `name`, `match` points, optional full `points` list), and a cache of
the tenant's profiles saved from the last server-requested scan (`<data>/scan-candidates.json`; match-only).

Results are kept in `<data>/discoveries.json`.

## Where you see them

- Local page (`http://127.0.0.1:8088`): "Detected by this gateway".
- `edge-agent -discoveries` prints the last result; `edge-agent -detect-now` runs one pass now and prints it.
- Dashboard Scan page: when the broker is reachable the agent publishes each changed result on `scan/result` with a
  scan id starting `auto-`. The server stores it as a finished proposal (`requested_by = edge-autodetect`), shown under
  "Detected by the gateway on its own". The operator picks a profile and presses Add, as with any scan. A result that
  could not be published (offline) is retried every minute and published when the broker comes back. Unchanged results
  are not published again.

## Safety

- Read-only. No writes, no config registers, no outputs.
- Nothing is polled until someone adds it. The server never adds from an auto result by itself.
- Server side: tenant and gateway come from the topic, the gateway must exist for that tenant, the kind must be
  `modbus-rtu`, `lan` or `bacnet`, the result must be under 512 KB, at most 30 per gateway per hour, and only the newest
  20 per gateway are kept. The Add endpoint still re-validates that the connection was in the result.
- A serial sweep talks on a shared RS-485 bus. Other masters on the same bus may see the traffic. Turn it off with
  `autodetect.serial: false` on buses where that matters.
- LAN scanning stays on private ranges, so the agent cannot be pointed at third parties.

## Config

```yaml
autodetect:
  enabled: true        # default true
  interval: 6h         # 15m minimum
  serial: true
  lan: true
  bacnet: true
  serial_from: 1
  serial_to: 32
  serial_ports: []     # extra ports besides the ones the OS lists
  lan_cidrs: []        # instead of the local subnets
  auto_add: false      # default false
```

## auto_add

Off by default; the dashboard confirmation is the default path. With `auto_add: true` the agent adds a device to
polling on its own only when all of these hold: Modbus RTU; exactly one profile scored 100% on all 3 probed registers
(no tie); that profile has a full polling definition on this gateway (built-in or a `profiles.d` file with `points`;
server-cache profiles never qualify); the port/address is not already configured; the finding is not ignored.
The device goes into `<data>/autodetected-devices.yaml`, never into your config file, and polling restarts. Delete
a device from that file to stop it. LAN and BACnet findings are never auto-added.

## Bug found while building this

`go.bug.st/serial` numbers stop bits as an enum where `1` means 1.5 stop bits. The agent passed the configured count
straight through, so `stop_bits: 1` (what scans and downloaded configs produce) asked the OS for 1.5 stop bits, which
Linux drivers reject ("stop bits invalid or not supported"). That was the error seen in the earlier end-to-end scan,
which was blamed on the pseudo-terminal. Fixed with an explicit mapping (0 or 1 means 1, 2 means 2) and a test.

## What was and was not verified

- Unit tests with fake ports, fake LAN and BACnet scanners: detection, port skipping, switches, ranges, ambiguity
  rules, payloads, store, auto-add rules, library loading. Server: integration test for the ingest rules and caps.
- Run against a pseudo-terminal pair with a Modbus simulator I wrote (a Selec MX300 register map): detect, confident
  match, auto-add, polling restart and decoded readings (230.5 V, 4.25 A, 50.01 Hz) all worked offline. The simulator
  encodes my reading of the datasheet, so it proves the pipeline, not the meter.
- Not tested: a real RS-485 adapter and real meter (so baud and parity discovery against real timing is unproven; a
  pty ignores baud and parity), real LAN or BACnet devices, Windows, arm64 hardware.
- Sweep time: with no device on a port it takes up to about 40 seconds per setting at 32 addresses, so a silent port
  costs about 5 minutes. The pass has a 25 minute limit. Raising `serial_to` raises this proportionally.

## Ignoring a proposal

`edge-agent -ignore KEY` (key shown by `-discoveries`) marks it ignored: it is not auto-added and not sent to the server
again. `-ignore KEY -ignore-undo` reverses it. The local page is read-only, so this is a CLI action.

## Not built

SNMP is not part of the loop (it needs a community string from the environment; `-scan-snmp` still works by hand).
mDNS in this repo finds the Hexmon server, not devices, so it is not a device pass.
