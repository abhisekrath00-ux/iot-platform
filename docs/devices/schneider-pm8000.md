# Schneider PowerLogic PM8000 series - Modbus register map provenance

Template `schneider-pm8000` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- Download page: https://www.se.com/uk/en/download/document/PM8000_Modbus_Map/
- Document: `PM8000_Modbus_Map.xlsx` (269 KB), served from
  https://download.se.com/files?p_Doc_Ref=PM8000_Modbus_Map - Schneider's official register
  list for the PM8000. The se.com FAQ FA244203 ("PM8000 ION7400 series meter register list")
  points ION7400 users at the same map.
- User manual: "PowerLogic PM8000 series - User manual" 7EN02-0336-12, from
  https://download.se.com/files?p_Doc_Ref=7EN02-0336 (Communications chapter).

Third-party copies of this map exist on reseller and aggregator sites; none were used.

## What is verified line-by-line against the workbook's Detailed Modbus Map

Every template point's register number, register count (2), data type (FLOAT32), unit and
permissions (R/O):

| Point      | Register | Quantity            | # regs | Data type | Unit |
|------------|----------|---------------------|--------|-----------|------|
| i1         | 3000     | Current A           | 2      | FLOAT32   | A    |
| i2         | 3002     | Current B           | 2      | FLOAT32   | A    |
| i3         | 3004     | Current C           | 2      | FLOAT32   | A    |
| i_avg      | 3010     | Current Avg         | 2      | FLOAT32   | A    |
| v12        | 3020     | Voltage A-B         | 2      | FLOAT32   | V    |
| v23        | 3022     | Voltage B-C         | 2      | FLOAT32   | V    |
| v31        | 3024     | Voltage C-A         | 2      | FLOAT32   | V    |
| v_avg_ll   | 3026     | Voltage L-L Avg     | 2      | FLOAT32   | V    |
| v1n        | 3028     | Voltage A-N         | 2      | FLOAT32   | V    |
| v2n        | 3030     | Voltage B-N         | 2      | FLOAT32   | V    |
| v3n        | 3032     | Voltage C-N         | 2      | FLOAT32   | V    |
| v_avg_ln   | 3036     | Voltage L-N Avg     | 2      | FLOAT32   | V    |
| p1         | 3054     | Active Power A      | 2      | FLOAT32   | W    |
| p2         | 3056     | Active Power B      | 2      | FLOAT32   | W    |
| p3         | 3058     | Active Power C      | 2      | FLOAT32   | W    |
| p_total    | 3060     | Active Power Total  | 2      | FLOAT32   | W    |
| q_total    | 3068     | Reactive Power Total| 2      | FLOAT32   | VAR  |
| s_total    | 3076     | Apparent Power Total| 2      | FLOAT32   | VA   |
| pf_total   | 3150     | Power Factor Total  | 2      | FLOAT32   | ---  |
| freq       | 3110     | Frequency           | 2      | FLOAT32   | Hz   |
| kwh_import | 2700     | kWh del             | 2      | FLOAT32   | kWh  |
| kwh_export | 2702     | kWh rec             | 2      | FLOAT32   | kWh  |

Note the power units: this meter reports W/VAR/VA, not kW/kvar/kVA like the PM53xx family.

## Conventions - all vendor-stated, no installer gate needed

1. Read function: the user manual's Modbus function-code table (Communications chapter)
   lists FC 3 "Read holding registers" as supported by the meter as client AND server.
   FC 4 "Read input registers" is client-only on this meter, so FC 03 is the read path.
2. Word order: the manual states "32-bit data (two words) is transmitted with the
   most-significant word first and the least-significant word second" - big-endian word
   order (`abcd`), matching FLOAT32 in the map.
3. Register base: the workbook's High-Level Modbus Map defines the generic-data address
   space as Start Address 0, End Address 65535, and the Detailed map's register numbers
   (2700, 3000, ...) sit directly in that space. Listed number = on-wire address, no
   offset.

## Serial settings

Protocol, baud rate (2400-115200), parity/stop bits (8N1/8N2/8E1/8E2/8O1/8O2) and unit ID
(1-247 for Modbus serial) are all configured on the meter (ION Setup, webpages or display).
The manual states no factory default, so match the form to your meter. The RS-485 port's
protocol must be set to Modbus RTU for the meter to act as a Modbus server.

## Cautions

- The workbook does not state the power-factor sign convention. `pf_total` therefore
  carries a wide -2..2 validation range that admits both plain and four-quadrant
  encodings. The map also exposes "Power Factor Lagging" (3172) and "Power Factor
  Leading" (3174) FLOAT32 registers that can disambiguate the sign if needed; they are
  not in the template.
- Per-phase reactive/apparent power (3062-3074) and neutral/ground current (3006/3008)
  exist in the map and follow the same conventions; only the totals are in the template
  to keep the base map small.
- kVARh (2708/2710) and kVAh (2716/2718) counters exist; excluded, the platform's energy
  model uses kWh import/export.
- The "HS" high-speed rows (21064+) update faster but duplicate the same quantities with
  the same types; the standard-rate rows are used.
- The 52001-59999 sections (external control, data recorders, enumerated and numeric
  registers) are configuration/logging, not fixed measurements. Excluded; the platform
  never writes to the meter.

This template is hardware-untested until an installer compares several independent
readings with the meter's own display, per the provisional text.
