# Iskra MC 3x0 (MC 320 / MC 330 / MC 350 / MC 350H) - Modbus register map provenance

Template `iskra-mc3x0` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- "Measuring centres MC320, MC330, MC350 & MC350H, User's Manual" Ver 8.00
  (K_MC3x0x_GB_22444000, 83 pages), Appendix A "MODBUS PROTOCOL" (register tables, data
  type decoding, serial defaults), from
  https://www.iskra.eu/f/docs/67294/K_MC3x0x_GB_22444000_Usersmanual_Ver_8.00.pdf

No third-party copies were used. (At capture time iskra.eu was intermittently unreachable
from the build sandbox; the PDF bytes are the Wayback Machine capture of the same vendor
URL.)

## Conventions - all vendor-stated

1. Read function: the measurement map lives in the 3xxxx range and the manual states "The
   memory reference for input and holding registers is 30000 and 40000 respectively".
   Input registers are read with FC 04.
2. Register base: listed numbers are Modicon references; on-wire (PDU) address = listed
   minus 30001 (frequency 30105 -> PDU 104).
3. Word order: the "Data types decoding" examples print 32-bit values MSW first (T5
   "123456*10^-3 = FD01 E240(16)"). Template word order `abcd`.
4. Data types: T5 = unsigned measurement (signed 8-bit decade exponent + unsigned 24-bit
   mantissa, value = mantissa x 10^exponent); T6 = same with signed mantissa; T1 =
   unsigned 16-bit. The edge agent's `t5`/`t6` decoder types (added with template #29)
   return the physical value in base units (V, A, W, var, VA, Hz) directly.
5. Factory serial settings (manual, settings chapter): Modbus address default 33, baud
   default 115200, parity default None, stop bits default 2. Surfaced as a hint with an
   explicit Apply button; the installer's edited values are never silently replaced.
6. REGISTER MAP SELECTION: holding register 40100 selects between two measurement maps
   ("Value 0 is MC7X0 compatible register map. Value 1 is MI71X0 compatible register
   map."). The manual does NOT state the factory default. This template uses the
   MC7x0-compatible map (40100 = 0); the provisional text requires the installer to
   confirm that register before trusting values.

## What is verified line-by-line against Appendix A, register table for the actual
## measurements (MC7x0 RM Comp. columns)

| Point     | Modicon | PDU | Description                    | Type | Scale | Unit |
|-----------|---------|-----|--------------------------------|------|-------|------|
| freq      | 30105   | 104 | Frequency f                    | T5   | dyn   | Hz   |
| v1        | 30107   | 106 | Voltage U1                     | T5   | dyn   | V    |
| v2        | 30109   | 108 | Voltage U2                     | T5   | dyn   | V    |
| v3        | 30111   | 110 | Voltage U3                     | T5   | dyn   | V    |
| v_avg_ln  | 30113   | 112 | Average phase voltage          | T5   | dyn   | V    |
| v12       | 30118   | 117 | Phase-to-phase voltage U12     | T5   | dyn   | V    |
| v23       | 30120   | 119 | Phase-to-phase voltage U23     | T5   | dyn   | V    |
| v31       | 30122   | 121 | Phase-to-phase voltage U31     | T5   | dyn   | V    |
| v_avg_ll  | 30124   | 123 | Avg. phase-to-phase voltage    | T5   | dyn   | V    |
| i1        | 30126   | 125 | Current I1                     | T5   | dyn   | A    |
| i2        | 30128   | 127 | Current I2                     | T5   | dyn   | A    |
| i3        | 30130   | 129 | Current I3                     | T5   | dyn   | A    |
| i_neutral | 30132   | 131 | Neutral current In             | T5   | dyn   | A    |
| i_total   | 30138   | 137 | Total current I                | T5   | dyn   | A    |
| p1        | 30142   | 141 | Real power P1                  | T6   | dyn   | W    |
| p2        | 30144   | 143 | Real power P2                  | T6   | dyn   | W    |
| p3        | 30146   | 145 | Real power P3                  | T6   | dyn   | W    |
| p_total   | 30140   | 139 | Total real power P             | T6   | dyn   | W    |
| q1        | 30150   | 149 | Reactive power Q1              | T6   | dyn   | var  |
| q2        | 30152   | 151 | Reactive power Q2              | T6   | dyn   | var  |
| q3        | 30154   | 153 | Reactive power Q3              | T6   | dyn   | var  |
| s1        | 30158   | 157 | Apparent power S1              | T5   | dyn   | VA   |
| s2        | 30160   | 159 | Apparent power S2              | T5   | dyn   | VA   |
| s3        | 30162   | 161 | Apparent power S3              | T5   | dyn   | VA   |
| thd_u1    | 30182   | 181 | THD U1                         | T1   | 1     | %    |
| thd_u2    | 30183   | 182 | THD U2                         | T1   | 1     | %    |
| thd_u3    | 30184   | 183 | THD U3                         | T1   | 1     | %    |
| thd_i1    | 30188   | 187 | THD I1                         | T1   | 1     | %    |
| thd_i2    | 30189   | 188 | THD I2                         | T1   | 1     | %    |
| thd_i3    | 30190   | 189 | THD I3                         | T1   | 1     | %    |

"dyn" = the scale is carried in each reading's decade exponent (T5/T6). THD is T1 on this
model family (unsigned 16-bit, whole percent) - NOT the MC 7x0's T16 with scale 0.01.

## Excluded, and why

- Total reactive power (30148) and total apparent power (30156): the manual's type codes
  for these two rows conflict with the family's own type definitions - Q_total is printed
  T5 (unsigned) where every other reactive value is T6 (signed, and export makes Q
  negative), and S_total is printed T7, which the same manual defines as the two-sign-byte
  power-factor format. These read as copy errors in the manual; rather than guess, both
  totals are excluded. Per-phase Q and S are consistent across the manual and included.
- Power factor (30164-30170, T7): two independent sign dimensions (import/export and
  inductive/capacitive) - not representable as one honest number.
- Energy counters (T3 with runtime exponent registers), maximum demand, angles,
  normalized-measurement table, MI71x0-compatible map variant, and all setup registers.

## Tested vs untested

- Register-evidence vitest asserts every address above, the T1-vs-T16 THD difference, the
  excluded totals, the serial hint, no overlapping register spans, and YAML with
  `func: 4` / `word_order: abcd`. The t5/t6 decode itself is unit-tested in Go against the
  vendor's worked examples (see template #29 / iskra-mc7x0.md).
- NOT tested on real hardware: no MC 3x0 was ever connected. The map-selection register
  40100 default is not stated by the vendor - the UI's provisional text requires
  confirming it. The provisional text in the UI says all of this.
