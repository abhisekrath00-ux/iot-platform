# Iskra MC 7x0 (MC 740 / MC 750 / MC 760) - Modbus register map provenance

Template `iskra-mc7x0` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Sources (vendor domain only)

- Register map + serial defaults: "Measuring Centres MC 7x0, User's Manual" Ver 1.01
  (K_MC7x0A_EN_22440500, 189 pages), Appendix A "MODBUS communication protocol", from
  https://www.iskra.eu/f/docs/Power-monitoring-devices/K_MC7x0A_EN_22440500_Users_manual_Ver_1.01.pdf
- Data type definitions: "Measuring centres MC320, MC330, MC350 & MC350H, User's Manual"
  Ver 8.00 (K_MC3x0x_GB_22444000, 83 pages), Appendix A "Data types decoding", from
  https://www.iskra.eu/f/docs/67294/K_MC3x0x_GB_22444000_Usersmanual_Ver_8.00.pdf

No third-party copies were used. The MC 7x0 manual lists register addresses with Iskra
type codes but does not decode the type codes itself ("For the latest and complete MODBUS
table please visit ISKRA web page"); the MC 3x0 manual decodes the same type system, and
its register table lists identical addresses and type codes for the cross-checked points
(frequency 30105 T5, total real power 30140 T6, total PF 30164 T7), which is why the type
definitions are treated as applying to the MC 7x0 map.

## Conventions - all vendor-stated

1. Read function: the map lives entirely in the 3xxxx range and the manual states "The
   memory reference for input and holding registers is 30000 and 40000 respectively".
   Input registers are read with FC 04.
2. Register base: the listed numbers are Modicon references; the on-wire (PDU) address is
   the listed number minus 30001 (frequency 30105 -> PDU 104).
3. Word order: the type-decoding examples print the value MSW first, e.g. T5
   "123456*10^-3 = FD01 E240(16)" - register one carries 0xFD01 (exponent byte FD = -3,
   top mantissa byte 01), register two 0xE240. Template word order `abcd`.
4. Data types: T5 = unsigned measurement (signed 8-bit decade exponent in byte 0, unsigned
   24-bit mantissa in bytes 1-3, value = mantissa x 10^exponent). T6 = the same with a
   signed 24-bit mantissa. T16 = unsigned 16-bit with 2 decimal places (scale 0.01). The
   exponent is part of every T5/T6 reading, so no static scale could represent these
   points - the edge agent decodes them as the `t5`/`t6` types (this release) and the
   decoded value is already the physical value in base units (V, A, W, var, VA, Hz).
5. Factory serial settings (manual, communication settings chapter): Modbus address
   default 33 (range 1-247), baud default 115200, parity default None, data bits 8, stop
   bits default 2. The template surfaces these as a hint with an explicit Apply button;
   the installer's edited values are never silently replaced.

## What is verified line-by-line against Appendix A, register tables (1) and (2)

| Point     | Modicon | PDU | Description                        | Type | Scale | Unit |
|-----------|---------|-----|------------------------------------|------|-------|------|
| freq      | 30105   | 104 | Frequency                          | T5   | dyn   | Hz   |
| v1        | 30107   | 106 | Voltage U1                         | T5   | dyn   | V    |
| v2        | 30109   | 108 | Voltage U2                         | T5   | dyn   | V    |
| v3        | 30111   | 110 | Voltage U3                         | T5   | dyn   | V    |
| v_avg_ln  | 30113   | 112 | Average phase voltage              | T5   | dyn   | V    |
| v12       | 30118   | 117 | Phase-to-phase voltage U12         | T5   | dyn   | V    |
| v23       | 30120   | 119 | Phase-to-phase voltage U23         | T5   | dyn   | V    |
| v31       | 30122   | 121 | Phase-to-phase voltage U31         | T5   | dyn   | V    |
| v_avg_ll  | 30124   | 123 | Average phase-to-phase voltage     | T5   | dyn   | V    |
| i1        | 30126   | 125 | Current I1                         | T5   | dyn   | A    |
| i2        | 30128   | 127 | Current I2                         | T5   | dyn   | A    |
| i3        | 30130   | 129 | Current I3                         | T5   | dyn   | A    |
| i_neutral | 30132   | 131 | Neutral current (calculated)       | T5   | dyn   | A    |
| i_avg     | 30136   | 135 | Average current                    | T5   | dyn   | A    |
| p_total   | 30140   | 139 | Total real power                   | T6   | dyn   | W    |
| p1        | 30142   | 141 | Real power P1                      | T6   | dyn   | W    |
| p2        | 30144   | 143 | Real power P2                      | T6   | dyn   | W    |
| p3        | 30146   | 145 | Real power P3                      | T6   | dyn   | W    |
| q_total   | 30148   | 147 | Total reactive power               | T6   | dyn   | var  |
| q1        | 30150   | 149 | Reactive power Q1                  | T6   | dyn   | var  |
| q2        | 30152   | 151 | Reactive power Q2                  | T6   | dyn   | var  |
| q3        | 30154   | 153 | Reactive power Q3                  | T6   | dyn   | var  |
| s_total   | 30156   | 155 | Total apparent power               | T5   | dyn   | VA   |
| s1        | 30158   | 157 | Apparent power S1                  | T5   | dyn   | VA   |
| s2        | 30160   | 159 | Apparent power S2                  | T5   | dyn   | VA   |
| s3        | 30162   | 161 | Apparent power S3                  | T5   | dyn   | VA   |
| thd_u1    | 30182   | 181 | THD U1                             | T16  | 0.01  | %    |
| thd_u2    | 30183   | 182 | THD U2                             | T16  | 0.01  | %    |
| thd_u3    | 30184   | 183 | THD U3                             | T16  | 0.01  | %    |
| thd_i1    | 30188   | 187 | THD I1                             | T16  | 0.01  | %    |
| thd_i2    | 30189   | 188 | THD I2                             | T16  | 0.01  | %    |
| thd_i3    | 30190   | 189 | THD I3                             | T16  | 0.01  | %    |

"dyn" = the scale is carried in each reading's decade exponent (T5/T6); the decoder
returns the physical value directly, so the template's fixed scale is 1.

## Excluded, and why

- Power factor (30164-30170, T7): T7 packs an import/export sign byte AND an
  inductive/capacitive sign byte plus a 16-bit magnitude - two independent sign
  dimensions no single number can represent honestly. Excluded rather than approximated.
- Energy counters (30406-30453, T3): each counter's scale lives in a separate exponent
  register (30401-30404) that can change at runtime ("Actual counter is calculated:
  Cnt x 10^exponent"). A static scale would silently mis-decode; excluded.
- Maximum demand values, dynamic demand values, harmonics, unbalance/angle readings,
  min/max, load profile, and all setup/write registers: out of scope for the core
  monitoring template (and setup registers are never read by this read-only template).

## Tested vs untested

- Go: `t5`/`t6` decoding unit-tested against the vendor's own worked examples
  (FD01 E240 -> 123.456; FDFE 1DC0 -> -123.456), plus an end-to-end poll test over a fake
  RTU port asserting FC 04 and the decoded values above, and a test that the write path
  rejects the read-only types. Register-evidence vitest asserts every address above, the
  types, the serial hint, no overlapping register spans, and that YAML is emitted with
  `func: 4` / `word_order: abcd`.
- NOT tested on real hardware: the map, the encoding, and the serial defaults are
  vendor-stated but no MC 7x0 was ever connected. The provisional text in the UI says so.
