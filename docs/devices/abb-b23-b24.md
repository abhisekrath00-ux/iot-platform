# ABB B23/B24 (three-phase DIN-rail meter, Modbus RTU on RS-485)

Evidence (vendor domain only):
- Manual: "B23/B24 User Manual" 2CMC485003M0201 Revision A (B23_B24_User_Manual.pdf),
  served from ABB's own library:
  https://library.e.abb.com/public/6d7783fdb45ace75c1257c3f00474022/B23_B24_User_Manual.pdf.pdf
  Chapter 9 "Communication with Modbus" (manual pages 79-104). Every table row below was
  verified by direct pixel inspection of the rendered vendor PDF pages - not OCR, not
  third-party mirrors.

## Vendor-stated conventions (chapter 9, all explicit)

- Reads: Function Code 3 (Read Holding Registers), chapter 9.1.1. (FC 16/06 are for writes;
  this template never writes.)
- Addressing: the mapping-table "Start Reg (Hex)" column "is expressed exactly as it is
  sent on the bus. That is, it should not be subtracted by 40 000 or decremented by 1, as
  is common for Modbus products." Listed hex register = on-wire address, verbatim.
- Word order: "For quantities that are represented as more than 1 register, the most
  significant byte is found in the high byte of the first (lowest) register." - ABCD,
  vendor-stated. No installer choice needed.
- Powers note (manual, instantaneous table): "Powers are sent out as 32 bit signed
  integers, expressed in W (or var/VA) with 2 decimals. This means that the maximum power
  possible to express is approximately +/-21 MW."
- Missing quantities: unused/missing registers answer with FFFF... (unsigned) or the
  highest expressible value (signed) - an out-of-range reading means the quantity does not
  exist on that meter variant, it is not a wiring fault.
- Serial: the RS485 port speaks "EQ-Bus and the Modbus protocol"; parameters are set in
  the meter's RS485 menu (chapter 4.1.7). No factory default is stated in the manual -
  match the edge setup form to the meter's settings.

## What IS verified (line-by-line against the Instantaneous values table, manual p.89-90)

| Point | Start reg (Hex) | Dec | Size | Res. | Unit | Data type |
| --- | --- | --- | --- | --- | --- | --- |
| v1n / v2n / v3n | 5B00 / 5B02 / 5B04 | 23296 / 23298 / 23300 | 2 | 0.1 | V | Unsigned |
| v12 | 5B06 | 23302 | 2 | 0.1 | V | Unsigned |
| v23 (manual: L3-L2) | 5B08 | 23304 | 2 | 0.1 | V | Unsigned |
| v31 (manual: L1-L3) | 5B0A | 23306 | 2 | 0.1 | V | Unsigned |
| i1 / i2 / i3 / i_n | 5B0C / 5B0E / 5B10 / 5B12 | 23308 / 23310 / 23312 / 23314 | 2 | 0.01 | A | Unsigned |
| p_total / p1 / p2 / p3 | 5B14 / 5B16 / 5B18 / 5B1A | 23316 / 23318 / 23320 / 23322 | 2 | 0.01 | W | Signed |
| q_total / q1 / q2 / q3 | 5B1C / 5B1E / 5B20 / 5B22 | 23324 / 23326 / 23328 / 23330 | 2 | 0.01 | var | Signed |
| s_total / s1 / s2 / s3 | 5B24 / 5B26 / 5B28 / 5B2A | 23332 / 23334 / 23336 / 23338 | 2 | 0.01 | VA | Signed |
| freq | 5B2C | 23340 | 1 | 0.01 | Hz | Unsigned |
| pf_total | 5B3A | 23354 | 1 | 0.001 | - | Signed, range -1.000..+1.000 |
| pf1 / pf2 / pf3 | 5B3B / 5B3C / 5B3D | 23355 / 23356 / 23357 | 1 | 0.001 | - | Signed, range -1.000..+1.000 |

Note the manual's line-line voltage order: L1-L2, L3-L2, L1-L3 (not L1-L2, L2-L3, L3-L1).
The template maps v23 to the L3-L2 register and v31 to the L1-L3 register; magnitudes are
identical, and this file is the naming record.

## Excluded (scope named, per evidence rules)

- ALL energy accumulators (total 5000+, tariffs 5170+, per-phase 5460+, resettable 552C+):
  4 registers each = 64-bit unsigned, and the edge driver has no u64 type. No 32-bit energy
  counter exists in this map. Use the meter's pulse output or read energy another way.
- Phase angles (5B2D-5B39), current quadrant (5B3E+), inputs/outputs (6300+, partly
  writable), event logs, alarms/tariff config (chapter 9.4-9.5): not needed for the base
  template; writes are never emitted.
- The "DMTME mapping" mentioned in the powers note (W without decimals for >21 MW): a
  different map, not covered.

## Hardware validation

Conventions are fully vendor-stated, so there is no installer-gated choice. Still, no
value from this template has been compared against a physical B23/B24: on first install,
compare SEVERAL independent known readings (for example frequency, one voltage and one
current) with the meter's own display before relying on the data. Hardware-untested.
