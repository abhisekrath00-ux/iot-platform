# SATEC PM130 PLUS - Modbus register map provenance

Template `satec-pm130` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- Protocol guide: "Series PM130 PLUS Powermeters PM130P/PM130E/PM130EH, Modbus
  Communications Protocol Reference Guide" BG0427 Rev. A10 (78 pages), from
  https://www.satec-global.com/wp-content/uploads/2025/09/PM130-PLUS-Modbus-Guide.pdf

No third-party copies were used.

## Conventions - all vendor-stated

1. Read function: the guide's function table (Section 2.3) lists FC 03 (read holding
   registers) and states "Function 04 can be used in the same context as function 03".
   The template uses FC 03.
2. Word order: Section 2.7.2 states 32-bit values are transmitted in two registers with
   "the first register contains the low-order word" (and 2.7.3 repeats "the low order
   register first" for the float option) - low-word-first (`cdab`).
3. Register base: Section 2.6 says the PM130 registers are numbered 0-65535 and the
   Modicon "4XXXX/4XXXXX" scheme is only an application-side notation ("add 40001 to the
   PM130 register address" to build the Modicon number). The listed address is the
   on-wire address; the template uses it verbatim.
4. 32-bit format: the guide's setup register 246 selects integer vs floating point for
   32-bit registers and defaults to 0 = 32-bit INTEGER. This template reads the integer
   format with the guide's printed scales. If a meter was switched to float, the decode
   is wrong - the provisional text requires checking register 246.
5. Units: Section 4 ties U1/U2/U3 to the device resolution option (setup offset +14,
   default 0 = Low resolution): Low = 1V, 1A, 1kW/kvar/kVA; High = 0.1V, 0.01A, and
   1W/1kW depending on PT ratio. The template labels the Low-resolution units and gates
   on the unitsUnverified checkbox - the installer must confirm the meter reports the
   labeled units.

## What is verified line-by-line against Section 3.4 (32-bit register set, 1-second values)

| Point      | Address | Description                       | Type  | Scale | Unit (low res) |
|------------|---------|-----------------------------------|-------|-------|----------------|
| v1n        | 13952   | V1/V12 Voltage (1-sec phase)      | UINT32| 1     | V              |
| v2n        | 13954   | V2/V23 Voltage                    | UINT32| 1     | V              |
| v3n        | 13956   | V3/V31 Voltage                    | UINT32| 1     | V              |
| i1         | 13958   | I1 Current                        | UINT32| 1     | A              |
| i2         | 13960   | I2 Current                        | UINT32| 1     | A              |
| i3         | 13962   | I3 Current                        | UINT32| 1     | A              |
| p1         | 13964   | kW L1                             | INT32 | 1     | kW             |
| p2         | 13966   | kW L2                             | INT32 | 1     | kW             |
| p3         | 13968   | kW L3                             | INT32 | 1     | kW             |
| v12        | 14012   | V12 Voltage (1-sec phase block)   | UINT32| 1     | V              |
| v23        | 14014   | V23 Voltage                       | UINT32| 1     | V              |
| v31        | 14016   | V31 Voltage                       | UINT32| 1     | V              |
| p_total    | 14336   | Total kW (1-sec totals)           | INT32 | 1     | kW             |
| q_total    | 14338   | Total kvar                        | INT32 | 1     | kvar           |
| s_total    | 14340   | Total kVA                         | UINT32| 1     | kVA            |
| pf_total   | 14342   | Total PF                          | INT32 | 0.001 | -              |
| v_avg_ln   | 14356   | 3-phase average L-N/L-L voltage   | UINT32| 1     | V              |
| v_avg_ll   | 14358   | 3-phase average L-L voltage       | UINT32| 1     | V              |
| i_avg      | 14360   | 3-phase average current           | UINT32| 1     | A              |
| freq       | 14468   | Frequency (auxiliary block)       | UINT32| 0.01  | Hz             |
| kwh_import | 14720   | kWh import (Total Energies)       | UINT32| 1     | kWh            |
| kwh_export | 14722   | kWh export                        | UINT32| 1     | kWh            |

## Wiring dependency (guide notation "V1/V12")

The first three phase-voltage registers carry phase-to-neutral voltage when the meter is
wired/programmed 4-wire (4LN3) and phase-to-phase voltage in 3-wire (4LL3) mode. The
explicit L-L set (14012-14016) is populated in 4-wire mode. The provisional text tells
the installer.

## Serial settings

COM1/COM2 protocol (SATEC ASCII / Modbus RTU / DNP3.0), address (1-247 for Modbus RTU),
baud (300-115200) and data format (7E1 / 8N1 / 8E1) are all setup registers configured
on the meter or via PAS. The guide states no factory default, so match the form to the
meter. The port's protocol must be Modbus RTU for this template.

## Exclusions

- The 16-bit scaled register sets (256-308, 6656-10935): raw 0-9999 values whose
  engineering conversion depends on the meter's configured voltage/current scales -
  redundant with the 32-bit set and harder to read honestly.
- 1-Cycle, Min/Max, demand, harmonic, TOU, phasor and summary-energy blocks: present in
  the map, not needed for the base template.
- kvarh import/export (14728/14730), kVAh (14736+), phase energies (14848+): named here;
  the platform's energy model uses kWh import/export.
- Per-phase kvar/kVA, per-phase PF, THD/K-factor/TDD rows in the 1-second block.
- All setup, control, relay and file/log registers: configuration and writes. The
  platform never writes to the meter.
- The 120 user-assignable remap registers (0-119) and their map registers (120-239).

This template is hardware-untested until an installer confirms the units, the integer
format setting and several display readings, per the provisional text.
