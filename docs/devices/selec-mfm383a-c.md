# Selec MFM383A-C (three-phase multifunction meter, RS-485)

Source: Selec "MFM383A Series Operating Instructions", OP623-V03, 4 pages (PDF supplied by the user). The communication
variant is the "-C" model (MFM383A without -C has no RS-485). Not run against a real meter.

## Link

RS-485, Modbus RTU, half duplex, address 1-255 (factory 1), 300-19200 baud (factory 9600), parity none/even/odd
(factory none), 1 or 2 stop bits (factory 1), 500 m max. Supports 3P-4W (default) and 3P-3W networks (holding
register 40001: 0 = 3P4W, 1 = 3P3W; on 3P3W only the line-line voltages and total values are meaningful).
Reach it through the edge agent on Ubuntu or Windows with a USB-RS485 adapter.

## Measurements (input registers, FC 04, float32, 2 registers each)

| Point | Parameter | Protocol address | Documented | Unit |
|---|---|---|---|---|
| v1n | V1-N voltage | 0x00 (0) | 30000 | V |
| v2n | V2-N voltage | 0x02 (2) | 30002 | V |
| v3n | V3-N voltage | 0x04 (4) | 30004 | V |
| v_ln_avg | Average L-N voltage | 0x06 (6) | 30006 | V |
| v12 | V1-2 voltage | 0x08 (8) | 30008 | V |
| v23 | V2-3 voltage | 0x0A (10) | 30010 | V |
| v31 | V3-1 voltage | 0x0C (12) | 30012 | V |
| v_ll_avg | Average L-L voltage | 0x0E (14) | 30014 | V |
| i1 | Current I1 | 0x10 (16) | 30016 | A |
| i2 | Current I2 | 0x12 (18) | 30018 | A |
| i3 | Current I3 | 0x14 (20) | 30020 | A |
| i_avg | Average current | 0x16 (22) | 30022 | A |
| kw1 | Active power L1 | 0x18 (24) | 30024 | kW |
| kw2 | Active power L2 | 0x1A (26) | 30026 | kW |
| kw3 | Active power L3 | 0x1C (28) | 30028 | kW |
| kva1 | Apparent power L1 | 0x1E (30) | 30030 | kVA |
| kva2 | Apparent power L2 | 0x20 (32) | 30032 | kVA |
| kva3 | Apparent power L3 | 0x22 (34) | 30034 | kVA |
| kvar1 | Reactive power L1 | 0x24 (36) | 30036 | kvar |
| kvar2 | Reactive power L2 | 0x26 (38) | 30038 | kvar |
| kvar3 | Reactive power L3 | 0x28 (40) | 30040 | kvar |
| kw_total | Total active power | 0x2A (42) | 30042 | kW |
| kva_total | Total apparent power | 0x2C (44) | 30044 | kVA |
| kvar_total | Total reactive power | 0x2E (46) | 30046 | kvar |
| pf1 | PF L1 | 0x30 (48) | 30048 | - |
| pf2 | PF L2 | 0x32 (50) | 30050 | - |
| pf3 | PF L3 | 0x34 (52) | 30052 | - |
| pf_avg | Average PF | 0x36 (54) | 30054 | - |
| freq | Frequency | 0x38 (56) | 30056 | Hz |
| kwh | Total active energy | 0x3A (58) | 30058 | kWh |
| kvah | Total apparent energy | 0x3C (60) | 30060 | kVAh |
| kvarh | Total reactive energy | 0x3E (62) | 30062 | kvarh |

Address 30064 (0x40) is the serial number (hex), not read here. Units for power and energy follow the manual's
labels (kW, kVA, kvar, kWh, kVAh, kvarh); the manual's resolution table says energy shows in kWh at 0.1k below a
PT x CT ratio of 150 and 1k above, which is a display resolution, not the Modbus scaling. Confirm units on first read.

## Word order

The manual's example box (total active energy 1234.12 kWh = 0x449A43D7) says **big endian (A-B-C-D) is the default**,
with mid-little endian (C-D-A-B) as the alternative, selectable in holding register 40070 (0 = LSRF, 1 = MSRF; config
page 15, factory MSRF). The profile uses `abcd`. The example's address 30090 does not exist in this meter's table;
the box is generic text, so ignore its address. Confirm with one live voltage read (about 230 V per phase).

