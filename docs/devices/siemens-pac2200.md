# Siemens SENTRON 7KM PAC2200 (Modbus TCP; RTU via optional RS485 interface)

Evidence (vendor domain only):
- Manual: "PAC2200 measuring device manual" L1V30415167B-06, edition 12/2022, the official
  Siemens Industry Support entry 109746835:
  https://support.industry.siemens.com/cs/document/109746835/pac2200-measuring-device-manual?dti=0&lc=en-WW
  PDF (4,324,231 bytes) served from
  https://support.industry.siemens.com/cs/attachments/109746835/MAN_L1V30415167B-06_en_en-US.pdf?download=true
  (Siemens blocks datacenter IPs; the file was fetched through a real browser session).
  Every table row below was verified by direct pixel inspection of the rendered vendor PDF
  pages (Appendix A.1, manual pages 103-107) - not OCR, not third-party mirrors.

## Interfaces (manual, chapters 4.4.2 and appendix A.1.11)

Modbus TCP on the base unit's Ethernet. Modbus RTU "if RS485 port is available" - the
RS485 interface "is available as an option" (4.4.2), same offsets either way. RTU settings
are parameters on the device (appendix A.1.11): address 1-247, baud 4800/9600/19200/38400/
57600/115200, framing 8N2/8E1/8O1/8N1. No factory default is stated in the manual.

## Vendor-stated conventions

- Function codes: appendix A.1.3 - "You can use the Modbus function codes 0x03 and 0x04 on
  all the measured variables listed below." This template reads with FC 04 (the same
  family choice as the PAC3200/PAC4200 templates).
- Addressing: the table column is "Offset" and the access notes say "Please ensure the
  start offset of the register is correct when making read accesses" with exception 02's
  remedy "Check the offset and the number of registers" - the listed offset is the start
  address used on the wire, unmodified. (Same addressing family as PAC3200/PAC4200, whose
  Siemens frame examples put the listed offset on the wire verbatim.)
- Formats: Float = 2 registers, Double = 4 registers (energy), Unsigned long = 2 registers.

## What IS verified (line-by-line against Table A-3, "Available measured variables")

| Point | Offset (as listed) | Registers | Format | Unit | Manual description |
| --- | --- | --- | --- | --- | --- |
| v1n / v2n / v3n | 1 / 3 / 5 | 2 | Float | V | Voltage U_L1-N / U_L2-N / U_L3-N |
| v12 / v23 / v31 | 7 / 9 / 11 | 2 | Float | V | Voltage U_L1-L2 / U_L2-L3 / U_L3-L1 |
| i1 / i2 / i3 | 13 / 15 / 17 | 2 | Float | A | Current L1 / L2 / L3 |
| s1 / s2 / s3 | 19 / 21 / 23 | 2 | Float | VA | Apparent power L1 / L2 / L3 |
| p1 / p2 / p3 | 25 / 27 / 29 | 2 | Float | W | Active power L1 / L2 / L3 |
| q1 / q2 / q3 | 31 / 33 / 35 | 2 | Float | var | Reactive power L1 / L2 / L3 |
| pf1 / pf2 / pf3 | 37 / 39 / 41 | 2 | Float | - | Power factor L1 / L2 / L3 (documented range 0 ... 1) |
| freq | 55 | 2 | Float | Hz | Frequency (documented range 45 ... 65) |
| v_avg_ln / v_avg_ll | 57 / 59 | 2 | Float | V | Average voltage U_L-N / U_L-L |
| i_avg | 61 | 2 | Float | A | Average Current |
| s_total | 63 | 2 | Float | VA | Total apparent power |
| p_total | 65 | 2 | Float | W | Total active power |
| q_total | 67 | 2 | Float | var | Total reactive power |
| i_n | 71 | 2 | Float | A | Neutral Current |
| energy_import | 801 | 4 | Double | Wh | Active energy import tariff 1 (overflow 1.0e+12) |
| energy_export | 809 | 4 | Double | Wh | Active energy export tariff 1 (overflow 1.0e+12) |
| varh_import | 817 | 4 | Double | varh | Reactive energy import tariff 1 (overflow 1.0e+12) |

## Excluded (scope named, per evidence rules)

- Total power factor (offset 69): the manual prints its value range as "-" (unstated), so
  no sane validation range exists; the per-phase PFs (documented 0 ... 1) are included
  instead.
- Tariff-2 energy counters (805/813/821), per-phase energy (841+), apparent energy (833+):
  same Double block, left out to keep the base template lean; add them from the same table
  if an installation needs them.
- Demand/period aggregates (501-597), diagnostics/status (205-219), universal counter,
  profile memories, I&M data and ALL writable/command parameters (60000+): read-only
  template; writes are never emitted.

## What is NOT stated (installer chooses; no trusted default)

The manual nowhere states the byte/word order of its multi-register values (Float 2
registers, Double 4 registers), and no swap setting is documented. The template gates YAML
generation on an explicit word-order choice (same treatment as the PAC4200 template).
Validate against the real meter with SEVERAL independent known readings (for example
frequency, one voltage and one current) compared with the meter's own display - a single
plausible value is only a smoke check, it does not prove the word order and says nothing
about the other registers. The Double energy counters are 4 registers and equally affected.
Until that display comparison is done, every value from this template is
hardware-untested.
