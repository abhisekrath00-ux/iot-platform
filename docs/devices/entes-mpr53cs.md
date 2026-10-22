# Entes MPR-53CS - Modbus register map provenance

Template `entes-mpr53cs` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- "MPR-53CS MODBUS Register Map" (2 pages, English), from
  https://www.entes.com.tr/uploads/files/MPR-53CS_Modbus_Register_Map_EN.pdf
- "MPR-53CS User Manual" A4396 R3 (16 pages), from
  https://www.enteselektronik.com/uploads/files/20210225-MPR_53CS-A4396-R3.pdf

No third-party copies were used.

## Conventions

1. Read function: FC 03 holding registers. Vendor-stated: the manual's MODBUS section shows
   a worked read example `01 03 00 00 00 02 ...` reading the L1 phase voltage.
2. Register base: listed addresses are the on-wire (PDU) addresses as printed. The worked
   example reads phase-1 voltage at address 0x0000, exactly where the map lists it.
3. Data width: 2 registers per measured value; the map's FORMAT column says "unsigned int"
   or "int" (32-bit), with the printed MULTIPLIER (0.1 / 0.001 / 0.01).
4. WORD ORDER IS NOT STATED in either document. The template sets
   `wordOrderUnverified: true`: the installer picks the word order below from the unit's
   behavior or a known reading (e.g. frequency or one voltage) against the meter's display
   before trusting any value. `abcd` is only the preselected suggestion, not a verified
   convention.
5. Values are primary-side: the meter applies its programmed CT/VT ratios (factory 1:1) and
   the register map's ranges are printed as "(0-3000)xUT", "(0-6000)xCT",
   "(-18000-18000)xCTxVT" etc. If ratios are programmed, Modbus readings come out already
   scaled by them; the multipliers in the table are the only scaling the template applies.
6. Factory serial settings (manual, "Factory Settings" + specifications): Modbus address 1,
   9600 baud, no parity, 8 data bits, 2 stop bits. Surfaced as a hint with an explicit
   Apply button; the installer's edited values are never silently replaced.

## What is verified line-by-line against the register map (instantaneous values)

| Point          | Dec | Hex  | Description (map wording)      | Format       | Multiplier | Unit |
|----------------|-----|------|--------------------------------|--------------|------------|------|
| v1             | 0   | 0000 | L1 PHASE VOLTAGE               | unsigned int | 0.1        | V    |
| v2             | 2   | 0002 | L2 PHASE VOLTAGE               | unsigned int | 0.1        | V    |
| v3             | 4   | 0004 | L3 PHASE VOLTAGE               | unsigned int | 0.1        | V    |
| i1             | 6   | 0006 | L1 PHASE CURRENT               | unsigned int | 0.001      | A    |
| i2             | 8   | 0008 | L2 PHASE CURRENT               | unsigned int | 0.001      | A    |
| i3             | 10  | 000A | L3 PHASE CURRENT               | unsigned int | 0.001      | A    |
| i_neutral      | 12  | 000C | NEUTRAL CURRENT                | unsigned int | 0.001      | A    |
| v12            | 14  | 000E | L1-L2 PHASE-PHASE VOLTAGE      | unsigned int | 0.1        | V    |
| v23            | 16  | 0010 | L2-L3 PHASE-PHASE VOLTAGE      | unsigned int | 0.1        | V    |
| v31            | 18  | 0012 | L3-L1 PHASE-PHASE VOLTAGE      | unsigned int | 0.1        | V    |
| p1             | 20  | 0014 | L1 PHASE ACTIVE POWER          | int          | 0.1        | W    |
| p2             | 22  | 0016 | L2 PHASE ACTIVE POWER          | int          | 0.1        | W    |
| p3             | 24  | 0018 | L3 PHASE ACTIVE POWER          | int          | 0.1        | W    |
| q1             | 26  | 001A | L1 PHASE REACTIVE POWER        | int          | 0.1        | var  |
| q2             | 28  | 001C | L2 PHASE REACTIVE POWER        | int          | 0.1        | var  |
| q3             | 30  | 001E | L3 PHASE REACTIVE POWER        | int          | 0.1        | var  |
| s1             | 32  | 0020 | L1 PHASE APPARENT POWER        | unsigned int | 0.1        | VA   |
| s2             | 34  | 0022 | L2 PHASE APPARENT POWER        | unsigned int | 0.1        | VA   |
| s3             | 36  | 0024 | L3 PHASE APPARENT POWER        | unsigned int | 0.1        | VA   |
| pf1            | 38  | 0026 | L1 PHASE COSj                  | int          | 0.001      | -    |
| pf2            | 40  | 0028 | L2 PHASE COSj                  | int          | 0.001      | -    |
| pf3            | 42  | 002A | L3 PHASE COSj                  | int          | 0.001      | -    |
| p_total_import | 44  | 002C | TOTAL IMPORT ACTIVE POWER      | int          | 0.1        | W    |
| p_total_export | 46  | 002E | TOTAL EXPORT ACTIVE POWER      | int          | 0.1        | W    |
| q_total_ind    | 48  | 0030 | TOTAL INDUCTIVE REACTIVE POWER | int          | 0.1        | var  |
| q_total_cap    | 50  | 0032 | TOTAL CAPACITIVE REACTIVE POWER| int          | 0.1        | var  |
| s_total        | 52  | 0034 | TOTAL APPARENT POWER           | unsigned int | 0.1        | VA   |
| freq           | 58  | 003A | FREQUENCY                      | unsigned int | 0.01       | Hz   |
| thd_u1         | 72  | 0048 | L1 PHASE VOLTAGE THD           | unsigned int | 0.1        | %    |
| thd_u2         | 74  | 004A | L2 PHASE VOLTAGE THD           | unsigned int | 0.1        | %    |
| thd_u3         | 76  | 004C | L3 PHASE VOLTAGE THD           | unsigned int | 0.1        | %    |
| thd_i1         | 78  | 004E | L1 PHASE CURRENT THD           | unsigned int | 0.1        | %    |
| thd_i2         | 80  | 0050 | L2 PHASE CURRENT THD           | unsigned int | 0.1        | %    |
| thd_i3         | 82  | 0052 | L3 PHASE CURRENT THD           | unsigned int | 0.1        | %    |

## Excluded, and why

- Energy counters (import/export active energy 1/2, inductive/capacitive reactive energy
  1/2, from 0x0056 on): the map prints FORMAT "long int" with RANGE 0-FFFFFFFFFFFFFFFF
  (a 64-bit span) at a register spacing of 2 - the two statements contradict each other,
  so no width is assumed and energies are left out entirely.
- Average inductive/capacitive cos-phi (54/56): separate sign-split averages with no single
  total power factor; skipped to keep the point set clean.
- Voltage/current angle registers (60-70): rarely charted, low value; skipped.
- Min., max. and max-demand registers (0x0076-0x00FE): writable/reset semantics (R/W),
  out of read-only polling scope.
- Pulse counters, digital I/O, run/spare hour counters: HEX or counter formats, skipped.
- All setup registers (0x8000+): configuration surface, never polled by a read template.

## Variants

The manual covers the CT-25 model variant (current via clamp CTs with a turn-number
setting). The Modbus map is the same; only the CT wiring differs.
