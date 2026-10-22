# Finder 7M.38 (three-phase DIN-rail energy meter/analyzer) - Modbus register map provenance

Template `finder-7m38` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Sources (vendor domain only)

- Register map + worked examples: "ModBUS COMMUNICATION PROTOCOL 7M24 - 7M38"
  (Modbus-7M24-7M38_v2_30062021.pdf, 58 pages), from
  https://cdn.findernet.com/app/uploads/Modbus-7M24-7M38_v2_30062021.pdf
- Factory serial defaults: "User's Manual 7M" (manual7M38_EN.pdf), communication
  settings / specification table, from
  https://cdn.findernet.com/app/uploads/manual7M38_EN.pdf

No third-party copies were used. The same protocol document covers the 7M.24 (single-phase)
and 7M.38 (three-phase); the template below uses ONLY rows from the 7M.38 "actual
measurements" table (the 7M.24's single-phase table lists the same addresses for its
subset - this template does not rely on it).

## Conventions - all vendor-stated

1. Read function: the map lives in the 3xxxx range and the protocol states "The memory
   reference for input and holding registers is 30000 and 40000 respectively". Input
   registers are read with FC 04 (the table's read column states 4 for every row).
2. Register base: the on-wire (PDU) address is the listed number minus 30000. The
   protocol's own request-frame example reads U1 (listed 30107) with request address
   bytes `00 6B` = 107 = 30107 - 30000. Registers below are stored at their on-wire
   values; no installer gate is needed because the base is stated AND demonstrated.
3. Word order: the type-decoding section's T5 example prints `123456*10^-3` stored as
   `FD01 E240` - register one carries 0xFD01 (exponent byte FD = -3, top mantissa byte
   01), register two 0xE240. MSW first; template word order `abcd`.
4. Data types (protocol's type-decoding section): T5 = unsigned measurement: byte 0 is a
   signed 8-bit decade exponent, bytes 1-3 an unsigned 24-bit mantissa, value =
   mantissa x 10^exponent. T6 = the same with a signed 24-bit mantissa. This is the same
   encoding the edge agent already decodes as the `t5`/`t6` types for the Iskra
   templates - Finder's worked example `FD01 E240 = 123456 x 10^-3` is bit-identical to
   the decoder's test vector. T16 = unsigned 16-bit with 2 decimal places (scale 0.01);
   T17 = signed 16-bit with 2 decimal places. The exponent is part of every T5/T6
   reading, so no static scale could represent those points - the decoded value is
   already the physical value in base units (V, A, W, var, VA, Hz).
5. Factory serial settings (7M User's Manual, specification table): RS485 Modbus RTU,
   Modbus address default 33, speed default 19200 bit/s, frame 8,N,2 (8 data bits, no
   parity, TWO stop bits). The template surfaces baud/parity/address as a hint with an
   explicit Apply button and presets stop bits to 2; the installer's edited values are
   never silently replaced.
6. Source caveat: the printed byte example in the protocol's request-frame page (a U1
   read) is internally inconsistent - its bytes do not decode to its printed value. The
   T5 type-definition example (`FD01 E240 = 123456 x 10^-3`) is unambiguous, matches the
   7M.24 table's identical rows, and is the example the decoder is tested against.

## Point map (7M.38 "actual measurements" table, on-wire = listed - 30000)

| Point | Listed | On-wire | Type | Decodes to |
| --- | --- | --- | --- | --- |
| freq | 30105 | 105 | T5 | Hz |
| v1 / v2 / v3 | 30107 / 30109 / 30111 | 107 / 109 / 111 | T5 | V |
| v_avg_ln | 30113 | 113 | T5 | V |
| v12 / v23 / v31 | 30118 / 30120 / 30122 | 118 / 120 / 122 | T5 | V |
| v_avg_ll | 30124 | 124 | T5 | V |
| i1 / i2 / i3 | 30126 / 30128 / 30130 | 126 / 128 / 130 | T5 | A |
| i_avg | 30136 | 136 | T5 | A |
| si | 30138 | 138 | T5 | A |
| p_total / p1 / p2 / p3 | 30140 / 30142 / 30144 / 30146 | 140 / 142 / 144 / 146 | T6 | W |
| q_total / q1 / q2 / q3 | 30148 / 30150 / 30152 / 30154 | 148 / 150 / 152 / 154 | T6 | var |
| s_total / s1 / s2 / s3 | 30156 / 30158 / 30160 / 30162 | 156 / 158 / 160 / 162 | T5 | VA |
| thd_v1 / thd_v2 / thd_v3 | 30182 / 30183 / 30184 | 182 / 183 / 184 | T16 | % (raw x 0.01) |
| thd_i1 / thd_i2 / thd_i3 | 30188 / 30189 / 30190 | 188 / 189 / 190 | T16 | % (raw x 0.01) |

32 points. All FC 04.

## Excluded - and why

- **Power factor (PFt, PF1-3)**: T7 packs TWO separate sign bytes - byte 0 import/export
  (00/FF), byte 1 inductive/capacitive (00/FF) - plus an unsigned magnitude (bits 15..0,
  x 1e-4; the doc's example decodes 0.9876 CAP). No single signed PF number is
  vendor-stated. The IEEE-754 mirror block lists a `Power Factor Total (PFt)` float and a
  separate `CAP/IND P. F.` float, but their sign semantics are not stated either. Never
  guess a sign convention for PF.
- **ALL energy counters**: the 7M User's Manual states the meter's energy registers "can
  be parameterised by the user regarding type of energy, active quadrants, direction of
  counting and tariff" - counter roles (import/export/tariff) are configuration, not
  fixed. On top of that, each T3 counter's scale lives in a SEPARATE exponent register
  (30401-30404 for counters n1-n4, 30446-30453 for counters 1-8) that can change at
  runtime, and the 1000x mirrors do not fix the roles. No honest fixed map exists.
- Demand/max values, harmonics beyond THD, run time, nominal values, and ALL
  setup/configuration registers (writable; the edge agent is read-only by design).

## Tested vs untested

Tested: register-evidence vitest asserts every on-wire address, type, the THD 0.01 scale,
the serial hint, no overlapping spans, and the FC 04 YAML (t5/t6 types carried through).
Full web suite and production build green. Picker pixel-verified on desktop and mobile.

NOT tested: real hardware. Compare one reading (for example frequency or one voltage)
against the meter's own display before trusting values.
