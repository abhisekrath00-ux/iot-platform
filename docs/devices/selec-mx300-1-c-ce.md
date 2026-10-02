# Selec MX300-1-C-CE (single-phase energy / power meter)

Source: the manufacturer's operating-instructions leaflet (doc OP987-V02), supplied as phone photos. Values
marked "unverified" were hard to read or are not in the table. Nothing here has been run against a real meter.

## Link

RS-485, Modbus RTU, half duplex. Address 1-255 (factory 1), 300-19200 baud (factory 9600), parity none/odd/even
(factory none), 1 or 2 stop bits (factory 1), 500 ms max response, 500 m max cable. Serial only: connect through
the edge agent on an Ubuntu or Windows box with a USB-RS485 adapter (`/dev/ttyUSB0` or `COM3`).

## Measurements (read-only, float32, 2 registers each)

| Point | Protocol address | Documented | Unit |
|---|---|---|---|
| voltage | 0x00 (0) | 30000 | V |
| current | 0x02 (2) | 30002 | A |
| active_power | 0x04 (4) | 30004 | kW |
| reactive_power | 0x06 (6) | 30006 | kvar |
| apparent_power | 0x08 (8) | 30008 | kVA |
| power_factor | 0x0A (10) | 30010 | - |
| frequency | 0x0C (12) | 30012 | Hz |

Word order is the leaflet's default "mid-little endian" = `cdab`. The leaflet's worked example (1234.12 kWh as
0x449A43D7, low word 0x43D7 at 30090 and high word 0x449A at 30091) confirms that order, and a unit test decodes it.
**Open conflict:** the config table (second photo, item 13, display `End.I`) lists Endianness as LSRF/MSRF with factory
setting MSRF (most significant register first, which would be `abcd`), while the worked example calls mid-little
endian (low word first, `cdab`) the default. Both cannot be the factory state. Do one live read of voltage and
check the value is sane (about 230 V); if it is garbage, switch `word_order` to `abcd`. Switching the meter to big-endian (holding register 40070) means changing `word_order` to `abcd`.

Unverified: function code (30xxx suggests FC 04, input registers), unit of power (kW/kvar/kVA as printed, but the
leaflet resolution table is in W), and whether the total-energy register at 30090 exists on this model (it appears
only in the worked example, not in the register table). Check with one live read before relying on them.

## Edge agent config

```yaml
  - id: mx300-1
    profile: modbus-generic
    port: /dev/ttyUSB0      # COM3 on Windows
    baud: 9600
    data_bits: 8
    stop_bits: 0            # agent default = 1
    parity: none
    address: 1
    interval: 10s
    points:
      - {id: voltage,        register: 0,  func: 4, type: f32, word_order: cdab, unit: V,    min: 0, max: 600000}
      - {id: current,        register: 2,  func: 4, type: f32, word_order: cdab, unit: A,    min: 0, max: 10000}
      - {id: active_power,   register: 4,  func: 4, type: f32, word_order: cdab, unit: kW,   min: 0, max: 1000000}
      - {id: reactive_power, register: 6,  func: 4, type: f32, word_order: cdab, unit: kvar, min: -1000000, max: 1000000}
      - {id: apparent_power, register: 8,  func: 4, type: f32, word_order: cdab, unit: kVA,  min: 0, max: 1000000}
      - {id: power_factor,   register: 10, func: 4, type: f32, word_order: cdab, unit: "",   min: 0, max: 1}
      - {id: frequency,      register: 12, func: 4, type: f32, word_order: cdab, unit: Hz,   min: 40, max: 70}
```

## Not done on purpose

The configuration and relay registers (40000-40070: slave id, baud, CT/PT ratios, trip limits, factory default)
are writable on the meter and can trip a relay. The agent only reads; writes would need the platform's approval
plus four-eyes path and are not implemented.

## Display and config notes (second photo)

DIP keys 1-3 pick the display parameter: 000 auto/manual scroll, 001 voltage, 010 current, 011 power factor,
100 active power, 101 reactive power, 110 apparent power, 111 frequency. Config pages 11.3-15 cover current
hysteresis, current trip time, frequency over/under limits (45-65 Hz, factory 60/50), frequency hysteresis,
endianness, trip/alarm mode and factory default. The energy-register section of the leaflet is cut off in the photo,
so the energy register stays unverified.