## Edge agent config

```yaml
  - id: mfm383-1
    profile: modbus-generic
    port: /dev/ttyUSB0      # COM3 on Windows
    baud: 9600
    data_bits: 8
    stop_bits: 0            # agent default = 1
    parity: none
    address: 1
    interval: 10s
    points:
      - {id: v1n, register: 0, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v2n, register: 2, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v3n, register: 4, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v_ln_avg, register: 6, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v12, register: 8, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v23, register: 10, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v31, register: 12, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: v_ll_avg, register: 14, func: 4, type: f32, word_order: abcd, unit: "V", min: 0, max: 100000}
      - {id: i1, register: 16, func: 4, type: f32, word_order: abcd, unit: "A", min: 0, max: 20000}
      - {id: i2, register: 18, func: 4, type: f32, word_order: abcd, unit: "A", min: 0, max: 20000}
      - {id: i3, register: 20, func: 4, type: f32, word_order: abcd, unit: "A", min: 0, max: 20000}
      - {id: i_avg, register: 22, func: 4, type: f32, word_order: abcd, unit: "A", min: 0, max: 20000}
      - {id: kw1, register: 24, func: 4, type: f32, word_order: abcd, unit: "kW", min: -10000000, max: 10000000}
      - {id: kw2, register: 26, func: 4, type: f32, word_order: abcd, unit: "kW", min: -10000000, max: 10000000}
      - {id: kw3, register: 28, func: 4, type: f32, word_order: abcd, unit: "kW", min: -10000000, max: 10000000}
      - {id: kva1, register: 30, func: 4, type: f32, word_order: abcd, unit: "kVA", min: 0, max: 10000000}
      - {id: kva2, register: 32, func: 4, type: f32, word_order: abcd, unit: "kVA", min: 0, max: 10000000}
      - {id: kva3, register: 34, func: 4, type: f32, word_order: abcd, unit: "kVA", min: 0, max: 10000000}
      - {id: kvar1, register: 36, func: 4, type: f32, word_order: abcd, unit: "kvar", min: -10000000, max: 10000000}
      - {id: kvar2, register: 38, func: 4, type: f32, word_order: abcd, unit: "kvar", min: -10000000, max: 10000000}
      - {id: kvar3, register: 40, func: 4, type: f32, word_order: abcd, unit: "kvar", min: -10000000, max: 10000000}
      - {id: kw_total, register: 42, func: 4, type: f32, word_order: abcd, unit: "kW", min: -10000000, max: 10000000}
      - {id: kva_total, register: 44, func: 4, type: f32, word_order: abcd, unit: "kVA", min: 0, max: 10000000}
      - {id: kvar_total, register: 46, func: 4, type: f32, word_order: abcd, unit: "kvar", min: -10000000, max: 10000000}
      - {id: pf1, register: 48, func: 4, type: f32, word_order: abcd, unit: "", min: -1, max: 1}
      - {id: pf2, register: 50, func: 4, type: f32, word_order: abcd, unit: "", min: -1, max: 1}
      - {id: pf3, register: 52, func: 4, type: f32, word_order: abcd, unit: "", min: -1, max: 1}
      - {id: pf_avg, register: 54, func: 4, type: f32, word_order: abcd, unit: "", min: -1, max: 1}
      - {id: freq, register: 56, func: 4, type: f32, word_order: abcd, unit: "Hz", min: 40, max: 70}
      - {id: kwh, register: 58, func: 4, type: f32, word_order: abcd, unit: "kWh", min: 0, max: 1000000000000}
      - {id: kvah, register: 60, func: 4, type: f32, word_order: abcd, unit: "kVAh", min: 0, max: 1000000000000}
      - {id: kvarh, register: 62, func: 4, type: f32, word_order: abcd, unit: "kvarh", min: 0, max: 1000000000000}
```

## Not done on purpose

Holding registers 40000-40070 are read/write (password, CT/PT ratios, slave id, baud, parity, stop bit, factory default,
and the three energy resets 40012-40014, which erase the meter's energy totals). The agent only reads. Writing any of
them would need `modbus.write` with a `writes:` allowlist, four-eyes approval, and I would keep the energy resets off
that list. CT/PT ratios set on the meter already scale the readings; do not scale again in the platform.
