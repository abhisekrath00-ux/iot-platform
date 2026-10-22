# Lovato DMG600 / DMG610 - Modbus register map provenance

Template `lovato-dmg600` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- "DMG SERIES - Digital multimeters-analyzers, MODBUS Communication Protocol" I317IGB02_22
  (41 pages, bilingual IT/EN), from
  https://datasheet.lovatoelectric.com/DOWNLOAD/Technical/TechInstructions/28.%20CONTATORI%20DI%20ENERGIA%20E%20ANALIZZATORI%20DI%20RETE/I317IGB02_22.pdf
- "Digital multimeter, instructions manual DMG600-DMG610-DMG615-DMG611R...-DMG620"
  I411IGB02_22, from catalogue.lovatoelectric.com (P07 menu defaults).

No third-party copies were used. The protocol's Table 2 carries a per-model check column;
every included point is marked for the DMG6 family column.

## Conventions - all vendor-stated

1. Read function: FC 04 input registers. The protocol's worked read example uses function
   04; the Table 2 header states the measures are usable "with functions 03 and 04", so FC
   03 is a documented equivalent.
2. Register base: listed minus 1. The protocol states "the address in the query must be
   decreased by one from the effective address reported in the table", and the worked
   example shows it: L2 active power is listed at 0016H and the request carries 00 15.
   Template registers are stored as on-wire (listed - 1).
3. Word order: MSW first (abcd), shown by the worked example's response bytes
   00 01 FB 00 = 129792 (= 1.29792 kW at the documented W/100 resolution).
4. Formats: "Unsigned long" (u32) and "Signed long" (i32), 2 words per value; scales from
   the UNIT column: V/100, A/10000, W/100, Var/100, VA/100, PF/10000, Hz/1000 (the table
   marks Hz/100 as the DMG210-only variant).
5. Factory serial settings (manual P07 menu table): node address default 01, speed default
   9600 bps, data format default 8-bit no parity, stop bits default 1, protocol Modbus-RTU.
   Surfaced as a hint with an explicit Apply button; the installer's edited values are
   never silently replaced.

## What is verified line-by-line against protocol Table 2 (instantaneous block, DMG6 column)

| Point    | Listed | PDU | Description (manual)     | Format       | Scale  | Unit |
|----------|--------|-----|--------------------------|--------------|--------|------|
| v1       | 0002H  | 1   | L1 Phase Voltage         | Unsigned long| 0.01   | V    |
| v2       | 0004H  | 3   | L2 Phase Voltage         | Unsigned long| 0.01   | V    |
| v3       | 0006H  | 5   | L3 Phase Voltage         | Unsigned long| 0.01   | V    |
| i1       | 0008H  | 7   | L1 Current               | Unsigned long| 0.0001 | A    |
| i2       | 000AH  | 9   | L2 Current               | Unsigned long| 0.0001 | A    |
| i3       | 000CH  | 11  | L3 Current               | Unsigned long| 0.0001 | A    |
| v12      | 000EH  | 13  | L1-L2 Voltage            | Unsigned long| 0.01   | V    |
| v23      | 0010H  | 15  | L2-L3 Voltage            | Unsigned long| 0.01   | V    |
| v31      | 0012H  | 17  | L3-L1 Voltage            | Unsigned long| 0.01   | V    |
| p1       | 0014H  | 19  | L1 Active Power          | Signed long  | 0.01   | W    |
| p2       | 0016H  | 21  | L2 Active Power          | Signed long  | 0.01   | W    |
| p3       | 0018H  | 23  | L3 Active Power          | Signed long  | 0.01   | W    |
| q1       | 001AH  | 25  | L1 Reactive Power        | Signed long  | 0.01   | var  |
| q2       | 001CH  | 27  | L2 Reactive Power        | Signed long  | 0.01   | var  |
| q3       | 001EH  | 29  | L3 Reactive Power        | Signed long  | 0.01   | var  |
| s1       | 0020H  | 31  | L1 Apparent Power        | Unsigned long| 0.01   | VA   |
| s2       | 0022H  | 33  | L2 Apparent Power        | Unsigned long| 0.01   | VA   |
| s3       | 0024H  | 35  | L3 Apparent Power        | Unsigned long| 0.01   | VA   |
| pf1      | 0026H  | 37  | L1 Power Factor          | Signed long  | 0.0001 | -    |
| pf2      | 0028H  | 39  | L2 Power Factor          | Signed long  | 0.0001 | -    |
| pf3      | 002AH  | 41  | L3 Power Factor          | Signed long  | 0.0001 | -    |
| freq     | 0032H  | 49  | Frequency                | Unsigned long| 0.001  | Hz   |
| v_avg_ln | 0034H  | 51  | Eqv. Phase Voltage       | Unsigned long| 0.01   | V    |
| v_avg_ll | 0036H  | 53  | Eqv. Phase-Phase Voltage | Unsigned long| 0.01   | V    |
| i_avg    | 0038H  | 55  | Eqv. Current             | Unsigned long| 0.0001 | A    |
| p_total  | 003AH  | 57  | Eqv. Active Power        | Signed long  | 0.01   | W    |
| q_total  | 003CH  | 59  | Eqv. Reactive Power      | Signed long  | 0.01   | var  |
| s_total  | 003EH  | 61  | Eqv. Apparent Power      | Unsigned long| 0.01   | VA   |
| pf_total | 0040H  | 63  | Eqv. Power Factor        | Signed long  | 0.0001 | -    |
| i_neutral| 0048H  | 71  | Neutral Current          | Unsigned long| 0.0001 | A    |

## Power-resolution caveat (carried in the provisional text)

Protocol footnote 1: power measures are normally returned at 1/100 W resolution, but if the
maximum theoretical power per phase (from the CT/VT combination) exceeds 5 MW, ALL power
measures (W, var, VA) switch to 1/10 W resolution. The protocol documents register 2F70H
to query which scale is active (0000h = 1/100 W), available from specific SW revisions.

## Excluded, and why

- Energy counters: the 32-bit total/partial energy rows (1A20H-1A32H) are NOT marked for
  the DMG6 family column (DMG300/700/800/900 only), and the 64-bit rows (1B20H+) are
  "Unsigned long-long" - wider than any decoder type the edge driver has. Excluded rather
  than guessed.
- Neutral-Earth voltage (006AH), neutral current (006CH) and CosPhi (002CH-0030H): marked
  for DMG900 only. (The included i_neutral is the separate 0048H row, marked for DMG6.)
- Asymmetry rows (0042H-0046H): low charting value; skipped.
- MAX (HI), MIN (LO), AVG (AV) and MAX DEMAND (MD) blocks at 0400H-0A62H: statistics over
  the same measures, not instantaneous points.
- Harmonic tables (3100H+): DMG800/900 only.
- Date/time (28FxH), phase sequence (2074H/20F0H), power-scale query (2F70H), and ALL
  setup/command registers (Tables 3, 5-8 incl. FC 06/0x10 writes): not charted measurements.
