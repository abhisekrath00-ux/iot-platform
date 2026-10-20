# Accuenergy Acuvim II - Modbus register map provenance

Template `accuenergy-acuvim2` in `web/src/lib/edgeSetup.ts`. Never run on a real meter;
the edge agent only reads.

## Source (vendor domain only)

- Register map index: https://www.accuenergy.com/support/modbus-map/
- Document: "Acuvim-II-V3-Modbus-map-v1.10.xlsx", downloaded from that page:
  https://www.accuenergy.com/wp-content/uploads/Acuvim-II-V3-Modbus-map-v1.10.xlsx
  Sheets used: "Basic Real-time (FLOAT)" and "Energy".
- Companion manual (for the data-type conventions): "Acuvim II Series Power Meter User
  Manual" 1040E1303, section 6.3:
  https://www.accuenergy.com/wp-content/uploads/Acuvim-II-Power-Meter-User-Manual-1040E1303.pdf

## What is verified line-by-line against the workbook

Sheet "Basic Real-time (FLOAT)" (all Float, unit per sheet):

| Point    | Address | Symbol | Parameter                    | Unit |
|----------|---------|--------|------------------------------|------|
| freq     | 4000H   | F      | Frequency                    | Hz   |
| v1n      | 4002H   | Ua     | Phase A Line-Neutral Voltage | V    |
| v2n      | 4004H   | Ub     | Phase B Line-Neutral Voltage | V    |
| v3n      | 4006H   | Uc     | Phase C Line-Neutral Voltage | V    |
| v_avg_ln | 4008H   | Uavg   | Average Line-Neutral Voltage | V    |
| v12      | 400AH   | Uab    | Line Voltage AB              | V    |
| v23      | 400CH   | Ubc    | Line Voltage BC              | V    |
| v31      | 400EH   | Uca    | Line Voltage CA              | V    |
| v_avg_ll | 4010H   | Ulavg  | Average line voltage         | V    |
| i1       | 4012H   | Ia     | Phase A current              | A    |
| i2       | 4014H   | Ib     | Phase B Current              | A    |
| i3       | 4016H   | Ic     | Phase C Current              | A    |
| i_avg    | 4018H   | Iavg   | Average Phase Current        | A    |
| i_n      | 401AH   | In     | Neutral Current              | A    |
| p1       | 401CH   | Pa     | Phase A Power                | kW   |
| p2       | 401EH   | Pb     | Phase B Power                | kW   |
| p3       | 4020H   | Pc     | Phase C Power                | kW   |
| p_total  | 4022H   | Psum   | Total System Power           | kW   |
| q_total  | 402AH   | Qsum   | Total Reactive Power         | kvar |
| s_total  | 4032H   | Ssum   | Total Apparent Power         | kVA  |
| pf_total | 403AH   | Pfsum  | Total Power Factor           | -    |

Sheet "Energy":

| Point      | Address | Parameter     | Relationship (primary mode) | Data type | Unit |
|------------|---------|---------------|-----------------------------|-----------|------|
| kwh_import | 4048H   | Import Energy | Ep_Imp = Rx/10              | UINT32    | kWh  |

The energy point is therefore `u32` with scale 0.1 in the template (raw range
0..999999999 -> 0..99999999.9 kWh), NOT float.

## Conventions

1. Read function: VENDOR-STATED - both sheet headers say "03H Read". Template fixes
   func 3; no gate.
2. Register base: VENDOR-STATED - addresses are protocol addresses (4000H etc.); the
   manual's frame examples use the listed addresses directly in the address field.
   Template uses them verbatim; no base gate.
3. Word order: PARTIALLY STATED - manual section 6.3.1 states the dword (uint32) layout
   explicitly: "high word at the front and the low word at the end" (Rx = high*65536 +
   low), i.e. big-endian word order. The float entry in the same list says only that a
   float occupies two data addresses; it does not state the word order for floats.
   Recorded as strong vendor indication of abcd, but NOT adopted as proven: the template
   gates on an explicit word-order choice (wordOrderUnverified) covering its float
   points. The uint32 energy point is covered by the dword statement. Until a float
   reading is compared against the meter display, every value from this template is
   hardware-untested.

## Notes and exclusions

- Only primary-mode relationships are used; secondary mode applies PT/CT ratios and is
  a meter configuration matter, not a template one.
- Demand, THD, harmonics, TOU, data-log, IO-module and SunSpec sheets: out of scope for
  the base map.
- The workbook's "Parameter & Status Settings" sheet is configuration (R/W); excluded.
  The platform never writes to the meter.

## Hardware status

Addresses, FC, data types, units and the energy scaling are vendor-documented as above,
but the map has not been run against a physical Acuvim II. Confirm real readings against
the meter display during the pilot; until then every value from this template is
hardware-untested.
