# Eastron SDM72D-M - Modbus register map provenance

Template `eastron-sdm72d-m` in `web/src/lib/edgeSetup.ts`. Never run on a real meter;
the edge agent only reads.

## Source (vendor domain only)

- Manufacturer: Zhejiang Eastron Electronic Co., Ltd. (eastrongroup.com). European arm's
  product page: https://www.eastroneurope.com/products/sdm72d-modbus
- Document: "Eastron SDM72D-M-1 User Manual V1.4" (2023), Part 3 "Modbus Protocol",
  pages 10-12:
  https://www.eastrongroup.com/eastrongroup/2024/08/21/eastronsdm72d-m-1usermanualv1.4.pdf

The newer SDM72D-M user manual V1.6 on eastroneurope.com is a 2-page datasheet without
the register table; V1.4 is the vendor document that contains it.

## What this meter measures

Per the manual's specification: active kWh and W, import and export, plus a resettable
partial energy. There are NO voltage, current or frequency registers in this model's map.
Do not expect them; the template carries only what the meter has.

## What is verified line-by-line against the V1.4 table

Input registers, each held in two consecutive 16-bit registers, data format Float:

| Point      | 3X address | Wire address (hex col) | Content                  | Unit |
|------------|-----------|------------------------|--------------------------|------|
| p1         | 30013     | 00 0C                  | Phase 1 active power     | W    |
| p2         | 30015     | 00 0E                  | Phase 2 active power     | W    |
| p3         | 30017     | 00 10                  | Phase 3 active power     | W    |
| p_total    | 30053     | 00 34                  | Total system power       | W    |
| kwh_import | 30073     | 00 48                  | Total Import kWh         | kWh  |
| kwh_export | 30075     | 00 4A                  | Total Export kWh         | kWh  |
| kwh_total  | 30343     | 01 56                  | Total kWh (import+export)| kWh  |
| p_import   | 31281     | 05 00                  | Total import active power| W    |
| p_export   | 31283     | 05 02                  | Total export active power| W    |

## Conventions

1. Read function: VENDOR-STATED - "Modbus Protocol function code 04 is used to access
   all parameters" (input registers). Template fixes func 4; no gate.
2. Register base: VENDOR-STATED - the table prints the "Modbus Protocol Start Address
   Hex" alongside each 3X number (e.g. 30053 -> 00 34). The template uses those wire
   addresses directly; no base gate.
3. Word order: NOT PINNED - the manual says "Hi/Lo byte order is set automatically to
   normal or reverse. It cannot be configured from the set-up menu." So the byte order
   genuinely varies per unit/firmware and no default can be trusted: the template gates
   on an explicit word-order choice (wordOrderUnverified). Choose it from your unit's
   documentation or by comparing one 32-bit reading (e.g. total power) against the
   meter display. Until that display comparison is done, every value from this template
   is hardware-untested.

## Notes and exclusions

- The manual limits each request to 30 parameters; this template reads 9.
- Holding registers (4xxx: parity, address, baud, password, etc.) are configuration;
  excluded. The platform never writes to the meter.
- Register 461457 (reset of resettable energy) is write-only; excluded.
- The manual's request example mentions "Amps 1" at start address 0006, a register its
  own table does not list for this model - a vendor doc inconsistency, noted and unused.
- Resettable energy registers (30385/30389/30391) excluded from the base map.

## Hardware status

Addresses, FC and units are vendor-documented as above, but the map has not been run
against a physical SDM72D-M. Confirm real readings against the meter display during the
pilot; until then every value from this template is hardware-untested.
