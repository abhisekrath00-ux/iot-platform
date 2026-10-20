# Eastron SDM72CT-M - Modbus register map provenance

Template `eastron-sdm72ct-m` in `web/src/lib/edgeSetup.ts`. Never run on a real meter;
the edge agent only reads.

## Source (vendor domain only)

- Document: "1_SDM72CT-M_Protocol_V1.1.pdf", PART 3 "Modbus Protocol", hosted by
  Eastron Europe (Zhejiang Eastron Electronic Co., Ltd. is the manufacturer):
  https://www.eastroneurope.com/images/uploads/products/protocol/1_SDM72CT-M_Protocol_V1.1.pdf

Note on extraction: this PDF has a broken font encoding (no usable text layer); the
register table was read from the rendered pages (OCR) and cross-checked two ways:
(1) every row's "Modbus Protocol Start Address Hex" column equals its 3X number minus
30001, with addresses stepping by 2, and (2) the shared parameters match Eastron's own
SDM630 protocol PDF row-for-row (30001 V1, 30007 I1, 30053 total W, 30071 Hz, 30073
import kWh, 30201 L-L volts, 30225 neutral current). All 38 template rows passed both
checks.

## What is verified against the V1.1 table

Input registers, each held in two consecutive 16-bit registers, data format Float.
The meter is CT-operated three-phase; unlike the SDM72D-M it has the full measurement
set.

| Point      | 3X address | Wire (hex col) | Content                        | Unit  |
|------------|-----------|----------------|--------------------------------|-------|
| v1n        | 30001     | 00 00          | Phase 1 line to neutral volts  | V     |
| v2n        | 30003     | 00 02          | Phase 2 line to neutral volts  | V     |
| v3n        | 30005     | 00 04          | Phase 3 line to neutral volts  | V     |
| i1         | 30007     | 00 06          | Phase 1 current                | A     |
| i2         | 30009     | 00 08          | Phase 2 current                | A     |
| i3         | 30011     | 00 0A          | Phase 3 current                | A     |
| p1         | 30013     | 00 0C          | Phase 1 active power           | W     |
| p2         | 30015     | 00 0E          | Phase 2 active power           | W     |
| p3         | 30017     | 00 10          | Phase 3 active power           | W     |
| s1         | 30019     | 00 12          | Phase 1 apparent power         | VA    |
| s2         | 30021     | 00 14          | Phase 2 apparent power         | VA    |
| s3         | 30023     | 00 16          | Phase 3 apparent power         | VA    |
| q1         | 30025     | 00 18          | Phase 1 reactive power         | VAr   |
| q2         | 30027     | 00 1A          | Phase 2 reactive power         | VAr   |
| q3         | 30029     | 00 1C          | Phase 3 reactive power         | VAr   |
| pf1        | 30031     | 00 1E          | Phase 1 power factor (note 1)  | -     |
| pf2        | 30033     | 00 20          | Phase 2 power factor (note 1)  | -     |
| pf3        | 30035     | 00 22          | Phase 3 power factor (note 1)  | -     |
| v_avg_ln   | 30043     | 00 2A          | Average line to neutral volts  | V     |
| i_avg      | 30047     | 00 2E          | Average line current           | A     |
| i_sum      | 30049     | 00 30          | Sum of line currents           | A     |
| p_total    | 30053     | 00 34          | Total system power             | W     |
| s_total    | 30057     | 00 38          | Total system volt amps         | VA    |
| q_total    | 30061     | 00 3C          | Total system VAr               | VAr   |
| pf_total   | 30063     | 00 3E          | Total system power factor (1)  | -     |
| freq       | 30071     | 00 46          | Frequency of supply voltages   | Hz    |
| kwh_import | 30073     | 00 48          | Import Wh since last reset     | kWh   |
| kwh_export | 30075     | 00 4A          | Export Wh since last reset     | kWh   |
| kvarh_import | 30077   | 00 4C          | Import VArh since last reset   | kVArh |
| kvarh_export | 30079   | 00 4E          | Export VArh since last reset   | kVArh |
| v12        | 30201     | 00 C8          | Line 1 to Line 2 volts         | V     |
| v23        | 30203     | 00 CA          | Line 2 to Line 3 volts         | V     |
| v31        | 30205     | 00 CC          | Line 3 to Line 1 volts         | V     |
| v_avg_ll   | 30207     | 00 CE          | Average line to line volts     | V     |
| i_neutral  | 30225     | 00 E0          | Neutral current                | A     |
| kwh_total  | 30343     | 01 56          | Total kWh (import+export)      | kWh   |
| kvarh_total| 30345     | 01 58          | Total kvarh (import+export)    | kVArh |

Note 1 (vendor): "The power factor has its sign adjusted to indicate the direction of
the current. Positive refers to forward current, negative refers to reverse current."
Note: kwh_import/kwh_export/kvarh_* at 30073-30079 are the RESETTABLE counters ("since
last reset"); 30343/30345 are the lifetime totals.

## Conventions

1. Read function: VENDOR-STATED - "Modbus Protocol function code 04 is used to access
   all parameters" (input registers). Template fixes func 4; no gate.
2. Register base: VENDOR-STATED - the table prints the "Modbus Protocol Start Address
   Hex" beside each 3X number (30053 -> 00 34). Template uses wire addresses directly;
   no base gate.
3. Word order: NOT STATED in this protocol document. The sibling SDM72D-M manual from
   the same manufacturer says the Hi/Lo byte order "is set automatically to normal or
   reverse" and cannot be configured - i.e. it varies per unit. No default is trusted:
   the template gates on an explicit word-order choice (wordOrderUnverified). Choose it
   from your unit's documentation or by comparing one 32-bit reading (for example
   frequency or total power) against the meter's own display. Until that display
   comparison is done, every value from this template is hardware-untested.

## Notes and exclusions

- The vendor limits each request to 30 parameters; the driver reads each point
  individually, so this does not apply.
- Holding registers (4xxx: parity, address, baud, password, CT ratio, etc.) are
  configuration; excluded. The platform never writes to the meter.
- The document's request example mentions "Amps 1" at start address 0006, consistent
  with the table (30007).

## Hardware status

Addresses, FC and units are vendor-documented as above, but the map has not been run
against a physical SDM72CT-M. Confirm real readings against the meter display during
the pilot; until then every value from this template is hardware-untested.
