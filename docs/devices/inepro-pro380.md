# Inepro PRO380-Mod - Modbus register map provenance

Template `inepro-pro380` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- Product page: https://www.ineprometering.com/product/pro380-mod-electricity-meter
- Document: "PRO380 user manual V2.18-8.pdf", linked from that page and served from
  Inepro's site CDN:
  https://cdn.prod.website-files.com/6617f74190bf0a53bfd776e8/69f0b42e8df8da5157fb250d_PRO380%20user%20manual%20V2.18-8.pdf
  Register map: Appendix A3.2. Serial defaults: Appendix A3.1 (9600 baud, 8 data bits,
  even parity, 1 stop bit, address 1; baud/parity configurable on the meter).

Third-party copies of this map exist on reseller sites; none were used.

## What is verified line-by-line against A3.2

Every template point's register address, function code, register length (2), unit and
data type:

| Point       | Reg. address | Content                | FC | Data type   | Unit |
|-------------|--------------|------------------------|----|-------------|------|
| v1n         | 0x5002       | L1 Voltage             | 03 | Float ABCD  | V    |
| v2n         | 0x5004       | L2 Voltage             | 03 | Float ABCD  | V    |
| v3n         | 0x5006       | L3 Voltage             | 03 | Float ABCD  | V    |
| freq        | 0x5008       | Grid frequency         | 03 | Float ABCD  | Hz   |
| i1          | 0x500C       | L1 Current             | 03 | Float ABCD  | A    |
| i2          | 0x500E       | L2 Current             | 03 | Float ABCD  | A    |
| i3          | 0x5010       | L3 Current             | 03 | Float ABCD  | A    |
| p_total     | 0x5012       | Total active power     | 03 | Float ABCD  | kW   |
| p1          | 0x5014       | L1 Active power        | 03 | Float ABCD  | kW   |
| p2          | 0x5016       | L2 Active power        | 03 | Float ABCD  | kW   |
| p3          | 0x5018       | L3 Active power        | 03 | Float ABCD  | kW   |
| q_total     | 0x501A       | Total reactive power   | 03 | Float ABCD  | kvar |
| s_total     | 0x5022       | Total apparent power   | 03 | Float ABCD  | kVA  |
| pf_total    | 0x502A       | Power factor           | 03 | Float ABCD  | -    |
| kwh_total   | 0x6000       | Total active energy    | 03 | Float ABCD  | kWh  |
| kwh_import  | 0x600C       | Forward active energy  | 03 | Float ABCD  | kWh  |
| kwh_export  | 0x6018       | Reverse active energy  | 03 | Float ABCD  | kWh  |

## Conventions - all vendor-stated, no installer gate needed

1. Read function: FC 03 is printed per row of the map.
2. Word order: "Float ABCD" is printed per row (big-endian word order).
3. Register base: the manual's own Modbus frame example ("01 06 4003 000A (new ID: 10)")
   uses the listed address 4003 verbatim in the on-wire address field, so listed address
   = on-wire address.

## Exclusions

- 0x5000 "Voltage*" and 0x500A "Current*": the asterisk is never defined anywhere in the
  manual. Excluded rather than guessed.
- The 0x4xxx block (serial number, meter code, Modbus ID, baud, CT ratio, etc.) is
  configuration/status, not measurements. Excluded; the platform never writes to the meter.
- Tariff (0x6048), resettable day counter (0x6049) and all tariff/reset/energy-per-phase
  rows beyond the three totals above: not needed for the base map.
- The manual notes the old V1.14 register map still works; this template uses the current
  V2.18 map only.

## Hardware status

Addresses, FC, types and units are vendor-documented as above, but the map has not been
run against a physical PRO380. Confirm a few real readings against the meter display
during the pilot; until then every value from this template is hardware-untested.
