# IME Conto D4-Pd (Legrand CE4DF3DTMID) - Modbus register map provenance

Template `ime-conto-d4` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- Communication protocol: `IDP000372_(IM-CE4DF3DTMID)` REV 1.01, served from Legrand's
  asset host: https://assets.legrand.com/pim/NP-FT-GT/IDP000372_(IM-CE4DF3DTMID).pdf
  (PDF rendering of the official protocol workbook for the Modbus variant CE4DF3DTMID).
- Product manual: technical data sheet `IDP000190EN_04` ("CONTO D4-Pd", codes
  CE4DF30PMID / CE4DF3DTMID / CE4DF3MTMID), from
  https://assets.legrand.com/pim/NP-FT-GT/IDP000190EN_04.pdf. IME is a Legrand brand;
  the manual points to imeitaly.com for the protocol tables.

IME is a Legrand-group brand and both documents are served from Legrand's own asset
domain. Third-party copies were not used.

## What is verified line-by-line against IDP000372 (input register table)

Every template point's register address (the protocol prints a 1-based "Register Number"
AND the "Register Address (Dec/Hex)" - the template stores the printed address, which is
the on-wire address), size, type, scale, unit and read function code:

| Point      | Addr (Dec) | Addr (Hex) | Description                          | Type   | Scale | Unit | FC |
|------------|------------|------------|--------------------------------------|--------|-------|------|----|
| i1         | 20480      | 5000       | Phase 1 Current Value (R)            | u32    | 1 mA  | mA   | 04 |
| i2         | 20482      | 5002       | Phase 2 Current Value (S)            | u32    | 1 mA  | mA   | 04 |
| i3         | 20484      | 5004       | Phase 3 Current Value (T)            | u32    | 1 mA  | mA   | 04 |
| v1n        | 20509      | 501D       | 1-N Voltage                          | u32    | 1 mV  | mV   | 04 |
| v2n        | 20511      | 501F       | 2-N Voltage                          | u32    | 1 mV  | mV   | 04 |
| v3n        | 20513      | 5021       | 3-N Voltage                          | u32    | 1 mV  | mV   | 04 |
| v12        | 20515      | 5023       | 1-2 Voltage                          | u32    | 1 mV  | mV   | 04 |
| v23        | 20517      | 5025       | 2-3 Voltage                          | u32    | 1 mV  | mV   | 04 |
| v31        | 20519      | 5027       | 3-1 Voltage                          | u32    | 1 mV  | mV   | 04 |
| freq       | 20537      | 5039       | Three-phase frequency                | u16    | 0.01  | Hz   | 04 |
| p_total    | 20538      | 503A       | Three-phase Active Power             | i32    | 0.01  | kW   | 04 |
| q_total    | 20540      | 503C       | Three-phase reactive power           | i32    | 0.01  | kvar | 04 |
| s_total    | 20544      | 5040       | Three-Phase Apparent Power           | i32    | 0.01  | kVA  | 04 |
| pf_total   | 20548      | 5044       | Three-phase Power Factor (PF)        | i16    | 0.001 | -    | 04 |
| p1         | 20551      | 5047       | Phase 1 Active Power                 | i32    | 0.01  | kW   | 04 |
| p2         | 20553      | 5049       | Phase 2 Active Power                 | i32    | 0.01  | kW   | 04 |
| p3         | 20555      | 504B       | Phase 3 Active Power                 | i32    | 0.01  | kW   | 04 |
| kwh_import | 20592      | 5070       | Positive Three-phase Active Energy   | u32    | 0.01  | kWh  | 04 |
| kwh_export | 20594      | 5072       | Negative Three-phase Active Energy   | u32    | 0.01  | kWh  | 04 |

Template units are A / V / kW / kvar / kVA / Hz / kWh via the stated scales
(0.001 for mA and mV, 0.01 for power and energy, 0.001 for PF, 0.01 for frequency).

## Conventions

1. Read function: FC 04 is printed in the "Read Function Code (Dec)" column of every
   input-register row. Vendor-stated.
2. Register base: the table prints both "Register Number" (1-based, e.g. 20481) and
   "Register Address (Dec/Hex)" (e.g. 20480 / 0x5000). The address column is the value
   that goes in the Modbus PDU address field; the template uses it verbatim. No offset
   choice is left to the installer.
3. Word order: the protocol's "Data Encoding" column says only "Big Endian (most
   significant byte first)" - byte order within a word. It does NOT state the order of
   the two words in a 32-bit value. -> The template forces an explicit installer
   word-order choice (wordOrderUnverified gate); no default is trusted.
4. Signedness (protocol notes 1-2): unsigned values fix MSB = 0; signed values carry the
   sign in the most significant bit of the (multi-word) value.
5. PF quadrant: pf_total is signed (note 2); the separate "Power Factor (PF) sector"
   register at address 20550 (0 = PF 1, 1 = inductive, 2 = capacitive) tells the load
   character. Not mapped; named here for installers.

## Serial settings (manual IDP000190EN_04)

Modbus RTU on a galvanically isolated RS-485 port. Addresses 1-255 programmable
(default 5). Speeds 4800 / 9600 / 19200 (default) / 38400. 8 data bits, parity
none / even (default) / odd, 1 stop bit. An internal 120-ohm terminator can be enabled
in the meter's SETUP menu (default off).

## Exclusions

- CE4DF30PMID (pulse output) and CE4DF3MTMID (MBus) variants: different or no Modbus
  interface. Only CE4DF3DTMID is covered.
- Per-phase reactive and apparent power (20557-20573), per-tariff energy counters
  (20596-20618), phase-shift angle registers (20725-20736), and the PF sector register
  (20550): present in the map, not needed for the base template.
- All holding registers (Modbus settings, transformation ratios, measurement system
  configuration, partial/resettable counters, run-hour meters): configuration and
  writable state. The platform never writes to the meter.
- The "Basic MAP" alternative protocol layout (holding register 0): the standard map
  is used.
- Discrete input 4096 (current active tariff): not a measurement point.

This template is hardware-untested until an installer compares several independent
readings with the meter's own display, per the provisional text.
